package plugin

// The /proxy endpoint: the handler and its construction, the pre-upstream
// security pipeline (ServeHTTP), the reverse proxy that fetches the validated
// target, outbound request header hygiene and the mapping of upstream errors
// to denial statuses.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/wilsonwaters/webview/pkg/security"
)

// proxyPath is the resource path the proxy endpoint is registered under.
const proxyPath = "/proxy"

// proxyUserAgent is the conservative, non-browser User-Agent the proxy presents
// to upstreams. The proxy is a stateless, unauthenticated fetcher: it identifies
// itself honestly as the plugin rather than impersonating a real browser, and
// carries no version/build detail that could fingerprint the Grafana instance.
const proxyUserAgent = "grafana-webview-proxy"

// proxyAccept is the conservative Accept header sent upstream. It advertises a
// document-oriented preference (the proxy fetches framed top-level pages) without
// echoing whatever the viewer's browser negotiated, which could leak client
// details. A sane HTML-first default is sufficient for the top-level fetch.
const proxyAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// strippedRequestHeaders are the exact-match headers removed from every outgoing
// proxied request before it is forwarded upstream. Header.Del canonicalises the
// key, so these match case-insensitively. The goal is that the forwarded request
// reveals NOTHING about the Grafana instance or the viewer — it is a stateless,
// unauthenticated fetch. Each removal is justified inline; X-Grafana-* tokens are
// swept separately by prefix (see stripRequestHeaders) since they are open-ended.
var strippedRequestHeaders = []string{
	// Credentials / session — must never reach the upstream.
	"Cookie",              // viewer session cookies (incl. Grafana session)
	"Cookie2",             // legacy RFC 2965 cookie variant
	"Authorization",       // bearer/basic creds carried inbound
	"Proxy-Authorization", // proxy-layer creds

	// Identity / origin leakage — reveal the Grafana instance or the viewer.
	"X-Forwarded-For",   // viewer/Grafana client IP chain
	"X-Forwarded-Host",  // Grafana instance hostname
	"X-Forwarded-Proto", // Grafana instance scheme
	"X-Forwarded-Port",  // Grafana instance port
	"Forwarded",         // RFC 7239 consolidated forwarding info
	"X-Real-Ip",         // viewer/Grafana client IP
	"Referer",           // the proxy/Grafana page that triggered the fetch
	"Origin",            // the Grafana origin
	"Via",               // proxy chain identifying intermediaries

	// Edge/CDN-injected client-IP and mTLS identity — set by infrastructure in
	// front of Grafana; could leak the viewer's IP or client-cert identity.
	"X-Forwarded-Client-Cert",  // Envoy/Istio mTLS client-cert (XFCC)
	"True-Client-IP",           // Akamai / Cloudflare Enterprise client IP
	"Cf-Connecting-Ip",         // Cloudflare client IP
	"Fastly-Client-Ip",         // Fastly client IP
	"X-Client-Ip",              // generic client IP
	"X-Cluster-Client-Ip",      // cluster ingress client IP
	"X-Original-Forwarded-For", // pre-rewrite forwarding chain
	"X-Original-Host",          // pre-rewrite Grafana host
	"X-Original-Url",           // pre-rewrite request URL
}

// xGrafanaHeaderPrefix is swept (case-insensitively) off every outgoing request:
// Grafana injects auth/identity context headers under this prefix (e.g.
// X-Grafana-Id, X-Grafana-Org-Id, X-Grafana-Device-Id). Deleting the whole prefix
// — rather than an enumerated list — guarantees no current or future Grafana
// identity header leaks to an arbitrary upstream.
const xGrafanaHeaderPrefix = "x-grafana-"

// defaultInstanceID is the rate-limiter instance key used when the plugin
// request context carries no Grafana namespace. The per-instance tier is keyed
// on the namespace (one per org, see instanceIDFromContext); without one, all
// such requests share this single bucket, so the limiter never fails open.
const defaultInstanceID = "default"

// proxyHandler holds the per-instance state for the /proxy endpoint: the loaded
// settings, the rate limiter built from them, and the secure dialer-backed HTTP
// transport that every upstream fetch flows through. It is constructed once per
// App instance (settings are immutable for the life of an App) and is safe for
// concurrent use: RateLimiter is internally synchronised and the transport is
// the standard library's concurrency-safe *http.Transport.
type proxyHandler struct {
	cfg         PluginSettings
	allowlist   []security.AllowlistEntry
	rateLimiter *security.RateLimiter
	transport   http.RoundTripper

	// logger is the structured logger used to emit the per-request audit entry
	// (P5 / AC 28). It defaults to log.DefaultLogger in newProxyHandler and is
	// injectable so tests can capture the emitted msg + key/value fields.
	logger log.Logger

	// metrics holds the Prometheus collectors for the endpoint (P6 / AC 29).
	// newProxyHandler registers them against prometheus.DefaultRegisterer so they
	// surface on the SDK's /metrics endpoint; tests inject a fresh registry.
	metrics *proxyMetrics
}

// newProxyHandler builds a proxyHandler from the loaded plugin settings. The
// HTTP transport is wired to SF4's secure dialer so that DNS-resolve-then-dial,
// resolved-IP validation, and the connect-time rebind guard all run at connect
// time for every upstream request. The dialer's base *net.Dialer carries the
// connection timeout from settings; serveProxy bounds the whole request with
// the same RequestTimeoutSec.
func newProxyHandler(cfg PluginSettings) *proxyHandler {
	return newProxyHandlerWithRegistry(cfg, defaultProxyMetricsRegisterer())
}

// defaultProxyMetricsRegisterer returns the Prometheus registerer the production
// /proxy handler registers its collectors against. It is prometheus.DefaultRegisterer
// — whose default registry backs prometheus.DefaultGatherer, which the SDK's
// diagnostics adapter serves on the plugin's standard /metrics endpoint
// (see grafana-plugin-sdk-go backend/serve.go: the DiagnosticsServer is built
// with newDiagnosticsSDKAdapter(prometheus.DefaultGatherer, handler)).
//
// The collectors are built exactly ONCE via sync.Once and reused on every
// subsequent newProxyHandler call. A Grafana app instance constructs a single
// proxyHandler, but the unit-test suite constructs many through newProxyHandler;
// without the once-guard the second construction would panic on duplicate
// registration against the shared default registry. Tests that need to assert
// metric values in isolation call newProxyHandlerWithRegistry with a fresh
// prometheus.NewRegistry() instead.
func defaultProxyMetricsRegisterer() prometheus.Registerer {
	return prometheus.DefaultRegisterer
}

var (
	defaultProxyMetricsOnce sync.Once
	defaultProxyMetrics     *proxyMetrics
)

// newProxyHandlerWithRegistry builds a proxyHandler exactly like newProxyHandler
// but registers the Prometheus collectors against the supplied registerer. When
// reg is prometheus.DefaultRegisterer the once-built shared metrics are reused
// (so repeated construction never double-registers); any other registerer (a
// fresh test registry) gets its own freshly-built collectors for isolated
// assertions.
func newProxyHandlerWithRegistry(cfg PluginSettings, reg prometheus.Registerer) *proxyHandler {
	h := buildProxyHandler(cfg)
	if reg == prometheus.DefaultRegisterer {
		defaultProxyMetricsOnce.Do(func() {
			defaultProxyMetrics = newProxyMetrics(reg)
		})
		h.metrics = defaultProxyMetrics
	} else {
		h.metrics = newProxyMetrics(reg)
	}
	return h
}

// buildProxyHandler constructs the proxyHandler's non-metrics state.
func buildProxyHandler(cfg PluginSettings) *proxyHandler {
	timeout := time.Duration(cfg.RequestTimeoutSec) * time.Second

	secureDialer := security.NewDialer(nil, &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	})

	transport := &http.Transport{
		DialContext:       secureDialer.DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      100,
		IdleConnTimeout:   90 * time.Second,
		// DisableKeepAlives is SECURITY-LOAD-BEARING. Connection reuse is the
		// cross-policy connection-reuse hazard: a TCP connection validated under
		// one matched domain's per-request Policy (e.g. one that opted in to a
		// private IP via AllowPrivateIP) must NEVER be silently reused for a later
		// request whose matched domain did NOT opt in. The SF4 resolve-then-dial +
		// connect-time Control gate runs ONLY on a fresh dial; a pooled connection
		// skips it entirely. Disabling keep-alives forces a brand-new dial — and
		// therefore a fresh per-request resolve/Control gate under the CURRENT
		// request's policy — on every single upstream connection, so a connection
		// admitted under one domain's policy can never carry a request another
		// domain's policy would have refused.
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &proxyHandler{
		cfg:         cfg,
		allowlist:   toAllowlistEntries(cfg.AllowedDomains),
		rateLimiter: security.NewRateLimiter(cfg.RateLimitPerInstancePerMin, cfg.RateLimitPerDomainPerMin, cfg.MaxConcurrentRequests, domainRateOverrides(cfg.AllowedDomains)),
		transport:   transport,
		logger:      log.DefaultLogger,
	}
}

// instanceIDFromContext derives the per-instance rate-limit key from the plugin
// request context. We key on the Grafana tenant namespace so the per-instance
// tier is shared across all viewers of a tenant. When no plugin context /
// namespace is present (e.g. in unit tests calling ServeHTTP directly), we fall
// back to defaultInstanceID.
func instanceIDFromContext(req *http.Request) string {
	pCtx := backend.PluginConfigFromContext(req.Context())
	if pCtx.Namespace != "" {
		return "ns-" + pCtx.Namespace
	}
	return defaultInstanceID
}

// writeDenial writes a denial response for the given reason: it looks up the
// authoritative status for that reason (statusForReason / reasonStatus), emits
// it via http.Error, and returns the reason so the caller can assign it to the
// handler's `denied` token for the metrics emission. Deriving BOTH the client
// status and the metric reason from the single reasonStatus table here is what
// guarantees the two can never drift on a denial path. It is the sole writer for
// the in-handler (pre-upstream) denials in ServeHTTP; the post-upstream
// ErrorHandler denials go through proxyErrorHandler, which uses the same table.
func writeDenial(w http.ResponseWriter, reason, message string) string {
	http.Error(w, message, statusForReason(reason))
	return reason
}

// ServeHTTP implements the /proxy endpoint. It runs the FULL security pipeline
// BEFORE any upstream connection, because httputil's Director/Rewrite cannot
// return an error and must never be the place a denial is decided. Only once
// every gate passes does it build and invoke a ReverseProxy whose Transport is
// the SF4 secure dialer and whose ModifyResponse strips framing and dangerous
// response headers and HTML-rewrites the body for the panel's srcdoc iframe.
//
// Pipeline order: parse url param → SF2 ValidateURL (scheme/userinfo/malformed) →
// SF3 MatchHostname (empty allowlist ⇒ deny) → SF2 port re-check against the
// matched domain's AllowedPorts → CR5 hide-selector check → SF5 Allow (rate
// tiers) and Acquire (concurrency). On any denial the handler writes the mapped status and returns
// without contacting the upstream.
func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// P5 / AC 28 + P6 / AC 29: exactly one audit entry and one metrics
	// observation per request, emitted by the deferred endAudit on every path.
	a := p.beginAudit(w, endpointTopLevel)
	defer p.endAudit(req, a)
	w = a.rec

	// Same-origin only (R1 D2): no CORS headers, and a preflight OPTIONS is just
	// another non-GET method.

	if req.Method != http.MethodGet {
		a.denied = writeDenial(w, denialReasonMethod, "method not allowed")
		return
	}

	// The target URL arrives percent-encoded in the `url` query parameter.
	// req.URL.Query() decodes it for us.
	// Surrounding whitespace is trimmed once here, so every later step (and the
	// upstream request) sees the same string.
	rawTarget := strings.TrimSpace(req.URL.Query().Get("url"))
	if rawTarget == "" {
		a.denied = writeDenial(w, denialReasonBadRequest, "missing required 'url' query parameter")
		return
	}
	// We now have a caller-supplied target; log it even on the denial paths below.
	a.url = rawTarget

	// Derive the normalised hostname for the allowlist match BEFORE the full
	// SF2 validation. We must match the allowlist first to learn the matched
	// domain's extra allowed ports, then run the single authoritative
	// ValidateURL with those ports — otherwise a legitimate non-standard port
	// (declared per-domain) would be rejected before the allowlist is consulted.
	hostname, herr := hostnameOf(rawTarget)
	if herr != nil {
		a.url = auditInvalidURL
		// SF2 reasons (scheme/port/userinfo/hostname/malformed) are all client
		// errors ⇒ denialReasonScheme ⇒ 400 via the authoritative table.
		a.denied = writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(herr))
		return
	}

	// SF3: allowlist match on the normalised hostname. An empty/nil allowlist
	// denies everything (fail-closed default).
	match := security.MatchHostname(hostname, p.allowlist)
	if !match.Allowed {
		a.denied = writeDenial(w, denialReasonAllowlist, "target host is not allowlisted")
		return
	}
	// Capture the matched domain's per-request private-IP opt-in. This is the ONLY
	// place AllowPrivateIP enters the dial path: it is read from the domain THIS
	// request matched, so it is recomputed on every request (and, for redirects,
	// on every hop, since each hop re-enters ServeHTTP and re-matches). It is recorded
	// on the audit line and drives the security.Policy threaded below.
	a.allowPrivate = match.Options.AllowPrivateIP

	// SF2: full validation of scheme, userinfo, host, and port — now allowing
	// the matched domain's extra ports. scheme/port/userinfo/hostname/malformed
	// are all client errors and map to 400 via denialReasonScheme.
	validated, err := security.ValidateURL(rawTarget, match.Options.AllowedPorts)
	if err != nil {
		a.denied = writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(err))
		return
	}

	// CR5: read the author hide selectors from the repeated `hide` query
	// params and refuse the request (400, JSON) when there are too many, one
	// is too long, or one uses a selector feature whose matching cost is not
	// bounded (see hide_selectors.go). This runs before any rate-limit token
	// is spent or the upstream is contacted. The panel editor does not send
	// such selectors, so only a hand-made request gets here. The params are a
	// PROXY-request-only control and are never forwarded upstream (the
	// outbound URL is rebuilt from `url` alone).
	hideSelectors := hideSelectorsOf(req)
	if msg := checkHideSelectors(hideSelectors); msg != "" {
		a.denied = writeJSONDenial(w, denialReasonBadRequest, msg)
		return
	}

	// SF5: rate-limit tiers (per instance, per domain), then concurrency cap.
	// The domain tier is keyed on the matched allowlist entry, not the request
	// host, so every subdomain of an allowSubdomains entry shares one bucket
	// (and that entry's rateLimitPerMin), and the buckets stay bounded by the
	// allowlist size.
	instanceID := instanceIDFromContext(req)
	if allowed, reason := p.rateLimiter.Allow(instanceID, match.Domain); !allowed {
		a.denied = writeDenial(w, denialReasonRateLimit, "rate limit exceeded: "+reason)
		return
	}

	release, ok := p.rateLimiter.Acquire()
	if !ok {
		a.denied = writeDenial(w, denialReasonConcurrency, "too many concurrent proxy requests")
		return
	}
	defer release()

	// Every gate passed: build the upstream target URL from the validated,
	// normalised components and proxy the request. The path/query of the
	// ORIGINAL target (not the proxy request) must be carried through; we parse
	// rawTarget again only to lift its path/query/fragment, having already
	// validated its host/scheme/port above.
	target, perr := buildTargetURL(rawTarget, validated)
	if perr != nil {
		// Should not happen — rawTarget already parsed cleanly in ValidateURL —
		// but fail closed.
		a.denied = writeDenial(w, denialReasonBadRequest, "invalid target URL")
		return
	}
	// Log the validated, normalised target rather than the raw caller string.
	a.url = target.String()

	// CR4: read the current redirect-hop depth from the reserved control param.
	// A missing/blank/bogus value parses to 0 (treated as the first hop); we clamp
	// negatives to 0. This param is on the PROXY request only and is never carried
	// onto the upstream request (the outbound URL is rebuilt from `url` alone).
	depth := redirectDepthOf(req)

	// Build the per-request relaxation Policy from the matched domain's opt-in.
	// When allowPrivate is false this is the strict zero-equivalent Policy
	// (AllowPrivate:false), so the default path is unchanged. OnPrivatePermit is
	// the recorder so any admitted private IP is audited/metered in the deferred
	// block. serveProxy sets this Policy onto the request context (before its
	// timeout wrap) so the SF4 dialer reads it back via security.WithPolicy.
	pol := security.Policy{AllowPrivate: a.allowPrivate, OnPrivatePermit: a.permits.record}

	// The pipeline approved the request; any further denial is decided by the
	// ReverseProxy's ErrorHandler (a dial-time blocked IP/metadata host, an
	// over-size response, an upstream timeout, or a gateway failure). serveProxy
	// returns the denial-reason token for that outcome (empty when the upstream
	// was served cleanly) so the deferred metrics emission records it.
	a.denied = p.serveProxy(w, req, target, depth, hideSelectors, pol)
}

// hideSelectorsOf reads the repeated `hide` query params (CR5) from the inbound
// proxy request, returning each non-blank value (trimmed) in order. Each value is
// ONE CSS selector (selectors may contain commas/whitespace, so they are passed as
// repeated params, never comma-joined). Validation is checkHideSelectors' job
// (and the rewriter's, which skips what it cannot use); this only lifts the raw
// values. A request with no `hide` params yields nil (a no-op
// downstream). The param is internal to the proxy URL and never sent upstream.
func hideSelectorsOf(req *http.Request) []string {
	values := req.URL.Query()[hideParam]
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// hostnameOf extracts and normalises the hostname from rawTarget for the
// allowlist lookup, ahead of the full SF2 validation. It deliberately does NOT
// validate scheme or port (that is the subsequent ValidateURL call's job); it
// only needs a canonical hostname to match. It returns an error carrying an SF2
// reason token so the handler can map a malformed URL or missing/un-normalisable
// host to the right status. Normalisation uses the same SF2 NormalizeHostname
// the allowlist matcher applies, so both sides compare the identical canonical
// form.
func hostnameOf(rawTarget string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawTarget))
	if err != nil {
		return "", &security.ValidationError{Reason: security.ReasonMalformed, Message: "cannot parse URL"}
	}
	rawHost := u.Hostname()
	if rawHost == "" {
		return "", &security.ValidationError{Reason: security.ReasonHostname, Message: "URL has no host"}
	}
	return security.NormalizeHostname(rawHost)
}

// buildTargetURL reconstructs the upstream URL the ReverseProxy will fetch from
// the validated, normalised scheme/host/port plus the path/query/fragment of
// the caller-supplied raw URL. Using the validated components for the authority
// guarantees the upstream host matches exactly what the pipeline approved, while
// preserving the original path and query. rawTarget is trimmed, as ValidateURL
// trims it, and the path keeps its original encoding (RawPath), so an encoded
// "%2F" in a path segment reaches the upstream encoded.
func buildTargetURL(rawTarget string, v *security.ValidatedURL) (*url.URL, error) {
	orig, err := url.Parse(strings.TrimSpace(rawTarget))
	if err != nil {
		return nil, err
	}
	host := v.Hostname
	// Re-attach the port only when it is non-default for the scheme, so the
	// Host header stays clean for standard ports.
	if (v.Scheme == "http" && v.Port != 80) || (v.Scheme == "https" && v.Port != 443) {
		host = net.JoinHostPort(v.Hostname, strconv.Itoa(v.Port))
	}
	return &url.URL{
		Scheme:   v.Scheme,
		Host:     host,
		Path:     orig.Path,
		RawPath:  orig.RawPath,
		RawQuery: orig.RawQuery,
		Fragment: orig.Fragment,
	}, nil
}

// serveProxy assembles a single-use httputil.ReverseProxy for the validated
// target and serves the request through it. The Transport is the SF4
// secure-dialer-backed transport, ModifyResponse strips framing, CORS and other
// dangerous response headers, and ErrorHandler maps transport/upstream failures to clean
// status codes (403 for a blocked IP/metadata host caught at dial time, 502/504
// otherwise). After the size guard (P4) and the framing/response-header strip
// (P1/P3), prepareHTMLBody detects HTML, gzip-decodes when present, and
// HTML-rewrites the body (CR2, FR5). Non-HTML passes through.
//
// It returns the denial-reason token for a request the ErrorHandler rejected
// (size-limit / timeout / ip-blocklist / metadata / upstream) or the empty
// string when the upstream response was served cleanly, so the caller's deferred
// metrics emission can record denials_total for the right reason. The ErrorHandler
// runs synchronously inside rp.ServeHTTP, so capturing into a local here is
// race-free for this single-goroutine-per-request handler.
func (p *proxyHandler) serveProxy(w http.ResponseWriter, req *http.Request, target *url.URL, depth int, hideSelectors []string, pol security.Policy) string {
	maxBytes := p.cfg.MaxResponseBytes

	// P4 / Q10: ONE total per-request budget. RequestTimeoutSec is the whole
	// deadline — it wraps the entire proxy round-trip AND the body copy, so a
	// slow or stalled upstream body trips it too (not just connection setup,
	// which the dialer's own Timeout already sub-bounds). On expiry the
	// transport surfaces context.DeadlineExceeded, which proxyErrorHandler maps
	// to 504. The HTML rewrite step waits for its slot within the same deadline.
	//
	// The per-request relaxation Policy is attached to the BASE context FIRST,
	// then the timeout context is derived from it, so the Policy survives the wrap
	// and the SF4 dialer reads it back via security.WithPolicy at connect time.
	// With pol.AllowPrivate==false this is the strict default; the policy only
	// ever relaxes RFC 1918 (see security.isRelaxablePrivate).
	baseCtx := security.WithPolicy(req.Context(), pol)
	ctx, cancel := context.WithTimeout(baseCtx, time.Duration(p.cfg.RequestTimeoutSec)*time.Second)
	defer cancel()

	var denied string
	rp := &httputil.ReverseProxy{
		Transport: p.transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			// Director/Rewrite cannot return an error, so it performs NO
			// security decisions — every gate already ran in ServeHTTP. It only
			// rewrites the outbound request to point at the validated target.
			r.SetURL(&url.URL{Scheme: target.Scheme, Host: target.Host})
			r.Out.URL.Path = target.Path
			r.Out.URL.RawPath = target.RawPath
			r.Out.URL.RawQuery = target.RawQuery
			r.Out.Host = "" // use target Host (r.Out.URL.Host) for the Host header

			// P2: strip auth/identity headers from the OUTGOING request and set
			// conservative UA/Accept. Operates on r.Out (the forwarded request),
			// never r.In, so nothing about the Grafana instance or viewer leaks.
			stripRequestHeaders(r.Out.Header)
		},
		// P4: enforce the max-response-body size, then the P1/P3 framing/header
		// strip, then the body step. The size step runs FIRST so an
		// over-Content-Length response short-circuits with errResponseTooLarge
		// before any header rewriting; the framing/header strip (P1/P3) runs next
		// on the headers; finally the body is read in full (VR-6/VR-7) and an HTML
		// body is rewritten. ModifyResponse runs before the ReverseProxy sends the
		// status, so every size or decode failure becomes a clean error status.
		ModifyResponse: func(resp *http.Response) error {
			if err := enforceResponseSize(resp, maxBytes); err != nil {
				return err
			}
			if err := stripFramingHeaders(resp); err != nil {
				return err
			}
			// CR4: handle a 3xx redirect BEFORE any body step. On a followable
			// redirect this rewrites Location to a proxy URL (so the browser
			// re-enters the proxy for the next hop) or replaces the response with a
			// denial (depth cap exceeded, or a Location into a non-allowlisted host
			// blocked at the redirect step). A non-redirect status falls through
			// unchanged. handled==true means the redirect path fully decided the
			// response, so the body steps below must be skipped.
			handled, rerr := p.handleRedirect(resp, target, depth, hideSelectors)
			if rerr != nil {
				return rerr
			}
			if handled {
				// The browser follows the rewritten Location and never shows the
				// redirect body, so it is dropped rather than streamed unbounded.
				discardBody(resp)
				return nil
			}
			return prepareHTMLBody(ctx, resp, maxBytes, target, hideSelectors)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			denied = proxyErrorHandler(w, r, err)
			// The client gets only the reason; the error, which can name the
			// resolved IP addresses of the target, goes to the server log.
			p.logger.FromContext(r.Context()).Warn("proxy upstream error", "url", redactURL(target.String()), "reason", denied, "error", redactErrorURL(err))
		},
	}

	rp.ServeHTTP(w, req.WithContext(ctx))
	return denied
}

// stripRequestHeaders sanitises the OUTGOING proxied request: it deletes every
// credential/identity/origin header (exact matches in strippedRequestHeaders plus
// any X-Grafana-* header found by prefix sweep) and then sets a conservative,
// non-browser User-Agent and a sane Accept. The proxy forwards nothing that
// identifies the Grafana instance or the viewer — it is a stateless,
// unauthenticated fetch. Response-side stripping (Set-Cookie/HSTS/etc.) is P3.
func stripRequestHeaders(h http.Header) {
	for _, name := range strippedRequestHeaders {
		h.Del(name)
	}

	// Sweep all X-Grafana-* headers by prefix (case-insensitive).
	deleteHeadersWithPrefix(h, xGrafanaHeaderPrefix)

	// Set conservative outgoing headers AFTER stripping so inbound values cannot
	// survive: overwrite (Set) rather than add.
	h.Set("User-Agent", proxyUserAgent)
	h.Set("Accept", proxyAccept)

	// CR1: pin Accept-Encoding to gzip explicitly. This is deliberate and
	// security-relevant. If the outbound request carries NO Accept-Encoding,
	// net/http's Transport silently adds "gzip" and TRANSPARENTLY decompresses
	// the response itself — UNBOUNDED — before ModifyResponse ever runs, which
	// (a) bypasses our decompression-bomb guard entirely and (b) hides the
	// Content-Encoding so prepareHTMLBody could never see it. By setting the
	// header ourselves we disable that auto-decompression (net/http only
	// auto-decodes when it was the one that added the header), so the COMPRESSED
	// gzip body is handed to ModifyResponse and prepareHTMLBody performs the one
	// and only, size-bounded, decode. We advertise only gzip (the encoding CR1
	// handles); upstreams that ignore it and send identity are handled fine too.
	h.Set("Accept-Encoding", contentEncodingGzip)
}

// proxyErrorHandler maps an upstream/transport error to a clean status code and
// the matching P6 denial reason. It classifies the error into a reason token and
// then writes the status that reason maps to via the SINGLE authoritative
// reasonStatus table (writeDenial), exactly like the in-handler denials — so a
// post-upstream denial's status and metric reason can never drift either:
//
//   - SF4 blocked resolved/connect IP (*DialError, ReasonBlockedIP) ⇒ ip-blocklist ⇒ 403
//   - SF4 cloud-metadata host (ReasonMetadataHost) ⇒ metadata ⇒ 403
//   - SF4 resolve failure / no host (ReasonResolveFailed, ReasonNoHost) ⇒ upstream ⇒ 502
//   - over-size response (P4, VR-6, VR-7) ⇒ size-limit ⇒ 413
//   - hide selectors over their work budget (CR5) ⇒ size-limit ⇒ 413
//   - unsupported Content-Encoding (VR-6) ⇒ upstream ⇒ 502
//   - HTML that could not be rewritten (errRewriteFailed) ⇒ upstream ⇒ 502
//   - CR4 redirect depth cap reached / redirects disabled ⇒ redirect-loop ⇒ 502
//   - CR4 redirect into a non-allowlisted host (pre-block) ⇒ allowlist ⇒ 403
//   - per-request-budget timeout / net.Error timeout (P4, Q10) ⇒ timeout ⇒ 504
//   - everything else ⇒ upstream ⇒ 502
//
// It returns the reason so the handler can increment denials_total{reason} for
// the ErrorHandler-driven denial.
func proxyErrorHandler(w http.ResponseWriter, _ *http.Request, err error) string {
	switch security.DialReasonOf(err) {
	case security.ReasonBlockedIP:
		return writeDenial(w, denialReasonIPBlocklist, "target resolves to a blocked address")
	case security.ReasonMetadataHost:
		return writeDenial(w, denialReasonMetadata, "target is a blocked cloud-metadata endpoint")
	case security.ReasonResolveFailed, security.ReasonNoHost:
		return writeDenial(w, denialReasonUpstream, "target host could not be resolved")
	}
	// CR4: a redirect refused at the redirect step (in handleRedirect via
	// ModifyResponse) BEFORE any followable Location was emitted. The depth cap /
	// disabled-redirects case is a gateway-level failure ⇒ 502; a redirect into a
	// non-allowlisted host reuses the SF3 allowlist reason ⇒ 403.
	if errors.Is(err, errRedirectDepthExceeded) {
		return writeDenial(w, denialReasonRedirect, "too many redirects")
	}
	if errors.Is(err, errRedirectBlocked) {
		return writeDenial(w, denialReasonAllowlist, "redirect target host is not allowlisted")
	}
	// CR5: the hide selectors need more than their work budget on this page
	// ⇒ 413, with a message that names the cause.
	if errors.Is(err, errHideMatchBudget) {
		return writeDenial(w, denialReasonSizeLimit, "upstream page is too large for the hide selectors")
	}
	// P4 / VR-6 / VR-7: the declared, read, decoded or rewritten size exceeded
	// the configured limit (no body bytes sent yet) ⇒ 413.
	if errors.Is(err, errResponseTooLarge) {
		return writeDenial(w, denialReasonSizeLimit, "upstream response exceeds maximum allowed size")
	}
	// VR-6: a Content-Encoding whose decoded size the proxy cannot bound ⇒ 502.
	if errors.Is(err, errUnsupportedEncoding) {
		return writeDenial(w, denialReasonUpstream, "upstream response uses an unsupported content encoding")
	}
	// The HTML could not be rewritten (fail closed) ⇒ 502.
	if errors.Is(err, errRewriteFailed) {
		return writeDenial(w, denialReasonUpstream, "upstream page could not be processed")
	}
	// P4: the total per-request budget (Q10) expired ⇒ 504. The transport
	// surfaces the context deadline as context.DeadlineExceeded; a stalled
	// connection/handshake instead surfaces a net.Error with Timeout()==true.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return writeDenial(w, denialReasonTimeout, "upstream request timed out")
	}
	return writeDenial(w, denialReasonUpstream, "upstream request failed")
}

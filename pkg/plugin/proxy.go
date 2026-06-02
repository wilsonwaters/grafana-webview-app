package plugin

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
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
}

// xGrafanaHeaderPrefix is swept (case-insensitively) off every outgoing request:
// Grafana injects auth/identity context headers under this prefix (e.g.
// X-Grafana-Id, X-Grafana-Org-Id, X-Grafana-Device-Id). Deleting the whole prefix
// — rather than an enumerated list — guarantees no current or future Grafana
// identity header leaks to an arbitrary upstream.
const xGrafanaHeaderPrefix = "x-grafana-"

// defaultInstanceID is the rate-limiter instance key used when the plugin
// request context does not carry an org/user we can key on. P1 keys the
// per-instance rate-limit tier on the Grafana org ID; when that is absent we
// fall back to this constant so the limiter still applies a single shared
// bucket rather than failing open. Refining the instance identity (e.g. mixing
// in user) is left to later phases.
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
}

// newProxyHandler builds a proxyHandler from the loaded plugin settings. The
// HTTP transport is wired to SF4's secure dialer so that DNS-resolve-then-dial,
// resolved-IP validation, and the connect-time rebind guard all run at connect
// time for every upstream request. The dialer's base *net.Dialer carries the
// per-request connection timeout from settings (the trivial transport-timeout
// wiring permitted in P1; full body-size/total-timeout enforcement is P4).
func newProxyHandler(cfg PluginSettings) *proxyHandler {
	timeout := time.Duration(cfg.RequestTimeoutSec) * time.Second

	secureDialer := security.NewDialer(nil, &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	})

	transport := &http.Transport{
		DialContext:           secureDialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &proxyHandler{
		cfg:         cfg,
		allowlist:   toAllowlistEntries(cfg.AllowedDomains),
		rateLimiter: security.NewRateLimiter(cfg.RateLimitPerInstancePerMin, cfg.RateLimitPerDomainPerMin, cfg.MaxConcurrentRequests, domainRateOverrides(cfg.AllowedDomains)),
		transport:   transport,
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

// setCORSHeaders applies permissive CORS headers to every /proxy response. The
// proxy is an unauthenticated, stateless fetcher, so a wildcard origin is
// appropriate: it carries no credentials and sets no cookies.
func setCORSHeaders(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "*")
}

// ServeHTTP implements the /proxy endpoint. It runs the FULL security pipeline
// in the handler — BEFORE any upstream connection — because httputil's
// Director/Rewrite cannot return an error and must never be the place a denial
// is decided. Only once every gate passes does it build and invoke a
// ReverseProxy whose Transport is the SF4 secure dialer and whose ModifyResponse
// strips framing headers.
//
// Pipeline order: parse url param → SF2 ValidateURL (scheme/userinfo/malformed) →
// SF3 MatchHostname (empty allowlist ⇒ deny) → SF2 port re-check against the
// matched domain's AllowedPorts → SF5 Allow (rate tiers) and Acquire
// (concurrency). On any denial the handler writes the mapped status and returns
// without contacting the upstream.
func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	setCORSHeaders(w.Header())

	// CORS preflight: answer and return without running the pipeline.
	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The target URL arrives percent-encoded in the `url` query parameter.
	// req.URL.Query() decodes it for us.
	rawTarget := req.URL.Query().Get("url")
	if strings.TrimSpace(rawTarget) == "" {
		http.Error(w, "missing required 'url' query parameter", http.StatusBadRequest)
		return
	}

	// Derive the normalised hostname for the allowlist match BEFORE the full
	// SF2 validation. We must match the allowlist first to learn the matched
	// domain's extra allowed ports, then run the single authoritative
	// ValidateURL with those ports — otherwise a legitimate non-standard port
	// (declared per-domain) would be rejected before the allowlist is consulted.
	hostname, herr := hostnameOf(rawTarget)
	if herr != nil {
		http.Error(w, "invalid target URL: "+security.ReasonOf(herr), validationStatus(security.ReasonOf(herr)))
		return
	}

	// SF3: allowlist match on the normalised hostname. An empty/nil allowlist
	// denies everything (fail-closed default).
	match := security.MatchHostname(hostname, p.allowlist)
	if !match.Allowed {
		http.Error(w, "target host is not allowlisted", http.StatusForbidden)
		return
	}

	// SF2: full validation of scheme, userinfo, host, and port — now allowing
	// the matched domain's extra ports. scheme/port/userinfo/hostname/malformed
	// all map to 400 (P7 will formalise reason-specific handling and metrics).
	validated, err := security.ValidateURL(rawTarget, match.Options.AllowedPorts)
	if err != nil {
		http.Error(w, "invalid target URL: "+security.ReasonOf(err), validationStatus(security.ReasonOf(err)))
		return
	}

	// SF5: rate-limit tiers (per instance, per domain), then concurrency cap.
	instanceID := instanceIDFromContext(req)
	if allowed, reason := p.rateLimiter.Allow(instanceID, validated.Hostname); !allowed {
		http.Error(w, "rate limit exceeded: "+reason, http.StatusTooManyRequests)
		return
	}

	release, ok := p.rateLimiter.Acquire()
	if !ok {
		http.Error(w, "too many concurrent proxy requests", http.StatusTooManyRequests)
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
		http.Error(w, "invalid target URL", http.StatusBadRequest)
		return
	}

	p.serveProxy(w, req, target)
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

// validationStatus maps an SF2 validation reason token to an HTTP status. All
// current SF2 reasons (scheme, port, userinfo, hostname, malformed) are client
// errors ⇒ 400. Kept as a function so P7 can refine per-reason behaviour.
func validationStatus(_ string) int {
	return http.StatusBadRequest
}

// buildTargetURL reconstructs the upstream URL the ReverseProxy will fetch from
// the validated, normalised scheme/host/port plus the path/query/fragment of
// the caller-supplied raw URL. Using the validated components for the authority
// guarantees the upstream host matches exactly what the pipeline approved, while
// preserving the original path and query.
func buildTargetURL(rawTarget string, v *security.ValidatedURL) (*url.URL, error) {
	orig, err := url.Parse(rawTarget)
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
		RawQuery: orig.RawQuery,
		Fragment: orig.Fragment,
	}, nil
}

// serveProxy assembles a single-use httputil.ReverseProxy for the validated
// target and serves the request through it. The Transport is the SF4
// secure-dialer-backed transport, ModifyResponse strips framing headers and
// re-applies CORS, and ErrorHandler maps transport/upstream failures to clean
// status codes (403 for a blocked IP/metadata host caught at dial time, 502/504
// otherwise).
//
// TODO(CR): content-rewriting (CR2/CR3) will extend ModifyResponse with HTML
// body rewriting (base tag, subresource URL rewriting through a /proxy-resource
// endpoint, frame-buster removal) and redirect Location rewriting. The
// subresource URL scheme is intentionally NOT designed here (Q9: P1 only ships
// the top-level /proxy?url= fetch).
func (p *proxyHandler) serveProxy(w http.ResponseWriter, req *http.Request, target *url.URL) {
	rp := &httputil.ReverseProxy{
		Transport: p.transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			// Director/Rewrite cannot return an error, so it performs NO
			// security decisions — every gate already ran in ServeHTTP. It only
			// rewrites the outbound request to point at the validated target.
			r.SetURL(&url.URL{Scheme: target.Scheme, Host: target.Host})
			r.Out.URL.Path = target.Path
			r.Out.URL.RawQuery = target.RawQuery
			r.Out.Host = "" // use target Host (r.Out.URL.Host) for the Host header

			// P2: strip auth/identity headers from the OUTGOING request and set
			// conservative UA/Accept. Operates on r.Out (the forwarded request),
			// never r.In, so nothing about the Grafana instance or viewer leaks.
			stripRequestHeaders(r.Out.Header)
		},
		ModifyResponse: stripFramingHeaders,
		ErrorHandler:   proxyErrorHandler,
	}
	rp.ServeHTTP(w, req)
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

	// Sweep all X-Grafana-* headers by prefix (case-insensitive). Collect first,
	// then delete, to avoid mutating the map while ranging over its keys.
	var grafanaKeys []string
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), xGrafanaHeaderPrefix) {
			grafanaKeys = append(grafanaKeys, key)
		}
	}
	for _, key := range grafanaKeys {
		h.Del(key)
	}

	// Set conservative outgoing headers AFTER stripping so inbound values cannot
	// survive: overwrite (Set) rather than add.
	h.Set("User-Agent", proxyUserAgent)
	h.Set("Accept", proxyAccept)
}

// proxyErrorHandler maps an upstream/transport error to a clean status code. A
// dial-time denial from SF4 (blocked resolved IP, metadata host, resolve
// failure) surfaces here as a *security.DialError and maps to 403; everything
// else is an upstream/gateway failure (502), and a context deadline is 504.
func proxyErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	setCORSHeaders(w.Header())
	switch reason := security.DialReasonOf(err); reason {
	case security.ReasonBlockedIP, security.ReasonMetadataHost:
		http.Error(w, "target resolves to a blocked address", http.StatusForbidden)
		return
	case security.ReasonResolveFailed, security.ReasonNoHost:
		http.Error(w, "target host could not be resolved", http.StatusBadGateway)
		return
	}
	// Distinguish a timeout (504) from other upstream failures (502).
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		http.Error(w, "upstream request timed out", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "upstream request failed", http.StatusBadGateway)
}

// frameAncestorsDirective is the CSP directive that controls who may frame a
// page. Removing it (along with X-Frame-Options) is what makes a proxied,
// otherwise-frameable page embeddable in a Grafana panel.
const frameAncestorsDirective = "frame-ancestors"

// stripFramingHeaders removes the response headers that prevent the proxied page
// from being framed: it deletes X-Frame-Options outright and neutralises any
// frame-ancestors directive inside Content-Security-Policy while leaving all
// other CSP directives intact. It also re-applies CORS (ReverseProxy may have
// copied upstream CORS headers over ours). Body/base-tag/frame-buster rewriting
// is OUT OF SCOPE here — that is content-rewriting (see TODO(CR) above).
func stripFramingHeaders(resp *http.Response) error {
	// X-Frame-Options: delete entirely. There is no partial form to preserve.
	resp.Header.Del("X-Frame-Options")

	// Content-Security-Policy (and the report-only variant): drop only the
	// frame-ancestors directive, keeping every other directive. CSP may appear
	// as multiple header values; rewrite each.
	for _, headerName := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		values := resp.Header.Values(headerName)
		if len(values) == 0 {
			continue
		}
		rewritten := make([]string, 0, len(values))
		for _, v := range values {
			if nv := removeFrameAncestors(v); nv != "" {
				rewritten = append(rewritten, nv)
			}
		}
		resp.Header.Del(headerName)
		for _, v := range rewritten {
			resp.Header.Add(headerName, v)
		}
	}

	// Re-assert permissive CORS on the final response.
	setCORSHeaders(resp.Header)
	return nil
}

// removeFrameAncestors returns the CSP header value with any frame-ancestors
// directive removed, preserving the order and content of all other directives.
// Directives are separated by ';'. Matching on the directive name is
// case-insensitive (CSP directive names are case-insensitive).
func removeFrameAncestors(csp string) string {
	parts := strings.Split(csp, ";")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		// A directive is "name value...". Compare only the name token.
		name := trimmed
		if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
			name = trimmed[:i]
		}
		if strings.EqualFold(name, frameAncestorsDirective) {
			continue // drop this directive
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}

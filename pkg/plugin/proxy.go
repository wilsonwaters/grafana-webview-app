package plugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
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

// errResponseTooLarge is the sentinel ModifyResponse returns when an upstream
// response is KNOWN (via a non-negative Content-Length) to exceed
// MaxResponseBytes, BEFORE any body byte has been streamed to the client. The
// ReverseProxy has not yet written status/headers at that point, so the
// ErrorHandler can cleanly emit a 413. Matched with errors.Is, never by string.
var errResponseTooLarge = errors.New("proxy: response body exceeds maximum allowed size")

// errRedirectDepthExceeded is the sentinel ModifyResponse returns when a 3xx
// redirect would push the hop depth past cfg.MaxRedirects (CR4). It is raised
// BEFORE any followable Location is emitted so the browser never re-enters the
// proxy for the over-cap hop; proxyErrorHandler maps it to a clean 502 with the
// redirect-loop reason. Matched with errors.Is, never by string.
var errRedirectDepthExceeded = errors.New("proxy: redirect depth exceeds maximum")

// errRedirectBlocked is the sentinel ModifyResponse returns when a 3xx redirect's
// resolved Location host fails the allowlist pre-check (CR4 defense-in-depth). It
// blocks the denied hop at the redirect step rather than emitting a rewritten
// Location the browser would have to be refused on re-request; proxyErrorHandler
// maps it to a 403 with the allowlist reason. Matched with errors.Is, never by
// string.
var errRedirectBlocked = errors.New("proxy: redirect target host is not allowlisted")

// proxyPath is the resource path the proxy endpoint is registered under.
const proxyPath = "/proxy"

// wvRedirParam is the reserved, internal query parameter the proxy uses to carry
// the redirect-hop depth across browser-driven re-entries (CR4). It lives ONLY on
// the proxy URL (the /proxy?url=… or /proxy-resource?url=… link the browser
// follows), NEVER on the upstream request: the outbound request is rebuilt purely
// from the `url` target param (see serveProxy's Rewrite / buildTargetURL), so this
// control param is never forwarded upstream. serve reads it inbound to learn the
// current hop depth; ModifyResponse writes depth+1 onto the rewritten Location.
//
// CR4 deliberately does NOT follow redirects server-side. Instead a 3xx response
// has its Location rewritten to a proxy URL so the BROWSER re-enters the proxy for
// the next hop, where the FULL security pipeline (SF2 scheme/port, SF3 allowlist,
// SF5 rate/concurrency, SF4 resolve-then-dial IP blocklist) re-runs on that hop.
// The browser therefore only ever talks to the proxy, never the upstream directly,
// so a denied destination is refused (403) BEFORE the browser contacts it.
const wvRedirParam = "_wvredir"

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

// strippedResponseHeaders are the exact-match headers removed from every proxied
// response, in addition to the framing strip (X-Frame-Options / CSP
// frame-ancestors) P1 already performs. Header.Del canonicalises the key, so these
// match case-insensitively and remove ALL values of a multi-valued header (e.g.
// every Set-Cookie line). The goal is that the proxied page cannot set state on,
// or impose origin-level security policy against, the viewer's browser via the
// Grafana origin: the proxy is a stateless, unauthenticated fetch and the response
// is served from Grafana's own origin. Each removal is justified inline.
var strippedResponseHeaders = []string{
	// State-setting — must never write cookies into the viewer's Grafana origin.
	"Set-Cookie", // upstream session/tracking cookies (all values removed)

	// Origin-level security policy the upstream tries to pin onto the Grafana
	// origin — would persist beyond this single fetch and affect Grafana itself.
	"Strict-Transport-Security",   // HSTS: would force HTTPS-only on the Grafana origin
	"Public-Key-Pins",             // HPKP: would pin cert keys against the Grafana origin
	"Public-Key-Pins-Report-Only", // HPKP report-only sibling: same pinning policy, report mode
	"Clear-Site-Data",             // could wipe the viewer's cookies/storage/cache for the Grafana origin
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

// auditAnonymousUser is the user value logged when the proxy request carries no
// identifiable Grafana user in its plugin context (e.g. a backend-initiated
// request, or a unit test calling ServeHTTP without a plugin context). Audit
// entries always carry a user field; this constant keeps it non-empty.
const auditAnonymousUser = "anonymous"

// auditMissingURL / auditInvalidURL are the placeholder url values logged when
// the request never yielded a usable target: missing/blank url param, or a url
// param that could not even be parsed for its hostname. They keep the audit
// entry meaningful (and the field always present) on the earliest denial paths.
const (
	auditMissingURL = "<missing>"
	auditInvalidURL = "<invalid>"
)

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
// per-request connection timeout from settings (the trivial transport-timeout
// wiring permitted in P1; full body-size/total-timeout enforcement is P4).
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

// auditResponseWriter wraps the http.ResponseWriter for one /proxy request so
// the single audit-log emission point can record the final HTTP status and the
// number of body bytes written — accurately across the success path, the early
// denial helpers (http.Error), and the ReverseProxy/ErrorHandler writes. It is
// used by exactly one goroutine per request (the handler), so it needs no
// locking. WriteHeader is intercepted to capture the status (defaulting to 200
// if the wrapped writer streams a body without an explicit WriteHeader), and
// Write counts bytes while propagating the underlying writer's (n, err).
type auditResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newAuditResponseWriter(w http.ResponseWriter) *auditResponseWriter {
	return &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (a *auditResponseWriter) WriteHeader(status int) {
	if !a.wroteHeader {
		a.status = status
		a.wroteHeader = true
	}
	a.ResponseWriter.WriteHeader(status)
}

func (a *auditResponseWriter) Write(b []byte) (int, error) {
	// An implicit 200 happens on the first Write without a prior WriteHeader;
	// mark it so a later (illegal) WriteHeader does not overwrite the captured
	// status, mirroring net/http's own behaviour.
	a.wroteHeader = true
	n, err := a.ResponseWriter.Write(b)
	a.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer when it supports http.Flusher.
// httputil.ReverseProxy streams responses and flushes periodically; without
// this passthrough the wrapper would silently swallow those flushes. Guarded by
// a type assertion so wrapping a non-flushing writer (e.g. some test recorders)
// is a no-op rather than a panic.
func (a *auditResponseWriter) Flush() {
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// auditUser derives the source Grafana user for the audit entry from the plugin
// request context. The Grafana user is carried in the plugin context's User
// (a *backend.User); we log its Login when present. This is the server-side
// audit of WHO (which Grafana viewer) triggered the fetch — distinct from the
// outbound request, which P2 deliberately strips of all identity. When no user
// is identifiable we fall back to auditAnonymousUser so the field is never empty.
func auditUser(ctx context.Context) string {
	if u := backend.PluginConfigFromContext(ctx).User; u != nil && u.Login != "" {
		return u.Login
	}
	return auditAnonymousUser
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

// permittedIP records a single private IP that the matched domain's
// AllowPrivateIP opt-in relaxed (admitted) at the SF4 gate, alongside its SF1
// reason class. It feeds the distinct permit audit-log Warn and the
// webview_proxy_private_ip_permitted_total metric.
type permittedIP struct {
	ip     net.IP
	reason string
}

// privatePermitRecorder collects the private IPs that a request's per-domain
// AllowPrivateIP opt-in caused to be admitted at the SF4 IP gate. Its record
// method is wired into the per-request security.Policy.OnPrivatePermit hook,
// which security.ClassifyIPPolicy invokes from whatever goroutine runs the
// classification — at the connect-time Control gate that is the DIAL goroutine,
// NOT the handler goroutine — so record must be concurrency-safe. A mutex
// guards the slice; the handler reads the collected permits only AFTER the
// ReverseProxy round-trip has returned (the dial goroutine has finished), in
// the single deferred audit block.
type privatePermitRecorder struct {
	mu      sync.Mutex
	permits []permittedIP
}

// record notes a permitted (ip, reason), DEDUPLICATED per (ip, reason). The
// opt-in permit hook (security.Policy.OnPrivatePermit) fires for the SAME IP at
// both SF4 gates — once during ResolveAndValidatePolicy and again at the
// connect-time NewControlPolicy Control re-check — so a single permitted IP
// would otherwise be recorded twice. Deduping here means the audit Warn and the
// metric are emitted exactly once per distinct permitted IP. It copies the
// net.IP so a later mutation of the caller's slice cannot corrupt the recorded
// value, and is safe for concurrent use.
func (r *privatePermitRecorder) record(ip net.IP, reason string) {
	cp := make(net.IP, len(ip))
	copy(cp, ip)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.permits {
		if p.reason == reason && p.ip.Equal(cp) {
			return
		}
	}
	r.permits = append(r.permits, permittedIP{ip: cp, reason: reason})
}

// snapshot returns a copy of the recorded permits for the deferred audit block.
// It is read after the dial goroutine has completed, but takes the lock anyway
// so it is correct regardless of timing.
func (r *privatePermitRecorder) snapshot() []permittedIP {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]permittedIP, len(r.permits))
	copy(out, r.permits)
	return out
}

// setCORSHeaders applies permissive CORS headers to every /proxy response. The
// proxy is an unauthenticated, stateless fetcher, so a wildcard origin is
// appropriate: it carries no credentials and sets no cookies.
func setCORSHeaders(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "*")
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

// endpointTopLevel / endpointResource name the two endpoints that share the
// security pipeline. The value is recorded on the audit log's `endpoint` field
// so the two streams are distinguishable even though they share the metrics
// collectors (sharing is acceptable — see newProxyMetrics). endpointTopLevel is
// the HTML-rewriting /proxy endpoint; endpointResource is the non-rewriting,
// Content-Type-preserving /proxy-resource subresource endpoint (CR3).
const (
	endpointTopLevel = "proxy"
	endpointResource = "proxy-resource"
)

// proxyResourceHandler adapts a proxyHandler to serve the /proxy-resource
// subresource endpoint (CR3). It carries NO state of its own: it shares the SAME
// pipeline, transport, rate limiter, audit logger and metrics as the owning
// proxyHandler, and differs ONLY in the ModifyResponse step — no HTML rewriting,
// so the upstream Content-Type and body (including a gzip Content-Encoding) are
// passed through unchanged, subject to the P4 size limit. a.proxyResource is
// constructed once alongside a.proxy and registered at proxyResourcePath.
type proxyResourceHandler struct {
	p *proxyHandler
}

// ServeHTTP implements the /proxy-resource endpoint. It runs the IDENTICAL
// security pipeline + header policy as /proxy via the shared serve method,
// selecting the resource ModifyResponse (framing + response-header strip only —
// NO HTML rewrite; Content-Type and body streamed through unchanged).
func (h proxyResourceHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.p.serve(w, req, endpointResource)
}

// ServeHTTP implements the /proxy endpoint. It delegates to the shared serve
// method, which runs the full security pipeline; the endpoint argument selects
// the HTML-rewriting ModifyResponse for /proxy.
func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.serve(w, req, endpointTopLevel)
}

// serve runs the FULL security pipeline shared by /proxy and /proxy-resource —
// BEFORE any upstream connection — because httputil's Director/Rewrite cannot
// return an error and must never be the place a denial is decided. Only once
// every gate passes does it build and invoke a ReverseProxy whose Transport is
// the SF4 secure dialer and whose ModifyResponse is selected by `endpoint`:
// /proxy strips framing headers and HTML-rewrites the body; /proxy-resource
// strips framing/response headers only and streams the body (and Content-Type)
// through unchanged.
//
// Pipeline order: parse url param → SF2 ValidateURL (scheme/userinfo/malformed) →
// SF3 MatchHostname (empty allowlist ⇒ deny) → SF2 port re-check against the
// matched domain's AllowedPorts → SF5 Allow (rate tiers) and Acquire
// (concurrency). On any denial the handler writes the mapped status and returns
// without contacting the upstream.
//
// Both endpoints share the audit log (P5) and metrics (P6); the `endpoint`
// value distinguishes the two streams on the audit entry's `endpoint` field.
func (p *proxyHandler) serve(w http.ResponseWriter, req *http.Request, endpoint string) {
	// P5 / AC 28: emit EXACTLY ONE structured audit entry per request, covering
	// the success path AND every early-return denial below. The wrapper records
	// the final status + body bytes; start/auditURL/auditUser feed the remaining
	// fields. A single deferred Info call is the sole emission point — no
	// per-branch logging — so it cannot be forgotten on a new denial path.
	start := time.Now()
	rec := newAuditResponseWriter(w)
	w = rec
	// auditURL holds the best-known target for the log line. It starts as the raw
	// `url` query value (or a placeholder when missing/unparseable) and is
	// upgraded to the validated, normalised target once the pipeline approves it.
	auditURL := auditMissingURL

	// P6 / AC 29: count this request as in-flight for the whole handler, and emit
	// the per-request metrics once in the SAME single deferred block as the audit
	// log. denied carries the denial-reason token a branch sets on its way out
	// (empty for the success/upstream-served path); observeRequest records the
	// requests_total{status} counter + duration histogram always, and the
	// denials_total{reason} counter only when denied is non-empty.
	p.metrics.inFlight.Inc()
	var denied string
	// allowPrivate is the matched domain's AllowPrivateIP opt-in for THIS request
	// (false until the allowlist matches). recorder collects any private IP the
	// opt-in admitted at the SF4 gate so the deferred block can emit the distinct
	// permit Warn + metric. Both are read in the deferred block AFTER the upstream
	// round-trip has returned (so the dial goroutine has finished writing).
	var allowPrivate bool
	recorder := &privatePermitRecorder{}
	defer func() {
		p.metrics.inFlight.Dec()
		p.metrics.observeRequest(rec.status, time.Since(start).Seconds(), denied)
		p.logger.Info("proxy request",
			"endpoint", endpoint,
			"url", auditURL,
			"user", auditUser(req.Context()),
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
			"allowPrivateIP", allowPrivate,
		)
		// A DISTINCT, high-signal audit line per private IP that the opt-in
		// admitted: this re-opened SSRF surface must be loud in the logs. It fires
		// ONLY when the matched domain opted in AND a private IP was actually
		// reached, so an opted-in domain resolving to a public IP produces nothing
		// here. The permit metric is incremented in the same place.
		for _, permit := range recorder.snapshot() {
			p.metrics.privateIPPermitted.WithLabelValues(permit.reason).Inc()
			p.logger.Warn("proxy private-ip permitted by opt-in",
				"endpoint", endpoint,
				"url", auditURL,
				"user", auditUser(req.Context()),
				"permittedIP", permit.ip.String(),
				"ipClass", permit.reason,
			)
		}
	}()

	setCORSHeaders(w.Header())

	// CORS preflight: answer and return without running the pipeline.
	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if req.Method != http.MethodGet {
		denied = writeDenial(w, denialReasonMethod, "method not allowed")
		return
	}

	// The target URL arrives percent-encoded in the `url` query parameter.
	// req.URL.Query() decodes it for us.
	rawTarget := req.URL.Query().Get("url")
	if strings.TrimSpace(rawTarget) == "" {
		denied = writeDenial(w, denialReasonBadRequest, "missing required 'url' query parameter")
		return
	}
	// We now have a caller-supplied target; log it even on the denial paths below.
	auditURL = rawTarget

	// Derive the normalised hostname for the allowlist match BEFORE the full
	// SF2 validation. We must match the allowlist first to learn the matched
	// domain's extra allowed ports, then run the single authoritative
	// ValidateURL with those ports — otherwise a legitimate non-standard port
	// (declared per-domain) would be rejected before the allowlist is consulted.
	hostname, herr := hostnameOf(rawTarget)
	if herr != nil {
		auditURL = auditInvalidURL
		// SF2 reasons (scheme/port/userinfo/hostname/malformed) are all client
		// errors ⇒ denialReasonScheme ⇒ 400 via the authoritative table.
		denied = writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(herr))
		return
	}

	// SF3: allowlist match on the normalised hostname. An empty/nil allowlist
	// denies everything (fail-closed default).
	match := security.MatchHostname(hostname, p.allowlist)
	if !match.Allowed {
		denied = writeDenial(w, denialReasonAllowlist, "target host is not allowlisted")
		return
	}
	// Capture the matched domain's per-request private-IP opt-in. This is the ONLY
	// place AllowPrivateIP enters the dial path: it is read from the domain THIS
	// request matched, so it is recomputed on every request (and, for redirects,
	// on every hop, since each hop re-enters serve and re-matches). It is recorded
	// on the audit line above and drives the security.Policy threaded below.
	allowPrivate = match.Options.AllowPrivateIP

	// SF2: full validation of scheme, userinfo, host, and port — now allowing
	// the matched domain's extra ports. scheme/port/userinfo/hostname/malformed
	// are all client errors and map to 400 via denialReasonScheme.
	validated, err := security.ValidateURL(rawTarget, match.Options.AllowedPorts)
	if err != nil {
		denied = writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(err))
		return
	}

	// SF5: rate-limit tiers (per instance, per domain), then concurrency cap.
	instanceID := instanceIDFromContext(req)
	if allowed, reason := p.rateLimiter.Allow(instanceID, validated.Hostname); !allowed {
		denied = writeDenial(w, denialReasonRateLimit, "rate limit exceeded: "+reason)
		return
	}

	release, ok := p.rateLimiter.Acquire()
	if !ok {
		denied = writeDenial(w, denialReasonConcurrency, "too many concurrent proxy requests")
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
		denied = writeDenial(w, denialReasonBadRequest, "invalid target URL")
		return
	}
	// Log the validated, normalised target rather than the raw caller string.
	auditURL = target.String()

	// CR4: read the current redirect-hop depth from the reserved control param.
	// A missing/blank/bogus value parses to 0 (treated as the first hop); we clamp
	// negatives to 0. This param is on the PROXY request only and is never carried
	// onto the upstream request (the outbound URL is rebuilt from `url` alone).
	depth := redirectDepthOf(req)

	// CR5: read the author hide-selectors from the repeated `hide` query params.
	// These are applied ONLY to top-level proxied HTML (in prepareHTMLBody via
	// rewriteHTML); they are a PROXY-request-only control and are never forwarded
	// upstream (the outbound URL is rebuilt from `url` alone). For /proxy-resource
	// the values are read but unused — subresources are not HTML documents.
	hideSelectors := hideSelectorsOf(req)

	// Build the per-request relaxation Policy from the matched domain's opt-in.
	// When allowPrivate is false this is the strict zero-equivalent Policy
	// (AllowPrivate:false), so the default path is unchanged. OnPrivatePermit is
	// the recorder so any admitted private IP is audited/metered in the deferred
	// block. serveProxy sets this Policy onto the request context (before its
	// timeout wrap) so the SF4 dialer reads it back via security.WithPolicy.
	pol := security.Policy{AllowPrivate: allowPrivate, OnPrivatePermit: recorder.record}

	// The pipeline approved the request; any further denial is decided by the
	// ReverseProxy's ErrorHandler (a dial-time blocked IP/metadata host, an
	// over-size response, an upstream timeout, or a gateway failure). serveProxy
	// returns the denial-reason token for that outcome (empty when the upstream
	// was served cleanly) so the deferred metrics emission records it. The
	// endpoint selects the ModifyResponse: /proxy HTML-rewrites the body,
	// /proxy-resource passes the body + Content-Type through unchanged.
	denied = p.serveProxy(w, req, target, endpoint, depth, hideSelectors, pol)
}

// hideSelectorsOf reads the repeated `hide` query params (CR5) from the inbound
// proxy request, returning each non-blank value (trimmed) in order. Each value is
// ONE CSS selector (selectors may contain commas/whitespace, so they are passed as
// repeated params, never comma-joined). Validation (length / count caps and
// cascadia compilation) is the rewriter's job in applyHideSelectors; this only
// lifts the raw values. A request with no `hide` params yields nil (a no-op
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

// redirectDepthOf reads the reserved redirect-hop depth control param from the
// inbound proxy request. A missing, blank, non-numeric, or negative value yields
// 0 (the first hop) — a caller-supplied bogus or oversized value cannot break the
// pipeline: it simply parses to 0 or a large int that immediately trips the depth
// cap. The param is internal and never forwarded upstream.
func redirectDepthOf(req *http.Request) int {
	raw := strings.TrimSpace(req.URL.Query().Get(wvRedirParam))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
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
// The endpoint argument selects the ModifyResponse body handling AFTER the
// shared size guard (P4) and framing/response-header strip (P1/P3):
//   - endpointTopLevel (/proxy): prepareHTMLBody detects HTML, gzip-decodes when
//     present, and HTML-rewrites the body (CR2). Non-HTML passes through.
//   - endpointResource (/proxy-resource): NO rewriting. The upstream Content-Type
//     is PRESERVED and the body (including a gzip Content-Encoding) streams through
//     unchanged so the browser interprets CSS/JS/images and decompresses gzip
//     itself. CR3 deliberately does NOT decode subresources — there is nothing to
//     rewrite, so the compressed bytes pass straight through.
//
// It returns the denial-reason token for a request the ErrorHandler rejected
// (size-limit / timeout / ip-blocklist / metadata / upstream) or the empty
// string when the upstream response was served cleanly, so the caller's deferred
// metrics emission can record denials_total for the right reason. The ErrorHandler
// runs synchronously inside rp.ServeHTTP, so capturing into a local here is
// race-free for this single-goroutine-per-request handler.
func (p *proxyHandler) serveProxy(w http.ResponseWriter, req *http.Request, target *url.URL, endpoint string, depth int, hideSelectors []string, pol security.Policy) string {
	maxBytes := p.cfg.MaxResponseBytes

	var denied string
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
		// P4: enforce the max-response-body size, then the P1/P3 framing/header
		// strip, then the endpoint-specific body step. The size step runs FIRST
		// so an over-Content-Length response short-circuits with errResponseTooLarge
		// before any header rewriting; the framing/header strip (P1/P3) runs next on
		// the headers (identical for both endpoints); finally the body is either
		// HTML-rewritten (/proxy) or left untouched (/proxy-resource — Content-Type
		// and body, incl. gzip, streamed through unchanged).
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
			if handled, rerr := p.handleRedirect(resp, target, endpoint, depth); handled || rerr != nil {
				return rerr
			}
			if endpoint == endpointResource {
				// CR3: subresource passthrough. No HTML rewrite, no gzip decode —
				// the upstream Content-Type and (possibly gzip) body are preserved
				// and streamed through, bounded only by the P4 limited body that
				// enforceResponseSize already wrapped around resp.Body.
				return nil
			}
			return prepareHTMLBody(resp, maxBytes, target, p.logger, hideSelectors)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			denied = proxyErrorHandler(w, r, err)
		},
	}

	// P4 / Q10: ONE total per-request budget. RequestTimeoutSec is the whole
	// deadline — it wraps the entire proxy round-trip AND the body copy, so a
	// slow or stalled upstream body trips it too (not just connection setup,
	// which the dialer's own Timeout already sub-bounds). On expiry the
	// transport surfaces context.DeadlineExceeded, which proxyErrorHandler maps
	// to 504.
	//
	// The per-request relaxation Policy is attached to the BASE context FIRST,
	// then the timeout context is derived from it, so the Policy survives the wrap
	// and the SF4 dialer reads it back via security.WithPolicy at connect time.
	// With pol.AllowPrivate==false this is the strict default; the policy only
	// ever relaxes RFC 1918 (see security.isRelaxablePrivate).
	baseCtx := security.WithPolicy(req.Context(), pol)
	ctx, cancel := context.WithTimeout(baseCtx, time.Duration(p.cfg.RequestTimeoutSec)*time.Second)
	defer cancel()

	rp.ServeHTTP(w, req.WithContext(ctx))
	return denied
}

// enforceResponseSize applies the MaxResponseBytes cap to an upstream response.
//
// Clean path (declared length): if the upstream sent a non-negative
// Content-Length that already exceeds the limit, return errResponseTooLarge.
// ModifyResponse runs before the ReverseProxy writes any status/header, so the
// ErrorHandler can still emit a clean 413 with no bytes leaked.
//
// Defense-in-depth (chunked / missing / lying Content-Length, i.e. -1 or a
// value that under-reports the real body): wrap resp.Body in a reader that
// caps reads at maxBytes. CAVEAT: once the body starts streaming the
// ReverseProxy has ALREADY written 200 + headers, so this path CANNOT retro-
// actively become a 413 — it can only stop reading at the limit and surface an
// error, truncating/failing the copy. That is acceptable belt-and-braces; the
// guaranteed clean 413 is the Content-Length path above.
func enforceResponseSize(resp *http.Response, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	if resp.ContentLength >= 0 && resp.ContentLength > maxBytes {
		return errResponseTooLarge
	}
	resp.Body = newLimitedBody(resp.Body, maxBytes)
	return nil
}

// limitedBody wraps an upstream response body and caps the total number of
// bytes that may be read at limit. Reading the (limit+1)th byte fails the copy
// with errResponseTooLarge instead of silently truncating, so an oversized
// chunked/undeclared body is rejected rather than served partial-but-200.
type limitedBody struct {
	rc        io.ReadCloser
	remaining int64 // bytes still allowed before the limit is breached
}

// newLimitedBody caps rc at limit total bytes. It permits exactly limit bytes
// and errors only when the upstream tries to send MORE than limit.
func newLimitedBody(rc io.ReadCloser, limit int64) *limitedBody {
	return &limitedBody{rc: rc, remaining: limit}
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errResponseTooLarge
	}
	// Read up to remaining+1 bytes: the extra byte lets us detect an over-limit
	// body (a full read of remaining bytes that is followed by more data).
	max := l.remaining + 1
	if int64(len(p)) > max {
		p = p[:max]
	}
	n, err := l.rc.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return int(l.remaining + int64(n)), errResponseTooLarge
	}
	return n, err
}

func (l *limitedBody) Close() error { return l.rc.Close() }

// isRedirectStatus reports whether status is one of the HTTP redirect codes that
// carry a Location the proxy must handle (CR4). Only the Location-bearing 3xx
// codes are handled — 304 Not Modified and 300 Multiple Choices are deliberately
// excluded: they are not single-target redirects the browser auto-follows, so
// their (rare/absent) Location is left untouched and the response passes through.
func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect: // 308
		return true
	default:
		return false
	}
}

// handleRedirect implements CR4 redirect handling for a proxied response, run in
// ModifyResponse AFTER the size guard and framing/header strip but BEFORE the
// endpoint body step. The proxy does NOT follow redirects server-side; instead it
// rewrites the Location to a proxy URL so the BROWSER re-enters the proxy for the
// next hop, where the FULL security pipeline re-validates that hop.
//
// It returns (handled, err):
//   - handled==false, err==nil: not a (followable) redirect — caller continues to
//     the normal body step. This covers a non-3xx status, a 3xx with no Location,
//     a 3xx with an unparseable Location, and a 3xx whose Location resolves to a
//     non-http(s) scheme (data:/mailto:/…), which is left as-is rather than
//     rewritten into a non-fetchable proxy URL.
//   - handled==true, err==nil: the redirect was rewritten in place. Location now
//     points at a proxy URL (/proxy or /proxy-resource, matching endpoint) carrying
//     the encoded absolute next-hop target and _wvredir=depth+1. The body step is
//     skipped (a redirect body is an inert courtesy page).
//   - err!=nil: the redirect is REFUSED at the redirect step. err is
//     errRedirectDepthExceeded (depth cap reached / MaxRedirects==0 ⇒ redirects
//     disabled) or errRedirectBlocked (resolved Location host fails the SF2+SF3
//     allowlist pre-check). proxyErrorHandler maps these to 502/403 respectively;
//     the ReverseProxy emits the denial BEFORE any followable Location is written,
//     so the browser never re-enters the proxy for the refused hop.
//
// SECURITY: the allowlist pre-check here is defense-in-depth — it blocks a denied
// hop at the redirect step. The AUTHORITATIVE per-hop IP-blocklist gate remains
// SF4's resolve-then-dial in the transport, which runs when the browser re-requests
// the rewritten proxy URL (we deliberately do NOT add a DNS lookup here). Because
// the browser only ever talks to the proxy — never the upstream directly — a
// destination that fails allowlist (here) or IP-blocklist (on re-request) is
// refused before the browser can contact it.
func (p *proxyHandler) handleRedirect(resp *http.Response, target *url.URL, endpoint string, depth int) (bool, error) {
	if resp == nil || !isRedirectStatus(resp.StatusCode) {
		return false, nil
	}
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if loc == "" {
		// A redirect status with no Location is malformed; nothing to rewrite or
		// follow — pass it through unchanged.
		return false, nil
	}

	// Resolve Location against the CURRENT hop's target to an absolute URL. A
	// relative Location ("/next", "../x") resolves against target; an absolute one
	// is used as-is.
	ref, err := url.Parse(loc)
	if err != nil {
		// An unparseable Location cannot be turned into a followable proxy URL;
		// strip it and pass the (now Location-less) redirect through rather than
		// emitting something the browser would chase to a non-proxied destination.
		resp.Header.Del("Location")
		return false, nil
	}
	abs := target.ResolveReference(ref)

	// Only http/https redirects are fetchable through the proxy. A non-http(s)
	// resolved scheme (data:, mailto:, tel:, javascript:, …) is left as-is: it is
	// not an SSRF vector the proxy fetches, and rewriting it into a proxy URL would
	// be wrong. Pass the response through unchanged.
	switch strings.ToLower(abs.Scheme) {
	case "http", "https":
	default:
		return false, nil
	}

	// Depth cap (Completion Criterion). The current hop arrived at `depth`; the
	// redirect would make the NEXT hop depth+1. If we have already reached the
	// configured maximum we refuse rather than emit a followable Location. With
	// MaxRedirects==0 this trips on the very first redirect ⇒ redirects disabled.
	if depth >= p.cfg.MaxRedirects {
		return false, errRedirectDepthExceeded
	}

	// Defense-in-depth allowlist pre-block (SF2 normalise + SF3 match) on the
	// resolved Location host. A denied hop is refused here, at the redirect step,
	// rather than being rewritten into a proxy URL that the re-request would refuse.
	hostname, herr := hostnameOf(abs.String())
	if herr != nil {
		return false, errRedirectBlocked
	}
	if match := security.MatchHostname(hostname, p.allowlist); !match.Allowed {
		return false, errRedirectBlocked
	}

	// Rewrite Location to a proxy URL the browser will re-enter, carrying the
	// encoded absolute next-hop target and the incremented hop depth. The base path
	// is the SAME endpoint the current request hit, so a redirected subresource
	// stays on /proxy-resource and a redirected page stays on /proxy.
	base := resourceBase + proxyPath
	if endpoint == endpointResource {
		base = resourceBase + proxyResourcePath
	}
	resp.Header.Set("Location", buildRedirectProxyURL(base, abs.String(), depth+1))
	return true, nil
}

// contentEncodingGzip is the Content-Encoding value CR1 decodes. Only gzip is
// handled: it is the encoding the issue scopes (AC 14) and is by far the most
// common on the web. deflate and br (Brotli) are intentionally NOT decoded here
// — a response carrying one of those encodings is treated as non-decodable and
// passed through with its body and headers untouched, exactly like a non-HTML
// response, so the client (or, later, CR2) never receives a half-decoded body.
const contentEncodingGzip = "gzip"

// isHTMLContentType reports whether a Content-Type header value names an HTML
// document. Matching is case-insensitive and tolerates parameters such as a
// "; charset=utf-8" suffix and surrounding whitespace, so "text/html",
// "TEXT/HTML; charset=UTF-8", and "text/html ;charset=utf-8" all match. Only
// the bare "text/html" media type is treated as HTML; anything else (JSON,
// images, application/xhtml+xml, etc.) is not, so it passes through unchanged.
func isHTMLContentType(contentType string) bool {
	// Cut off any parameters (charset, boundary, ...) at the first ';'.
	mediaType := contentType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/html")
}

// prepareHTMLBody detects HTML by Content-Type, obtains the plain HTML bytes
// (gzip-decoding when needed, bounded), and rewrites them with goquery (CR2:
// base-href, subresource/navigation URL rewriting, srcset, CSP/refresh-meta and
// frame-buster removal). It runs AFTER the P4 size guard and the P1/P3
// framing/header strip.
//
// Behaviour:
//   - Non-HTML responses (any Content-Type that is not text/html) are passed
//     through COMPLETELY UNCHANGED: no body read, no header touched. This keeps
//     compressed JSON/images/etc. byte-identical to the upstream.
//   - HTML responses are read into memory (decoding Content-Encoding: gzip when
//     present; deflate/br are out of scope — see contentEncodingGzip — so a body
//     carrying one of those is left untouched and not rewritten), then rewritten
//     by rewriteHTML. CR2 rewrites ALL HTML, not just gzip HTML.
//   - After rewriting, resp.Body is replaced with the rewritten bytes,
//     Content-Length is set to the new length, and (if the body was gzip) the
//     Content-Encoding header is removed so the client does not re-decode.
//
// SECURITY (gzip-bomb guard): decompression is a decompression-bomb DoS vector,
// so the DECODED size is bounded by the same MaxResponseBytes limit P4 enforces
// on the wire size. The gzip stream is read through an io.LimitReader capped at
// maxBytes+1; if the decoded body would exceed maxBytes we return the
// errResponseTooLarge sentinel (→ clean 413 via proxyErrorHandler) instead of
// reading the stream unbounded into memory. A maxBytes <= 0 means "no limit".
//
// DEGRADATION: a gzip-DECODE failure (or over-size) returns an error so the
// ReverseProxy's ErrorHandler emits a clean 413/502 before any body is written.
// A rewriteHTML failure (parse quirk etc.) does NOT fail the gateway — the
// security gates already ran — so the DECODED ORIGINAL HTML is served (200) and a
// warning is logged. pageURL is the validated top-level target captured in the
// ModifyResponse closure; it is the base for relative-URL resolution. hideSelectors
// are the CR5 author hide-selectors threaded from the inbound request; rewriteHTML
// validates and applies them (a nil/empty slice is a no-op).
func prepareHTMLBody(resp *http.Response, maxBytes int64, pageURL *url.URL, logger log.Logger, hideSelectors []string) error {
	if resp == nil || resp.Body == nil {
		return nil
	}

	// Non-HTML ⇒ leave the response entirely alone.
	if !isHTMLContentType(resp.Header.Get("Content-Type")) {
		return nil
	}

	gzipped := strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), contentEncodingGzip)
	hasOtherEncoding := !gzipped && strings.TrimSpace(resp.Header.Get("Content-Encoding")) != ""
	if hasOtherEncoding {
		// HTML carrying a non-gzip encoding (deflate/br) is out of scope to decode;
		// leave the body untouched rather than rewrite a still-encoded payload.
		return nil
	}

	var decoded []byte
	var err error
	if gzipped {
		decoded, err = decodeGzipBounded(resp.Body, maxBytes)
	} else {
		// Plain HTML: read the (already size-guarded) body into memory.
		decoded, err = io.ReadAll(resp.Body)
	}
	// resp.Body is fully consumed (or errored) above; close the original reader
	// regardless of outcome so the upstream connection can be reused/released.
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		// errResponseTooLarge propagates to proxyErrorHandler (→ 413); a malformed
		// gzip stream surfaces as a generic error (→ 502). Either way ModifyResponse
		// returning a non-nil error means the ReverseProxy invokes its ErrorHandler
		// before writing any body, so the client never sees a half-decoded payload.
		return err
	}

	// CR2: rewrite the plain HTML with goquery. On failure, degrade to the decoded
	// original (the security gates already ran — a parse quirk must not 502).
	body := decoded
	rewritten, rerr := htmlRewriter(decoded, pageURL, resp.Header.Get("Content-Type"), hideSelectors)
	if rerr != nil {
		if logger != nil {
			logger.Warn("html rewrite failed; serving decoded original", "url", pageURL.String(), "error", rerr)
		}
	} else {
		body = rewritten
		// rewriteHTML emits UTF-8; update the Content-Type charset so the client
		// decodes it correctly regardless of the upstream's declared charset.
		resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	// The body is now plain (rewritten) HTML: drop the encoding header so the
	// client does not attempt to re-decode, and set Content-Length to the new
	// length so the framing is correct (no stale compressed length).
	if gzipped {
		resp.Header.Del("Content-Encoding")
	}
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// decodeGzipBounded gzip-decompresses r, reading at most maxBytes decoded bytes
// (maxBytes <= 0 disables the bound). If the decoded stream would exceed
// maxBytes it returns errResponseTooLarge WITHOUT buffering the whole stream,
// which is the decompression-bomb guard: the underlying gzip reader is wrapped
// in an io.LimitReader capped at maxBytes+1 so io.ReadAll can never allocate
// more than one byte past the limit before we detect the overflow.
func decodeGzipBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Best-effort close of the gzip reader; the decoded bytes are already
		// buffered and any read error is reported via ReadAll below.
		_ = gr.Close()
	}()

	if maxBytes <= 0 {
		return io.ReadAll(gr)
	}

	// Read one byte past the limit so a body of exactly maxBytes is allowed while
	// anything larger is detected as an overflow.
	limited := io.LimitReader(gr, maxBytes+1)
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return decoded, nil
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
//   - over-size response (P4) ⇒ size-limit ⇒ 413
//   - CR4 redirect depth cap reached / redirects disabled ⇒ redirect-loop ⇒ 502
//   - CR4 redirect into a non-allowlisted host (pre-block) ⇒ allowlist ⇒ 403
//   - per-request-budget timeout / net.Error timeout (P4, Q10) ⇒ timeout ⇒ 504
//   - everything else ⇒ upstream ⇒ 502
//
// It returns the reason so the handler can increment denials_total{reason} for
// the ErrorHandler-driven denial.
func proxyErrorHandler(w http.ResponseWriter, _ *http.Request, err error) string {
	setCORSHeaders(w.Header())
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
	// P4: response body exceeded the configured size limit (clean Content-Length
	// path — no body bytes streamed yet) ⇒ 413.
	if errors.Is(err, errResponseTooLarge) {
		return writeDenial(w, denialReasonSizeLimit, "upstream response exceeds maximum allowed size")
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

// frameAncestorsDirective is the CSP directive that controls who may frame a
// page. Removing it (along with X-Frame-Options) is what makes a proxied,
// otherwise-frameable page embeddable in a Grafana panel.
const frameAncestorsDirective = "frame-ancestors"

// stripFramingHeaders sanitises the proxied response. It removes the headers that
// prevent the page from being framed (X-Frame-Options outright; the
// frame-ancestors directive inside Content-Security-Policy while leaving all other
// CSP directives intact — P1) AND deletes the dangerous incoming response headers
// in strippedResponseHeaders (Set-Cookie, HSTS, HPKP, Clear-Site-Data — P3) so the
// upstream cannot set cookies on, or pin origin-level security policy onto, the
// Grafana origin the response is served from. It also re-applies CORS (ReverseProxy
// may have copied upstream CORS headers over ours). Body/base-tag/frame-buster
// rewriting is OUT OF SCOPE here — that is content-rewriting (see TODO(CR) above).
func stripFramingHeaders(resp *http.Response) error {
	// P3: drop dangerous incoming response headers (state-setting + origin-level
	// security policy). Header.Del canonicalises the key and removes every value,
	// so multi-valued headers (e.g. several Set-Cookie lines) are fully cleared.
	for _, name := range strippedResponseHeaders {
		resp.Header.Del(name)
	}

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

package plugin

// Audit log and metrics emission for the /proxy and /check-frameable requests:
// one audit entry and one metrics observation per request, URL redaction for
// the logs, and the recorder for private IPs admitted by the AllowPrivateIP
// opt-in.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

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

// redactedQueryValue replaces every query value in a logged URL.
const redactedQueryValue = "redacted"

// redactURL returns raw in the form the logs record: query values are replaced
// with "redacted" (the keys and their order are kept), a fragment is dropped
// and user information is replaced, because tile and API URLs often carry keys
// in the query. The audit placeholders pass through; a value that does not
// parse as a URL is logged as auditInvalidURL.
func redactURL(raw string) string {
	if raw == auditMissingURL || raw == auditInvalidURL {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return auditInvalidURL
	}
	if u.User != nil {
		u.User = url.User(redactedQueryValue)
	}
	u.Fragment, u.RawFragment = "", ""
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, part := range parts {
			if key, _, hasValue := strings.Cut(part, "="); hasValue {
				parts[i] = key + "=" + redactedQueryValue
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	u.ForceQuery = false
	return u.String()
}

// redactErrorURL returns err for logging with the URL of a *url.Error (which
// net/http's client puts in the message) redacted like redactURL.
func redactErrorURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return &url.Error{Op: ue.Op, URL: redactURL(ue.URL), Err: ue.Err}
	}
	return err
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

// endpointTopLevel / endpointCheckFrameable name the endpoints that share the
// security pipeline: the HTML-rewriting /proxy endpoint and /check-frameable
// (FR1). The value is recorded on the audit log's `endpoint` field so the
// streams are distinguishable even though they share the metrics collectors
// (sharing is acceptable — see newProxyMetrics).
const (
	endpointTopLevel       = "proxy"
	endpointCheckFrameable = "check-frameable"
)

// requestAudit is the per-request audit and metrics state of one pipeline
// request (P5 / AC 28, P6 / AC 29). beginAudit creates it and endAudit, which
// the handler defers, emits it. That single deferred emission is the only place
// an audit entry is written, so a new denial path cannot forget it. Handler
// branches set url, denied and allowPrivate on their way out. The handler
// goroutine owns the struct; permits is the only field the dial goroutine
// writes, and it has its own lock.
type requestAudit struct {
	endpoint string
	start    time.Time
	rec      *auditResponseWriter
	// url is the best-known target for the log line: a placeholder until the
	// raw `url` param is read, then the validated, normalised target.
	url string
	// denied is the denial-reason token (empty for a served request).
	denied string
	// allowPrivate is the matched domain's AllowPrivateIP opt-in (false until
	// the allowlist matches).
	allowPrivate bool
	// permits collects the private IPs the opt-in admitted at the SF4 gate.
	permits *privatePermitRecorder
}

// beginAudit marks the request in flight and wraps w so the final status and
// body size are recorded. The caller must defer endAudit and write through
// a.rec.
func (p *proxyHandler) beginAudit(w http.ResponseWriter, endpoint string) *requestAudit {
	p.metrics.inFlight.Inc()
	return &requestAudit{
		endpoint: endpoint,
		start:    time.Now(),
		rec:      newAuditResponseWriter(w),
		url:      auditMissingURL,
		permits:  &privatePermitRecorder{},
	}
}

// endAudit emits the single audit entry and the per-request metrics:
// requests_total{status} and the duration histogram always, denials_total only
// when a.denied is set. It also emits one distinct Warn line (and the permit
// metric) per private IP that an AllowPrivateIP opt-in admitted, because that
// re-opened SSRF surface must be loud in the logs. It runs after the upstream
// round-trip has returned, so the dial goroutine has finished writing permits.
func (p *proxyHandler) endAudit(req *http.Request, a *requestAudit) {
	duration := time.Since(a.start)
	p.metrics.inFlight.Dec()
	p.metrics.observeRequest(a.rec.status, duration.Seconds(), a.denied)
	user := auditUser(req.Context())
	logger := p.logger.FromContext(req.Context())
	loggedURL := redactURL(a.url)
	logger.Info("proxy request",
		"endpoint", a.endpoint,
		"url", loggedURL,
		"user", user,
		"status", a.rec.status,
		"bytes", a.rec.bytes,
		"duration", duration,
		"allowPrivateIP", a.allowPrivate,
	)
	for _, permit := range a.permits.snapshot() {
		p.metrics.privateIPPermitted.WithLabelValues(permit.reason).Inc()
		logger.Warn("proxy private-ip permitted by opt-in",
			"endpoint", a.endpoint,
			"url", loggedURL,
			"user", user,
			"permittedIP", permit.ip.String(),
			"ipClass", permit.reason,
		)
	}
}

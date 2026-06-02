package plugin

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metric/label name constants for the /proxy endpoint. Names follow
// the snake_case convention with a shared webview_proxy_ namespace; counters end
// in _total and the duration histogram in _seconds (P6 / AC 29).
const (
	metricRequestsTotal      = "webview_proxy_requests_total"
	metricDenialsTotal       = "webview_proxy_denials_total"
	metricRequestsInFlight   = "webview_proxy_requests_in_flight"
	metricRequestDurationSec = "webview_proxy_request_duration_seconds"

	// labelStatus carries the final HTTP status code as a string on
	// requests_total; labelReason carries the denial-reason token on
	// denials_total.
	labelStatus = "status"
	labelReason = "reason"
)

// Denial-reason label values for denials_total. P7 makes the full
// denial→(status, reason) matrix authoritative: every denial branch picks a
// reason token here and writes the status reasonStatus (below) assigns to it,
// so the status sent to the client and the reason recorded on denials_total
// can never drift. The set covers every early-return denial in ServeHTTP and
// every error-handler denial in proxyErrorHandler.
const (
	denialReasonAllowlist   = "allowlist"     // SF3 empty/non-matching allowlist (403)
	denialReasonIPBlocklist = "ip-blocklist"  // SF4 blocked resolved/connect IP (403)
	denialReasonMetadata    = "metadata"      // SF4 cloud-metadata host (403)
	denialReasonRateLimit   = "rate-limit"    // SF5 per-instance/per-domain rate tier (429)
	denialReasonConcurrency = "concurrency"   // SF5 concurrency cap exhausted (429)
	denialReasonSizeLimit   = "size-limit"    // P4 response exceeds MaxResponseBytes (413)
	denialReasonScheme      = "scheme"        // SF2 scheme/port/userinfo/host/malformed (400)
	denialReasonBadRequest  = "bad-request"   // missing url param / unbuildable target (400)
	denialReasonMethod      = "method"        // non-GET method (405)
	denialReasonTimeout     = "timeout"       // P4 per-request budget expired (504)
	denialReasonUpstream    = "upstream"      // upstream resolve/transport/gateway failure (502)
	denialReasonRedirect    = "redirect-loop" // CR4 redirect depth cap exceeded (502)
)

// reasonStatus is the authoritative denial-reason → HTTP-status mapping for the
// /proxy endpoint (P7) and the SINGLE source of truth for the status a denial
// produces. Every denial branch picks a reason and writes the status this table
// assigns to it (via statusForReason / writeDenial), so a future edit cannot let
// the status sent to the client diverge from the reason recorded on
// denials_total. The denial classes intentionally span 400/403/405/413/429/502/504.
var reasonStatus = map[string]int{
	denialReasonAllowlist:   http.StatusForbidden,             // 403
	denialReasonIPBlocklist: http.StatusForbidden,             // 403
	denialReasonMetadata:    http.StatusForbidden,             // 403
	denialReasonRateLimit:   http.StatusTooManyRequests,       // 429
	denialReasonConcurrency: http.StatusTooManyRequests,       // 429
	denialReasonSizeLimit:   http.StatusRequestEntityTooLarge, // 413
	denialReasonScheme:      http.StatusBadRequest,            // 400
	denialReasonBadRequest:  http.StatusBadRequest,            // 400
	denialReasonMethod:      http.StatusMethodNotAllowed,      // 405
	denialReasonTimeout:     http.StatusGatewayTimeout,        // 504
	denialReasonUpstream:    http.StatusBadGateway,            // 502
	denialReasonRedirect:    http.StatusBadGateway,            // 502
}

// statusForReason returns the HTTP status the given denial reason maps to. An
// unknown reason — which the closed reason set above is meant to make
// impossible — is treated as an internal error rather than silently mapped to
// 200, so any drift between the reason set and the table surfaces loudly.
func statusForReason(reason string) int {
	if status, ok := reasonStatus[reason]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// proxyMetrics holds the Prometheus collectors for the /proxy endpoint. It is
// constructed once (per registry) by newProxyMetrics and is safe for concurrent
// use — all prometheus collectors are internally synchronised.
type proxyMetrics struct {
	requests *prometheus.CounterVec
	denials  *prometheus.CounterVec
	inFlight prometheus.Gauge
	duration prometheus.Histogram
}

// newProxyMetrics defines and registers the /proxy collectors against reg.
//
// In production newProxyHandler passes prometheus.DefaultRegisterer so the
// metrics are gathered by the SDK's diagnostics adapter (backed by
// prometheus.DefaultGatherer) and served on the plugin's standard /metrics
// endpoint. Tests pass a fresh prometheus.NewRegistry() so each test asserts in
// isolation and avoids duplicate-registration panics.
func newProxyMetrics(reg prometheus.Registerer) *proxyMetrics {
	factory := promauto.With(reg)
	return &proxyMetrics{
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: metricRequestsTotal,
			Help: "Total number of /proxy requests handled, labelled by final HTTP status code.",
		}, []string{labelStatus}),
		denials: factory.NewCounterVec(prometheus.CounterOpts{
			Name: metricDenialsTotal,
			Help: "Total number of /proxy requests denied, labelled by denial reason.",
		}, []string{labelReason}),
		inFlight: factory.NewGauge(prometheus.GaugeOpts{
			Name: metricRequestsInFlight,
			Help: "Number of /proxy requests currently in flight.",
		}),
		duration: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    metricRequestDurationSec,
			Help:    "Duration of /proxy request handling in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

// observeRequest records the per-request metrics emitted once per /proxy request
// in the handler's single deferred block: the requests_total counter keyed by
// final status and the request-duration histogram. denied carries the denial
// reason (empty for a successful/upstream-served request); when non-empty the
// denials_total counter is incremented for that reason. Successful requests do
// NOT increment denials.
func (m *proxyMetrics) observeRequest(status int, durationSec float64, denied string) {
	m.requests.WithLabelValues(statusLabel(status)).Inc()
	m.duration.Observe(durationSec)
	if denied != "" {
		m.denials.WithLabelValues(denied).Inc()
	}
}

// statusLabel renders an HTTP status code as the decimal string used for the
// requests_total status label (e.g. 200, 403, 504).
func statusLabel(status int) string {
	return strconv.Itoa(status)
}

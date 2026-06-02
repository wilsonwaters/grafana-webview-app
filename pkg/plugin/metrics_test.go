package plugin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newMetricsTestHandler builds a proxyHandler whose Prometheus collectors are
// registered against a FRESH prometheus.NewRegistry (not the shared default
// registry), so each test asserts metric values in isolation and never panics
// on duplicate registration. When upstream is non-nil the transport is swapped
// for the same loopback-redirecting dialer newTestHandler uses, so the full
// security pipeline + ReverseProxy + ModifyResponse run without real network.
// It returns the handler alongside the registry the test gathers from.
func newMetricsTestHandler(t *testing.T, cfg PluginSettings, upstream *httptest.Server) (*proxyHandler, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	p := newProxyHandlerWithRegistry(cfg, reg)
	if upstream != nil {
		upstreamURL, err := url.Parse(upstream.URL)
		if err != nil {
			t.Fatalf("parse upstream URL: %v", err)
		}
		var d net.Dialer
		p.transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.DialContext(ctx, network, upstreamURL.Host)
			},
		}
	}
	return p, reg
}

// requestsTotalFor returns the current value of webview_proxy_requests_total for
// the given status-code label (0 when the label has not been observed yet).
func requestsTotalFor(t *testing.T, p *proxyHandler, status int) float64 {
	t.Helper()
	return testutil.ToFloat64(p.metrics.requests.WithLabelValues(statusLabel(status)))
}

// denialsTotalFor returns the current value of webview_proxy_denials_total for
// the given reason label (0 when the reason has not been observed yet).
func denialsTotalFor(t *testing.T, p *proxyHandler, reason string) float64 {
	t.Helper()
	return testutil.ToFloat64(p.metrics.denials.WithLabelValues(reason))
}

// TestMetricsAllFourDefinedAndRegistered covers Completion Criterion: all four
// metric types are defined and registered on the injected registry. The
// in-flight gauge is always present (count 1) and reads 0 before any request;
// the *_total / *_seconds families register lazily on first observation, so we
// assert their collectors are non-nil and the families surface after one request.
func TestMetricsAllFourDefinedAndRegistered(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RequestTimeoutSec = 1
	p, reg := newMetricsTestHandler(t, cfg, nil)
	p.transport = blockingRoundTripper{} // deterministic 504, no real network/DNS

	// All four collectors must be constructed.
	if p.metrics.requests == nil || p.metrics.denials == nil || p.metrics.inFlight == nil || p.metrics.duration == nil {
		t.Fatal("newProxyMetrics left a collector nil")
	}

	// The gauge is registered and gatherable from the fresh registry at value 0.
	if n, err := testutil.GatherAndCount(reg, metricRequestsInFlight); err != nil {
		t.Fatalf("gather %s: %v", metricRequestsInFlight, err)
	} else if n != 1 {
		t.Fatalf("%s gather count: got %d, want 1", metricRequestsInFlight, n)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight before any request: got %v, want 0", got)
	}

	// Drive one request so the counter/histogram families materialise. The
	// blocking transport makes it a deterministic 504 (timeout denial), which
	// forces both requests_total and denials_total to register lazily.
	if rec := doProxy(p, "http://example.com/page"); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("warm-up request: got status %d, want 504", rec.Code)
	}
	for _, name := range []string{metricRequestsTotal, metricDenialsTotal, metricRequestDurationSec} {
		if n, err := testutil.GatherAndCount(reg, name); err != nil {
			t.Fatalf("gather %s: %v", name, err)
		} else if n == 0 {
			t.Fatalf("metric %s did not materialise after a request", name)
		}
	}
}

// TestMetricsSuccessIncrementsRequests covers Completion Criterion: a successful
// request increments requests_total{status="200"}, observes the duration
// histogram once, increments NO denial, and leaves in_flight at 0 on completion.
func TestMetricsSuccessIncrementsRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, _ := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	if rec := doProxy(p, "http://example.com/ok"); rec.Code != http.StatusOK {
		t.Fatalf("success: got status %d, want 200", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusOK); got != 1 {
		t.Fatalf("requests_total{status=200}: got %v, want 1", got)
	}
	if got := testutil.CollectAndCount(p.metrics.duration); got != 1 {
		t.Fatalf("duration histogram series count: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after completion: got %v, want 0", got)
	}
	// A successful request must NOT increment any denial reason.
	if got := testutil.CollectAndCount(p.metrics.denials); got != 0 {
		t.Fatalf("denials_total should be empty on success, got %v series", got)
	}
}

// TestMetricsDenialAllowlist covers Completion Criterion: an empty-allowlist
// denial (403) increments requests_total{status="403"} and
// denials_total{reason="allowlist"}, and leaves in_flight at 0.
func TestMetricsDenialAllowlist(t *testing.T) {
	p, _ := newMetricsTestHandler(t, settingsWith(nil), nil) // empty allowlist => 403

	if rec := doProxy(p, "https://example.com/page"); rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist: got status %d, want 403", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusForbidden); got != 1 {
		t.Fatalf("requests_total{status=403}: got %v, want 1", got)
	}
	if got := denialsTotalFor(t, p, denialReasonAllowlist); got != 1 {
		t.Fatalf("denials_total{reason=allowlist}: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after denial: got %v, want 0", got)
	}
}

// TestMetricsDenialRateLimit covers Completion Criterion: a rate-limit denial
// (429) increments requests_total{status="429"} and
// denials_total{reason="rate-limit"}. The first request succeeds (200); the
// second trips the per-domain 1/min limit.
func TestMetricsDenialRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RateLimitPerInstancePerMin = 100
	cfg.RateLimitPerDomainPerMin = 1
	p, _ := newMetricsTestHandler(t, cfg, upstream)

	if rec := doProxy(p, "http://example.com/a"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200", rec.Code)
	}
	if rec := doProxy(p, "http://example.com/b"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got status %d, want 429", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusOK); got != 1 {
		t.Fatalf("requests_total{status=200}: got %v, want 1", got)
	}
	if got := requestsTotalFor(t, p, http.StatusTooManyRequests); got != 1 {
		t.Fatalf("requests_total{status=429}: got %v, want 1", got)
	}
	if got := denialsTotalFor(t, p, denialReasonRateLimit); got != 1 {
		t.Fatalf("denials_total{reason=rate-limit}: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after completion: got %v, want 0", got)
	}
}

// TestMetricsDenialSizeLimit covers Completion Criterion: an over-size response
// (clean Content-Length path, 413) increments requests_total{status="413"} and
// denials_total{reason="size-limit"}.
func TestMetricsDenialSizeLimit(t *testing.T) {
	const oversized = "this body is definitely longer than sixteen bytes"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(oversized))
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 16
	p, _ := newMetricsTestHandler(t, cfg, upstream)

	if rec := doProxy(p, "http://example.com/big"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: got status %d, want 413", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusRequestEntityTooLarge); got != 1 {
		t.Fatalf("requests_total{status=413}: got %v, want 1", got)
	}
	if got := denialsTotalFor(t, p, denialReasonSizeLimit); got != 1 {
		t.Fatalf("denials_total{reason=size-limit}: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after completion: got %v, want 0", got)
	}
}

// TestMetricsDenialTimeout covers Completion Criterion: a per-request-budget
// timeout (504) increments requests_total{status="504"} and
// denials_total{reason="timeout"}. A blocking transport drives the deadline
// deterministically.
func TestMetricsDenialTimeout(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RequestTimeoutSec = 1
	p, _ := newMetricsTestHandler(t, cfg, nil)
	p.transport = blockingRoundTripper{}

	if rec := doProxy(p, "http://example.com/slow"); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: got status %d, want 504", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusGatewayTimeout); got != 1 {
		t.Fatalf("requests_total{status=504}: got %v, want 1", got)
	}
	if got := denialsTotalFor(t, p, denialReasonTimeout); got != 1 {
		t.Fatalf("denials_total{reason=timeout}: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after completion: got %v, want 0", got)
	}
}

// TestMetricsInFlightReturnsToZero covers Completion Criterion: in_flight is a
// gauge that increments during a request and returns to 0 afterwards across a
// mix of success and denial outcomes. We drive several requests and assert the
// gauge settles back to 0.
func TestMetricsInFlightReturnsToZero(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, _ := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	doProxy(p, "http://example.com/a")     // success
	doProxy(p, "http://notallowed.test/b") // allowlist denial (403)
	doProxy(p, "http://example.com/c")     // success

	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after all requests completed: got %v, want 0", got)
	}
	// The denial for the non-allowlisted host must be recorded once.
	if got := denialsTotalFor(t, p, denialReasonAllowlist); got != 1 {
		t.Fatalf("denials_total{reason=allowlist}: got %v, want 1", got)
	}
}

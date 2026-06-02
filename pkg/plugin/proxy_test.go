package plugin

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wilsonwaters/webview/pkg/security"
)

// newTestHandler builds a proxyHandler from cfg and swaps in a transport that
// dials the given httptest server regardless of the requested upstream host, so
// the success-path tests exercise the full security pipeline + ReverseProxy +
// ModifyResponse without any real network or DNS. The production transport (the
// SF4 secure dialer) is verified separately by TestNewProxyHandlerWiresSecureDialer.
func newTestHandler(t *testing.T, cfg PluginSettings, upstream *httptest.Server) *proxyHandler {
	t.Helper()
	p := newProxyHandler(cfg)
	if upstream != nil {
		upstreamURL, err := url.Parse(upstream.URL)
		if err != nil {
			t.Fatalf("parse upstream URL: %v", err)
		}
		var d net.Dialer
		p.transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				// Redirect every dial to the test server's real loopback address.
				return d.DialContext(ctx, network, upstreamURL.Host)
			},
		}
	}
	return p
}

// allowExample is a one-domain allowlist used by most tests.
func allowExample(opts DomainOptions) []AllowedDomain {
	return []AllowedDomain{{Domain: "example.com", Options: opts}}
}

// doProxy issues a GET /proxy?url=<target> against handler and returns the
// recorder. target is the RAW (unencoded) absolute URL; it is percent-encoded
// into the query string here exactly as a real caller would.
func doProxy(handler http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+url.QueryEscape(target), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestProxyEmptyAllowlistDenies covers Completion Criterion: empty allowlist => 403.
func TestProxyEmptyAllowlistDenies(t *testing.T) {
	cfg := settingsWith(nil)
	p := newProxyHandler(cfg) // no upstream needed: denied before any fetch
	rec := doProxy(p, "https://example.com/page")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist: got status %d, want 403", rec.Code)
	}
}

// TestProxyAllowlistedSuccess covers Completion Criterion: allowlisted host
// success path — the stub upstream's 200 body is returned to the caller.
func TestProxyAllowlistedSuccess(t *testing.T) {
	const body = "hello from upstream"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/page" {
			t.Errorf("upstream got path %q, want /page", r.URL.Path)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("success path: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("success path: got body %q, want %q", got, body)
	}
}

// TestProxyBadScheme covers Completion Criterion: bad scheme => 400.
func TestProxyBadScheme(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		rec := doProxy(p, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("scheme %q: got status %d, want 400", target, rec.Code)
		}
	}
}

// TestProxyDisallowedPort covers Completion Criterion: disallowed port => 400.
func TestProxyDisallowedPort(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{})) // no extra ports
	p := newProxyHandler(cfg)
	rec := doProxy(p, "https://example.com:8443/page")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disallowed port: got status %d, want 400", rec.Code)
	}
}

// TestProxyAllowedExtraPort confirms a port declared in the matched domain's
// AllowedPorts passes the SF2 re-check (and is denied without it). Reinforces
// the port re-check placement after the allowlist match.
func TestProxyAllowedExtraPort(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{AllowedPorts: []int{8081}}))
	p := newTestHandler(t, cfg, upstream)
	rec := doProxy(p, "http://example.com:8081/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed extra port: got status %d, want 200", rec.Code)
	}
}

// TestProxyNonAllowlistedHost covers Completion Criterion: non-allowlisted host => 403.
func TestProxyNonAllowlistedHost(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	rec := doProxy(p, "https://evil.com/page")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted host: got status %d, want 403", rec.Code)
	}
}

// TestProxyMissingURLParam covers Completion Criterion: missing/empty url => 400.
func TestProxyMissingURLParam(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	for _, q := range []string{"/proxy", "/proxy?url=", "/proxy?url=%20"} {
		req := httptest.NewRequest(http.MethodGet, q, nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: got status %d, want 400", q, rec.Code)
		}
	}
}

// TestProxyRateLimitExhaustion covers Completion Criterion: rate-limit
// exhaustion => 429. The per-domain limit is set to 1/min so the second request
// in the same minute is rejected.
func TestProxyRateLimitExhaustion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RateLimitPerInstancePerMin = 100
	cfg.RateLimitPerDomainPerMin = 1
	p := newTestHandler(t, cfg, upstream)

	if rec := doProxy(p, "http://example.com/a"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200", rec.Code)
	}
	if rec := doProxy(p, "http://example.com/b"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (rate limited): got status %d, want 429", rec.Code)
	}
}

// TestProxyConcurrencyCapExhaustion covers the concurrency tier of the rate
// limiter: with max-concurrent held at 0 effective slots, Acquire fails => 429.
// We hold the only slot by exhausting MaxConcurrentRequests=1 with a blocking
// upstream, but a simpler deterministic check is to pre-acquire the slot.
func TestProxyConcurrencyCapExhaustion(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxConcurrentRequests = 1
	cfg.RateLimitPerInstancePerMin = 100
	cfg.RateLimitPerDomainPerMin = 100
	p := newProxyHandler(cfg)

	// Hold the single concurrency slot so the request's Acquire() fails.
	release, ok := p.rateLimiter.Acquire()
	if !ok {
		t.Fatal("setup: expected to acquire the only slot")
	}
	defer release()

	rec := doProxy(p, "https://example.com/page")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrency cap: got status %d, want 429", rec.Code)
	}
}

// TestProxyStripsXFrameOptions covers Completion Criterion: X-Frame-Options removed.
func TestProxyStripsXFrameOptions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)
	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if v := rec.Header().Get("X-Frame-Options"); v != "" {
		t.Fatalf("X-Frame-Options should be removed, got %q", v)
	}
}

// TestProxyNeutralisesCSPFrameAncestors covers Completion Criterion: CSP
// frame-ancestors neutralised while other directives are preserved.
func TestProxyNeutralisesCSPFrameAncestors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; script-src 'self'")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)
	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(strings.ToLower(csp), "frame-ancestors") {
		t.Fatalf("frame-ancestors should be removed, CSP=%q", csp)
	}
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("other CSP directives should be preserved, CSP=%q", csp)
	}
}

// TestProxyCORSHeaderPresent covers Completion Criterion: permissive CORS header present.
func TestProxyCORSHeaderPresent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	// Success path carries CORS.
	rec := doProxy(p, "http://example.com/page")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("success: Access-Control-Allow-Origin = %q, want *", got)
	}

	// Denial path also carries CORS.
	pDeny := newProxyHandler(settingsWith(nil))
	recDeny := doProxy(pDeny, "https://example.com/page")
	if got := recDeny.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("denial: Access-Control-Allow-Origin = %q, want *", got)
	}
}

// TestProxyOptionsPreflight verifies OPTIONS preflight returns 204 with CORS and
// without running the pipeline (no url param required).
func TestProxyOptionsPreflight(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	req := httptest.NewRequest(http.MethodOptions, "/proxy", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS preflight: got status %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("OPTIONS preflight: Access-Control-Allow-Origin = %q, want *", got)
	}
}

// TestNewProxyHandlerWiresSecureDialer confirms the PRODUCTION transport is an
// *http.Transport whose DialContext is wired to SF4's secure dialer (so DNS
// validation and the rebind guard run at connect time). We assert behaviour: a
// loopback target is rejected at dial time with a SF4 *DialError (ReasonBlockedIP),
// which the ErrorHandler maps to 403 — proving the secure dialer is in the path.
func TestNewProxyHandlerWiresSecureDialer(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg) // uses the real secure-dialer transport

	if _, ok := p.transport.(*http.Transport); !ok {
		t.Fatalf("production transport should be *http.Transport, got %T", p.transport)
	}

	// Drive a dial directly through the production transport's dialer to confirm
	// it is the secure dialer: a loopback address must be blocked by SF4.
	tr := p.transport.(*http.Transport)
	_, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("secure dialer should reject loopback, got nil error")
	}
	if reason := security.DialReasonOf(err); reason != security.ReasonBlockedIP {
		t.Fatalf("secure dialer reject reason = %q, want %q", reason, security.ReasonBlockedIP)
	}
}

// timeoutError is a net.Error reporting Timeout()==true, used to exercise the
// 504 branch of proxyErrorHandler.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestProxyErrorHandlerMapping covers Completion Criterion: upstream/transport
// failures map to clean status codes — SF4 dial denials (blocked IP / metadata
// host) => 403, resolve failure => 502, timeout => 504, generic failure => 502.
// CORS is asserted present on the error response.
func TestProxyErrorHandlerMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"blocked IP => 403", &security.DialError{Reason: security.ReasonBlockedIP, IPReason: "loopback", BlockedIP: net.ParseIP("127.0.0.1"), Message: "blocked"}, http.StatusForbidden},
		{"metadata host => 403", &security.DialError{Reason: security.ReasonMetadataHost, Message: "metadata"}, http.StatusForbidden},
		{"resolve failed => 502", &security.DialError{Reason: security.ReasonResolveFailed, Message: "nxdomain"}, http.StatusBadGateway},
		{"timeout => 504", timeoutError{}, http.StatusGatewayTimeout},
		{"generic => 502", io.ErrUnexpectedEOF, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
			proxyErrorHandler(rec, req, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("got status %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("error response missing CORS: %q", got)
			}
		})
	}
}

// settingsWith returns a PluginSettings with the given allowlist and otherwise
// generous defaults so rate limits do not interfere with non-rate-limit tests.
func settingsWith(domains []AllowedDomain) PluginSettings {
	return PluginSettings{
		AllowedDomains:             domains,
		MaxResponseBytes:           DefaultMaxResponseBytes,
		RequestTimeoutSec:          DefaultRequestTimeoutSec,
		MaxRedirects:               DefaultMaxRedirects,
		RateLimitPerInstancePerMin: 1000,
		RateLimitPerDomainPerMin:   1000,
		MaxConcurrentRequests:      100,
	}
}

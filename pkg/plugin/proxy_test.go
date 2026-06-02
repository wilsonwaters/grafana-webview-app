package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestProxyStripsResponseHeaders covers Completion Criterion (P3): the dangerous
// incoming response headers — Set-Cookie (incl. multiple values), HSTS, HPKP (both
// variants), and Clear-Site-Data — are ABSENT on the proxied response after the
// real path through ModifyResponse, while a benign header survives.
func TestProxyStripsResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		// Multiple Set-Cookie values: all must be cleared, not just the first.
		h.Add("Set-Cookie", "sid=secret; Path=/; HttpOnly")
		h.Add("Set-Cookie", "track=abc; Path=/")
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		h.Set("Public-Key-Pins", `pin-sha256="abc"; max-age=5184000`)
		h.Set("Public-Key-Pins-Report-Only", `pin-sha256="def"; report-uri="https://x/r"`)
		h.Set("Clear-Site-Data", `"cookies", "storage"`)
		h.Set("Content-Type", "text/html; charset=utf-8") // benign: must survive
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)
	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	for _, name := range []string{
		"Set-Cookie",
		"Strict-Transport-Security",
		"Public-Key-Pins",
		"Public-Key-Pins-Report-Only",
		"Clear-Site-Data",
	} {
		if v := rec.Header().Values(name); len(v) != 0 {
			t.Errorf("header %q should be stripped from the proxied response, got %v", name, v)
		}
	}

	// A benign, render-relevant header must NOT be removed.
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Error("Content-Type should be preserved on the proxied response")
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

// recordingTransport is an http.RoundTripper whose DialContext records the host
// portion of every address the proxy attempts to dial, then redirects the dial
// to a single loopback upstream (so the genuinely-allowlisted control case can
// complete a real round-trip). The recorded hosts let a test assert WHICH host
// would actually have been contacted — the crux of the parser-differential
// invariant: a denied request must reach dial for NO host at all, and an allowed
// request must dial ONLY the validated, allowlisted host.
type recordingTransport struct {
	*http.Transport
	mu       sync.Mutex
	dialed   []string
	upstream string // loopback host:port the dial is redirected to
}

func newRecordingTransport(upstream *httptest.Server) *recordingTransport {
	rt := &recordingTransport{}
	if upstream != nil {
		u, err := url.Parse(upstream.URL)
		if err == nil {
			rt.upstream = u.Host
		}
	}
	var d net.Dialer
	rt.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				host = addr
			}
			rt.mu.Lock()
			rt.dialed = append(rt.dialed, host)
			rt.mu.Unlock()
			// Redirect to the loopback upstream so the allowlisted control case
			// can complete; the assertion is on the RECORDED requested host, not
			// the loopback we dial in its place.
			return d.DialContext(ctx, network, rt.upstream)
		},
	}
	return rt
}

// dialedHosts returns a copy of the hosts the proxy attempted to dial.
func (rt *recordingTransport) dialedHosts() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]string, len(rt.dialed))
	copy(out, rt.dialed)
	return out
}

// TestProxySSRFParserDifferentialResistance LOCKS the core SSRF invariant of the
// /proxy pipeline: the host that the allowlist matches (SF3), the host SF2
// validates, and the host that is actually dialed (SF4) are all the SAME
// canonical value, because the upstream target is reconstructed from VALIDATED
// components (buildTargetURL/hostnameOf), never from the raw attacker string. A
// future refactor that re-parses the raw string differently in any of those
// places — reintroducing a parser-differential SSRF bypass — must fail this test.
//
// The allowlist is exactly example.com (exact match, subdomains OFF). A recording
// transport captures every host the proxy attempts to dial. For every malicious
// row the response must be a denial (400/403) AND no non-allowlisted host
// (evil.com / internal) may ever be dialed. The legitimate control row must
// succeed and dial exactly example.com, preserving path and query.
func TestProxySSRFParserDifferentialResistance(t *testing.T) {
	const allowedHost = "example.com"
	const evilHost = "evil.com"

	cases := []struct {
		name string
		// target is the RAW absolute URL placed (percent-encoded) into ?url=.
		target string
		// allowed true => success path (dial of the allowlisted host expected);
		// false => denial (no non-allowlisted host may be dialed).
		allowed bool
		// wantStatus is asserted exactly for clarity/regression on the mapping.
		wantStatus int
		// wantDialHost, when allowed, is the only host that may be dialed.
		wantDialHost string
		// wantPath/wantQuery assert path+query preservation on the control row.
		wantPath  string
		wantQuery string
	}{
		{
			// Go parses "example.com" as USERINFO and the authority as evil.com.
			// SF3 (allowlist) runs before SF2 (userinfo) and denies the unlisted
			// evil.com host with 403; either way evil.com is never dialed.
			name:       "userinfo trick @evil.com",
			target:     "http://example.com@evil.com",
			allowed:    false,
			wantStatus: http.StatusForbidden,
		},
		{
			// Percent-encoded '@': url.Parse rejects the bad escape => malformed => 400.
			name:       "encoded userinfo %40evil.com",
			target:     "http://example.com%40evil.com",
			allowed:    false,
			wantStatus: http.StatusBadRequest,
		},
		{
			// Backslash before '@': url.Parse rejects invalid userinfo => malformed => 400.
			name:       "backslash userinfo \\@evil.com",
			target:     `http://example.com\@evil.com`,
			allowed:    false,
			wantStatus: http.StatusBadRequest,
		},
		{
			// Fragment decoy: authority is evil.com, "#example.com" is just a fragment.
			name:       "fragment decoy #example.com",
			target:     "http://evil.com#example.com",
			allowed:    false,
			wantStatus: http.StatusForbidden,
		},
		{
			// Path decoy: authority is evil.com, "/example.com" is just a path.
			name:       "path decoy /example.com",
			target:     "http://evil.com/example.com",
			allowed:    false,
			wantStatus: http.StatusForbidden,
		},
		{
			// Suffix trick: example.com.evil.com is a different host (subdomains off).
			name:       "suffix trick example.com.evil.com",
			target:     "http://example.com.evil.com",
			allowed:    false,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "plain non-allowlisted host",
			target:     "http://evil.com",
			allowed:    false,
			wantStatus: http.StatusForbidden,
		},
		{
			// Legitimate control: allowed, dials exactly example.com, preserves path+query.
			name:         "control allowlisted host",
			target:       "http://example.com/path?q=1#frag",
			allowed:      true,
			wantStatus:   http.StatusOK,
			wantDialHost: allowedHost,
			wantPath:     "/path",
			wantQuery:    "q=1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantPath != "" && r.URL.Path != tc.wantPath {
					t.Errorf("upstream path = %q, want %q", r.URL.Path, tc.wantPath)
				}
				if tc.wantQuery != "" && r.URL.RawQuery != tc.wantQuery {
					t.Errorf("upstream query = %q, want %q", r.URL.RawQuery, tc.wantQuery)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := settingsWith(allowExample(DomainOptions{})) // exact match, subdomains off
			p := newProxyHandler(cfg)
			rt := newRecordingTransport(upstream)
			p.transport = rt

			rec := doProxy(p, tc.target)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			dialed := rt.dialedHosts()
			for _, h := range dialed {
				if h == evilHost || h != allowedHost {
					t.Fatalf("proxy dialed disallowed host %q (all dialed: %v)", h, dialed)
				}
			}

			if tc.allowed {
				if len(dialed) != 1 || dialed[0] != tc.wantDialHost {
					t.Fatalf("allowed row: dialed %v, want exactly [%q]", dialed, tc.wantDialHost)
				}
			} else if len(dialed) != 0 {
				t.Fatalf("denied row: expected NO dial, but dialed %v", dialed)
			}
		})
	}
}

// TestProxySSRFMultipleURLParamsFirstWins documents and locks the multi-value
// url= behaviour: url.Values.Get returns the FIRST value, so only the
// genuinely-allowlisted first host (example.com) is acted on and the trailing
// evil.com is ignored entirely — never validated, never dialed.
func TestProxySSRFMultipleURLParamsFirstWins(t *testing.T) {
	const allowedHost = "example.com"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	rt := newRecordingTransport(upstream)
	p.transport = rt

	// Two url params; the first is allowlisted, the second is not. Build the
	// request directly (doProxy escapes a single target) so both params are sent.
	req := httptest.NewRequest(http.MethodGet,
		"/proxy?url="+url.QueryEscape("http://example.com/")+"&url="+url.QueryEscape("http://evil.com/"), nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("first-param-wins: status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	dialed := rt.dialedHosts()
	if len(dialed) != 1 || dialed[0] != allowedHost {
		t.Fatalf("first-param-wins: dialed %v, want exactly [%q] (evil.com must never be dialed)", dialed, allowedHost)
	}
}

// TestProxyStripsRequestHeaders covers Completion Criteria: auth and
// Grafana-specific headers are absent from the forwarded request, and the
// conservative User-Agent/Accept are present with the expected values. An inbound
// request carrying every sensitive header category is sent; the stub upstream
// RECORDS exactly the headers it received, and we assert on those.
func TestProxyStripsRequestHeaders(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	// Inbound request loaded with every category that must be stripped.
	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+url.QueryEscape("http://example.com/page"), nil)
	inbound := map[string]string{
		"Cookie":                   "grafana_session=secret",
		"Cookie2":                  "$Version=1",
		"Authorization":            "Bearer leaked-token",
		"Proxy-Authorization":      "Basic abc",
		"X-Grafana-Id":             "id-token-value",
		"X-Grafana-Org-Id":         "1",
		"X-Grafana-Device-Id":      "device-xyz",
		"X-Forwarded-For":          "10.0.0.1",
		"X-Forwarded-Host":         "grafana.internal",
		"X-Forwarded-Proto":        "https",
		"X-Forwarded-Port":         "3000",
		"Forwarded":                "for=10.0.0.1;host=grafana.internal",
		"X-Real-Ip":                "10.0.0.1",
		"Referer":                  "https://grafana.internal/d/abc",
		"Origin":                   "https://grafana.internal",
		"Via":                      "1.1 grafana",
		"X-Forwarded-Client-Cert":  "By=spiffe://x;Hash=abc",
		"True-Client-IP":           "10.0.0.1",
		"Cf-Connecting-Ip":         "10.0.0.1",
		"Fastly-Client-Ip":         "10.0.0.1",
		"X-Client-Ip":              "10.0.0.1",
		"X-Cluster-Client-Ip":      "10.0.0.1",
		"X-Original-Forwarded-For": "10.0.0.1",
		"X-Original-Host":          "grafana.internal",
		"X-Original-Url":           "/d/abc",
	}
	for k, v := range inbound {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if received == nil {
		t.Fatal("upstream did not record any request headers")
	}

	// Every stripped category must be ABSENT on the upstream-received request.
	for _, name := range []string{
		"Cookie", "Cookie2", "Authorization", "Proxy-Authorization",
		"X-Grafana-Id", "X-Grafana-Org-Id", "X-Grafana-Device-Id",
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port",
		"Forwarded", "X-Real-Ip", "Referer", "Origin", "Via",
		"X-Forwarded-Client-Cert", "True-Client-IP", "Cf-Connecting-Ip", "Fastly-Client-Ip",
		"X-Client-Ip", "X-Cluster-Client-Ip", "X-Original-Forwarded-For", "X-Original-Host", "X-Original-Url",
	} {
		if v := received.Get(name); v != "" {
			t.Errorf("header %q should be stripped, upstream got %q", name, v)
		}
	}

	// Belt-and-braces: NO X-Grafana-* header of any kind survived the prefix sweep.
	for key := range received {
		if strings.HasPrefix(strings.ToLower(key), "x-grafana-") {
			t.Errorf("X-Grafana-* header %q leaked to upstream", key)
		}
	}

	// Conservative UA/Accept must be PRESENT with the expected values.
	if got := received.Get("User-Agent"); got != proxyUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, proxyUserAgent)
	}
	if got := received.Get("Accept"); got != proxyAccept {
		t.Errorf("Accept = %q, want %q", got, proxyAccept)
	}
}

// TestStripRequestHeadersUnit exercises stripRequestHeaders directly (no proxy),
// confirming the prefix sweep, exact-match deletions, and the overwrite (not
// append) semantics of the conservative UA/Accept even when inbound values exist.
func TestStripRequestHeadersUnit(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "x=1")
	h.Set("Authorization", "Bearer t")
	h.Set("X-Grafana-Anything", "v")
	h.Set("User-Agent", "Mozilla/5.0 (real browser)")
	h.Set("Accept", "application/json")
	h.Set("Accept-Language", "en") // not stripped: kept for a correct fetch

	stripRequestHeaders(h)

	for _, name := range []string{"Cookie", "Authorization", "X-Grafana-Anything"} {
		if v := h.Get(name); v != "" {
			t.Errorf("%q should be deleted, got %q", name, v)
		}
	}
	if got := h["User-Agent"]; len(got) != 1 || got[0] != proxyUserAgent {
		t.Errorf("User-Agent = %v, want exactly [%q]", got, proxyUserAgent)
	}
	if got := h["Accept"]; len(got) != 1 || got[0] != proxyAccept {
		t.Errorf("Accept = %v, want exactly [%q]", got, proxyAccept)
	}
	if got := h.Get("Accept-Language"); got != "en" {
		t.Errorf("Accept-Language should be preserved, got %q", got)
	}
}

// TestStripFramingHeadersStripsResponseHeadersUnit exercises stripFramingHeaders
// directly (no proxy) to confirm the P3 strip removes every dangerous incoming
// response header — including ALL values of a multi-valued Set-Cookie — while
// leaving a benign render-relevant header intact. It also returns no error.
func TestStripFramingHeadersStripsResponseHeadersUnit(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "sid=secret")
	resp.Header.Add("Set-Cookie", "track=abc")
	resp.Header.Set("Strict-Transport-Security", "max-age=63072000")
	resp.Header.Set("Public-Key-Pins", `pin-sha256="abc"`)
	resp.Header.Set("Public-Key-Pins-Report-Only", `pin-sha256="def"`)
	resp.Header.Set("Clear-Site-Data", `"cookies"`)
	resp.Header.Set("Content-Type", "text/html") // benign: must survive

	if err := stripFramingHeaders(resp); err != nil {
		t.Fatalf("stripFramingHeaders returned error: %v", err)
	}

	for _, name := range []string{
		"Set-Cookie",
		"Strict-Transport-Security",
		"Public-Key-Pins",
		"Public-Key-Pins-Report-Only",
		"Clear-Site-Data",
	} {
		if v := resp.Header.Values(name); len(v) != 0 {
			t.Errorf("%q should be deleted, got %v", name, v)
		}
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type should be preserved, got %q", got)
	}
}

// TestProxyResponseTooLargeContentLength covers Completion Criterion (P4):
// an upstream response whose declared Content-Length exceeds MaxResponseBytes
// is rejected with a CLEAN 413 before any body byte is streamed. The limit is
// set tiny (16 bytes) so the test allocates nothing large. enforceResponseSize
// returns errResponseTooLarge from ModifyResponse, which runs before the
// ReverseProxy writes status/headers, so the ErrorHandler emits 413.
func TestProxyResponseTooLargeContentLength(t *testing.T) {
	const oversized = "this body is definitely longer than sixteen bytes"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Set an explicit, honest Content-Length larger than the 16-byte limit.
		w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, oversized); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 16
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/big")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized Content-Length: got status %d, want 413 (body=%q)", rec.Code, rec.Body.String())
	}
	// The clean path must NOT have leaked the oversized upstream body.
	if strings.Contains(rec.Body.String(), oversized) {
		t.Fatalf("413 path leaked upstream body: %q", rec.Body.String())
	}
}

// TestProxyResponseTooLargeChunked covers the P4 defense-in-depth path: an
// upstream that does NOT declare Content-Length (chunked / -1) but streams more
// than MaxResponseBytes. DOCUMENTED BEHAVIOUR: because the ReverseProxy has
// already written 200 + headers before the body is read, this path CANNOT
// become a clean 413 — the limited reader caps the copy at the limit and errors,
// so the delivered body is truncated to at most MaxResponseBytes (and the copy
// fails). We assert the body is capped, never the full oversized payload.
func TestProxyResponseTooLargeChunked(t *testing.T) {
	const limit = 16
	// Build a body strictly larger than the limit. No Content-Length is set
	// (the handler writes without one and flushes => chunked), so resp.ContentLength
	// is -1 and only the limitedBody can catch the over-size.
	oversized := strings.Repeat("A", limit*4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Write in small chunks so the response is genuinely streamed.
		for i := 0; i < len(oversized); i += 4 {
			if _, err := io.WriteString(w, oversized[i:i+4]); err != nil {
				return // client may have hung up after the limit; not a test failure
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = limit
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/stream")
	// Status was already 200 before streaming began (documented caveat); the key
	// guarantee is that the delivered body is CAPPED at the limit, never the full
	// oversized payload.
	if int64(rec.Body.Len()) > limit {
		t.Fatalf("chunked oversize: delivered %d bytes, want <= %d (body must be capped)", rec.Body.Len(), limit)
	}
	if rec.Body.String() == oversized {
		t.Fatalf("chunked oversize: full body leaked, limited reader did not cap")
	}
}

// blockingRoundTripper is an http.RoundTripper that never returns a response: it
// blocks until the request context is cancelled, then returns ctx.Err(). This
// drives the total-request-budget (Q10) deadline deterministically without any
// real multi-second sleep — when the context.WithTimeout in serveProxy fires,
// the round-trip returns context.DeadlineExceeded.
type blockingRoundTripper struct{}

func (blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestProxyRequestTimeout covers Completion Criterion (P4): a request that
// exceeds the total per-request budget (RequestTimeoutSec, Q10 single budget)
// returns 504. We use a 1-second configured timeout and a transport that blocks
// on the context, so the deadline fires deterministically and the transport
// surfaces context.DeadlineExceeded, which proxyErrorHandler maps to 504.
func TestProxyRequestTimeout(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RequestTimeoutSec = 1 // smallest the int-seconds field allows post-defaulting
	p := newProxyHandler(cfg)
	p.transport = blockingRoundTripper{}

	start := time.Now()
	rec := doProxy(p, "http://example.com/slow")
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: got status %d, want 504 (body=%q)", rec.Code, rec.Body.String())
	}
	// Sanity: it must have actually waited for the budget, not failed instantly,
	// and must not have hung far beyond it.
	if elapsed < 900*time.Millisecond {
		t.Fatalf("timeout fired too early after %v, expected ~1s budget", elapsed)
	}
}

// TestProxyErrorHandlerResourceLimitMapping covers the P4 additions to
// proxyErrorHandler in isolation: errResponseTooLarge => 413 and
// context.DeadlineExceeded => 504, with CORS present on both.
func TestProxyErrorHandlerResourceLimitMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"too large => 413", errResponseTooLarge, http.StatusRequestEntityTooLarge},
		{"wrapped too large => 413", fmt.Errorf("copy failed: %w", errResponseTooLarge), http.StatusRequestEntityTooLarge},
		{"deadline => 504", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"wrapped deadline => 504", fmt.Errorf("round trip: %w", context.DeadlineExceeded), http.StatusGatewayTimeout},
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

// TestEnforceResponseSizeUnit exercises enforceResponseSize directly: a declared
// Content-Length over the limit returns errResponseTooLarge immediately; a body
// within or without a declared length is wrapped in a limitedBody that caps reads.
func TestEnforceResponseSizeUnit(t *testing.T) {
	t.Run("oversized content-length errors before streaming", func(t *testing.T) {
		resp := &http.Response{
			ContentLength: 100,
			Body:          io.NopCloser(strings.NewReader(strings.Repeat("x", 100))),
		}
		if err := enforceResponseSize(resp, 16); !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("want errResponseTooLarge, got %v", err)
		}
	})

	t.Run("undeclared oversize body is capped and errors", func(t *testing.T) {
		resp := &http.Response{
			ContentLength: -1, // chunked / unknown
			Body:          io.NopCloser(strings.NewReader(strings.Repeat("y", 64))),
		}
		if err := enforceResponseSize(resp, 16); err != nil {
			t.Fatalf("wrap step should not error up front, got %v", err)
		}
		got, err := io.ReadAll(resp.Body)
		if !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("reading oversize body: want errResponseTooLarge, got %v", err)
		}
		if int64(len(got)) > 16 {
			t.Fatalf("limitedBody delivered %d bytes, want <= 16", len(got))
		}
	})

	t.Run("within-limit body reads cleanly", func(t *testing.T) {
		const body = "small"
		resp := &http.Response{
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(strings.NewReader(body)),
		}
		if err := enforceResponseSize(resp, 16); err != nil {
			t.Fatalf("within-limit should not error, got %v", err)
		}
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading within-limit body: %v", err)
		}
		if string(got) != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
	})
}

// TestProxyReadsConfiguredLimit covers Completion Criterion (P4): a configured
// MaxResponseBytes overrides the default — a body that is UNDER the generous
// default but OVER a small configured limit is rejected with 413. (Default
// application itself is F3-tested; this confirms the proxy reads the configured
// value rather than a hardcoded default.)
func TestProxyReadsConfiguredLimit(t *testing.T) {
	const body = "0123456789ABCDEF0123456789" // 26 bytes: under 5 MiB default, over 16
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	// With the configured small limit: 413.
	cfgSmall := settingsWith(allowExample(DomainOptions{}))
	cfgSmall.MaxResponseBytes = 16
	if rec := doProxy(newTestHandler(t, cfgSmall, upstream), "http://example.com/x"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("configured small limit: got status %d, want 413", rec.Code)
	}

	// With the default (generous) limit: the same body passes through as 200.
	cfgDefault := settingsWith(allowExample(DomainOptions{}))
	if rec := doProxy(newTestHandler(t, cfgDefault, upstream), "http://example.com/x"); rec.Code != http.StatusOK {
		t.Fatalf("default limit: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
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

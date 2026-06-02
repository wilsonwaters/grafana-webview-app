package plugin

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

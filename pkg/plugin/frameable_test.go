package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// errDialRefused is the synthetic dial error used to simulate an upstream that
// passes the pipeline but is unreachable at connect time.
var errDialRefused = errors.New("connection refused")

// doCheckFrameable issues a GET /check-frameable?url=<target> against handler
// and returns the recorder. target is the RAW (unencoded) absolute URL; it is
// percent-encoded into the query string here exactly as a real caller would.
func doCheckFrameable(handler http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/check-frameable?url="+url.QueryEscape(target), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// decodeFrameable decodes the HTTP 200 JSON verdict body, failing the test if
// the status is not 200 or the body is not the expected contract shape.
func decodeFrameable(t *testing.T, rec *httptest.ResponseRecorder) frameableResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got frameableResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode verdict JSON: %v (body=%q)", err, rec.Body.String())
	}
	return got
}

// frameableHandler builds a checkFrameableHandler over a proxyHandler whose
// transport is swapped to dial the given upstream (the same pattern as
// newTestHandler), so the success-path tests exercise the full pipeline + fetch
// without any real network or DNS.
func frameableHandler(t *testing.T, cfg PluginSettings, upstream *httptest.Server) checkFrameableHandler {
	t.Helper()
	return checkFrameableHandler{p: newTestHandler(t, cfg, upstream)}
}

// --- Verdict / response-class tests (pipeline PASSES, HTTP 200) ---------------

// TestCheckFrameableNoHeadersDirect maps to Completion Criterion: a page with no
// framing restrictions is frameable => 200, recommendedMode "direct".
func TestCheckFrameableNoHeadersDirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if !got.Frameable || got.RecommendedMode != recommendedModeDirect {
		t.Fatalf("no headers: got %+v, want frameable=true mode=direct", got)
	}
}

// TestCheckFrameableXFrameOptionsDeny maps to Completion Criterion: X-Frame-Options
// DENY blocks framing => 200, frameable=false, recommendedMode "proxy".
func TestCheckFrameableXFrameOptionsDeny(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("XFO DENY: got %+v, want frameable=false mode=proxy", got)
	}
}

// TestCheckFrameableXFrameOptionsSameOrigin maps to Completion Criterion:
// X-Frame-Options SAMEORIGIN blocks third-party framing => 200, proxy.
func TestCheckFrameableXFrameOptionsSameOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "sameorigin") // case-insensitive
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("XFO SAMEORIGIN: got %+v, want frameable=false mode=proxy", got)
	}
}

// TestCheckFrameableCSPFrameAncestorsNone maps to Completion Criterion: CSP
// frame-ancestors 'none' blocks framing => 200, proxy.
func TestCheckFrameableCSPFrameAncestorsNone(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("CSP frame-ancestors none: got %+v, want frameable=false mode=proxy", got)
	}
}

// TestCheckFrameableCSPFrameAncestorsOriginList maps to Completion Criterion: a
// CSP frame-ancestors directive with a restrictive origin list (no `*`) is
// conservatively treated as blocking => 200, proxy.
func TestCheckFrameableCSPFrameAncestorsOriginList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-ancestors https://trusted.example https://other.example")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("CSP frame-ancestors origin list: got %+v, want frameable=false mode=proxy", got)
	}
}

// TestCheckFrameableCSPFrameAncestorsWildcard confirms a `*` source list permits
// any origin and is therefore NOT blocked by CSP => 200, direct.
func TestCheckFrameableCSPFrameAncestorsWildcard(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors *")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if !got.Frameable || got.RecommendedMode != recommendedModeDirect {
		t.Fatalf("CSP frame-ancestors wildcard: got %+v, want frameable=true mode=direct", got)
	}
}

// TestCheckFrameableRedirectRecommendsProxy confirms a 3xx (redirects NOT
// followed) yields no framing verdict for the final destination, so the verdict
// is proxy-recommended => 200, frameable=false.
func TestCheckFrameableRedirectRecommendsProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://example.com/elsewhere")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	h := frameableHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("redirect: got %+v, want frameable=false mode=proxy", got)
	}
}

// TestCheckFrameableUpstreamErrorRecommendsProxy maps to Completion Criterion:
// the pipeline passes but the dial fails; the ambiguous/error case is treated as
// proxy-recommended => 200, frameable=false (NOT a pipeline denial).
func TestCheckFrameableUpstreamErrorRecommendsProxy(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newProxyHandler(cfg)
	// Swap in a transport whose dial always fails, simulating an upstream that is
	// approved by the pipeline but unreachable at connect time.
	p.transport = &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Err: errDialRefused}
		},
	}
	h := checkFrameableHandler{p: p}

	got := decodeFrameable(t, doCheckFrameable(h, "http://example.com/page"))
	if got.Frameable || got.RecommendedMode != recommendedModeProxy {
		t.Fatalf("upstream error: got %+v, want frameable=false mode=proxy", got)
	}
	if got.Reason == "" {
		t.Errorf("upstream error: reason should name the failure, got empty")
	}
}

// --- Pipeline-denial tests (proxy-style HTTP error codes) ---------------------

// TestCheckFrameableEmptyAllowlistDenies maps to Completion Criterion: empty
// allowlist => 403, before any fetch.
func TestCheckFrameableEmptyAllowlistDenies(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(nil))}
	if rec := doCheckFrameable(h, "https://example.com/page"); rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist: got status %d, want 403", rec.Code)
	}
}

// TestCheckFrameableNonAllowlistedHost maps to Completion Criterion:
// non-allowlisted host => 403.
func TestCheckFrameableNonAllowlistedHost(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	if rec := doCheckFrameable(h, "https://evil.com/page"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted host: got status %d, want 403", rec.Code)
	}
}

// TestCheckFrameableBadScheme maps to Completion Criterion: bad scheme => 400.
func TestCheckFrameableBadScheme(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		if rec := doCheckFrameable(h, target); rec.Code != http.StatusBadRequest {
			t.Errorf("scheme %q: got status %d, want 400", target, rec.Code)
		}
	}
}

// TestCheckFrameableDisallowedPort maps to Completion Criterion: disallowed port => 400.
func TestCheckFrameableDisallowedPort(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	if rec := doCheckFrameable(h, "https://example.com:8443/page"); rec.Code != http.StatusBadRequest {
		t.Fatalf("disallowed port: got status %d, want 400", rec.Code)
	}
}

// TestCheckFrameableMissingURLParam maps to Completion Criterion: missing/empty
// url => 400.
func TestCheckFrameableMissingURLParam(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	for _, q := range []string{"/check-frameable", "/check-frameable?url=", "/check-frameable?url=%20"} {
		req := httptest.NewRequest(http.MethodGet, q, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: got status %d, want 400", q, rec.Code)
		}
	}
}

// TestCheckFrameableRateLimitExhaustion maps to Completion Criterion: rate-limit
// exhaustion => 429. The per-domain limit is 1/min so the second request is denied.
func TestCheckFrameableRateLimitExhaustion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RateLimitPerInstancePerMin = 100
	cfg.RateLimitPerDomainPerMin = 1
	h := frameableHandler(t, cfg, upstream)

	if rec := doCheckFrameable(h, "http://example.com/a"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200", rec.Code)
	}
	if rec := doCheckFrameable(h, "http://example.com/b"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (rate limited): got status %d, want 429", rec.Code)
	}
}

// TestCheckFrameableConcurrencyCapExhaustion confirms the concurrency tier:
// holding the only slot makes the request's Acquire fail => 429.
func TestCheckFrameableConcurrencyCapExhaustion(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxConcurrentRequests = 1
	p := newProxyHandler(cfg)

	release, ok := p.rateLimiter.Acquire()
	if !ok {
		t.Fatal("setup: expected to acquire the only slot")
	}
	defer release()

	h := checkFrameableHandler{p: p}
	if rec := doCheckFrameable(h, "https://example.com/page"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrency cap: got status %d, want 429", rec.Code)
	}
}

// TestCheckFrameableNonGETMethod confirms a non-GET method is rejected without
// running the pipeline (mirrors /proxy's method handling).
func TestCheckFrameableNonGETMethod(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	req := httptest.NewRequest(http.MethodPost, "/check-frameable?url="+url.QueryEscape("https://example.com/page"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got status %d, want 405", rec.Code)
	}
}

// TestCheckFrameableOptionsPreflight confirms OPTIONS returns 204 with CORS and
// does not run the pipeline (no url param required).
func TestCheckFrameableOptionsPreflight(t *testing.T) {
	h := checkFrameableHandler{p: newProxyHandler(settingsWith(allowExample(DomainOptions{})))}
	req := httptest.NewRequest(http.MethodOptions, "/check-frameable", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS: got status %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("OPTIONS: Access-Control-Allow-Origin = %q, want *", got)
	}
}

// --- Pure header-parse unit coverage -----------------------------------------

// TestXFrameOptionsBlocks covers the X-Frame-Options classification directly.
func TestXFrameOptionsBlocks(t *testing.T) {
	cases := []struct {
		value   string
		blocked bool
	}{
		{"DENY", true},
		{" deny ", true},
		{"SAMEORIGIN", true},
		{"sameorigin", true},
		{"", false},
		{"ALLOW-FROM https://x", false},
	}
	for _, c := range cases {
		if blocked, _ := xFrameOptionsBlocks(c.value); blocked != c.blocked {
			t.Errorf("xFrameOptionsBlocks(%q) = %v, want %v", c.value, blocked, c.blocked)
		}
	}
}

// TestCSPFrameAncestorsBlocks covers the conservative CSP frame-ancestors rule.
func TestCSPFrameAncestorsBlocks(t *testing.T) {
	cases := []struct {
		csp     string
		blocked bool
	}{
		{"frame-ancestors 'none'", true},
		{"default-src 'self'; frame-ancestors 'none'", true},
		{"frame-ancestors", true}, // bare directive == 'none'
		{"frame-ancestors https://a https://b", true},
		{"frame-ancestors 'self'", true},
		{"frame-ancestors *", false},
		{"default-src 'self'; FRAME-ANCESTORS *", false}, // case-insensitive directive name
		{"default-src 'self'", false},                    // no frame-ancestors directive
		{"", false},
	}
	for _, c := range cases {
		if blocked, _ := cspFrameAncestorsBlocks(c.csp); blocked != c.blocked {
			t.Errorf("cspFrameAncestorsBlocks(%q) = %v, want %v", c.csp, blocked, c.blocked)
		}
	}
}

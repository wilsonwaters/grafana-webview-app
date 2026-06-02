package plugin

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// doProxyResource issues a GET /proxy-resource?url=<target> against handler and
// returns the recorder. target is the RAW (unencoded) absolute URL; it is
// percent-encoded into the query string here exactly as CR2's rewritten HTML
// emits it. It mirrors doProxy so the two endpoints are exercised identically
// apart from the path.
func doProxyResource(handler http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, proxyResourcePath+"?url="+url.QueryEscape(target), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// newResourceHandler builds the /proxy-resource handler over a test proxyHandler
// wired to the given stub upstream (same transport swap as newTestHandler).
func newResourceHandler(t *testing.T, cfg PluginSettings, upstream *httptest.Server) proxyResourceHandler {
	t.Helper()
	return proxyResourceHandler{p: newTestHandler(t, cfg, upstream)}
}

// TestProxyResourceAllowlistedCSS covers Completion Criterion: allowed
// subresource success path — the upstream body is returned AND the upstream
// Content-Type (text/css) is PRESERVED (no rewriting, no HTML content type).
func TestProxyResourceAllowlistedCSS(t *testing.T) {
	const body = "body { color: red; }"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/styles.css" {
			t.Errorf("upstream got path %q, want /styles.css", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/styles.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("css subresource: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("css subresource: got body %q, want %q (must pass through unrewritten)", got, body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Fatalf("css subresource: Content-Type = %q, want it PRESERVED as text/css; charset=utf-8", ct)
	}
}

// TestProxyResourcePreservesContentType covers Completion Criterion: the
// upstream Content-Type is preserved across a representative set of subresource
// media types (CSS, JS, image), confirming the endpoint never coerces to
// text/html the way /proxy's HTML rewrite does.
func TestProxyResourcePreservesContentType(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/app.js", "application/javascript", "console.log('hi')"},
		{"/styles.css", "text/css", "a{color:blue}"},
		{"/pixel.png", "image/png", "\x89PNG\r\n\x1a\n binary-ish"},
	}
	for _, c := range cases {
		t.Run(c.contentType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", c.contentType)
				if _, err := io.WriteString(w, c.body); err != nil {
					t.Errorf("upstream write: %v", err)
				}
			}))
			defer upstream.Close()

			cfg := settingsWith(allowExample(DomainOptions{}))
			h := newResourceHandler(t, cfg, upstream)

			rec := doProxyResource(h, "http://example.com"+c.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got status %d, want 200", c.contentType, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != c.contentType {
				t.Fatalf("%s: Content-Type = %q, want it preserved", c.contentType, got)
			}
			if got := rec.Body.String(); got != c.body {
				t.Fatalf("%s: body = %q, want %q unchanged", c.contentType, got, c.body)
			}
		})
	}
}

// TestProxyResourceGzipPassthrough covers the gzip-passthrough contract: since
// CR1 pins Accept-Encoding: gzip, a gzip subresource arrives compressed. The
// subresource endpoint does NOT decode it — the Content-Encoding: gzip header
// and the compressed bytes pass straight through so the browser decompresses.
func TestProxyResourceGzipPassthrough(t *testing.T) {
	const css = "body { color: red; }"
	gz := gzipBytes(t, []byte(css))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(gz); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/styles.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("gzip subresource: got status %d, want 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("gzip subresource: Content-Encoding = %q, want it preserved as gzip (passthrough)", ce)
	}
	if got, want := rec.Body.Bytes(), gz; !bytes.Equal(got, want) {
		t.Fatalf("gzip subresource: body was decoded/altered; want the compressed bytes passed through unchanged")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Fatalf("gzip subresource: Content-Type = %q, want text/css preserved", ct)
	}
}

// TestProxyResourceEmptyAllowlistDenies covers Completion Criterion: empty
// allowlist => 403 (fail-closed), enforced identically to /proxy.
func TestProxyResourceEmptyAllowlistDenies(t *testing.T) {
	cfg := settingsWith(nil)
	h := proxyResourceHandler{p: newProxyHandler(cfg)} // no upstream needed
	rec := doProxyResource(h, "https://example.com/styles.css")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist: got status %d, want 403", rec.Code)
	}
}

// TestProxyResourceNonAllowlistedHost covers Completion Criterion: a host not in
// the allowlist => 403.
func TestProxyResourceNonAllowlistedHost(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	h := proxyResourceHandler{p: newProxyHandler(cfg)}
	rec := doProxyResource(h, "https://evil.com/styles.css")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted host: got status %d, want 403", rec.Code)
	}
}

// TestProxyResourceBadScheme covers Completion Criterion: a non-http(s) scheme
// => 400 (SF2), identically to /proxy.
func TestProxyResourceBadScheme(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	h := proxyResourceHandler{p: newProxyHandler(cfg)}
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		rec := doProxyResource(h, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("scheme %q: got status %d, want 400", target, rec.Code)
		}
	}
}

// TestProxyResourceMissingURL covers Completion Criterion: a missing/blank url
// query param => 400.
func TestProxyResourceMissingURL(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	h := proxyResourceHandler{p: newProxyHandler(cfg)}
	req := httptest.NewRequest(http.MethodGet, proxyResourcePath, nil) // no url param
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url: got status %d, want 400", rec.Code)
	}
}

// TestProxyResourceRateLimited covers Completion Criterion: the SF5 per-instance
// rate tier denies with 429, identically to /proxy.
func TestProxyResourceRateLimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		if _, err := io.WriteString(w, "x"); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RateLimitPerInstancePerMin = 1 // allow exactly one request, deny the second
	h := newResourceHandler(t, cfg, upstream)

	if rec := doProxyResource(h, "http://example.com/a.css"); rec.Code != http.StatusOK {
		t.Fatalf("first subresource request: got status %d, want 200", rec.Code)
	}
	if rec := doProxyResource(h, "http://example.com/b.css"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second subresource request: got status %d, want 429", rec.Code)
	}
}

// TestProxyResourceSizeLimited covers Completion Criterion: an oversized
// subresource (declared Content-Length over MaxResponseBytes) => 413, reusing
// P4's enforceResponseSize.
func TestProxyResourceSizeLimited(t *testing.T) {
	big := strings.Repeat("a", 4096)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Length", strconv.Itoa(len(big)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, big); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 1024 // smaller than the body
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/big.css")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized subresource: got status %d, want 413", rec.Code)
	}
}

// TestProxyResourceStripsResponseHeaders covers Completion Criterion: the same
// P3 response-header strip applies — a Set-Cookie from the upstream is removed
// — and the framing headers (X-Frame-Options) are stripped, identically to /proxy.
func TestProxyResourceStripsResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Add("Set-Cookie", "sid=secret")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000")
		if _, err := io.WriteString(w, "a{}"); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/x.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("header-strip subresource: got status %d, want 200", rec.Code)
	}
	if v := rec.Header().Get("Set-Cookie"); v != "" {
		t.Errorf("Set-Cookie = %q, want it stripped", v)
	}
	if v := rec.Header().Get("X-Frame-Options"); v != "" {
		t.Errorf("X-Frame-Options = %q, want it stripped", v)
	}
	if v := rec.Header().Get("Strict-Transport-Security"); v != "" {
		t.Errorf("Strict-Transport-Security = %q, want it stripped", v)
	}
}

// TestProxyResourceCORSPresent covers Completion Criterion: the same permissive
// CORS headers are set on /proxy-resource responses.
func TestProxyResourceCORSPresent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		if _, err := io.WriteString(w, "a{}"); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/x.css")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS Access-Control-Allow-Origin = %q, want *", got)
	}
}

// TestProxyResourceStripsRequestIdentityHeaders covers Completion Criterion: the
// same P2 outgoing-request header policy applies — inbound auth/identity headers
// (Cookie, Authorization, X-Grafana-*) never reach the upstream, and the
// Accept-Encoding: gzip pin is set.
func TestProxyResourceStripsRequestIdentityHeaders(t *testing.T) {
	var gotHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/css")
		if _, err := io.WriteString(w, "a{}"); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	req := httptest.NewRequest(http.MethodGet, proxyResourcePath+"?url="+url.QueryEscape("http://example.com/x.css"), nil)
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("Authorization", "Bearer leak")
	req.Header.Set("X-Grafana-Id", "user-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("request-strip subresource: got status %d, want 200", rec.Code)
	}
	for _, k := range []string{"Cookie", "Authorization", "X-Grafana-Id"} {
		if v := gotHeaders.Get(k); v != "" {
			t.Errorf("upstream saw %s = %q, want it stripped", k, v)
		}
	}
	if ae := gotHeaders.Get("Accept-Encoding"); ae != contentEncodingGzip {
		t.Errorf("upstream Accept-Encoding = %q, want gzip pin", ae)
	}
}

// TestProxyResourceMethodNotAllowed confirms the shared method gate applies:
// a non-GET method => 405, identically to /proxy.
func TestProxyResourceMethodNotAllowed(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	h := proxyResourceHandler{p: newProxyHandler(cfg)}
	req := httptest.NewRequest(http.MethodPost, proxyResourcePath+"?url="+url.QueryEscape("http://example.com/x.css"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST subresource: got status %d, want 405", rec.Code)
	}
}

// TestProxyVsProxyResourceRewriteRegression is the regression assert that /proxy
// still HTML-rewrites while /proxy-resource does NOT. The SAME HTML body is
// served through both endpoints: /proxy injects a <base> tag and rewrites the
// link (so the body changes and Content-Type is coerced to text/html), whereas
// /proxy-resource passes the identical HTML bytes through unchanged with the
// upstream Content-Type preserved.
func TestProxyVsProxyResourceRewriteRegression(t *testing.T) {
	const html = `<!doctype html><html><head><link rel="stylesheet" href="/styles.css"></head><body>hi</body></html>`
	newUpstream := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := io.WriteString(w, html); err != nil {
				t.Errorf("upstream write: %v", err)
			}
		}))
	}
	cfg := settingsWith(allowExample(DomainOptions{}))

	// /proxy: HTML is rewritten — body differs and gains a /proxy-resource ref.
	up1 := newUpstream()
	defer up1.Close()
	proxyRec := doProxy(newTestHandler(t, cfg, up1), "http://example.com/page")
	if proxyRec.Code != http.StatusOK {
		t.Fatalf("/proxy: got status %d, want 200", proxyRec.Code)
	}
	proxyBody := proxyRec.Body.String()
	if proxyBody == html {
		t.Fatalf("/proxy: body was NOT rewritten (regression) — got the upstream HTML verbatim")
	}
	if !strings.Contains(proxyBody, resourceBase+proxyResourcePath) {
		t.Fatalf("/proxy: rewritten HTML must reference %s; got %q", resourceBase+proxyResourcePath, proxyBody)
	}

	// /proxy-resource: the SAME HTML is passed through verbatim, Content-Type
	// preserved (NOT coerced to a rewritten text/html; charset=utf-8 body).
	up2 := newUpstream()
	defer up2.Close()
	resRec := doProxyResource(newResourceHandler(t, cfg, up2), "http://example.com/page")
	if resRec.Code != http.StatusOK {
		t.Fatalf("/proxy-resource: got status %d, want 200", resRec.Code)
	}
	if got := resRec.Body.String(); got != html {
		t.Fatalf("/proxy-resource: body was rewritten (regression). got %q, want the verbatim upstream HTML", got)
	}
	if ct := resRec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("/proxy-resource: Content-Type = %q, want the upstream value preserved", ct)
	}
}

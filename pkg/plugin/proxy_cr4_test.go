package plugin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// CR4 — Redirect handling tests.
//
// These exercise the browser-driven re-entry design: the proxy does NOT follow
// redirects server-side. A 3xx response has its Location rewritten (in
// handleRedirect, via ModifyResponse) to a proxy URL carrying the absolute
// next-hop target and the incremented _wvredir depth, so the browser re-enters
// the proxy where the full pipeline re-validates the hop. Over-depth and
// non-allowlisted hops are refused AT the redirect step (no followable Location
// emitted). All assertions go through an httptest recorder against a stub
// upstream that returns 30x + Location, in the house style of proxy_cr3_test.go.

// doProxyDepth issues GET /proxy?url=<target>&_wvredir=<depth> and returns the
// recorder. It mirrors doProxy but threads the reserved redirect-depth control
// param so tests can simulate the browser arriving N hops deep.
func doProxyDepth(handler http.Handler, target string, depth string) *httptest.ResponseRecorder {
	q := url.Values{}
	q.Set("url", target)
	q.Set(wvRedirParam, depth)
	req := httptest.NewRequest(http.MethodGet, proxyPath+"?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// redirectUpstream returns a stub upstream that answers every request with the
// given 3xx status and Location header (and a tiny inert body).
func redirectUpstream(t *testing.T, status int, location string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		// 304 (and other bodiless statuses) must not carry a body; only write the
		// inert courtesy body for statuses that allow one.
		if status != http.StatusNotModified && status != http.StatusNoContent {
			if _, err := io.WriteString(w, "redirecting"); err != nil {
				t.Errorf("upstream write: %v", err)
			}
		}
	}))
}

// TestRedirectToAllowlistedHostRewritesLocation covers Completion Criteria:
// "Location headers rewritten to proxy URLs" + "each redirect hop re-validated".
// A 302 to an allowlisted host has Location rewritten to /proxy?url=<enc>&_wvredir=1.
func TestRedirectToAllowlistedHostRewritesLocation(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/start")
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect: got status %d, want 302 (body=%q)", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	wantPrefix := resourceBase + proxyPath + "?"
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Fatalf("Location = %q, want a /proxy URL prefixed %q", loc, wantPrefix)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse rewritten Location %q: %v", loc, err)
	}
	if got := u.Query().Get("url"); got != "http://example.com/next" {
		t.Fatalf("rewritten url param = %q, want the absolute next hop", got)
	}
	if got := u.Query().Get(wvRedirParam); got != "1" {
		t.Fatalf("rewritten %s = %q, want 1 (depth incremented)", wvRedirParam, got)
	}
}

// TestRedirectRelativeLocationResolvedAgainstTarget confirms a RELATIVE Location
// is resolved against the current hop's target before being rewritten — part of
// per-hop re-validation (the absolute next-hop target must be well-formed).
func TestRedirectRelativeLocationResolvedAgainstTarget(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusMovedPermanently, "/deeper/page")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/a/b")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("relative redirect: got status %d, want 301", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get("url"); got != "http://example.com/deeper/page" {
		t.Fatalf("resolved url param = %q, want absolute http://example.com/deeper/page", got)
	}
}

// TestRedirectToNonAllowlistedHostBlocked covers Completion Criterion: a redirect
// into a denied destination is blocked BEFORE following — at the redirect step.
// The response is a 403 and Location is NOT a followable proxy URL.
func TestRedirectToNonAllowlistedHostBlocked(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "https://evil.com/landing")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/start")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("redirect to non-allowlisted host: got status %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "evil.com") || strings.Contains(loc, proxyPath+"?") {
		t.Fatalf("Location = %q, want NO followable proxy URL to the denied host", loc)
	}
}

// TestRedirectDepthCapExceeded covers Completion Criterion: requests exceeding
// the redirect depth cap return an error. Arriving already at depth == MaxRedirects
// (default 3) trips the cap on the next 3xx; the response is a 502 redirect-loop
// error and no followable Location is emitted.
func TestRedirectDepthCapExceeded(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{})) // MaxRedirects = 3
	h := newTestHandler(t, cfg, upstream)

	rec := doProxyDepth(h, "http://example.com/start", "3") // depth == cap
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("depth-cap exceeded: got status %d, want 502 (body=%q)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, proxyPath+"?") {
		t.Fatalf("Location = %q, want NO followable proxy URL once the cap is reached", loc)
	}
}

// TestRedirectDepthBelowCapStillFollows confirms the boundary: at depth one below
// the cap the redirect is still rewritten (handled), so the cap is exclusive of
// the final permitted hop — depth 2 with cap 3 rewrites to _wvredir=3.
func TestRedirectDepthBelowCapStillFollows(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{})) // MaxRedirects = 3
	h := newTestHandler(t, cfg, upstream)

	rec := doProxyDepth(h, "http://example.com/start", "2")
	if rec.Code != http.StatusFound {
		t.Fatalf("depth below cap: got status %d, want 302", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get(wvRedirParam); got != "3" {
		t.Fatalf("%s = %q, want 3", wvRedirParam, got)
	}
}

// TestRedirectMaxRedirectsZeroDisables covers Completion Criterion / constraint:
// MaxRedirects == 0 disables redirects — the very first 3xx (depth 0) trips the
// cap and errors with 502 rather than emitting a followable Location.
func TestRedirectMaxRedirectsZeroDisables(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxRedirects = 0 // redirects disabled
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/start") // depth 0
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("MaxRedirects=0: got status %d, want 502 (redirects disabled)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, proxyPath+"?") {
		t.Fatalf("Location = %q, want NO followable proxy URL when redirects are disabled", loc)
	}
}

// TestRedirectResourceEndpointRewritesToProxyResource covers Completion Criterion:
// a redirect on the /proxy-resource endpoint is rewritten to a /proxy-resource URL
// (so a redirected subresource stays proxied as a subresource).
func TestRedirectResourceEndpointRewritesToProxyResource(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/real.css")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	rec := doProxyResource(h, "http://example.com/styles.css")
	if rec.Code != http.StatusFound {
		t.Fatalf("resource redirect: got status %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	wantPrefix := resourceBase + proxyResourcePath + "?"
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Fatalf("Location = %q, want a /proxy-resource URL prefixed %q", loc, wantPrefix)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get("url"); got != "http://example.com/real.css" {
		t.Fatalf("rewritten url param = %q, want the absolute next hop", got)
	}
	if got := u.Query().Get(wvRedirParam); got != "1" {
		t.Fatalf("rewritten %s = %q, want 1", wvRedirParam, got)
	}
}

// TestRedirectNonHTTPLocationNotRewritten covers Completion Criterion: a 3xx whose
// Location resolves to a non-http(s) scheme (data:, mailto:) is NOT rewritten into
// a proxy URL — it is passed through as-is (not turned into a followable proxy URL).
func TestRedirectNonHTTPLocationNotRewritten(t *testing.T) {
	for _, loc := range []string{"mailto:someone@example.com", "data:text/plain,hi"} {
		t.Run(loc, func(t *testing.T) {
			upstream := redirectUpstream(t, http.StatusFound, loc)
			defer upstream.Close()

			cfg := settingsWith(allowExample(DomainOptions{}))
			h := newTestHandler(t, cfg, upstream)

			rec := doProxy(h, "http://example.com/start")
			if rec.Code != http.StatusFound {
				t.Fatalf("%s: got status %d, want 302 passed through", loc, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != loc {
				t.Fatalf("%s: Location = %q, want it left as-is (not rewritten to a proxy URL)", loc, got)
			}
		})
	}
}

// TestRedirectUnparseableLocationStripped covers the edge case: a 3xx whose
// Location is unparseable cannot be turned into a followable proxy URL, so it is
// stripped and the (now Location-less) redirect is passed through rather than
// emitting something the browser would chase to a non-proxied destination.
func TestRedirectUnparseableLocationStripped(t *testing.T) {
	// A bare "%" is an invalid percent-escape that url.Parse rejects.
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/%zz%")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/start")
	if rec.Code != http.StatusFound {
		t.Fatalf("unparseable Location: got status %d, want 302 passed through", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want it stripped (unparseable, not rewritten)", loc)
	}
}

// TestRedirectControlParamNotForwardedUpstream covers the constraint: the internal
// _wvredir control param lives only on the proxy URL and must NEVER be forwarded to
// the upstream request (the outbound URL is rebuilt from the `url` target alone).
func TestRedirectControlParamNotForwardedUpstream(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/html")
		if _, err := io.WriteString(w, "<html></html>"); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	// Arrive two hops deep with a target that has its own query string.
	rec := doProxyDepth(h, "http://example.com/page?q=1", "2")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if strings.Contains(gotQuery, wvRedirParam) {
		t.Fatalf("upstream saw query %q containing %s; the control param must not be forwarded", gotQuery, wvRedirParam)
	}
	if gotQuery != "q=1" {
		t.Fatalf("upstream query = %q, want only the target's own q=1", gotQuery)
	}
}

// TestRedirectBogusDepthParamTreatedAsZero covers the constraint that a bogus /
// non-numeric _wvredir cannot break the pipeline: it parses to 0, so a first-hop
// redirect (cap 3) is still rewritten to _wvredir=1.
func TestRedirectBogusDepthParamTreatedAsZero(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxyDepth(h, "http://example.com/start", "not-a-number")
	if rec.Code != http.StatusFound {
		t.Fatalf("bogus depth: got status %d, want 302 (treated as depth 0)", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get(wvRedirParam); got != "1" {
		t.Fatalf("%s = %q, want 1 (bogus depth parsed as 0)", wvRedirParam, got)
	}
}

// TestNonRedirectPassthroughUnchanged is the regression guard that CR4 does not
// disturb the non-redirect paths: a 200 HTML response still flows through the CR2
// rewrite (body changes, Content-Type coerced), unaffected by handleRedirect.
func TestNonRedirectPassthroughUnchanged(t *testing.T) {
	const html = `<!doctype html><html><head><link rel="stylesheet" href="/s.css"></head><body>hi</body></html>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := io.WriteString(w, html); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)

	rec := doProxy(h, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("non-redirect 200: got status %d, want 200", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Fatalf("non-redirect response unexpectedly carries a Location header")
	}
	if !strings.Contains(rec.Body.String(), resourceBase+proxyResourcePath) {
		t.Fatalf("non-redirect HTML was not CR2-rewritten (regression)")
	}
}

// TestRedirectStatusCodesAllHandled confirms every Location-bearing 3xx code
// (301/302/303/307/308) is rewritten, while a 304 (not a single-target redirect)
// passes through with its Location untouched.
func TestRedirectStatusCodesAllHandled(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		upstream := redirectUpstream(t, status, "http://example.com/next")
		cfg := settingsWith(allowExample(DomainOptions{}))
		h := newTestHandler(t, cfg, upstream)
		rec := doProxy(h, "http://example.com/start")
		if rec.Code != status {
			t.Errorf("status %d: got %d, want it preserved", status, rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Location"), resourceBase+proxyPath+"?") {
			t.Errorf("status %d: Location not rewritten to a proxy URL", status)
		}
		upstream.Close()
	}

	// 304 Not Modified: not a redirect we rewrite — pass through untouched.
	upstream := redirectUpstream(t, http.StatusNotModified, "http://example.com/next")
	defer upstream.Close()
	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newTestHandler(t, cfg, upstream)
	rec := doProxy(h, "http://example.com/start")
	if rec.Code != http.StatusNotModified {
		t.Fatalf("304: got status %d, want 304 passed through", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "http://example.com/next" {
		t.Fatalf("304: Location = %q, want it left untouched", got)
	}
}

package plugin

// Non-HTML responses through /proxy, and the removal of /proxy-resource (FR5).
//
// The tests here were moved from the /proxy-resource suite (CR3) when that
// route was removed: the behaviours they cover (Content-Type and body
// passthrough for non-HTML, gzip passthrough, hide-selectors ignored for
// non-HTML) are shared with /proxy. The /proxy-resource pipeline tests that
// duplicated existing /proxy tests were dropped with the route; their /proxy
// equivalents are TestProxyEmptyAllowlistDenies, TestProxyNonAllowlistedHost,
// TestProxyBadScheme, TestProxyMissingURLParam, TestProxyRateLimitExhaustion,
// TestProxyResponseTooLargeContentLength, TestProxyStripsResponseHeaders,
// TestProxyStripsRequestHeaders and TestDenialMatrixStatusAndMetricReason.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// TestProxyNonHTMLPreservesContentType covers the non-HTML passthrough: the
// upstream Content-Type and body of CSS, JS, JSON and image responses are
// returned unchanged (never coerced to a rewritten text/html body).
func TestProxyNonHTMLPreservesContentType(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/app.js", "application/javascript", "console.log('hi')"},
		{"/styles.css", "text/css; charset=utf-8", "a{color:blue}"},
		{"/data.json", "application/json", `{"html":"<a href=\"/x\">x</a>"}`},
		{"/pixel.png", "image/png", "\x89PNG\r\n\x1a\n binary-ish"},
	}
	for _, c := range cases {
		t.Run(c.contentType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.path {
					t.Errorf("upstream got path %q, want %q", r.URL.Path, c.path)
				}
				w.Header().Set("Content-Type", c.contentType)
				_, _ = io.WriteString(w, c.body)
			}))
			defer upstream.Close()

			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
			rec := doProxy(p, "http://example.com"+c.path)
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

// TestProxyNonHTMLGzipPassthrough covers the gzip contract for non-HTML: the
// compressed bytes and Content-Encoding: gzip reach the client unchanged.
func TestProxyNonHTMLGzipPassthrough(t *testing.T) {
	const css = "body { color: red; }"
	gz := gzipBytes(t, []byte(css))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(gz)
	}))
	defer upstream.Close()

	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	rec := doProxy(p, "http://example.com/styles.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("gzip non-HTML: got status %d, want 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("gzip non-HTML: Content-Encoding = %q, want gzip", ce)
	}
	if !bytes.Equal(rec.Body.Bytes(), gz) {
		t.Fatal("gzip non-HTML: body was decoded or altered; want the compressed bytes unchanged")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Fatalf("gzip non-HTML: Content-Type = %q, want text/css", ct)
	}
}

// TestProxyNonHTMLIgnoresHide covers CR5 for non-HTML: a `hide` param never
// alters a non-HTML body, even one that textually contains the selector.
func TestProxyNonHTMLIgnoresHide(t *testing.T) {
	const css = `.ad { color: red; }`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(css))
	}))
	defer upstream.Close()

	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	rec := doProxyHide(p, "http://example.com/styles.css", ".ad")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != css {
		t.Errorf("non-HTML body altered by hide param: got %q, want %q", body, css)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
}

// TestProxyResourceRouteRemoved covers FR5 / D5: /proxy-resource is not
// registered, so every request to it returns 404 from the resource mux and
// never reaches the proxy pipeline or the upstream.
func TestProxyResourceRouteRemoved(t *testing.T) {
	app := newTestApp(t)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
		var r mockCallResourceResponseSender
		err := app.CallResource(context.Background(), &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{User: &backend.User{Login: "admin", Role: "Admin"}},
			Method:        method,
			Path:          "proxy-resource",
			URL:           "proxy-resource?url=" + url.QueryEscape("https://example.com/style.css"),
		}, &r)
		if err != nil {
			t.Fatalf("%s: CallResource: %v", method, err)
		}
		if r.response == nil || r.response.Status != http.StatusNotFound {
			t.Fatalf("%s /proxy-resource: got %+v, want 404", method, r.response)
		}
	}
}

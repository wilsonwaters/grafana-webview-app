package plugin

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// cr1PageURL is a representative validated page URL passed to prepareHTMLBody in
// the CR1 unit tests. The CR1 tests assert decode/passthrough behaviour; the
// rewrite step (CR2) is covered separately in rewrite_test.go.
func cr1PageURL(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("http://example.com/page")
	if err != nil {
		t.Fatalf("parse page URL: %v", err)
	}
	return u
}

// gzipBytes gzip-compresses b for building upstream fixtures.
func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(b); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// CR1 Completion Criterion: "Gzip responses decompressed before rewriting" +
// "framing headers still stripped". A gzip-encoded text/html response is
// decompressed in ModifyResponse — the caller receives the ORIGINAL HTML bytes,
// Content-Encoding is removed so the client does not re-decode, and the P1/P3
// framing/response-header strip still ran (X-Frame-Options gone, Set-Cookie gone).
func TestCR1GzipHTMLDecompressed(t *testing.T) {
	const html = "<!doctype html><html><head><title>hi</title></head><body>hello</body></html>"
	gz := gzipBytes(t, []byte(html))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Add("Set-Cookie", "sid=secret")
		w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(gz); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("gzip HTML: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	// CR2: the decoded HTML is rewritten (base injected); the original text and
	// title must survive the decode+rewrite round-trip.
	if got := rec.Body.String(); !strings.Contains(got, "hello") || !strings.Contains(got, "<title>hi</title>") {
		t.Fatalf("gzip HTML: body = %q, want decoded content preserved", got)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("gzip HTML: Content-Encoding = %q, want removed", ce)
	}
	// P1/P3 framing strip must still have run on the decoded HTML response.
	if v := rec.Header().Get("X-Frame-Options"); v != "" {
		t.Errorf("framing strip regressed: X-Frame-Options = %q, want removed", v)
	}
	if v := rec.Header().Values("Set-Cookie"); len(v) != 0 {
		t.Errorf("response-header strip regressed: Set-Cookie = %v, want removed", v)
	}
}

// CR2 (supersedes the CR1 plain-HTML-passthrough expectation): plain (non-gzip)
// HTML is now rewritten too — CR2 runs goquery on ALL HTML, not just gzip HTML.
// The body's text content must survive the rewrite and the response must be 200.
func TestCR1PlainHTMLPassthrough(t *testing.T) {
	const html = "<html><body>plain</body></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := io.WriteString(w, html); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("plain HTML: got status %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "plain") {
		t.Fatalf("plain HTML: body = %q, want decoded content preserved", got)
	}
}

// CR1 Completion Criterion: "Non-HTML bodies pass through unmodified" /
// "HTML vs non-HTML correctly distinguished". A gzip-encoded NON-HTML response
// (application/json) must be passed through COMPLETELY UNCHANGED — the gzipped
// bytes and the Content-Encoding header are preserved (the proxy does NOT decode
// non-HTML). Driven at the unit level on prepareHTMLBody so we can assert the
// exact body bytes the upstream sent survive byte-for-byte.
func TestCR1NonHTMLGzipUntouched(t *testing.T) {
	const json = `{"hello":"world"}`
	gz := gzipBytes(t, []byte(json))

	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(gz)),
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.ContentLength = int64(len(gz))

	if err := prepareHTMLBody(resp, 1<<20, cr1PageURL(t), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody(non-HTML gzip): %v", err)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("non-HTML gzip: Content-Encoding = %q, want preserved gzip", ce)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, gz) {
		t.Errorf("non-HTML gzip: body was modified, want byte-identical gzipped payload")
	}
}

// CR1: a non-gzip non-HTML response (image/png) is likewise untouched — confirms
// the content-type gate, not just the encoding gate, leaves the body intact.
func TestCR1NonHTMLPlainUntouched(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}

	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(png)),
	}
	resp.Header.Set("Content-Type", "image/png")
	resp.ContentLength = int64(len(png))

	if err := prepareHTMLBody(resp, 1<<20, cr1PageURL(t), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody(non-HTML plain): %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, png) {
		t.Errorf("non-HTML plain: body was modified, want byte-identical bytes")
	}
}

// CR1 SECURITY (gzip-bomb guard): a gzip stream whose DECODED size exceeds the
// configured MaxResponseBytes must fail with errResponseTooLarge → 413, without
// buffering the whole decoded stream. Build a highly-compressible HTML body that
// decodes far larger than a tiny limit; assert prepareHTMLBody returns the
// errResponseTooLarge sentinel (proxyErrorHandler maps that to 413).
func TestCR1GzipBombBounded(t *testing.T) {
	// Decodes to 64 KiB of highly-compressible HTML; the gzipped form is tiny so
	// the test itself allocates almost nothing.
	bigHTML := "<html><body>" + strings.Repeat("A", 64*1024) + "</body></html>"
	gz := gzipBytes(t, []byte(bigHTML))

	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(gz)),
	}
	resp.Header.Set("Content-Type", "text/html")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.ContentLength = int64(len(gz)) // small compressed length passes the wire guard

	const limit = 1024 // decoded body (64 KiB+) far exceeds this
	err := prepareHTMLBody(resp, limit, cr1PageURL(t), nil, nil)
	if err == nil {
		t.Fatalf("gzip bomb: prepareHTMLBody returned nil, want errResponseTooLarge")
	}
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("gzip bomb: err = %v, want errResponseTooLarge", err)
	}
}

// TestCR1GzipBombThrough413 drives the bomb end-to-end through the proxy to
// confirm the sentinel reaches proxyErrorHandler and produces a clean 413.
func TestCR1GzipBombThrough413(t *testing.T) {
	bigHTML := "<html><body>" + strings.Repeat("A", 64*1024) + "</body></html>"
	gz := gzipBytes(t, []byte(bigHTML))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(gz)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(gz); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 1024 // smaller than the decoded size, larger than the gzipped size
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/bomb")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("gzip bomb e2e: got status %d, want 413 (body=%q)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "AAAA") {
		t.Fatalf("gzip bomb e2e: 413 path leaked decoded body")
	}
}

// CR1: a malformed gzip stream on a text/html response must be handled
// gracefully — prepareHTMLBody returns a (non-sentinel) error rather than
// panicking, and end-to-end the proxy emits 502 (upstream failure) via the
// ErrorHandler instead of a half-decoded body.
func TestCR1MalformedGzipUnit(t *testing.T) {
	garbage := []byte("this is definitely not a gzip stream")
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(garbage)),
	}
	resp.Header.Set("Content-Type", "text/html")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.ContentLength = int64(len(garbage))

	err := prepareHTMLBody(resp, 1<<20, cr1PageURL(t), nil, nil)
	if err == nil {
		t.Fatalf("malformed gzip: prepareHTMLBody returned nil, want a decode error")
	}
	if errors.Is(err, errResponseTooLarge) {
		t.Fatalf("malformed gzip: err should not be errResponseTooLarge, got %v", err)
	}
}

// TestCR1MalformedGzipThrough502 confirms the decode error reaches the
// ReverseProxy's ErrorHandler and produces a 502, not a half-decoded body.
func TestCR1MalformedGzipThrough502(t *testing.T) {
	garbage := []byte("this is definitely not a gzip stream")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(garbage)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(garbage); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/badgzip")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("malformed gzip e2e: got status %d, want 502 (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestCR1GzipHTMLNoLimit covers the maxBytes <= 0 ("no limit") decode branch:
// gzip HTML is still decompressed correctly and the Content-Encoding header is
// dropped when no size cap is configured.
func TestCR1GzipHTMLNoLimit(t *testing.T) {
	const html = "<html><body>unbounded</body></html>"
	gz := gzipBytes(t, []byte(html))
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(gz)),
	}
	resp.Header.Set("Content-Type", "text/html")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.ContentLength = int64(len(gz))

	if err := prepareHTMLBody(resp, 0, cr1PageURL(t), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody(no limit): %v", err)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Errorf("no-limit gzip: Content-Encoding = %q, want removed", ce)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// CR2: the decoded HTML is now rewritten (a <base> is injected). The original
	// text content must survive and the no-limit decode branch must still work.
	if !strings.Contains(string(body), "unbounded") {
		t.Errorf("no-limit gzip: body = %q, want it to contain decoded content", string(body))
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("no-limit gzip: ContentLength = %d, want %d", resp.ContentLength, len(body))
	}
}

// TestCR1NilResponseGuard covers the defensive nil guards: a nil response or a
// response with a nil body must be a no-op (no panic).
func TestCR1NilResponseGuard(t *testing.T) {
	if err := prepareHTMLBody(nil, 1<<20, cr1PageURL(t), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody(nil): %v", err)
	}
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Content-Type", "text/html")
	if err := prepareHTMLBody(resp, 1<<20, cr1PageURL(t), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody(nil body): %v", err)
	}
}

// TestCR1IsHTMLContentType exercises the content-type classifier directly,
// covering "HTML vs non-HTML correctly distinguished".
func TestCR1IsHTMLContentType(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/HTML; charset=UTF-8", true},
		{"text/html ;charset=utf-8", true},
		{"  text/html  ", true},
		{"application/json", false},
		{"image/png", false},
		{"application/xhtml+xml", false},
		{"", false},
		{"text/plain", false},
	}
	for _, c := range cases {
		if got := isHTMLContentType(c.in); got != c.want {
			t.Errorf("isHTMLContentType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

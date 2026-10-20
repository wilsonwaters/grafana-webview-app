package plugin

// S2 (#142) backend security tests for the /proxy endpoint. The /check-frameable
// role gate (VR-3) is in frameable_authz_test.go and the IP blocklist (VR-11) in
// pkg/security/ipblocklist_test.go.

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// chunkedUpstream returns an upstream that streams body in small flushed
// chunks with no Content-Length (so the proxy sees ContentLength -1), with the
// given status, Content-Type, Content-Encoding and Location.
func chunkedUpstream(t *testing.T, status int, contentType, encoding, location string, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if encoding != "" {
			w.Header().Set("Content-Encoding", encoding)
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < len(body); i += 512 {
			end := i + 512
			if end > len(body) {
				end = len(body)
			}
			if _, err := w.Write(body[i:end]); err != nil {
				return // the proxy may stop reading at the limit
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

// TestProxyOversizeUndeclaredLengthIsCleanError covers VR-7: a response with no
// Content-Length that is larger than MaxResponseBytes is never delivered as a
// truncated 200. Every content type ends as a clean 413 with none of the
// upstream bytes; a followed redirect drops its body instead; a body exactly at
// the limit is delivered whole with a matching Content-Length.
func TestProxyOversizeUndeclaredLengthIsCleanError(t *testing.T) {
	const limit = 4096
	over := bytes.Repeat([]byte("Z"), limit*4)
	atLimit := bytes.Repeat([]byte("Z"), limit)

	cases := []struct {
		name        string
		status      int
		contentType string
		location    string
		body        []byte
		wantStatus  int
		wantBody    []byte // nil: only assert no upstream bytes leaked
	}{
		{"html over limit", http.StatusOK, "text/html", "", over, http.StatusRequestEntityTooLarge, nil},
		{"css over limit", http.StatusOK, "text/css", "", over, http.StatusRequestEntityTooLarge, nil},
		{"json over limit", http.StatusOK, "application/json", "", over, http.StatusRequestEntityTooLarge, nil},
		{"image over limit", http.StatusOK, "image/png", "", over, http.StatusRequestEntityTooLarge, nil},
		{"error status over limit", http.StatusInternalServerError, "text/plain", "", over, http.StatusRequestEntityTooLarge, nil},
		{"non-followable redirect over limit", http.StatusFound, "text/plain", "mailto:a@example.com", over, http.StatusRequestEntityTooLarge, nil},
		{"followed redirect drops its body", http.StatusFound, "text/html", "http://example.com/next", over, http.StatusFound, []byte{}},
		{"non-html exactly at limit", http.StatusOK, "application/octet-stream", "", atLimit, http.StatusOK, atLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := chunkedUpstream(t, tc.status, tc.contentType, "", tc.location, tc.body)
			defer upstream.Close()
			cfg := settingsWith(allowExample(DomainOptions{}))
			cfg.MaxResponseBytes = limit
			p := newTestHandler(t, cfg, upstream)

			rec := doProxy(p, "http://example.com/stream")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %d bytes)", rec.Code, tc.wantStatus, rec.Body.Len())
			}
			if tc.wantBody == nil {
				if bytes.Contains(rec.Body.Bytes(), []byte("ZZZZ")) {
					t.Fatalf("upstream bytes leaked into the error response (%d bytes)", rec.Body.Len())
				}
				return
			}
			if !bytes.Equal(rec.Body.Bytes(), tc.wantBody) {
				t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(tc.wantBody))
			}
			if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(tc.wantBody)) {
				t.Fatalf("Content-Length = %q, want %d", got, len(tc.wantBody))
			}
		})
	}
}

// TestProxyGzipBombBounded covers VR-6: a gzip body whose DECODED size exceeds
// MaxResponseBytes is refused with 413 for every content type, whether or not
// it declares a Content-Length, while a gzip body within the limit is still
// passed through compressed and byte-identical (non-HTML) or decoded and
// rewritten (HTML). A malformed gzip body is a 502.
func TestProxyGzipBombBounded(t *testing.T) {
	const limit = 64 * 1024
	bomb := gzipBytes(t, bytes.Repeat([]byte("A"), 16*limit)) // ~1 MiB decoded, ~1 KiB on the wire
	small := gzipBytes(t, []byte(`{"ok":true}`))
	if len(bomb) >= limit {
		t.Fatalf("test setup: compressed bomb is %d bytes, must be under the %d limit", len(bomb), limit)
	}

	cases := []struct {
		name        string
		contentType string
		body        []byte
		declareLen  bool
		wantStatus  int
	}{
		{"octet-stream bomb, declared length", "application/octet-stream", bomb, true, http.StatusRequestEntityTooLarge},
		{"css bomb, chunked", "text/css", bomb, false, http.StatusRequestEntityTooLarge},
		{"json bomb, chunked", "application/json", bomb, false, http.StatusRequestEntityTooLarge},
		{"html bomb, chunked", "text/html", bomb, false, http.StatusRequestEntityTooLarge},
		{"json within limit", "application/json", small, true, http.StatusOK},
		{"malformed gzip non-html", "application/json", []byte("not gzip at all"), true, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Content-Encoding", "gzip")
				if tc.declareLen {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.body)
			}))
			defer upstream.Close()
			cfg := settingsWith(allowExample(DomainOptions{}))
			cfg.MaxResponseBytes = limit
			p := newTestHandler(t, cfg, upstream)

			rec := doProxy(p, "http://example.com/bomb")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if !bytes.Equal(rec.Body.Bytes(), tc.body) || rec.Header().Get("Content-Encoding") != "gzip" {
					t.Fatalf("within-limit gzip body not passed through compressed and unchanged")
				}
			}
		})
	}
}

// TestProxyUnsupportedContentEncodingRejected covers VR-6: the proxy asks only
// for gzip; a response in any other coding cannot have its decoded size checked
// and is refused with 502 instead of being passed through.
func TestProxyUnsupportedContentEncodingRejected(t *testing.T) {
	cases := []struct {
		encoding    string
		contentType string
		wantStatus  int
	}{
		{"br", "text/html", http.StatusBadGateway},
		{"br", "application/javascript", http.StatusBadGateway},
		{"deflate", "text/css", http.StatusBadGateway},
		{"zstd", "image/png", http.StatusBadGateway},
		{"gzip, br", "application/json", http.StatusBadGateway},
		{"compress", "text/plain", http.StatusBadGateway},
		{"identity", "text/plain", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.encoding+" "+tc.contentType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Content-Encoding", tc.encoding)
				_, _ = io.WriteString(w, "opaque-bytes")
			}))
			defer upstream.Close()
			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
			rec := doProxy(p, "http://example.com/x")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK && strings.Contains(rec.Body.String(), "opaque-bytes") {
				t.Fatal("upstream body leaked into the 502")
			}
		})
	}
}

// TestResponseEncodingUnit covers the Content-Encoding parser behind VR-6.
func TestResponseEncodingUnit(t *testing.T) {
	cases := []struct {
		values  []string
		want    string
		wantErr bool
	}{
		{nil, "", false},
		{[]string{"identity"}, "", false},
		{[]string{"gzip"}, contentEncodingGzip, false},
		{[]string{" GZIP "}, contentEncodingGzip, false},
		{[]string{"x-gzip"}, contentEncodingGzip, false},
		{[]string{"identity, gzip"}, contentEncodingGzip, false},
		{[]string{"br"}, "", true},
		{[]string{"gzip", "gzip"}, "", true},
		{[]string{"gzip, deflate"}, "", true},
	}
	for _, tc := range cases {
		h := http.Header{}
		for _, v := range tc.values {
			h.Add("Content-Encoding", v)
		}
		got, err := responseEncoding(h)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("responseEncoding(%q) = (%q, %v), want (%q, err=%v)", tc.values, got, err, tc.want, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, errUnsupportedEncoding) {
			t.Errorf("responseEncoding(%q) error = %v, want errUnsupportedEncoding", tc.values, err)
		}
	}
}

// TestRewriteOutputLimit covers VR-6 for the rewriter: the rewritten document
// may not be longer than MaxResponseBytes. A long base URL multiplied over
// many short relative references (in attributes or srcset) is stopped early
// with errResponseTooLarge instead of growing the document without bound.
func TestRewriteOutputLimit(t *testing.T) {
	page := mustParseURL(t, examplePage)
	longBase := `<base href="http://example.com/` + strings.Repeat("d", 10000) + `/">`
	cases := []struct {
		name    string
		html    string
		limit   int64
		wantErr bool
	}{
		{"href amplification", "<html><head>" + longBase + "</head><body>" + strings.Repeat(`<a href="x">x</a>`, 2000) + "</body></html>", 1 << 20, true},
		{"srcset amplification", "<html><head>" + longBase + "</head><body><img srcset=\"" + strings.Repeat("a 1x,", 2000) + "\"></body></html>", 1 << 20, true},
		{"injection pushes output over the limit", "<html><body>" + strings.Repeat("p", 2000) + "</body></html>", 2100, true},
		{"normal page within the limit", `<html><body><img src="a.png"><a href="/x">x</a></body></html>`, 1 << 20, false},
		{"no limit", "<html><body>" + strings.Repeat(`<a href="x">x</a>`, 50) + "</body></html>", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := rewriteHTMLLimited([]byte(tc.html), page, "text/html", nil, tc.limit)
			if tc.wantErr {
				if !errors.Is(err, errResponseTooLarge) {
					t.Fatalf("err = %v, want errResponseTooLarge", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rewriteHTMLLimited: %v", err)
			}
			if tc.limit > 0 && int64(len(out)) > tc.limit {
				t.Fatalf("output %d bytes exceeds the %d limit", len(out), tc.limit)
			}
		})
	}
}

// TestProxyRewrittenHTMLOverLimitIs413 covers VR-6 through /proxy: an HTML page
// that fits MaxResponseBytes but whose rewritten form does not is a 413, not
// the 502 used for other rewrite failures.
func TestProxyRewrittenHTMLOverLimitIs413(t *testing.T) {
	const limit = 8192
	html := "<html><head>" + `<base href="http://example.com/` + strings.Repeat("d", 1000) + `/">` +
		"</head><body>" + strings.Repeat(`<a href="x">x</a>`, 200) + "</body></html>"
	if len(html) > limit {
		t.Fatalf("test setup: page is %d bytes, must fit the %d limit", len(html), limit)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, html)
	}))
	defer upstream.Close()
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = limit
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %d bytes)", rec.Code, rec.Body.Len())
	}
}

// TestSetBufferedBodyBodilessStatus confirms a bodiless status (304) gets no
// Content-Length from the buffered body.
func TestSetBufferedBodyBodilessStatus(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{}}
	setBufferedBody(resp, nil)
	if v := resp.Header.Get("Content-Length"); v != "" {
		t.Fatalf("304 Content-Length = %q, want none", v)
	}
	resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Trailer": {"X-T"}}, Trailer: http.Header{"X-T": nil}}
	setBufferedBody(resp, []byte("abc"))
	if resp.Header.Get("Content-Length") != "3" || resp.ContentLength != 3 || resp.Trailer != nil || resp.Header.Get("Trailer") != "" {
		t.Fatalf("200 framing = CL %q / %d, trailer %v / %q", resp.Header.Get("Content-Length"), resp.ContentLength, resp.Trailer, resp.Header.Get("Trailer"))
	}
}

// TestProxyStripsRefreshHeader covers VR-5: an upstream Refresh header (a
// client-side redirect that bypasses per-hop re-validation) never reaches the
// client, for HTML, non-HTML and redirect responses, in any header-name case.
func TestProxyStripsRefreshHeader(t *testing.T) {
	cases := []struct {
		name        string
		headerName  string
		status      int
		contentType string
		location    string
	}{
		{"html 200", "Refresh", http.StatusOK, "text/html; charset=utf-8", ""},
		{"plain 200", "Refresh", http.StatusOK, "text/plain", ""},
		{"lower-case name", "refresh", http.StatusOK, "text/html", ""},
		{"redirect 302", "Refresh", http.StatusFound, "text/html", "http://example.com/next"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Set the raw map key so the non-canonical spelling really reaches the wire.
				w.Header()[tc.headerName] = []string{"0; url=https://evil.example/"}
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("X-Benign", "kept")
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "<html><body>x</body></html>")
			}))
			defer upstream.Close()

			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
			rec := doProxy(p, "http://example.com/page")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.status, rec.Body.String())
			}
			if v := rec.Header().Values("Refresh"); len(v) != 0 {
				t.Fatalf("Refresh header reached the client: %v", v)
			}
			if got := rec.Header().Get("X-Benign"); got != "kept" {
				t.Fatalf("benign header X-Benign = %q, want kept", got)
			}
		})
	}
}

// TestProxyBlocksTranslatedInternalAddresses covers VR-11 end to end: an
// allowlisted host that resolves to a 6to4 or NAT64 address wrapping an
// internal IPv4 address is refused with 403 and never dialled, while a NAT64
// address wrapping a public IPv4 address is dialled.
func TestProxyBlocksTranslatedInternalAddresses(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{"6to4 metadata", "2002:a9fe:a9fe::1", true},
		{"6to4 rfc1918", "2002:0a00:0001::1", true},
		{"6to4 loopback", "2002:7f00:0001::1", true},
		{"nat64 metadata", "64:ff9b::a9fe:a9fe", true},
		{"nat64 rfc1918", "64:ff9b::10.0.0.1", true},
		{"nat64 local-use", "64:ff9b:1::a9fe:a9fe", true},
		{"nat64 public", "64:ff9b::93.184.216.34", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recDialer := &tc1RecordingDialer{}
			p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
			p.transport = tc1AllowlistedTransport(tc1NewStubResolver(tc.ip), recDialer)

			rec := doProxy(p, "https://example.com/page")
			dialed := recDialer.addrs()
			if tc.blocked {
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s: got %d, want 403 (body=%q)", tc.ip, rec.Code, rec.Body.String())
				}
				if len(dialed) != 0 {
					t.Fatalf("%s: blocked address was dialled: %v", tc.ip, dialed)
				}
				return
			}
			// The trap dialer refuses the connection after recording it, so the
			// status is a gateway error; the assertion is the recorded dial.
			if len(dialed) != 1 {
				t.Fatalf("%s: expected one validated dial, got %v (status %d)", tc.ip, dialed, rec.Code)
			}
		})
	}
}

// TestStripFramingHeadersRemovesRefreshUnit covers VR-5 at the unit level.
func TestStripFramingHeadersRemovesRefreshUnit(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Refresh", "0; url=https://evil.example/")
	resp.Header.Add("Refresh", "5")
	if err := stripFramingHeaders(resp); err != nil {
		t.Fatalf("stripFramingHeaders: %v", err)
	}
	if v := resp.Header.Values("Refresh"); len(v) != 0 {
		t.Fatalf("Refresh = %v, want every value removed", v)
	}
}

package plugin

// RV2 (#140) regression tests for the /proxy response path.

import (
	"net/http"
	"testing"
)

// TestProxyEmptyEncodedBodyPassesThrough covers RV2: a response that declares
// Content-Encoding: gzip but has no body (304, 204, or an empty 200) is not a
// 502. It passes through with its status, an empty body and no
// Content-Encoding. An empty body with an unsupported coding passes too: there
// is nothing to decode.
func TestProxyEmptyEncodedBodyPassesThrough(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		contentType string
		encoding    string
	}{
		{"304 html gzip", http.StatusNotModified, "text/html", "gzip"},
		{"204 gzip", http.StatusNoContent, "text/html", "gzip"},
		{"200 empty html gzip", http.StatusOK, "text/html; charset=utf-8", "gzip"},
		{"200 empty json gzip", http.StatusOK, "application/json", "gzip"},
		{"200 empty html br", http.StatusOK, "text/html", "br"},
		{"304 br", http.StatusNotModified, "text/css", "br"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := chunkedUpstream(t, tc.status, tc.contentType, tc.encoding, "", nil)
			defer upstream.Close()
			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

			rec := doProxy(p, "http://example.com/page")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.status, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", rec.Body.String())
			}
			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none on an empty body", ce)
			}
		})
	}
}

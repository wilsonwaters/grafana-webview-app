package plugin

// S2 (#142) backend security tests for the /proxy endpoint. The /check-frameable
// role gate (VR-3) is in frameable_authz_test.go and the IP blocklist (VR-11) in
// pkg/security/ipblocklist_test.go.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

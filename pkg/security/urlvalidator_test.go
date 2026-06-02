package security

import (
	"strings"
	"testing"
)

// TestValidateURLValid covers well-formed http/https URLs that must pass,
// asserting the returned normalised scheme, hostname, and effective port.
func TestValidateURLValid(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		extraPorts   []int
		wantScheme   string
		wantHostname string
		wantPort     int
	}{
		{"http default port", "http://example.com/path", nil, "http", "example.com", 80},
		{"https default port", "https://example.com", nil, "https", "example.com", 443},
		{"http explicit 80", "http://example.com:80/", nil, "http", "example.com", 80},
		{"https explicit 443", "https://example.com:443/", nil, "https", "example.com", 443},
		{"http with query and fragment", "http://example.com/a?b=c#frag", nil, "http", "example.com", 80},
		{"https subdomain", "https://api.example.com/v1", nil, "https", "api.example.com", 443},
		{"ipv6 literal", "https://[2606:4700:4700::1111]/x", nil, "https", "2606:4700:4700::1111", 443},
		{"ipv6 literal with port allowed", "https://[2606:4700:4700::1111]:8443/x", []int{8443}, "https", "2606:4700:4700::1111", 8443},
		{"ipv4 literal", "http://93.184.216.34/", nil, "http", "93.184.216.34", 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateURL(tt.raw, tt.extraPorts)
			if err != nil {
				t.Fatalf("ValidateURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got.Scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", got.Scheme, tt.wantScheme)
			}
			if got.Hostname != tt.wantHostname {
				t.Errorf("hostname = %q, want %q", got.Hostname, tt.wantHostname)
			}
			if got.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", got.Port, tt.wantPort)
			}
		})
	}
}

// TestValidateURLSchemeRejected asserts that every non-http(s) scheme is
// rejected with the stable ReasonScheme token. (Completion Criterion: non-HTTP
// schemes rejected.)
func TestValidateURLSchemeRejected(t *testing.T) {
	cases := []string{
		"ftp://example.com/",
		"file:///etc/passwd",
		"gopher://example.com/",
		"data:text/plain;base64,SGk=",
		"javascript:alert(1)",
		"ws://example.com/socket",
		"wss://example.com/socket",
		"mailto:user@example.com",
		"ssh://example.com",
		"tel:+15551234",
		"//example.com/protocol-relative",
		"example.com/no-scheme",
		"/just/a/path",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ValidateURL(raw, nil)
			if err == nil {
				t.Fatalf("ValidateURL(%q) = nil error, want rejection", raw)
			}
			if got := ReasonOf(err); got != ReasonScheme {
				t.Errorf("ValidateURL(%q) reason = %q, want %q", raw, got, ReasonScheme)
			}
		})
	}
}

// TestValidateURLPort asserts the port rules: scheme defaults always allowed,
// caller-supplied extra ports allowed, everything else rejected with
// ReasonPort. (Completion Criterion: disallowed ports rejected; allowed pass.)
func TestValidateURLPort(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		extraPorts []int
		wantErr    bool
		wantReason string
		wantPort   int
	}{
		{"default http", "http://example.com/", nil, false, "", 80},
		{"default https", "https://example.com/", nil, false, "", 443},
		{"explicit 80 always ok", "http://example.com:80/", nil, false, "", 80},
		{"explicit 443 always ok", "https://example.com:443/", nil, false, "", 443},
		{"443 on http ok (default)", "http://example.com:443/", nil, false, "", 443},
		{"80 on https ok (default)", "https://example.com:80/", nil, false, "", 80},
		{"extra port allowed", "https://example.com:8443/", []int{8443}, false, "", 8443},
		{"extra port among several", "https://example.com:9000/", []int{8443, 9000, 8080}, false, "", 9000},
		{"disallowed port no extras", "http://example.com:8080/", nil, true, ReasonPort, 0},
		{"disallowed port with other extras", "http://example.com:8080/", []int{9000}, true, ReasonPort, 0},
		{"port out of range high", "http://example.com:70000/", nil, true, ReasonPort, 0},
		{"port zero", "http://example.com:0/", nil, true, ReasonPort, 0},
		{"extra port zero is invalid range", "http://example.com:0/", []int{0}, true, ReasonPort, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateURL(tt.raw, tt.extraPorts)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateURL(%q) = nil error, want rejection", tt.raw)
				}
				if r := ReasonOf(err); r != tt.wantReason {
					t.Errorf("reason = %q, want %q", r, tt.wantReason)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", got.Port, tt.wantPort)
			}
		})
	}
}

// TestValidateURLHostnameNormalisation asserts hostnames are normalised
// consistently: lowercased and a single trailing dot stripped. (Completion
// Criterion: hostname normalised consistently.)
func TestValidateURLHostnameNormalisation(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantHostname string
	}{
		{"uppercase host", "https://EXAMPLE.COM/", "example.com"},
		{"mixed case host", "https://ExAmPlE.CoM/path", "example.com"},
		{"trailing dot stripped", "https://example.com./", "example.com"},
		{"uppercase with trailing dot", "https://EXAMPLE.COM./", "example.com"},
		{"subdomain mixed case", "https://API.Example.Com/", "api.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateURL(tt.raw, nil)
			if err != nil {
				t.Fatalf("ValidateURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got.Hostname != tt.wantHostname {
				t.Errorf("hostname = %q, want %q", got.Hostname, tt.wantHostname)
			}
		})
	}
}

// TestValidateURLIDN asserts internationalised domain names are converted to
// ASCII punycode without panicking, and invalid IDN input is rejected with
// ReasonHostname. (Completion Criterion: IDN inputs handled without panic.)
func TestValidateURLIDN(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantErr      bool
		wantHostname string
	}{
		{"german umlaut", "https://bücher.example/", false, "xn--bcher-kva.example"},
		{"chinese label", "https://例え.テスト/", false, "xn--r8jz45g.xn--zckzah"},
		{"cyrillic", "https://пример.испытание/", false, "xn--e1afmkfd.xn--80akhbyknj4f"},
		{"already punycode passthrough", "https://xn--bcher-kva.example/", false, "xn--bcher-kva.example"},
		{"idn uppercase normalised", "https://BÜCHER.example/", false, "xn--bcher-kva.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateURL(tt.raw, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateURL(%q) = nil error, want rejection", tt.raw)
				}
				if r := ReasonOf(err); r != ReasonHostname {
					t.Errorf("reason = %q, want %q", r, ReasonHostname)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got.Hostname != tt.wantHostname {
				t.Errorf("hostname = %q, want %q", got.Hostname, tt.wantHostname)
			}
		})
	}
}

// TestValidateURLMalformed asserts unparseable, empty, hostless, and otherwise
// structurally invalid URLs are rejected and fail closed.
func TestValidateURLMalformed(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantReason string
	}{
		{"empty string", "", ReasonMalformed},
		{"whitespace only", "   ", ReasonMalformed},
		{"control char in url", "http://exa\x7fmple.com/", ReasonMalformed},
		{"opaque form no host", "http:example.com", ReasonMalformed},
		{"scheme only no host", "http://", ReasonHostname},
		{"scheme only https no host", "https://", ReasonHostname},
		{"host is only trailing dot", "http://./", ReasonHostname},
		{"invalid punycode label", "http://xn--a/", ReasonHostname},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateURL(tt.raw, nil)
			if err == nil {
				t.Fatalf("ValidateURL(%q) = nil error, want rejection", tt.raw)
			}
			if r := ReasonOf(err); r != tt.wantReason {
				t.Errorf("ValidateURL(%q) reason = %q, want %q", tt.raw, r, tt.wantReason)
			}
		})
	}
}

// TestValidateURLUserinfo asserts embedded credentials are rejected with the
// stable ReasonUserinfo token (an SSRF / credential-leak vector).
func TestValidateURLUserinfo(t *testing.T) {
	cases := []string{
		"http://user@example.com/",
		"https://user:password@example.com/",
		"https://:password@example.com/",
		"http://admin:@example.com/",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := ValidateURL(raw, nil)
			if err == nil {
				t.Fatalf("ValidateURL(%q) = nil error, want rejection", raw)
			}
			if r := ReasonOf(err); r != ReasonUserinfo {
				t.Errorf("ValidateURL(%q) reason = %q, want %q", raw, r, ReasonUserinfo)
			}
		})
	}
}

// TestNormalizeHostname exercises the exported helper directly, including the
// error cases that the allowlist matcher (SF3) will rely on for fail-closed
// behaviour.
func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		want    string
		wantErr bool
	}{
		{"plain", "Example.COM", "example.com", false},
		{"trailing dot", "example.com.", "example.com", false},
		{"idn", "bücher.example", "xn--bcher-kva.example", false},
		{"empty", "", "", true},
		{"only dot", ".", "", true},
		{"invalid punycode", "xn--a", "", true},
		{"ipv4 literal passthrough", "192.168.0.1", "192.168.0.1", false},
		{"ipv6 literal passthrough", "2606:4700:4700::1111", "2606:4700:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHostname(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeHostname(%q) = nil error, want error", tt.host)
				}
				if r := ReasonOf(err); r != ReasonHostname {
					t.Errorf("reason = %q, want %q", r, ReasonHostname)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeHostname(%q) unexpected error: %v", tt.host, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeHostname(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

// TestReasonOf verifies the error-to-token helper handles nil and non-validation
// errors gracefully, returning the empty string rather than panicking.
func TestReasonOf(t *testing.T) {
	if got := ReasonOf(nil); got != "" {
		t.Errorf("ReasonOf(nil) = %q, want empty", got)
	}
	if got := ReasonOf(errPlain("boom")); got != "" {
		t.Errorf("ReasonOf(non-validation) = %q, want empty", got)
	}
	_, err := ValidateURL("ftp://example.com/", nil)
	if got := ReasonOf(err); got != ReasonScheme {
		t.Errorf("ReasonOf(scheme error) = %q, want %q", got, ReasonScheme)
	}
}

// errPlain is a trivial error type used to confirm ReasonOf ignores errors that
// are not *ValidationError.
type errPlain string

func (e errPlain) Error() string { return string(e) }

// TestValidationErrorMessage confirms the Error() string carries both the
// stable reason token and the human-readable message.
func TestValidationErrorMessage(t *testing.T) {
	_, err := ValidateURL("ftp://example.com/", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if msg == "" {
		t.Fatal("Error() returned empty string")
	}
	for _, want := range []string{ReasonScheme, "ftp"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to contain %q", msg, want)
		}
	}
}

// TestValidateURLNoPanic is a lightweight fuzz-style guard: a spread of weird
// inputs must never panic — they either validate or return a *ValidationError.
func TestValidateURLNoPanic(t *testing.T) {
	inputs := []string{
		"", " ", "h", "http", "http:", "http:/", "http://", "://", "::::",
		"http://[::1]", "http://[", "http://]", "http://:80", "http://a:b:c",
		"https://例え.テスト:8443/x", "http://%/", "http://%zz/", string([]byte{0x00}),
		"HTTP://EXAMPLE.COM./PATH?Q=1#F", "https://xn--/", "http://..../",
	}
	for _, raw := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ValidateURL(%q) panicked: %v", raw, r)
				}
			}()
			if _, err := ValidateURL(raw, []int{8443}); err != nil {
				if ReasonOf(err) == "" {
					t.Errorf("ValidateURL(%q) returned non-ValidationError: %v", raw, err)
				}
			}
		}()
	}
}

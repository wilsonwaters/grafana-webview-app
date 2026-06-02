package security

import (
	"testing"

	"github.com/wilsonwaters/webview/pkg/plugin"
)

// dom is a small helper to build an AllowedDomain with the subdomain flag and
// (optionally) per-domain options, keeping the table rows compact.
func dom(domain string, allowSubdomains bool) plugin.AllowedDomain {
	return plugin.AllowedDomain{
		Domain:  domain,
		Options: plugin.DomainOptions{AllowSubdomains: allowSubdomains},
	}
}

// TestMatchHostnameExact covers exact hostname matching against an allowlist
// entry's Domain (after canonicalisation), including the AllowSubdomains=false
// case where only the exact apex matches.
//
// Completion Criterion: "Exact and subdomain matching work correctly" (exact half).
func TestMatchHostnameExact(t *testing.T) {
	allow := []plugin.AllowedDomain{
		dom("example.com", false),
		dom("api.internal.test", false),
	}
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"apex exact match", "example.com", true},
		{"second entry exact", "api.internal.test", true},
		{"subdomain denied when flag off", "api.example.com", false},
		{"deep subdomain denied when flag off", "a.b.example.com", false},
		{"unrelated host", "other.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchHostname(tt.host, allow)
			if got.Allowed != tt.want {
				t.Fatalf("MatchHostname(%q).Allowed = %v, want %v (result=%q)", tt.host, got.Allowed, tt.want, got.Result)
			}
			if tt.want && got.Result != ResultExact {
				t.Errorf("Result = %q, want %q", got.Result, ResultExact)
			}
		})
	}
}

// TestMatchHostnameSubdomain covers subdomain matching (including multi-level)
// when the matched entry opts in via AllowSubdomains.
//
// Completion Criterion: "Exact and subdomain matching work correctly" (subdomain half).
func TestMatchHostnameSubdomain(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", true)}
	tests := []struct {
		name       string
		host       string
		wantAllow  bool
		wantResult string
	}{
		{"apex still matches exact", "example.com", true, ResultExact},
		{"single-level subdomain", "api.example.com", true, ResultSubdomain},
		{"multi-level subdomain", "a.b.example.com", true, ResultSubdomain},
		{"deep multi-level subdomain", "x.y.z.example.com", true, ResultSubdomain},
		{"unrelated host", "example.org", false, ResultNoMatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchHostname(tt.host, allow)
			if got.Allowed != tt.wantAllow {
				t.Fatalf("MatchHostname(%q).Allowed = %v, want %v", tt.host, got.Allowed, tt.wantAllow)
			}
			if got.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", got.Result, tt.wantResult)
			}
		})
	}
}

// TestMatchHostnameSubdomainDisabled asserts that with AllowSubdomains=false the
// entry covers only the exact apex and rejects every subdomain.
//
// Completion Criterion: "Exact and subdomain matching work correctly"
// (AllowSubdomains=false rejecting subdomains).
func TestMatchHostnameSubdomainDisabled(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", false)}
	for _, host := range []string{"api.example.com", "a.b.example.com", "www.example.com"} {
		if got := MatchHostname(host, allow); got.Allowed {
			t.Errorf("MatchHostname(%q) allowed with AllowSubdomains=false, want denied (result=%q)", host, got.Result)
		}
	}
	// The apex itself must still match exactly.
	if got := MatchHostname("example.com", allow); !got.Allowed || got.Result != ResultExact {
		t.Errorf("apex MatchHostname(example.com) = %+v, want allowed exact", got)
	}
}

// TestMatchHostnamePartialAndSuffixTricks asserts that partial-label matches and
// the suffix trick (example.com.evil.com) never match, even with subdomain
// matching enabled. These are the classic allowlist bypass vectors.
//
// Completion Criterion: "Exact and subdomain matching work correctly"
// (partial-label and suffix-trick non-match).
func TestMatchHostnamePartialAndSuffixTricks(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", true)}
	tricks := []string{
		"notexample.com",       // partial label, shares suffix without a dot boundary
		"badexample.com",       // partial label
		"example.com.evil.com", // suffix trick: host does not END in .example.com
		"evilexample.com",      // partial label
		"xexample.com",         // partial label
	}
	for _, host := range tricks {
		t.Run(host, func(t *testing.T) {
			if got := MatchHostname(host, allow); got.Allowed {
				t.Errorf("MatchHostname(%q) allowed, want denied (result=%q)", host, got.Result)
			}
		})
	}
}

// TestMatchHostnameEmptyAndNilAllowlist asserts the fail-closed default: an empty
// or nil allowlist denies every hostname.
//
// Completion Criterion: "Empty allowlist denies everything".
func TestMatchHostnameEmptyAndNilAllowlist(t *testing.T) {
	hosts := []string{"example.com", "api.example.com", "anything.test"}
	for _, host := range hosts {
		t.Run("nil/"+host, func(t *testing.T) {
			got := MatchHostname(host, nil)
			if got.Allowed {
				t.Errorf("nil allowlist allowed %q, want denied", host)
			}
			if got.Result != ResultNoMatch {
				t.Errorf("nil allowlist Result = %q, want %q", got.Result, ResultNoMatch)
			}
		})
		t.Run("empty/"+host, func(t *testing.T) {
			got := MatchHostname(host, []plugin.AllowedDomain{})
			if got.Allowed {
				t.Errorf("empty allowlist allowed %q, want denied", host)
			}
			if got.Result != ResultNoMatch {
				t.Errorf("empty allowlist Result = %q, want %q", got.Result, ResultNoMatch)
			}
		})
	}
}

// TestMatchHostnameOptionsReturned asserts that the matched entry's per-domain
// options are returned to the caller on both exact and subdomain matches, so
// downstream controls (SF2 ports, SF4 private-IP, SF5 rate limit) can apply
// per-domain policy.
//
// Completion Criterion: "Per-domain options are returned to the caller on match".
func TestMatchHostnameOptionsReturned(t *testing.T) {
	opts := plugin.DomainOptions{
		AllowSubdomains: true,
		AllowPrivateIP:  true,
		RateLimitPerMin: 42,
		AllowedPorts:    []int{8443, 9000},
	}
	allow := []plugin.AllowedDomain{{Domain: "example.com", Options: opts}}

	assertOpts := func(t *testing.T, host string, wantResult string) {
		t.Helper()
		got := MatchHostname(host, allow)
		if !got.Allowed {
			t.Fatalf("MatchHostname(%q) denied, want allowed", host)
		}
		if got.Result != wantResult {
			t.Errorf("Result = %q, want %q", got.Result, wantResult)
		}
		if got.Domain != "example.com" {
			t.Errorf("Domain = %q, want %q", got.Domain, "example.com")
		}
		if got.Options.AllowPrivateIP != true {
			t.Errorf("Options.AllowPrivateIP = %v, want true", got.Options.AllowPrivateIP)
		}
		if got.Options.RateLimitPerMin != 42 {
			t.Errorf("Options.RateLimitPerMin = %d, want 42", got.Options.RateLimitPerMin)
		}
		if len(got.Options.AllowedPorts) != 2 || got.Options.AllowedPorts[0] != 8443 || got.Options.AllowedPorts[1] != 9000 {
			t.Errorf("Options.AllowedPorts = %v, want [8443 9000]", got.Options.AllowedPorts)
		}
	}

	t.Run("on exact match", func(t *testing.T) { assertOpts(t, "example.com", ResultExact) })
	t.Run("on subdomain match", func(t *testing.T) { assertOpts(t, "api.example.com", ResultSubdomain) })
}

// TestMatchHostnameCanonicalisation asserts that case, trailing-dot, and
// IDN/Unicode folding apply to BOTH the query hostname and the configured Domain
// (the SF2 #20 carry-forward). Both sides go through NormalizeHostname so the
// canonical ASCII forms compare equal.
//
// Completion Criterion: "Exact and subdomain matching work correctly"
// (case/trailing-dot/IDN folding on both sides).
func TestMatchHostnameCanonicalisation(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		sub    bool
		host   string
		want   bool
	}{
		{"uppercase host vs lowercase domain", "example.com", false, "EXAMPLE.COM", true},
		{"uppercase domain vs lowercase host", "EXAMPLE.COM", false, "example.com", true},
		{"trailing dot on host", "example.com", false, "example.com.", true},
		{"trailing dot on domain", "example.com.", false, "example.com", true},
		{"trailing dot on both", "example.com.", false, "example.com.", true},
		{"mixed case subdomain", "Example.COM", true, "API.Example.com", true},
		// IDN folding on the query side: CJK full stop "。" folds to "." so the
		// host canonicalises to example.com and matches the ASCII domain.
		{"idn fullstop in host", "example.com", false, "example。com", true},
		// IDN folding on the configured-domain side: the admin typed the domain
		// with a CJK full stop; it must canonicalise the same way.
		{"idn fullstop in domain", "example。com", false, "example.com", true},
		// A genuine internationalised label folds to punycode on both sides.
		{"unicode label both sides", "bücher.example", false, "Bücher.example", true},
		{"unicode label host vs punycode domain", "xn--bcher-kva.example", false, "bücher.example", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allow := []plugin.AllowedDomain{dom(tt.domain, tt.sub)}
			got := MatchHostname(tt.host, allow)
			if got.Allowed != tt.want {
				t.Fatalf("MatchHostname(%q) against domain %q = %v, want %v (result=%q)", tt.host, tt.domain, got.Allowed, tt.want, got.Result)
			}
		})
	}
}

// TestMatchHostnameUnnormalisableEntrySkipped asserts that a configured entry
// whose Domain is empty or cannot be canonicalised is skipped (treated as
// unusable) rather than matching loosely, and does not poison the remaining
// usable entries.
//
// Completion Criterion: fail-closed handling of an unusable configured entry.
func TestMatchHostnameUnnormalisableEntrySkipped(t *testing.T) {
	// A bad entry followed by a good one: the good entry must still match.
	allow := []plugin.AllowedDomain{
		dom("", false),                 // empty domain -> unnormalisable, skipped
		dom("exa mple.com", false),     // space is a disallowed label char -> skipped
		dom("\x00bad.com", false),      // control char -> skipped
		dom("good.example.com", false), // usable
	}
	if got := MatchHostname("good.example.com", allow); !got.Allowed || got.Result != ResultExact {
		t.Errorf("MatchHostname(good.example.com) = %+v, want allowed exact despite bad entries", got)
	}
	// A host that only "matches" the unusable entries must be denied.
	for _, host := range []string{"exa mple.com", "bad.com"} {
		if got := MatchHostname(host, allow); got.Allowed {
			t.Errorf("MatchHostname(%q) allowed via an unusable entry, want denied", host)
		}
	}
	// An allowlist consisting ONLY of unusable entries denies everything.
	onlyBad := []plugin.AllowedDomain{dom("", false), dom("exa mple.com", true)}
	if got := MatchHostname("anything.com", onlyBad); got.Allowed {
		t.Errorf("MatchHostname against only-unusable allowlist allowed, want denied (result=%q)", got.Result)
	}
}

// TestMatchHostnameInvalidQueryDenied asserts that an empty or un-normalisable
// query hostname is denied with ResultInvalidInput (fail-closed), independent of
// the allowlist contents.
//
// Completion Criterion: fail-closed handling of invalid query input.
func TestMatchHostnameInvalidQueryDenied(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", true)}
	bad := []string{
		"",             // empty
		".",            // only a trailing dot
		"exa mple.com", // disallowed character
		"\x00.com",     // control char
	}
	for _, host := range bad {
		t.Run(host, func(t *testing.T) {
			got := MatchHostname(host, allow)
			if got.Allowed {
				t.Errorf("MatchHostname(%q) allowed, want denied", host)
			}
			if got.Result != ResultInvalidInput {
				t.Errorf("Result = %q, want %q", got.Result, ResultInvalidInput)
			}
		})
	}
}

// TestMatchHostnameIPLiteralEncodings asserts the SF2 #20 carry-forward: obfuscated
// IP-literal encodings (decimal/octal/hex) are NOT treated as matchable domains.
// IP enforcement is SF4's job; here such strings must simply fail to match a
// domain allowlist that contains real domains. They are not canonicalised to an
// IP and accidentally allowed.
//
// Completion Criterion: SF2 carry-forward (no IP-literal-as-domain matching).
func TestMatchHostnameIPLiteralEncodings(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", true)}
	// None of these are the allowlisted domain, so none must match.
	literals := []string{
		"2130706433",      // decimal 127.0.0.1
		"0177.0.0.1",      // octal
		"0x7f.0.0.1",      // hex
		"127.0.0.1",       // dotted-quad IP literal
		"192.168.0.1",     // dotted-quad private IP literal
		"[::1]",           // bracketed IPv6 (not a host on its own here)
		"169.254.169.254", // cloud metadata IP literal
	}
	for _, host := range literals {
		t.Run(host, func(t *testing.T) {
			got := MatchHostname(host, allow)
			if got.Allowed {
				t.Errorf("MatchHostname(%q) allowed against domain allowlist, want denied (result=%q)", host, got.Result)
			}
		})
	}
}

// TestMatchHostnameExactPreferredOverSubdomain asserts deterministic option
// selection: when both an exact entry and a broader subdomain entry could cover
// a host, the exact match wins and its options are returned.
func TestMatchHostnameExactPreferredOverSubdomain(t *testing.T) {
	allow := []plugin.AllowedDomain{
		{Domain: "example.com", Options: plugin.DomainOptions{AllowSubdomains: true, RateLimitPerMin: 10}},
		{Domain: "api.example.com", Options: plugin.DomainOptions{AllowSubdomains: false, RateLimitPerMin: 99}},
	}
	got := MatchHostname("api.example.com", allow)
	if !got.Allowed || got.Result != ResultExact {
		t.Fatalf("MatchHostname(api.example.com) = %+v, want allowed exact", got)
	}
	if got.Options.RateLimitPerMin != 99 {
		t.Errorf("Options.RateLimitPerMin = %d, want 99 (exact entry's options)", got.Options.RateLimitPerMin)
	}
}

// TestIsHostnameAllowed exercises the boolean convenience wrapper.
func TestIsHostnameAllowed(t *testing.T) {
	allow := []plugin.AllowedDomain{dom("example.com", true)}
	if !IsHostnameAllowed("api.example.com", allow) {
		t.Error("IsHostnameAllowed(api.example.com) = false, want true")
	}
	if IsHostnameAllowed("example.org", allow) {
		t.Error("IsHostnameAllowed(example.org) = true, want false")
	}
	if IsHostnameAllowed("example.com", nil) {
		t.Error("IsHostnameAllowed against nil allowlist = true, want false")
	}
}

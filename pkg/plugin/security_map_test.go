package plugin

import (
	"reflect"
	"testing"

	"github.com/wilsonwaters/webview/pkg/security"
)

// TestToAllowlistEntries covers Completion Criterion: the plugin→security
// allowlist mapping (AllowedDomain → AllowlistEntry, DomainOptions →
// EntryOptions), including the nil/empty fail-closed mapping.
func TestToAllowlistEntries(t *testing.T) {
	t.Run("nil maps to nil (deny all)", func(t *testing.T) {
		if got := toAllowlistEntries(nil); got != nil {
			t.Fatalf("nil input: got %v, want nil", got)
		}
		if got := toAllowlistEntries([]AllowedDomain{}); got != nil {
			t.Fatalf("empty input: got %v, want nil", got)
		}
	})

	t.Run("field-for-field mapping", func(t *testing.T) {
		in := []AllowedDomain{
			{
				Domain: "example.com",
				Options: DomainOptions{
					AllowSubdomains: true,
					AllowPrivateIP:  true,
					RateLimitPerMin: 42,
					AllowedPorts:    []int{8080, 8443},
				},
			},
			{Domain: "plain.org", Options: DomainOptions{}},
		}
		want := []security.AllowlistEntry{
			{
				Domain: "example.com",
				Options: security.EntryOptions{
					AllowSubdomains: true,
					AllowPrivateIP:  true,
					RateLimitPerMin: 42,
					AllowedPorts:    []int{8080, 8443},
				},
			},
			{Domain: "plain.org", Options: security.EntryOptions{}},
		}
		got := toAllowlistEntries(in)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mapping mismatch:\n got=%+v\nwant=%+v", got, want)
		}
	})

	t.Run("AllowedPorts slice is copied, not aliased", func(t *testing.T) {
		ports := []int{8080}
		in := []AllowedDomain{{Domain: "example.com", Options: DomainOptions{AllowedPorts: ports}}}
		got := toAllowlistEntries(in)
		ports[0] = 9999 // mutate the source
		if got[0].Options.AllowedPorts[0] != 8080 {
			t.Fatalf("AllowedPorts should be copied; mutation leaked: %v", got[0].Options.AllowedPorts)
		}
	})
}

// TestDomainRateOverrides covers Completion Criterion: per-domain rate-limit
// override map construction (normalised keys, positive-only, skip un-normalisable).
func TestDomainRateOverrides(t *testing.T) {
	in := []AllowedDomain{
		{Domain: "Example.COM", Options: DomainOptions{RateLimitPerMin: 10}}, // normalised key
		{Domain: "zero.com", Options: DomainOptions{RateLimitPerMin: 0}},     // dropped
		{Domain: "neg.com", Options: DomainOptions{RateLimitPerMin: -5}},     // dropped
		{Domain: "ok.org", Options: DomainOptions{RateLimitPerMin: 7}},
	}
	got := domainRateOverrides(in)

	want := map[string]int{"example.com": 10, "ok.org": 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

package plugin

import (
	"encoding/json"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// mustJSON marshals v to JSON bytes and panics on error. Test helper only.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestLoadSettings_EmptyJSONData verifies that empty (or missing) JSONData
// produces all safe defaults and an empty AllowedDomains list (fail-closed).
// AC: Empty allowlist default is explicit and tested.
// AC: Go config loader reads settings and falls back to safe defaults.
func TestLoadSettings_EmptyJSONData(t *testing.T) {
	cfg, err := LoadSettings(backend.AppInstanceSettings{})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	// AllowedDomains MUST be empty — fail-closed: no domains => proxy nothing.
	if len(cfg.AllowedDomains) != 0 {
		t.Errorf("AllowedDomains: got %d entries, want 0 (fail-closed)", len(cfg.AllowedDomains))
	}

	if cfg.MaxResponseBytes != DefaultMaxResponseBytes {
		t.Errorf("MaxResponseBytes: got %d, want %d", cfg.MaxResponseBytes, DefaultMaxResponseBytes)
	}
	if cfg.RequestTimeoutSec != DefaultRequestTimeoutSec {
		t.Errorf("RequestTimeoutSec: got %d, want %d", cfg.RequestTimeoutSec, DefaultRequestTimeoutSec)
	}
	if cfg.MaxRedirects != DefaultMaxRedirects {
		t.Errorf("MaxRedirects: got %d, want %d", cfg.MaxRedirects, DefaultMaxRedirects)
	}
	if cfg.RateLimitPerInstancePerMin != DefaultRateLimitPerInstancePerMin {
		t.Errorf("RateLimitPerInstancePerMin: got %d, want %d", cfg.RateLimitPerInstancePerMin, DefaultRateLimitPerInstancePerMin)
	}
	if cfg.RateLimitPerDomainPerMin != DefaultRateLimitPerDomainPerMin {
		t.Errorf("RateLimitPerDomainPerMin: got %d, want %d", cfg.RateLimitPerDomainPerMin, DefaultRateLimitPerDomainPerMin)
	}
	if cfg.MaxConcurrentRequests != DefaultMaxConcurrentRequests {
		t.Errorf("MaxConcurrentRequests: got %d, want %d", cfg.MaxConcurrentRequests, DefaultMaxConcurrentRequests)
	}
}

// TestLoadSettings_NullJSONData verifies the same defaults when JSONData contains
// a JSON null (Grafana may send this for unconfigured plugins).
func TestLoadSettings_NullJSONData(t *testing.T) {
	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: []byte("null")})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if len(cfg.AllowedDomains) != 0 {
		t.Errorf("AllowedDomains: got %d entries, want 0", len(cfg.AllowedDomains))
	}
	if cfg.MaxResponseBytes != DefaultMaxResponseBytes {
		t.Errorf("MaxResponseBytes: got %d, want %d", cfg.MaxResponseBytes, DefaultMaxResponseBytes)
	}
}

// TestLoadSettings_PartialOverrides verifies that explicitly set fields are
// used while unset fields fall back to safe defaults.
// AC: Go config loader reads settings and falls back to safe defaults.
func TestLoadSettings_PartialOverrides(t *testing.T) {
	data := mustJSON(map[string]any{
		"maxResponseBytes":  10 * 1024 * 1024, // 10 MiB
		"requestTimeoutSec": 30,
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	// Overridden fields.
	if cfg.MaxResponseBytes != 10*1024*1024 {
		t.Errorf("MaxResponseBytes: got %d, want %d", cfg.MaxResponseBytes, 10*1024*1024)
	}
	if cfg.RequestTimeoutSec != 30 {
		t.Errorf("RequestTimeoutSec: got %d, want 30", cfg.RequestTimeoutSec)
	}

	// Non-overridden fields must be at safe defaults.
	if cfg.MaxRedirects != DefaultMaxRedirects {
		t.Errorf("MaxRedirects: got %d, want %d", cfg.MaxRedirects, DefaultMaxRedirects)
	}
	if cfg.RateLimitPerInstancePerMin != DefaultRateLimitPerInstancePerMin {
		t.Errorf("RateLimitPerInstancePerMin: got %d, want %d", cfg.RateLimitPerInstancePerMin, DefaultRateLimitPerInstancePerMin)
	}
	if cfg.RateLimitPerDomainPerMin != DefaultRateLimitPerDomainPerMin {
		t.Errorf("RateLimitPerDomainPerMin: got %d, want %d", cfg.RateLimitPerDomainPerMin, DefaultRateLimitPerDomainPerMin)
	}
	if cfg.MaxConcurrentRequests != DefaultMaxConcurrentRequests {
		t.Errorf("MaxConcurrentRequests: got %d, want %d", cfg.MaxConcurrentRequests, DefaultMaxConcurrentRequests)
	}
}

// TestLoadSettings_ZeroAndNegativeSanitised verifies that zero or negative
// values for numeric limits are replaced with safe defaults.
// AC: zero/negative sanitised to safe defaults.
func TestLoadSettings_ZeroAndNegativeSanitised(t *testing.T) {
	data := mustJSON(map[string]any{
		"maxResponseBytes":           0,
		"requestTimeoutSec":          -1,
		"maxRedirects":               -5,
		"rateLimitPerInstancePerMin": -10,
		"rateLimitPerDomainPerMin":   0,
		"maxConcurrentRequests":      -1,
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if cfg.MaxResponseBytes != DefaultMaxResponseBytes {
		t.Errorf("MaxResponseBytes: got %d, want %d", cfg.MaxResponseBytes, DefaultMaxResponseBytes)
	}
	if cfg.RequestTimeoutSec != DefaultRequestTimeoutSec {
		t.Errorf("RequestTimeoutSec: got %d, want %d", cfg.RequestTimeoutSec, DefaultRequestTimeoutSec)
	}
	if cfg.MaxRedirects != DefaultMaxRedirects {
		t.Errorf("MaxRedirects: got %d, want %d", cfg.MaxRedirects, DefaultMaxRedirects)
	}
	if cfg.RateLimitPerInstancePerMin != DefaultRateLimitPerInstancePerMin {
		t.Errorf("RateLimitPerInstancePerMin: got %d, want %d", cfg.RateLimitPerInstancePerMin, DefaultRateLimitPerInstancePerMin)
	}
	if cfg.RateLimitPerDomainPerMin != DefaultRateLimitPerDomainPerMin {
		t.Errorf("RateLimitPerDomainPerMin: got %d, want %d", cfg.RateLimitPerDomainPerMin, DefaultRateLimitPerDomainPerMin)
	}
	if cfg.MaxConcurrentRequests != DefaultMaxConcurrentRequests {
		t.Errorf("MaxConcurrentRequests: got %d, want %d", cfg.MaxConcurrentRequests, DefaultMaxConcurrentRequests)
	}
}

// TestLoadSettings_MaxRedirectsZeroAllowed verifies that maxRedirects=0 is
// preserved as a valid value (disables redirect following), rather than being
// replaced with the default.
func TestLoadSettings_MaxRedirectsZeroAllowed(t *testing.T) {
	data := mustJSON(map[string]any{
		"maxRedirects": 0,
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if cfg.MaxRedirects != 0 {
		t.Errorf("MaxRedirects: got %d, want 0 (zero is valid — disables redirects)", cfg.MaxRedirects)
	}
}

// TestLoadSettings_AllowedDomainsEmpty verifies the fail-closed behaviour when
// allowedDomains is explicitly set to an empty array.
// AC: Empty allowlist default is explicit and tested.
func TestLoadSettings_AllowedDomainsEmpty(t *testing.T) {
	data := mustJSON(map[string]any{
		"allowedDomains": []any{},
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if len(cfg.AllowedDomains) != 0 {
		t.Errorf("AllowedDomains: got %d entries, want 0", len(cfg.AllowedDomains))
	}
}

// TestLoadSettings_PerDomainOptionsParsed verifies that per-domain options are
// correctly parsed from JSONData, including non-default values.
// AC: Per-domain options structure is defined and exported for SF3 to consume.
func TestLoadSettings_PerDomainOptionsParsed(t *testing.T) {
	data := mustJSON(map[string]any{
		"allowedDomains": []any{
			map[string]any{
				"domain": "example.com",
				"options": map[string]any{
					"allowSubdomains": true,
					"allowPrivateIP":  false,
					"rateLimitPerMin": 15,
					"allowedPorts":    []int{8080, 9000},
				},
			},
			map[string]any{
				"domain": "internal.corp",
				"options": map[string]any{
					"allowSubdomains": false,
					"allowPrivateIP":  true,
					"rateLimitPerMin": 0,
					"allowedPorts":    []int{},
				},
			},
		},
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if len(cfg.AllowedDomains) != 2 {
		t.Fatalf("AllowedDomains: got %d entries, want 2", len(cfg.AllowedDomains))
	}

	// First domain: example.com
	d0 := cfg.AllowedDomains[0]
	if d0.Domain != "example.com" {
		t.Errorf("AllowedDomains[0].Domain: got %q, want %q", d0.Domain, "example.com")
	}
	if !d0.Options.AllowSubdomains {
		t.Errorf("AllowedDomains[0].Options.AllowSubdomains: got false, want true")
	}
	if d0.Options.AllowPrivateIP {
		t.Errorf("AllowedDomains[0].Options.AllowPrivateIP: got true, want false")
	}
	if d0.Options.RateLimitPerMin != 15 {
		t.Errorf("AllowedDomains[0].Options.RateLimitPerMin: got %d, want 15", d0.Options.RateLimitPerMin)
	}
	if len(d0.Options.AllowedPorts) != 2 || d0.Options.AllowedPorts[0] != 8080 || d0.Options.AllowedPorts[1] != 9000 {
		t.Errorf("AllowedDomains[0].Options.AllowedPorts: got %v, want [8080 9000]", d0.Options.AllowedPorts)
	}

	// Second domain: internal.corp
	d1 := cfg.AllowedDomains[1]
	if d1.Domain != "internal.corp" {
		t.Errorf("AllowedDomains[1].Domain: got %q, want %q", d1.Domain, "internal.corp")
	}
	if d1.Options.AllowSubdomains {
		t.Errorf("AllowedDomains[1].Options.AllowSubdomains: got true, want false")
	}
	if !d1.Options.AllowPrivateIP {
		t.Errorf("AllowedDomains[1].Options.AllowPrivateIP: got false, want true")
	}
	if d1.Options.RateLimitPerMin != 0 {
		t.Errorf("AllowedDomains[1].Options.RateLimitPerMin: got %d, want 0", d1.Options.RateLimitPerMin)
	}
}

// TestLoadSettings_InvalidJSON verifies that malformed JSONData returns a
// descriptive error rather than silently applying defaults.
func TestLoadSettings_InvalidJSON(t *testing.T) {
	_, err := LoadSettings(backend.AppInstanceSettings{JSONData: []byte(`{bad json}`)})
	if err == nil {
		t.Error("LoadSettings with invalid JSON: expected error, got nil")
	}
}

// TestLoadSettings_FullConfig verifies that a fully-populated JSONData is
// parsed correctly with no fields overridden by defaults.
// AC: Settings schema covers all required fields with documented defaults.
func TestLoadSettings_FullConfig(t *testing.T) {
	data := mustJSON(map[string]any{
		"allowedDomains": []any{
			map[string]any{
				"domain": "grafana.com",
				"options": map[string]any{
					"allowSubdomains": true,
					"allowPrivateIP":  false,
					"rateLimitPerMin": 20,
					"allowedPorts":    []int{},
				},
			},
		},
		"maxResponseBytes":           int64(1 * 1024 * 1024),
		"requestTimeoutSec":          5,
		"maxRedirects":               1,
		"rateLimitPerInstancePerMin": 120,
		"rateLimitPerDomainPerMin":   60,
		"maxConcurrentRequests":      20,
	})

	cfg, err := LoadSettings(backend.AppInstanceSettings{JSONData: data})
	if err != nil {
		t.Fatalf("LoadSettings error: %v", err)
	}

	if len(cfg.AllowedDomains) != 1 || cfg.AllowedDomains[0].Domain != "grafana.com" {
		t.Errorf("AllowedDomains: unexpected value %+v", cfg.AllowedDomains)
	}
	if cfg.MaxResponseBytes != 1*1024*1024 {
		t.Errorf("MaxResponseBytes: got %d, want %d", cfg.MaxResponseBytes, 1*1024*1024)
	}
	if cfg.RequestTimeoutSec != 5 {
		t.Errorf("RequestTimeoutSec: got %d, want 5", cfg.RequestTimeoutSec)
	}
	if cfg.MaxRedirects != 1 {
		t.Errorf("MaxRedirects: got %d, want 1", cfg.MaxRedirects)
	}
	if cfg.RateLimitPerInstancePerMin != 120 {
		t.Errorf("RateLimitPerInstancePerMin: got %d, want 120", cfg.RateLimitPerInstancePerMin)
	}
	if cfg.RateLimitPerDomainPerMin != 60 {
		t.Errorf("RateLimitPerDomainPerMin: got %d, want 60", cfg.RateLimitPerDomainPerMin)
	}
	if cfg.MaxConcurrentRequests != 20 {
		t.Errorf("MaxConcurrentRequests: got %d, want 20", cfg.MaxConcurrentRequests)
	}
}

package plugin

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// Default values for plugin settings (fail-closed safe defaults).
// These are applied whenever a field is absent, zero, or otherwise invalid.
const (
	// DefaultMaxResponseBytes is the maximum allowed response body size for a
	// proxied request. Responses exceeding this limit are rejected with 413.
	// Default: 5 MiB.
	DefaultMaxResponseBytes = 5 * 1024 * 1024

	// DefaultRequestTimeoutSec is the per-request timeout in seconds covering
	// both connection establishment and the full response transfer.
	// Default: 10 seconds.
	DefaultRequestTimeoutSec = 10

	// DefaultMaxRedirects is the maximum number of HTTP redirects followed for
	// a single proxy request. Each redirect destination is re-validated against
	// the allowlist and IP blocklist.
	// Default: 3.
	DefaultMaxRedirects = 3

	// DefaultRateLimitPerInstancePerMin is the maximum proxy requests per minute
	// across the entire Grafana instance. Implemented as an in-process token
	// bucket. Default: 60 req/min.
	DefaultRateLimitPerInstancePerMin = 60

	// DefaultRateLimitPerDomainPerMin is the maximum proxy requests per minute
	// directed at any single target domain. Default: 30 req/min.
	DefaultRateLimitPerDomainPerMin = 30

	// DefaultMaxConcurrentRequests is the maximum number of proxy requests that
	// may be in-flight simultaneously. Default: 10.
	DefaultMaxConcurrentRequests = 10
)

// DomainOptions holds per-domain configuration that refines the behaviour of
// the security controls for a specific allowed domain. It is stored as part of
// each entry in AllowedDomains.
//
// This struct is the canonical definition consumed by the security-foundation
// stream (SF3) when implementing allowlist matching.
type DomainOptions struct {
	// AllowSubdomains controls whether the allowlist entry also covers all
	// subdomains of the configured domain (e.g. "example.com" also covers
	// "api.example.com"). Default: false (exact-match only).
	AllowSubdomains bool `json:"allowSubdomains"`

	// AllowPrivateIP is an explicit opt-in that permits the resolved IP address
	// of this domain to fall within RFC 1918 / link-local / loopback ranges.
	// This MUST remain false in almost all deployments; it exists only for
	// internal-status-board use cases where the admin has accepted the risk.
	// Default: false.
	AllowPrivateIP bool `json:"allowPrivateIP"`

	// RateLimitPerMin overrides the global per-domain rate limit for this
	// specific domain. A value of 0 means "use the global default".
	// Default: 0 (use global default).
	RateLimitPerMin int `json:"rateLimitPerMin"`

	// AllowedPorts lists additional TCP ports (beyond 80 and 443) that are
	// permitted for this domain. An empty slice means only 80 and 443 are
	// allowed. Default: empty (standard ports only).
	AllowedPorts []int `json:"allowedPorts"`
}

// AllowedDomain pairs a hostname pattern with its per-domain options.
// The Domain field is the bare hostname (e.g. "example.com") without scheme
// or path. Matching semantics (exact vs. subdomain) are governed by
// Options.AllowSubdomains.
type AllowedDomain struct {
	// Domain is the hostname (or hostname pattern) that is permitted.
	// Must not include a scheme, port, or path component.
	Domain string `json:"domain"`

	// Options contains per-domain overrides for security controls.
	Options DomainOptions `json:"options"`
}

// PluginSettings contains all admin-configurable settings for the Web View
// plugin. These are stored in Grafana's non-secret jsonData for the plugin
// instance and can be managed via the Grafana plugin settings UI.
//
// IMPORTANT — fail-closed defaults: every security-relevant field defaults to
// the most restrictive safe value. In particular, AllowedDomains is empty by
// default, which means a fresh install proxies nothing (all proxy requests
// return 403) until the admin explicitly adds allowed domains.
//
// None of these fields belong in secureJsonData because they are not secrets;
// they are operational configuration that admins should be able to see and
// audit.
type PluginSettings struct {
	// AllowedDomains is the explicit allowlist of domains that the proxy is
	// permitted to fetch on behalf of dashboard viewers. An empty list is the
	// default and means the proxy refuses all requests. The admin must
	// explicitly add domains here before the plugin will proxy anything.
	//
	// FAIL-CLOSED: missing or empty => deny all proxy requests.
	AllowedDomains []AllowedDomain `json:"allowedDomains"`

	// MaxResponseBytes is the maximum response body size in bytes that the
	// proxy will accept from a target server. Responses larger than this limit
	// are rejected with 413 Payload Too Large.
	// Default: 5 MiB (5 * 1024 * 1024).
	MaxResponseBytes int64 `json:"maxResponseBytes"`

	// RequestTimeoutSec is the per-request timeout in seconds. This covers both
	// TCP connection establishment and the total duration of the response
	// transfer. Requests that exceed this limit are cancelled with 504.
	// Default: 10.
	RequestTimeoutSec int `json:"requestTimeoutSec"`

	// MaxRedirects is the maximum number of HTTP redirects the proxy will
	// follow for a single request. Each redirect target is independently
	// validated against the allowlist and IP blocklist. Setting this to 0
	// disables redirect following entirely.
	// Default: 3.
	//
	// Note: this field uses a pointer internally during JSON parsing so that
	// an explicitly supplied value of 0 (disable redirects) is distinguished
	// from an absent field (which should fall back to DefaultMaxRedirects).
	// Callers always see the resolved int value — the pointer is an
	// implementation detail of LoadSettings.
	MaxRedirects int `json:"maxRedirects"`

	// RateLimitPerInstancePerMin is the maximum total number of proxy requests
	// the plugin will serve per minute across the entire Grafana instance.
	// Implemented as a token-bucket limiter. Requests that exceed the limit
	// receive 429 Too Many Requests.
	// Default: 60.
	RateLimitPerInstancePerMin int `json:"rateLimitPerInstancePerMin"`

	// RateLimitPerDomainPerMin is the maximum proxy requests per minute
	// directed at any single target domain. This prevents a single domain from
	// consuming the full instance rate limit.
	// Default: 30.
	RateLimitPerDomainPerMin int `json:"rateLimitPerDomainPerMin"`

	// MaxConcurrentRequests is the maximum number of proxy requests that may
	// be in-flight at the same time. Requests that arrive when the cap is
	// reached receive 429.
	// Default: 10.
	MaxConcurrentRequests int `json:"maxConcurrentRequests"`
}

// rawPluginSettings is the intermediate struct used when unmarshalling
// JSONData. MaxRedirects is a pointer so that a JSON value of 0 is
// distinguishable from the field being absent (omitted), allowing
// LoadSettings to apply DefaultMaxRedirects only when truly missing.
type rawPluginSettings struct {
	AllowedDomains             []AllowedDomain `json:"allowedDomains"`
	MaxResponseBytes           int64           `json:"maxResponseBytes"`
	RequestTimeoutSec          int             `json:"requestTimeoutSec"`
	MaxRedirects               *int            `json:"maxRedirects"`
	RateLimitPerInstancePerMin int             `json:"rateLimitPerInstancePerMin"`
	RateLimitPerDomainPerMin   int             `json:"rateLimitPerDomainPerMin"`
	MaxConcurrentRequests      int             `json:"maxConcurrentRequests"`
}

// LoadSettings parses the plugin settings from the provided AppInstanceSettings
// and applies safe defaults for any field that is absent, zero, or invalid.
//
// The JSONData field of AppInstanceSettings is the non-secret plugin
// configuration stored and managed by Grafana (equivalent to jsonData in the
// plugin settings UI). Only PluginSettings fields belong there — no secrets.
//
// Fail-closed behaviour: if JSONData is empty or missing, all defaults apply
// and AllowedDomains remains empty, so the plugin refuses to proxy anything
// until explicitly configured by an admin.
//
// Special case for MaxRedirects: 0 is a valid configured value (disables
// redirect following) and is preserved as-is. Only a missing field causes
// DefaultMaxRedirects to be applied.
func LoadSettings(settings backend.AppInstanceSettings) (PluginSettings, error) {
	raw := rawPluginSettings{}

	if len(settings.JSONData) > 0 {
		if err := json.Unmarshal(settings.JSONData, &raw); err != nil {
			return PluginSettings{}, fmt.Errorf("parsing plugin settings JSONData: %w", err)
		}
	}

	cfg := PluginSettings{
		AllowedDomains:             raw.AllowedDomains,
		MaxResponseBytes:           raw.MaxResponseBytes,
		RequestTimeoutSec:          raw.RequestTimeoutSec,
		RateLimitPerInstancePerMin: raw.RateLimitPerInstancePerMin,
		RateLimitPerDomainPerMin:   raw.RateLimitPerDomainPerMin,
		MaxConcurrentRequests:      raw.MaxConcurrentRequests,
	}

	// MaxRedirects: nil means the field was absent from JSONData → use default.
	// A pointer value of 0 means "disable redirects" and is preserved.
	if raw.MaxRedirects != nil {
		cfg.MaxRedirects = *raw.MaxRedirects
	} else {
		cfg.MaxRedirects = DefaultMaxRedirects
	}

	applyDefaults(&cfg)
	return cfg, nil
}

// applyDefaults fills in safe default values for any PluginSettings field that
// is zero or negative. AllowedDomains is intentionally left as-is (nil/empty
// is the correct fail-closed default — it means "deny all").
//
// MaxRedirects is NOT defaulted here because its zero value is legitimate
// (disable redirects); the absent-vs-zero distinction is handled in
// LoadSettings using a pointer during JSON unmarshalling.
func applyDefaults(cfg *PluginSettings) {
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if cfg.RequestTimeoutSec <= 0 {
		cfg.RequestTimeoutSec = DefaultRequestTimeoutSec
	}
	if cfg.MaxRedirects < 0 {
		// Negative values are invalid; clamp to the safe default.
		// 0 has already been set intentionally by LoadSettings.
		cfg.MaxRedirects = DefaultMaxRedirects
	}
	if cfg.RateLimitPerInstancePerMin <= 0 {
		cfg.RateLimitPerInstancePerMin = DefaultRateLimitPerInstancePerMin
	}
	if cfg.RateLimitPerDomainPerMin <= 0 {
		cfg.RateLimitPerDomainPerMin = DefaultRateLimitPerDomainPerMin
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	}
}

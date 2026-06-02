package plugin

import "github.com/wilsonwaters/webview/pkg/security"

// toAllowlistEntries maps the plugin-package allowlist configuration
// (PluginSettings.AllowedDomains) onto the dependency-free security-package
// input type (security.AllowlistEntry).
//
// pkg/security is a leaf package that deliberately does not import pkg/plugin
// (see allowlist.go's type doc), so this mapping lives here, at the consuming
// boundary. The two struct shapes mirror each other field-for-field; this
// shim is the single place the two type families are bridged.
//
// A nil or empty AllowedDomains slice maps to a nil entry slice, which
// MatchHostname treats as fail-closed (deny all) — preserving the empty-by-
// default allowlist semantics.
func toAllowlistEntries(domains []AllowedDomain) []security.AllowlistEntry {
	if len(domains) == 0 {
		return nil
	}
	entries := make([]security.AllowlistEntry, 0, len(domains))
	for _, d := range domains {
		entries = append(entries, security.AllowlistEntry{
			Domain:  d.Domain,
			Options: toEntryOptions(d.Options),
		})
	}
	return entries
}

// toEntryOptions maps a single plugin.DomainOptions onto the security-package
// security.EntryOptions. AllowedPorts is copied so the security package never
// aliases the caller's slice.
func toEntryOptions(o DomainOptions) security.EntryOptions {
	var ports []int
	if len(o.AllowedPorts) > 0 {
		ports = make([]int, len(o.AllowedPorts))
		copy(ports, o.AllowedPorts)
	}
	return security.EntryOptions{
		AllowSubdomains: o.AllowSubdomains,
		AllowPrivateIP:  o.AllowPrivateIP,
		RateLimitPerMin: o.RateLimitPerMin,
		AllowedPorts:    ports,
	}
}

// domainRateOverrides builds the per-domain rate-limit override map that
// security.NewRateLimiter consumes. The map is keyed by the normalised
// hostname so the keys line up with the domain string passed to
// RateLimiter.Allow at request time (which is the normalised hostname produced
// by ValidateURL). Only positive overrides are included; NewRateLimiter
// additionally drops any non-positive value defensively. An entry with no
// override (0) is omitted so the global per-domain default applies. A domain
// whose configured value cannot be normalised is skipped.
func domainRateOverrides(domains []AllowedDomain) map[string]int {
	overrides := make(map[string]int)
	for _, d := range domains {
		if d.Options.RateLimitPerMin <= 0 {
			continue
		}
		canon, err := security.NormalizeHostname(d.Domain)
		if err != nil {
			continue
		}
		overrides[canon] = d.Options.RateLimitPerMin
	}
	return overrides
}

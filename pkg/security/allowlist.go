// Package security provides the hardcoded, non-configurable security building
// blocks shared by every proxying endpoint.
//
// This file adds the allowlist matcher: the third such block. Given the
// admin-configured domain allowlist (PluginSettings.AllowedDomains) it decides
// whether a request hostname is permitted, using exact matching and — when the
// matched entry opts in — subdomain matching, and returns the matched
// per-domain options so downstream controls (SF2 port checks, SF4 private-IP
// opt-in, SF5 rate limiting) can apply the right policy.
//
// The matcher is a pure library. It performs no DNS resolution (SF4), no URL
// scheme/port validation (SF2), and no rate limiting (SF5); it reads no
// settings of its own — the allowlist is supplied by the caller. Like the IP
// blocklist and URL validator it fails closed: an empty or nil allowlist denies
// every hostname, and any configured entry that cannot be canonicalised is
// skipped rather than matched loosely.
//
// Canonicalisation is shared with the URL validator: both the query hostname
// and every configured Domain are folded through NormalizeHostname before
// comparison, so case, trailing-dot, and IDN/Unicode/homograph differences fold
// to the same ASCII form on both sides and cannot be used to slip past — or
// spoof — an allowlist entry.
package security

// AllowlistEntry pairs a configured hostname with its per-domain options, as
// consumed by MatchHostname. It is a security-owned input type: the fields
// mirror plugin.AllowedDomain / plugin.DomainOptions exactly, but the type is
// defined here so that pkg/security imports no project package and stays a
// dependency-free leaf (consistent with the IP blocklist taking net.IP and the
// URL validator taking string+[]int).
//
// The consuming proxy/frameability endpoint is responsible for mapping each
// configured plugin.AllowedDomain to an AllowlistEntry at the call site; this
// package deliberately does not import pkg/plugin to perform that mapping.
type AllowlistEntry struct {
	// Domain is the configured hostname (e.g. "example.com") without scheme,
	// port, or path. Mirrors plugin.AllowedDomain.Domain.
	Domain string
	// Options carries the per-domain controls for a matched entry. Mirrors
	// plugin.AllowedDomain.Options.
	Options EntryOptions
}

// EntryOptions carries the per-domain security controls for an allowlist entry.
// Its fields mirror plugin.DomainOptions exactly; it is defined here to keep
// pkg/security a leaf package. The endpoint maps plugin.DomainOptions →
// EntryOptions when constructing the AllowlistEntry slice it passes in.
type EntryOptions struct {
	// AllowSubdomains controls whether the entry also covers all subdomains of
	// the configured domain (e.g. "example.com" also covers "api.example.com").
	AllowSubdomains bool
	// AllowPrivateIP is an explicit opt-in permitting the resolved IP to fall
	// within private/link-local/loopback ranges (consumed by SF4).
	AllowPrivateIP bool
	// RateLimitPerMin overrides the global per-domain rate limit for this domain
	// (consumed by SF5). 0 means "use the global default".
	RateLimitPerMin int
	// AllowedPorts lists additional TCP ports (beyond 80 and 443) permitted for
	// this domain (consumed by SF2). Empty means standard ports only.
	AllowedPorts []int
}

// Result tokens returned by MatchHostname. These are short, stable, machine
// readable identifiers suitable for use as metric and audit-log labels. They
// must remain stable because consumers may key alerts and dashboards on them.
const (
	// ResultExact means the hostname matched an allowlist entry's Domain
	// exactly (after canonicalisation on both sides).
	ResultExact = "exact"
	// ResultSubdomain means the hostname is a subdomain of an allowlist entry
	// whose Options.AllowSubdomains is true.
	ResultSubdomain = "subdomain"
	// ResultNoMatch means no allowlist entry covers the hostname (including the
	// fail-closed empty/nil allowlist case).
	ResultNoMatch = "no-match"
	// ResultInvalidInput means the query hostname itself was empty or could not
	// be canonicalised; such input is denied (fail-closed).
	ResultInvalidInput = "invalid-input"
)

// Match is the outcome of an allowlist lookup. When Allowed is true, Result is
// ResultExact or ResultSubdomain, Domain is the canonicalised configured domain
// that matched, and Options carries that entry's per-domain options for
// downstream controls. When Allowed is false, Result is ResultNoMatch or
// ResultInvalidInput and the other fields are zero values.
type Match struct {
	// Allowed reports whether the hostname is permitted by the allowlist.
	Allowed bool
	// Result is one of the stable Result* constants describing how (or why not)
	// the hostname matched.
	Result string
	// Domain is the canonicalised configured Domain that matched (empty when
	// not allowed). This is the normalised apex, not the query hostname.
	Domain string
	// Options is a copy of the matched entry's per-domain options (subdomain
	// flag, private-IP opt-in, rate-limit override, extra allowed ports). It is
	// the zero EntryOptions when not allowed. Callers feed these into SF2
	// (AllowedPorts), SF4 (AllowPrivateIP), and SF5 (RateLimitPerMin).
	Options EntryOptions
}

// MatchHostname reports whether hostname is permitted by the supplied
// allowlist, returning the matched per-domain options on success.
//
// Both hostname and each entry's Domain are canonicalised via NormalizeHostname
// before comparison, so the comparison is case-insensitive, trailing-dot
// insensitive, and IDN/Unicode-folded on both sides (matching the exact form
// the SF2 URL validator produces for request hosts).
//
// Matching rules:
//
//   - Exact: the canonical hostname equals the canonical Domain. Always applies.
//   - Subdomain: when the entry's Options.AllowSubdomains is true, the entry
//     also covers any hostname ending in "." + canonical Domain (e.g.
//     "example.com" covers "api.example.com" and "a.b.example.com"). Partial
//     label matches ("notexample.com") and suffix tricks
//     ("example.com.evil.com") do not match.
//
// Fail-closed behaviour:
//
//   - An empty or nil allowlist denies every hostname (ResultNoMatch).
//   - An empty or un-normalisable query hostname is denied (ResultInvalidInput).
//   - A configured entry whose Domain is empty or cannot be canonicalised is
//     skipped (it can never match); it does not poison the rest of the list.
//
// When more than one entry matches, an exact match is preferred over a
// subdomain match; among entries of the same kind the first in allowlist order
// wins, so the returned Options are deterministic.
func MatchHostname(hostname string, allowlist []AllowlistEntry) Match {
	canonHost, err := NormalizeHostname(hostname)
	if err != nil {
		// The query hostname cannot be proven to be a valid name, so it cannot
		// be safely compared against the allowlist. Deny.
		return Match{Allowed: false, Result: ResultInvalidInput}
	}

	// Tracks the first subdomain match found, if any. An exact match found later
	// is preferred, so we only fall back to this after scanning the whole list.
	var (
		haveSubdomain   bool
		subdomainDomain string
		subdomainOpts   EntryOptions
	)

	// A nil or empty allowlist denies everything (the loop simply never runs).
	// This is the safe default for a fresh install.
	for i := range allowlist {
		entry := &allowlist[i]
		canonDomain, derr := NormalizeHostname(entry.Domain)
		if derr != nil {
			// An entry that cannot be canonicalised (empty or invalid Domain) is
			// unusable: skip it rather than risk a loose or accidental match.
			continue
		}

		if canonHost == canonDomain {
			// Exact match wins immediately; no later entry can outrank it.
			return Match{
				Allowed: true,
				Result:  ResultExact,
				Domain:  canonDomain,
				Options: entry.Options,
			}
		}

		// Subdomain match only when the entry opts in. The host must end in
		// ".<domain>" so that only true sub-labels match: this rejects both
		// partial-label matches ("notexample.com" vs "example.com") and the
		// suffix trick ("example.com.evil.com" vs "example.com", which does not
		// end in ".example.com"). Record the first such match but keep scanning
		// in case a later entry is an exact match, which is preferred.
		if entry.Options.AllowSubdomains && !haveSubdomain && isSubdomainOf(canonHost, canonDomain) {
			haveSubdomain = true
			subdomainDomain = canonDomain
			subdomainOpts = entry.Options
		}
	}

	if haveSubdomain {
		return Match{
			Allowed: true,
			Result:  ResultSubdomain,
			Domain:  subdomainDomain,
			Options: subdomainOpts,
		}
	}

	return Match{Allowed: false, Result: ResultNoMatch}
}

// IsHostnameAllowed is a convenience wrapper around MatchHostname for call sites
// that need a plain boolean gate and do not care about the matched options or
// the reason. It fails closed identically to MatchHostname.
func IsHostnameAllowed(hostname string, allowlist []AllowlistEntry) bool {
	return MatchHostname(hostname, allowlist).Allowed
}

// isSubdomainOf reports whether host is a strict subdomain of domain: host must
// be longer than domain and end in "." + domain. Both arguments are expected to
// already be canonicalised (lowercased, IDN-folded, no trailing dot). A host
// equal to domain is NOT a subdomain (that is the exact-match case), and a host
// that merely shares a suffix without a label boundary (e.g. "notexample.com"
// against "example.com") is rejected because it does not contain the leading
// dot separator.
func isSubdomainOf(host, domain string) bool {
	// Belt-and-suspenders: callers only reach here with a non-empty, normalised
	// domain (empty/un-normalisable entries are skipped before this call), but
	// guard anyway so an empty domain can never make every host a "subdomain".
	if domain == "" {
		return false
	}
	suffix := "." + domain
	// len(host) > len(suffix) guarantees at least one non-empty label precedes
	// the dot, so ".example.com" itself (empty leading label) does not match.
	return len(host) > len(suffix) && host[len(host)-len(suffix):] == suffix
}

// Package security provides the hardcoded, non-configurable security building
// blocks shared by every proxying endpoint.
//
// This file adds the URL validator: the second such block. It enforces a
// scheme allowlist (http/https only), restricts the target port to the
// scheme defaults (80/443) plus any extra ports the caller passes in for a
// matched domain, normalises the hostname (lowercase, trailing-dot removal,
// IDN/Unicode-to-ASCII punycode), and rejects malformed URLs, missing hosts,
// and embedded credentials.
//
// The validator is a pure library. It performs no allowlist matching (SF3),
// no DNS resolution (SF4), and reads no settings: the per-domain extra ports
// are supplied by the caller. Like the IP blocklist it fails closed — any
// input that cannot be proven safe is rejected. The normalised hostname is
// returned so the downstream allowlist matcher (SF3) can match on the exact
// same canonical form this validator produced.
package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// Reason tokens returned by ValidateURL. These are short, stable, machine
// readable identifiers suitable for use as metric and audit-log labels. They
// must remain stable because consumers may key alerts and dashboards on them.
const (
	// ReasonMalformed covers URLs that cannot be parsed, are not absolute, or
	// are otherwise structurally invalid.
	ReasonMalformed = "malformed"
	// ReasonScheme covers URLs whose scheme is not http or https.
	ReasonScheme = "scheme"
	// ReasonUserinfo covers URLs that embed credentials (user[:password]@host).
	ReasonUserinfo = "userinfo"
	// ReasonHostname covers a missing, empty, or un-normalisable hostname
	// (including IDN inputs that cannot be converted to ASCII).
	ReasonHostname = "hostname"
	// ReasonPort covers an explicit port that is neither a scheme default
	// (80/443) nor one of the caller-supplied extra allowed ports.
	ReasonPort = "port"
)

// Default ports that are always permitted, keyed by scheme. A URL with no
// explicit port is treated as using its scheme default and is always allowed.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"

	defaultPortHTTP  = 80
	defaultPortHTTPS = 443
)

// ValidatedURL is the normalised, validated result returned by ValidateURL.
// Every field is in canonical form ready for downstream use: Scheme is
// lowercase http or https, Hostname is the lowercased, trailing-dot-stripped,
// punycode-encoded ASCII host, and Port is the effective TCP port (the
// scheme default when the URL had no explicit port).
type ValidatedURL struct {
	// Scheme is the validated, lowercased scheme: "http" or "https".
	Scheme string
	// Hostname is the normalised ASCII hostname (see NormalizeHostname).
	Hostname string
	// Port is the effective TCP port: the explicit port if present, otherwise
	// the scheme default (80 for http, 443 for https).
	Port int
}

// ValidationError carries a stable Reason token alongside a human-readable
// message. Callers that need to branch on the failure class should inspect
// Reason (one of the Reason* constants); the message is for logs only.
type ValidationError struct {
	// Reason is one of the stable Reason* constants.
	Reason string
	// Message is a human-readable explanation, not suitable as a metric label.
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("url validation failed (%s): %s", e.Reason, e.Message)
}

// ReasonOf extracts the stable Reason token from an error returned by
// ValidateURL. It returns the empty string if err is nil or is not a
// *ValidationError, letting callers map any validation failure to a label
// without type-asserting at every call site.
func ReasonOf(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return ""
}

func newError(reason, format string, args ...any) *ValidationError {
	return &ValidationError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// NormalizeHostname returns the canonical ASCII form of a hostname: it strips a
// single trailing dot, lowercases the result, and converts any IDN/Unicode
// labels to their ASCII punycode (xn--) representation using the IDNA2008
// lookup profile.
//
// It is exported so the downstream allowlist matcher (SF3) can canonicalise
// admin-configured domains with the exact same rules the validator applies to
// request URLs, guaranteeing both sides compare equal strings.
//
// An empty host, or a host that cannot be encoded as a valid IDNA name,
// returns an error: callers must treat that as a rejection (fail closed).
func NormalizeHostname(host string) (string, error) {
	if host == "" {
		return "", newError(ReasonHostname, "empty hostname")
	}
	// Strip a single trailing dot (the DNS root label) if present. A host that
	// is only "." has no labels and is invalid.
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
		if host == "" {
			return "", newError(ReasonHostname, "hostname is only a trailing dot")
		}
	}
	// IP-literal hosts (IPv4 dotted-quad or IPv6, the latter already de-bracketed
	// by url.Hostname()) are not domain names and must not be run through the
	// IDNA profile, which rejects the ':' in IPv6 literals. Return the canonical
	// textual form so the downstream IP blocklist (SF1) sees a stable value.
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	// idna.Lookup applies the strict IDNA2008 lookup profile, which lowercases,
	// validates labels, and produces punycode for non-ASCII input. It rejects
	// names that are not usable for lookup (e.g. disallowed code points).
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", newError(ReasonHostname, "cannot normalise hostname %q: %v", host, err)
	}
	// ToASCII preserves case for already-ASCII input, so lowercase explicitly to
	// guarantee a canonical, case-insensitive result for every code path.
	ascii = strings.ToLower(ascii)
	if ascii == "" {
		return "", newError(ReasonHostname, "hostname normalised to empty string")
	}
	return ascii, nil
}

// ValidateURL parses and validates rawURL against the security rules and, on
// success, returns its normalised components. It fails closed: any parse
// error, disallowed scheme, embedded credential, missing host, un-normalisable
// hostname, or disallowed port yields a *ValidationError carrying a stable
// Reason token (one of the Reason* constants).
//
// extraAllowedPorts is the per-domain list of additional TCP ports the caller
// permits for the matched domain (sourced from DomainOptions.AllowedPorts in
// plugin settings). Ports 80 and 443 are always allowed regardless of this
// list. A URL with no explicit port uses its scheme default and always passes
// the port check. ValidateURL does no allowlist lookup itself — the caller
// supplies these ports.
func ValidateURL(rawURL string, extraAllowedPorts []int) (*ValidatedURL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, newError(ReasonMalformed, "empty URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, newError(ReasonMalformed, "cannot parse URL: %v", err)
	}

	// Scheme: must be present and one of http/https (case-insensitive; url.Parse
	// already lowercases the scheme, but normalise defensively).
	scheme := strings.ToLower(u.Scheme)
	if scheme != schemeHTTP && scheme != schemeHTTPS {
		return nil, newError(ReasonScheme, "scheme %q is not allowed (only http/https)", u.Scheme)
	}

	// Reject embedded credentials outright: they are a common SSRF/credential
	// leak vector and have no place in a proxied target URL.
	if u.User != nil {
		return nil, newError(ReasonUserinfo, "URL must not contain userinfo (credentials)")
	}

	// An absolute http(s) URL must be hierarchical (have an authority). Reject
	// opaque forms such as "http:example.com" that carry no host.
	if u.Opaque != "" {
		return nil, newError(ReasonMalformed, "URL must be hierarchical, got opaque form")
	}

	// Host (without port) and explicit port. u.Hostname()/u.Port() split the
	// authority and handle bracketed IPv6 literals.
	rawHost := u.Hostname()
	if rawHost == "" {
		return nil, newError(ReasonHostname, "URL has no host")
	}

	hostname, err := NormalizeHostname(rawHost)
	if err != nil {
		return nil, err
	}

	port, err := resolvePort(scheme, u.Port(), extraAllowedPorts)
	if err != nil {
		return nil, err
	}

	return &ValidatedURL{
		Scheme:   scheme,
		Hostname: hostname,
		Port:     port,
	}, nil
}

// resolvePort determines the effective port for scheme from the explicit port
// string (empty when the URL had none) and validates it against the always
// allowed scheme defaults plus the caller-supplied extra ports.
func resolvePort(scheme, explicit string, extraAllowedPorts []int) (int, error) {
	if explicit == "" {
		// No explicit port: use the scheme default, which is always allowed.
		if scheme == schemeHTTPS {
			return defaultPortHTTPS, nil
		}
		return defaultPortHTTP, nil
	}

	port, err := strconv.Atoi(explicit)
	if err != nil {
		// url.Hostname()/Port() should already have rejected non-numeric ports,
		// but fail closed if anything slips through.
		return 0, newError(ReasonPort, "port %q is not a valid integer", explicit)
	}
	// Valid TCP ports are 1..65535. Port 0 is not a connectable port.
	if port < 1 || port > 65535 {
		return 0, newError(ReasonPort, "port %d is out of range", port)
	}

	if port == defaultPortHTTP || port == defaultPortHTTPS {
		return port, nil
	}
	for _, extra := range extraAllowedPorts {
		if port == extra {
			return port, nil
		}
	}
	return 0, newError(ReasonPort, "port %d is not allowed", port)
}

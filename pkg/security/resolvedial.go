// Package security provides the hardcoded, non-configurable security building
// blocks shared by every proxying endpoint.
//
// This file adds the DNS-resolve-then-dial helper: the fourth such block. It
// closes the gap between validating a hostname and actually connecting to it,
// which is where DNS rebinding lives. The flow is:
//
//  1. Reject known cloud-metadata hostnames by name (a defence-in-depth layer
//     on top of the SF1 IP blocklist, which already blocks 169.254.169.254).
//  2. Resolve the hostname to its IP(s) through an injectable resolver.
//  3. Classify EVERY resolved IP through the SF1 blocklist (ClassifyIP). If any
//     single record is blocked the whole request is rejected (fail closed —
//     see DialContext for the rationale).
//  4. Dial the exact validated IP, not the hostname, so a later DNS answer
//     cannot redirect the connection. A net.Dialer.Control hook independently
//     re-validates the concrete IP the OS is about to connect to, catching a
//     rebind that slips between resolution and connect. It is the authoritative
//     gate for any IP that reaches the dial path.
//
// A note on obfuscated IP-literal encodings (decimal "2130706433", octal
// "0177.0.0.1", hex "0x7f.0.0.1"): Go's resolver does NOT parse these forms.
// Passed as a host they are treated as a hostname, sent to DNS, fail to resolve
// (NXDOMAIN) and are rejected with ReasonResolveFailed (fail closed) — they
// never reach Control. The residual risk is a *caller* that pre-parses such a
// form into a real IP (some libc getaddrinfo configurations do); that caller
// must classify the result through SF1 (ClassifyIP) before passing it here.
//
// Like the rest of this package it is a pure standard-library leaf: it imports
// no project packages, takes its only dependency (the resolver) as an injected
// value, and fails closed on anything it cannot prove safe. It does NO
// allowlist matching (SF3), URL/scheme/port validation (SF2), rate limiting
// (SF5), private-IP opt-in, or audit logging — those belong to the consuming
// endpoint. The SF1 blocklist is enforced strictly with no opt-in relaxation.
package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Reason tokens returned by the resolve-then-dial helper. These are short,
// stable, machine readable identifiers suitable for use as metric and
// audit-log labels and must remain stable because consumers may key alerts and
// dashboards on them. They sit alongside (and do not overlap with) the SF1
// ClassifyIP reasons, which are surfaced verbatim when a resolved IP is
// blocked.
const (
	// ReasonMetadataHost covers a hostname that matches a known cloud-metadata
	// name (for example metadata.google.internal). These are blocked by name
	// before any DNS resolution happens.
	ReasonMetadataHost = "metadata-host"
	// ReasonResolveFailed covers a DNS resolution that errored or returned no
	// addresses. Without an address the target cannot be proven safe, so it is
	// rejected (fail closed).
	ReasonResolveFailed = "resolve-failed"
	// ReasonNoHost covers an empty or all-whitespace hostname input.
	ReasonNoHost = "no-host"
	// ReasonBlockedIP covers a resolved (or connect-time) IP that the SF1
	// blocklist classified as blocked. The DialError wraps the specific SF1
	// reason (one of the ClassifyIP Reason* tokens) so callers can drill down.
	ReasonBlockedIP = "blocked-ip"
)

// metadataHostnames is the hardcoded set of cloud-metadata service hostnames
// that must never be reached, matched by name in addition to the SF1 IP-range
// check. Names are stored in their canonical lowercase, trailing-dot-stripped
// form; lookups normalise the input the same way. The set is intentionally
// unexported and has no setter: it cannot be extended or disabled at runtime.
//
// The corresponding metadata IP (169.254.169.254) is already blocked by SF1's
// link-local range; blocking by name as well stops a request whose hostname is
// a metadata alias from ever being resolved, and documents intent.
//
// This name set is GCP-specific by design: AWS, Azure and Alibaba expose their
// metadata service only via the bare 169.254.169.254 IP (no DNS name), which
// SF1 already blocks at the IP layer, so no name entries are needed for them.
var metadataHostnames = map[string]struct{}{
	"metadata.google.internal": {}, // GCP
	"metadata.goog":            {}, // GCP short alias
}

// DialError carries a stable Reason token alongside a human-readable message.
// When the failure is an SF1 IP-blocklist rejection, IPReason holds the
// specific ClassifyIP reason (e.g. "loopback") and BlockedIP holds the offending
// address, so callers can branch or label without re-classifying. Callers that
// only need the top-level class should inspect Reason (one of the Reason*
// constants); the message is for logs only.
type DialError struct {
	// Reason is one of the stable Reason* constants defined in this file.
	Reason string
	// IPReason is the SF1 ClassifyIP reason when Reason == ReasonBlockedIP,
	// otherwise empty.
	IPReason string
	// BlockedIP is the offending address when Reason == ReasonBlockedIP,
	// otherwise nil.
	BlockedIP net.IP
	// Message is a human-readable explanation, not suitable as a metric label.
	Message string
}

func (e *DialError) Error() string {
	return fmt.Sprintf("dial blocked (%s): %s", e.Reason, e.Message)
}

// DialReasonOf extracts the stable Reason token from an error returned by the
// resolve-then-dial helper. It returns the empty string if err is nil or does
// not wrap a *DialError, letting callers map any dial failure to a label
// without type-asserting at every call site. Because DialContext is used as a
// net.Dialer.DialContext, the error may be wrapped by the net stack, so this
// unwraps via errors.As.
func DialReasonOf(err error) string {
	var de *DialError
	if errors.As(err, &de) {
		return de.Reason
	}
	return ""
}

func newDialError(reason, format string, args ...any) *DialError {
	return &DialError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// Resolver is the minimal DNS lookup surface the helper depends on. It is
// satisfied by *net.Resolver (whose LookupIPAddr has this exact signature), so
// production callers pass net.DefaultResolver and tests pass a stub that
// returns canned addresses without touching the network.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// IsMetadataHostname reports whether host matches a known cloud-metadata
// service name. The input is normalised (trimmed, lowercased, single trailing
// dot stripped) before comparison so that "Metadata.Google.Internal." matches.
// It is exported so a consuming endpoint can reject metadata names early in its
// own pipeline if desired; DialContext applies it unconditionally.
func IsMetadataHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasSuffix(h, ".") {
		h = strings.TrimSuffix(h, ".")
	}
	_, ok := metadataHostnames[h]
	return ok
}

// ResolveAndValidate resolves host through the resolver and validates every
// returned address against the SF1 blocklist. It implements the fail-closed
// multi-record strategy: it returns the full list of resolved IPs only if ALL
// of them pass ClassifyIP; if any single record is blocked it returns a
// *DialError (ReasonBlockedIP with the offending IP and SF1 reason) and no
// addresses. A metadata hostname, an empty host, or a resolution error/empty
// answer is likewise rejected.
//
// Rationale for failing the whole request rather than dialing a "good" record:
// an attacker who controls DNS can return one public and one internal address;
// silently dialing the public one would leave the request's behaviour at the
// mercy of resolver ordering and would still hand the attacker a probe of which
// records are accepted. Rejecting outright removes the ambiguity.
func ResolveAndValidate(ctx context.Context, resolver Resolver, host string) ([]net.IP, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return nil, newDialError(ReasonNoHost, "empty hostname")
	}
	if IsMetadataHostname(h) {
		return nil, newDialError(ReasonMetadataHost, "hostname %q is a known cloud-metadata endpoint", h)
	}

	addrs, err := resolver.LookupIPAddr(ctx, h)
	if err != nil {
		return nil, newDialError(ReasonResolveFailed, "resolving %q: %v", h, err)
	}
	if len(addrs) == 0 {
		return nil, newDialError(ReasonResolveFailed, "resolving %q returned no addresses", h)
	}

	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if blocked, reason := ClassifyIP(a.IP); blocked {
			return nil, &DialError{
				Reason:    ReasonBlockedIP,
				IPReason:  reason,
				BlockedIP: a.IP,
				Message:   fmt.Sprintf("resolved IP %s for %q is blocked (%s)", a.IP, h, reason),
			}
		}
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// validateConnectAddr is the net.Dialer.Control body and the authoritative
// connect-time gate. It runs for the concrete address the OS is about to
// connect to, parses it as an IP literal, and re-classifies it through SF1.
// Re-classifying here is the defence-in-depth check that defeats a DNS rebind
// landing between resolution and connect: it does not trust the earlier
// ResolveAndValidate result, it validates the actual wire address. The address
// it receives is always a canonical dotted/colon IP literal because DialContext
// dials a net.JoinHostPort of an already-resolved net.IP; obfuscated literal
// encodings never reach this point (see the package doc — they fail DNS
// resolution upstream).
func validateConnectAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// No port, or otherwise unparseable: try the raw address, then fail
		// closed if it is not a usable IP.
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return newDialError(ReasonBlockedIP, "connect address %q is not a valid IP", address)
	}
	if blocked, reason := ClassifyIP(ip); blocked {
		return &DialError{
			Reason:    ReasonBlockedIP,
			IPReason:  reason,
			BlockedIP: ip,
			Message:   fmt.Sprintf("connect IP %s is blocked (%s)", ip, reason),
		}
	}
	return nil
}

// NewControl returns a net.Dialer.Control function that re-validates the exact
// IP the OS is about to connect to against the SF1 blocklist, rejecting the
// connection (before the socket connects) if it is blocked. It is exported so a
// caller assembling its own *net.Dialer or *net.Transport can install the same
// connect-time guard without using DialContext. The network and underlying
// syscall handle are unused; only the resolved address is inspected.
func NewControl() func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		return validateConnectAddr(address)
	}
}

// Dialer is the resolve-then-dial helper. It owns an injected Resolver and a
// base *net.Dialer whose options (timeout, keep-alive) the caller controls; the
// Control hook is always overwritten by this helper and must not be relied on
// by the caller. The zero value is not usable — construct one with NewDialer.
type Dialer struct {
	resolver Resolver
	base     *net.Dialer
}

// NewDialer constructs a Dialer. If resolver is nil, net.DefaultResolver is
// used. If base is nil, a zero-value *net.Dialer is used. A copy of base is
// taken so the caller's dialer is never mutated and so the Control hook this
// helper installs cannot leak back to the caller.
func NewDialer(resolver Resolver, base *net.Dialer) *Dialer {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	var d net.Dialer
	if base != nil {
		d = *base
	}
	return &Dialer{resolver: resolver, base: &d}
}

// DialContext resolves host (taken from addr, which is "host:port"), validates
// every resolved IP via SF1, then dials the FIRST validated IP directly using a
// net.Dialer whose Control hook re-validates the concrete connect IP. Because
// the connection targets a literal IP rather than the hostname, the OS does no
// second DNS lookup, so the validated address is exactly the address connected
// to — defeating DNS rebinding. The original hostname is never sent to the
// dialer, so the caller is responsible for preserving the Host header / TLS SNI
// (see the package and method docs); this helper only chooses the wire IP.
//
// addr must be in "host:port" form (the form an http.Transport.DialContext
// receives). host may be a hostname or an IP literal; IP literals are still
// classified through SF1. A blocked metadata name, resolution failure, or any
// blocked resolved IP yields a *DialError (fail closed, per Q6).
//
// The signature matches the DialContext field of net.Dialer and the
// http.Transport DialContext hook, so it can be plugged into a Transport
// directly:
//
//	tr := &http.Transport{DialContext: secDialer.DialContext}
//
// When wired into a Transport this way, http keeps using the request URL's
// hostname for the Host header and TLS ServerName, while the connection itself
// goes to the validated IP — the Host header is preserved automatically.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, newDialError(ReasonNoHost, "address %q is not host:port: %v", addr, err)
	}

	ips, err := ResolveAndValidate(ctx, d.resolver, host)
	if err != nil {
		return nil, err
	}

	// Take a copy of the base dialer per call and install the connect-time
	// re-validation guard. All resolved IPs already passed SF1; the Control
	// hook is the defence-in-depth check against a rebind between resolve and
	// connect. It is the single authoritative gate (NewControl), shared with
	// callers that assemble their own dialer, so the gate has one definition.
	dialer := *d.base
	dialer.Control = NewControl()

	// Dial the first validated IP directly. Every IP in the slice has already
	// been validated, so the choice of the first is purely for determinism; the
	// Control hook re-validates whichever address is actually used.
	target := net.JoinHostPort(ips[0].String(), port)
	return dialer.DialContext(ctx, network, target)
}

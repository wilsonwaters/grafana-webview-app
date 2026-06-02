// Package security provides the hardcoded, non-configurable security building
// blocks shared by every proxying endpoint. The IP blocklist in this file is
// the first such block: it classifies a resolved IP address against a fixed set
// of private, reserved, and special-use ranges.
//
// The blocklist is deliberately not configurable and cannot be disabled or
// extended at runtime. There are no exported setters and no configuration
// inputs: the ranges below are the single source of truth. Later tasks (the
// DNS-resolve-then-dial helper and the proxy/frameability endpoints) consume
// ClassifyIP; this package wires to nothing on its own.
package security

import "net"

// Reason tokens returned by ClassifyIP. These are short, stable, machine
// readable identifiers suitable for use as metric and audit-log labels. They
// must remain stable because consumers may key alerts and dashboards on them.
const (
	// ReasonPrivate covers the RFC 1918 private IPv4 ranges.
	ReasonPrivate = "private"
	// ReasonLoopback covers IPv4 127.0.0.0/8 and IPv6 ::1.
	ReasonLoopback = "loopback"
	// ReasonLinkLocal covers IPv4 169.254.0.0/16 and IPv6 fe80::/10. This range
	// includes the cloud metadata endpoint 169.254.169.254.
	ReasonLinkLocal = "link-local"
	// ReasonCGNAT covers the RFC 6598 carrier-grade NAT range 100.64.0.0/10.
	ReasonCGNAT = "cgnat"
	// ReasonULA covers the IPv6 unique-local address range fc00::/7.
	ReasonULA = "ula"
	// ReasonMulticast covers IPv4 and IPv6 multicast ranges.
	ReasonMulticast = "multicast"
	// ReasonUnspecified covers the "this host" / unspecified addresses
	// 0.0.0.0/8 and ::.
	ReasonUnspecified = "unspecified"
	// ReasonReserved covers other reserved / special-use ranges that are not
	// routable on the public internet (documentation, benchmarking, IETF
	// protocol assignments, the former Class E space, and so on).
	ReasonReserved = "reserved"
)

// blockedRange pairs a CIDR network with the reason token reported when an
// address falls inside it.
type blockedRange struct {
	net    *net.IPNet
	reason string
}

// mustCIDR parses a CIDR string at package-init time, panicking on malformed
// input. Every input here is a compile-time constant string, so a panic can
// only happen if this file is edited incorrectly, which a single test run
// surfaces immediately.
func mustCIDR(cidr string) *net.IPNet {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic("security: invalid hardcoded CIDR " + cidr + ": " + err.Error())
	}
	return n
}

// blockedRanges is the authoritative, package-private blocklist. It is built
// once at init and never mutated. It is intentionally unexported and has no
// setter: callers cannot add, remove, or disable entries.
//
// Order matters only for which reason token is reported when ranges overlap;
// the blocked/not-blocked result is independent of order. More specific
// categories are listed before broader catch-all reserved ranges so the most
// descriptive reason wins.
var blockedRanges = func() []blockedRange {
	specs := []struct {
		cidr   string
		reason string
	}{
		// Unspecified / "this host". Listed first so 0.0.0.0 reports
		// "unspecified" rather than the broader reserved 0.0.0.0/8.
		{"0.0.0.0/32", ReasonUnspecified},
		{"::/128", ReasonUnspecified},

		// RFC 1918 private IPv4.
		{"10.0.0.0/8", ReasonPrivate},
		{"172.16.0.0/12", ReasonPrivate},
		{"192.168.0.0/16", ReasonPrivate},

		// Loopback.
		{"127.0.0.0/8", ReasonLoopback},
		{"::1/128", ReasonLoopback},

		// Link-local (includes cloud metadata 169.254.169.254 and fe80::/10).
		{"169.254.0.0/16", ReasonLinkLocal},
		{"fe80::/10", ReasonLinkLocal},

		// Carrier-grade NAT (RFC 6598).
		{"100.64.0.0/10", ReasonCGNAT},

		// IPv6 unique-local addresses (RFC 4193).
		{"fc00::/7", ReasonULA},

		// Multicast.
		{"224.0.0.0/4", ReasonMulticast},
		{"ff00::/8", ReasonMulticast},

		// Other reserved / special-use ranges that must never be reachable.
		{"0.0.0.0/8", ReasonReserved},        // "this network"
		{"192.0.0.0/24", ReasonReserved},     // IETF protocol assignments
		{"192.0.2.0/24", ReasonReserved},     // TEST-NET-1 documentation
		{"198.18.0.0/15", ReasonReserved},    // benchmarking (RFC 2544)
		{"198.51.100.0/24", ReasonReserved},  // TEST-NET-2 documentation
		{"203.0.113.0/24", ReasonReserved},   // TEST-NET-3 documentation
		{"240.0.0.0/4", ReasonReserved},      // former Class E / reserved
		{"::/8", ReasonReserved},             // IPv6 reserved (includes ::)
		{"2001:db8::/32", ReasonReserved},    // IPv6 documentation
		{"100::/64", ReasonReserved},         // IPv6 discard-only (RFC 6666)
		{"2001::/23", ReasonReserved},        // IETF protocol assignments
	}
	ranges := make([]blockedRange, 0, len(specs))
	for _, s := range specs {
		ranges = append(ranges, blockedRange{net: mustCIDR(s.cidr), reason: s.reason})
	}
	return ranges
}()

// normalize returns the canonical form of ip used for classification. An
// IPv4-mapped IPv6 address (::ffff:a.b.c.d) is unwrapped to its 4-byte IPv4
// form so that it is classified against the IPv4 ranges and cannot bypass them
// by being expressed in IPv6 notation. All other addresses are returned
// unchanged. A nil result indicates an invalid input.
func normalize(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	// To4 returns a non-nil 4-byte slice for both native IPv4 values and
	// IPv4-mapped IPv6 values, collapsing the mapped form to plain IPv4.
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	// Reject inputs that are neither valid IPv4 nor valid 16-byte IPv6.
	if len(ip) != net.IPv6len {
		return nil
	}
	return ip
}

// ClassifyIP reports whether ip falls within any hardcoded blocked range and,
// when blocked, a short stable reason token (one of the Reason* constants).
//
// IPv4-mapped IPv6 addresses are unwrapped to their IPv4 form before
// classification, so a mapped address is subject to the same IPv4 ranges as the
// equivalent bare IPv4 address. A nil or otherwise invalid IP is treated as
// blocked with reason "reserved" (fail-closed): callers should never dial an
// address they cannot classify.
//
// Public, globally routable addresses (for example 8.8.8.8, 1.1.1.1, or a
// public IPv6 such as 2606:4700:4700::1111) are not blocked and return
// (false, "").
func ClassifyIP(ip net.IP) (blocked bool, reason string) {
	n := normalize(ip)
	if n == nil {
		// Unparseable input cannot be proven safe, so fail closed.
		return true, ReasonReserved
	}
	for _, r := range blockedRanges {
		if r.net.Contains(n) {
			return true, r.reason
		}
	}
	return false, ""
}

// IsAllowed is a convenience wrapper around ClassifyIP that returns true only
// when ip is not blocked. It exists for call sites that need a plain boolean
// gate and do not care about the specific reason.
func IsAllowed(ip net.IP) bool {
	blocked, _ := ClassifyIP(ip)
	return !blocked
}

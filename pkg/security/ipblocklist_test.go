package security

import (
	"net"
	"testing"
)

// mustIP parses a textual IP for tests, failing fast on a malformed literal.
func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("test setup: invalid IP literal %q", s)
	}
	return ip
}

// TestClassifyIP exercises every blocked category, including network and
// edge/broadcast boundary addresses, the IPv4-mapped IPv6 unwrap case, and a
// set of public addresses that must not be blocked.
func TestClassifyIP(t *testing.T) {
	tests := []struct {
		name        string
		ip          string
		wantBlocked bool
		wantReason  string
	}{
		// RFC 1918 private, with low/high boundaries of each range.
		{"private 10/8 network", "10.0.0.0", true, ReasonPrivate},
		{"private 10/8 broadcast", "10.255.255.255", true, ReasonPrivate},
		{"private 172.16/12 network", "172.16.0.0", true, ReasonPrivate},
		{"private 172.16/12 broadcast", "172.31.255.255", true, ReasonPrivate},
		{"private 192.168/16 network", "192.168.0.0", true, ReasonPrivate},
		{"private 192.168/16 broadcast", "192.168.255.255", true, ReasonPrivate},

		// Loopback.
		{"loopback v4 base", "127.0.0.0", true, ReasonLoopback},
		{"loopback v4 common", "127.0.0.1", true, ReasonLoopback},
		{"loopback v4 broadcast", "127.255.255.255", true, ReasonLoopback},
		{"loopback v6", "::1", true, ReasonLoopback},

		// Link-local, including the cloud metadata endpoint.
		{"link-local v4 network", "169.254.0.0", true, ReasonLinkLocal},
		{"link-local v4 metadata", "169.254.169.254", true, ReasonLinkLocal},
		{"link-local v4 broadcast", "169.254.255.255", true, ReasonLinkLocal},
		{"link-local v6 network", "fe80::", true, ReasonLinkLocal},
		{"link-local v6 high", "febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true, ReasonLinkLocal},

		// Carrier-grade NAT (100.64.0.0/10).
		{"cgnat network", "100.64.0.0", true, ReasonCGNAT},
		{"cgnat broadcast", "100.127.255.255", true, ReasonCGNAT},

		// IPv6 unique-local (fc00::/7).
		{"ula fc00 network", "fc00::", true, ReasonULA},
		{"ula fd common", "fd12:3456:789a::1", true, ReasonULA},
		{"ula fdff high", "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true, ReasonULA},

		// Multicast.
		{"multicast v4 network", "224.0.0.0", true, ReasonMulticast},
		{"multicast v4 high", "239.255.255.255", true, ReasonMulticast},
		{"multicast v6 base", "ff00::", true, ReasonMulticast},
		{"multicast v6 all-nodes", "ff02::1", true, ReasonMulticast},

		// Unspecified / "this host".
		{"unspecified v4", "0.0.0.0", true, ReasonUnspecified},
		{"unspecified v6", "::", true, ReasonUnspecified},

		// Other reserved / special-use ranges.
		{"reserved this-network", "0.1.2.3", true, ReasonReserved},
		{"reserved proto assignment", "192.0.0.1", true, ReasonReserved},
		{"reserved test-net-1", "192.0.2.1", true, ReasonReserved},
		{"reserved benchmarking", "198.18.0.1", true, ReasonReserved},
		{"reserved test-net-2", "198.51.100.1", true, ReasonReserved},
		{"reserved test-net-3", "203.0.113.1", true, ReasonReserved},
		{"reserved class-e", "240.0.0.1", true, ReasonReserved},
		{"reserved class-e high", "255.255.255.254", true, ReasonReserved},
		{"reserved v6 documentation", "2001:db8::1", true, ReasonReserved},
		{"reserved v6 discard", "100::1", true, ReasonReserved},

		// IPv4-mapped IPv6 must unwrap and be classified as the underlying
		// IPv4 address, so it cannot bypass the IPv4 ranges.
		{"mapped v6 private", "::ffff:10.0.0.5", true, ReasonPrivate},
		{"mapped v6 loopback", "::ffff:127.0.0.1", true, ReasonLoopback},
		{"mapped v6 metadata", "::ffff:169.254.169.254", true, ReasonLinkLocal},
		{"mapped v6 public allowed", "::ffff:8.8.8.8", false, ""},

		// Public / global-unicast addresses that must NOT be blocked.
		{"public google dns v4", "8.8.8.8", false, ""},
		{"public cloudflare dns v4", "1.1.1.1", false, ""},
		{"public quad9 v4", "9.9.9.9", false, ""},
		{"public arbitrary v4", "93.184.216.34", false, ""},
		{"public cloudflare dns v6", "2606:4700:4700::1111", false, ""},
		{"public google dns v6", "2001:4860:4860::8888", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := mustIP(t, tt.ip)
			gotBlocked, gotReason := ClassifyIP(ip)
			if gotBlocked != tt.wantBlocked {
				t.Errorf("ClassifyIP(%s) blocked = %v, want %v", tt.ip, gotBlocked, tt.wantBlocked)
			}
			if gotReason != tt.wantReason {
				t.Errorf("ClassifyIP(%s) reason = %q, want %q", tt.ip, gotReason, tt.wantReason)
			}
			// IsAllowed must always be the inverse of the blocked result.
			if allowed := IsAllowed(ip); allowed == tt.wantBlocked {
				t.Errorf("IsAllowed(%s) = %v, want %v", tt.ip, allowed, !tt.wantBlocked)
			}
		})
	}
}

// TestClassifyIPInvalid verifies fail-closed behaviour for nil and malformed
// inputs: an address that cannot be classified is treated as blocked.
func TestClassifyIPInvalid(t *testing.T) {
	cases := []struct {
		name string
		ip   net.IP
	}{
		{"nil", nil},
		{"empty", net.IP{}},
		{"wrong length", net.IP{1, 2, 3}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := ClassifyIP(tt.ip)
			if !blocked {
				t.Errorf("ClassifyIP(%v) blocked = false, want true (fail closed)", tt.ip)
			}
			if reason != ReasonReserved {
				t.Errorf("ClassifyIP(%v) reason = %q, want %q", tt.ip, reason, ReasonReserved)
			}
			if IsAllowed(tt.ip) {
				t.Errorf("IsAllowed(%v) = true, want false (fail closed)", tt.ip)
			}
		})
	}
}

// TestMappedAndBareAgree confirms an IPv4-mapped IPv6 address classifies
// identically to the equivalent bare IPv4 address across representative inputs.
func TestMappedAndBareAgree(t *testing.T) {
	for _, v4 := range []string{"10.1.2.3", "172.16.5.5", "192.168.1.1", "169.254.169.254", "100.64.0.1", "8.8.8.8", "1.1.1.1"} {
		bare := mustIP(t, v4)
		mapped := mustIP(t, "::ffff:"+v4)
		bb, br := ClassifyIP(bare)
		mb, mr := ClassifyIP(mapped)
		if bb != mb || br != mr {
			t.Errorf("mismatch for %s: bare=(%v,%q) mapped=(%v,%q)", v4, bb, br, mb, mr)
		}
	}
}

// TestClassifyIPTranslationPrefixes covers VR-11: 6to4 (2002::/16) and NAT64
// well-known-prefix (64:ff9b::/96) addresses are classified by the IPv4 address
// they embed, so internal targets are blocked and public ones are not; the
// NAT64 local-use prefix (64:ff9b:1::/48) is always blocked.
func TestClassifyIPTranslationPrefixes(t *testing.T) {
	tests := []struct {
		name        string
		ip          string
		wantBlocked bool
		wantReason  string
	}{
		// 6to4: bits 16-47 carry the IPv4 address.
		{"6to4 metadata", "2002:a9fe:a9fe::", true, ReasonLinkLocal},
		{"6to4 metadata with host bits", "2002:a9fe:a9fe:1:2:3:4:5", true, ReasonLinkLocal},
		{"6to4 rfc1918 10/8", "2002:0a00:0001::1", true, ReasonPrivate},
		{"6to4 rfc1918 192.168/16", "2002:c0a8:0101::", true, ReasonPrivate},
		{"6to4 loopback", "2002:7f00:0001::", true, ReasonLoopback},
		{"6to4 cgnat", "2002:6440:0001::", true, ReasonCGNAT},
		{"6to4 unspecified", "2002::", true, ReasonUnspecified},
		{"6to4 public allowed", "2002:0808:0808::1", false, ""},

		// NAT64 well-known prefix: the last 32 bits carry the IPv4 address.
		{"nat64 metadata", "64:ff9b::a9fe:a9fe", true, ReasonLinkLocal},
		{"nat64 metadata dotted", "64:ff9b::169.254.169.254", true, ReasonLinkLocal},
		{"nat64 rfc1918", "64:ff9b::10.1.2.3", true, ReasonPrivate},
		{"nat64 loopback", "64:ff9b::127.0.0.1", true, ReasonLoopback},
		{"nat64 multicast", "64:ff9b::224.0.0.1", true, ReasonMulticast},
		{"nat64 public allowed", "64:ff9b::8.8.8.8", false, ""},
		{"nat64 public allowed hex", "64:ff9b::5db8:d822", false, ""},
		// Outside the /96 (non-zero bits 64-95): not a WKP address, still
		// caught by the ::/8 reserved range.
		{"64:ff9b outside the /96", "64:ff9b::1:0:808:808", true, ReasonReserved},

		// NAT64 local-use prefix: embedding is operator-defined, always blocked.
		{"nat64 local-use metadata", "64:ff9b:1::a9fe:a9fe", true, ReasonReserved},
		{"nat64 local-use public", "64:ff9b:1:abcd::808:808", true, ReasonReserved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBlocked, gotReason := ClassifyIP(mustIP(t, tt.ip))
			if gotBlocked != tt.wantBlocked || gotReason != tt.wantReason {
				t.Errorf("ClassifyIP(%s) = (%v, %q), want (%v, %q)", tt.ip, gotBlocked, gotReason, tt.wantBlocked, tt.wantReason)
			}
		})
	}
}

// TestClassifyIPPolicyTranslatedPrivateNotRelaxed covers VR-11 with the
// AllowPrivateIP opt-in: the opt-in relaxes a bare RFC 1918 address but never
// one wrapped in a 6to4 or NAT64 address.
func TestClassifyIPPolicyTranslatedPrivateNotRelaxed(t *testing.T) {
	permits := 0
	pol := Policy{AllowPrivate: true, OnPrivatePermit: func(net.IP, string) { permits++ }}

	if blocked, _ := ClassifyIPPolicy(mustIP(t, "10.0.0.1"), pol); blocked {
		t.Fatal("control: bare 10.0.0.1 should be relaxed by the opt-in")
	}
	for _, ip := range []string{"2002:0a00:0001::", "64:ff9b::10.0.0.1", "64:ff9b::192.168.1.1"} {
		blocked, reason := ClassifyIPPolicy(mustIP(t, ip), pol)
		if !blocked || reason != ReasonPrivate {
			t.Errorf("ClassifyIPPolicy(%s, opt-in) = (%v, %q), want (true, %q)", ip, blocked, reason, ReasonPrivate)
		}
	}
	if permits != 1 {
		t.Errorf("OnPrivatePermit called %d times, want 1 (only the bare control address)", permits)
	}
}

package security

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
)

// stubResolver is a unit-test Resolver that returns canned addresses (or a
// canned error) without ever touching the network. It records the last host it
// was asked to resolve so tests can assert the resolved name.
type stubResolver struct {
	addrs    []net.IPAddr
	err      error
	lastHost string
}

func (s *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	s.lastHost = host
	if s.err != nil {
		return nil, s.err
	}
	return s.addrs, nil
}

// ipAddrs builds a []net.IPAddr from textual IPs, failing the test on a bad
// literal so a typo in a test never silently becomes a nil IP.
func ipAddrs(t *testing.T, ips ...string) []net.IPAddr {
	t.Helper()
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test setup: invalid IP literal %q", s)
		}
		out = append(out, net.IPAddr{IP: ip})
	}
	return out
}

// --- ResolveAndValidate -----------------------------------------------------

// AC: "Resolved IP validated against SF1 before dialling" (allowed path) +
// "Dialler connects to the validated IP" precondition: the validated IP list is
// returned for a public address.
func TestResolveAndValidate_AllowedPublicIP(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "8.8.8.8")}
	ips, err := ResolveAndValidate(context.Background(), r, "example.com")
	if err != nil {
		t.Fatalf("expected allowed, got error: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("8.8.8.8")) {
		t.Fatalf("expected [8.8.8.8], got %v", ips)
	}
	if r.lastHost != "example.com" {
		t.Fatalf("resolver asked for %q, want example.com", r.lastHost)
	}
}

// AC: "Resolved IP validated against SF1 before dialling" (denied path). Each
// blocked class is rejected and surfaces the SF1 reason verbatim.
func TestResolveAndValidate_BlockedResolvedIP(t *testing.T) {
	cases := []struct {
		name     string
		ip       string
		ipReason string
	}{
		{"loopback", "127.0.0.1", ReasonLoopback},
		{"private", "10.0.0.5", ReasonPrivate},
		{"linklocal_metadata_ip", "169.254.169.254", ReasonLinkLocal},
		{"private_192", "192.168.1.1", ReasonPrivate},
		{"ipv6_loopback", "::1", ReasonLoopback},
		{"ipv6_ula", "fc00::1", ReasonULA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &stubResolver{addrs: ipAddrs(t, tc.ip)}
			_, err := ResolveAndValidate(context.Background(), r, "evil.example")
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.ip)
			}
			if got := DialReasonOf(err); got != ReasonBlockedIP {
				t.Fatalf("Reason = %q, want %q", got, ReasonBlockedIP)
			}
			var de *DialError
			if !errors.As(err, &de) {
				t.Fatalf("error is not *DialError: %v", err)
			}
			if de.IPReason != tc.ipReason {
				t.Fatalf("IPReason = %q, want %q", de.IPReason, tc.ipReason)
			}
			if de.BlockedIP == nil || !de.BlockedIP.Equal(net.ParseIP(tc.ip)) {
				t.Fatalf("BlockedIP = %v, want %s", de.BlockedIP, tc.ip)
			}
		})
	}
}

// AC: "Multiple A/AAAA record strategy documented and implemented" (Q6 fail
// closed): a set with one bad record fails the whole request even though a good
// public record is present.
func TestResolveAndValidate_MultiRecordOneBadFailsClosed(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "8.8.8.8", "127.0.0.1")}
	_, err := ResolveAndValidate(context.Background(), r, "rebind.example")
	if err == nil {
		t.Fatal("expected whole request to fail when any record is blocked (Q6)")
	}
	if got := DialReasonOf(err); got != ReasonBlockedIP {
		t.Fatalf("Reason = %q, want %q", got, ReasonBlockedIP)
	}
	var de *DialError
	if errors.As(err, &de) && !de.BlockedIP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("BlockedIP = %v, want 127.0.0.1", de.BlockedIP)
	}
}

// All records valid: full validated list returned (multi-record happy path).
func TestResolveAndValidate_MultiRecordAllGood(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "8.8.8.8", "1.1.1.1", "2606:4700:4700::1111")}
	ips, err := ResolveAndValidate(context.Background(), r, "ok.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 3 {
		t.Fatalf("expected 3 validated IPs, got %d (%v)", len(ips), ips)
	}
}

// AC: "Metadata hostnames blocked by name" — rejected before any resolution.
func TestResolveAndValidate_MetadataHostnameBlockedByName(t *testing.T) {
	cases := []string{
		"metadata.google.internal",
		"metadata.goog",
		"METADATA.GOOGLE.INTERNAL",  // case-insensitive
		"metadata.google.internal.", // trailing dot stripped
		"  metadata.goog  ",         // whitespace trimmed
	}
	for _, host := range cases {
		t.Run(strings.TrimSpace(host), func(t *testing.T) {
			r := &stubResolver{addrs: ipAddrs(t, "8.8.8.8")} // would pass if resolved
			_, err := ResolveAndValidate(context.Background(), r, host)
			if err == nil {
				t.Fatalf("expected metadata host %q to be blocked", host)
			}
			if got := DialReasonOf(err); got != ReasonMetadataHost {
				t.Fatalf("Reason = %q, want %q", got, ReasonMetadataHost)
			}
			if r.lastHost != "" {
				t.Fatalf("resolver must not be called for metadata host; got lookup of %q", r.lastHost)
			}
		})
	}
}

func TestResolveAndValidate_EmptyHost(t *testing.T) {
	r := &stubResolver{}
	_, err := ResolveAndValidate(context.Background(), r, "   ")
	if err == nil || DialReasonOf(err) != ReasonNoHost {
		t.Fatalf("expected ReasonNoHost, got %v", err)
	}
}

func TestResolveAndValidate_ResolveError(t *testing.T) {
	r := &stubResolver{err: errors.New("nxdomain")}
	_, err := ResolveAndValidate(context.Background(), r, "nope.example")
	if err == nil || DialReasonOf(err) != ReasonResolveFailed {
		t.Fatalf("expected ReasonResolveFailed on lookup error, got %v", err)
	}
}

func TestResolveAndValidate_NoAddresses(t *testing.T) {
	r := &stubResolver{addrs: nil}
	_, err := ResolveAndValidate(context.Background(), r, "empty.example")
	if err == nil || DialReasonOf(err) != ReasonResolveFailed {
		t.Fatalf("expected ReasonResolveFailed on empty answer, got %v", err)
	}
}

// --- IsMetadataHostname ------------------------------------------------------

func TestIsMetadataHostname(t *testing.T) {
	yes := []string{"metadata.google.internal", "metadata.goog", "Metadata.Google.Internal.", " metadata.goog "}
	for _, h := range yes {
		if !IsMetadataHostname(h) {
			t.Errorf("IsMetadataHostname(%q) = false, want true", h)
		}
	}
	no := []string{"example.com", "metadata.google.com", "notmetadata.goog", ""}
	for _, h := range no {
		if IsMetadataHostname(h) {
			t.Errorf("IsMetadataHostname(%q) = true, want false", h)
		}
	}
}

// --- Control hook (connect-time re-validation) -------------------------------

// AC: "Dialler connects to the validated IP" defence-in-depth: the Control hook
// rejects a blocked IP even if the resolver had returned a good one (rebind
// between resolve and connect).
func TestControl_RejectsRebindToBlockedIP(t *testing.T) {
	ctrl := NewControl()
	// Simulate the OS about to connect to a rebound private address.
	err := ctrl("tcp4", "169.254.169.254:80", nil)
	if err == nil {
		t.Fatal("Control must reject a blocked connect IP (rebind)")
	}
	if got := DialReasonOf(err); got != ReasonBlockedIP {
		t.Fatalf("Reason = %q, want %q", got, ReasonBlockedIP)
	}
}

// AC: Control allows a validated public connect IP.
func TestControl_AllowsPublicIP(t *testing.T) {
	ctrl := NewControl()
	if err := ctrl("tcp4", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("Control rejected a public IP: %v", err)
	}
}

// Control blocks the *canonical* loopback and metadata connect addresses. This
// is what Control actually sees in production: DialContext hands it the
// net.JoinHostPort of an already-resolved net.IP, i.e. a canonical dotted-IP
// literal. It does NOT exercise obfuscated encodings (those never reach Control
// — see TestObfuscatedHost_RejectedByResolveFailure for the real #22 path).
func TestControl_BlocksCanonicalLoopbackAndMetadataConnectAddr(t *testing.T) {
	ctrl := NewControl()
	cases := []struct {
		name string
		addr string
	}{
		{"loopback", "127.0.0.1:80"},
		{"metadata", "169.254.169.254:80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ctrl("tcp4", tc.addr, nil); err == nil {
				t.Fatalf("Control must block canonical address %s", tc.addr)
			}
		})
	}
}

// Pin the empirical fact the #22 rationale rests on: Go's net.ParseIP does NOT
// parse obfuscated IP-literal encodings (non-dotted decimal, octal, hex). It
// returns nil for each, which is why a net.Dialer treats such a string as a
// hostname and sends it to DNS rather than dialing a loopback IP.
func TestObfuscatedLiterals_NotParsedByParseIP(t *testing.T) {
	for _, s := range []string{"2130706433", "0177.0.0.1", "0x7f.0.0.1"} {
		if ip := net.ParseIP(s); ip != nil {
			t.Fatalf("net.ParseIP(%q) = %v, want nil (obfuscated forms are not parsed)", s, ip)
		}
	}
}

// Genuine #22 carry-forward test. An obfuscated IP-literal encoding passed as a
// host is NOT canonicalised by Go's resolver; it is treated as a hostname and
// fails DNS resolution (NXDOMAIN). The stub resolver mirrors that real
// behaviour by returning a resolution error for these inputs, and we assert the
// request is rejected with ReasonResolveFailed (fail closed). The safe outcome
// here is resolution failure, NOT any canonicalisation at Control.
func TestObfuscatedHost_RejectedByResolveFailure(t *testing.T) {
	obfuscated := []string{"2130706433", "0177.0.0.1", "0x7f.0.0.1"}
	for _, host := range obfuscated {
		t.Run(host, func(t *testing.T) {
			// Real resolvers return NXDOMAIN for these non-hostname strings.
			r := &stubResolver{err: errors.New("nxdomain")}
			if _, err := ResolveAndValidate(context.Background(), r, host); err == nil ||
				DialReasonOf(err) != ReasonResolveFailed {
				t.Fatalf("ResolveAndValidate(%q): expected ReasonResolveFailed, got %v", host, err)
			}
			if r.lastHost != host {
				t.Fatalf("resolver asked for %q, want the obfuscated host %q (passed as a name, not canonicalised)", r.lastHost, host)
			}

			// And through DialContext (host:port form): same fail-closed result.
			rd := &stubResolver{err: errors.New("nxdomain")}
			d := NewDialer(rd, &net.Dialer{})
			conn, derr := d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, "80"))
			if conn != nil {
				_ = conn.Close()
				t.Fatalf("DialContext(%q) connected; must fail closed on resolve failure", host)
			}
			if DialReasonOf(derr) != ReasonResolveFailed {
				t.Fatalf("DialContext(%q): expected ReasonResolveFailed, got %v", host, derr)
			}
		})
	}
}

func TestValidateConnectAddr_InvalidAddress(t *testing.T) {
	if err := validateConnectAddr("not-an-ip"); err == nil {
		t.Fatal("expected fail-closed on unparseable connect address")
	}
	// Address with no port falls through to raw-host parse; a bare valid IP
	// must still be classified.
	if err := validateConnectAddr("10.0.0.1"); err == nil {
		t.Fatal("expected blocked private IP without port to be rejected")
	}
	if err := validateConnectAddr("8.8.8.8"); err != nil {
		t.Fatalf("bare public IP should be allowed: %v", err)
	}
}

// --- DialContext + Host-header preservation ----------------------------------

// AC: "Dialler connects to the validated IP, not the hostname" (fail-closed
// branch). A resolved loopback IP must be rejected before any socket is opened,
// so the helper never connects to a blocked address. The caller-supplied
// hostname ("blocked.example") is what gets resolved — the helper never sends
// the IP to the resolver — which is the mechanism by which the consuming
// http.Transport keeps the original Host header / SNI (see DialContext docs).
func TestDialContext_FailsClosedOnBlockedResolvedIP(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "127.0.0.1")}
	d := NewDialer(r, &net.Dialer{})
	conn, err := d.DialContext(context.Background(), "tcp", "blocked.example:80")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("dialer connected to a blocked IP; must fail closed")
	}
	if err == nil || DialReasonOf(err) != ReasonBlockedIP {
		t.Fatalf("expected ReasonBlockedIP, got %v", err)
	}
	if r.lastHost != "blocked.example" {
		t.Fatalf("helper resolved %q, want the original hostname blocked.example", r.lastHost)
	}
}

// AC: validation runs on the resolved IP for any blocked class reached through
// DialContext (reserved TEST-NET-3 here), keeping the test offline while
// confirming the gate fires inside DialContext.
func TestDialContext_BlocksReservedResolvedIP(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "203.0.113.1")} // TEST-NET-3 (reserved)
	d := NewDialer(r, nil)
	_, err := d.DialContext(context.Background(), "tcp", "doc.example:80")
	if DialReasonOf(err) != ReasonBlockedIP {
		t.Fatalf("expected ReasonBlockedIP for reserved TEST-NET-3, got %v", err)
	}
}

// DialContext rejects a metadata hostname before resolving.
func TestDialContext_MetadataHostBlocked(t *testing.T) {
	r := &stubResolver{addrs: ipAddrs(t, "8.8.8.8")}
	d := NewDialer(r, nil)
	_, err := d.DialContext(context.Background(), "tcp", "metadata.google.internal:80")
	if DialReasonOf(err) != ReasonMetadataHost {
		t.Fatalf("expected ReasonMetadataHost, got %v", err)
	}
	if r.lastHost != "" {
		t.Fatalf("resolver should not run for metadata host, got %q", r.lastHost)
	}
}

// DialContext rejects an addr that is not host:port (fail closed).
func TestDialContext_BadAddr(t *testing.T) {
	d := NewDialer(&stubResolver{}, nil)
	_, err := d.DialContext(context.Background(), "tcp", "no-port")
	if DialReasonOf(err) != ReasonNoHost {
		t.Fatalf("expected ReasonNoHost for malformed addr, got %v", err)
	}
}

// Transport mechanics: a net.Dialer with an allow-all Control reaches a real
// loopback listener. SF1 blocks loopback so the security guard itself is tested
// in the Control tests above; this isolates and confirms the base dial path the
// helper builds on actually connects when the guard permits.
func TestDialer_BaseDialerReachesListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()

	// Directly use a net.Dialer with an allow-all Control to confirm the base
	// dial path works against the listener (the security guard is tested in the
	// Control tests above; this isolates the transport mechanics).
	var d net.Dialer
	d.Control = func(_, _ string, _ syscall.RawConn) error { return nil }
	conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	_ = conn.Close()
	<-accepted
}

// NewDialer defaults: nil resolver / nil base must not panic and must produce a
// usable dialer.
func TestNewDialer_Defaults(t *testing.T) {
	d := NewDialer(nil, nil)
	if d == nil || d.resolver == nil || d.base == nil {
		t.Fatal("NewDialer should default resolver and base")
	}
}

// NewDialer must copy the base dialer so the caller's *net.Dialer is never
// mutated (the helper installs its own Control hook).
func TestNewDialer_CopiesBase(t *testing.T) {
	base := &net.Dialer{}
	d := NewDialer(&stubResolver{}, base)
	if d.base == base {
		t.Fatal("NewDialer must take a copy of base, not the caller's pointer")
	}
}

// DialReasonOf on nil / non-DialError returns "".
func TestDialReasonOf_NonDialError(t *testing.T) {
	if DialReasonOf(nil) != "" {
		t.Fatal("nil error should yield empty reason")
	}
	if DialReasonOf(errors.New("plain")) != "" {
		t.Fatal("non-DialError should yield empty reason")
	}
}

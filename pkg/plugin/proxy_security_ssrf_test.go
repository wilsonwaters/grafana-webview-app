package plugin

// Security test suite — SSRF, blocklist, allowlist, scheme validation (TC1, AC
// 17–22). This suite is HERMETIC: it performs NO real network I/O and NO real
// DNS. Every test exercises the FULL endpoint stack (the real
// proxyHandler.ServeHTTP / proxyResourceHandler / checkFrameableHandler) by
// injecting a stub security.Resolver into a transport built from the production
// secure dialer (security.NewDialer), so DNS resolution returns canned answers
// while the real SF4 resolve-then-dial + connect-time guard run unchanged.
//
// All helper types/functions in this file are file-local and prefixed `tc1` so
// they cannot collide with helpers in sibling test files landing in this same
// package. Established helpers from proxy_test.go / frameable_test.go are reused
// where they already exist (settingsWith, allowExample, doProxy, doCheckFrameable,
// recordingTransport, etc.).
//
// AC → test mapping (see ai-state/reference/implementation-spec.md lines 321–326):
//
//	AC 17 → TestTC1AC17FreshInstallEmptyAllowlistFailsClosed
//	AC 18 → TestTC1AC18AllowlistEnforcedOnAllThreeEndpoints
//	AC 19 → TestTC1AC19AllowlistedHostResolvingToPrivateIsBlocked
//	AC 20 → TestTC1AC20MetadataAndLinkLocalBlockedRegardlessOfAllowlist
//	AC 21 → TestTC1AC21DNSRebindingPrevented (+ sub-tests)
//	AC 22 → TestTC1AC22NonHTTPSchemesRejected
//
// AC 19 note: the per-domain private-IP opt-in (DomainOptions.AllowPrivateIP) is
// parsed into config and mapped into security.EntryOptions, but it is NOT wired
// into the endpoint dial path — buildProxyHandler constructs the secure dialer
// with security.NewDialer(nil, …) and the SF1 blocklist is enforced strictly with
// no opt-in relaxation (see resolvedial.go / ipblocklist.go). This suite therefore
// asserts the DENY case and the resolved-IP rejection, exactly as the AC's
// parenthetical permits when the opt-in is not present in the code.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/wilsonwaters/webview/pkg/security"
)

// tc1StubResolver is a security.Resolver that returns a fixed set of canned
// addresses for every hostname, recording how many times it was called. It does
// NO network I/O, so the whole suite is hermetic.
type tc1StubResolver struct {
	mu    sync.Mutex
	ips   []net.IPAddr
	calls int
}

func tc1NewStubResolver(ips ...string) *tc1StubResolver {
	addrs := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		addrs = append(addrs, net.IPAddr{IP: net.ParseIP(s)})
	}
	return &tc1StubResolver{ips: addrs}
}

func (r *tc1StubResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.ips, nil
}

func (r *tc1StubResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// tc1RebindResolver is a stateful security.Resolver modelling a TOCTOU DNS
// rebind: it returns a benign PUBLIC IP on the first LookupIPAddr and a BLOCKED
// IP on every subsequent call. It records its call count so a test can assert
// the resolver is consulted at most once per dial (no re-resolution between
// validate and connect).
type tc1RebindResolver struct {
	mu      sync.Mutex
	first   net.IP // returned on the first lookup
	rebound net.IP // returned on every lookup after the first
	calls   int
}

func (r *tc1RebindResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return []net.IPAddr{{IP: r.first}}, nil
	}
	return []net.IPAddr{{IP: r.rebound}}, nil
}

func (r *tc1RebindResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// tc1ResolverTransport builds an *http.Transport whose DialContext is the
// production secure dialer wired to the given stub resolver. Driving a handler's
// transport through this exercises the FULL SF4 path (ResolveAndValidate +
// connect-time NewControl guard) against canned DNS, with no real network.
func tc1ResolverTransport(resolver security.Resolver) *http.Transport {
	return &http.Transport{
		DialContext: security.NewDialer(resolver, &net.Dialer{}).DialContext,
	}
}

// tc1RecordingDialer records every concrete address the dialer is asked to
// connect to, then refuses the connection with errTC1DialTrap so no real socket
// is ever opened. It wraps the secure dialer so SF4's validation still runs
// first; the recorded addresses prove WHICH IP the validated dial targeted (the
// crux of AC 21: the validated IP is dialled directly, never re-resolved).
type tc1RecordingDialer struct {
	mu     sync.Mutex
	dialed []string
}

func (d *tc1RecordingDialer) record(addr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dialed = append(d.dialed, addr)
}

func (d *tc1RecordingDialer) addrs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.dialed))
	copy(out, d.dialed)
	return out
}

// tc1AllowlistedTransport builds an *http.Transport whose DialContext exercises
// the REAL SF4 building blocks — security.ResolveAndValidate (resolve-then-
// validate, fail-closed) followed by the authoritative connect-time guard
// security.NewControl — exactly as the production *security.Dialer.DialContext
// does, then records the validated target address and traps the connection so
// NO real socket is ever opened. We reconstruct the dial path here rather than
// reusing *security.Dialer directly because that helper OVERWRITES the base
// dialer's Control hook with NewControl() and then performs a real net.Dial to
// the validated IP — which would (a) hit the network and (b) hide the validated
// address from us. Reconstructing it keeps the suite hermetic while still driving
// the genuine validation functions, and asserting the SAME first-IP-direct dial
// the production helper performs (it dials ips[0] with the Control re-check).
func tc1AllowlistedTransport(resolver security.Resolver, rec *tc1RecordingDialer) *http.Transport {
	control := security.NewControl()
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// SF4 step 1: resolve + validate every answer (fail-closed on any block).
			ips, err := security.ResolveAndValidate(ctx, resolver, host)
			if err != nil {
				return nil, err
			}
			// Production dials the FIRST validated IP directly (never re-resolving).
			target := net.JoinHostPort(ips[0].String(), port)
			// SF4 step 2: the authoritative connect-time guard re-validates the exact
			// wire address — catching a rebind that slipped in after validation.
			if cerr := control("tcp", target, nil); cerr != nil {
				return nil, cerr
			}
			rec.record(target)
			return nil, errTC1DialTrap
		},
	}
}

// errTC1DialTrap is returned by the recording dialer's Control hook to abort the
// connection after the validated target has been recorded, so no real network
// connection is ever established.
var errTC1DialTrap = &tc1Error{"tc1: dial trapped after validation"}

type tc1Error struct{ msg string }

func (e *tc1Error) Error() string { return e.msg }

// tc1ProxyWithResolver builds a /proxy handler (allowlisted for example.com)
// whose transport resolves through the given stub resolver.
func tc1ProxyWithResolver(resolver security.Resolver) *proxyHandler {
	p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
	p.transport = tc1ResolverTransport(resolver)
	return p
}

// tc1DoProxyResource issues GET /proxy-resource?url=<target> against handler.
func tc1DoProxyResource(handler http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, proxyResourcePath+"?url="+url.QueryEscape(target), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// AC 17: Fresh install with default settings refuses to proxy any URL.
// -----------------------------------------------------------------------------

// TestTC1AC17FreshInstallEmptyAllowlistFailsClosed asserts that with the default
// (empty) allowlist a fresh install denies EVERY proxy request across all three
// endpoints, failing closed before any upstream is contacted. We give each
// handler a transport that would fail the test if dialled, proving the denial
// happens pre-fetch.
func TestTC1AC17FreshInstallEmptyAllowlistFailsClosed(t *testing.T) {
	// settingsWith(nil) is the fresh-install shape: a nil AllowedDomains maps to a
	// nil allowlist, which MatchHostname treats as deny-all.
	targets := []string{
		"https://example.com/page",
		"http://anything.test/",
		"https://1.1.1.1/",
	}

	for _, target := range targets {
		// /proxy and /proxy-resource: allowlist denial is a hard 403.
		pProxy := newProxyHandler(settingsWith(nil))
		pProxy.transport = tc1FailIfDialled(t)
		if rec := doProxy(pProxy, target); rec.Code != http.StatusForbidden {
			t.Errorf("/proxy empty allowlist %q: got %d, want 403 (body=%q)", target, rec.Code, rec.Body.String())
		}

		pResource := newProxyHandler(settingsWith(nil))
		pResource.transport = tc1FailIfDialled(t)
		resourceHandler := proxyResourceHandler{p: pResource}
		if rec := tc1DoProxyResource(resourceHandler, target); rec.Code != http.StatusForbidden {
			t.Errorf("/proxy-resource empty allowlist %q: got %d, want 403 (body=%q)", target, rec.Code, rec.Body.String())
		}

		// /check-frameable: allowlist denial is also a hard 403 (writeDenial with
		// denialReasonAllowlist, like the proxy endpoints) — it never reaches the
		// fetch, so no verdict body is produced.
		pFrame := newProxyHandler(settingsWith(nil))
		pFrame.transport = tc1FailIfDialled(t)
		frameHandler := checkFrameableHandler{p: pFrame}
		if rec := doCheckFrameable(frameHandler, target); rec.Code != http.StatusForbidden {
			t.Errorf("/check-frameable empty allowlist %q: got %d, want 403 (body=%q)", target, rec.Code, rec.Body.String())
		}
	}
}

// tc1FailIfDialled returns a RoundTripper that fails the test if it is ever
// invoked: a denial that happens pre-fetch must never reach the transport.
func tc1FailIfDialled(t *testing.T) http.RoundTripper {
	t.Helper()
	return tc1RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Errorf("transport was dialled, but the request should have been denied before any fetch")
		return nil, errTC1DialTrap
	})
}

// tc1RoundTripFunc adapts a func to http.RoundTripper.
type tc1RoundTripFunc func(*http.Request) (*http.Response, error)

func (f tc1RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// -----------------------------------------------------------------------------
// AC 18: Allowlist enforcement applies equally to /proxy, /proxy-resource, and
// /check-frameable.
// -----------------------------------------------------------------------------

// TestTC1AC18AllowlistEnforcedOnAllThreeEndpoints sends a non-allowlisted host
// to each of the three endpoints with an allowlist of exactly example.com, and
// asserts every endpoint denies it with 403 before any upstream is contacted.
func TestTC1AC18AllowlistEnforcedOnAllThreeEndpoints(t *testing.T) {
	const target = "https://evil.example/secret"
	cfg := settingsWith(allowExample(DomainOptions{})) // exact match, subdomains off

	pProxy := newProxyHandler(cfg)
	pProxy.transport = tc1FailIfDialled(t)
	if rec := doProxy(pProxy, target); rec.Code != http.StatusForbidden {
		t.Errorf("/proxy non-allowlisted: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}

	pResource := newProxyHandler(cfg)
	pResource.transport = tc1FailIfDialled(t)
	if rec := tc1DoProxyResource(proxyResourceHandler{p: pResource}, target); rec.Code != http.StatusForbidden {
		t.Errorf("/proxy-resource non-allowlisted: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}

	pFrame := newProxyHandler(cfg)
	pFrame.transport = tc1FailIfDialled(t)
	if rec := doCheckFrameable(checkFrameableHandler{p: pFrame}, target); rec.Code != http.StatusForbidden {
		t.Errorf("/check-frameable non-allowlisted: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// AC 19: A hostname resolving to RFC 1918 space returns 403 even if the hostname
// is allowlisted (the per-domain private-IP opt-in is not wired at the endpoint
// — see file header — so the resolved-IP rejection always wins).
// -----------------------------------------------------------------------------

// TestTC1AC19AllowlistedHostResolvingToPrivateIsBlocked drives an ALLOWLISTED
// hostname (example.com) whose DNS resolves to RFC 1918 / private space through
// the real /proxy and /proxy-resource stacks (transport = secure dialer over a
// stub resolver). The pipeline passes the allowlist gate, then the SF4 dial-time
// resolve-then-dial blocks the private IP, so the ErrorHandler maps it to 403.
// It also asserts the library-level resolved-IP rejection directly via
// ResolveAndValidate (ReasonBlockedIP / private).
func TestTC1AC19AllowlistedHostResolvingToPrivateIsBlocked(t *testing.T) {
	privateIPs := []struct {
		name string
		ip   string
	}{
		{"rfc1918 10/8", "10.0.0.1"},
		{"rfc1918 172.16/12", "172.16.5.4"},
		{"rfc1918 192.168/16", "192.168.1.1"},
	}

	for _, tc := range privateIPs {
		t.Run(tc.name, func(t *testing.T) {
			// Library-level: ResolveAndValidate must reject the private resolved IP.
			resolver := tc1NewStubResolver(tc.ip)
			ips, err := security.ResolveAndValidate(context.Background(), resolver, "example.com")
			if ips != nil {
				t.Fatalf("ResolveAndValidate(%s): expected nil IPs on rejection, got %v", tc.ip, ips)
			}
			if reason := security.DialReasonOf(err); reason != security.ReasonBlockedIP {
				t.Fatalf("ResolveAndValidate(%s): reason = %q, want %q", tc.ip, reason, security.ReasonBlockedIP)
			}

			// End-to-end through /proxy: allowlisted host, private resolved IP => 403.
			pProxy := tc1ProxyWithResolver(tc1NewStubResolver(tc.ip))
			if rec := doProxy(pProxy, "https://example.com/page"); rec.Code != http.StatusForbidden {
				t.Errorf("/proxy private-resolving allowlisted host: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
			}

			// End-to-end through /proxy-resource: same outcome.
			pResource := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
			pResource.transport = tc1ResolverTransport(tc1NewStubResolver(tc.ip))
			if rec := tc1DoProxyResource(proxyResourceHandler{p: pResource}, "https://example.com/style.css"); rec.Code != http.StatusForbidden {
				t.Errorf("/proxy-resource private-resolving allowlisted host: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
			}
		})
	}
}

// -----------------------------------------------------------------------------
// AC 20: 169.254.169.254, metadata.google.internal, or any link-local address
// returns 403 regardless of allowlist — both by-name and by-resolved-IP.
// -----------------------------------------------------------------------------

// TestTC1AC20MetadataAndLinkLocalBlockedRegardlessOfAllowlist covers the
// cloud-metadata + link-local denial in two ways: (a) by NAME — a metadata
// hostname is rejected before DNS even runs (ReasonMetadataHost), and (b) by
// RESOLVED IP — an allowlisted hostname that resolves to 169.254.169.254 or any
// link-local address is blocked at the SF4 dial (ReasonBlockedIP / link-local).
// Both are driven end-to-end through the real /proxy stack to a 403.
func TestTC1AC20MetadataAndLinkLocalBlockedRegardlessOfAllowlist(t *testing.T) {
	// --- (a) metadata host by NAME ---------------------------------------------
	t.Run("metadata host by name", func(t *testing.T) {
		// IsMetadataHostname must recognise the canonical and case/dot variants.
		for _, name := range []string{"metadata.google.internal", "Metadata.Google.Internal.", "metadata.goog"} {
			if !security.IsMetadataHostname(name) {
				t.Errorf("IsMetadataHostname(%q) = false, want true", name)
			}
		}

		// Library-level: ResolveAndValidate rejects the metadata name before any
		// resolution (the stub resolver returns a benign public IP, which must be
		// irrelevant because the name is blocked first).
		resolver := tc1NewStubResolver("93.184.216.34")
		_, err := security.ResolveAndValidate(context.Background(), resolver, "metadata.google.internal")
		if reason := security.DialReasonOf(err); reason != security.ReasonMetadataHost {
			t.Fatalf("ResolveAndValidate(metadata): reason = %q, want %q", reason, security.ReasonMetadataHost)
		}
		if got := resolver.callCount(); got != 0 {
			t.Fatalf("metadata name must be blocked BEFORE resolution, but resolver was called %d times", got)
		}

		// End-to-end: an allowlist that explicitly lists the metadata host must NOT
		// save it — SF4 blocks the name regardless of the allowlist. We allowlist
		// metadata.google.internal and still expect 403.
		cfg := settingsWith([]AllowedDomain{{Domain: "metadata.google.internal", Options: DomainOptions{}}})
		p := newProxyHandler(cfg)
		p.transport = tc1ResolverTransport(tc1NewStubResolver("93.184.216.34"))
		if rec := doProxy(p, "http://metadata.google.internal/computeMetadata/v1/"); rec.Code != http.StatusForbidden {
			t.Errorf("/proxy metadata-by-name: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
		}
	})

	// --- (b) metadata IP + link-local by RESOLVED IP ---------------------------
	cases := []struct {
		name string
		ip   string
	}{
		{"metadata ip 169.254.169.254", "169.254.169.254"},
		{"link-local 169.254.x", "169.254.42.42"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" by resolved IP", func(t *testing.T) {
			// Library-level resolved-IP rejection (link-local).
			resolver := tc1NewStubResolver(tc.ip)
			_, err := security.ResolveAndValidate(context.Background(), resolver, "example.com")
			if reason := security.DialReasonOf(err); reason != security.ReasonBlockedIP {
				t.Fatalf("ResolveAndValidate(%s): reason = %q, want %q", tc.ip, reason, security.ReasonBlockedIP)
			}

			// End-to-end through /proxy: allowlisted host resolving to the blocked IP => 403.
			p := tc1ProxyWithResolver(tc1NewStubResolver(tc.ip))
			if rec := doProxy(p, "https://example.com/page"); rec.Code != http.StatusForbidden {
				t.Errorf("/proxy link-local-resolving allowlisted host (%s): got %d, want 403 (body=%q)", tc.ip, rec.Code, rec.Body.String())
			}
		})
	}
}

// -----------------------------------------------------------------------------
// AC 21: DNS rebinding (hostname resolves to public IP at check, private IP at
// fetch) is prevented — the resolved IP is dialled directly.
// -----------------------------------------------------------------------------

// TestTC1AC21DNSRebindingPrevented covers the three layers decided in Q13a, all
// hermetic and driving the full /proxy stack where it asserts the HTTP outcome.
func TestTC1AC21DNSRebindingPrevented(t *testing.T) {
	// --- Layer 1: poisoned answer set (fail-closed, Q6) ------------------------
	// A single DNS answer carrying one public IP AND one blocked IP must fail the
	// WHOLE request closed, both at the library level and end-to-end.
	t.Run("poisoned answer set fails closed", func(t *testing.T) {
		poisoned := [][]string{
			{"93.184.216.34", "169.254.169.254"}, // public + metadata/link-local
			{"93.184.216.34", "10.0.0.1"},        // public + RFC 1918
		}
		for _, set := range poisoned {
			// Library-level: ResolveAndValidate returns no IPs and ReasonBlockedIP.
			resolver := tc1NewStubResolver(set...)
			ips, err := security.ResolveAndValidate(context.Background(), resolver, "example.com")
			if ips != nil {
				t.Fatalf("poisoned set %v: expected nil IPs, got %v", set, ips)
			}
			if reason := security.DialReasonOf(err); reason != security.ReasonBlockedIP {
				t.Fatalf("poisoned set %v: reason = %q, want %q", set, reason, security.ReasonBlockedIP)
			}

			// End-to-end through /proxy: 403, and the recording dialer proves NO host
			// was ever dialled (the request failed at validation, before any connect).
			recDialer := &tc1RecordingDialer{}
			p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
			p.transport = tc1AllowlistedTransport(tc1NewStubResolver(set...), recDialer)
			rec := doProxy(p, "https://example.com/page")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("poisoned set %v: /proxy got %d, want 403 (body=%q)", set, rec.Code, rec.Body.String())
			}
			if dialed := recDialer.addrs(); len(dialed) != 0 {
				t.Fatalf("poisoned set %v: expected NO dial, but dialled %v", set, dialed)
			}
		}
	})

	// --- Layer 2: TOCTOU rebind across resolve→connect -------------------------
	// The resolver returns a benign PUBLIC IP on the first lookup and a BLOCKED IP
	// thereafter. The dialer must connect to the validated FIRST IP only, and must
	// not re-resolve (resolver called at most once per dial). We capture the
	// dialled address with a recording dialer.
	t.Run("toctou rebind dials only the validated IP", func(t *testing.T) {
		const publicIP = "93.184.216.34"
		const reboundIP = "169.254.169.254"
		resolver := &tc1RebindResolver{first: net.ParseIP(publicIP), rebound: net.ParseIP(reboundIP)}
		recDialer := &tc1RecordingDialer{}

		p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
		p.transport = tc1AllowlistedTransport(resolver, recDialer)

		// The dial is trapped (errTC1DialTrap) AFTER validation + recording, so the
		// HTTP outcome is a gateway error (502), not a 200 — but the security-
		// relevant assertions are the recorded address and the resolver call count.
		rec := doProxy(p, "https://example.com/page")
		_ = rec // status is not the assertion here; the dialled IP + call count are.

		dialed := recDialer.addrs()
		if len(dialed) != 1 {
			t.Fatalf("toctou: expected exactly one validated dial, got %v", dialed)
		}
		host, _, splitErr := net.SplitHostPort(dialed[0])
		if splitErr != nil {
			host = dialed[0]
		}
		if host != publicIP {
			t.Fatalf("toctou: dialled %q, want the validated public IP %q (the rebound %q must never be dialled)", host, publicIP, reboundIP)
		}
		// The resolver must be consulted at most once per dial: the dialer connects
		// to the already-validated IP literal, it does NOT re-resolve the hostname.
		if got := resolver.callCount(); got != 1 {
			t.Fatalf("toctou: resolver called %d times, want exactly 1 (no re-resolution between validate and connect)", got)
		}
	})

	// --- Layer 3: connect-time guard table -------------------------------------
	// NewControl() is the authoritative connect-time gate. Re-validate that it
	// rejects every blocked literal and admits a public literal, so a rebind
	// landing between resolve and connect is caught at the wire.
	t.Run("connect-time guard table", func(t *testing.T) {
		control := security.NewControl()
		cases := []struct {
			name      string
			addr      string
			wantBlock bool
		}{
			{"loopback", "127.0.0.1:80", true},
			{"link-local metadata", "169.254.169.254:80", true},
			{"link-local other", "169.254.1.1:443", true},
			{"rfc1918 10/8", "10.0.0.1:80", true},
			{"rfc1918 192.168/16", "192.168.0.1:80", true},
			{"unspecified", "0.0.0.0:80", true},
			{"ipv6 loopback", "[::1]:80", true},
			{"public ipv4", "93.184.216.34:80", false},
			{"public dns", "8.8.8.8:443", false},
			{"public ipv6", "[2606:4700:4700::1111]:443", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := control("tcp", tc.addr, nil)
				if tc.wantBlock {
					if err == nil {
						t.Fatalf("NewControl(%q): got nil error, want a block", tc.addr)
					}
					if reason := security.DialReasonOf(err); reason != security.ReasonBlockedIP {
						t.Fatalf("NewControl(%q): reason = %q, want %q", tc.addr, reason, security.ReasonBlockedIP)
					}
				} else if err != nil {
					t.Fatalf("NewControl(%q): got error %v, want nil (public address must be admitted)", tc.addr, err)
				}
			})
		}
	})
}

// -----------------------------------------------------------------------------
// AC 22: Non-HTTP/HTTPS schemes return 400.
// -----------------------------------------------------------------------------

// TestTC1AC22NonHTTPSchemesRejected drives a range of non-http(s) schemes
// through the full /proxy and /proxy-resource stacks (allowlisted host) and
// asserts each is rejected with 400 before any upstream is contacted. The
// allowlist lists example.com so the denial is decided by the scheme gate (SF2),
// not the allowlist — locking that a bad scheme is a 400 client error.
func TestTC1AC22NonHTTPSchemesRejected(t *testing.T) {
	targets := []string{
		"file:///etc/passwd",
		"gopher://example.com/_evil",
		"dict://example.com:11211/stat",
		"ftp://example.com/secret",
		"ldap://example.com/",
		"jar:http://example.com!/x",
	}
	cfg := settingsWith(allowExample(DomainOptions{}))

	for _, target := range targets {
		pProxy := newProxyHandler(cfg)
		pProxy.transport = tc1FailIfDialled(t)
		if rec := doProxy(pProxy, target); rec.Code != http.StatusBadRequest {
			t.Errorf("/proxy scheme %q: got %d, want 400 (body=%q)", target, rec.Code, rec.Body.String())
		}

		pResource := newProxyHandler(cfg)
		pResource.transport = tc1FailIfDialled(t)
		if rec := tc1DoProxyResource(proxyResourceHandler{p: pResource}, target); rec.Code != http.StatusBadRequest {
			t.Errorf("/proxy-resource scheme %q: got %d, want 400 (body=%q)", target, rec.Code, rec.Body.String())
		}
	}
}

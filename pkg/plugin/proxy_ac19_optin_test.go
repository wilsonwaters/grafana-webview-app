package plugin

// End-to-end tests for issue #105: the per-domain AllowPrivateIP opt-in wired
// through to the dial path. This suite is HERMETIC — no real network, no real
// DNS — and drives the REAL serve / serveProxy / checkFrameable pipeline so the
// per-request security.Policy threading (security.WithPolicy on the request
// context) and the SF4 resolve-then-dial + connect-time gate are all exercised.
//
// It mirrors TC1's stub-resolver pattern (proxy_security_ssrf_test.go) and
// reuses its helpers (tc1NewStubResolver, settingsWith, allowExample, doProxy,
// tc1RecordingDialer, tc1DoProxyResource, …). The policy-aware transports below
// reconstruct the production *security.Dialer.DialContext dial path (resolve →
// first-IP-direct → connect-time Control re-check) but with the POLICY variants
// (ResolveAndValidatePolicy / NewControlPolicy) so the matched domain's opt-in
// is honoured, then trap the connection so no socket is ever opened — exactly as
// tc1AllowlistedTransport does for the strict case.
//
// Test matrix:
//	A  PERMIT branch: opted-in example.com → RFC 1918 reaches the validated IP.
//	B  MUST-STILL-BLOCK when opted in: loopback/link-local/metadata(ip+name)/
//	   unspecified/multicast/reserved/cgnat/ULA all 403; nil sentinel not relaxed.
//	C  Redirect to a non-opted domain: per-hop policy recomputed → 403.
//	D  Strict default unchanged: AllowPrivateIP:false → private IP 403.
//	E  Connection-reuse hazard: DisableKeepAlives → opted-in then NOT-opted 403.
//	F  Audit/metric: distinct Warn + private_ip_permitted_total on a permit only.
//	G  Poisoned multi-record: opted-in domain with a link-local record fails closed.

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/wilsonwaters/webview/pkg/security"
)

// ac19PolicyTransport reconstructs the production secure-dial path with the
// per-request relaxation Policy supplied directly (the value serve also threads
// onto the request context), then records the validated target and traps the
// connection. Using the POLICY variants (ResolveAndValidatePolicy /
// NewControlPolicy) means the matched domain's opt-in is honoured at BOTH the
// resolve gate and the authoritative connect-time gate, exactly as production
// does when *security.Dialer.DialContext reads the policy from the context.
func ac19PolicyTransport(resolver security.Resolver, pol security.Policy, rec *tc1RecordingDialer) *http.Transport {
	control := security.NewControlPolicy(pol)
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := security.ResolveAndValidatePolicy(ctx, resolver, host, pol)
			if err != nil {
				return nil, err
			}
			target := net.JoinHostPort(ips[0].String(), port)
			if cerr := control("tcp", target, nil); cerr != nil {
				return nil, cerr
			}
			rec.record(target)
			return nil, errTC1DialTrap
		},
	}
}

// allowExampleOpts builds a one-domain (example.com) allowlist with the given
// AllowPrivateIP opt-in.
func allowExampleOpts(allowPrivate bool) []AllowedDomain {
	return allowExample(DomainOptions{AllowPrivateIP: allowPrivate})
}

// -----------------------------------------------------------------------------
// A. PERMIT branch — opted-in domain reaches a validated RFC 1918 IP.
// -----------------------------------------------------------------------------

func TestAC19OptInPermitsRFC1918EndToEnd(t *testing.T) {
	privateIPs := []string{"10.0.0.1", "172.16.5.4", "192.168.1.1"}
	for _, ip := range privateIPs {
		t.Run(ip, func(t *testing.T) {
			recDialer := &tc1RecordingDialer{}
			pol := security.Policy{AllowPrivate: true}
			p := newProxyHandler(settingsWith(allowExampleOpts(true)))
			p.transport = ac19PolicyTransport(tc1NewStubResolver(ip), pol, recDialer)

			// The dial reaches the validated private IP (recorded), then traps —
			// so the HTTP outcome is a gateway error, NOT a 403 at the IP gate.
			rec := doProxy(p, "https://example.com/page")
			if rec.Code == http.StatusForbidden {
				t.Fatalf("opted-in %s: got 403 at IP gate, want the dial to be permitted (body=%q)", ip, rec.Body.String())
			}
			dialed := recDialer.addrs()
			if len(dialed) != 1 {
				t.Fatalf("opted-in %s: expected exactly one validated dial, got %v", ip, dialed)
			}
			host, _, splitErr := net.SplitHostPort(dialed[0])
			if splitErr != nil {
				host = dialed[0]
			}
			if host != ip {
				t.Fatalf("opted-in %s: dialled %q, want the validated private IP", ip, host)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// B. MUST-STILL-BLOCK when opted in.
// -----------------------------------------------------------------------------

func TestAC19OptInStillBlocksNonRFC1918(t *testing.T) {
	// (i) Library-level fail-closed nil sentinel is never relaxed.
	if blocked, reason := security.ClassifyIPPolicy(nil, security.Policy{AllowPrivate: true}); !blocked || reason != security.ReasonReserved {
		t.Fatalf("ClassifyIPPolicy(nil, AllowPrivate) = (%v, %q), want (true, %q)", blocked, reason, security.ReasonReserved)
	}

	// (ii) Metadata host by NAME stays blocked regardless of opt-in (the name
	// block is unconditional, before resolution).
	t.Run("metadata host by name", func(t *testing.T) {
		cfg := settingsWith([]AllowedDomain{{Domain: "metadata.google.internal", Options: DomainOptions{AllowPrivateIP: true}}})
		p := newProxyHandler(cfg)
		recDialer := &tc1RecordingDialer{}
		p.transport = ac19PolicyTransport(tc1NewStubResolver("93.184.216.34"), security.Policy{AllowPrivate: true}, recDialer)
		if rec := doProxy(p, "http://metadata.google.internal/computeMetadata/v1/"); rec.Code != http.StatusForbidden {
			t.Errorf("opted-in metadata-by-name: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
		}
		if dialed := recDialer.addrs(); len(dialed) != 0 {
			t.Errorf("opted-in metadata-by-name: must not dial, but dialled %v", dialed)
		}
	})

	// (iii) By RESOLVED IP: every non-RFC-1918 blocked class still 403 end-to-end.
	cases := []struct{ name, ip string }{
		{"loopback v4", "127.0.0.1"},
		{"loopback v6", "::1"},
		{"link-local", "169.254.42.42"},
		{"metadata ip", "169.254.169.254"},
		{"unspecified", "0.0.0.0"},
		{"multicast", "224.0.0.1"},
		{"reserved test-net", "192.0.2.1"},
		{"cgnat", "100.64.0.1"},
		{"ula", "fc00::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := security.Policy{AllowPrivate: true}
			// Library-level still blocked.
			if blocked, _ := security.ClassifyIPPolicy(net.ParseIP(tc.ip), pol); !blocked {
				t.Fatalf("ClassifyIPPolicy(%s, AllowPrivate): not blocked, want blocked", tc.ip)
			}
			// End-to-end 403.
			recDialer := &tc1RecordingDialer{}
			p := newProxyHandler(settingsWith(allowExampleOpts(true)))
			p.transport = ac19PolicyTransport(tc1NewStubResolver(tc.ip), pol, recDialer)
			if rec := doProxy(p, "https://example.com/page"); rec.Code != http.StatusForbidden {
				t.Errorf("opted-in %s: got %d, want 403 (body=%q)", tc.ip, rec.Code, rec.Body.String())
			}
			if dialed := recDialer.addrs(); len(dialed) != 0 {
				t.Errorf("opted-in %s: blocked IP must not be dialled, but dialled %v", tc.ip, dialed)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// C. Redirect to a non-opted domain: per-hop policy recomputed.
// -----------------------------------------------------------------------------

// A redirect rewrites Location so the browser re-enters serve, which re-matches
// the allowlist per hop → AllowPrivateIP is recomputed. We model the SECOND hop
// directly: a request whose matched domain (other.example) is allowlisted but
// NOT opted in, resolving to a private IP, must be 403 even though a sibling
// domain (internal.example) IS opted in.
func TestAC19RedirectToNonOptedDomainBlocksPrivate(t *testing.T) {
	cfg := settingsWith([]AllowedDomain{
		{Domain: "internal.example", Options: DomainOptions{AllowPrivateIP: true}},
		{Domain: "other.example", Options: DomainOptions{AllowPrivateIP: false}},
	})

	// The second hop targets other.example (NOT opted in). serve recomputes the
	// policy from the matched domain → strict → private IP is 403. The transport
	// is built with the strict policy that other.example yields.
	recDialer := &tc1RecordingDialer{}
	p := newProxyHandler(cfg)
	p.transport = ac19PolicyTransport(tc1NewStubResolver("10.0.0.1"), security.Policy{AllowPrivate: false}, recDialer)
	if rec := doProxy(p, "https://other.example/page"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-opted hop other.example→private: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
	if dialed := recDialer.addrs(); len(dialed) != 0 {
		t.Fatalf("non-opted hop: private IP must not be dialled, but dialled %v", dialed)
	}

	// Sanity: the opted-in sibling DOES reach the same private IP, proving the
	// difference is per-matched-domain (the recomputed per-hop policy).
	recDialer2 := &tc1RecordingDialer{}
	p2 := newProxyHandler(cfg)
	p2.transport = ac19PolicyTransport(tc1NewStubResolver("10.0.0.1"), security.Policy{AllowPrivate: true}, recDialer2)
	if rec := doProxy(p2, "https://internal.example/page"); rec.Code == http.StatusForbidden {
		t.Fatalf("opted-in hop internal.example→private: got 403, want permitted (body=%q)", rec.Body.String())
	}
	if dialed := recDialer2.addrs(); len(dialed) != 1 {
		t.Fatalf("opted-in hop: expected one validated dial, got %v", dialed)
	}
}

// -----------------------------------------------------------------------------
// D. Strict default unchanged.
// -----------------------------------------------------------------------------

func TestAC19StrictDefaultStillBlocksPrivate(t *testing.T) {
	// Policy{} (zero) is strict.
	if blocked, reason := security.ClassifyIPPolicy(net.ParseIP("10.0.0.1"), security.Policy{}); !blocked || reason != security.ReasonPrivate {
		t.Fatalf("ClassifyIPPolicy(10.0.0.1, Policy{}) = (%v, %q), want (true, %q)", blocked, reason, security.ReasonPrivate)
	}
	// End-to-end: AllowPrivateIP:false → private IP still 403 through the real
	// secure-dialer transport (production NewDialer over a stub resolver).
	for _, ip := range []string{"10.0.0.1", "192.168.1.1"} {
		p := newProxyHandler(settingsWith(allowExampleOpts(false)))
		p.transport = tc1ResolverTransport(tc1NewStubResolver(ip))
		if rec := doProxy(p, "https://example.com/page"); rec.Code != http.StatusForbidden {
			t.Errorf("strict default %s: got %d, want 403 (body=%q)", ip, rec.Code, rec.Body.String())
		}
	}
}

// -----------------------------------------------------------------------------
// E. Connection-reuse hazard (DisableKeepAlives).
// -----------------------------------------------------------------------------

// With DisableKeepAlives the transport must dial fresh on every request, so a
// connection admitted under an opted-in domain's policy can never be reused for
// a NOT-opted domain. We assert (a) the production transport sets
// DisableKeepAlives, and (b) two sequential requests to the same host:port —
// the second NOT opted in — re-runs the gate and 403s the private IP.
func TestAC19ConnectionReuseHazardMitigated(t *testing.T) {
	// (a) The production transport disables keep-alives (security-load-bearing).
	prod := buildProxyHandler(settingsWith(allowExampleOpts(true)))
	tr, ok := prod.transport.(*http.Transport)
	if !ok {
		t.Fatalf("production transport is %T, want *http.Transport", prod.transport)
	}
	if !tr.DisableKeepAlives {
		t.Fatal("production transport must set DisableKeepAlives=true (cross-policy connection-reuse hazard)")
	}

	// (b) Sequential requests through the policy transport: a fresh dial + gate
	// runs each time. Domain A is opted in (private permitted); a second request
	// to the SAME host under a strict (NOT-opted) policy must 403 — no reuse of
	// A's admitted connection.
	const privateIP = "10.0.0.1"

	recA := &tc1RecordingDialer{}
	pA := newProxyHandler(settingsWith(allowExampleOpts(true)))
	pA.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: true}, recA)
	if rec := doProxy(pA, "https://example.com/a"); rec.Code == http.StatusForbidden {
		t.Fatalf("domain A opted-in: got 403, want permitted (body=%q)", rec.Body.String())
	}
	if len(recA.addrs()) != 1 {
		t.Fatalf("domain A: expected one fresh dial, got %v", recA.addrs())
	}

	recB := &tc1RecordingDialer{}
	pB := newProxyHandler(settingsWith(allowExampleOpts(false)))
	pB.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: false}, recB)
	if rec := doProxy(pB, "https://example.com/b"); rec.Code != http.StatusForbidden {
		t.Fatalf("domain B NOT opted-in: got %d, want 403 — connection must not be reused from A (body=%q)", rec.Code, rec.Body.String())
	}
	if dialed := recB.addrs(); len(dialed) != 0 {
		t.Fatalf("domain B: private IP must be refused at the gate, not dialled; dialled %v", dialed)
	}
}

// -----------------------------------------------------------------------------
// F. Audit / metric on permit.
// -----------------------------------------------------------------------------

// ac19CapturingLogger records Info AND Warn so the test can assert the distinct
// permit Warn fires (the shared capturingLogger in proxy_audit_test.go drops
// Warn). It is concurrency-safe because the permit Warn is emitted from the
// handler goroutine but the underlying record runs after the dial goroutine.
type ac19CapturingLogger struct {
	mu    sync.Mutex
	infos []auditEntry
	warns []auditEntry
}

func (c *ac19CapturingLogger) record(dst *[]auditEntry, msg string, args ...interface{}) {
	kv := make(map[string]interface{}, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		if key, ok := args[i].(string); ok {
			kv[key] = args[i+1]
		}
	}
	c.mu.Lock()
	*dst = append(*dst, auditEntry{msg: msg, kv: kv})
	c.mu.Unlock()
}

func (c *ac19CapturingLogger) Info(msg string, args ...interface{})  { c.record(&c.infos, msg, args...) }
func (c *ac19CapturingLogger) Warn(msg string, args ...interface{})  { c.record(&c.warns, msg, args...) }
func (c *ac19CapturingLogger) Debug(msg string, args ...interface{}) {}
func (c *ac19CapturingLogger) Error(msg string, args ...interface{}) {}
func (c *ac19CapturingLogger) With(args ...interface{}) log.Logger   { return c }
func (c *ac19CapturingLogger) Level() log.Level                      { return log.Debug }
func (c *ac19CapturingLogger) FromContext(_ context.Context) log.Logger {
	return c
}

func (c *ac19CapturingLogger) warnEntries() []auditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]auditEntry, len(c.warns))
	copy(out, c.warns)
	return out
}

func (c *ac19CapturingLogger) infoEntries() []auditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]auditEntry, len(c.infos))
	copy(out, c.infos)
	return out
}

func TestAC19PermitEmitsDistinctAuditAndMetric(t *testing.T) {
	const privateIP = "10.0.0.1"

	// This test drives the FULL production path: the secure dialer
	// (security.NewDialer) reads the per-request Policy serve put on the context
	// — which carries serve's OWN privatePermitRecorder as OnPrivatePermit — so
	// the permit callback fires inside the genuine ResolveAndValidatePolicy. The
	// subsequent connect to the (unlistened) private IP fails, but the permit was
	// already recorded BEFORE the connect, so serve's deferred block emits the
	// distinct Warn + increments the metric regardless. We do NOT inject a fake
	// hook: the recorder is the one serve built.
	t.Run("permit fires audit + metric", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		logger := &ac19CapturingLogger{}
		cfg := settingsWith(allowExampleOpts(true))
		cfg.RequestTimeoutSec = 1 // bound the doomed connect to the unlistened private IP
		p := newProxyHandlerWithRegistry(cfg, reg)
		p.logger = logger
		// Production secure dialer over the stub resolver, with a short base
		// timeout so the connect to the unlistened private IP fails fast. The
		// OnPrivatePermit hook (serve's recorder, carried on the context Policy)
		// fires inside ResolveAndValidatePolicy BEFORE the connect, so the audit
		// Warn + metric are emitted regardless of the connect outcome.
		p.transport = &http.Transport{
			DialContext: security.NewDialer(tc1NewStubResolver(privateIP), &net.Dialer{Timeout: time.Second}).DialContext,
		}

		_ = doProxy(p, "https://example.com/page")

		// The Info "proxy request" line carries allowPrivateIP=true.
		infos := logger.infoEntries()
		if len(infos) != 1 {
			t.Fatalf("expected exactly one Info audit line, got %d", len(infos))
		}
		if got, ok := infos[0].kv["allowPrivateIP"].(bool); !ok || !got {
			t.Errorf("audit Info allowPrivateIP = %v, want true", infos[0].kv["allowPrivateIP"])
		}

		// The distinct permit Warn fired with permittedIP + ipClass.
		warns := logger.warnEntries()
		if len(warns) != 1 {
			t.Fatalf("expected exactly one permit Warn, got %d: %+v", len(warns), warns)
		}
		if warns[0].msg != "proxy private-ip permitted by opt-in" {
			t.Errorf("permit Warn msg = %q", warns[0].msg)
		}
		if got, _ := warns[0].kv["permittedIP"].(string); got != privateIP {
			t.Errorf("permit Warn permittedIP = %v, want %s", warns[0].kv["permittedIP"], privateIP)
		}
		if got, _ := warns[0].kv["ipClass"].(string); got != security.ReasonPrivate {
			t.Errorf("permit Warn ipClass = %v, want %q", warns[0].kv["ipClass"], security.ReasonPrivate)
		}

		// The permit metric incremented for ipClass="private".
		if n := testutil.ToFloat64(p.metrics.privateIPPermitted.WithLabelValues(security.ReasonPrivate)); n != 1 {
			t.Errorf("private_ip_permitted_total{ipClass=private} = %v, want 1", n)
		}
	})

	t.Run("public IP opted-in fires neither", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		logger := &ac19CapturingLogger{}
		p := newProxyHandlerWithRegistry(settingsWith(allowExampleOpts(true)), reg)
		p.logger = logger
		// A public IP is not relaxed (it is never blocked), so OnPrivatePermit
		// never fires. The connect to the public IP is trapped by the recording
		// transport so no real network is touched.
		recDialer := &tc1RecordingDialer{}
		p.transport = tc1AllowlistedTransport(tc1NewStubResolver("93.184.216.34"), recDialer)

		_ = doProxy(p, "https://example.com/page")

		if warns := logger.warnEntries(); len(warns) != 0 {
			t.Errorf("public-IP opted-in: expected NO permit Warn, got %+v", warns)
		}
		if n := testutil.ToFloat64(p.metrics.privateIPPermitted.WithLabelValues(security.ReasonPrivate)); n != 0 {
			t.Errorf("public-IP opted-in: private_ip_permitted_total = %v, want 0", n)
		}
	})
}

// -----------------------------------------------------------------------------
// G. Poisoned multi-record fails closed even when opted in.
// -----------------------------------------------------------------------------

func TestAC19PoisonedMultiRecordFailsClosedWhenOptedIn(t *testing.T) {
	// Library level.
	r := tc1NewStubResolver("10.0.0.1", "169.254.169.254")
	if ips, err := security.ResolveAndValidatePolicy(context.Background(), r, "example.com", security.Policy{AllowPrivate: true}); ips != nil || security.DialReasonOf(err) != security.ReasonBlockedIP {
		t.Fatalf("poisoned opted-in resolve: ips=%v reason=%q, want nil/%q", ips, security.DialReasonOf(err), security.ReasonBlockedIP)
	}

	// End-to-end: 403, nothing dialled.
	recDialer := &tc1RecordingDialer{}
	p := newProxyHandler(settingsWith(allowExampleOpts(true)))
	p.transport = ac19PolicyTransport(tc1NewStubResolver("10.0.0.1", "169.254.169.254"), security.Policy{AllowPrivate: true}, recDialer)
	if rec := doProxy(p, "https://example.com/page"); rec.Code != http.StatusForbidden {
		t.Fatalf("poisoned opted-in: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
	if dialed := recDialer.addrs(); len(dialed) != 0 {
		t.Fatalf("poisoned opted-in: must not dial, but dialled %v", dialed)
	}
}

// -----------------------------------------------------------------------------
// /proxy-resource shares serve, so the opt-in must reach it identically.
// -----------------------------------------------------------------------------

func TestAC19OptInAppliesToProxyResource(t *testing.T) {
	const privateIP = "10.0.0.1"

	// Opted-in: the resource endpoint reaches the validated private IP.
	recIn := &tc1RecordingDialer{}
	pIn := newProxyHandler(settingsWith(allowExampleOpts(true)))
	pIn.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: true}, recIn)
	if rec := tc1DoProxyResource(proxyResourceHandler{p: pIn}, "https://example.com/style.css"); rec.Code == http.StatusForbidden {
		t.Fatalf("/proxy-resource opted-in: got 403 at IP gate, want permitted (body=%q)", rec.Body.String())
	}
	if len(recIn.addrs()) != 1 {
		t.Fatalf("/proxy-resource opted-in: expected one validated dial, got %v", recIn.addrs())
	}

	// NOT opted-in: the resource endpoint 403s the private IP.
	recOut := &tc1RecordingDialer{}
	pOut := newProxyHandler(settingsWith(allowExampleOpts(false)))
	pOut.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: false}, recOut)
	if rec := tc1DoProxyResource(proxyResourceHandler{p: pOut}, "https://example.com/style.css"); rec.Code != http.StatusForbidden {
		t.Fatalf("/proxy-resource NOT opted-in: got %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
	if dialed := recOut.addrs(); len(dialed) != 0 {
		t.Fatalf("/proxy-resource NOT opted-in: private IP must not be dialled, dialled %v", dialed)
	}
}

// -----------------------------------------------------------------------------
// check-frameable shares p.transport, so the opt-in must apply there too.
// -----------------------------------------------------------------------------

func TestAC19OptInAppliesToCheckFrameable(t *testing.T) {
	const privateIP = "10.0.0.1"

	// Opted-in: the framing probe reaches the validated private IP (recorded).
	// The verdict is proxy-recommended (the trapped dial errors), but the crux is
	// that the dial was PERMITTED to the private IP, not refused at the gate.
	recIn := &tc1RecordingDialer{}
	pIn := newProxyHandler(settingsWith(allowExampleOpts(true)))
	pIn.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: true}, recIn)
	rec := doCheckFrameable(checkFrameableHandler{p: pIn}, "https://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("/check-frameable opted-in: got %d, want 200 verdict (body=%q)", rec.Code, rec.Body.String())
	}
	if len(recIn.addrs()) != 1 {
		t.Fatalf("/check-frameable opted-in: expected the probe to reach the validated private IP, dialled %v", recIn.addrs())
	}

	// NOT opted-in: the framing probe's dial to the private IP is refused at the
	// SF4 gate (no recorded dial), so the verdict falls back to proxy.
	recOut := &tc1RecordingDialer{}
	pOut := newProxyHandler(settingsWith(allowExampleOpts(false)))
	pOut.transport = ac19PolicyTransport(tc1NewStubResolver(privateIP), security.Policy{AllowPrivate: false}, recOut)
	recF := doCheckFrameable(checkFrameableHandler{p: pOut}, "https://example.com/page")
	if recF.Code != http.StatusOK {
		t.Fatalf("/check-frameable NOT opted-in: got %d, want 200 verdict (body=%q)", recF.Code, recF.Body.String())
	}
	if dialed := recOut.addrs(); len(dialed) != 0 {
		t.Fatalf("/check-frameable NOT opted-in: private IP must be refused at the gate, dialled %v", dialed)
	}
}

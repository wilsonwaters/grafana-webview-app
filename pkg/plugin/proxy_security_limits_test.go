package plugin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TC2 — Security test suite for acceptance criteria 23–29.
//
// Each AC is covered by its own named TestSecurityTC2_AC<n>_... function driving
// the FULL endpoint stack through the real proxyHandler.ServeHTTP with a
// hermetic, loopback-only httptest upstream (no external network, no DNS). The
// suite REUSES the existing exported test helpers in this package — newTestHandler,
// newMetricsTestHandler, newRecordingTransport, withCapturingLogger,
// requireSingleAuditEntry, doProxy, allowExample, settingsWith, redirectUpstream,
// requestsTotalFor, denialsTotalFor — rather than redefining them. The very few
// helpers added here are file-local and tc2-prefixed so they cannot collide with a
// sibling PR landing in the same package.
//
// These tests assert the OBSERVABLE security outcome (HTTP status, header
// presence/absence, a captured audit line's fields, scraped metric values) so a
// regression in any criterion fails the build.

// tc2MultiDomainAllowlist returns an allowlist of several exact-match hosts. It is
// used by the per-instance rate-limit test (AC 25) so requests can be spread
// across DISTINCT allowlisted domains — making the per-DOMAIN tier a non-factor
// and forcing the shared per-INSTANCE bucket to be the only thing that can deny.
func tc2MultiDomainAllowlist(hosts ...string) []AllowedDomain {
	out := make([]AllowedDomain, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, AllowedDomain{Domain: h, Options: DomainOptions{}})
	}
	return out
}

// tc2DialAllUpstream swaps in a recording transport that dials the given loopback
// upstream for ANY requested host, so a multi-domain allowlist test can complete
// real round-trips to every allowlisted host without DNS. It is applied to a
// handler the caller already built (e.g. one created with newProxyHandler for a
// multi-host allowlist, which newTestHandler's single-domain helper does not cover).
func tc2DialAllUpstream(t *testing.T, p *proxyHandler, upstream *httptest.Server) {
	t.Helper()
	if _, err := url.Parse(upstream.URL); err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	p.transport = newRecordingTransport(upstream)
}

// ---------------------------------------------------------------------------
// AC 23: Redirect into a denied destination is blocked at the redirect step,
// not followed.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC23_RedirectIntoDeniedDestinationBlocked drives a 302 whose
// Location points at a NON-allowlisted host through the real ServeHTTP stack and
// asserts the redirect is refused at the redirect step: the response is a 403 and
// NO followable proxy URL to the denied host is emitted. A recording transport
// proves the denied host is never dialled — the redirect is blocked, not followed.
func TestSecurityTC2_AC23_RedirectIntoDeniedDestinationBlocked(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "https://evil.com/landing")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{})) // only example.com allowlisted
	p := newProxyHandler(cfg)
	rt := newRecordingTransport(upstream)
	p.transport = rt

	rec := doProxy(p, "http://example.com/start")

	// Blocked at the redirect step ⇒ 403 (allowlist reason), not a 3xx the browser
	// could follow.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("redirect into denied host: got status %d, want 403 (body=%q)", rec.Code, rec.Body.String())
	}
	// No followable Location to the denied host, and no proxy URL to chase it.
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "evil.com") || strings.Contains(loc, proxyPath+"?") {
		t.Fatalf("Location = %q, want NO followable URL to the denied destination", loc)
	}
	// The denied host must never be dialled: only the first hop (example.com) was
	// ever contacted; the redirect was refused before any second-hop dial.
	for _, h := range rt.dialedHosts() {
		if h == "evil.com" {
			t.Fatalf("proxy dialled the denied redirect host %q; it must be blocked, not followed", h)
		}
	}
}

// TestSecurityTC2_AC23_RedirectAllowlistedHostRewritten is the positive control:
// a redirect to an ALLOWLISTED host is rewritten to a re-entrant proxy URL (so the
// browser re-validates the next hop) rather than blocked — confirming AC 23 blocks
// only the DENIED destination, not every redirect.
func TestSecurityTC2_AC23_RedirectAllowlistedHostRewritten(t *testing.T) {
	upstream := redirectUpstream(t, http.StatusFound, "http://example.com/next")
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/start")
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect to allowlisted host: got status %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, resourceBase+proxyPath+"?") {
		t.Fatalf("Location = %q, want a re-entrant /proxy URL", loc)
	}
}

// ---------------------------------------------------------------------------
// AC 24: Response larger than configured max-body-size is rejected with 413.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC24_OversizeResponseRejected413 drives an upstream that
// declares a Content-Length exceeding the configured MaxResponseBytes through the
// real ServeHTTP stack and asserts a clean 413 with NO oversized body leaked. A
// within-limit control confirms the limit is the trigger, not an unrelated failure.
func TestSecurityTC2_AC24_OversizeResponseRejected413(t *testing.T) {
	const oversized = "this upstream body is comfortably larger than the configured limit"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, oversized); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 16 // far below len(oversized)
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/big")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize response: got status %d, want 413 (body=%q)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), oversized) {
		t.Fatalf("413 path must not leak the oversized upstream body, got %q", rec.Body.String())
	}
}

// TestSecurityTC2_AC24_WithinLimitResponsePasses is the boundary control for
// AC 24: a body AT the configured limit passes through as 200, proving the 413 is
// driven by the size policy rather than rejecting everything.
func TestSecurityTC2_AC24_WithinLimitResponsePasses(t *testing.T) {
	const body = "0123456789ABCDEF" // exactly 16 bytes
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = int64(len(body)) // allow exactly the body size
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/atlimit")
	if rec.Code != http.StatusOK {
		t.Fatalf("at-limit response: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AC 25: Request exceeding the per-instance rate limit returns 429.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC25_PerInstanceRateLimitReturns429 specifically exercises the
// per-INSTANCE tier (distinct from the per-domain tier proxy_test.go covers). The
// per-instance limit is set to 1/min (a burst of 1) while the per-domain limit is
// generous, and the two requests target DIFFERENT allowlisted domains. Because the
// per-domain buckets are separate but the per-instance bucket is shared, only the
// per-instance tier can deny the second request — proving the 429 comes from the
// per-instance limit, not the per-domain one.
func TestSecurityTC2_AC25_PerInstanceRateLimitReturns429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(tc2MultiDomainAllowlist("a.example", "b.example"))
	cfg.RateLimitPerInstancePerMin = 1  // burst 1: only the FIRST request across the whole instance passes
	cfg.RateLimitPerDomainPerMin = 1000 // generous: per-domain tier never denies here
	cfg.MaxConcurrentRequests = 100     // concurrency never the limiter
	p := newProxyHandler(cfg)
	tc2DialAllUpstream(t, p, upstream)

	// First request (to a.example) consumes the single instance token ⇒ 200.
	if rec := doProxy(p, "http://a.example/one"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	// Second request to a DIFFERENT domain (b.example) — its own per-domain bucket
	// is full, so any denial must come from the shared per-instance bucket ⇒ 429.
	if rec := doProxy(p, "http://b.example/two"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (per-instance limited): got status %d, want 429 (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestSecurityTC2_AC25_PerInstanceRateLimitMetricReason confirms the per-instance
// 429 is recorded as denials_total{reason="rate-limit"} on an isolated registry,
// tying the AC 25 outcome to the metric the operator would alert on.
func TestSecurityTC2_AC25_PerInstanceRateLimitMetricReason(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(tc2MultiDomainAllowlist("a.example", "b.example"))
	cfg.RateLimitPerInstancePerMin = 1
	cfg.RateLimitPerDomainPerMin = 1000
	cfg.MaxConcurrentRequests = 100
	p, _ := newMetricsTestHandler(t, cfg, nil)
	tc2DialAllUpstream(t, p, upstream)

	if rec := doProxy(p, "http://a.example/one"); rec.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200", rec.Code)
	}
	if rec := doProxy(p, "http://b.example/two"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got status %d, want 429", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusTooManyRequests); got != 1 {
		t.Fatalf("requests_total{status=429}: got %v, want 1", got)
	}
	if got := denialsTotalFor(t, p, denialReasonRateLimit); got != 1 {
		t.Fatalf("denials_total{reason=rate-limit}: got %v, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// AC 26: Cookie, Authorization, and X-Grafana-* headers are stripped from
// outgoing requests.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC26_OutgoingHeadersStripped sends an inbound request carrying
// Cookie, Authorization, and several X-Grafana-* headers and asserts NONE of them
// reach the upstream. The stub upstream records exactly the headers it received;
// the assertion is on those received headers — the genuine outgoing request.
func TestSecurityTC2_AC26_OutgoingHeadersStripped(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+url.QueryEscape("http://example.com/page"), nil)
	req.Header.Set("Cookie", "grafana_session=topsecret")
	req.Header.Set("Authorization", "Bearer leaked-token")
	req.Header.Set("X-Grafana-Id", "id-token")
	req.Header.Set("X-Grafana-Org-Id", "7")
	req.Header.Set("X-Grafana-Device-Id", "device-abc")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if received == nil {
		t.Fatal("upstream recorded no request headers")
	}

	// The three categories named in AC 26 must be ABSENT on the outgoing request.
	if v := received.Get("Cookie"); v != "" {
		t.Errorf("Cookie leaked to upstream: %q", v)
	}
	if v := received.Get("Authorization"); v != "" {
		t.Errorf("Authorization leaked to upstream: %q", v)
	}
	// No X-Grafana-* header of any kind may survive the prefix sweep.
	for key := range received {
		if strings.HasPrefix(strings.ToLower(key), "x-grafana-") {
			t.Errorf("X-Grafana-* header %q leaked to upstream", key)
		}
	}
}

// ---------------------------------------------------------------------------
// AC 27: Set-Cookie, Strict-Transport-Security, Public-Key-Pins are stripped
// from incoming responses.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC27_IncomingResponseHeadersStripped has the upstream SET
// Set-Cookie (multiple values), Strict-Transport-Security, and Public-Key-Pins,
// then asserts ALL of them are absent from the proxied response after the real
// path through ModifyResponse, while a benign Content-Type survives.
func TestSecurityTC2_AC27_IncomingResponseHeadersStripped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Add("Set-Cookie", "sid=secret; Path=/; HttpOnly")
		h.Add("Set-Cookie", "track=xyz; Path=/") // a second value: all must go
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		h.Set("Public-Key-Pins", `pin-sha256="abc"; max-age=5184000`)
		h.Set("Content-Type", "text/plain; charset=utf-8") // benign: must survive
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	for _, name := range []string{"Set-Cookie", "Strict-Transport-Security", "Public-Key-Pins"} {
		if v := rec.Header().Values(name); len(v) != 0 {
			t.Errorf("response header %q should be stripped, got %v", name, v)
		}
	}
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Error("benign Content-Type should be preserved on the proxied response")
	}
}

// ---------------------------------------------------------------------------
// AC 28: Every proxy request emits a structured audit log entry with target URL,
// status, size, duration.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC28_AuditEntryCarriesURLStatusSizeDuration drives a successful
// request through the real stack with an injected capturing logger and asserts the
// single audit entry carries the four AC-28 fields: the target URL, the status,
// the size (body bytes), and a duration. The byte count is asserted to match the
// served body exactly.
func TestSecurityTC2_AC28_AuditEntryCarriesURLStatusSizeDuration(t *testing.T) {
	const body = "audited upstream body"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	e := requireSingleAuditEntry(t, capLog)

	// Target URL: the validated, normalised target.
	if got, ok := e.kv["url"].(string); !ok || got != "http://example.com/page" {
		t.Errorf("audit url: got %v, want %q", e.kv["url"], "http://example.com/page")
	}
	// Status.
	if got, ok := e.kv["status"].(int); !ok || got != http.StatusOK {
		t.Errorf("audit status: got %v, want 200", e.kv["status"])
	}
	// Size: the body bytes written to the client.
	if got, ok := e.kv["bytes"].(int64); !ok || got != int64(len(body)) {
		t.Errorf("audit bytes: got %v, want %d", e.kv["bytes"], len(body))
	}
	// Duration: present in the structured entry.
	if _, ok := e.kv["duration"]; !ok {
		t.Errorf("audit entry missing duration field")
	}
}

// TestSecurityTC2_AC28_AuditEntryEmittedOnDenial confirms AC 28's "EVERY proxy
// request" scope by asserting a DENIED request (empty allowlist ⇒ 403) also emits
// exactly one structured entry carrying the caller's target URL, the 403 status,
// a body size, and a duration. This is the error-path half of the criterion.
func TestSecurityTC2_AC28_AuditEntryEmittedOnDenial(t *testing.T) {
	p := newProxyHandler(settingsWith(nil)) // empty allowlist ⇒ fail-closed 403
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "https://example.com/secret")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denial: got status %d, want 403", rec.Code)
	}

	e := requireSingleAuditEntry(t, capLog)
	if got, ok := e.kv["url"].(string); !ok || got != "https://example.com/secret" {
		t.Errorf("audit url on denial: got %v, want %q", e.kv["url"], "https://example.com/secret")
	}
	if got, ok := e.kv["status"].(int); !ok || got != http.StatusForbidden {
		t.Errorf("audit status on denial: got %v, want 403", e.kv["status"])
	}
	if got, ok := e.kv["bytes"].(int64); !ok || got <= 0 {
		t.Errorf("audit bytes on denial: got %v, want > 0 (http.Error wrote a body)", e.kv["bytes"])
	}
	if _, ok := e.kv["duration"]; !ok {
		t.Errorf("audit entry on denial missing duration field")
	}
}

// ---------------------------------------------------------------------------
// AC 29: Prometheus metrics are exposed for proxy requests, denials by reason,
// in-flight, and duration.
// ---------------------------------------------------------------------------

// TestSecurityTC2_AC29_AllFourMetricFamiliesExposed asserts the four AC-29 metric
// families are exposed on an isolated registry by their canonical names after the
// full stack runs: requests (requests_total), denials by reason (denials_total),
// in-flight (requests_in_flight), and duration (request_duration_seconds). One
// success and one denial are driven so the lazily-registered *_total families
// materialise; the in-flight gauge is always present.
func TestSecurityTC2_AC29_AllFourMetricFamiliesExposed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, reg := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	if rec := doProxy(p, "http://example.com/ok"); rec.Code != http.StatusOK {
		t.Fatalf("success: got status %d, want 200", rec.Code)
	}
	if rec := doProxy(p, "http://denied.test/x"); rec.Code != http.StatusForbidden {
		t.Fatalf("denial: got status %d, want 403", rec.Code)
	}

	// All four families must be gatherable from the registry by name.
	for _, name := range []string{
		metricRequestsTotal,
		metricDenialsTotal,
		metricRequestsInFlight,
		metricRequestDurationSec,
	} {
		if n, err := testutil.GatherAndCount(reg, name); err != nil {
			t.Fatalf("gather %s: %v", name, err)
		} else if n == 0 {
			t.Fatalf("metric family %s is not exposed on the registry", name)
		}
	}
}

// TestSecurityTC2_AC29_MetricsIncrementOnSuccessAndDenial asserts the families
// carry the RIGHT values across a success and a denial on an isolated registry:
// requests_total{status=200} and {status=403} each at 1, denials_total keyed by
// the denial reason (allowlist) at 1, the duration histogram materialised as one
// series, and the in-flight gauge settled back to 0.
func TestSecurityTC2_AC29_MetricsIncrementOnSuccessAndDenial(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, _ := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	if rec := doProxy(p, "http://example.com/ok"); rec.Code != http.StatusOK {
		t.Fatalf("success: got status %d, want 200", rec.Code)
	}
	if rec := doProxy(p, "http://denied.test/x"); rec.Code != http.StatusForbidden {
		t.Fatalf("denial: got status %d, want 403", rec.Code)
	}

	if got := requestsTotalFor(t, p, http.StatusOK); got != 1 {
		t.Fatalf("requests_total{status=200}: got %v, want 1", got)
	}
	if got := requestsTotalFor(t, p, http.StatusForbidden); got != 1 {
		t.Fatalf("requests_total{status=403}: got %v, want 1", got)
	}
	// Denials by reason: the non-allowlisted host is recorded under the allowlist reason.
	if got := denialsTotalFor(t, p, denialReasonAllowlist); got != 1 {
		t.Fatalf("denials_total{reason=allowlist}: got %v, want 1", got)
	}
	// Duration: a single histogram series materialised (two requests observed into it).
	if got := testutil.CollectAndCount(p.metrics.duration); got != 1 {
		t.Fatalf("duration histogram series count: got %v, want 1", got)
	}
	// In-flight settled back to 0 once both requests completed.
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Fatalf("in_flight after completion: got %v, want 0", got)
	}
}

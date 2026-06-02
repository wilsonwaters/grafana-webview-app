package plugin

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/wilsonwaters/webview/pkg/security"
)

// P7 — denial → (HTTP status, denials_total{reason}) wiring.
//
// These tests lock the FULL denial→(status, reason) matrix: for every denial
// class they assert BOTH the HTTP status returned to the client AND that the
// correct denials_total{reason} label is incremented exactly once, driven
// through a fresh injected prometheus registry (newMetricsTestHandler). This is
// what guarantees the status sent to the client and the reason recorded on the
// metric can never silently drift apart.

// TestReasonStatusTableExhaustiveAndConsistent guards the authoritative
// reasonStatus table itself: every denial-reason constant must have an entry
// (so statusForReason never falls through to 500), and each entry must carry the
// status the P7 matrix requires. A new reason added without a table row, or a
// row whose status is changed, fails here immediately.
func TestReasonStatusTableExhaustiveAndConsistent(t *testing.T) {
	want := map[string]int{
		denialReasonAllowlist:   http.StatusForbidden,             // 403
		denialReasonIPBlocklist: http.StatusForbidden,             // 403
		denialReasonMetadata:    http.StatusForbidden,             // 403
		denialReasonRateLimit:   http.StatusTooManyRequests,       // 429
		denialReasonConcurrency: http.StatusTooManyRequests,       // 429
		denialReasonSizeLimit:   http.StatusRequestEntityTooLarge, // 413
		denialReasonScheme:      http.StatusBadRequest,            // 400
		denialReasonBadRequest:  http.StatusBadRequest,            // 400
		denialReasonMethod:      http.StatusMethodNotAllowed,      // 405
		denialReasonTimeout:     http.StatusGatewayTimeout,        // 504
		denialReasonUpstream:    http.StatusBadGateway,            // 502
		denialReasonRedirect:    http.StatusBadGateway,            // 502 (CR4 redirect depth cap)
	}
	for reason, wantStatus := range want {
		if got := statusForReason(reason); got != wantStatus {
			t.Errorf("statusForReason(%q) = %d, want %d", reason, got, wantStatus)
		}
	}
	// The table must contain EXACTLY the known reasons — no stray rows that could
	// shadow a real denial path with the wrong status.
	if len(reasonStatus) != len(want) {
		t.Fatalf("reasonStatus has %d entries, want %d (table/reason-set drift)", len(reasonStatus), len(want))
	}
	// An unknown reason must surface loudly as a 500, never a silent 200.
	if got := statusForReason("does-not-exist"); got != http.StatusInternalServerError {
		t.Fatalf("statusForReason(unknown) = %d, want 500", got)
	}
}

// TestDenialMatrixStatusAndMetricReason is the core P7 test: a table covering
// every in-handler (pre-upstream) denial class plus the timeout error-handler
// path, asserting for EACH that (a) the client receives the mapped HTTP status
// AND (b) denials_total increments the expected reason label exactly once, with
// no other reason touched. The IP-blocklist and cloud-metadata 403 paths run
// through the SF4 dialer and are covered by TestProxyErrorHandlerReasonMapping.
func TestDenialMatrixStatusAndMetricReason(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus int
		wantReason string
		// drive sets up the handler and issues the request that triggers the
		// denial, returning the recorder. It receives a freshly-built handler bound
		// to an isolated registry; some cases need to swap the transport first.
		drive func(t *testing.T, p *proxyHandler) *httptest.ResponseRecorder
	}{
		{
			name:       "method not allowed => 405 / method",
			wantStatus: http.StatusMethodNotAllowed,
			wantReason: denialReasonMethod,
			drive: func(t *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/proxy?url="+url.QueryEscape("http://example.com/x"), nil)
				rec := httptest.NewRecorder()
				p.ServeHTTP(rec, req)
				return rec
			},
		},
		{
			name:       "missing url param => 400 / bad-request",
			wantStatus: http.StatusBadRequest,
			wantReason: denialReasonBadRequest,
			drive: func(t *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
				rec := httptest.NewRecorder()
				p.ServeHTTP(rec, req)
				return rec
			},
		},
		{
			name:       "bad scheme => 400 / scheme",
			wantStatus: http.StatusBadRequest,
			wantReason: denialReasonScheme,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				return doProxy(p, "ftp://example.com/x")
			},
		},
		{
			name:       "disallowed port => 400 / scheme",
			wantStatus: http.StatusBadRequest,
			wantReason: denialReasonScheme,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				return doProxy(p, "https://example.com:8443/page")
			},
		},
		{
			name:       "allowlist no-match => 403 / allowlist",
			wantStatus: http.StatusForbidden,
			wantReason: denialReasonAllowlist,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				return doProxy(p, "https://evil.example/page")
			},
		},
		{
			name:       "rate-limit => 429 / rate-limit",
			wantStatus: http.StatusTooManyRequests,
			wantReason: denialReasonRateLimit,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				// Per-domain tier is 1/min in this case's cfg (set below); the first
				// request consumes it so the second is rate-limited. Both hit the same
				// loopback upstream wired by newMetricsTestHandler.
				doProxy(p, "http://example.com/first")
				return doProxy(p, "http://example.com/second")
			},
		},
		{
			name:       "concurrency cap => 429 / concurrency",
			wantStatus: http.StatusTooManyRequests,
			wantReason: denialReasonConcurrency,
			drive: func(t *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				// Hold the only concurrency slot so the request's Acquire() fails.
				release, ok := p.rateLimiter.Acquire()
				if !ok {
					t.Fatal("setup: expected to acquire the only slot")
				}
				defer release()
				return doProxy(p, "https://example.com/page")
			},
		},
		{
			name:       "oversize response => 413 / size-limit",
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: denialReasonSizeLimit,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				return doProxy(p, "http://example.com/big")
			},
		},
		{
			name:       "request timeout => 504 / timeout",
			wantStatus: http.StatusGatewayTimeout,
			wantReason: denialReasonTimeout,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				p.transport = blockingRoundTripper{} // deterministic deadline, no real network
				return doProxy(p, "http://example.com/slow")
			},
		},
		{
			name:       "redirect depth cap => 502 / redirect-loop",
			wantStatus: http.StatusBadGateway,
			wantReason: denialReasonRedirect,
			drive: func(_ *testing.T, p *proxyHandler) *httptest.ResponseRecorder {
				// Arrive at depth == MaxRedirects (3) so the upstream 302 trips the cap.
				return doProxyDepth(p, "http://example.com/loop", "3")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build a per-case handler against an isolated registry. Cases that need
			// a working upstream (rate-limit, oversize) get a loopback server; the
			// rest deny before any fetch.
			cfg := settingsWith(allowExample(DomainOptions{}))
			cfg.RequestTimeoutSec = 1

			var upstream *httptest.Server
			switch tc.wantReason {
			case denialReasonRateLimit:
				cfg.RateLimitPerInstancePerMin = 100
				cfg.RateLimitPerDomainPerMin = 1
				upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
			case denialReasonConcurrency:
				cfg.MaxConcurrentRequests = 1
			case denialReasonSizeLimit:
				const oversized = "this body is definitely longer than sixteen bytes"
				cfg.MaxResponseBytes = 16
				upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, oversized)
				}))
			case denialReasonRedirect:
				upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", "http://example.com/next")
					w.WriteHeader(http.StatusFound)
				}))
			}
			if upstream != nil {
				defer upstream.Close()
			}

			p, _ := newMetricsTestHandler(t, cfg, upstream)

			rec := tc.drive(t, p)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := requestsTotalFor(t, p, tc.wantStatus); got < 1 {
				t.Fatalf("requests_total{status=%d}: got %v, want >= 1", tc.wantStatus, got)
			}
			if got := denialsTotalFor(t, p, tc.wantReason); got != 1 {
				t.Fatalf("denials_total{reason=%q}: got %v, want 1", tc.wantReason, got)
			}
			// No OTHER denial reason should have been incremented for this single
			// denial outcome (the rate-limit case's first request is a success, not
			// a denial, so the total denial-series count is still 1).
			if got := testutil.CollectAndCount(p.metrics.denials); got != 1 {
				t.Fatalf("expected exactly one denial reason series, got %d", got)
			}
			if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
				t.Fatalf("in_flight after completion: got %v, want 0", got)
			}
		})
	}
}

// TestProxyErrorHandlerReasonMapping locks the post-upstream (ErrorHandler)
// denial matrix at the reason level — the SF4 dial-time 403s (blocked IP /
// cloud-metadata host), the resolve-failure 502, the size-limit 413, the
// timeout 504, and the generic upstream 502 — asserting BOTH the written status
// AND the returned P6 reason for each. This is where the P7 consolidation fixed
// the prior drift: the cloud-metadata host now maps to reason "metadata" (was
// "ip-blocklist") and the resolve failure to "upstream" (was "metadata"), while
// every status stays as before.
func TestProxyErrorHandlerReasonMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
	}{
		{"blocked IP => 403 / ip-blocklist", &security.DialError{Reason: security.ReasonBlockedIP, IPReason: "loopback", BlockedIP: net.ParseIP("127.0.0.1"), Message: "blocked"}, http.StatusForbidden, denialReasonIPBlocklist},
		{"metadata host => 403 / metadata", &security.DialError{Reason: security.ReasonMetadataHost, Message: "metadata"}, http.StatusForbidden, denialReasonMetadata},
		{"resolve failed => 502 / upstream", &security.DialError{Reason: security.ReasonResolveFailed, Message: "nxdomain"}, http.StatusBadGateway, denialReasonUpstream},
		{"no host => 502 / upstream", &security.DialError{Reason: security.ReasonNoHost, Message: "no host"}, http.StatusBadGateway, denialReasonUpstream},
		{"oversize => 413 / size-limit", errResponseTooLarge, http.StatusRequestEntityTooLarge, denialReasonSizeLimit},
		{"wrapped oversize => 413 / size-limit", fmt.Errorf("copy failed: %w", errResponseTooLarge), http.StatusRequestEntityTooLarge, denialReasonSizeLimit},
		{"net timeout => 504 / timeout", timeoutError{}, http.StatusGatewayTimeout, denialReasonTimeout},
		{"generic => 502 / upstream", io.ErrUnexpectedEOF, http.StatusBadGateway, denialReasonUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/proxy", nil)

			gotReason := proxyErrorHandler(rec, req, tc.err)

			if gotReason != tc.wantReason {
				t.Fatalf("reason: got %q, want %q", gotReason, tc.wantReason)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
			// The status written to the client must match what the returned reason
			// maps to in the authoritative table — the no-drift invariant.
			if want := statusForReason(gotReason); rec.Code != want {
				t.Fatalf("status %d does not match reasonStatus[%q]=%d", rec.Code, gotReason, want)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("error response missing CORS: %q", got)
			}
		})
	}
}

// TestProxyResponseAtExactLimitSucceeds folds in the tracked P4 boundary nit: a
// response whose Content-Length equals MaxResponseBytes EXACTLY must be served
// (200), not rejected — locking enforceResponseSize's strict-greater-than ('>')
// boundary so a future off-by-one to '>=' is caught. The body is delivered
// intact and no denial reason is recorded.
func TestProxyResponseAtExactLimitSucceeds(t *testing.T) {
	const limit = 16
	body := make([]byte, limit) // exactly at the limit
	for i := range body {
		body[i] = 'a'
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = limit
	p, _ := newMetricsTestHandler(t, cfg, upstream)

	rec := doProxy(p, "http://example.com/exact")
	if rec.Code != http.StatusOK {
		t.Fatalf("exactly-at-limit body: got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Len(); got != limit {
		t.Fatalf("exactly-at-limit body: delivered %d bytes, want %d", got, limit)
	}
	if got := testutil.CollectAndCount(p.metrics.denials); got != 0 {
		t.Fatalf("at-limit success must record no denial, got %d series", got)
	}
	if got := requestsTotalFor(t, p, http.StatusOK); got != 1 {
		t.Fatalf("requests_total{status=200}: got %v, want 1", got)
	}
}

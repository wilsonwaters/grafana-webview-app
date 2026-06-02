package plugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// auditEntry is a single captured Info() call: the message plus its flattened
// key/value pairs (the variadic args the structured logger received).
type auditEntry struct {
	msg string
	kv  map[string]interface{}
}

// capturingLogger implements log.Logger and records every Info() call so the
// audit tests can assert exactly one entry per request and inspect its fields.
// It satisfies the full log.Logger interface (the SDK ships no public test
// logger that captures args); only Info is exercised. Guarded by a mutex so it
// is safe even if a path logs from another goroutine.
type capturingLogger struct {
	mu      sync.Mutex
	entries []auditEntry
}

func (c *capturingLogger) record(msg string, args ...interface{}) {
	kv := make(map[string]interface{}, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		kv[key] = args[i+1]
	}
	c.mu.Lock()
	c.entries = append(c.entries, auditEntry{msg: msg, kv: kv})
	c.mu.Unlock()
}

func (c *capturingLogger) infoEntries() []auditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]auditEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

func (c *capturingLogger) Info(msg string, args ...interface{})     { c.record(msg, args...) }
func (c *capturingLogger) Debug(msg string, args ...interface{})    {}
func (c *capturingLogger) Warn(msg string, args ...interface{})     {}
func (c *capturingLogger) Error(msg string, args ...interface{})    {}
func (c *capturingLogger) With(args ...interface{}) log.Logger      { return c }
func (c *capturingLogger) Level() log.Level                         { return log.Debug }
func (c *capturingLogger) FromContext(_ context.Context) log.Logger { return c }

// requireSingleAuditEntry asserts exactly one "proxy request" Info entry was
// emitted and returns it. One emission per request is the core P5 guarantee.
func requireSingleAuditEntry(t *testing.T, c *capturingLogger) auditEntry {
	t.Helper()
	entries := c.infoEntries()
	if len(entries) != 1 {
		t.Fatalf("audit: got %d log entries, want exactly 1: %+v", len(entries), entries)
	}
	if entries[0].msg != "proxy request" {
		t.Fatalf("audit: got msg %q, want %q", entries[0].msg, "proxy request")
	}
	return entries[0]
}

// assertAuditFields verifies the always-present field set: url, user, status,
// bytes, duration. status/wantURL/wantUser are checked exactly; bytes is checked
// against wantBytes when >= 0 (pass -1 to skip, e.g. streamed/truncated paths);
// duration is only asserted to be present and a time.Duration.
func assertAuditFields(t *testing.T, e auditEntry, wantURL, wantUser string, wantStatus int, wantBytes int64) {
	t.Helper()
	if got, ok := e.kv["url"].(string); !ok || got != wantURL {
		t.Errorf("audit url: got %v, want %q", e.kv["url"], wantURL)
	}
	if got, ok := e.kv["user"].(string); !ok || got != wantUser {
		t.Errorf("audit user: got %v, want %q", e.kv["user"], wantUser)
	}
	if got, ok := e.kv["status"].(int); !ok || got != wantStatus {
		t.Errorf("audit status: got %v, want %d", e.kv["status"], wantStatus)
	}
	if wantBytes >= 0 {
		if got, ok := e.kv["bytes"].(int64); !ok || got != wantBytes {
			t.Errorf("audit bytes: got %v, want %d", e.kv["bytes"], wantBytes)
		}
	} else if _, ok := e.kv["bytes"].(int64); !ok {
		t.Errorf("audit bytes: got %v, want an int64 to be present", e.kv["bytes"])
	}
	d, ok := e.kv["duration"].(time.Duration)
	if !ok {
		t.Errorf("audit duration: got %T, want time.Duration present", e.kv["duration"])
	} else if d < 0 {
		t.Errorf("audit duration: got negative %v", d)
	}
}

// withCapturingLogger swaps a capturing logger into the handler and returns it.
func withCapturingLogger(p *proxyHandler) *capturingLogger {
	c := &capturingLogger{}
	p.logger = c
	return c
}

// TestAuditDefaultLogger covers Completion Criterion: newProxyHandler wires a
// non-nil logger (log.DefaultLogger) so production requests always have an
// emission target even without injection.
func TestAuditDefaultLogger(t *testing.T) {
	p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
	if p.logger == nil {
		t.Fatal("newProxyHandler: logger is nil, want log.DefaultLogger")
	}
}

// TestAuditSuccessLogsAllFields covers Completion Criteria: a structured entry
// is emitted on the SUCCESS path with all required fields (url, user, status,
// bytes, duration) and the correct values. The validated/normalised target URL
// is logged, and the source Grafana user is taken from the plugin context.
func TestAuditSuccessLogsAllFields(t *testing.T) {
	const body = "hello from upstream"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	capLog := withCapturingLogger(p)

	// Inject a plugin context carrying a Grafana user so the audit logs a login.
	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+url.QueryEscape("http://example.com/page"), nil)
	ctx := backend.WithPluginContext(req.Context(), backend.PluginContext{
		User: &backend.User{Login: "viewer@example.com"},
	})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("success: got status %d, want 200", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	// The logged URL is the validated, normalised target (no default port).
	assertAuditFields(t, e, "http://example.com/page", "viewer@example.com", http.StatusOK, int64(len(body)))
}

// TestAuditAnonymousUserWhenNoContext covers Completion Criterion: when the
// request carries no identifiable Grafana user, the user field falls back to the
// "anonymous" constant rather than being empty or omitted.
func TestAuditAnonymousUserWhenNoContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "http://example.com/page") // no plugin context injected
	if rec.Code != http.StatusOK {
		t.Fatalf("success: got status %d, want 200", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	if got := e.kv["user"]; got != auditAnonymousUser {
		t.Fatalf("audit user without context: got %v, want %q", got, auditAnonymousUser)
	}
}

// TestAuditDenialEmptyAllowlist covers Completion Criterion: an entry is emitted
// on a DENIAL path (empty allowlist => 403) too, with the caller-supplied target
// URL recorded. This is the key "error paths as well as success" guarantee.
func TestAuditDenialEmptyAllowlist(t *testing.T) {
	p := newProxyHandler(settingsWith(nil)) // empty allowlist => fail-closed 403
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "https://example.com/page")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist: got status %d, want 403", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	// 403 body bytes are written by http.Error, so bytes is asserted as present.
	assertAuditFields(t, e, "https://example.com/page", auditAnonymousUser, http.StatusForbidden, -1)
	if b, _ := e.kv["bytes"].(int64); b <= 0 {
		t.Errorf("audit denial bytes: got %d, want > 0 (http.Error wrote a body)", b)
	}
}

// TestAuditDenialMissingURL covers Completion Criterion: the EARLIEST denial
// (missing url param => 400, before any target is known) still emits exactly one
// entry, with the placeholder "<missing>" url so the field is never empty.
func TestAuditDenialMissingURL(t *testing.T) {
	p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
	capLog := withCapturingLogger(p)

	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url: got status %d, want 400", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	assertAuditFields(t, e, auditMissingURL, auditAnonymousUser, http.StatusBadRequest, -1)
}

// TestAuditTooLargeLogsStatus covers Completion Criterion: the 413 (oversized
// declared Content-Length) error path — driven through the ReverseProxy
// ErrorHandler — emits exactly one entry with status 413 and the validated URL,
// confirming the recorder captures the ErrorHandler's write.
func TestAuditTooLargeLogsStatus(t *testing.T) {
	const oversized = "this body is definitely longer than sixteen bytes"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, oversized); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.MaxResponseBytes = 16
	p := newTestHandler(t, cfg, upstream)
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "http://example.com/big")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: got status %d, want 413", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	assertAuditFields(t, e, "http://example.com/big", auditAnonymousUser, http.StatusRequestEntityTooLarge, -1)
}

// TestAuditTimeoutLogsStatus covers Completion Criterion: the 504 (total request
// budget exceeded) error path emits exactly one entry with status 504, confirming
// the timeout/ErrorHandler path is audited like every other.
func TestAuditTimeoutLogsStatus(t *testing.T) {
	cfg := settingsWith(allowExample(DomainOptions{}))
	cfg.RequestTimeoutSec = 1
	p := newProxyHandler(cfg)
	p.transport = blockingRoundTripper{}
	capLog := withCapturingLogger(p)

	rec := doProxy(p, "http://example.com/slow")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: got status %d, want 504", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	assertAuditFields(t, e, "http://example.com/slow", auditAnonymousUser, http.StatusGatewayTimeout, -1)
}

// TestAuditOptionsPreflightLogged confirms the CORS preflight (204) is also
// audited once — every request through ServeHTTP yields exactly one entry, with
// the placeholder url since no target is parsed for OPTIONS.
func TestAuditOptionsPreflightLogged(t *testing.T) {
	p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
	capLog := withCapturingLogger(p)

	req := httptest.NewRequest(http.MethodOptions, "/proxy", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: got status %d, want 204", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	if e.kv["status"] != http.StatusNoContent {
		t.Errorf("preflight status: got %v, want 204", e.kv["status"])
	}
	if e.kv["url"] != auditMissingURL {
		t.Errorf("preflight url: got %v, want %q", e.kv["url"], auditMissingURL)
	}
}

// TestAuditResponseWriterRecordsStatusAndBytes is a focused unit test for the
// recorder: an explicit WriteHeader is captured, Write counts bytes and
// propagates the underlying (n, err), and a no-WriteHeader writer defaults to
// 200. Verifies the recorder underpinning every audit entry.
func TestAuditResponseWriterRecordsStatusAndBytes(t *testing.T) {
	// Explicit status + body.
	inner := httptest.NewRecorder()
	rec := newAuditResponseWriter(inner)
	rec.WriteHeader(http.StatusTeapot)
	n, err := rec.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 5 {
		t.Fatalf("write n: got %d, want 5", n)
	}
	if rec.status != http.StatusTeapot {
		t.Fatalf("recorded status: got %d, want 418", rec.status)
	}
	if rec.bytes != 5 {
		t.Fatalf("recorded bytes: got %d, want 5", rec.bytes)
	}

	// No WriteHeader: an implicit 200 is recorded on first Write.
	inner2 := httptest.NewRecorder()
	rec2 := newAuditResponseWriter(inner2)
	if _, err := io.WriteString(rec2, "abc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if rec2.status != http.StatusOK {
		t.Fatalf("implicit status: got %d, want 200", rec2.status)
	}
	if rec2.bytes != 3 {
		t.Fatalf("implicit bytes: got %d, want 3", rec2.bytes)
	}
	// A later (illegal) WriteHeader must not overwrite the implicit 200.
	rec2.WriteHeader(http.StatusInternalServerError)
	if rec2.status != http.StatusOK {
		t.Fatalf("status after late WriteHeader: got %d, want 200 (unchanged)", rec2.status)
	}

	// Underlying body is intact (passthrough).
	if got := inner.Body.String(); got != "hello" {
		t.Fatalf("passthrough body: got %q, want %q", got, "hello")
	}
}

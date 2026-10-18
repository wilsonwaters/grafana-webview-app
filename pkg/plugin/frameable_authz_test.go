package plugin

// VR-3 authorization tests. /check-frameable requires the Editor or Admin org
// role; /proxy stays open to any signed-in user because dashboard viewers must
// be able to render proxy-mode panels. The decision is recorded in
// ai-state/streams/v1-stabilisation/authz-model.md.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// withOrgRole returns req with a plugin context whose user has the given
// Grafana org role, as the SDK httpadapter sets it for a resource call.
func withOrgRole(req *http.Request, role string) *http.Request {
	ctx := backend.WithPluginContext(req.Context(), backend.PluginContext{
		User: &backend.User{Login: "user-" + strings.ToLower(role), Role: role},
	})
	return req.WithContext(ctx)
}

// withNilUser returns req with a plugin context that carries no user.
func withNilUser(req *http.Request) *http.Request {
	return req.WithContext(backend.WithPluginContext(req.Context(), backend.PluginContext{}))
}

// decodeJSONDenial decodes a JSON denial body.
func decodeJSONDenial(t *testing.T, rec *httptest.ResponseRecorder) jsonDenial {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("denial Content-Type = %q, want application/json (body=%q)", ct, rec.Body.String())
	}
	var got jsonDenial
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON denial: %v (body=%q)", err, rec.Body.String())
	}
	return got
}

// TestCheckFrameableRoleMatrix covers VR-3: the org role gate on
// /check-frameable for every role class. Denied roles get a 403 JSON error
// before any upstream is contacted; Editor and Admin reach the verdict.
func TestCheckFrameableRoleMatrix(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*http.Request) *http.Request
		allowed bool
	}{
		{"no plugin context", func(r *http.Request) *http.Request { return r }, false},
		{"plugin context without user", withNilUser, false},
		{"empty role", func(r *http.Request) *http.Request { return withOrgRole(r, "") }, false},
		{"Viewer", func(r *http.Request) *http.Request { return withOrgRole(r, "Viewer") }, false},
		{"None", func(r *http.Request) *http.Request { return withOrgRole(r, "None") }, false},
		{"unknown role", func(r *http.Request) *http.Request { return withOrgRole(r, "Superuser") }, false},
		{"Editor", func(r *http.Request) *http.Request { return withOrgRole(r, "Editor") }, true},
		{"Admin", func(r *http.Request) *http.Request { return withOrgRole(r, "Admin") }, true},
		{"admin (case-insensitive)", func(r *http.Request) *http.Request { return withOrgRole(r, "admin") }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settingsWith(allowExample(DomainOptions{}))
			var h checkFrameableHandler
			if tc.allowed {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				defer upstream.Close()
				h = frameableHandler(t, cfg, upstream)
			} else {
				p := newProxyHandler(cfg)
				p.transport = tc1FailIfDialled(t) // a denied role must never reach the fetch
				h = checkFrameableHandler{p: p}
			}

			req := httptest.NewRequest(http.MethodGet, "/check-frameable?url="+url.QueryEscape("http://example.com/page"), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.prepare(req))

			if tc.allowed {
				got := decodeFrameable(t, rec)
				if !got.Frameable || got.RecommendedMode != recommendedModeDirect {
					t.Fatalf("%s: verdict %+v, want frameable direct", tc.name, got)
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: got status %d, want 403 (body=%q)", tc.name, rec.Code, rec.Body.String())
			}
			got := decodeJSONDenial(t, rec)
			if got.Reason != denialReasonRole || got.Message == "" || got.Error == "" {
				t.Fatalf("%s: JSON denial = %+v, want reason %q with error and message", tc.name, got, denialReasonRole)
			}
		})
	}
}

// TestCheckFrameableRoleGateRunsFirst covers VR-3: the role gate runs before
// the allowlist, so a Viewer gets the role denial for any host and cannot
// learn which hosts are allowlisted.
func TestCheckFrameableRoleGateRunsFirst(t *testing.T) {
	p := newProxyHandler(settingsWith(allowExample(DomainOptions{})))
	p.transport = tc1FailIfDialled(t)
	h := checkFrameableHandler{p: p}
	for _, target := range []string{"https://example.com/page", "https://not-allowlisted.test/", "ftp://example.com/x"} {
		req := httptest.NewRequest(http.MethodGet, "/check-frameable?url="+url.QueryEscape(target), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, withOrgRole(req, "Viewer"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as Viewer: got %d, want 403", target, rec.Code)
		}
		if got := decodeJSONDenial(t, rec); got.Reason != denialReasonRole {
			t.Fatalf("%s as Viewer: reason %q, want %q", target, got.Reason, denialReasonRole)
		}
	}
}

// TestCheckFrameableRoleDenialAuditedAndCounted covers VR-3: the role denial
// emits exactly one audit entry and is counted as denials_total{reason="role"},
// like every other denial.
func TestCheckFrameableRoleDenialAuditedAndCounted(t *testing.T) {
	p, _ := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), nil)
	p.transport = tc1FailIfDialled(t)
	capLog := withCapturingLogger(p)

	const target = "https://example.com/page"
	req := httptest.NewRequest(http.MethodGet, "/check-frameable?url="+url.QueryEscape(target), nil)
	rec := httptest.NewRecorder()
	checkFrameableHandler{p: p}.ServeHTTP(rec, withOrgRole(req, "Viewer"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Viewer: got %d, want 403", rec.Code)
	}

	e := requireSingleAuditEntry(t, capLog)
	assertAuditFields(t, e, target, "user-viewer", http.StatusForbidden, -1)
	if got := e.kv["endpoint"]; got != endpointCheckFrameable {
		t.Errorf("audit endpoint = %v, want %q", got, endpointCheckFrameable)
	}
	if got := denialsTotalFor(t, p, denialReasonRole); got != 1 {
		t.Errorf("denials_total{reason=role} = %v, want 1", got)
	}
	if got := requestsTotalFor(t, p, http.StatusForbidden); got != 1 {
		t.Errorf("requests_total{status=403} = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(p.metrics.denials); got != 1 {
		t.Errorf("denial reason series = %d, want 1", got)
	}
	if got := testutil.ToFloat64(p.metrics.inFlight); got != 0 {
		t.Errorf("in_flight after completion = %v, want 0", got)
	}
}

// TestCheckFrameableSuccessAudited confirms an allowed check emits one audit
// entry with the validated target and records no denial.
func TestCheckFrameableSuccessAudited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	p, _ := newMetricsTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	capLog := withCapturingLogger(p)

	rec := doCheckFrameable(checkFrameableHandler{p: p}, "http://example.com/page")
	if rec.Code != http.StatusOK {
		t.Fatalf("Editor: got %d, want 200", rec.Code)
	}
	e := requireSingleAuditEntry(t, capLog)
	assertAuditFields(t, e, "http://example.com/page", "user-editor", http.StatusOK, -1)
	if got := testutil.CollectAndCount(p.metrics.denials); got != 0 {
		t.Errorf("successful check recorded %d denial series, want 0", got)
	}
}

// TestProxyOpenToAnyOrgRole covers the VR-3 decision for /proxy: the proxy has
// no role gate, so a Viewer (and a request without a plugin context user)
// still gets the proxied page.
func TestProxyOpenToAnyOrgRole(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	for _, role := range []string{"Viewer", "None", "Editor", "Admin"} {
		req := httptest.NewRequest(http.MethodGet, "/proxy?url="+url.QueryEscape("http://example.com/page"), nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, withOrgRole(req, role))
		if rec.Code != http.StatusOK {
			t.Fatalf("/proxy as %s: got %d, want 200 (body=%q)", role, rec.Code, rec.Body.String())
		}
	}
}

// TestCheckFrameableRoleThroughCallResource drives the role gate through the
// real App.CallResource path, so the user comes from the plugin context the SDK
// httpadapter sets. With the default empty allowlist an Admin passes the role
// gate and is then denied by the allowlist (plain-text 403); a Viewer gets the
// JSON role denial.
func TestCheckFrameableRoleThroughCallResource(t *testing.T) {
	app := newTestApp(t)
	call := func(role string) *backend.CallResourceResponse {
		t.Helper()
		var r mockCallResourceResponseSender
		err := app.CallResource(context.Background(), &backend.CallResourceRequest{
			PluginContext: backend.PluginContext{User: &backend.User{Login: "u", Role: role}},
			Method:        http.MethodGet,
			Path:          "check-frameable",
			URL:           "check-frameable?url=" + url.QueryEscape("https://example.com/"),
		}, &r)
		if err != nil {
			t.Fatalf("CallResource: %v", err)
		}
		if r.response == nil {
			t.Fatal("no response from CallResource")
		}
		return r.response
	}

	viewer := call("Viewer")
	if viewer.Status != http.StatusForbidden || !strings.Contains(string(viewer.Body), `"reason":"role"`) {
		t.Fatalf("Viewer: status %d body %s, want 403 JSON role denial", viewer.Status, viewer.Body)
	}
	admin := call("Admin")
	if admin.Status != http.StatusForbidden || strings.Contains(string(admin.Body), `"reason":"role"`) {
		t.Fatalf("Admin: status %d body %s, want the allowlist 403 (past the role gate)", admin.Status, admin.Body)
	}
}

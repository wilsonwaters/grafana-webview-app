package plugin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// newTestApp builds an App with empty/zero settings. The /health endpoint must
// answer even with no configuration, so this exercises that path.
func newTestApp(t *testing.T) *App {
	t.Helper()
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{})
	if err != nil {
		t.Fatalf("new app: %s", err)
	}
	app, ok := inst.(*App)
	if !ok {
		t.Fatal("inst must be of type *App")
	}
	return app
}

// TestHealthGETReturns200 maps to Completion Criterion:
// "/health returns 200 when the backend is running" and
// "Unit test confirms the handler responds correctly".
func TestHealthGETReturns200(t *testing.T) {
	app := newTestApp(t)

	var r mockCallResourceResponseSender
	if err := app.CallResource(context.Background(), &backend.CallResourceRequest{
		Method: http.MethodGet,
		Path:   "health",
	}, &r); err != nil {
		t.Fatalf("CallResource error: %s", err)
	}
	if r.response == nil {
		t.Fatal("no response received from CallResource")
	}
	if r.response.Status != http.StatusOK {
		t.Errorf("status should be %d, got %d", http.StatusOK, r.response.Status)
	}
	if body := string(r.response.Body); !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body should contain %q, got %s", `"status":"ok"`, body)
	}
	if ct := r.response.Headers["Content-Type"]; len(ct) == 0 || ct[0] != "application/json" {
		t.Errorf("Content-Type should be application/json, got %v", ct)
	}
}

// TestHealthRegisteredInRouter maps to Completion Criterion:
// "Endpoint is registered in the backend router". A registered route returns
// 200; an unregistered one would 404 through the httpadapter mux.
func TestHealthRegisteredInRouter(t *testing.T) {
	app := newTestApp(t)

	var r mockCallResourceResponseSender
	if err := app.CallResource(context.Background(), &backend.CallResourceRequest{
		Method: http.MethodGet,
		Path:   "health",
	}, &r); err != nil {
		t.Fatalf("CallResource error: %s", err)
	}
	if r.response == nil || r.response.Status == http.StatusNotFound {
		t.Fatalf("expected /health to be registered, got %+v", r.response)
	}
}

// TestHealthNoSideEffectsNoConfig maps to the Test Strategy
// "Handler returns 200; no side effects": the handler must answer with empty
// config and must not touch the proxy pipeline.
func TestHealthNoSideEffectsNoConfig(t *testing.T) {
	app := newTestApp(t)

	// Two successive calls must behave identically (no mutated state).
	for i := 0; i < 2; i++ {
		var r mockCallResourceResponseSender
		if err := app.CallResource(context.Background(), &backend.CallResourceRequest{
			Method: http.MethodGet,
			Path:   "health",
		}, &r); err != nil {
			t.Fatalf("CallResource error: %s", err)
		}
		if r.response == nil || r.response.Status != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %+v", i, r.response)
		}
		if body := string(r.response.Body); !strings.Contains(body, `"status":"ok"`) {
			t.Errorf("call %d: body should contain status ok, got %s", i, body)
		}
	}
}

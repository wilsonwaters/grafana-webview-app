package plugin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// mockCallResourceResponseSender implements backend.CallResourceResponseSender
// for use in tests.
type mockCallResourceResponseSender struct {
	response *backend.CallResourceResponse
}

// Send sets the received *backend.CallResourceResponse to s.response
func (s *mockCallResourceResponseSender) Send(response *backend.CallResourceResponse) error {
	s.response = response
	return nil
}

// TestCallResource tests CallResource calls, using backend.CallResourceRequest and backend.CallResourceResponse.
// This ensures the httpadapter for CallResource works correctly. It also locks
// VR-8: the create-plugin scaffold routes /ping and /echo are not registered
// and return 404 for every method.
func TestCallResource(t *testing.T) {
	// Initialize app
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{})
	if err != nil {
		t.Fatalf("new app: %s", err)
	}
	if inst == nil {
		t.Fatal("inst must not be nil")
	}
	app, ok := inst.(*App)
	if !ok {
		t.Fatal("inst must be of type *App")
	}

	// Set up and run test cases
	for _, tc := range []struct {
		name string

		method string
		path   string
		body   []byte

		expStatus int
		expBody   []byte
	}{
		{
			name:      "get health 200",
			method:    http.MethodGet,
			path:      "health",
			expStatus: http.StatusOK,
			expBody:   []byte(healthBody),
		},
		{
			name:      "get ping 404 (scaffold route removed)",
			method:    http.MethodGet,
			path:      "ping",
			expStatus: http.StatusNotFound,
		},
		{
			name:      "get echo 404 (scaffold route removed)",
			method:    http.MethodGet,
			path:      "echo",
			expStatus: http.StatusNotFound,
		},
		{
			name:      "post echo 404 (scaffold route removed)",
			method:    http.MethodPost,
			path:      "echo",
			body:      []byte(`{"message":"ok"}`),
			expStatus: http.StatusNotFound,
		},
		{
			name:      "get non existing handler 404",
			method:    http.MethodGet,
			path:      "not_found",
			expStatus: http.StatusNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Request by calling CallResource. This tests the httpadapter.
			var r mockCallResourceResponseSender
			err = app.CallResource(context.Background(), &backend.CallResourceRequest{
				Method: tc.method,
				Path:   tc.path,
				Body:   tc.body,
			}, &r)
			if err != nil {
				t.Fatalf("CallResource error: %s", err)
			}
			if r.response == nil {
				t.Fatal("no response received from CallResource")
			}
			if tc.expStatus > 0 && tc.expStatus != r.response.Status {
				t.Errorf("response status should be %d, got %d", tc.expStatus, r.response.Status)
			}
			if len(tc.expBody) > 0 {
				if tb := bytes.TrimSpace(r.response.Body); !bytes.Equal(tb, tc.expBody) {
					t.Errorf("response body should be %s, got %s", tc.expBody, tb)
				}
			}
			if tc.expStatus == http.StatusNotFound && bytes.Contains(r.response.Body, []byte(`"message"`)) {
				t.Errorf("removed route reflected a body: %s", r.response.Body)
			}
		})
	}
}

// orderRecordingWriter records the order of WriteHeader and Write calls and
// can fail every Write, to check the /health write order (VR-8).
type orderRecordingWriter struct {
	header   http.Header
	calls    []string
	writeErr error
}

func (w *orderRecordingWriter) Header() http.Header { return w.header }

func (w *orderRecordingWriter) WriteHeader(status int) {
	w.calls = append(w.calls, "WriteHeader:"+http.StatusText(status))
}

func (w *orderRecordingWriter) Write(b []byte) (int, error) {
	w.calls = append(w.calls, "Write")
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(b), nil
}

// TestHealthWriteOrder covers VR-8: /health sends the status exactly once,
// before the body, and does not call WriteHeader again when the body write
// fails.
func TestHealthWriteOrder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		writeErr error
	}{
		{"write succeeds", nil},
		{"write fails", errors.New("broken pipe")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &orderRecordingWriter{header: http.Header{}, writeErr: tc.writeErr}
			(&App{}).handleHealth(w, httptest.NewRequest(http.MethodGet, "/health", nil))
			want := []string{"WriteHeader:OK", "Write"}
			if len(w.calls) != len(want) || w.calls[0] != want[0] || w.calls[1] != want[1] {
				t.Fatalf("call order = %v, want %v", w.calls, want)
			}
			if ct := w.header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

package plugin

import (
	"net/http"
)

// healthBody is the fixed /health response body.
const healthBody = `{"status":"ok"}`

// handleHealth is a bare liveness endpoint (FR2). It always returns HTTP 200
// with a small JSON body and runs no security pipeline, no proxying and depends
// on no settings — it only confirms the backend resource server is reachable.
// It never reads the request body.
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// The status is already sent, so a failed body write cannot be reported to
	// the client (VR-8: no WriteHeader after Write).
	_, _ = w.Write([]byte(healthBody))
}

// registerRoutes registers the plugin's resource routes on mux. Any other path
// returns 404. The create-plugin scaffold routes /ping and /echo were removed
// (VR-8): they bypassed the security pipeline and /echo decoded unbounded
// request bodies.
func (a *App) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", a.handleHealth)
	if a.proxy != nil {
		mux.Handle(proxyPath, a.proxy)
		// CR3: the subresource endpoint shares a.proxy's pipeline/transport/
		// rate-limiter/audit/metrics via the proxyResourceHandler wrapper.
		mux.Handle(proxyResourcePath, a.proxyResource)
		// FR1: the frameability-check endpoint runs the same security pipeline.
		mux.Handle(checkFrameablePath, a.checkFrameable)
	}
}

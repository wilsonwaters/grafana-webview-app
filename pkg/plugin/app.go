package plugin

import (
	"context"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

// Make sure App implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. Plugin should not implement all these interfaces - only those which are
// required for a particular task.
var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

// App is the Web View app plugin backend instance. Each Grafana instance that
// has the plugin installed gets its own App (managed by the SDK's instance
// manager). The Config field holds the parsed, defaults-applied plugin
// settings loaded from Grafana's non-secret jsonData at startup.
type App struct {
	backend.CallResourceHandler

	// Config holds the admin-configured plugin settings for this instance.
	// It is populated once in NewApp and is safe for concurrent read access
	// thereafter (the SDK creates a new App when settings change).
	Config PluginSettings

	// proxy is the /proxy endpoint handler, built once in NewApp from Config.
	// It owns the per-instance/per-domain rate limiter, the in-flight
	// concurrency cap, and the secure-dialer-backed HTTP transport, and is safe
	// for concurrent use.
	proxy *proxyHandler

	// proxyResource is the /proxy-resource subresource handler (CR3). It wraps
	// the SAME proxyHandler — sharing its pipeline, transport, rate limiter,
	// audit logger and metrics — and differs only in serving subresources without
	// HTML rewriting (Content-Type preserved, body streamed through unchanged).
	proxyResource proxyResourceHandler

	// checkFrameable is the /check-frameable endpoint handler (FR1). It wraps the
	// SAME proxyHandler — sharing its settings, allowlist, rate limiter and SF4
	// secure-dialer transport — and runs the identical pre-fetch security pipeline
	// before issuing a single non-following GET to inspect framing headers.
	checkFrameable checkFrameableHandler
}

// NewApp creates a new *App instance. It parses the plugin settings from
// AppInstanceSettings.JSONData and applies safe defaults before storing the
// resulting Config. If the settings JSON is malformed, NewApp returns an error
// and the plugin instance is not started.
func NewApp(_ context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	cfg, err := LoadSettings(settings)
	if err != nil {
		return nil, err
	}

	var app App
	app.Config = cfg

	// Build the proxy handler once from settings. It constructs the rate
	// limiter (including per-domain overrides) and the secure-dialer-backed
	// transport; keyed rate-limit buckets are created lazily on first use.
	app.proxy = newProxyHandler(cfg)
	// The subresource endpoint shares the same proxyHandler (and thus the same
	// pipeline, transport, rate limiter, audit logger and metrics).
	app.proxyResource = proxyResourceHandler{p: app.proxy}
	// FR1: the /check-frameable endpoint shares the same proxyHandler (pipeline,
	// transport, rate limiter) and runs the identical validation before fetching.
	app.checkFrameable = checkFrameableHandler{p: app.proxy}

	// Use a httpadapter (provided by the SDK) for resource calls. This allows us
	// to use a *http.ServeMux for resource calls, so we can map multiple routes
	// to CallResource without having to implement extra logic.
	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)

	return &app, nil
}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created.
func (a *App) Dispose() {
	// cleanup
}

// CheckHealth handles health checks sent from Grafana to the plugin.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}

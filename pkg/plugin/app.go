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

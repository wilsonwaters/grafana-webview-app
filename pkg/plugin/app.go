package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

// healthDetails is the JSONDetails of the CheckHealth result.
type healthDetails struct {
	AllowlistEntries int      `json:"allowlistEntries"`
	InvalidEntries   []string `json:"invalidEntries,omitempty"`
}

// CheckHealth reports the state of the domain allowlist. Invalid entries (not
// a bare host name) are an error, because they are ignored and the hosts the
// admin meant to allow get 403s. An empty allowlist is a valid, fail-closed
// configuration, so it is OK with a message saying the proxy denies every
// request.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	domains := a.Config.AllowedDomains
	invalid := invalidAllowlistDomains(domains)
	details, err := json.Marshal(healthDetails{AllowlistEntries: len(domains), InvalidEntries: invalid})
	if err != nil {
		return nil, err
	}

	result := &backend.CheckHealthResult{Status: backend.HealthStatusOk, JSONDetails: details}
	switch {
	case len(invalid) > 0:
		result.Status = backend.HealthStatusError
		result.Message = fmt.Sprintf("%d of %d allowlist entries are not a valid host name and are ignored: %s. Use a bare host name such as example.com, without a scheme, port or path.",
			len(invalid), len(domains), strings.Join(quoteAll(invalid), ", "))
		if len(invalid) == len(domains) {
			result.Message += " No valid entries remain, so the proxy denies every request."
		}
	case len(domains) == 0:
		result.Message = "The domain allowlist is empty, so the proxy denies every request. An admin adds domains on the plugin's Configuration page."
	default:
		result.Message = fmt.Sprintf("ok (allowlist entries: %d)", len(domains))
	}
	return result, nil
}

// quoteAll returns each string in Go-quoted form, so an empty or odd entry is
// visible in a message.
func quoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.Quote(v)
	}
	return out
}

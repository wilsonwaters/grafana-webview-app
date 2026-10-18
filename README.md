# Webview — Grafana App Plugin

> **Status: approaching the first stable release (v1.0.0).** Interfaces and panel
> options are stabilising but may still change ahead of the tagged release.

[![CI](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml/badge.svg)](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/wilsonwaters/grafana-webview-app)](LICENSE)
[![Grafana](https://img.shields.io/badge/Grafana-%3E%3D12.3.0-orange)](https://grafana.com)

A Grafana **App plugin** (`wilsonwaters-webview-app`) that embeds an external web
page inside a Grafana dashboard panel and lets the dashboard author control the
viewport — panning and zooming to focus on a specific region of the page (for
example, a single city on a weather radar).

![A Web View panel rendering an external page directly inside a Grafana dashboard](docs/screenshots/configuration/panel-direct-render.png)

The plugin ships a nested **Web View panel** (`wilsonwaters-webview-panel`) and a
Go backend. It supports two loading strategies:

- a **direct iframe mode** for sites that permit framing, and
- a **security-hardened backend proxy mode** that fetches and re-serves a page
  server-side for sites that block direct embedding via `X-Frame-Options` or a
  `Content-Security-Policy` `frame-ancestors` directive.

## The problem it solves

Many useful web pages — radar maps, status pages, internal tools — cannot simply
be dropped into a Grafana dashboard with an iframe: they either set framing-control
headers that browsers enforce, or they render at a size that is unusable inside a
small panel. This plugin addresses both problems. The backend proxy makes
framing-blocked pages embeddable server-side, and the viewport controls let an
author capture and save exactly the region of a page that a viewer should see.

## Quick start

1. **Install** the plugin into a Grafana `>= 12.3.0` instance (backend-plugin
   support is required for proxy mode; direct mode works without it). See the
   **[Installation guide](docs/installation.md)** for signing and install steps.
2. **Add a panel:** create or edit a dashboard, add a panel, and pick
   **Web View** from the visualization picker.
3. **Configure it:** enter a URL, click **Test URL** to check frameability,
   choose a load mode (**Auto** / **Direct** / **Proxy**), and drag/zoom the
   preview to position the viewport. See the
   **[Configuration reference](docs/configuration.md)**.
4. **For proxy mode, allowlist the domain first.** The proxy allowlist is **empty
   by default (deny-all)**, so an administrator must add the target domain before
   proxy requests succeed. See the
   **[Administration guide](docs/administration.md#2-allowlist-management)**.

Prefer to start from a working example? Import
[`docs/examples/webview-example-dashboard.json`](docs/examples/webview-example-dashboard.json)
— see the [examples README](docs/examples/README.md) for the two demo panels and
the allowlist prerequisite.

## Modes of operation

The plugin has a hard split between two modes, matching the two audiences:

- **Configuration mode** (panel editor, Admin/editor) — the author enters a URL,
  runs a "Test URL" frameability check, chooses a load mode (Auto / Direct /
  Proxy), and interactively positions the viewport (drag to pan, wheel to zoom)
  with a live coordinate readout before saving.
- **View mode** (dashboard viewer) — the panel statically renders the configured
  URL at the saved viewport. There is no re-detection and no interactive
  pan/zoom; the iframe is non-interactive (`pointer-events: none`) and the viewer
  sees exactly the region the author captured.

## Features

- **Nested Web View panel** registered alongside the app plugin; multiple
  independent panel instances can run on the same dashboard.
- **Direct iframe loading** — embeds the target URL in a sandboxed `<iframe>`
  (`sandbox="allow-scripts allow-same-origin"`, `referrerPolicy="no-referrer"`)
  with no server-side involvement; zero added latency for sites that permit
  framing.
- **Backend proxy pipeline** — a Go backend fetches a remote page server-side,
  strips framing-control and other sensitive headers, and rewrites the HTML so
  subresources resolve, allowing content from sites that set
  `X-Frame-Options: DENY/SAMEORIGIN` (or a CSP `frame-ancestors`) to be served
  same-origin to Grafana. (See [Known limitations](#known-limitations) for the
  v1 in-panel rendering caveat.)
- **Frameability detection ("Test URL")** — the editor calls the backend to
  determine whether a URL can be framed directly or needs the proxy, and stores
  the result so view mode never has to re-detect.
- **Viewport capture** — pan/zoom the preview and save a viewport (X/Y offset and
  zoom factor, applied at view time via CSS `transform: scale() translate()`),
  with numeric inputs and configurable virtual iframe dimensions.
- **Auto-refresh** — optional per-panel refresh interval; each panel instance
  keeps its own independent refresh cycle.
- **Hide selectors** — a list of CSS selectors for elements to hide; applied
  server-side during proxy HTML rewriting.
- **Debug overlay** — an optional author-facing overlay showing the resolved
  load mode and viewport coordinates.
- **Graceful degradation** — when no backend is available (e.g. a Grafana
  deployment without backend-plugin support), the panel remains usable in
  **direct-iframe-only** mode; the proxy option and "Test URL" are cleanly
  disabled in the editor. Backend availability is detected via the `/health`
  resource.

## Security model

The backend proxy is the security-critical component and is designed to be
conservative by default. Every proxying endpoint runs the same pre-fetch
pipeline, and **the order is load-bearing**:

1. **Hostname normalization** — the target host is canonicalised so the
   allowlist and later checks compare an identical form.
2. **Domain allowlist match** — the normalised host is checked against the
   allowlist, which is **empty by default, so the default is deny**. (Matching
   the allowlist first lets a per-domain non-standard port be honoured by the
   next step.)
3. **URL scheme / port validation** (`ValidateURL`) — only `http`/`https`, no
   user-info, and only the matched domain's permitted ports.
4. **Rate-limit and concurrency caps** — per-instance and per-domain rate tiers,
   then a global concurrency slot.
5. **Resolve-then-dial IP validation** — at connect time the dialler resolves
   DNS and validates the resolved IP against a hardcoded, non-configurable
   private/reserved **IP blocklist** with a DNS-rebinding-safe `Control` hook, so
   it cannot be skipped.

The request then strips outgoing `Cookie` / `Authorization` / `X-Grafana-*`
headers, fetches with a timeout and maximum-body limit, strips framing, cookie
and HSTS response headers, rewrites the HTML, and emits a single audit-log line
and Prometheus metric. The proxy is stateless and unauthenticated: it forwards no
credentials, sessions, cookies, WebSockets or SSE, and never executes proxied
JavaScript server-side.

For the full policy and threat model see **[SECURITY.md](SECURITY.md)**, and for
the architecture and decision log see
**[docs/architecture.md](docs/architecture.md)**.

## Compatibility

| Requirement | Version |
|-------------|---------|
| Grafana | >= 12.3.0 |
| Grafana Plugin SDK for Go | see `go.mod` |

## Installation

See the **[Installation guide](docs/installation.md)** for signing and install
instructions. Because the plugin includes a backend, it must be installed in a
Grafana deployment that supports backend plugins to use proxy mode; direct mode
works without a backend.

> **Admins:** the proxy domain allowlist is empty by default, which denies all
> proxied requests until you configure it. See the
> [Administration guide](docs/administration.md) and
> [Configuration reference](docs/configuration.md).

## Documentation

- [Installation guide](docs/installation.md) — signing, install, and deployment.
- [Configuration reference](docs/configuration.md) — every panel option and the
  editor workflow.
- [Administration guide](docs/administration.md) — the proxy allowlist, limits,
  and operating the backend.
- [Architecture & decision log](docs/architecture.md) — the request pipeline and
  the SF1–SF5 security controls.
- [Developer guide](docs/developer-guide.md) — local workflow and project layout.
- [Troubleshooting](docs/troubleshooting.md) — common failure modes and fixes.
- [Signing guide](docs/signing.md) — how the plugin is signed.
- [Example dashboard](docs/examples/README.md) — an importable dashboard with a
  direct-mode and a proxy-mode panel.
- [Security policy](SECURITY.md) — threat model and vulnerability reporting.
- [Contributing](CONTRIBUTING.md) — how to propose changes.

## Development

### Prerequisites

- Node.js (see `.nvmrc`; `package.json` requires Node >= 22)
- Go (version per `go.mod`)
- [Mage](https://magefile.org/) (`go install github.com/magefile/mage@latest`)
- Docker and Docker Compose

### Frontend

```bash
# Install dependencies
npm install

# Start frontend in watch/dev mode
npm run dev

# Production build
npm run build

# Type-check
npm run typecheck

# Run unit tests (CI mode — exits after completion)
npm run test:ci

# Lint
npm run lint

# Run end-to-end tests (requires a running Grafana instance)
npm run e2e
```

### Backend

```bash
# Build the Go backend for Linux
mage -v build:linux
```

### Run a local Grafana instance

```bash
# Build the plugin and start Grafana with it pre-loaded at http://localhost:3000
npm run server
# (equivalent to `docker compose up --build`)
```

Default credentials: `admin` / `admin`.

See the **[Developer guide](docs/developer-guide.md)** for the full local
workflow and the project layout.

## Known limitations

- **In-panel Proxy-mode rendering is deferred for v1.** Grafana's core HTTP layer
  stamps `X-Frame-Options: deny` and a sandbox `Content-Security-Policy` on all
  `/api/plugins/*/resources/*` routes, which blocks framing the proxy resource
  directly inside a panel (even same-origin). As a result, **Direct mode renders
  in-panel, but Proxy mode does not yet render in the panel** in this release.
  The backend proxy pipeline itself is complete and verified (fetch, header
  stripping, SSRF hardening and HTML rewriting all work); only the in-panel
  display of proxied content is pending. A follow-up (fetch-then-`srcdoc`) is
  planned for a future release. See issue
  [#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102).
- **Proxy mode is deny-all until a domain is allowlisted.** The proxy domain
  allowlist is **empty by default**, so on a fresh install the backend proxies
  nothing. An administrator must allowlist each target domain before proxy
  requests (or a "Test URL" check against a framing-blocked site) succeed. See
  the [Administration guide](docs/administration.md#2-allowlist-management).
- **Frameability is detected only at configuration time.** If a target site later
  changes its framing headers, re-run "Test URL" and re-save the panel (or use a
  manual load-mode override) to pick up the new mode.
- **`hideSelectors` applies to proxied pages only.** Hiding elements requires
  rewriting the page HTML, which only happens in proxy mode; it cannot be applied
  to a cross-origin direct iframe.
- **Proxy mode does not transfer authenticated sessions.** The proxy is
  stateless and unauthenticated, so pages that require a logged-in session,
  cookies, service workers, WebSockets/SSE, or that make cross-origin AJAX/fetch
  calls may not render fully. Complex single-page apps may also render
  incompletely, and some sites actively detect and block proxied access.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before opening a pull request.

## Security

To report a vulnerability, please use
[GitHub Security Advisories](https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new).
Do **not** open a public issue for security reports. See [SECURITY.md](SECURITY.md)
for the full reporting policy.

## License

Licensed under the [Apache License 2.0](LICENSE).

# Webview — Grafana App Plugin

> **Status: approaching the first stable release (v1.0.0).** Interfaces and panel
> options are stabilising but may still change ahead of the tagged release.

[![CI](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml/badge.svg)](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/wilsonwaters/grafana-webview-app)](LICENSE)
[![Grafana](https://img.shields.io/badge/Grafana-%3E%3D12.4.0-orange)](https://grafana.com)

A Grafana **App plugin** (`wilsonwaters-webview-app`) that embeds an external web
page inside a Grafana dashboard panel and lets the dashboard author control the
viewport — panning and zooming to focus on a specific region of the page (for
example, a single city on a weather radar).

![A Web View panel rendering an external page directly inside a Grafana dashboard](docs/screenshots/configuration/panel-direct-render.png)

The plugin ships a nested **Web View panel** (`wilsonwaters-webview-panel`) and a
Go backend. It supports two loading strategies:

- a **direct iframe mode** for sites that permit framing, and
- a **security-hardened backend proxy mode** for sites that block direct
  embedding via `X-Frame-Options` or a `Content-Security-Policy`
  `frame-ancestors` directive. The backend fetches the page, and the panel shows
  it in an isolated, sandboxed frame. Proxy mode works on a standard Grafana
  install: you do not need to change `allow_embedding` or any other Grafana
  setting.

## The problem it solves

Many useful web pages — radar maps, status pages, internal tools — cannot simply
be dropped into a Grafana dashboard with an iframe: they either set framing-control
headers that browsers enforce, or they render at a size that is unusable inside a
small panel. This plugin addresses both problems. The backend proxy makes
framing-blocked pages embeddable server-side, and the viewport controls let an
author capture and save exactly the region of a page that a viewer should see.

## Quick start

1. **Install** the plugin into a Grafana `>= 12.4.0` instance (backend-plugin
   support is required for proxy mode; direct mode works without it). See the
   **[Installation guide](docs/installation.md)** for signing and install steps.
2. **Allowlist the domains you need.** The proxy allowlist is **empty by default
   (deny-all)**. An administrator adds domains on the app's **Configuration**
   page (**Administration → Plugins and data → Plugins → Webview →
   Configuration**). Changes apply to the next request; no restart is needed.
   The proxy and the **Test URL** check work only for allowlisted domains. See
   the **[Administration guide](docs/administration.md#2-allowlist-management)**.
3. **Add a panel:** create or edit a dashboard, add a panel, and pick
   **Web View** from the visualization picker.
4. **Configure it:** enter a URL, click **Test URL** to check frameability,
   choose a load mode (**Auto** / **Direct** / **Proxy**), and drag/zoom the
   preview to position the viewport. See the
   **[Configuration reference](docs/configuration.md)**.

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
- **URL validation** — the panel loads only absolute `http://` and `https://`
  URLs. Any other value (for example a `javascript:` or `data:` URL) shows an
  error instead of a frame.
- **Direct iframe loading** — embeds the target URL in a sandboxed `<iframe>`
  (`referrerPolicy="no-referrer"`) with no server-side involvement; zero added
  latency for sites that permit framing. A cross-origin page gets
  `sandbox="allow-scripts allow-same-origin"`, so it keeps its own origin and
  cannot reach Grafana. A URL on the Grafana origin gets `allow-scripts` only.
- **Backend proxy pipeline** — a Go backend fetches the HTML page, strips
  framing-control and other sensitive headers, and rewrites the HTML so that
  every link and subresource URL points at the original site. The panel reads
  the result and shows it in a `srcdoc` iframe with `sandbox="allow-scripts"`.
  The page's scripts run in an isolated (opaque) origin with no access to
  Grafana. Images, scripts and stylesheets load directly from the original site
  in the viewer's browser. See [Known limitations](#known-limitations).
- **Configuration page** — administrators manage the domain allowlist and the
  six proxy limits in the app's Configuration page. Changes apply without a
  restart.
- **Frameability detection ("Test URL")** — the editor calls the backend to
  determine whether a URL can be framed directly or needs the proxy, and stores
  the result so view mode never has to re-detect. Requires the Editor or Admin
  role and an allowlisted domain.
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
conservative by default. The two backend endpoints that contact other sites,
`/proxy` and `/check-frameable`, run the same pre-fetch pipeline, and **the
order is load-bearing**:

1. **Hostname normalization** — the target host is canonicalised so the
   allowlist and later checks compare an identical form.
2. **Domain allowlist match** — the normalised host is checked against the
   allowlist, which is **empty by default, so the default is deny**. (Matching
   the allowlist first lets a per-domain non-standard port be honoured by the
   next step.)
3. **URL scheme / port validation** (`ValidateURL`) — only `http`/`https`, no
   user-info, and only the matched domain's permitted ports.
4. **Rate-limit and concurrency caps** — per-organisation and per-domain rate
   tiers, then a global concurrency slot.
5. **Resolve-then-dial IP validation** — at connect time the dialler resolves
   DNS and validates the resolved IP against a hardcoded, non-configurable
   private/reserved **IP blocklist** with a DNS-rebinding-safe `Control` hook, so
   it cannot be skipped.

The request then strips outgoing `Cookie` / `Authorization` / `X-Grafana-*`
headers, fetches with a timeout and maximum-body limit, strips framing, cookie,
HSTS, `Refresh` and `Access-Control-*` response headers, rewrites the HTML, and
emits a single audit-log line and Prometheus metric. The whole response is read
before the proxy replies, so a response over the size limit is always a `413`,
never a truncated page. The proxy is stateless: it forwards no credentials,
sessions, cookies, WebSockets or SSE to the upstream site, and never executes
proxied JavaScript server-side.

**Who can use it.** Grafana requires a signed-in user for every backend call.
`/proxy` is available to every user of the organisation, including Viewers (and
anonymous viewers, if Grafana's anonymous access is on), because dashboard
viewers must be able to load proxy-mode panels. `/check-frameable` (the
**Test URL** button) requires the Editor or Admin role. The domain allowlist and
the rate limits are the access control for the proxy, so allowlist only sites
that every user of the organisation may see.

For the full policy and threat model see **[SECURITY.md](SECURITY.md)**, and for
the architecture and decision log see
**[docs/architecture.md](docs/architecture.md)**.

## Compatibility

| Requirement | Version |
|-------------|---------|
| Grafana | >= 12.4.0 (tested on 12.4, 13.0, 13.1 and 13.2) |
| Browsers | Tested with Playwright Chromium, Firefox and WebKit builds (real Safari not yet tested) |
| Grafana Plugin SDK for Go | see `go.mod` |

## Installation

See the **[Installation guide](docs/installation.md)** for signing and install
instructions. Because the plugin includes a backend, it must be installed in a
Grafana deployment that supports backend plugins to use proxy mode; direct mode
works without a backend.

> **Admins:** the proxy domain allowlist is empty by default, which denies all
> proxied requests until you add domains on the app's Configuration page. See the
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

- Node.js 22 or later (see `.nvmrc`)
- Go 1.27.2 (the `go` directive in `go.mod`)
- [Mage](https://magefile.org/) (`go install github.com/magefile/mage@latest`)
- Docker and Docker Compose

On Windows, run the Node toolchain in a Linux container or WSL. See the
[Developer guide](docs/developer-guide.md#windows-workstations).

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

> **Local development only.** The bundled Docker Compose setup signs every
> visitor in as an anonymous **Admin**, runs Grafana in development mode and
> loads the unsigned plugin. Never expose it to an untrusted network, and never
> copy its settings to a production Grafana. Production installs use the signed
> release archive. See the [Installation guide](docs/installation.md).

See the **[Developer guide](docs/developer-guide.md)** for the full local
workflow and the project layout.

## Known limitations

Proxy mode sends only the HTML page through Grafana. Keep these limits in mind:

- **Grafana's own Content Security Policy blocks proxied scripts.** If Grafana
  runs with `[security] content_security_policy = true`, a proxied page shows
  its markup and images only. Its scripts and external stylesheets do not load.
- **The viewer's browser must reach the original site.** Images, scripts and
  stylesheets load directly from that site, which sees the viewer's IP address
  (but no `Referer`). If only the Grafana server can reach the site, the page
  shows without styles or images.
- **The page runs in an isolated origin.** Its cookies and storage are kept in
  memory only and are lost on every reload. Requests back to its own server are
  cross-origin, so single-page apps that call their own APIs do not work. Web
  fonts and module scripts load only if the site allows cross-origin requests.
- **Bot protection can block the page.** Sites that use services such as Akamai
  or reCAPTCHA can block the server-side fetch or the browser's requests.
- **Example: the Bureau of Meteorology radar.** The new BOM radar page is a
  single-page app with bot protection; it does not work in proxy mode. The
  legacy radar loop (`https://reg.bom.gov.au/products/IDR663.loop.shtml`) works
  in proxy mode, and also in direct mode because it sends no framing headers.
- **Proxy mode is deny-all until a domain is allowlisted.** The proxy domain
  allowlist is **empty by default**, so on a fresh install the backend proxies
  nothing. An administrator must allowlist each target domain before proxy
  requests or a **Test URL** check succeed. See the
  [Administration guide](docs/administration.md#2-allowlist-management).
- **Frameability is detected only at configuration time.** If a target site later
  changes its framing headers, re-run "Test URL" and re-save the panel (or use a
  manual load-mode override) to pick up the new mode.
- **`hideSelectors` applies to proxied pages only.** Hiding elements requires
  rewriting the page HTML, which only happens in proxy mode; it cannot be applied
  to a cross-origin direct iframe.
- **Proxy mode does not transfer authenticated sessions.** The proxy sends no
  credentials to the upstream site, so pages that require a logged-in session,
  service workers or WebSockets/SSE do not render fully.

The [Troubleshooting guide](docs/troubleshooting.md#proxy-mode-page-shows-without-styles-images-or-scripts)
explains how to recognise each case.

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

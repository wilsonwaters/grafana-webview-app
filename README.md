# Webview — Grafana App Plugin

> **Status: in active development.** This plugin is not yet production-ready. APIs, configuration, and features may change without notice.

[![CI](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml/badge.svg)](https://github.com/wilsonwaters/grafana-webview-app/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/wilsonwaters/grafana-webview-app)](LICENSE)
[![Release](https://img.shields.io/github/v/release/wilsonwaters/grafana-webview-app)](https://github.com/wilsonwaters/grafana-webview-app/releases)
[![Grafana](https://img.shields.io/badge/Grafana-%3E%3D12.3.0-orange)](https://grafana.com)

A Grafana App plugin (`wilsonwaters-webview-app`) that embeds external web pages inside a Grafana panel with interactive pan/zoom viewport control. The plugin supports two loading strategies: a **direct iframe mode** for sites that permit framing, and a **security-hardened backend proxy mode** that fetches and re-serves content server-side for sites that block direct embedding via `X-Frame-Options` or `Content-Security-Policy` headers.

---

## Features

- **Config mode** — administrators configure the target URL, loading strategy, and proxy settings from the plugin configuration page (requires Admin role).
- **View mode** — end-users interact with the embedded page via an iframe rendered inside the Grafana panel.
- **Direct iframe loading** — embeds the target URL in a sandboxed `<iframe>` with no server-side involvement; zero-latency for sites that permit framing.
- **Backend proxy mode** — a Go backend service fetches the remote page and rewrites it for same-origin delivery, allowing content from sites that set `X-Frame-Options: DENY/SAMEORIGIN` to be displayed inside Grafana.
- **Direct-only fallback** — when no backend is available (e.g. Grafana without backend plugin support), the plugin falls back to direct iframe mode automatically.
- **Viewport pan/zoom** — users can pan and zoom the embedded viewport using mouse/touch gestures, useful for dashboards showing large web pages at reduced scale.
- **Security-hardened proxy** — the backend proxy is in development and will include request allowlisting, header sanitisation, and Grafana RBAC checks before content is forwarded.

> **Note:** Features listed above describe the intended design. Not all features are fully implemented. See [CHANGELOG.md](CHANGELOG.md) for what is landed in each release.

---

## Compatibility

| Requirement | Version |
|-------------|---------|
| Grafana | >= 12.3.0 |
| Grafana Plugin SDK for Go | see `go.mod` |

---

## Quick Start

> Full installation and configuration guides are being written as part of a documentation milestone.

- [Installation guide](docs/installation.md) *(coming soon)*
- [Configuration reference](docs/configuration.md) *(coming soon)*
- [Administration guide](docs/administration.md) *(coming soon)*

In the meantime, use the Development section below to run the plugin locally.

---

## Development

### Prerequisites

- Node.js (see `.nvmrc` or `package.json` for the required version)
- Go 1.21+
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

# Run unit tests (CI mode — exits after completion)
npm run test:ci

# Run end-to-end tests (requires a running Grafana instance)
npm run e2e

# Lint
npm run lint
```

### Backend

```bash
# Build Go backend for Linux
mage -v build:linux
```

### Run a local Grafana instance

```bash
# Starts Grafana with the plugin pre-loaded at http://localhost:3000
docker compose up
```

Default credentials: `admin` / `admin`.

---

## Known Limitations

- *Placeholder* — known limitations will be documented here as the plugin matures.
- Sites with strict CSP or cookie requirements may not render correctly via the proxy.
- The backend proxy is not yet feature-complete; some rewrites (JavaScript asset paths, WebSockets) are not yet handled.

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before opening a pull request.

---

## Security

To report a vulnerability, please use [GitHub Security Advisories](https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new). Do **not** open a public issue for security reports. See [SECURITY.md](SECURITY.md) for the full reporting policy.

---

## License

Licensed under the [Apache License 2.0](LICENSE).

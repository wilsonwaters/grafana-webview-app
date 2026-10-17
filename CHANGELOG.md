# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First release of the Webview app plugin for Grafana (`wilsonwaters-webview-app`),
a Grafana app plugin with a Go backend and a nested **Web View panel**
(`wilsonwaters-webview-panel`) for embedding external web pages in a dashboard
with a controllable, author-captured viewport.

### Added

- **Web View panel** that embeds an external URL in a sandboxed iframe, with a
  hard split between configuration mode (panel editor) and view mode (dashboard
  viewer).
- **Viewport control** — authors pan and zoom a live preview to capture a
  viewport (X/Y offset and zoom), saved into panel options and reapplied at view
  time via a CSS transform. View mode is non-interactive and renders exactly the
  captured region. Virtual iframe dimensions and numeric viewport inputs are
  configurable.
- **Direct iframe mode** for sites that permit framing, with no backend
  involvement.
- **Backend proxy pipeline** — a Go backend that fetches a page server-side,
  strips framing-control headers, and rewrites the HTML so subresources resolve,
  making framing-blocked sites embeddable. Resource handlers: `/check-frameable`,
  `/proxy`, `/proxy-resource`, and `/health`.
- **Frameability detection ("Test URL")** that determines whether a URL can be
  framed directly or must be proxied, with Auto / Direct / Proxy load-mode
  selection. The detected mode is stored so view mode never re-detects.
- **Auto-refresh** with an optional per-panel refresh interval, a CSS
  **hide-selectors** option applied during proxy HTML rewriting, and an optional
  author-facing **debug overlay**.
- **Graceful degradation** to direct-iframe-only mode when no backend is
  available, with the proxy option and "Test URL" disabled in the editor; backend
  availability is detected via the `/health` resource.
- **Security hardening** for the backend proxy: an empty-by-default domain
  allowlist (default deny), a hardcoded non-configurable private/reserved IP
  blocklist with a DNS-rebinding-safe dialler, scheme and header restrictions
  (outgoing credential/`X-Grafana-*` stripping and response header stripping),
  per-instance and per-domain rate limiting, a concurrency cap,
  response-size and timeout limits, structured audit logging, and Prometheus
  metrics. All controls are on by default and fail closed. See
  [SECURITY.md](SECURITY.md).
- **Project foundation** — Grafana app plugin scaffold (Go backend, created via
  `@grafana/create-plugin`), local development environment (Docker Compose +
  Grafana), Playwright-based end-to-end tests, plugin signing wired into the
  release workflow, and repository documentation.

### Known limitations

- In-panel Proxy-mode rendering is deferred for this release: Grafana stamps
  `X-Frame-Options: deny` and a sandbox CSP on plugin resource routes, which
  blocks framing the proxy resource inside a panel. Direct mode renders in-panel;
  the proxy pipeline is backend-complete and verified. See the
  [README](README.md#known-limitations) and issue
  [#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102).

[Unreleased]: https://github.com/wilsonwaters/grafana-webview-app/commits/main

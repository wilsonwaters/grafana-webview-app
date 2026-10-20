# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - Unreleased

First stable release. Proxy mode now renders inside the panel on a standard
Grafana install, administrators manage the allowlist on the Configuration page,
and every finding from the 2026-06-10 security review is fixed.

### Added

- **In-panel proxy rendering** ([#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102)).
  The panel reads the rewritten HTML from `/proxy` and renders it in an
  `<iframe srcdoc sandbox="allow-scripts">`. The page runs in an isolated
  (opaque) origin. Images, scripts and stylesheets load directly from the
  upstream site in the viewer's browser. Works on stock Grafana 12.4–13.2 in
  Chromium, Firefox and WebKit, with no change to `allow_embedding` or cookie
  settings, and under a Grafana sub-path. The editor preview uses the same
  renderer.
- **Configuration page** ([#129](https://github.com/wilsonwaters/grafana-webview-app/issues/129)).
  Admins add, edit and remove allowlist entries (domain, subdomains, private-IP
  opt-in with a warning, extra ports, rate-limit override) and set the six
  global limits. Every field is validated before saving, an empty allowlist
  shows a notice, and changes apply to the next request without a restart.
- Proxy-mode loading and error states in the panel: a status message while the
  page loads, and the HTTP status and reason when the proxy refuses a request.
- The rewriter injects `<meta name="referrer" content="no-referrer">` and a
  small compatibility script that gives the sandboxed page in-memory cookies and
  storage, so pages such as the BOM legacy radar loop keep working.
- `/check-frameable` requests are now audited and metered like `/proxy`
  requests (`endpoint=check-frameable`).
- New denial reason `role` (403) in `webview_proxy_denials_total`.

### Changed

- **Grafana 12.4.0 or later is required** (was 12.3.0). Tested on 12.4, 13.0,
  13.1 and 13.2.
- `@grafana/*` packages moved to 13.2.4 with React 19 (the code stays React 18
  compatible); create-plugin scaffold 7.13.0; Go 1.27.2 and
  grafana-plugin-sdk-go v0.297.0.
- The HTML rewriter emits **absolute upstream URLs** for subresources and links
  instead of routing them back through Grafana, and places `<base href>` first
  in `<head>`.
- `/proxy` reads the whole upstream response before it replies. An oversize
  response is always a `413`, never a truncated `200`.
- `/proxy` and `/check-frameable` send no CORS headers and remove the upstream's
  `Access-Control-*` headers.
- The per-instance rate limit is counted per Grafana organisation.
- The provisioning file is now for development and CI only. It no longer
  contains a secret or the unused `apiUrl` key, and production installs start
  with an empty allowlist.
- CI: every workflow action is pinned to a commit SHA, the Levitate API
  compatibility gate is back on, and the e2e matrix is resolved from
  `grafanaDependency`.
- Documentation updated throughout for the above.

### Fixed

- The rewriter's subresource URLs resolved against the injected `<base href>`
  and pointed at the upstream host, so proxied subresources never loaded.
- `Access-Control-Allow-Origin` was sent twice on proxy responses.
- Saving the old Configuration page replaced `jsonData` and dropped the
  allowlist ([#113](https://github.com/wilsonwaters/grafana-webview-app/issues/113)).
  The new page merges its changes into `jsonData`.
- `/health` wrote its status after the body.
- The Web View visualization is selected reliably in e2e tests on Grafana
  12.4–13.3.
- Shell scripts keep LF line endings on Windows checkouts.

### Security

Fixes for the 2026-06-10 security review (VR-1 to VR-14):

- **Panel URL validation** (VR-1): only absolute `http://` and `https://` URLs
  are loaded, in view mode and in the editor preview. Other schemes, relative
  URLs, and URLs with credentials or control characters show an error.
- **Per-mode iframe sandbox** (VR-2): proxy-mode frames get
  `sandbox="allow-scripts"` only. Direct-mode frames get `allow-same-origin`
  only for a cross-origin URL.
- **Authorisation** (VR-3, VR-12): `/check-frameable` requires the Editor or
  Admin org role. `/proxy` stays available to every signed-in user, and the
  documentation now states this model instead of promising RBAC checks.
- **Provisioning** (VR-4, VR-13): no committed secret, no allowlist outside
  dev/CI, unused `apiUrl`/`apiKey` settings removed.
- **`Refresh` response header stripped** (VR-5).
- **Decoded-size limits** (VR-6, VR-7): the decoded size of every gzip response,
  and the size of the rewritten HTML, is bounded by `maxResponseBytes` (413); an
  unsupported `Content-Encoding` is refused (502).
- **Scaffold routes removed** (VR-8): `/ping` and `/echo` return 404.
- **Development setup warnings** (VR-9) in the README, installation, developer
  and administration guides.
- **Dependencies** (VR-10): the 13.2 dependency update clears the release
  validator's critical and high `osv-scanner` findings.
- **6to4 and NAT64** (VR-11): addresses in `2002::/16` and `64:ff9b::/96` are
  classified by their embedded IPv4 address; `64:ff9b:1::/48` is always
  blocked; the private-IP opt-in never relaxes a translated address.
- **Action pinning** (VR-14): every GitHub Actions dependency is pinned to a
  commit SHA.
- **Bounded hide-selector matching**: `/proxy` refuses (400) more than 20 hide
  selectors, a selector over 256 bytes, and selectors that use `:has()`,
  `:contains()`, `:matches()`, `#=` or a combinator inside `:not()`.
  Combinators are matched in linear time, and the matching work per page is
  capped (413). The panel editor shows which selectors it does not send.

### Removed

- The `/proxy-resource` route. Subresources now load directly from the
  upstream site.
- The scaffold `/ping` and `/echo` routes.
- The scaffold **API URL** and **API Key** fields on the Configuration page.

### Known issues

- With Grafana's `[security] content_security_policy = true`, proxied pages show
  markup and images only; their scripts and external stylesheets are blocked.
- Proxy mode needs the viewer's browser to reach the upstream site. Pages run in
  an isolated origin with in-memory cookies and storage, so single-page apps that
  call their own APIs, and pages behind bot protection, do not work. For
  example, the new BOM radar page does not work; the legacy loop
  `https://reg.bom.gov.au/products/IDR663.loop.shtml` does.
- **Test URL** checks the basic org role, so a Viewer with a custom RBAC role
  that allows dashboard editing cannot use it.
- The allowlist is not confidential: any signed-in user can learn whether a
  host is allowlisted.
- Real Safari, Grafana Enterprise and Grafana Cloud are not tested.

## [0.0.1] - 2026-10-18

> **Never released.** The v0.0.1 release run failed at the dependency security
> gate, so no 0.0.1 package was published. Its changes ship in 1.0.0.

First **alpha** release of the Webview app plugin for Grafana (`wilsonwaters-webview-app`),
a Grafana app plugin with a Go backend and a nested **Web View panel**
(`wilsonwaters-webview-panel`) for embedding external web pages in a dashboard
with a controllable, author-captured viewport. Privately signed for self-hosted
(Path 1) use. This is an early alpha — see **Known limitations** below.

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

- **In-panel Proxy-mode rendering is deferred** for this release: Grafana stamps
  `X-Frame-Options: deny` and a sandbox CSP on plugin resource routes, which
  blocks framing the proxy resource inside a panel. Direct mode renders in-panel;
  the proxy pipeline is backend-complete and verified. See the
  [README](README.md#known-limitations) and issue
  [#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102).
- **No allowlist configuration UI yet.** The domain allowlist (the proxy's core
  security control) is configured via the Grafana plugin settings API or
  provisioning — see the [administration guide](docs/administration.md). An
  in-app editor is planned for a follow-up release
  ([#129](https://github.com/wilsonwaters/grafana-webview-app/issues/129)).
- **Grafana 13.2+ not yet verified.** Validated against Grafana 12.3–13.1; a
  dependency bump for 13.2+ is planned for a follow-up
  ([#131](https://github.com/wilsonwaters/grafana-webview-app/issues/131)).

[1.0.0]: https://github.com/wilsonwaters/grafana-webview-app/releases/tag/v1.0.0
[0.0.1]: https://github.com/wilsonwaters/grafana-webview-app/releases/tag/v0.0.1

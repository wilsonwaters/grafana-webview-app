# Project Brief — Grafana Web View App Plugin

## What is being built

A Grafana **App plugin** (`wilsonwaters-webview-app`) with a Go backend that ships a
nested **Web View panel**. The panel embeds an external web page inside a Grafana
dashboard panel and lets a dashboard author control the viewport — panning and zooming
to focus on a specific region of the page (e.g. a single city on a weather radar).

There is a hard split between two modes:

- **Configuration mode** (panel editor) — the author enters a URL, tests whether the
  site allows direct framing, chooses a load mode, and interactively positions the
  viewport. This is an authoring activity that may call the backend.
- **View mode** (dashboard viewer) — the panel statically renders the configured URL at
  the saved viewport. No detection, no interactive pan/zoom. The viewer sees exactly the
  region the author captured.

The page is loaded one of two ways, decided at config time and saved into panel options:

- **Direct iframe** — for sites that permit framing; the iframe `src` is the target URL
  with no backend involvement.
- **Backend proxy** — for sites that block framing via `X-Frame-Options` or CSP
  `frame-ancestors`; the Go backend fetches the page server-side, strips framing-control
  headers, rewrites HTML so subresources resolve, and serves it same-origin to Grafana.

The backend proxy is the **security-critical** component. It is designed to survive a
Grafana Labs catalog security review: empty-by-default domain allowlist, a hardcoded
non-configurable private/reserved IP blocklist, a DNS-rebinding-safe dialler, header
stripping in both directions, scheme/port restrictions, rate limits, a concurrency cap,
response-size/timeout limits, structured audit logging, and Prometheus metrics. All
security controls are on by default and fail closed.

## Target users

- **Dashboard authors** — configure a Web View panel: set the URL, run frameability
  detection, position the viewport, choose refresh and hide-selectors.
- **Dashboard viewers** — consume the configured panel; see a static, non-interactive
  region of the embedded page.
- **Grafana admins** — install and sign the plugin, configure the proxy allowlist and
  limits via plugin settings, monitor audit logs and metrics.

## Key features

- Web View panel nested inside the app plugin, registered alongside the app.
- Config mode: URL entry, "Test URL" frameability check, load-mode selector
  (Auto / Direct / Proxy), interactive drag-pan and wheel-zoom preview with live
  coordinate readout, "Capture current view" button, numeric X/Y/zoom inputs, virtual
  iframe dimensions, optional refresh interval and CSS hide-selectors.
- View mode: static viewport via CSS `transform: scale() translate()`, clipped by an
  `overflow: hidden` container, `pointer-events: none` on the iframe, optional auto-refresh,
  multiple independent panel instances per dashboard.
- Backend resource handlers: `/check-frameable`, `/proxy`, `/proxy-resource`, `/health`.
- Security pipeline shared by every proxying endpoint: scheme check → allowlist →
  rate limit → concurrency cap → DNS resolve + IP blocklist → dial-resolved-IP →
  outgoing header strip → fetch (timeout + max body) → response header strip →
  HTML rewriting → audit log + metrics.
- **Graceful degradation:** when the backend is unavailable (e.g. Grafana Cloud without
  backend support), the panel is still creatable in **direct-iframe-only** mode, with the
  proxy option and "Test URL" cleanly disabled in the editor. Backend availability is
  detected via the `/health` resource.

## Platforms

- Grafana `>=12.3.0` (per `grafanaDependency` in `plugin.json`).
- Backend binary `gpx_webview` built with Mage; runs as a gRPC subprocess.
- Local dev via the scaffolded `docker compose up`.
- Deployment Path 1: self-hosted Grafana, private-signed, distributed via GitHub releases.
- Deployment Path 2 (deferred): Grafana Cloud catalog via Community signing.

## Tech stack

- **Frontend:** TypeScript / React 18, `@grafana/data`, `@grafana/ui`, `@grafana/runtime`,
  `@grafana/schema`, `@grafana/i18n`; styles via `useStyles2()`; webpack/swc/jest from
  `@grafana/create-plugin` (do not hand-roll).
- **Backend:** Go with `github.com/grafana/grafana-plugin-sdk-go`; proxy built on
  `net/http/httputil.ReverseProxy`; HTML manipulation with `goquery` /
  `golang.org/x/net/html` (no regex HTML parsing); `httpadapter` + `http.ServeMux` for
  resource routing (matches existing scaffold in `pkg/plugin/`).
- **Testing:** Jest (frontend), Go `testing` (backend), Playwright via
  `@grafana/plugin-e2e` (E2E); `@grafana/plugin-validator` before release.
- **CI/CD:** existing GitHub Actions (`ci.yml`, `release.yml`, `e2e.yml` to be confirmed/added),
  `dependabot.yml`.

## Integrations

- Grafana panel editor context (to distinguish config vs view mode).
- `getBackendSrv()` to call backend resource handlers.
- Grafana plugin settings UI for allowlist and proxy limits (not env vars).
- Grafana log stream for audit entries; Prometheus endpoint for metrics.
- Grafana plugin signing (private, then community).

## Hard constraints

- All security controls mandatory, on by default, fail closed; the IP blocklist is
  hardcoded and not admin-configurable.
- iframe `sandbox="allow-scripts allow-same-origin"` always; never broadened.
- Proxy is stateless and unauthenticated: no cookies, no `Authorization`, no
  `X-Grafana-*` forwarded; no sessions; no WebSockets/SSE; no server-side JS execution.
- Conform to Grafana plugin development standards; TypeScript strict mode; ESLint/Prettier
  defaults from create-plugin; minimum Grafana version declared via `grafanaDependency`.
- Do not modify the scaffold's `.config/`, workflows, `plugin.json`, or `package.json`
  during planning. Author/org identity is the GitHub identity `wilsonwaters` only.

## Success criteria

- Acceptance criteria 1–36 in the implementation spec are all met and traceable to tasks.
- A self-hosted, private-signed v1.0 release installs and works for both direct and
  proxied sites (Path 1 complete).
- The panel remains creatable in direct-only mode when no backend is present.
- The security suite runs on every PR and cannot be skipped; plugin passes
  `@grafana/plugin-validator`.
- Documentation (README, SECURITY.md, docs/) anticipates catalog-reviewer questions, with
  Path 2 submission materials prepared but submission deferred.

## Explicit non-goals

- No frontend-only / direct-iframe-only *product* variant as a separate distribution
  (the spec's "Path 3"). Direct-only is a runtime degradation of the same plugin, not a
  separate build.
- No headless-browser rendering (no chromedp/go-rod), no screenshot services.
- No credential/cookie/session forwarding; no WebSocket/SSE proxying; no server-side
  execution of proxied JavaScript.
- No actual catalog submission in this phase — materials are prepared, submission deferred
  pending stakeholder go-ahead.
- The detailed written threat model is a deferred documentation task, not part of these
  planning files.

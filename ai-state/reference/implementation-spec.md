# Task: Build a Grafana Panel Plugin — "Web View"

## Overview

Create a Grafana panel plugin that embeds external web pages with a controllable viewport (pan, zoom, focus on a specific region of the page). The plugin must work generically across many websites and gracefully handle sites that block iframe embedding.

A clear distinction must be maintained between **configuration mode** (panel editor — where the dashboard author sets up the panel) and **view mode** (the normal dashboard view — where the configured panel is simply displayed to viewers).

## Deployment Strategy

The plugin is designed for two deployment paths:

**Path 1 (initial target): Self-hosted Grafana.** Sign as a Private plugin via a Grafana Cloud organisation account, distribute via GitHub releases, and let users install via `GF_INSTALL_PLUGINS` or manual install. This is where the plugin must work first.

**Path 2 (long-term goal): Published in the Grafana plugin catalog as a Community-signed plugin.** This is the only way users on Grafana Cloud (or other managed Grafana services that disallow side-loading) can install it. Reaching this path requires passing Grafana Labs' security review.

Because Path 2 requires Grafana Labs to approve a backend plugin that proxies arbitrary URLs and strips frame-busting headers, **security hardening is treated as a first-class feature**, not a finishing touch. Reviewers will scrutinise SSRF risk, open-proxy risk, header-stripping policy, and the plugin's ability to bypass Grafana's `[security.egress]` controls. Every security control listed below is mandatory and must be implemented from the start, with documentation that anticipates reviewer questions.

Path 3 (a frontend-only, direct-iframe-only variant for Cloud) is **not in scope** at this stage. The plugin's value depends on the proxy capability.

## Functional Requirements

### Configuration Mode (Panel Editor)

This is where a dashboard author sets up the web view. The editor allows the author to:

1. **Enter a target URL**
2. **Run a "Test URL" check** that detects whether the target site permits direct iframe embedding
   - Performed via a backend HEAD/GET request that inspects `X-Frame-Options` and CSP `frame-ancestors` headers
   - Result displayed to the author as: `Direct (allowed)`, `Proxied (required)`, or `Error`
3. **Set the load mode** with three options:
   - **Auto** (default) — the test result determines whether to use direct or proxy
   - **Direct** (manual override) — always load via iframe directly
   - **Proxy** (manual override) — always load via the backend proxy
4. **Interactively position the viewport** within the iframe using a preview pane:
   - Drag to pan
   - Scroll wheel to zoom
   - Live coordinates display (X, Y, zoom factor)
   - "Capture current view" button writes the current pan/zoom into the configuration
5. **Configure virtual iframe dimensions** (default 1920×1080) so content renders at expected size
6. **Optionally configure**: refresh interval, custom CSS selectors to hide

### View Mode (Dashboard Viewer)

When the dashboard is viewed normally:

1. The panel **simply renders the configured URL** with the configured viewport (X, Y, zoom)
2. **No interactive pan/zoom** by default — the viewer sees exactly the portion the author configured
3. Load mode is determined by the saved configuration (no runtime detection — that already happened at config time)
4. Optional auto-refresh based on configured interval
5. The viewer should not be able to accidentally modify what they see

This separation is critical: configuration is an authoring activity, viewing is a consumption activity. The "test URL" detection only happens during configuration, never during normal viewing.

### Backend Proxy Requirements

When proxy mode is active (either by auto-detection at config time or manual override), the backend (Go plugin) must:

- Fetch the target URL server-side
- Strip `X-Frame-Options` header
- Strip or modify CSP `frame-ancestors` directives
- Strip CSP meta tags in HTML that block framing
- Inject `<base href="...">` tag so relative URLs resolve correctly
- Rewrite absolute URLs in `src`/`href` attributes to route through the proxy for subresources
- Remove common frame-busting JavaScript patterns (`if (top !== self)`, etc.)
- Handle gzip-compressed responses
- Handle redirects by rewriting Location headers to proxy URLs
- Validate target URL against a configurable domain allowlist (security)
- Forward appropriate request headers (User-Agent, Accept)
- Set permissive CORS headers on proxy responses

### Security Requirements (Mandatory — Required for Catalog Approval)

The proxy is the security-critical part of this plugin. Reviewers from Grafana Labs will treat a backend plugin that fetches arbitrary URLs server-side as high risk. Every control below is mandatory, must be on by default, and must be documented in `SECURITY.md` and `docs/administration.md` with the threat it mitigates.

**Threat model (document this explicitly):**

The proxy could be abused as:
1. An **SSRF tool** to reach internal services on the Grafana host's network (e.g. internal APIs, databases, cloud metadata endpoints like `169.254.169.254`)
2. A **generic open proxy** for arbitrary outbound traffic from Grafana's IP space
3. A **clickjacking vector** by proxying legitimate sites with `X-Frame-Options` stripped, then embedding them in attacker-controlled dashboards
4. A **DNS rebinding target** where a hostname resolves to a public IP at check time, then to an internal IP at fetch time
5. A **bandwidth/resource exhaustion vector** via huge response bodies or many concurrent requests

Each control below addresses one or more of these threats.

**SSRF and open-proxy prevention:**

- **Domain allowlist is required and empty by default.** A fresh install of the plugin proxies nothing. The Grafana admin must explicitly configure allowed domains via plugin settings before the proxy will respond with anything but `403 Forbidden`. This is the single most important control. Document this prominently — admins who don't read docs should still get fail-safe behaviour.
- **Hardcoded, non-configurable IP blocklist** applied to the resolved IP of every proxy target. The admin cannot disable or modify this list. It must include at minimum:
  - All RFC 1918 private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`)
  - Loopback (`127.0.0.0/8`, `::1`)
  - Link-local (`169.254.0.0/16`, `fe80::/10`) — covers AWS/GCP/Azure metadata endpoints
  - Carrier-grade NAT (`100.64.0.0/10`)
  - IPv6 unique-local (`fc00::/7`) and IPv4-mapped IPv6 addresses
  - Multicast and reserved ranges
- **DNS rebinding protection:** resolve the target hostname to its IP once, validate the IP against the blocklist, then dial that exact IP directly (with the original `Host` header) rather than re-resolving. Use Go's `net.Dialer.Control` callback to enforce this.
- **Cloud metadata endpoint blocking** by hostname as well as IP — explicitly deny `metadata.google.internal`, `metadata.goog`, and any hostname resolving into link-local space.
- **Scheme allowlist:** `http` and `https` only. Reject `file://`, `gopher://`, `dict://`, `ftp://`, and anything else.
- **Port restrictions:** by default only `80`, `443`, and ports declared in the allowed domains list. No proxying to arbitrary ports.
- **Optional opt-in for private ranges:** if an admin genuinely needs to proxy an internal page (e.g. an internal status board), they can opt-in per-domain to bypass the IP blocklist for that specific domain only. This is opt-in, audit-logged, and off by default.

**Request and response controls:**

- **Maximum response body size** (default 5 MiB, configurable) — protects against memory exhaustion. Reject larger responses with `413 Payload Too Large`.
- **Per-request timeout** (default 10 seconds, configurable) on both connection and total request.
- **Maximum redirect depth** (default 3). Each redirect target is re-validated against the allowlist and IP blocklist — a redirect into private space is rejected.
- **Strip request headers** that could leak Grafana context: `Cookie`, `Authorization`, `X-Grafana-*`, any custom auth headers. The proxy is explicitly stateless and unauthenticated.
- **Set conservative outgoing headers:** a plugin-identifying `User-Agent`, `Accept` for HTML/CSS/JS/images, no auth, no cookies.
- **Strip response headers** beyond the framing-control ones: `Set-Cookie` (don't let proxied sites set cookies in Grafana's origin), `Strict-Transport-Security` (don't apply third-party HSTS to Grafana's domain), `Public-Key-Pins`, `Clear-Site-Data`.

**Rate limiting and abuse prevention:**

- **Per-Grafana-instance rate limit** on the proxy endpoint (default 60 req/min, configurable). Implemented as a token bucket in-memory in the plugin process.
- **Per-target-domain rate limit** to prevent any single domain being hammered (default 30 req/min per domain).
- **Concurrent connection cap** (default 10 in-flight proxy requests) — protects the Grafana process from being overwhelmed.
- **Log every proxy request** at info level with: target URL, source Grafana user (if identifiable), response status, response size, duration. This gives admins an audit trail and aids incident response.

**Frontend security:**

- iframe must always use `sandbox="allow-scripts allow-same-origin"` — never broaden this
- Never inject user-provided strings into the proxied HTML without escaping (the inline-CSS hide-selector feature is a vector to watch — validate CSS selectors strictly)
- The "Test URL" endpoint must enforce the **same allowlist and IP blocklist** as the proxy itself. A common mistake is leaving check endpoints unguarded.

**Configuration and operability:**

- All security-relevant defaults must be safe (empty allowlist, all blocklists enabled, modest rate limits).
- Admins configure the plugin via Grafana plugin settings UI, not environment variables, so changes are visible and auditable.
- Plugin emits Prometheus metrics for: proxy requests by status code, requests denied by reason (allowlist, IP blocklist, rate limit, size limit), in-flight requests, request duration.
- Plugin must be signed before distribution; document the Private→Community signing path.

**What this plugin deliberately does NOT do (document this too):**

- It does not forward credentials or cookies
- It does not maintain session state across requests
- It does not bypass `[security.egress]` controls *for the requests it makes* — the egress allowlist is one layer; the plugin's own allowlist is a second layer; both apply
- It does not proxy WebSockets, Server-Sent Events, or non-HTTP protocols
- It does not execute proxied JavaScript server-side (no headless browser, no rendering)

## Recommended Off-the-Shelf Foundations

To reduce custom code (and therefore security surface area), the backend should be built on top of these well-established libraries rather than writing a proxy from scratch:

### Go Backend Libraries

**`net/http/httputil.ReverseProxy` (Go standard library)** — Use as the proxy foundation. It is battle-tested, provides clean hooks for our needs, and is part of Go's standard library (no external dependency).

- `Director` function — modify the outgoing request (set headers, validate allowlist)
- `ModifyResponse` function — strip `X-Frame-Options`, modify CSP headers
- `ErrorHandler` function — handle proxy failures gracefully
- Automatically handles streaming, hop-by-hop headers, connection pooling

Example sketch:
```go
proxy := &httputil.ReverseProxy{
    Director: func(req *http.Request) {
        // Validate against allowlist, rewrite target URL
    },
    ModifyResponse: func(resp *http.Response) error {
        resp.Header.Del("X-Frame-Options")
        modifyCSP(resp.Header)
        if isHTML(resp) {
            return rewriteHTMLBody(resp)
        }
        return nil
    },
}
```

**`golang.org/x/net/html` or `github.com/PuerkitoBio/goquery`** — Use for HTML manipulation (URL rewriting, base tag injection, CSP meta removal). **Do not use regex for HTML parsing** — it is fragile and a known anti-pattern.

- `golang.org/x/net/html` is the official Go HTML parser
- `goquery` provides a jQuery-like API on top of it (often more ergonomic)

**`github.com/grafana/grafana-plugin-sdk-go`** — Required. Grafana's official Go SDK for backend plugins.

### Off-the-shelf solutions evaluated and rejected

| Solution | Why rejected |
|----------|--------------|
| **Iframely** (Node.js) | Focused on oEmbed/Open Graph metadata for known publishers (YouTube, Twitter), not generic page proxying. Also Node.js, not Go. |
| **chromedp / go-rod** | Headless browser libraries. Would give perfect rendering but are far too heavyweight for an in-process Grafana plugin (requires bundled Chromium, GBs of RAM per instance). |
| **elazarl/goproxy** | HTTP forward proxy (man-in-the-middle for browsers), not a reverse proxy. Wrong shape for our use case. |
| **AaronO/gogo-proxy** | Adds load balancing on top of httputil.ReverseProxy. Unnecessary complexity for this use case. |
| **PHP-Proxy / similar** | Wrong language. We need a Go implementation to fit Grafana's backend plugin model. |

**Conclusion:** No single off-the-shelf solution does exactly what we need, but `httputil.ReverseProxy` + `goquery` covers ~80% of the heavy lifting. The custom code is limited to: allowlist validation, header stripping policy, HTML rewriting rules, and frameability detection. This is a small, well-defined surface area that is straightforward to audit.

## Technical Specifications

### Grafana Plugin Development Standards (Mandatory)

All development must conform to Grafana's official plugin development standards. Reference: https://grafana.com/developers/plugin-tools/

**Scaffolding & tooling:**
- Use `npx @grafana/create-plugin@latest` to scaffold the project (creates correct structure, build setup, CI config)
- Choose plugin type: **App plugin with backend** (allows shipping panel + backend resource handlers together)
- Do not hand-roll webpack/babel/typescript config — use what create-plugin provides
- Use `mage -v build:backend` to build Go backend
- Use `docker compose up` (provided in scaffold) for local development

**Plugin SDK & APIs:**
- Use the latest stable plugin API versions
- Frontend dependencies: `@grafana/data`, `@grafana/ui`, `@grafana/runtime`, `@grafana/schema`
- Backend dependency: `github.com/grafana/grafana-plugin-sdk-go`
- Use Grafana UI components (`@grafana/ui`) wherever possible instead of custom UI — ensures theme consistency and accessibility
- Respect Grafana's theme system (light/dark mode) via `useTheme2()` or `useStyles2()` hooks

**Backwards compatibility:**
- Specify minimum supported Grafana version in `plugin.json` via `grafanaDependency`
- Use runtime checks for newer APIs rather than build-time imports where possible
- Maintain a single development branch across the supported Grafana version range

**Quality & testing:**
- TypeScript strict mode enabled
- ESLint and Prettier configured (defaults from create-plugin)
- Unit tests with Jest (frontend) and Go's testing package (backend)
- End-to-end tests with Playwright (`@grafana/plugin-e2e` package) across a matrix of Grafana versions
- Use the Grafana plugin validator (`@grafana/plugin-validator`) before submission

**CI/CD:**
- GitHub Actions workflow for build + test on every PR (template included by create-plugin)
- Automated release workflow that builds, signs, and packages on tag push
- Plugin signing required for distribution outside private installations — use Grafana's signing process

**plugin.json requirements:**
- Proper plugin ID following `<organization>-<name>-<type>` convention
- Accurate metadata: name, description, author, links to docs and source
- Screenshot assets in `src/img/`
- `backend: true` and `executable` field set for the Go binary
- Resource routes declared via the backend SDK

**Documentation:**
- README with installation, configuration, usage, screenshots, limitations
- CHANGELOG following Keep-a-Changelog format
- LICENSE file (recommend Apache 2.0 or MIT)
- Inline JSDoc/TSDoc for exported types
- Inline Go doc comments for exported functions

### Frontend (TypeScript/React)

- Panel component in **config mode**: renders an interactive viewport editor with drag/zoom and a "Test URL" button
- Panel component in **view mode**: renders a static viewport showing the configured region
- Detect mode via Grafana's panel editor context (the panel knows whether it's being edited)
- Container with `overflow: hidden` for clipping
- Iframe sized to virtual dimensions, transformed via CSS `transform: scale() translate()`
- In view mode: `pointer-events: none` on iframe (no interaction)
- In config mode: container captures mouse events for the author's pan/zoom interaction
- Use `getBackendSrv()` to call backend resource handlers
- Use `@grafana/ui` components (`Input`, `Button`, `Field`, `Switch`, `Select`) for all editor controls
- All styles via `useStyles2()` for theme consistency

### Backend (Go)

- Use `github.com/grafana/grafana-plugin-sdk-go`
- Built on `net/http/httputil.ReverseProxy` for the proxy itself
- Use `golang.org/x/net/html` or `goquery` for HTML manipulation (no regex parsing of HTML)
- Implement resource handlers:
  - `GET /proxy?url=...` — Main proxy endpoint (uses httputil.ReverseProxy under the hood)
  - `GET /proxy-resource?url=...` — Subresource proxy
  - `GET /check-frameable?url=...` — Returns `{frameable: bool, reason: string, recommendedMode: "direct" | "proxy"}`
  - `GET /health` — Health check
- Configuration via plugin settings (allowlist, timeouts, max response size)

### Panel Configuration Schema

```typescript
interface PanelOptions {
  // Source
  url: string;                          // Target URL
  
  // Load mode (decided at config time)
  loadMode: 'auto' | 'direct' | 'proxy'; // Default 'auto'
  detectedMode: 'direct' | 'proxy' | null; // Result of last "Test URL" check
  
  // Viewport (the saved view that viewers will see)
  viewportX: number;                    // X offset, default 0
  viewportY: number;                    // Y offset, default 0
  viewportZoom: number;                 // Zoom level 0.1-5.0, default 1.0
  
  // Virtual iframe dimensions
  iframeWidth: number;                  // Default 1920
  iframeHeight: number;                 // Default 1080
  
  // View-mode behaviour
  refreshIntervalSec: number;           // Default 0 (disabled)
  
  // Optional content tweaks
  hideSelectors: string;                // CSS selectors to hide, comma-separated
  
  // Debug/UX
  showDebugOverlay: boolean;            // Default false in view mode
}
```

## Acceptance Criteria

### Configuration mode
1. ✅ Author can enter a URL and click "Test URL" to detect frameability
2. ✅ Detection result is clearly displayed (Direct / Proxied / Error)
3. ✅ Detection result is stored in panel options so it persists with the dashboard
4. ✅ Author can manually override the auto-detected mode (Direct or Proxy)
5. ✅ Author can drag and scroll-wheel-zoom within the preview to position the viewport
6. ✅ Author can click "Capture current view" to save the current pan/zoom to options
7. ✅ Author can manually edit X, Y, zoom as numeric inputs in the editor

### View mode
8. ✅ Dashboard viewer sees the page at the configured X, Y, zoom
9. ✅ Viewer cannot pan or zoom (no interactive controls visible)
10. ✅ Configured load mode is honoured (direct or proxy, no re-detection)
11. ✅ Optional refresh interval reloads the iframe on schedule
12. ✅ Multiple panel instances on the same dashboard work independently

### Proxy behaviour
13. ✅ Proxied site (e.g. `https://www.bom.gov.au/australia/radar/`) loads successfully when proxy mode is active **and the domain is in the allowlist**
14. ✅ CSS, images, and JS within proxied pages load correctly (relative URLs resolve)
15. ✅ Domain not in allowlist returns 403 from proxy
16. ✅ Direct mode uses the URL as iframe src without involving the backend

### Security (mandatory — all must pass)
17. ✅ Fresh install with default settings refuses to proxy any URL (empty allowlist, fails closed)
18. ✅ Allowlist enforcement applies equally to `/proxy`, `/proxy-resource`, and `/check-frameable` endpoints
19. ✅ Request to a hostname resolving to RFC 1918 space returns 403 even if the hostname is in the allowlist (unless per-domain private-IP opt-in is set)
20. ✅ Request to `169.254.169.254`, `metadata.google.internal`, or any link-local address returns 403 regardless of allowlist
21. ✅ DNS rebinding attack (hostname resolves to public IP at check, private IP at fetch) is prevented — the resolved IP is dialled directly
22. ✅ Non-HTTP/HTTPS schemes (`file://`, `gopher://`, `dict://`, etc.) return 400
23. ✅ Redirect into a denied destination is blocked at the redirect step, not followed
24. ✅ Response larger than configured max-body-size is rejected with 413
25. ✅ Request exceeding the per-instance rate limit returns 429
26. ✅ `Cookie`, `Authorization`, and `X-Grafana-*` headers are stripped from outgoing requests
27. ✅ `Set-Cookie`, `Strict-Transport-Security`, `Public-Key-Pins` are stripped from incoming responses
28. ✅ Every proxy request emits a structured audit log entry with target URL, status, size, duration
29. ✅ Prometheus metrics are exposed for proxy requests, denials by reason, in-flight, and duration
30. ✅ Iframe always uses `sandbox="allow-scripts allow-same-origin"` — verified in E2E test
31. ✅ User-provided CSS selectors for hiding are validated and cannot inject markup

### Quality
32. ✅ Plugin passes `@grafana/plugin-validator` checks
33. ✅ Unit tests cover URL rewriting, header stripping, viewport transform math, IP blocklist, allowlist matching
34. ✅ E2E tests cover config flow, view-mode rendering, and security boundary cases
35. ✅ Light and dark themes both render correctly
36. ✅ All security tests pass in CI on every PR (security suite cannot be skipped)

## Deliverables

1. Complete plugin source code (frontend + backend)
2. Comprehensive repository documentation (see **Documentation Requirements** section below)
3. Example dashboard JSON demonstrating the panel with both direct and proxied URLs
4. Unit tests:
   - Backend: URL rewriting, header stripping, allowlist enforcement
   - Frontend: viewport transform calculations, config/view mode switching
5. E2E tests with `@grafana/plugin-e2e` covering the config flow and view rendering
6. GitHub Actions workflows for CI (build/test) and release (sign/package)

## Documentation Requirements

All documentation must live in the plugin's dedicated GitHub repository. Produce the following:

### Root-level repository files

**`README.md`** — the main entry point. Must include:
- Plugin name, short description, and a hero screenshot/GIF showing the plugin in action
- Status badges (CI, license, latest release, Grafana version compatibility, plugin signature status)
- Feature summary (configuration mode vs view mode, direct/proxy loading, viewport control)
- Compatibility matrix: supported Grafana versions, supported plugin signature levels, supported deployment platforms (Docker, Kubernetes, self-hosted, Grafana Cloud where applicable)
- Quick-start: how a user installs the plugin and creates their first panel in under 5 minutes
- Links to all other documentation files
- Screenshots: panel editor, view mode, the "Test URL" detection result, dashboard with multiple panels
- Known limitations summary
- Link to issues/discussions for support
- License notice

**`CHANGELOG.md`** — follows [Keep a Changelog](https://keepachangelog.com/) format and [Semantic Versioning](https://semver.org/). Each release section includes Added / Changed / Deprecated / Removed / Fixed / Security categories as relevant. Initial entry covers the first release.

**`LICENSE`** — Apache 2.0 or MIT (preferred for Grafana plugin ecosystem compatibility).

**`CONTRIBUTING.md`** — covers:
- Code of conduct expectations
- How to file a good bug report / feature request
- Branch naming conventions and PR process
- Commit message conventions (e.g. Conventional Commits)
- Required checks before submitting a PR (lint, tests, build)
- Reviewer expectations and turnaround

**`CODE_OF_CONDUCT.md`** — adopt the Contributor Covenant (standard for open source).

**`SECURITY.md`** — the public-facing security policy. Must include:
- How to responsibly report a security vulnerability (private email contact, do not file public issue)
- Scope: what is in/out of scope for security reports
- Expected response timeline and supported versions for security patches
- **Threat model summary** — what the proxy can be abused for (SSRF, open proxy, clickjacking, DNS rebinding, resource exhaustion) and the layered controls that mitigate each. This section is read by Grafana Labs reviewers during catalog submission and must be substantive, not boilerplate.
- **Security architecture summary**: empty allowlist by default, hardcoded IP blocklist, DNS-rebinding-protected dialler, scheme/port restrictions, header stripping (both directions), rate limits, audit logging
- **Explicit non-goals**: the proxy does not forward credentials, does not maintain sessions, does not replace Grafana's `[security.egress]` controls
- Pointer to `docs/administration.md` for operational guidance

**`.github/` directory** — must include:
- `ISSUE_TEMPLATE/bug_report.md` — structured bug report template
- `ISSUE_TEMPLATE/feature_request.md` — structured feature request template
- `ISSUE_TEMPLATE/config.yml` — links to discussions/support channels
- `PULL_REQUEST_TEMPLATE.md` — PR checklist
- `dependabot.yml` — automated dependency updates for npm and Go modules
- `workflows/ci.yml` — build, lint, test on every PR
- `workflows/release.yml` — sign and package on tag push
- `workflows/e2e.yml` — E2E tests across Grafana version matrix

### `docs/` directory

A dedicated documentation folder containing:

**`docs/developer-guide.md`** — for contributors and local development:
- Prerequisites: Node.js version, Go version, Docker, Mage, recommended editor + extensions
- Repository structure walkthrough (`src/`, `pkg/`, `provisioning/`, etc.)
- Initial setup: `git clone`, `npm install`, `mage -v build:backend`
- Development workflow:
  - `npm run dev` to watch frontend
  - `mage -v build:backend` after Go changes
  - `docker compose up` to launch Grafana with the plugin mounted
  - Where to find the plugin in the dev Grafana instance (http://localhost:3000)
  - Hot reload behaviour and limitations
- Running tests:
  - `npm run test` for frontend unit tests
  - `mage test` for backend unit tests
  - `npm run e2e` for Playwright E2E tests
  - How to run a single test, how to update snapshots
- Linting & formatting: `npm run lint`, `npm run lint:fix`, Go `gofmt`, `go vet`
- Debugging:
  - Frontend: browser devtools, source maps
  - Backend: attaching a debugger to the Go plugin subprocess
  - Common pitfalls (plugin not reloading, signature errors in dev mode, etc.)
- How to test against multiple Grafana versions locally
- How to validate with `@grafana/plugin-validator` before pushing

**`docs/installation.md`** — for end users installing the plugin. Must cover **both deployment paths** clearly:

*Path 1 — Self-hosted Grafana (available from first release):*
- System requirements (Grafana version, OS, architecture)
- Installation methods, each with copy-paste commands:
  - **Via Grafana CLI**: `grafana cli plugins install <plugin-id>` (once published) or `--pluginUrl` for a private release zip
  - **Via Docker**: setting `GF_INSTALL_PLUGINS=<url>;<plugin-id>` for private-signed releases
  - **Via Kubernetes / Helm**: values.yaml example for the official Grafana Helm chart
  - **Manual installation**: download release zip, extract to plugins directory, restart Grafana
- Private signing notes — explain `rootUrls` and how to obtain a build signed for your Grafana instance
- `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS` for development only, with strong warnings against production use
- Post-install verification: how to confirm the plugin loaded (Plugins page, server logs)
- **Required first-time configuration**: the allowlist is empty by default and the plugin will refuse to proxy anything until configured. Walk through configuring the first allowed domain.
- Upgrade procedure, uninstallation procedure

*Path 2 — Grafana Cloud (available once plugin is Community-signed and in the catalog):*
- Note that this path requires the plugin to be approved by Grafana Labs and published in the catalog
- How to install via the Grafana Cloud plugin catalog UI
- Configuration limitations on Cloud (allowlist still configurable via plugin settings, but admin access depends on Cloud plan)
- Note that other managed Grafana services (Amazon Managed Grafana, Azure Managed Grafana) may have additional restrictions — check vendor docs

*Other managed Grafana:*
- Azure Managed Grafana **does not support installing catalog plugins** — document this limitation
- Amazon Managed Grafana — confirm catalog plugin support at install time

**`docs/configuration.md`** — for dashboard authors using the plugin:
- Walkthrough with screenshots: adding a Web View panel to a dashboard
- Entering a URL and using "Test URL"
- Understanding the detected mode (Direct vs Proxied) and when to override
- Positioning the viewport: drag to pan, scroll to zoom, "Capture current view"
- Worked examples:
  - Embedding a public dashboard that allows framing (Direct mode)
  - Embedding the BOM weather radar (Proxied mode, with viewport positioned on a specific city)
  - Embedding an internal status page
- Configuring refresh interval
- Hiding unwanted page elements via CSS selectors
- Multiple panels on one dashboard

**`docs/administration.md`** — for Grafana admins running the plugin. This is the primary security-facing document for operators:
- **Threat model overview** (copy/expand from SECURITY.md): what the proxy can and cannot do, what risks it introduces, what controls mitigate them
- **First-time setup checklist**: confirm allowlist is empty, decide allowed domains, set rate limits appropriate to your Grafana scale
- **Allowlist management**:
  - How to add/remove domains via plugin settings UI
  - Per-domain options: allow subdomains, allow private IP opt-in (with strong warnings about when this is appropriate), per-domain rate limit override
  - Recommended workflow for handling allowlist requests from dashboard authors
- **IP blocklist** explanation — which ranges are hardcoded, why they cannot be disabled
- **Rate limit tuning** with example values for small/medium/large Grafana instances
- **Maximum response size and timeout tuning**
- **Audit logging**:
  - What gets logged for every proxy request
  - Where logs go (Grafana's main log stream)
  - Suggested log-based alerts (sudden spike in denials, repeated failed allowlist checks from one user, etc.)
- **Metrics**:
  - Prometheus endpoint exposed by the plugin
  - Key metrics to dashboard (proxy requests by status, denials by reason, in-flight, p95 duration)
  - Suggested alerting rules
- **Incident response**:
  - How to disable the plugin quickly (uninstall, remove from `GF_INSTALL_PLUGINS`, clear allowlist)
  - How to investigate suspected abuse (correlate logs with Grafana user/audit logs)
- **Interaction with Grafana's `[security.egress]` controls** — the plugin respects egress controls on top of its own allowlist; document this defence-in-depth posture
- **Upgrade and rollback procedures**

**`docs/troubleshooting.md`** — common issues and resolutions:
- "Refused to connect" errors — diagnosis flowchart
- Proxied page renders but CSS broken — likely subresource proxy issue
- Allowlist 403 errors
- Plugin not appearing in Grafana — signature, version compatibility
- Logs to check, where they live
- How to file a useful bug report (link to issue template)

**`docs/architecture.md`** — for advanced users and contributors:
- High-level architecture diagram (the one in this prompt)
- Configuration time vs view time flow diagrams
- Backend resource handler reference
- HTML rewriting strategy and known edge cases
- Decision log of architectural choices

**`docs/publishing.md`** — process for getting this plugin officially recognised by Grafana. Structured around the two-path deployment strategy:

*Phase 1 — Private signing for self-hosted distribution (achievable immediately):*
- Create a Grafana Cloud organisation account (free tier is sufficient for signing)
- Confirm the plugin ID prefix matches the org slug
- Generate an Access Policy token with `plugins:write` scope
- Use `@grafana/sign-plugin` with `--rootUrls` for the target Grafana instances
- Publish the signed zip as a GitHub release
- Document how downstream users install the private-signed build
- This phase has no Grafana Labs review — it's purely a self-publishing flow

*Phase 2 — Community signing and catalog submission (the Cloud goal):*

Pre-submission checklist:
- All quality gates passing (lint, tests, E2E, `@grafana/plugin-validator`)
- Plugin ID follows `<organization>-<name>-<type>` convention and is unique
- `plugin.json` metadata complete (description, links, screenshots, version)
- README, CHANGELOG, LICENSE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY all in place
- Screenshots in `src/img/`
- Plugin tested against the declared Grafana version range
- All security controls implemented (see SECURITY.md and administration.md)

**Security review preparation** — this is the part most likely to challenge the plugin. Reviewers will look at the proxy and ask hard questions. Prepare the following before submission:

- A written threat model document (`docs/architecture.md` should already contain this) explaining each risk and mitigation
- Evidence that the allowlist is empty by default and the plugin fails closed on a fresh install
- Evidence that the IP blocklist cannot be disabled by configuration
- Evidence of DNS-rebinding protection (resolve-once-then-dial-IP behaviour)
- Documentation of what request/response headers are stripped and why
- Rate limit and resource exhaustion controls with sensible defaults
- A clear statement that the plugin does not forward Grafana credentials, cookies, or authentication context
- Audit logging covering the data points a Grafana Labs SRE would want during an incident
- Honest documentation of limitations and known abuse vectors

Anticipated reviewer concerns and pre-prepared responses (include these in the submission cover letter):
- **"Could this be used as an open proxy?"** — Allowlist is empty by default, per-instance rate limits cap throughput, every request is audit-logged
- **"Could this be used for SSRF?"** — Hardcoded IP blocklist applied to resolved IPs, DNS rebinding protected, cloud metadata endpoints explicitly blocked, scheme/port restrictions enforced
- **"Could this bypass Grafana's egress controls?"** — Plugin makes requests through standard Go HTTP client which respects system-level controls; documents this as defence-in-depth not replacement
- **"What if a target site changes to malicious content?"** — Sandbox attribute on iframe limits damage; users opted-in to the domain via allowlist; plugin doesn't execute server-side
- **"Could header stripping enable clickjacking?"** — Document that proxy is opt-in per-domain and the admin's allowlist is the trust boundary

Submission process to the Grafana plugin catalog:
- Where to submit (confirm the current URL at submission time at https://grafana.com/legal/plugins)
- What the review team checks: security, quality, documentation, accessibility, plugin policy compliance
- Typical review timeline (weeks to months for plugins with novel attack surface)
- How to respond to feedback iteratively
- **Be prepared for a "no"**: Grafana Labs may decide the proxy aspect is incompatible with Cloud security posture. If that happens, document the outcome and continue with Path 1 (self-hosted via private signing). Do not attempt to work around a rejection.

Post-acceptance:
- How releases are published once the plugin is in the catalog (Grafana CDN serves the catalog version)
- Versioning expectations (semver, breaking changes need clear migration guidance)
- How to deprecate or transfer ownership
- Cloud rollout behaviour — installs are async, may take minutes to provision

Reference links:
- Grafana plugin publishing guide: https://grafana.com/developers/plugin-tools/publish-a-plugin/publish-a-plugin
- Plugin policy: https://grafana.com/legal/plugins
- Publishing best practices: https://grafana.com/developers/plugin-tools/publish-a-plugin/publishing-best-practices
- Plugin signature levels: https://grafana.com/docs/grafana/latest/administration/plugin-management/plugin-sign/

The agent should confirm the current submission URL and review process at submission time, as Grafana periodically updates these.

### Plugin-specific assets

- `src/img/logo.svg` — plugin logo (both light and dark variants if needed)
- `src/img/screenshot-*.png` — at least 3 screenshots showing key features
- `src/plugin.json` — fully populated metadata, including:
  - `info.description` — concise, accurate description
  - `info.author` — organization details
  - `info.keywords` — discoverability keywords (e.g. `["webview", "iframe", "proxy", "embed"]`)
  - `info.links` — links to documentation, source, license, issues
  - `info.screenshots` — references to images in `src/img/`
  - `info.version` and `info.updated` — kept in sync with releases
  - `dependencies.grafanaDependency` — supported Grafana version range
  - `backend: true`, `executable: <binary name>`

## Known Limitations to Document

- AJAX/fetch from proxied pages may fail due to CORS
- Authenticated sessions don't transfer (proxy is unauthenticated)
- Complex SPAs may not render correctly when proxied
- Service workers will not register in proxied pages
- WebSockets in proxied pages will fail
- Some sites detect and block proxy/headless access
- Frameability detection happens only at configuration time — if the target site later changes its headers, the dashboard must be re-saved to pick up the new mode (or the author can switch to Auto-with-override)

## Architecture Diagram

```
DEPLOYMENT PATHS:
┌──────────────────────────────────────────────────────────────┐
│ Path 1 (initial): Self-hosted Grafana, Private-signed plugin │
│   • Install via GF_INSTALL_PLUGINS or manual                 │
│   • Admin configures allowlist before use                    │
│                                                              │
│ Path 2 (goal):    Grafana Cloud via catalog (Community-signed)│
│   • Requires Grafana Labs security review approval           │
│   • Same plugin code, same security controls                 │
└──────────────────────────────────────────────────────────────┘

CONFIGURATION TIME (Panel Editor):
┌──────────────────────────────────────────────────────────────┐
│ Author opens panel editor                                    │
│   1. Enters URL                                              │
│   2. Clicks "Test URL"  ──► backend /check-frameable         │
│                              (subject to allowlist + IP      │
│                               blocklist + rate limit)        │
│                              returns {mode: "direct"|"proxy"}│
│   3. Optionally overrides mode                               │
│   4. Drags/zooms preview to position viewport                │
│   5. Saves dashboard ──► PanelOptions persisted              │
└──────────────────────────────────────────────────────────────┘

VIEW TIME (Dashboard Viewer):
┌──────────────────────────────────────────────────────────────┐
│ Viewer loads dashboard                                       │
│   Panel reads saved PanelOptions                             │
│   ├── loadMode: 'direct' ──► iframe src = target URL         │
│   └── loadMode: 'proxy'  ──► iframe src = /resources/proxy?  │
│                                                              │
│   CSS transform applied: scale(zoom) translate(-X, -Y)       │
│   No interactive controls                                    │
└──────────────────────────────────────────────────────────────┘

BACKEND (Go gRPC subprocess, every request goes through all layers):
┌──────────────────────────────────────────────────────────────┐
│ Grafana Backend Plugin (Go)                                  │
│                                                              │
│ Request enters → Security pipeline (in order):               │
│   1. URL scheme check (http/https only)                      │
│   2. Allowlist match (empty by default = deny)               │
│   3. Rate limit (per-instance + per-domain)                  │
│   4. Concurrent-request cap                                  │
│   5. DNS resolve → IP blocklist check                        │
│   6. Dial resolved IP directly (DNS rebinding protection)    │
│   7. Strip outgoing Cookie/Authorization/X-Grafana-*         │
│   8. Fetch with timeout + max body size                      │
│   9. Strip framing/cookie/HSTS response headers              │
│  10. (if HTML) goquery rewriting: base tag, URL rewriting,   │
│      frame-buster removal                                    │
│  11. Audit log + Prometheus metrics                          │
│                                                              │
│ Resource handlers:                                           │
│   /resources/check-frameable  — HEAD-style request           │
│   /resources/proxy             — main proxy via              │
│                                  httputil.ReverseProxy       │
│   /resources/proxy-resource    — subresource proxy           │
│   /resources/health            — health check                │
│                                                              │
│ All endpoints go through the SAME security pipeline above.   │
└──────────────────────────────────────────────────────────────┘
```

## Suggested Implementation Order

Security is **interleaved with feature work, not deferred to the end**. The goal is that at every milestone, the plugin is in a safe state — never "we'll add the allowlist later". The order below reflects that.

**Milestone 1 — Foundation:**
1. **Scaffold** plugin via `npx @grafana/create-plugin@latest` (App plugin, with backend)
2. **Repository hygiene** from day one — LICENSE, initial README, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY.md (even as stubs), `.github/` templates and workflows
3. **Plugin settings schema** for allowlist, rate limits, max body size, timeouts — with safe defaults baked in

**Milestone 2 — Direct mode works (no proxy yet):**
4. **Panel skeleton** with split between config mode and view mode rendering
5. **View-mode viewport** — CSS transform applied to iframe based on saved options
6. **Config-mode editor** — drag/zoom interaction in preview, "Capture current view" button
7. **Direct mode** end-to-end working — at this milestone, the plugin works without any backend proxy. This is shippable as a self-hosted private build for sites that allow framing.

**Milestone 3 — Secure foundation for the backend (before any proxy work):**
8. **IP blocklist library** — hardcoded ranges, IPv6 handling, link-local, metadata endpoints. Build this first with thorough unit tests.
9. **URL validator** — scheme check, port check, hostname normalization, IDN handling
10. **Allowlist matcher** — exact and subdomain matching, per-domain options structure
11. **DNS-resolve-then-dial helper** using `net.Dialer.Control` to prevent rebinding
12. **Rate limiter** — token bucket per Grafana instance and per target domain
13. **All of milestones 8–12 must have unit tests before any code that uses them is written.**

**Milestone 4 — Frameability detection:**
14. **Backend `check-frameable` endpoint** — uses the secure foundation above (allowlist, blocklist, rate limit all applied). HEAD request, parse `X-Frame-Options` and CSP.
15. **Frontend "Test URL" button** wired to `check-frameable`, stores result in options

**Milestone 5 — Proxy with header stripping:**
16. **Backend proxy** using `httputil.ReverseProxy` with the secure foundation already in place. `ModifyResponse` strips framing headers and dangerous response headers.
17. **Request header stripping** for `Cookie`, `Authorization`, `X-Grafana-*`
18. **Response size limiting** and timeout enforcement
19. **Audit logging** for every proxy request
20. **Prometheus metrics** for requests, denials, in-flight, duration

**Milestone 6 — Content rewriting:**
21. **HTML rewriting** using `goquery` — base tag, URL rewriting, frame-buster removal, CSP meta removal
22. **Subresource proxy** for CSS/JS/images on proxied pages — applies the same security stack
23. **Redirect handling** with allowlist re-check on every hop

**Milestone 7 — Tests and hardening:**
24. **Security test suite** — every acceptance criterion 17–31 has a dedicated test that runs in CI
25. **E2E tests** with `@grafana/plugin-e2e` — config flow, view-mode, security boundary cases
26. **CI/CD** — verify GitHub Actions workflows, add signing, add E2E matrix across Grafana versions

**Milestone 8 — Documentation and Path 1 release:**
27. **Documentation in `docs/`** — developer guide, installation, configuration, administration (with threat model), troubleshooting, architecture, publishing
28. **README polish** — hero screenshot, badges, quick-start, links to docs
29. **Example dashboard JSON** — demonstrating both direct and proxied URLs
30. **Private-signed v1.0 release** — Path 1 complete. Self-hosted users can install.

**Milestone 9 — Path 2 preparation:**
31. **Pre-submission validation** — run `@grafana/plugin-validator`, security audit against own threat model, verify all docs and assets are in place
32. **Submission cover letter** drafted addressing anticipated reviewer concerns (see `docs/publishing.md`)
33. **Submit to Grafana plugin catalog** for Community signing review
34. **Iterate on reviewer feedback** as needed. If approved, the plugin becomes installable on Grafana Cloud.

Documentation should be written incrementally alongside the code, not left to the end — the developer-guide.md and SECURITY.md in particular should be kept up to date as scaffolding, security foundation, and proxy work proceed.

## References

- Grafana plugin development portal: https://grafana.com/developers/plugin-tools/
- Plugin best practices: https://grafana.com/developers/plugin-tools/key-concepts/best-practices
- Publishing best practices: https://grafana.com/developers/plugin-tools/publish-a-plugin/publishing-best-practices
- Grafana plugin SDK for Go: https://github.com/grafana/grafana-plugin-sdk-go
- Plugin examples: https://github.com/grafana/grafana-plugin-examples
- Go `httputil.ReverseProxy`: https://pkg.go.dev/net/http/httputil#ReverseProxy
- goquery: https://github.com/PuerkitoBio/goquery

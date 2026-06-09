# Stream Decomposition

Streams are ordered by dependency. Status values: Not started / In progress / Done.

The scaffold already exists (App plugin `wilsonwaters-webview-app`, Go backend with
`httpadapter` + `ServeMux`, example pages, CI/release/e2e workflows, dependabot). Feature
work is **paused pending stakeholder go-ahead**; these streams are the plan, not yet
dispatched.

## Dependency overview

```
foundation
  ├── panel-core ──────────────┐
  │     └── frameability        │
  ├── security-foundation       │
  │     └── frameability ──┐    │
  │           └── proxy ───┴── content-rewriting
  │                  └── direct-only-fallback (depends on panel-core + frameability)
  └── (all) ── testing-cicd ── docs-release ── catalog-prep
```

- `panel-core` and `security-foundation` can run **in parallel** after `foundation`.
- `frameability` needs the backend secure foundation (allowlist/blocklist/rate-limit) and
  the panel editor skeleton.
- `direct-only-fallback` needs `panel-core` (rendering) and `frameability` (the health/
  availability detection it disables on).
- `testing-cicd` runs continuously but its dedicated hardening tasks land after `proxy` and
  `content-rewriting`.
- `docs-release` is written incrementally but its release task gates on everything.
- `catalog-prep` is last and its submission task is deferred.

## Streams

### 1. Foundation & Repo Hygiene  —  `foundation`
**Outcome:** Repo metadata, settings schema with safe defaults, and panel registration
scaffold are in place so all later work has a stable base.
**Capabilities:** repo hygiene files (LICENSE/CHANGELOG/CONTRIBUTING/CODE_OF_CONDUCT/
SECURITY stub, issue/PR templates), plugin settings schema for allowlist/limits, nested
Web View panel registration, shared TypeScript options type and Go config type.
**Depends on:** scaffold (done).
**Size:** M.
**Status:** ✅ Done — F1–F4 merged (#1, #72, #73, #74).

### 2. Panel Core & Direct Mode  —  `panel-core`
**Outcome:** A working Web View panel that renders a direct-iframe URL with a configured
viewport and an interactive config-mode editor — shippable for framable sites with no proxy.
**Capabilities:** config/view mode split, CSS-transform viewport, drag-pan + wheel-zoom
editor, capture-view, numeric inputs, virtual dimensions, refresh, hide-selectors,
multi-instance.
**Depends on:** foundation.
**Size:** L.
**Status:** ✅ Done — PC1–PC5 merged (#75, #76, #77, #79, #80). Direct-mode panel shippable;
verified in Grafana 12.3.6/12.4.3/13.0.1/nightly e2e. (AC 6 reconciled to live-capture;
hide-selectors deferred to proxy/CR5.)

### 3. Security Foundation (backend libraries)  —  `security-foundation`
**Outcome:** Audited, unit-tested Go building blocks (IP blocklist, URL validator,
allowlist matcher, rebinding-safe dialler, rate limiter) that every proxying endpoint will
use — built and tested before any endpoint consumes them.
**Capabilities:** hardcoded IP blocklist, scheme/port/URL validation, allowlist matching
with per-domain options, DNS-resolve-then-dial helper, token-bucket rate limiter +
concurrency cap.
**Depends on:** foundation.
**Size:** L.
**Status:** ✅ Done — SF1–SF5 merged (#81, #82, #83, #84, #85). `pkg/security/` is a dependency-free
leaf package: IP blocklist, URL validator, allowlist matcher, DNS-resolve-then-dial (rebind-safe),
rate limiter + concurrency cap. All fail-closed, unit-tested (94–99% coverage), race-clean. No
endpoint consumes them yet — the frameability/proxy endpoints map `plugin.AllowedDomain →
security.AllowlistEntry` at the call site to preserve the leaf boundary.

### 4. Frameability Detection  —  `frameability`
**Outcome:** Author can click "Test URL" and get a Direct/Proxied/Error result, persisted to
panel options, with the same security gates as the proxy.
**Capabilities:** `/check-frameable` backend endpoint, frontend "Test URL" button + result
display + persistence, backend-availability awareness via `/health`.
**Depends on:** security-foundation, panel-core.
**Size:** M.
**Status:** ✅ Done — FR1–FR4 merged (#99, #98, #100, #101). `/check-frameable` (full pipeline + SF4
transport, SSRF-safe, Q7 contract) + `/health` (bare liveness); frontend Test-URL button (calls
/check-frameable, shows Direct/Proxied/Error, persists detectedMode); load-mode resolution + view-mode
wiring (proxy mode → iframe src `${appSubUrl}/api/plugins/wilsonwaters-webview-app/resources/proxy?url=…`
+ `hide=` params for CR5). **The full path is usable in-panel:** detect → choose mode → render via proxy.

### 5. Backend Proxy & Hardening  —  `proxy`
**Outcome:** A security-hardened `/proxy` endpoint that fetches a page, strips framing and
dangerous headers, and enforces every request/response control, with audit logs and metrics.
**Capabilities:** `httputil.ReverseProxy` with secure director/dialler, response & request
header stripping, size/timeout limits, audit logging, Prometheus metrics.
**Depends on:** security-foundation (frameability helpful but not blocking).
**Size:** L.
**Status:** ✅ Done — P1–P7 merged (#86, #87, #88, #89, #90, #91, #92). `/proxy?url=<encoded>` via
`httputil.ReverseProxy`: security pipeline in the handler (SF3 allowlist → SF2 validate → SF5
rate-limit/concurrency) + SF4 resolve-then-dial transport (rebind-safe); target rebuilt from validated
components (parser-differential-SSRF-safe). Strips framing (X-Frame-Options, CSP frame-ancestors),
outgoing identity/auth headers (incl. edge/CDN), and incoming Set-Cookie/HSTS/HPKP/Clear-Site-Data;
enforces body-size (413) + total timeout (504); structured audit log + Prometheus metrics
(requests/denials/in-flight/duration) on the SDK `/metrics`; single reasonStatus table wires every
denial→(status, reason). NOT yet wired: HTML body rewriting / subresource proxying (content-rewriting),
and the frontend Proxy load-mode selector (frameability FR4). The backend `/proxy` is testable via
direct HTTP today; a framing-blocked site only RENDERS fully after content-rewriting.

### 6. Content Rewriting & Subresources  —  `content-rewriting`
**Outcome:** Proxied HTML pages render correctly — relative URLs resolve, subresources load
through the proxy, frame-busters and CSP meta removed, redirects re-validated.
**Capabilities:** goquery HTML rewriting (base tag, src/href rewrite, frame-buster removal,
CSP meta removal), `/proxy-resource` subresource endpoint, redirect handling with per-hop
allowlist re-check, gzip handling.
**Depends on:** proxy.
**Size:** L.
**Status:** ✅ Done — CR1–CR5 merged (#93, #94, #95, #96, #97). Proxied HTML now renders: CR1 gzip-decode +
HTML detection (size-bounded, Accept-Encoding pinned); CR2 goquery rewrite (subresource→/proxy-resource,
nav→/proxy, base-href, frame-buster + CSP/refresh-meta removal, XSS-safe); CR3 /proxy-resource endpoint
(same pipeline, Content-Type preserved, size-limited); CR4 redirect handling (Location→proxy URL, _wvredir
depth cap, per-hop allowlist+IP re-validation, no raw Location escapes); CR5 hide-selectors (goquery inline
display:none, cascadia-validated, markup-injection-proof). The proxy can fully render a framing-blocked page
end-to-end. NOT yet wired: the frontend Proxy load-mode selector (frameability FR4) so a panel points its
iframe at /proxy?url=… in-panel.

### 7. Direct-Only Fallback / Graceful Degradation  —  `direct-only-fallback`
**Outcome:** When the backend is unavailable, the panel is still creatable in
direct-iframe-only mode with proxy features cleanly disabled and explained in the UI.
**Capabilities:** backend-availability detection via `/health`, editor degradation (disable
proxy mode + Test URL, show explanatory state), view-mode guard against a proxy-mode config
running with no backend.
**Depends on:** panel-core, frameability.
**Size:** M.
**Status:** ✅ Done — DF1–DF3 merged (#107, #108, #109). `useBackendAvailable` hook (probe `/health` once,
module-scoped shared cache, fail-safe per Q12); editor degradation (load-mode selector → custom
`LoadModeEditor` that omits Auto/Proxy + disables Test URL + shows a note when backend unavailable;
display-only clamp); view-mode guard (proxy + no backend ⇒ accessible fallback, no broken iframe; direct
mode never waits on the probe). The plugin now degrades cleanly to direct-only when no backend is present.

### 8. Testing & CI/CD  —  `testing-cicd`
**Outcome:** A non-skippable security suite, full unit/E2E coverage, and CI/release/E2E-matrix
workflows verified and signing wired in.
**Capabilities:** security test suite mapping AC 17–31, frontend/backend unit tests, Playwright
E2E for config/view/security boundaries, CI verification, signing in release, Grafana-version
E2E matrix, `@grafana/plugin-validator` gate.
**Depends on:** proxy, content-rewriting (security tests); runs continuously otherwise.
**Size:** L.
**Status:** Not started.

### 9. Documentation & Release (Path 1)  —  `docs-release`
**Outcome:** Complete repo + `docs/` documentation, example dashboard, and a private-signed
v1.0 release for self-hosted users.
**Capabilities:** developer/installation/configuration/administration/troubleshooting/
architecture/publishing docs, written threat-model document, README polish, example dashboard
JSON, private signing + GitHub release.
**Depends on:** all feature streams (release task); docs written incrementally.
**Size:** L.
**Status:** Not started.

### 10. Path 2 Catalog Preparation  —  `catalog-prep`
**Outcome:** Submission materials and a self-audit are prepared for Community signing; actual
submission is deferred pending stakeholder go-ahead.
**Capabilities:** `@grafana/plugin-validator` pre-submission run, self security audit against
the threat model, submission cover letter, deferred submission + feedback-iteration task.
**Depends on:** docs-release.
**Size:** M.
**Status:** Not started.

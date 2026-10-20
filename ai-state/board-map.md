# Board Map — Task → GitHub Issue

| Task | Issue # | Title | Stream | Size | Status |
|------|---------|-------|--------|------|--------|
| F1 | #1 | Repo hygiene files | foundation | S | done |
| F2 | #11 | Define shared panel options type (TS + Go) | foundation | M | done |
| F3 | #12 | Define plugin settings schema with safe defaults | foundation | M | done |
| F4 | #13 | Register nested Web View panel inside the app plugin | foundation | M | done |
| PC1 | #14 | View-mode iframe render with CSS transform viewport | panel-core | M | done |
| PC2 | #15 | Viewport transform math helpers and unit tests | panel-core | M | done |
| PC3 | #16 | Config-mode editor — drag-pan and wheel-zoom interactive preview | panel-core | M | done |
| PC4 | #17 | Config-mode editor — capture view and manual inputs | panel-core | M | done |
| PC5 | #18 | View-mode behaviours: refresh, hide-selectors, debug overlay, multi-instance | panel-core | M | done |
| SF1 | #19 | IP blocklist library — classify private/reserved/special-use ranges | security-foundation | M | done |
| SF2 | #20 | URL validator — scheme allowlist, port restriction, hostname normalisation | security-foundation | M | done |
| SF3 | #21 | Allowlist matcher — exact/subdomain matching with per-domain options | security-foundation | M | done |
| SF4 | #22 | DNS-resolve-then-dial helper — validate resolved IP before connecting | security-foundation | M | done |
| SF5 | #23 | Rate limiter and concurrency cap with configurable defaults | security-foundation | M | done |
| FR1 | #24 | Backend /check-frameable endpoint through full security pipeline | frameability | M | done |
| FR2 | #25 | Backend /health liveness endpoint | frameability | S | done |
| FR3 | #26 | Frontend "Test URL" button with result display and persistence | frameability | M | done |
| FR4 | #27 | Load-mode selector — Auto / Direct / Proxy with view-mode wiring | frameability | M | done |
| FR5 | #102 | In-panel Proxy render blocked by Grafana resource-route XFO/CSP — fetch-then-srcdoc | frameability | M | blocked (CRITICAL follow-up; see ai-state/Q17-proxy-render-headers.md) |
| P1 | #28 | Core proxy slice — /proxy endpoint with security pipeline and framing-header removal | proxy | M | done |
| P2 | #29 | Outgoing request header stripping — remove auth and Grafana headers | proxy | M | done |
| P3 | #30 | Incoming response header stripping — Set-Cookie, HSTS, HPKP, Clear-Site-Data | proxy | S | done |
| P4 | #31 | Resource limits — max body size, connection and total timeout | proxy | M | done |
| P5 | #32 | Audit logging — structured per-request log for proxy requests | proxy | M | done |
| P6 | #33 | Prometheus metrics — request counters, denial reasons, in-flight gauge, duration histogram | proxy | M | done |
| P7 | #34 | Rate-limit and denial HTTP response wiring with metrics reasons | proxy | S | done |
| CR1 | #35 | gzip handling and HTML detection in ModifyResponse | content-rewriting | M | done |
| CR2 | #36 | goquery HTML rewriting — base href, URL rewrite, frame-buster and CSP meta removal | content-rewriting | L | done |
| CR3 | #37 | /proxy-resource subresource endpoint through full security pipeline | content-rewriting | M | done |
| CR4 | #38 | Redirect handling — cap depth, re-validate each hop, rewrite Location headers | content-rewriting | M | done |
| CR5 | #39 | Hide-selector application to proxied HTML with CSS selector safety validation | content-rewriting | M | done |
| DF1 | #40 | Backend-availability detection hook with caching | direct-only-fallback | M | done |
| DF2 | #41 | Editor degradation — disable proxy mode and Test URL when backend unavailable | direct-only-fallback | M | done |
| DF3 | #42 | View-mode guard — fallback state when proxy config has no available backend | direct-only-fallback | M | done |
| TC1 | #43 | Security test suite — SSRF, blocklist, allowlist, scheme validation (AC 17–22) | testing-cicd | L | done |
| TC2 | #44 | Security test suite — limits, header stripping, redirects, logging, metrics (AC 23–29) | testing-cicd | L | done |
| TC3 | #45 | Frontend unit and component test coverage | testing-cicd | M | done |
| TC4 | #46 | E2E suite with @grafana/plugin-e2e and Playwright | testing-cicd | L | done |
| TC5 | #47 | CI verification and non-skippable security gate | testing-cicd | M | done |
| TC6 | #48 | Release workflow, signing, plugin-validator gate, and E2E Grafana-version matrix | testing-cicd | M | done |
| — | #124 | ci: Levitate forward-compat check made advisory (TEMPORARY — revert at 13.x bump) | testing-cicd | S | in-review (PR open) |
| DR1 | #49 | Flesh out repo docs — README, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT | docs-release | M | done |
| DR2 | #50 | Developer guide — setup, workflow, testing, debugging | docs-release | M | done |
| DR3 | #51 | Configuration guide for dashboard authors | docs-release | M | done |
| DR4 | #52 | Administration guide — allowlist, rate limits, audit logging, metrics, incident response | docs-release | L | done |
| DR5 | #53 | Threat model, SECURITY.md content, architecture docs, and backend handler reference | docs-release | L | done |
| DR6 | #54 | Installation and troubleshooting docs | docs-release | M | done |
| DR7 | #55 | README polish and example dashboard JSON | docs-release | M | done |
| DR8 | #56 | Private-signed v1.0 release (Path 1 complete) | docs-release | M | handed off to another agent (→1.0.0): release workflow fixed; blocked on osv-scanner CVE gate = #131 dep bump |
| CP1 | #57 | Pre-submission plugin-validator run and metadata completeness check | catalog-prep | M | blocked |
| CP2 | #58 | Self security audit against the threat model with evidence checklist | catalog-prep | M | blocked |
| CP3 | #59 | Submission cover letter — docs/publishing.md | catalog-prep | M | blocked |
| CP4 | #60 | Deferred catalog submission and feedback iteration (PAUSED) | catalog-prep | M | blocked |
| — | #105 | Per-domain `AllowPrivateIP` opt-in wired through (scoped RFC1918 relaxation) | security-foundation | M | done |
| — | #113 | [investigate] Provisioned jsonData.allowedDomains may not reach the backend (TC4 success-path skips) | testing-cicd | S | blocked (investigate; relevant to DR4/DR7) |
| — | #129 | [gap] No AppConfig UI to manage the domain allowlist | panel-core | M | blocked (release-readiness decision) |
| — | #131 | [13.x compat] Bump @grafana deps for 13.2+ (e2e harness + revert Levitate advisory #124) | testing-cicd | M | blocked (deferred to next cycle) |

## v1.0.0 stabilisation (2026-10-18)

| Task | Issue # | Title | Stream | Size |
|------|---------|-------|--------|------|
| R1 | #138 | Research spike: in-panel proxy render on stock Grafana | v1-stabilisation | L |
| M1 | #139 | Scaffold, dependency and CI update | v1-stabilisation | L |
| RV1/RV2 | #140 | Standards review and fixes | v1-stabilisation | M |
| S1 | #141 | Frontend security: VR-1, VR-2 | v1-stabilisation | M |
| S2 | #142 | Backend security: VR-3/5/6/7/8/11 | v1-stabilisation | M |
| GH1 | #143 | Repository hygiene | v1-stabilisation | S |
| DEV1 | #144 | Dev server Grafana upgrade + plugin install | v1-stabilisation | M |
| DOC1 | #145 | Docs refresh for v1.0.0 | v1-stabilisation | M |
| FR5 | #102 | In-panel proxy render (fetch + srcdoc) | v1-stabilisation | L |
| AU1 | #129 | Settings page for allowlist and limits (+ #113, VR-4, VR-13) | v1-stabilisation | L |
| REL1 | #56 | v1.0.0 release (private-signed) | v1-stabilisation | M |

PR: #146 (`release/v1.0.0` → `main`).

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
| FR1 | #24 | Backend /check-frameable endpoint through full security pipeline | frameability | M | in-progress |
| FR2 | #25 | Backend /health liveness endpoint | frameability | S | done |
| FR3 | #26 | Frontend "Test URL" button with result display and persistence | frameability | M | blocked (FR1) |
| FR4 | #27 | Load-mode selector — Auto / Direct / Proxy with view-mode wiring | frameability | M | blocked (FR3) |
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
| DF1 | #40 | Backend-availability detection hook with caching | direct-only-fallback | M | blocked |
| DF2 | #41 | Editor degradation — disable proxy mode and Test URL when backend unavailable | direct-only-fallback | M | blocked |
| DF3 | #42 | View-mode guard — fallback state when proxy config has no available backend | direct-only-fallback | M | blocked |
| TC1 | #43 | Security test suite — SSRF, blocklist, allowlist, scheme validation (AC 17–22) | testing-cicd | L | blocked |
| TC2 | #44 | Security test suite — limits, header stripping, redirects, logging, metrics (AC 23–29) | testing-cicd | L | blocked |
| TC3 | #45 | Frontend unit and component test coverage | testing-cicd | M | blocked |
| TC4 | #46 | E2E suite with @grafana/plugin-e2e and Playwright | testing-cicd | L | blocked |
| TC5 | #47 | CI verification and non-skippable security gate | testing-cicd | M | blocked |
| TC6 | #48 | Release workflow, signing, plugin-validator gate, and E2E Grafana-version matrix | testing-cicd | M | blocked |
| DR1 | #49 | Flesh out repo docs — README, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT | docs-release | M | ready |
| DR2 | #50 | Developer guide — setup, workflow, testing, debugging | docs-release | M | ready |
| DR3 | #51 | Configuration guide for dashboard authors | docs-release | M | blocked |
| DR4 | #52 | Administration guide — allowlist, rate limits, audit logging, metrics, incident response | docs-release | L | blocked |
| DR5 | #53 | Threat model, SECURITY.md content, architecture docs, and backend handler reference | docs-release | L | blocked |
| DR6 | #54 | Installation and troubleshooting docs | docs-release | M | blocked |
| DR7 | #55 | README polish and example dashboard JSON | docs-release | M | blocked |
| DR8 | #56 | Private-signed v1.0 release (Path 1 complete) | docs-release | M | blocked |
| CP1 | #57 | Pre-submission plugin-validator run and metadata completeness check | catalog-prep | M | blocked |
| CP2 | #58 | Self security audit against the threat model with evidence checklist | catalog-prep | M | blocked |
| CP3 | #59 | Submission cover letter — docs/publishing.md | catalog-prep | M | blocked |
| CP4 | #60 | Deferred catalog submission and feedback iteration (PAUSED) | catalog-prep | M | blocked |

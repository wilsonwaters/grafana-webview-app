# PROGRESS

Narrative log of project status, maintained primarily by the orchestrator agent.

## Status summary

Setup complete and the **execution loop is running** (task branch → PR → review → CI-green →
squash-merge into `main`). **foundation (F1–F4), panel-core (PC1–PC5), security-foundation (SF1–SF5),
the backend PROXY stream (P1–P7), CONTENT-REWRITING (CR1–CR5), and FRAMEABILITY (FR1–FR4) are all DONE.** A security-hardened
`/proxy?url=` endpoint runs the full pipeline (allowlist→validate→rate-limit→resolve-then-dial), strips
framing + identity + dangerous response headers, enforces body-size/timeout, emits audit logs + Prometheus
metrics, AND now fully RENDERS a framing-blocked page: goquery HTML rewrite, `/proxy-resource` subresources,
safe redirect re-validation, and hide-selectors. Frameability (FR1–FR4) wires the panel end-to-end:
Test-URL detection → Auto/Direct/Proxy selector → view-mode renders via `/proxy?url=…` in proxy mode.
The plugin is also still a **shippable direct-mode Web View
panel** today: sandboxed iframe at a configured viewport, interactive editor (drag-pan/wheel-zoom +
numeric inputs/reset), auto-refresh, debug overlay, multi-instance — e2e-verified across Grafana
12.3.6/12.4.3/13.0.1/nightly and privately signed. The backend now has a complete set of audited,
unit-tested security building blocks in `pkg/security/` (IP blocklist, URL validator, allowlist
matcher, DNS-resolve-then-dial, rate limiter) — a **dependency-free leaf package** consumed by the
proxy + frameability endpoints. **The framing-blocked-site path (e.g. BOM radar) is feature-complete
end-to-end**; remaining streams are direct-only-fallback, testing-cicd, and docs-release/catalog-prep.

## Handoff notes for the next orchestrator (updated 2026-06-03)

**Resume point:** foundation, panel-core, security-foundation, proxy (P1–P7), content-rewriting (CR1–CR5),
and frameability (FR1–FR4) are ALL merged. The backend proxy is feature-complete + runtime-verified (renders
real pages live). **Recommended next stream: `testing-cicd`** (TC1/TC2 = the non-skippable security suite
mapping AC 17–31 — highest value, Q17-independent), then `direct-only-fallback` (DF1–DF3, now unblocked by
frameability), then `docs-release`. **CRITICAL open item: FR5/#102 (Q17)** — in-panel PROXY render is blocked
by Grafana's resource-route `X-Frame-Options: deny` + `CSP: sandbox`; fix = fetch-then-srcdoc; FULL brief in
`ai-state/Q17-proxy-render-headers.md` (the stakeholder may resolve this separately with another agent).
Everything needed to resume is in `ai-state/` — read `brief.md`, this file, `streams.md`, `board-map.md`,
`OPEN-QUESTIONS.md`, the relevant `streams/<name>/master-plan.md`, and `Q17-proxy-render-headers.md`. Then
`mcp__github__list_issues` (label `status:ready`). Quirks that are NOT obvious from the code:

- **Always check real GitHub CI on each PR** (`pull_request_read get_check_runs`), not just local
  gates — that is how the signing/compat breakages (#78) were caught. CI signs privately via the
  `QA-Alintech` env (no blocking approval rule; PR CI runs unattended).
- **plugin-e2e `setVisualization` is flaky** — run e2e/Playwright with `--retries=2` (or a direct
  navigation flow); it typically passes on retry.
- **Benign dev-Grafana log noise** to ignore during runtime verification: TLS cert failures
  reaching `grafana.com` (sandbox has no outbound to it) and "missing provisioning directory"
  warnings. Use `https://example.com` as the framable test URL. Real plugin errors look different.
- **Dev env**: mage at `/root/go/bin/mage`; add `$(go env GOPATH)/bin` to PATH. Start Docker if
  down: `sudo bash -c 'setsid dockerd >/var/log/dockerd.log 2>&1 </dev/null &'` then
  `docker compose up -d --build`; wait for `:3000/api/health`. Container: `wilsonwaters-webview-app`.
- **Tracked debt** (non-blocking, fix opportunistically): (a) `VIEWPORT_ZOOM_MIN/MAX` duplicated
  in `types.ts` (private) and `viewport.ts` (exported) — centralise; (b) `normalizeOptions` in
  `src/types.ts` rejects negative `viewportX/Y`, but cursor-anchored pan can legitimately produce
  small negatives — allow negative X/Y in a future cleanup; (c) PC5 test `iframe is remounted` has
  a weak assertion (the reload mechanism is covered by sibling tests).
- **Resolved design decisions** (don't relitigate): Q1–Q4, Q14 in `OPEN-QUESTIONS.md` (nested-panel
  packaging, demo-page removal, custom-options-editor instead of edit-mode detection, debug-overlay
  vs hide-selectors, private signing). AC 6 ("Capture current view") was intentionally reconciled
  to live-capture (no button) — see panel-core master-plan changelog.

## Currently in flight

- **CURRENT STATE (2026-06-09):** This session merged TC1, TC2, TC5 (security suite + non-skippable CI gate),
  DF1–DF3 (**direct-only-fallback COMPLETE**), TC3 (frontend coverage), and **#105 (AllowPrivateIP wire-through —
  MERGED, dual-reviewed incl. adversarial security)**. System-verification = SYSVERIFY-OK (see below).
  TC4 (#46 e2e) MERGED. **testing-cicd: TC1/TC2/TC3/TC4/TC5 all DONE; only TC6 (#48) remains** — release
  workflow + signing + plugin-validator gate + the E2E Grafana-version matrix. **TC6 is GATED on Q13b**
  (whether the e2e matrix spans the full `>=12.3.0` range or a pinned subset) — a stakeholder cost/coverage
  decision → checking in. After TC6: **docs-release** (DR1/DR2 ready now; DR4 should fold in #113 + the #105
  re-opened-surface + the FR5 known-limitation). Tracked follow-ups: #102 (FR5, deferred), #113 (provisioning
  delivery), check-frameable permit-audit parity, ULA relaxation.
- **testing-cicd (2026-06-09).** TC1 (#43) + TC2 (#44) MERGED — the non-skippable backend
  security suite (AC 17–29) is COMPLETE. Resolved Q13a (DNS-rebinding-in-CI = injected stub
  `security.Resolver`, hermetic, NO production change). Both were backend-only/test-only ⇒ e2e unaffected;
  merged once build/lint/test + compatibility green (each branch updated to current `main` first so
  golangci-lint v2 lints the combined package).
- **TC5 (#47) MERGED (2026-06-09)** — non-skippable CI security gate (AC 36): a dedicated unconditional
  `security-suite` job (its own status check, ran green on its own PR) + `scripts/security-suite.sh`
  guard that fails if any AC 17–29 test is missing/renamed/skipped/not-PASS. Review APPROVE (fail-closed
  verified across removed/renamed/skip/no-Magefile negative tests). Note: making it a *required* check is
  a repo branch-protection setting (admin action) — flagged, not yet done. `.gitignore` has an unanchored
  `ci/` rule, so the script lives at `scripts/security-suite.sh` (NOT `scripts/ci/`).
- **Remaining testing-cicd readiness (decision point):** **TC3 (#45 frontend tests)
  is partly gated on direct-only-fallback**; **TC4 (#46 E2E) is entangled with FR5/Q17** (in-panel proxy
  render still blocked by Grafana resource-route XFO/CSP — do NOT assert in-panel proxy render in e2e
  until FR5 lands); **TC6 (#48) is gated on Q13b (e2e-matrix scope) + TC4/TC5.** After TC5 the natural
  pivot is **direct-only-fallback (DF1–DF3)** which unblocks TC3. **WARNING to self:** never
  `git reset --hard` with uncommitted ai-state edits in the tree (bit me twice this session) — commit first.
- **STAKEHOLDER DECISIONS (2026-06-09 check-in):**
  1. **Next stream = direct-only-fallback (DF1–DF3).** Dispatching DF1 (#40, backend-availability hook).
     Q12 resolved (fixed-per-session, module-scoped shared `/health` probe; fail-safe true-only-on-200+ok).
  2. **FR5/#102 = DEFERRED INDEFINITELY for v1.** In-panel proxy render is a documented v1 KNOWN LIMITATION
     (backend `/proxy` is complete+verified; only in-panel rendering deferred). NOT a release blocker. TC4
     skips in-panel-proxy e2e. Must be documented in DR3/DR6/README. #102 stays open (post-v1).
  3. **#105/Q18 AllowPrivateIP = WIRE IT THROUGH.** Thread per-domain opt-in into the dial path, relaxing
     SF1 ONLY for opted-in allowlisted domains, scoped to private/RFC1918 (loopback/link-local/metadata stay
     hard-blocked — confirm in design), distinct audit logging, dedicated tests, admin+threat-model docs.
     Security-critical → needs design + adversarial security review. #105 now `status:ready`; sequenced
     AFTER direct-only-fallback.
- Live render test DONE (PROXY-RENDER-OK; see "Runtime render test + KEY finding" below). The
  backend proxy + content-rewriting + frameability are all merged and runtime-verified.
- **Stakeholder decisions (2026-06-03):**
  1. **Q17 (in-panel proxy render blocked by Grafana resource-route XFO/CSP) = tracked CRITICAL follow-up,
     NOT fixed now.** GitHub issue **#102 [FR5]**; standalone brief `ai-state/Q17-proxy-render-headers.md`
     (written so the stakeholder can resolve it with a separate agent). Fix = fetch-then-srcdoc (+ a security
     decision on the srcdoc `sandbox` flags — must NOT use `allow-same-origin`).
  2. **Orchestrator handoff** to a fresh agent is intended (this session's context is very large). ai-state
     is being brought fully current for that handoff (this commit). The next orchestrator resumes per the
     Handoff notes above.
- **Recommended next stream: `testing-cicd`** (security suite AC 17–31 + e2e) — highest value, independent
  of Q17. Alternatives: `direct-only-fallback` (now unblocked), `docs-release`.
- **Streams remaining:** frameability FR5/#102 (Q17 fix), direct-only-fallback (DF1–DF3), testing-cicd
  (TC1–TC6), docs-release (DR1–DR8), catalog-prep (CP1–CP4).

## System verification (stream boundary, 2026-06-09) — SYSVERIFY-OK

After direct-only-fallback completed (6 feature merges this session), a system-verification pass on
integrated `main` was **ALL PASS, no regressions**: (1) frontend `npm ci`/typecheck/lint clean, `test:ci`
160/160; (2) backend `go test ./... -race` all ok + `mage build:backend`/`build:linux` produce
`gpx_webview_linux_amd64`; (3) `scripts/security-suite.sh` exit 0 (all AC 17–29 PASS); (4) dev Grafana 12.4.0
loads app + child `wilsonwaters-webview-panel`, backend process up, no plugin errors; (5) live endpoints:
`/resources/health`→200 `{"status":"ok"}`, `/metrics`→`webview_*` families, `/resources/proxy?url=http://example.com/`
(empty allowlist)→**403**, `file://`→**400**, `/resources/check-frameable?url=file://`→**400**. Security
pipeline intact end-to-end on the loaded plugin. Dev env LEFT RUNNING (container `wilsonwaters-webview-app`,
:3000 anon). Quirks: docker was down (restarted); mage was not at `/root/go/bin/mage` (re-installed via `go install`).

## Parallel execution (updated this session)

The earlier "serial, single shared working tree" constraint is **obsolete**. Sub-agents are now
dispatched with `isolation: worktree` (each gets its own git worktree off the shared `.git`), so
multiple independent impl/review tasks run concurrently without colliding. This session ran SF4+SF5
impl in parallel plus reviews/fixes. RULES that bit us and are now standing practice:
- Do NOT run orchestrator git writes in the PRIMARY working tree while a non-isolated agent is on it
  (the first SF2 impl agent raced our `main` commit). Always use `isolation: worktree` for agents,
  and the orchestrator's own bookkeeping commits go to `main` in the primary tree.
- **golangci-lint is BROKEN in this dev sandbox (go-version mismatch) but ENFORCED in CI.** Backend
  lint issues (errcheck/staticcheck) only surface in CI — SF4 tripped 5 errcheck + 1 staticcheck that
  local `go vet` missed. ALWAYS let CI's "Build, lint and unit tests" job go green before merging a
  backend PR, and **update the PR branch to current `main` first** so CI builds/lints the COMBINED
  package (this caught the SF4 lint failure that the standalone-branch CI would have shown only for
  SF4's own files). Commit-signing also fails from a `/tmp` worktree ("missing source") — do
  orchestrator commits from the primary repo dir.

## Runtime verification (proxy, 2026-06-02)

First live exercise of `/proxy` in Grafana 12.4.0 (dev env) after the proxy stream completed — **PROXY-RUNTIME-OK**:
- Plugin builds (`mage -v build:linux` + `npm ci && npm run build`), loads (unsigned), resource routes serve under
  `/api/plugins/wilsonwaters-webview-app/resources/proxy?url=`. Anonymous auth suffices for resource calls.
- Probe matrix correct: non-allowlisted→403, bad scheme→400, missing url→400, metadata IP→403 (denied at the
  allowlist stage), CORS present. Structured audit log emits one entry/request (url/user/status/bytes/duration).
- **Prometheus metrics are LIVE + accurate** at `/api/plugins/wilsonwaters-webview-app/metrics` (NOT `…/resources/metrics`):
  `webview_proxy_requests_total{status}`, `denials_total{reason}`, `requests_in_flight`, `request_duration_seconds`.
- No panics/real errors. Dev env LEFT RUNNING (container `wilsonwaters-webview-app` Up) for content-rewriting tests.
- **Env limitation (NOT a code defect):** the sandbox container has no untampered outbound HTTPS (TLS-intercepting
  proxy), so a real allowlisted fetch returns 502 `upstream request failed`. A live 200 + `X-Frame-Options`/CSP
  `frame-ancestors` stripping could NOT be observed at runtime (it IS unit-tested). To verify live: use a plain-HTTP
  framable upstream reachable from the container, supply the interception CA, or test in the real target network.
- Quirk: dev `node_modules` was corrupt; `rm -rf node_modules && npm ci` fixed the frontend build.

PR runtime screenshots must be committed to `docs/screenshots/issue-<N>/` and embedded via raw
GitHub URLs in the PR body (a bare `/tmp/...` path is invisible to reviewers). Codified in
`.claude/agents/orchestrator.md`. Backfilled #74/#75/#77/#79/#80 with inline screenshot comments;
key shots committed under `docs/screenshots/`.

## Runtime render test + KEY finding (2026-06-03)

**PROXY-RENDER-OK** — live in dev Grafana, the proxy FETCHED + REWROTE + RENDERED a real reachable page
(`http://neverssl.com`, plain-HTTP to bypass the sandbox HTTPS MITM): HTTP 200, `<base href>` injected,
audit log (`status=200 bytes=3994`) + metrics (`webview_proxy_requests_total{status="200"}`) confirmed,
and a browser screenshot (`docs/screenshots/runtime/proxy-render-neverssl.png`) shows the fully-styled page.
This is the live end-to-end backend evidence the earlier HTTPS smoke test (502) couldn't get. CR1–CR5 work.

**⚠️ KEY FINDING — Grafana stamps `X-Frame-Options: deny` + `Content-Security-Policy: sandbox` on ALL
`/api/plugins/*/resources/*` routes** (observed on `/resources/health` too; framework-injected, NOT our
code — our `stripFramingHeaders` correctly Del's the UPSTREAM XFO). Consequence for FR4's approach
(WebViewPanel points the iframe `src` directly at `/resources/proxy?url=…`): in a real Grafana the panel
iframe will likely be **blocked from framing** (XFO:deny, even same-origin) and proxied **JS won't run**
(CSP:sandbox) — the screenshot's "JavaScript appears to be disabled" banner is exactly this. So in-panel
PROXY mode is not yet functional even though the backend pipeline is fully working. Direct mode is
unaffected (frames the external URL cross-origin, not a resource route).
**Recommended fix:** render proxy mode via fetch-then-`srcdoc` — `getBackendSrv().get()` the rewritten
HTML (fetch is NOT subject to XFO), set iframe `srcdoc`; subresources (`/proxy-resource`) load as normal
subresource requests (XFO/CSP-sandbox don't apply to img/script/link). **Security nuance to design/review:**
the iframe `sandbox` flags for srcdoc — `allow-same-origin` on srcdoc inherits the GRAFANA origin (XSS/
escalation risk for attacker-influenced proxied content), so it must NOT be set the way direct-mode does.
Also config-dependent: Grafana `allow_embedding` affects XFO. Tracked as a new task + OPEN-QUESTION Q17.

## CI / signing health (resolved 2026-06-02)

Full CI is **green**, including the e2e matrix across Grafana 12.3.6 / 12.4.3 / 13.0.1 / nightly.
Two CI issues were found (by checking GitHub Actions, not just local gates) and fixed in #78:
1. The compatibility check broke once F4 added a second `module.tsx`; now runs as a matrix over
   all module entrypoints (also covers the panel module).
2. `npm run sign` returned HTTP 409 (public/community signing is rejected for an unpublished
   plugin). Now **private signing** in both `ci.yml` and `release.yml`, using the `QA-Alintech`
   GitHub Actions environment variable `GRAFANA_INSTANCE_URL` (+ `http://localhost:3000`) as root
   URLs, with the token from the `GRAFANA_ACCESS_POLICY_TOKEN` repo secret. See `docs/signing.md`.
   The `QA-Alintech` environment has no blocking approval rule (PR CI runs unattended).
LESSON: verify actual GitHub Actions status on each PR, not only local gates.

## Last completions

- **#112 (TC4)** merged — e2e suite (audit-then-gap-fill on PC4/PC5 specs): config flow + Test-URL
  allowlist-denied error (security boundary, AC34), load-mode selection (asserted via end-to-end iframe src),
  AC30 sandbox attribute (exact `allow-scripts allow-same-origin` + pointer-events:none + no-referrer), AC31
  editor input-safety (authoritative guard is backend CR5), AC35 light+dark theme. **In-panel proxy render
  OUT OF SCOPE (FR5 deferred)** — view tests use example.com DIRECT. Dev/e2e-only provisioning allowlist entry
  (shipped fail-closed default unchanged). 8/8 pass --retries=2, no flakes. Review APPROVE-WITH-NITS (no
  blocking; genuine + FR5-respecting). Full CI green incl. e2e ×4. **Follow-up #113 filed:** provisioned
  `jsonData.allowedDomains` may not reach the backend (TC4 success-path self-skips; honest skip) — investigate
  for DR4/DR7. **⇒ testing-cicd is now TC1/TC2/TC3/TC4/TC5 DONE; only TC6 remains (gated on Q13b).**
- **#111 (#105 AllowPrivateIP)** merged — per-domain opt-in wired through (was parsed-but-inert). Relaxes
  SF1 for **RFC1918 IPv4 ONLY**, only for an allowlisted+opted-in domain, per request via `security.Policy`
  threaded through context into `ResolveAndValidatePolicy`/`NewControlPolicy` (zero-Policy delegates →
  default byte-for-byte unchanged, existing TC1/SF tests pass UNEDITED). Loopback/link-local/metadata(name+IP)/
  CGNAT/ULA/unspecified/multicast/reserved + the fail-closed nil sentinel stay hard-blocked when opted in;
  multi-record fail-closed preserved. **`DisableKeepAlives:true` is security-load-bearing** (prevents
  cross-domain connection-reuse leaking the opt-in — proven by a test). Distinct `Warn` audit +
  `webview_proxy_private_ip_permitted_total` on each permit; per-hop redirect policy auto-recomputed.
  **Two independent reviews — adversarial-SECURITY + correctness — both APPROVE, no blocking** (security
  invariants verified with adversarial tests; CI golangci-lint green). **Non-blocking follow-ups tracked:**
  (1) `check-frameable` reaches a permitted private IP WITHOUT a permit audit-trail (parity gap; capture in
  DR5 threat-model / consider wiring the recorder there); (2) IPv6 ULA relaxation deferred (one-line follow-up
  if a stakeholder use case appears); (3) multi-private answer set over-reports permits in audit (louder, not a
  leak). NB: this scopes-relaxes the brief's "hardcoded, not admin-configurable" blocklist per the Q18 decision —
  DR5 threat-model must document the re-opened surface.
- **#110 (TC3)** merged — frontend unit/component coverage (AC 33), audit-then-gap-fill: +16 genuine tests
  (160→176), webview-component branch coverage 82.5%→93.8%; covered `extractErrorMessage` fallbacks,
  resolve/reject-after-unmount guards, the LoadModeEditor degraded commit-guard (faithful RadioButtonGroup
  mock), ViewportEditor NaN/re-sync/no-clobber/no-drag. Test-only; no production change; existing tests
  preserved. Review APPROVE-WITH-NITS (mutation-verified genuine; 1 tracked test-debt nit: a "rejects after
  unmount" test asserts `expect(true)` — strengthen to spy on console.error; its resolve-twin already covers
  the guard). Full CI green incl. e2e ×4 + security gate.
- **#109 (DF3)** merged — **direct-only-fallback stream COMPLETE (DF1–DF3).** View-mode guard: `useBackendAvailable`
  read only in the proxy branch; direct mode renders immediately (never waits on the probe); proxy + backend
  unavailable ⇒ accessible fallback (no broken iframe — `buildProxySrc` unreachable until settled-available);
  loading ⇒ neutral placeholder. Direct fallback intentionally NOT attempted for proxy-configured sites
  (framability unknowable at view time). Review APPROVE (no blocking; 2 mutation checks confirm). Full CI green
  incl. e2e ×4 + security-suite gate. (Cosmetic nit deferred: stale `refreshKey` comment in WebViewPanel.tsx.)
- **#108 (DF2)** merged — editor degradation: load-mode selector converted from a standard `addRadio` to a
  custom `LoadModeEditor` consuming `useBackendAvailable`; when backend unavailable it omits Auto/Proxy
  (omission + a `handleChange` guard, because `@grafana/ui` `RadioButtonGroup` doesn't propagate per-option
  `disabled` to the input) leaving only Direct, and the Test URL button is disabled with a clear note;
  auto-re-enables when present; no note flash while loading. **Clamp is display-only** — a saved proxy/auto
  value is NOT mutated on mount (DF3 enforces at view time). Review APPROVE (no blocking; 3 mutation checks
  confirm regression-sensitivity). Full CI green incl. e2e ×4 + security-suite gate. FR3/FR4 tests preserved.
- **#107 (DF1)** merged — `useBackendAvailable` hook (direct-only-fallback STARTED): probes `/health` once,
  module-scoped shared promise cache (one probe across all instances, no poll), fail-safe per Q12
  (`backendAvailable` true ONLY on 200+`{status:"ok"}`; any error/non-2xx/timeout/unexpected ⇒ false;
  `loading`→`true|false`, never permanent unknown). `PLUGIN_ID` exported from loadMode.ts. 9 unit tests
  (states + single-shared-probe, mutation-verified by review). Review APPROVE (no blocking). Full CI green
  incl. e2e 12.3.7/12.4.4/13.0.2/nightly + the new non-skippable security-suite gate. Single source of
  truth for DF2/DF3. **Editor note for DF2:** the load-mode selector is a standard `.addRadio({path:'loadMode'})`
  in `module.tsx` (can't read async backend state) → DF2 must convert it to a custom editor that consumes
  `useBackendAvailable`. The Test URL button is the `FrameabilityEditor` custom editor (easy to wire).
- **#103 (TC2)** merged — AC 23–29 security suite (`pkg/plugin/proxy_security_limits_test.go`, 11 hermetic
  tests through the real `ServeHTTP`): redirect-into-denied blocked (23), oversize→413 (24), per-INSTANCE
  rate limit→429 (25, deliberately spread across two domains so only the shared instance bucket can deny),
  outgoing auth/Grafana strip (26, asserted at the upstream), incoming Set-Cookie/HSTS/HPKP strip (27),
  audit log url/status/size/duration on success+denial (28), all four metric families + increment-on-denial
  on an isolated registry (29). Test-only; CI build/lint/test + compatibility green; independent review
  APPROVE (no blocking, 3 minor nits). Backend-only ⇒ e2e unaffected.
- **#104 (TC1)** — AC 17–22 security suite (`pkg/plugin/proxy_security_ssrf_test.go`, hermetic, stub
  resolver + dial trap, NO network): fresh-install fail-closed across all 3 endpoints (17), allowlist on
  `/proxy`+`/proxy-resource`+`/check-frameable` (18), allowlisted-host→RFC1918 denied (19), metadata
  by-name + link-local by-IP denied regardless of allowlist (20), DNS-rebinding prevented at 3 layers
  (21: poisoned set fail-closed, TOCTOU dials only validated first IP w/ resolver-call-count==1,
  connect-time `NewControl` guard table), non-HTTP schemes→400 (22). Review APPROVE (no blocking; AC-21
  reconstruction confirmed faithful to production `(*Dialer).DialContext`; `/check-frameable` IP-block
  confirmed enforced+surfaced as a 200 proxy verdict per Q7). **MERGED** (re-run CI on the main-updated
  branch green: build/lint/test + compatibility). Surfaced the `AllowPrivateIP` dead-config finding →
  #105/Q18. **⇒ The non-skippable BACKEND SECURITY SUITE (AC 17–29) is now COMPLETE.**
- **#101 (FR4)** merged — **frameability COMPLETE.** Load-mode resolution (`resolveLoadMode`) + view-mode
  proxy-src wiring (`buildProxySrc` → `${config.appSubUrl}/api/plugins/…/proxy?url=<enc>` + `hide=` per
  selector for CR5; sub-path-safe; param-injection-safe). The panel renders via the proxy in proxy mode.
- **#100 (FR3)** merged — Test-URL button (calls /check-frameable, Direct/Proxied/Error, persists detectedMode).
- **#99 (FR1)** + **#98 (FR2)** merged — `/check-frameable` (SSRF-safe, Q7) + `/health` (liveness).
- **#97 (CR5)** merged — **content-rewriting stream COMPLETE.** Hide-selectors applied to proxied HTML via
  goquery `Find`+inline `display:none!important` (markup-injection-proof: selector text never enters markup;
  cascadia-validated + length/count caps; `hide` query param, not forwarded upstream). 93.3% coverage. Review
  APPROVE (no findings). The proxy now fully renders a framing-blocked page.
- **#96 (CR4)** merged — redirect handling: 3xx `Location` rewritten → proxy URL (browser re-enters proxy
  per hop → full pipeline re-validates: allowlist/scheme/rate + SF4 IP gate); `_wvredir` depth cap
  (MaxRedirects=3 → 502 redirect-loop; loops terminate); ModifyResponse allowlist pre-block of denied hops
  (403); `_wvredir` never forwarded upstream; no raw http(s) Location escapes to the browser. Both endpoints.
  92.9% coverage. Adversarial SSRF review APPROVE (no bypass).
- **#95 (CR3)** merged — `/proxy-resource` endpoint. Extracted a shared `(*proxyHandler).serve(w,r,endpoint)`
  so `/proxy` and `/proxy-resource` run the IDENTICAL pipeline/header-policy/audit/metrics (`/proxy`
  byte-for-byte unchanged, no test touched). Resource branch: framing/header strip, NO HTML rewrite,
  Content-Type preserved, gzip subresources stream compressed, size-limited; same validated-host dial path
  (SSRF-safe). 92.8% coverage. Review APPROVE (no findings). HTML+subresource render path (CR1–CR3) complete.
- **#94 (CR2)** merged — goquery HTML rewriting (`pkg/plugin/rewrite.go`): rewrites subresource refs →
  `/proxy-resource?url=` and navigation → `/proxy?url=` (Q9 query-encoded), injects/fixes `<base href>`,
  removes CSP/refresh `<meta>`, removes inline frame-busters (Q11 comparison-AND-navigation marker pair),
  charset-aware. Restructured the CR1 seam so rewriting runs on ALL HTML (fixing CR1's plain-HTML gap);
  non-HTML still byte-identical. goquery-escaped output (XSS-safe, independently verified); rewrite error
  degrades to serving the decoded original (200, not 502). Added `github.com/PuerkitoBio/goquery`. 92.7%
  coverage, race-clean. Design pass (Plan agent) → review APPROVE (no blocking).
- **#93 (CR1)** merged — gzip decode + HTML detection in `ModifyResponse` (content-rewriting started).
  HTML detected by Content-Type; gzip HTML decoded (Content-Encoding removed, Content-Length fixed) with
  a `// CR2:` rewrite seam; non-HTML passes through byte-identical. **Security fix:** pins
  `Accept-Encoding: gzip` on the outbound request so net/http does NOT transparently/unboundedly
  auto-decompress before ModifyResponse — the single decode is bounded by `MaxResponseBytes`
  (gzip-bomb → 413); malformed gzip → 502, no panic. 93.8% coverage. Review APPROVE.
- **#92 (P7)** merged — **proxy stream COMPLETE.** Single `reasonStatus` table + `writeDenial` wires every
  denial→(HTTP status, `denials_total{reason}`) so they can't drift; fixed two metric-reason-drift bugs
  (metadata mislabeled ip-blocklist; resolve-failure mislabeled metadata) — reason-only, status unchanged;
  removed the `validationStatus` stub. Exhaustive denial-matrix test (status + exact reason per class) +
  the P4 exactly-at-limit boundary test. 94.0% coverage. Review APPROVE (no findings).
- **#91 (P6)** merged — Prometheus metrics on `/proxy` exposed via the SDK `/metrics` (default registry):
  `webview_proxy_requests_total{status}`, `denials_total{reason}`, `requests_in_flight` gauge,
  `request_duration_seconds` histogram. `sync.Once` registers once; all handlers share the registered
  collectors (no lost increments, no panic). Status codes unchanged (P7 refines). Bounded cardinality.
  92.8% coverage, race-clean, go.mod tidy. Review APPROVE.
- **#90 (P5)** merged — structured audit log per `/proxy` request (url/user/status/bytes/duration) via a
  single `defer` in `ServeHTTP` + an `auditResponseWriter` (status/byte recorder with `http.Flusher`
  passthrough so streaming is preserved); injectable `log.Logger`; emitted on success + all denials;
  nil-safe user (`anonymous` fallback); logs no secrets. 92.6% coverage. Review APPROVE.
- **#89 (P4)** merged — resource limits: max body size → 413 (clean Content-Length path; `limitedBody`
  caps chunked/undeclared as defense-in-depth), total per-request timeout → 504 (`context.WithTimeout`,
  deferred cancel can't truncate a legit body), error mapping via `errors.Is` (413/504; 403/502 intact).
  Q10 = one total budget. 91.9% coverage, race-clean. Review APPROVE-WITH-NITS (boundary test tracked).
  A transient CI `compare` ECONNRESET (bundle-size action's npm install) was re-run green.
- **#88 (P3)** merged — incoming response-header stripping in `stripFramingHeaders`: `Set-Cookie`,
  `Strict-Transport-Security`, `Public-Key-Pins`(+report-only), `Clear-Site-Data` — upstream can't set
  cookies on / pin policy against / clear data for the Grafana origin. 91.8% coverage. Self-reviewed
  (trivial S task); CI green.
- **#87 (P2)** merged — outgoing request-header stripping in the ReverseProxy Rewrite hook (on `r.Out`):
  Cookie/Authorization/Proxy-Authorization, all `X-Grafana-*` (prefix sweep), forwarding/identity
  (`X-Forwarded-*`, Forwarded, X-Real-Ip, Referer, Origin, Via), and edge/CDN client-IP + mTLS identity
  (`X-Forwarded-Client-Cert`, True-Client-IP, CF/Fastly-Client-IP, X-Client-IP, X-Cluster-Client-IP,
  `X-Original-*` — folded in from review). Conservative UA/Accept. Forwarded request leaks nothing about
  Grafana/viewer. 91.7% coverage. Review APPROVE-WITH-NITS (nit folded in).
- **#86 (P1)** merged — **core `/proxy` endpoint** (`pkg/plugin/proxy.go`), first assembly of the
  security pipeline behind HTTP. `GET /proxy?url=<encoded>` on `httputil.ReverseProxy`: pipeline runs
  in the handler BEFORE any upstream connect (SF3 allowlist → SF2 validate w/ matched ports → SF5
  rate-limit + Acquire), SF4 resolve-then-dial wired as the transport (IP validation + rebind guard).
  Target reconstructed from VALIDATED components → no parser-differential SSRF (locked by
  `TestProxySSRFParserDifferentialResistance`). `ModifyResponse` strips `X-Frame-Options` + neutralises
  CSP `frame-ancestors`; permissive CORS; denial→code mapping; empty allowlist fails closed.
  `plugin.AllowedDomain → security.AllowlistEntry` shim (`security_map.go`) keeps `pkg/security` a leaf.
  Rate-limit keyed on tenant `Namespace`. 91.2% coverage. Review APPROVE-WITH-NITS (no blocking). TWO CI
  golangci-lint round-trips fixed (errcheck-style + SA1019 `OrgID`→`Namespace`) that local gates missed.
- **#84 (SF4)** merged — DNS-resolve-then-dial (`pkg/security/resolvedial.go`): injectable resolver,
  validate every resolved IP via SF1, FAIL CLOSED if any record blocked (Q6), pin dial to validated IP +
  re-validate the exact connect IP in `net.Dialer.Control` (rebind defense), GCP metadata-by-name (IP
  layer covers the rest). Obfuscated decimal/octal/hex literals fail DNS → `resolve-failed` (review
  corrected a false "Go canonicalises at Control" claim + the test that mis-asserted it). Leaf, 94.9%,
  race-clean. A golangci-lint failure (5 errcheck + 1 staticcheck), caught only in CI, was fixed.
- **#85 (SF5)** merged — rate limiter (`pkg/security/ratelimiter.go`): two-tier token bucket
  (per-instance + per-domain, per-domain overrides) + in-flight concurrency cap; thread-safe,
  injectable Clock, fail-closed on non-positive limits, stable reason tokens. Leaf, 98.6%, race-clean,
  no new deps. Review APPROVE.
- **#83 (SF3)** merged — allowlist matcher (`pkg/security/allowlist.go`): exact + opt-in subdomain
  matching, per-domain options, empty/nil denies all; reuses `NormalizeHostname` on BOTH sides
  (homograph-safe), rejects partial-label/suffix-trick bypasses, skips un-normalisable entries.
  Review caught a latent IMPORT CYCLE (security imported plugin); fixed via Option B — security-owned
  `AllowlistEntry`/`EntryOptions`, keeping `pkg/security` a dependency-free leaf. 97.2%.
- **#82 (SF2)** merged — URL validator library (`pkg/security/urlvalidator.go`): http/https scheme
  allowlist, port restriction (80/443 + per-domain `DomainOptions.AllowedPorts`), hostname
  normalisation (lowercase, trailing-dot strip) + IDN→punycode via `x/net/idna` (Lookup profile,
  fails closed), stable `Reason*` tokens, userinfo/malformed rejection. IP-literal hosts short-circuit
  IDNA and canonicalise to match SF1's form. 97.6% coverage, 128 subtests. Independent review APPROVE,
  full CI green (incl. 4-version e2e). Also brought SF1's `ipblocklist.go` to gofmt-clean (whitespace
  only). Carry-forward security notes propagated to #21 (SF3) and #22 (SF4).
- **#81 (SF1)** merged — hardcoded IP-blocklist library (`pkg/security/`), fail-closed, IPv4-mapped
  IPv6 unwrap, ~50 tests, 96% coverage. (security-foundation stream started.)
- **#80 (PC5)** merged — view-mode behaviours: auto-refresh, debug overlay, multi-instance.
  hide-selectors deferred to proxy (CR5); OPEN-QUESTIONS Q4 resolved. **panel-core stream done.**
- **#79 (PC4)** merged — editor numeric inputs, dimension inputs, reset; URL deduped. AC6 reconciled.
- **#78 (CI fix)** merged — multi-module compatibility matrix + private signing (QA-Alintech).
  Full CI incl. 4-version e2e matrix now green.
- **#77 (PC3)** merged — interactive viewport editor (custom options editor: drag-pan,
  cursor-anchored wheel-zoom, live readout). Q3 resolved.
- **#76 (PC2)** merged — viewport interaction maths (pan-delta, cursor-anchored zoom, clamp).
- **#75 (PC1)** merged — view-mode direct iframe render with CSS-transform viewport.
- **#74 (F4)** merged — nested Web View panel registered (`src/panels/webview/` with its own
  `plugin.json`, globbed to `dist/panels/webview/`, registered as a child of the app). Demo pages
  removed. Runtime-verified: "Web View" appears in the visualization picker. OPEN-QUESTIONS Q1/Q2
  resolved.
- **#73 (F3)** merged — plugin settings schema + Go config loader with fail-closed defaults
  (empty allowlist, 5 MiB body, 10 s timeout, 3 redirects, 60/30/10 rate limits). TS mirror type.
- **#72 (F2)** merged — canonical `PanelOptions` type, `DEFAULT_PANEL_OPTIONS`, tested
  `normalizeOptions` helper.
- **#61** merged — project setup (scaffold, dev env, plan, board, orchestrator, signing wiring).
- Board: 51 issues (#1 + #11–#60); 9 setup-retry duplicates (#2–#10) closed.

## Next to dispatch (in order)

1. **panel-core** (#14 PC1 → #18 PC5) — direct-iframe view-mode render + CSS-transform viewport,
   then the config-mode interactive editor. This delivers the shippable direct-mode panel.
2. **security-foundation** (#19 SF1 → #23 SF5) — independent of panel-core; can interleave. Must
   land (with tests) before the proxy stream.
Then: frameability (#24–#27), proxy (#28–#34), content-rewriting, direct-only-fallback, etc.

Execution is **serial** (single shared working tree) — one impl agent at a time, each on its own
task branch, reviewed by a separate agent, runtime-verified for UI tasks, then squash-merged.

## Active blockers

- None. Remaining open questions in `OPEN-QUESTIONS.md` are deferred to their tasks
  (e.g. Q3 config-vs-view detection → panel-core; Q5/Q6 → security-foundation/proxy).

## Notes

- Dev env: `docker compose up -d`; Grafana 12.4.0 at :3000 (anon admin). In this sandbox the
  Docker daemon needs restarting periodically (see `RUNBOOK.md`). Playwright Chromium under
  `/opt/pw-browsers`.
- Board format is Issues + labels (no Projects v2 via tooling). All artifacts authored as
  `wilsonwaters`; no personal name/email committed; no Anthropic attribution in commits/PRs.
- Orchestrator bookkeeping (this file, board-map, OPEN-QUESTIONS) is committed directly to `main`;
  feature code goes via task-branch PRs.

# PROGRESS

Narrative log of project status, maintained primarily by the orchestrator agent.

## Status summary

Setup complete and the **execution loop is running** (task branch → PR → review → CI-green →
squash-merge into `main`). **foundation (F1–F4) and panel-core (PC1–PC5) are DONE; security-
foundation is in progress (SF1 #81 merged).** The plugin is a **shippable direct-mode Web View
panel** today: sandboxed iframe at a configured viewport, interactive editor (drag-pan/wheel-zoom +
numeric inputs/reset), auto-refresh, debug overlay, multi-instance — e2e-verified across Grafana
12.3.6/12.4.3/13.0.1/nightly and privately signed. No proxy or security *enforcement* wired yet
(SF1 is a standalone library); those come in security-foundation → frameability → proxy →
content-rewriting, which is the path to a framing-blocked site like the BOM radar.

## Handoff notes for the next orchestrator (2026-06-02)

Everything needed to resume is in `ai-state/` — read `brief.md`, this file, `streams.md`,
`board-map.md`, `OPEN-QUESTIONS.md`, and the relevant `streams/<name>/master-plan.md`. Then
`mcp__github__list_issues` (label `status:ready`) and continue at **SF2 (#20)**. A few quirks
that are NOT obvious from the code:

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

- **SF2 (#20) URL validator** — implementation sub-agent dispatched (branch `sf2-url-validator`,
  PR into `main`). Pure backend Go library in `pkg/security/` (sibling of SF1): scheme allowlist
  (http/https), port restriction (80/443 + per-domain extra ports from `DomainOptions.AllowedPorts`),
  hostname normalisation + IDN→punycode. Issue labelled `status:in-progress`. Awaiting PR, then an
  independent review pass (no runtime verification needed — internal library, no endpoint).
- Then: SF3 (#21) allowlist matcher → SF4 (#22) DNS-resolve-then-dial → SF5 (#23) rate limiter.
  Then frameability (#24–27) → proxy (#28–34) → content-rewriting (#35–39). The BOM radar test
  becomes possible once proxy + content-rewriting land.

## Screenshots convention (added 2026-06-02)

PR runtime screenshots must be committed to `docs/screenshots/issue-<N>/` and embedded via raw
GitHub URLs in the PR body (a bare `/tmp/...` path is invisible to reviewers). Codified in
`.claude/agents/orchestrator.md`. Backfilled #74/#75/#77/#79/#80 with inline screenshot comments;
key shots committed under `docs/screenshots/`.

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

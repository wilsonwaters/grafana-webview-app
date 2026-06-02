# PROGRESS

Narrative log of project status, maintained primarily by the orchestrator agent.

## Status summary

Setup complete and the **execution loop is running** (task branch → PR → squash-merge into `main`).
The **foundation stream is DONE** (F1–F4 merged). The Web View panel is registered as a nested
panel inside the app and renders a placeholder; canonical `PanelOptions` type and the fail-closed
plugin settings schema/loader are in place. No real viewport, proxy, or security enforcement yet.

## Currently in flight

- None. **foundation + panel-core streams COMPLETE.** Next stream to start:
  `security-foundation` (#19–#23) — the backend security building blocks required before any
  proxy work. (BOM radar specifically needs the proxy, which depends on this stream.)

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

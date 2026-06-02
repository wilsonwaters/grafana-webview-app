# PROGRESS

Narrative log of project status, maintained primarily by the orchestrator agent.

## Status summary

Setup complete and the **execution loop is running** (task branch → PR → squash-merge into `main`).
The **foundation stream is DONE** (F1–F4 merged). The Web View panel is registered as a nested
panel inside the app and renders a placeholder; canonical `PanelOptions` type and the fail-closed
plugin settings schema/loader are in place. No real viewport, proxy, or security enforcement yet.

## Currently in flight

- None at this instant — about to dispatch `panel-core` PC1 (#14).

## Last completions

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

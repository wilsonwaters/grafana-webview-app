# Master Plan — Foundation & Repo Hygiene (`foundation`)

## Goal

Establish repo metadata, the plugin settings schema with safe defaults, and the nested
Web View panel registration, so every later stream builds on a stable, documented base.
This stream produces little user-visible output and is intentionally not vertically sliced.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| F1 | Repo hygiene files: LICENSE (Apache-2.0, already declared), real README intro, CHANGELOG (Keep-a-Changelog), CONTRIBUTING, CODE_OF_CONDUCT, SECURITY.md **stub** (no threat-model content yet), `.github/ISSUE_TEMPLATE/*` + `PULL_REQUEST_TEMPLATE.md`. Verify existing `dependabot.yml` covers npm + Go. | S | — |
| F2 | Define the shared **panel options type** in TS (`url`, `loadMode`, `detectedMode`, `viewportX/Y/Zoom`, `iframeWidth/Height`, `refreshIntervalSec`, `hideSelectors`, `showDebugOverlay`) with safe defaults, plus matching Go config structs. Reconcile spec's `PanelOptions` with the context doc's older field names; document the canonical schema. | M | F1 |
| F3 | Define the **plugin settings schema** (admin-configurable): domain allowlist (empty by default), per-instance + per-domain rate limits, max body size (5 MiB), timeouts (10 s), max redirect depth (3), per-domain options structure. Wire safe defaults into the Go backend config loader. No enforcement logic yet — types + defaults + loader only. | M | F1 |
| F4 | Register the nested **Web View panel** inside the app plugin: add the panel module/registration, a minimal placeholder panel component, and the `includes`/registration wiring so the panel appears in the panel picker. Confirm packaging of a panel nested in an app plugin. | M | F2 |

## Integration points

- F2 panel options type is imported by `panel-core`, `frameability`, `direct-only-fallback`.
- F3 settings schema + defaults are consumed by `security-foundation`, `proxy`, `frameability`.
- F4 panel registration is the surface every frontend task renders into.
- SECURITY.md stub (F1) is filled in incrementally by `docs-release`.

## Out of scope

- Any rendering, viewport, or interaction behaviour (panel-core).
- Any security enforcement logic (security-foundation, proxy).
- Final README content, screenshots, badges (docs-release).
- The substantive threat-model text (docs-release).

## Open questions

- Exact packaging mechanics for a panel nested inside an app plugin (module registration vs
  separate panel `plugin.json`). Blocks F4. (See OPEN-QUESTIONS.)
- Whether to keep the scaffold's example app pages (Page One–Four) or remove them. Blocks F4.

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.

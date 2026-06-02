# Master Plan — Direct-Only Fallback / Graceful Degradation (`direct-only-fallback`)

## Goal

Ensure the panel is still creatable and usable in **direct-iframe-only** mode when the
backend is unavailable (e.g. Grafana Cloud without backend support), with proxy features
cleanly disabled and explained in the UI. This is a runtime degradation of the same plugin,
not a separate build. Stakeholder-required; appears as explicit tasks. Vertical slices that
each leave the panel functional.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| DF1 | Backend-availability detection slice: a frontend hook that probes `/health` (frameability FR2) once at editor mount, caches the result, and exposes a `backendAvailable` flag with loading/unknown states. | M | frameability FR2; panel-core PC4 |
| DF2 | Editor degradation slice: when `backendAvailable` is false, disable the "Test URL" button and the Proxy load-mode option, force/clamp `loadMode` to Direct, and show a clear explanatory note ("backend unavailable — direct iframe only"). Controls re-enable when the backend is present. | M | DF1; frameability FR3, FR4 |
| DF3 | View-mode guard slice: if a saved panel has `loadMode: proxy` but no backend is available at view time, render a clear, accessible fallback/empty state (and attempt direct where the URL permits) rather than a broken iframe. | M | DF1; panel-core PC1 |

## Integration points

- Consumes `/health` from `frameability` FR2 and wraps the `frameability` FR3/FR4 editor
  controls and the `panel-core` PC1/PC4 surfaces.
- The `backendAvailable` flag is the single source of truth shared between editor (DF2) and
  view (DF3) degradation.
- Coordinates with the shared open question on what `/health` reports (liveness vs capability).

## Out of scope

- The `/health` endpoint implementation itself (`frameability` FR2).
- Any proxy functionality (`proxy`, `content-rewriting`).
- A separate frontend-only distribution/build (explicit non-goal — this is runtime degradation).
- E2E coverage of the degraded path (`testing-cicd`).

## Open questions

- Whether backend availability can change within a session (plugin enabled/disabled live) and
  whether to re-probe, or treat it as fixed per editor session. Blocks DF1.
  (See OPEN-QUESTIONS.)
- On Grafana Cloud specifically, what `/health` returns when the backend is simply not
  provisioned vs erroring — affects how DF1 interprets failures. Blocks DF1.
  (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.

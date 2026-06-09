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

- ~~Re-probe vs fixed-per-session; Cloud not-provisioned vs erroring.~~ **RESOLVED (Q12, 2026-06-09):**
  fixed-per-session (probe `/health` once, module-scoped shared cache, no poll; optional manual refetch);
  fail-safe (`true` only on 200 + `{status:"ok"}`, any error/non-200/timeout/unexpected ⇒ `false`;
  `loading`→`true|false`, never permanent `unknown`). Full text in OPEN-QUESTIONS Q12.

## Stakeholder decisions

- 2026-06-09: direct-only-fallback is the active stream after the testing-cicd security suite. FR5/#102
  (in-panel proxy render) is DEFERRED indefinitely for v1 — DF degradation is about backend ABSENCE, which
  is independent of FR5, so DF proceeds normally.

## Changelog

- 2026-06-09 — stream STARTED; Q12 resolved; dispatching DF1 (#40, backend-availability hook).

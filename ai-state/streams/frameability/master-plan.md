# Master Plan — Frameability Detection (`frameability`)

## Goal

Let a dashboard author click "Test URL" in the editor and get a clear Direct / Proxied /
Error result that persists into panel options, with the detection endpoint subject to the
exact same security gates as the proxy. Vertical slice: a working button that produces a
visible, saved result.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| FR1 | Backend `/check-frameable` endpoint: HEAD/GET the target through the security pipeline (SF2 scheme/port → SF3 allowlist → SF5 rate limit → SF4 resolve+blocklist+dial), parse `X-Frame-Options` and CSP `frame-ancestors`, return `{frameable, reason, recommendedMode}`. Same allowlist/blocklist as the proxy. (AC 1 backend, 18 partial) | M | security-foundation SF2–SF5; foundation F3 |
| FR2 | Backend `/health` resource: lightweight liveness endpoint used by the frontend to detect backend availability (no proxying, no security pipeline). (supports direct-only-fallback) | S | foundation F4 |
| FR3 | Frontend "Test URL" slice: button in the config editor calling `/check-frameable` via `getBackendSrv()`, displaying Direct / Proxied / Error, with loading and error states; result stored in `detectedMode` in panel options so it persists with the dashboard. (AC 1, 2, 3) | M | FR1; panel-core PC4 |
| FR4 | Load-mode selector slice: `@grafana/ui` Select for Auto / Direct / Proxy in the editor; Auto uses `detectedMode`; manual Direct/Proxy override the saved `loadMode`; view-mode rendering branch (panel-core PC1) honours the resolved mode. (AC 4, 10) | M | FR3 |

## Integration points

- Consumes all `security-foundation` libraries; FR1 is the first real consumer and proves the
  pipeline composes correctly.
- FR2 `/health` is consumed by `direct-only-fallback` for backend-availability detection.
- FR3/FR4 extend the `panel-core` PC4 editor; `detectedMode`/`loadMode` come from foundation F2.
- The resolved `loadMode` feeds `proxy` (which `src` the iframe uses) and `panel-core` view mode.

## Out of scope

- The proxy fetch/rewrite path itself (`proxy`, `content-rewriting`).
- Disabling Test URL / proxy when the backend is down (`direct-only-fallback`).
- Re-detection at view time — explicitly never happens (detection is config-time only).

## Open questions

- Should `recommendedMode` be returned for sites that error (treat ambiguous as proxy vs
  surface as Error)? Blocks FR1. (See OPEN-QUESTIONS.)
- Whether `/health` should report backend capability detail (e.g. proxy enabled) or just
  liveness. Blocks FR2 / direct-only-fallback. (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.

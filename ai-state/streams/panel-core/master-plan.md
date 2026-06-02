# Master Plan — Panel Core & Direct Mode (`panel-core`)

## Goal

Deliver a working Web View panel that renders a direct-iframe URL at a configured
viewport, with a full config-mode editor for positioning that viewport. At the end of
this stream the plugin is shippable as a self-hosted private build for sites that allow
framing — no backend proxy required. Tasks are vertical slices: each closes with a
demonstrable panel state.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| PC1 | View-mode render slice: panel reads saved options and renders the target URL in an iframe at the saved X/Y/zoom via CSS `transform: scale() translate()`, clipped by an `overflow: hidden` container, `sandbox="allow-scripts allow-same-origin"`, `pointer-events: none`. Direct mode only (iframe `src` = URL). Detect config-vs-view mode from panel editor context. (AC 8, 9, 10, 16, 30) | M | foundation F2, F4 |
| PC2 | Viewport **interaction** math + unit tests. NOTE: the basic `buildViewportTransform` (X/Y/zoom → `scale() translate()`) was already delivered by PC1 in `src/panels/webview/viewport.ts`. PC2 now adds the remaining pure helpers that the PC3 editor needs: cursor-position-aware wheel-zoom (adjust X/Y so zoom anchors at the cursor), pan-delta (screen-pixel drag → virtual X/Y accounting for zoom), and zoom clamping 0.1–5.0 — all unit-tested, building on the existing helper. (AC 33 partial) | M | PC1 |
| PC3 | Config-mode editor slice — interactive preview: drag-to-pan and wheel-to-zoom inside the editor preview pane, live X/Y/zoom coordinate readout, container captures mouse events. (AC 5) | M | PC2 |
| PC4 | Config-mode editor slice — capture & manual inputs: "Capture current view" button writes current pan/zoom to options; numeric `@grafana/ui` inputs for X, Y, zoom kept in sync with the preview; URL field; virtual iframe dimension inputs (default 1920×1080). (AC 6, 7) | M | PC3 |
| PC5 | View-mode behaviours: optional auto-refresh on the configured interval (default 0 = off) reloading the iframe; CSS hide-selectors applied where origin allows; optional debug overlay (off by default in view mode); verify multiple panel instances render independently. (AC 11, 12) | M | PC1 |

## Integration points

- Consumes the panel options type and panel registration from `foundation` (F2, F4).
- `frameability` adds the "Test URL" button and load-mode selector into the PC4 editor.
- `direct-only-fallback` wraps the PC4 editor controls to disable proxy/Test URL when no
  backend is present.
- `proxy` supplies the proxy `src` URL that view mode (PC1) selects when `loadMode` resolves
  to proxy; PC1 must branch cleanly on resolved mode.

## Out of scope

- Frameability detection and load-mode selection UI (`frameability`).
- Any backend proxying or proxy `src` construction logic beyond branching on resolved mode
  (`proxy`).
- Backend-availability degradation (`direct-only-fallback`).
- E2E tests (covered in `testing-cicd`); this stream ships unit + component tests.

## Open questions

- How reliably can config-vs-view mode be detected from the panel editor context across the
  supported Grafana version range? Blocks PC1. (See OPEN-QUESTIONS.)
- Whether hide-selectors and debug overlay can apply to cross-origin direct iframes at all
  (DOM access blocked) — may only be meaningful in proxy/same-origin mode. Blocks PC5.

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
- 2026-06-02: PC1 (#75) merged. PC1 necessarily implemented `buildViewportTransform`
  (the basic CSS transform helper) that PC2 had been scoped to create. Refined PC2 to focus
  on the still-needed *interaction* maths (cursor-anchored wheel-zoom, pan-delta, clamp) built
  on PC1's helper, avoiding redundant work. Issue #15 updated to match.
- 2026-06-02: PC2 (#76) merged. Minor tracked debt (review INFO): zoom bounds
  `VIEWPORT_ZOOM_MIN/MAX` are duplicated (private in `types.ts`, exported in `viewport.ts`) with
  identical values — no numeric divergence. A future cleanup can centralise to one source.
- 2026-06-02: Resolved OPEN-QUESTIONS Q3 — the interactive viewport positioning is implemented as
  a **custom panel options editor** (rendered by Grafana only in the edit pane), so the panel
  component never needs to detect edit-vs-view mode and view mode stays non-interactive. PC3
  follows this approach.
- 2026-06-02: CI/signing fixed (#78) — compatibility check runs as a matrix over both module
  entrypoints; private signing via the QA-Alintech env `GRAFANA_INSTANCE_URL` (+ localhost).
  Full CI incl. the 4-version e2e matrix is green.
- 2026-06-02: AC 6 ("Capture current view" button) reconciled with the Q3 custom-editor design:
  the editor commits viewport changes live as the author positions the preview, so explicit
  capture is redundant. AC 6 is satisfied implicitly by live capture; PC4 adds a "Reset view"
  button instead of a no-op capture button. Issue #17 updated. (Spec update, not a copout —
  per methodology, fix-or-update-spec rather than interpret.)
- 2026-06-02: PC4 (#17) — numeric X/Y/zoom inputs (two-way sync with preview, zoom clamped
  0.1–5.0 via `clampZoom`), iframeWidth/iframeHeight dimension inputs (defaults 1920/1080,
  non-positive values fall back to defaults), "Reset view" button (X0/Y0/zoom1). URL control
  reconciled: the standard `url` field registered in module.tsx (F4) is the canonical URL
  input; ViewportEditor reads `context.options.url` and reacts via Grafana re-renders — no
  duplicate URL input. 26 component tests added; e2e runtime-verified (pc4-editor.png).
  All quality gates green.

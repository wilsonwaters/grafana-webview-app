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
| PC2 | Viewport transform math + unit tests: pure helpers converting (X, Y, zoom, virtual dims, container size) to a CSS transform, including wheel-zoom that is cursor-position-aware and clamps zoom 0.1–5.0. (AC 33 partial) | M | PC1 |
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

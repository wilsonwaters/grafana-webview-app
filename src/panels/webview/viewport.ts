/**
 * Pure helpers for the CSS-transform viewport technique.
 *
 * The "viewport into an iframe section" is achieved by transforming the
 * `<iframe>` element itself (not its contents — so it works cross-origin).
 * The iframe is rendered at its virtual dimensions, then scaled and translated
 * so that the saved region is positioned at the top-left of a clipping
 * (`overflow: hidden`) container.
 *
 * Ordering matters. With `transform-origin: top left` the transform list is
 * applied right-to-left: the translate happens first (in unscaled iframe
 * pixels), then the scale. This matches the spec's
 * `scale(zoom) translate(-X, -Y)` (see implementation-spec.md "VIEW TIME"
 * diagram) and keeps X/Y expressed in virtual iframe pixels.
 *
 * ## Sign and units convention
 *
 * - `viewportX` / `viewportY` — virtual iframe pixels. Positive values scroll
 *   the content to the right and down respectively (i.e. viewportX = 100 means
 *   the left edge of the visible region is 100 virtual pixels into the iframe).
 * - `viewportZoom` — dimensionless scale factor in [0.1, 5.0]. A zoom of 2
 *   means the iframe is rendered at double size relative to the container, so
 *   the author can see a finer region.
 * - Screen (drag) deltas — positive dx moves the viewport right (increases
 *   viewportX); positive dy moves the viewport down (increases viewportY).
 *   This mirrors the natural "drag content left to see what is on the right"
 *   behaviour that the CSS translate produces.
 * - Cursor position — expressed in container-local pixels from the top-left
 *   corner of the preview container (i.e. the same coordinate space the mouse
 *   events report relative to the clipping div).
 */

export interface ViewportTransformInput {
  /** Horizontal scroll offset in virtual iframe pixels. */
  viewportX: number;
  /** Vertical scroll offset in virtual iframe pixels. */
  viewportY: number;
  /** Zoom factor applied via CSS scale(). */
  viewportZoom: number;
}

/**
 * Builds the CSS `transform` value for the saved viewport.
 *
 * @returns e.g. `scale(1.5) translate(-100px, -200px)`
 */
export function buildViewportTransform({
  viewportX,
  viewportY,
  viewportZoom,
}: ViewportTransformInput): string {
  return `scale(${viewportZoom}) translate(${-viewportX}px, ${-viewportY}px)`;
}

// ---------------------------------------------------------------------------
// PC2 — Viewport interaction helpers
// ---------------------------------------------------------------------------

/** Minimum allowed zoom factor (inclusive). Matches normalizeOptions in types.ts. */
export const VIEWPORT_ZOOM_MIN = 0.1;

/** Maximum allowed zoom factor (inclusive). Matches normalizeOptions in types.ts. */
export const VIEWPORT_ZOOM_MAX = 5.0;

/**
 * Clamps `zoom` to the valid range [VIEWPORT_ZOOM_MIN, VIEWPORT_ZOOM_MAX].
 *
 * This is the single source of clamping logic for interaction helpers. The
 * same bounds are enforced by `normalizeOptions` in `src/types.ts` when
 * persisting options to the dashboard JSON.
 *
 * @param zoom - Raw zoom value (may be outside bounds)
 * @returns A zoom value guaranteed to be in [0.1, 5.0]
 */
export function clampZoom(zoom: number): number {
  return Math.min(VIEWPORT_ZOOM_MAX, Math.max(VIEWPORT_ZOOM_MIN, zoom));
}

/**
 * Converts a screen-pixel drag delta into a change in virtual viewport
 * coordinates, accounting for the current zoom level.
 *
 * When the author drags the preview area, the mouse delta is expressed in
 * screen pixels. The CSS transform means that one screen pixel corresponds to
 * `1 / viewportZoom` virtual pixels, so the virtual offset must be divided by
 * the current zoom to keep the content under the cursor stationary during a
 * drag.
 *
 * **Sign convention:** positive `screenDx` / `screenDy` increase `viewportX` /
 * `viewportY`. Dragging right (positive dx) scrolls the virtual canvas to the
 * right, revealing content further to the right — consistent with the CSS
 * `translate(-viewportX, -viewportY)` relationship.
 *
 * @param screenDx    - Horizontal drag delta in screen pixels
 * @param screenDy    - Vertical drag delta in screen pixels
 * @param viewportZoom - Current zoom factor
 * @returns Object with `dx` and `dy` — the amount to add to `viewportX` /
 *          `viewportY` (in virtual iframe pixels)
 *
 * @example
 * // At zoom 2, a 100 px screen drag = 50 virtual px
 * panDelta(100, 0, 2) // → { dx: 50, dy: 0 }
 *
 * @example
 * // At zoom 1, drag equals virtual change 1:1
 * panDelta(30, -20, 1) // → { dx: 30, dy: -20 }
 */
export function panDelta(
  screenDx: number,
  screenDy: number,
  viewportZoom: number
): { dx: number; dy: number } {
  return {
    dx: screenDx / viewportZoom,
    dy: screenDy / viewportZoom,
  };
}

/** Input state for the cursor-anchored zoom helper. */
export interface ZoomAtCursorInput {
  /** Current horizontal viewport offset in virtual iframe pixels. */
  viewportX: number;
  /** Current vertical viewport offset in virtual iframe pixels. */
  viewportY: number;
  /** Current zoom factor. */
  viewportZoom: number;
  /**
   * Horizontal cursor position in container-local pixels (from the top-left
   * corner of the clipping/preview container, i.e. what `event.offsetX`
   * reports on the container element).
   */
  cursorX: number;
  /**
   * Vertical cursor position in container-local pixels.
   */
  cursorY: number;
  /**
   * Multiplicative zoom step. Values > 1 zoom in; values < 1 zoom out.
   * Typically derived from a wheel event: `factor = event.deltaY < 0 ? 1.1 : 1/1.1`.
   */
  zoomFactor: number;
}

/** Output state from the cursor-anchored zoom helper. */
export interface ZoomAtCursorOutput {
  /** New horizontal viewport offset in virtual iframe pixels. */
  viewportX: number;
  /** New vertical viewport offset in virtual iframe pixels. */
  viewportY: number;
  /** New zoom factor, clamped to [VIEWPORT_ZOOM_MIN, VIEWPORT_ZOOM_MAX]. */
  viewportZoom: number;
}

/**
 * Computes the new viewport state after a zoom step anchored to the cursor.
 *
 * The invariant is: **the virtual point currently under the cursor remains
 * under the cursor after the zoom**. This is achieved by:
 *
 * 1. Finding the virtual point P under the cursor:
 *    `P = cursorPos / oldZoom + viewportOffset`
 * 2. Computing the new zoom (clamped to [0.1, 5.0]).
 * 3. Adjusting the viewport offset so that P stays at the same screen position:
 *    `newOffset = P - cursorPos / newZoom`
 *
 * **Derivation:**
 * The CSS transform maps virtual point (vx, vy) to screen point (sx, sy) as:
 *   `sx = (vx - viewportX) * viewportZoom`
 *   `sy = (vy - viewportY) * viewportZoom`
 *
 * Keeping `sx` and `sy` fixed while changing zoom from `z` to `z'`:
 *   `(vx - viewportX') * z' = (vx - viewportX) * z`
 *   `viewportX' = vx - (vx - viewportX) * z / z'`
 *
 * Substituting `vx = cursorX / z + viewportX` gives the formula above.
 *
 * @param input - Current viewport state and cursor position + zoom factor
 * @returns New viewport state with zoom clamped
 *
 * @example
 * // Zoom in by 10 % with cursor at the origin (0,0) — only zoom changes
 * zoomAtCursor({ viewportX: 0, viewportY: 0, viewportZoom: 1, cursorX: 0, cursorY: 0, zoomFactor: 1.1 })
 * // → { viewportX: 0, viewportY: 0, viewportZoom: 1.1 }
 *
 * @example
 * // Cursor is at (200, 100) in a view where zoom=1 and offset=(0,0).
 * // The virtual point under the cursor is (200, 100).
 * // After zooming to 2, that virtual point must still appear at (200, 100)
 * // on screen, so newViewportX = 200 - 200/2 = 100, newViewportY = 100 - 100/2 = 50.
 * zoomAtCursor({ viewportX: 0, viewportY: 0, viewportZoom: 1, cursorX: 200, cursorY: 100, zoomFactor: 2 })
 * // → { viewportX: 100, viewportY: 50, viewportZoom: 2 }
 */
export function zoomAtCursor({
  viewportX,
  viewportY,
  viewportZoom,
  cursorX,
  cursorY,
  zoomFactor,
}: ZoomAtCursorInput): ZoomAtCursorOutput {
  const newZoom = clampZoom(viewportZoom * zoomFactor);

  // Virtual point under the cursor before the zoom
  const virtualX = cursorX / viewportZoom + viewportX;
  const virtualY = cursorY / viewportZoom + viewportY;

  // Adjust offsets so the same virtual point is under the cursor at the new zoom
  const newViewportX = virtualX - cursorX / newZoom;
  const newViewportY = virtualY - cursorY / newZoom;

  return {
    viewportX: newViewportX,
    viewportY: newViewportY,
    viewportZoom: newZoom,
  };
}

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

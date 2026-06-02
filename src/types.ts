/**
 * Canonical panel options for the Web View panel.
 *
 * This is the single source of truth for the panel configuration schema.
 * All fields match the implementation spec (ai-state/reference/implementation-spec.md).
 *
 * Note on field-name history: earlier research documents used different names
 * (e.g. `forceProxy`, `initialX`, `initialZoom`, `interactive`). Those names
 * are NOT used here. The spec names below are canonical and must be used by all
 * downstream streams (panel-core, frameability, direct-only-fallback, etc.).
 */
export interface PanelOptions {
  /**
   * Target URL to display in the panel.
   * Empty string means no URL is configured.
   */
  url: string;

  /**
   * Determines how the URL is loaded.
   * - `'auto'`   — use the mode decided at config time (the stored detectedMode
   *                from the "Test URL" check); view mode never re-detects
   * - `'direct'` — always use a plain iframe (no backend proxy)
   * - `'proxy'`  — always route through the backend proxy
   * @default 'auto'
   */
  loadMode: 'auto' | 'direct' | 'proxy';

  /**
   * Result of the last "Test URL" frameability check performed in the panel
   * editor. Null until a check has been run. This is stored so that the
   * auto-mode rendering path does not need to re-detect at view time.
   * @default null
   */
  detectedMode: 'direct' | 'proxy' | null;

  /**
   * Horizontal scroll offset (in virtual iframe pixels) of the saved viewport.
   * The CSS transform translates by this amount so the viewer sees the
   * intended region of the page.
   * @default 0
   */
  viewportX: number;

  /**
   * Vertical scroll offset (in virtual iframe pixels) of the saved viewport.
   * @default 0
   */
  viewportY: number;

  /**
   * Zoom factor applied to the iframe via CSS `scale()`.
   * Valid range: 0.1 – 5.0 (clamped by normalizeOptions).
   * @default 1.0
   */
  viewportZoom: number;

  /**
   * Width of the virtual iframe in pixels. The iframe is rendered at this
   * width and then scaled to fit the panel container.
   * @default 1920
   */
  iframeWidth: number;

  /**
   * Height of the virtual iframe in pixels.
   * @default 1080
   */
  iframeHeight: number;

  /**
   * Auto-refresh interval in seconds. 0 means disabled.
   * @default 0
   */
  refreshIntervalSec: number;

  /**
   * Comma-separated CSS selectors identifying elements to hide inside the
   * iframe via injected CSS. Empty string means nothing is hidden.
   *
   * Warning: selectors are validated before injection to prevent markup
   * injection (handled by the rendering layer — see panel-core stream).
   * @default ''
   */
  hideSelectors: string;

  /**
   * Whether to show a debug overlay on the panel (viewport coordinates,
   * load mode, iframe dimensions, etc.). Intended for authors during
   * configuration, should remain false in production dashboards.
   * @default false
   */
  showDebugOverlay: boolean;
}

/**
 * Safe defaults for all PanelOptions fields.
 * These are the values used for any option not explicitly set in the dashboard JSON.
 */
export const DEFAULT_PANEL_OPTIONS: Readonly<PanelOptions> = {
  url: '',
  loadMode: 'auto',
  detectedMode: null,
  viewportX: 0,
  viewportY: 0,
  viewportZoom: 1.0,
  iframeWidth: 1920,
  iframeHeight: 1080,
  refreshIntervalSec: 0,
  hideSelectors: '',
  showDebugOverlay: false,
};

/** Minimum allowed zoom factor (inclusive). */
const VIEWPORT_ZOOM_MIN = 0.1;

/** Maximum allowed zoom factor (inclusive). */
const VIEWPORT_ZOOM_MAX = 5.0;

/**
 * Merges a partial options object over the safe defaults and applies
 * range constraints to numeric fields.
 *
 * Constraints applied:
 * - `viewportZoom` is clamped to [0.1, 5.0]
 * - `iframeWidth`, `iframeHeight`, and `refreshIntervalSec` are reset to
 *   their defaults when negative (negative values are invalid)
 * - `viewportX` and `viewportY` are reset to 0 when negative
 *
 * This function is pure (no side effects) and safe to call with any partial
 * or unknown input produced by Grafana's dashboard JSON deserialisation.
 *
 * @param partial - Partial panel options (e.g. from a saved dashboard)
 * @returns A fully-populated PanelOptions object with all values in range
 */
export function normalizeOptions(partial: Partial<PanelOptions>): PanelOptions {
  const merged: PanelOptions = {
    ...DEFAULT_PANEL_OPTIONS,
    ...partial,
  };

  // Clamp zoom to [VIEWPORT_ZOOM_MIN, VIEWPORT_ZOOM_MAX]
  merged.viewportZoom = Math.min(
    VIEWPORT_ZOOM_MAX,
    Math.max(VIEWPORT_ZOOM_MIN, merged.viewportZoom)
  );

  // Reject negative offsets — fall back to default (0)
  if (merged.viewportX < 0) {
    merged.viewportX = DEFAULT_PANEL_OPTIONS.viewportX;
  }
  if (merged.viewportY < 0) {
    merged.viewportY = DEFAULT_PANEL_OPTIONS.viewportY;
  }

  // Reject negative dimensions — fall back to defaults
  if (merged.iframeWidth < 0) {
    merged.iframeWidth = DEFAULT_PANEL_OPTIONS.iframeWidth;
  }
  if (merged.iframeHeight < 0) {
    merged.iframeHeight = DEFAULT_PANEL_OPTIONS.iframeHeight;
  }

  // Reject negative refresh interval — fall back to default (disabled)
  if (merged.refreshIntervalSec < 0) {
    merged.refreshIntervalSec = DEFAULT_PANEL_OPTIONS.refreshIntervalSec;
  }

  return merged;
}

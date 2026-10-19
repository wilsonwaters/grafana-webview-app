import { clampZoom } from './panels/webview/viewport';

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
   * The URL (trimmed) that `detectedMode` was measured for. Auto mode uses
   * `detectedMode` only while `url` is still this URL; after the URL changes,
   * the URL counts as not tested (see `detectedModeForUrl` in loadMode.ts).
   *
   * Null on panels saved before this field existed. Their verdict is kept (see
   * `migratePanelOptions`, which records the saved URL on load).
   * @default null
   */
  detectedForUrl: string | null;

  /**
   * Horizontal scroll offset (in virtual iframe pixels) of the saved viewport.
   * The CSS transform translates by this amount so the viewer sees the
   * intended region of the page. A negative value moves the page right.
   * @default 0
   */
  viewportX: number;

  /**
   * Vertical scroll offset (in virtual iframe pixels) of the saved viewport.
   * A negative value moves the page down.
   * @default 0
   */
  viewportY: number;

  /**
   * Zoom factor applied to the iframe via CSS `scale()`.
   * Valid range: `VIEWPORT_ZOOM_MIN` to `VIEWPORT_ZOOM_MAX` (clamped by normalizeOptions).
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
   * Auto-refresh interval in whole seconds. 0 means disabled. A positive value
   * is rounded to whole seconds and is at least {@link MIN_REFRESH_INTERVAL_SEC}
   * (applied by normalizeOptions).
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
  detectedForUrl: null,
  viewportX: 0,
  viewportY: 0,
  viewportZoom: 1.0,
  iframeWidth: 1920,
  iframeHeight: 1080,
  refreshIntervalSec: 0,
  hideSelectors: '',
  showDebugOverlay: false,
};

/**
 * Shortest auto-refresh interval, in seconds. Each refresh reloads the page,
 * and in proxy mode each refresh is a backend request that counts against the
 * proxy rate limits, so a smaller stored value is raised to this minimum.
 */
export const MIN_REFRESH_INTERVAL_SEC = 5;

/** Zero or less disables auto-refresh; a positive value is whole seconds, at least the minimum. */
function toRefreshInterval(value: unknown): number {
  const n = toFiniteNumber(value);
  if (n === undefined || n <= 0) {
    return DEFAULT_PANEL_OPTIONS.refreshIntervalSec;
  }
  return Math.max(MIN_REFRESH_INTERVAL_SEC, Math.round(n));
}

type LoadMode = PanelOptions['loadMode'];
type DetectedMode = Exclude<PanelOptions['detectedMode'], null>;

const LOAD_MODES: readonly LoadMode[] = ['auto', 'direct', 'proxy'];
const DETECTED_MODES: readonly DetectedMode[] = ['direct', 'proxy'];

/**
 * Reads a number from a stored option value. Accepts a finite number or a
 * numeric string (hand-edited or provisioned dashboard JSON). Returns
 * `undefined` for anything else, including `null`, `''`, `NaN` and `Infinity`.
 */
function toFiniteNumber(value: unknown): number | undefined {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : undefined;
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const n = Number(value);
    return Number.isFinite(n) ? n : undefined;
  }
  return undefined;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return allowed.find((candidate) => candidate === value);
}

/** A positive dimension in pixels, or the default. */
function toDimension(value: unknown, fallback: number): number {
  const n = toFiniteNumber(value);
  return n !== undefined && n > 0 ? n : fallback;
}

/**
 * Builds a complete, valid `PanelOptions` from stored options.
 *
 * Saved, provisioned or hand-edited dashboard JSON can hold any value, so each
 * field is coerced on its own and falls back to its default when the value is
 * missing or has the wrong type:
 * - `url`, `detectedForUrl` and `hideSelectors`: strings (`detectedForUrl` may
 *   be null). `url` is trimmed, so every consumer sees the URL that loads.
 * - `loadMode` and `detectedMode`: one of their allowed values.
 * - `viewportX` and `viewportY`: finite numbers. Negative values are valid:
 *   they move the page right or down inside the panel, and the preview can pan
 *   and zoom past the top-left edge of the page.
 * - `viewportZoom`: a finite number, clamped with {@link clampZoom}.
 * - `iframeWidth` and `iframeHeight`: positive numbers.
 * - `refreshIntervalSec`: zero or less disables auto-refresh. A positive value
 *   is rounded to whole seconds and raised to {@link MIN_REFRESH_INTERVAL_SEC}.
 * - `showDebugOverlay`: `true` only for the boolean `true`.
 *
 * The function is pure and accepts any partial or unknown input.
 */
export function normalizeOptions(partial: Partial<PanelOptions> | null | undefined): PanelOptions {
  const raw = (typeof partial === 'object' && partial !== null ? partial : {}) as Record<string, unknown>;
  const d = DEFAULT_PANEL_OPTIONS;

  const zoom = toFiniteNumber(raw.viewportZoom);

  return {
    url: typeof raw.url === 'string' ? raw.url.trim() : d.url,
    loadMode: oneOf(raw.loadMode, LOAD_MODES) ?? d.loadMode,
    detectedMode: oneOf(raw.detectedMode, DETECTED_MODES) ?? d.detectedMode,
    detectedForUrl: typeof raw.detectedForUrl === 'string' ? raw.detectedForUrl : d.detectedForUrl,
    viewportX: toFiniteNumber(raw.viewportX) ?? d.viewportX,
    viewportY: toFiniteNumber(raw.viewportY) ?? d.viewportY,
    viewportZoom: zoom === undefined ? d.viewportZoom : clampZoom(zoom),
    iframeWidth: toDimension(raw.iframeWidth, d.iframeWidth),
    iframeHeight: toDimension(raw.iframeHeight, d.iframeHeight),
    refreshIntervalSec: toRefreshInterval(raw.refreshIntervalSec),
    hideSelectors: typeof raw.hideSelectors === 'string' ? raw.hideSelectors : d.hideSelectors,
    showDebugOverlay: raw.showDebugOverlay === true,
  };
}

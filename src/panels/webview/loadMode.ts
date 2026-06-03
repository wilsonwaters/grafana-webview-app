import type { PanelOptions } from '../../types';

/**
 * Plugin id of the host app plugin. Single source of truth for building
 * plugin resource URLs (mirrors src/plugin.json `id`).
 */
const PLUGIN_ID = 'wilsonwaters-webview-app';

/**
 * Base path for the backend proxy resource handler.
 *
 * `GET /proxy?url=<encoded>[&hide=<encoded selector> ...]` streams the upstream
 * page through the backend so it can be framed same-origin to Grafana, applying
 * each `hide` CSS selector during HTML rewriting (see content-rewriting task CR5).
 */
export const PROXY_RESOURCE_BASE = `/api/plugins/${PLUGIN_ID}/resources/proxy`;

/**
 * Resolves the effective load mode for the view-mode render.
 *
 * - `'direct'` / `'proxy'` are honoured verbatim (manual override).
 * - `'auto'` defers to the mode detected at config time (`detectedMode`); when
 *   nothing was detected (`null`) it falls back to `'direct'`. View mode never
 *   re-detects.
 *
 * Pure function — safe to unit test in isolation.
 */
export function resolveLoadMode(opts: PanelOptions): 'direct' | 'proxy' {
  if (opts.loadMode === 'direct') {
    return 'direct';
  }
  if (opts.loadMode === 'proxy') {
    return 'proxy';
  }
  // 'auto' — use the stored detection result, defaulting to direct.
  return opts.detectedMode ?? 'direct';
}

/**
 * Builds the iframe `src` for proxy mode: the backend proxy resource URL with
 * the target `url` and one `hide` query param per non-empty CSS selector.
 *
 * `hideSelectors` is a newline-separated list; blank/whitespace-only lines are
 * dropped and each remaining selector is trimmed. Encoding is handled by
 * `URLSearchParams` so URLs and selectors with special characters are safe.
 *
 * Pure function — safe to unit test in isolation.
 */
export function buildProxySrc(opts: PanelOptions): string {
  const params = new URLSearchParams();
  params.set('url', opts.url);

  for (const selector of opts.hideSelectors.split('\n')) {
    const trimmed = selector.trim();
    if (trimmed.length > 0) {
      params.append('hide', trimmed);
    }
  }

  return `${PROXY_RESOURCE_BASE}?${params.toString()}`;
}

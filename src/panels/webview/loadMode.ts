import { PLUGIN_ID } from '../../constants';
import type { PanelOptions } from '../../types';
import { parseHideSelectors } from './hideSelectors';

/**
 * Base path for the backend proxy resource handler.
 *
 * `GET /proxy?url=<encoded>[&hide=<encoded selector> ...]` fetches the upstream
 * page through the backend and returns the rewritten HTML, applying each `hide`
 * CSS selector during HTML rewriting (see content-rewriting task CR5). The panel
 * reads this HTML with `getBackendSrv().fetch` and renders it with `srcdoc`
 * (FR5). It never loads this route as an iframe `src`: Grafana sends
 * `X-Frame-Options: deny` and `Content-Security-Policy: sandbox` on every
 * plugin resource response.
 */
export const PROXY_RESOURCE_BASE = `/api/plugins/${PLUGIN_ID}/resources/proxy`;

/** The load mode a panel actually renders with, after `auto` is resolved. */
export type ResolvedLoadMode = 'direct' | 'proxy';

/**
 * The stored Test URL verdict, if it applies to the current URL.
 *
 * - No verdict (`detectedMode` is null): null.
 * - The verdict was measured for another URL (`detectedForUrl` differs from the
 *   trimmed `url`): null. The author changed the URL without testing it again,
 *   so the old verdict says nothing about the new site.
 * - `detectedForUrl` is null: the panel was saved before the field existed. The
 *   verdict is kept, so existing dashboards render as before. The panel
 *   migration (`migratePanelOptions`) records the saved URL on load, so a later
 *   URL edit is detected.
 *
 * Pure function.
 */
export function detectedModeForUrl(opts: PanelOptions): PanelOptions['detectedMode'] {
  if (opts.detectedMode === null || opts.detectedForUrl === null) {
    return opts.detectedMode;
  }
  return opts.detectedForUrl.trim() === opts.url.trim() ? opts.detectedMode : null;
}

/**
 * Resolves the effective load mode for the view-mode render.
 *
 * - `'direct'` / `'proxy'` are honoured verbatim (manual override).
 * - `'auto'` defers to the mode detected at config time for this URL (see
 *   {@link detectedModeForUrl}); when there is none it falls back to
 *   `'direct'`. View mode never re-detects.
 *
 * Pure function — safe to unit test in isolation.
 */
export function resolveLoadMode(opts: PanelOptions): ResolvedLoadMode {
  if (opts.loadMode === 'direct') {
    return 'direct';
  }
  if (opts.loadMode === 'proxy') {
    return 'proxy';
  }
  // 'auto' — use the stored detection result for this URL, defaulting to direct.
  return detectedModeForUrl(opts) ?? 'direct';
}

/**
 * Builds the request path for the proxied HTML of `url` (FR5): the backend proxy
 * resource path with the target `url` and one `hide` query param per non-empty
 * CSS selector.
 *
 * `hideSelectors` is a newline-separated list; blank/whitespace-only lines are
 * dropped and each remaining selector is trimmed. Selectors the backend would
 * refuse, and any beyond the first `MAX_HIDE_SELECTORS`, are left out
 * (see {@link parseHideSelectors}); the editor shows why. Encoding is handled
 * by `URLSearchParams` so URLs and selectors with special characters are safe.
 *
 * The path is root-relative and has no sub-url prefix. `getBackendSrv()` removes
 * the leading `/` from local URLs, so the request resolves against Grafana's
 * `<base href>`, which already includes `config.appSubUrl` (for example
 * `root_url = https://host/grafana/`). Adding the sub-url here would double it.
 *
 * Pure function — safe to unit test in isolation.
 */
export function buildProxyFetchUrl(url: string, hideSelectors: string): string {
  const params = new URLSearchParams();
  params.set('url', url);

  for (const selector of parseHideSelectors(hideSelectors).selectors) {
    params.append('hide', selector);
  }

  return `${PROXY_RESOURCE_BASE}?${params.toString()}`;
}

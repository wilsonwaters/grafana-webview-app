/**
 * The iframe `sandbox` value for each load mode (security review VR-2).
 *
 * Every Web View iframe (the view-mode panel and the editor preview) takes its
 * `sandbox` value from {@link iframeSandbox}. Do not write a sandbox literal in a
 * component.
 *
 * - Proxy mode renders the proxied HTML with `srcdoc`. A `srcdoc` document has
 *   the origin of the page that embeds it, which is Grafana. With
 *   `allow-same-origin` its scripts could read the Grafana session and call the
 *   Grafana API. Proxy mode therefore gets `allow-scripts` only: the scripts run
 *   in an opaque origin.
 * - Direct mode loads the URL with `src`, so the document has the origin of the
 *   URL. `allow-same-origin` lets a cross-origin page keep its own origin (its
 *   own cookies and storage) and gives no access to Grafana. A direct URL on the
 *   Grafana origin would get Grafana-origin access, so it gets `allow-scripts`
 *   only.
 *
 * Never add `allow-popups`, `allow-forms`, `allow-modals` or
 * `allow-top-navigation*`.
 */
import type { ResolvedLoadMode } from './loadMode';

/** Scripts run in an opaque origin. Used for proxy mode and same-origin direct URLs. */
export const SANDBOX_OPAQUE_ORIGIN = 'allow-scripts';

/** Scripts run in the frame's own origin. Used only for cross-origin direct URLs. */
export const SANDBOX_CROSS_ORIGIN = 'allow-scripts allow-same-origin';

function originOf(url: string): string | undefined {
  try {
    return new URL(url).origin;
  } catch {
    return undefined;
  }
}

/**
 * Returns the `sandbox` value for an iframe that shows `url` in `mode`.
 *
 * @param mode - The resolved load mode.
 * @param url - The validated target URL (see `validateWebViewUrl`).
 * @param hostOrigin - The origin of the Grafana page. Defaults to `window.location.origin`.
 */
export function iframeSandbox(
  mode: ResolvedLoadMode,
  url: string,
  hostOrigin: string = window.location.origin
): string {
  if (mode === 'proxy') {
    return SANDBOX_OPAQUE_ORIGIN;
  }
  const targetOrigin = originOf(url);
  if (targetOrigin === undefined || targetOrigin === 'null' || targetOrigin === hostOrigin) {
    return SANDBOX_OPAQUE_ORIGIN;
  }
  return SANDBOX_CROSS_ORIGIN;
}

import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, PanelProps } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import { buildViewportTransform } from '../viewport';
import { buildProxySrc, resolveLoadMode } from '../loadMode';
import { useBackendAvailable } from '../useBackendAvailable';
import { webViewPanelTestIds } from './testIds';

type Props = PanelProps<PanelOptions>;

/**
 * Web View panel — view-mode render slice (PC1 + PC5).
 *
 * Renders the configured URL in a sandboxed iframe sized to the virtual
 * `iframeWidth`×`iframeHeight` dimensions, with the saved viewport applied via
 * a CSS transform (`scale(zoom) translate(-X, -Y)`, transform-origin top left).
 * The iframe is clipped by an `overflow: hidden` container and is
 * non-interactive (`pointer-events: none`) — the viewer sees exactly the region
 * the author configured.
 *
 * PC5 additions:
 * - **Auto-refresh**: when `refreshIntervalSec > 0`, a `setInterval` bumps a
 *   counter key each interval, which causes React to remount the iframe with a
 *   fresh load. The timer is torn down on unmount and re-armed when the interval
 *   value changes. A counter-key approach avoids any module-level shared state,
 *   so two panel instances each maintain their own independent refresh cycle.
 * - **Debug overlay**: when `showDebugOverlay` is true we render a small overlay
 *   **in our own DOM** (not inside the iframe — cross-origin DOM is inaccessible).
 *   The overlay is theme-aware via `useStyles2`.
 * - **hide-selectors** (`hideSelectors`): this option is intentionally NOT acted
 *   on here. Injecting CSS into a cross-origin iframe requires DOM access which
 *   the browser blocks (same-origin policy). `hideSelectors` is preserved in the
 *   schema and will be applied server-side during HTML rewriting in proxy mode —
 *   see stream content-rewriting task CR5.
 *
 * Multi-instance safety: no module-level mutable state, no fixed DOM ids. Each
 * component instance is independently keyed by its own React state (refreshKey),
 * so multiple panels on the same dashboard cannot interfere.
 *
 * Load-mode resolution (FR4): the effective mode is resolved via
 * `resolveLoadMode` (manual `direct`/`proxy` win; `auto` uses the stored
 * `detectedMode`, defaulting to direct). In `direct` mode the iframe `src` is
 * the raw target URL (cross-origin); in `proxy` mode it is the backend proxy
 * resource URL built by `buildProxySrc` (same-origin to Grafana), which carries
 * the `hideSelectors` as repeated `hide=` query params for server-side CSS
 * rewriting (CR5). View mode never re-detects.
 *
 * DF3 — proxy view-mode guard: a proxy iframe `src` is only loadable when the
 * backend proxy resource is actually serving. We consult the shared
 * per-session liveness probe (`useBackendAvailable`, DF1) and, *only for proxy
 * mode*, gate the iframe behind it:
 * - while the probe is in flight → a neutral "checking" placeholder (never the
 *   proxy iframe, to avoid a flash of a broken frame);
 * - settled unavailable → an accessible fallback message (no iframe);
 * - settled available → the proxy iframe exactly as before.
 *
 * Direct mode is completely unaffected: it needs no backend, so it renders the
 * raw-URL iframe immediately and NEVER waits on the probe.
 *
 * "Attempt direct where the URL permits" (the issue's secondary clause):
 * deliberately NOT done. A site is only ever resolved to proxy mode because it
 * was determined to block framing (manual `proxy`, or `auto` + detected proxy);
 * framability cannot be re-checked at view time. Silently swapping in a raw
 * direct iframe would therefore most likely reproduce the very broken frame the
 * proxy was meant to avoid (X-Frame-Options / frame-ancestors). We instead show
 * the clear fallback. See PR body for the full rationale.
 */
export function WebViewPanel({ options, width, height }: Props) {
  const styles = useStyles2(getStyles);

  // DF3: consult the shared backend-availability probe (DF1). Called
  // unconditionally at the top to satisfy the Rules of Hooks; the result is
  // only consumed in the proxy branch below (direct mode ignores it entirely).
  const { loading: backendLoading, backendAvailable } = useBackendAvailable();

  // Normalise so the component is robust against partial/legacy saved options.
  const opts = normalizeOptions(options);

  // PC5: auto-refresh key — incrementing this causes React to remount the iframe,
  // which triggers a fresh load of the src URL. We use a counter rather than
  // setting src directly so we never mutate the DOM imperatively, and each panel
  // instance owns its own counter (no shared mutable state).
  //
  // Note: the `refreshKey` value is not consumed in JSX directly (it is passed
  // as the iframe's React `key` prop inside the conditional below). We only need
  // the setter here; the linter may warn about the unused value destructuring —
  // suppressing via `_refreshKey` prefix to signal intentional discard.
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    if (opts.refreshIntervalSec <= 0) {
      // Auto-refresh disabled — nothing to arm.
      return;
    }

    const intervalMs = opts.refreshIntervalSec * 1000;
    // `setRefreshKey` is the stable setter from useState — React guarantees it
    // does not change across renders, so there is no stale-closure risk.
    const id = setInterval(() => {
      setRefreshKey((k) => k + 1);
    }, intervalMs);

    // Cleanup: fires on unmount and before re-running when refreshIntervalSec changes.
    return () => {
      clearInterval(id);
    };
  }, [opts.refreshIntervalSec, setRefreshKey]);

  // Resolved load mode ('direct' | 'proxy'): manual modes win, 'auto' uses the
  // stored detectedMode (defaulting to direct). Drives both the iframe src and
  // the debug overlay.
  const resolvedMode = resolveLoadMode(opts);

  // Empty / blank URL: render a clear empty state rather than an iframe with an
  // empty src.
  if (!opts.url.trim()) {
    return (
      <div className={styles.container} style={{ width, height }} data-testid={webViewPanelTestIds.container}>
        <div className={styles.empty} data-testid={webViewPanelTestIds.placeholder}>
          No URL configured. Set a URL in the panel options.
        </div>
      </div>
    );
  }

  const transform = buildViewportTransform(opts);

  // The debug overlay is rendered in our own DOM (not inside the iframe, which is
  // cross-origin and inaccessible). It is shown alongside whatever the body of
  // the panel resolves to below — including the proxy fallback/loading states —
  // so authors can always see the resolved mode. Visible only when enabled.
  const debugOverlay = opts.showDebugOverlay ? (
    <div className={styles.debugOverlay} data-testid={webViewPanelTestIds.debugOverlay}>
      <span>mode: {resolvedMode}</span>
      <span>
        X: {opts.viewportX} Y: {opts.viewportY}
      </span>
      <span>zoom: {opts.viewportZoom}</span>
    </div>
  ) : null;

  const renderContainer = (body: React.ReactNode) => (
    <div
      className={styles.container}
      style={{ width, height, overflow: 'hidden' }}
      data-testid={webViewPanelTestIds.container}
    >
      {body}
      {debugOverlay}
    </div>
  );

  const renderIframe = (src: string) => (
    <iframe
      // PC5: refreshKey forces a fresh iframe mount on each auto-refresh tick.
      // Using `key` is intentional: it causes React to unmount/remount the
      // iframe element, which reloads the src. No shared state across instances.
      key={refreshKey}
      title="Web View"
      src={src}
      data-testid={webViewPanelTestIds.iframe}
      className={styles.iframe}
      // SECURITY: never broaden this sandbox value.
      sandbox="allow-scripts allow-same-origin"
      referrerPolicy="no-referrer"
      style={{
        width: opts.iframeWidth,
        height: opts.iframeHeight,
        transform,
        transformOrigin: 'top left',
      }}
    />
  );

  // DF3: proxy mode is the ONLY mode gated on the backend probe — a proxy `src`
  // points at the backend proxy resource, which cannot serve content when the
  // backend is unavailable. Direct mode falls through to the render below and is
  // never affected by the probe (it loads the raw URL cross-origin).
  if (resolvedMode === 'proxy') {
    // Probe still in flight: render a neutral placeholder rather than the proxy
    // iframe, so we never flash a broken frame before the probe settles. This is
    // a transient state; once settled we either render the iframe or the
    // fallback below.
    if (backendLoading) {
      return renderContainer(
        <div className={styles.empty} data-testid={webViewPanelTestIds.backendLoading}>
          Checking backend availability…
        </div>
      );
    }

    // Settled unavailable: the backend proxy cannot serve this content. Show a
    // clear, accessible fallback (real text, reusing the empty-state container)
    // instead of a guaranteed-to-fail proxy iframe. We deliberately do NOT fall
    // back to a raw direct iframe here — see the component-level rationale above.
    if (!backendAvailable) {
      return renderContainer(
        <div className={styles.empty} data-testid={webViewPanelTestIds.proxyUnavailable}>
          This panel is configured to load its content through the backend proxy, which is currently
          unavailable. The content can’t be shown until the backend proxy is running.
        </div>
      );
    }

    // Settled available: render the proxy iframe exactly as before.
    return renderContainer(renderIframe(buildProxySrc(opts)));
  }

  // Direct mode: load the raw URL (cross-origin) immediately — no backend needed,
  // never waits on the probe.
  return renderContainer(renderIframe(opts.url));
}

const getStyles = (theme: GrafanaTheme2) => ({
  container: css`
    position: relative;
    width: 100%;
    height: 100%;
    overflow: hidden;
    background: ${theme.colors.background.canvas};
  `,
  iframe: css`
    border: none;
    /* View mode is non-interactive: the viewer cannot pan/zoom/click. */
    pointer-events: none;
  `,
  empty: css`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: ${theme.spacing(2)};
    text-align: center;
    color: ${theme.colors.text.secondary};
  `,
  debugOverlay: css`
    position: absolute;
    bottom: ${theme.spacing(1)};
    left: ${theme.spacing(1)};
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(0.5)};
    padding: ${theme.spacing(0.5)} ${theme.spacing(1)};
    background: ${theme.colors.background.secondary};
    border: 1px solid ${theme.colors.border.weak};
    border-radius: ${theme.shape.radius.default};
    font-family: ${theme.typography.fontFamilyMonospace};
    font-size: ${theme.typography.bodySmall.fontSize};
    color: ${theme.colors.text.primary};
    /* The overlay must sit above the (pointer-events:none) iframe. */
    pointer-events: none;
    z-index: 1;
    opacity: 0.9;
  `,
});

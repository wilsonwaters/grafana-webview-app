import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, PanelProps } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import { buildViewportTransform } from '../viewport';
import { resolveLoadMode } from '../loadMode';
import { webViewPanelTestIds } from './testIds';
import { WebViewFrame, type WebViewFrameTestIds } from './WebViewFrame';

type Props = PanelProps<PanelOptions>;

/** The panel's test ids for the elements that the shared frame renders. */
const frameTestIds: WebViewFrameTestIds = {
  iframe: webViewPanelTestIds.iframe,
  invalidUrl: webViewPanelTestIds.invalidUrl,
  backendLoading: webViewPanelTestIds.backendLoading,
  proxyUnavailable: webViewPanelTestIds.proxyUnavailable,
  proxyLoading: webViewPanelTestIds.proxyLoading,
  proxyError: webViewPanelTestIds.proxyError,
};

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
 * The iframe itself (URL validation, direct and proxy rendering, sandbox, DF3
 * backend guard) comes from the shared {@link WebViewFrame}, which the editor
 * preview uses too. See that component for the security notes (VR-1, VR-2) and
 * the proxy-mode render (FR5: fetch the rewritten HTML, render it with
 * `srcdoc`).
 *
 * PC5 additions:
 * - **Auto-refresh**: when `refreshIntervalSec > 0`, a `setInterval` bumps a
 *   counter key each interval. Direct mode remounts the iframe (a fresh load);
 *   proxy mode refetches the HTML and swaps it in when it arrives. The timer is
 *   torn down on unmount and re-armed when the interval value changes. A
 *   counter-key approach avoids any module-level shared state, so two panel
 *   instances each maintain their own independent refresh cycle.
 * - **Debug overlay**: when `showDebugOverlay` is true we render a small overlay
 *   **in our own DOM** (not inside the iframe). The overlay is theme-aware via
 *   `useStyles2`.
 * - **hide-selectors** (`hideSelectors`): applied server-side during HTML
 *   rewriting in proxy mode (CR5). The frame passes them as `hide=` params.
 *
 * Multi-instance safety: no module-level mutable state, no fixed DOM ids. Each
 * component instance is independently keyed by its own React state (refreshKey),
 * so multiple panels on the same dashboard cannot interfere.
 *
 * Load-mode resolution (FR4): the effective mode is resolved via
 * `resolveLoadMode` (manual `direct`/`proxy` win; `auto` uses the stored
 * `detectedMode`, defaulting to direct). View mode never re-detects.
 */
export function WebViewPanel({ options, width, height }: Props) {
  const styles = useStyles2(getStyles);

  // Normalise so the component is robust against partial/legacy saved options.
  const opts = normalizeOptions(options);

  // PC5: auto-refresh key — incrementing this reloads the frame (see above).
  // Each panel instance owns its own counter (no shared mutable state).
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
  // stored detectedMode (defaulting to direct). Drives both the frame and the
  // debug overlay.
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

  // The debug overlay is rendered in our own DOM. It is shown alongside whatever
  // the frame renders — including the error, loading and fallback states — so
  // authors can always see the resolved mode. Visible only when enabled.
  const debugOverlay = opts.showDebugOverlay ? (
    <div className={styles.debugOverlay} data-testid={webViewPanelTestIds.debugOverlay}>
      <span>mode: {resolvedMode}</span>
      <span>
        X: {opts.viewportX} Y: {opts.viewportY}
      </span>
      <span>zoom: {opts.viewportZoom}</span>
    </div>
  ) : null;

  return (
    <div
      className={styles.container}
      style={{ width, height, overflow: 'hidden' }}
      data-testid={webViewPanelTestIds.container}
    >
      <WebViewFrame
        url={opts.url}
        mode={resolvedMode}
        hideSelectors={opts.hideSelectors}
        reloadKey={refreshKey}
        title="Web View"
        testIds={frameTestIds}
        frameStyle={{
          width: opts.iframeWidth,
          height: opts.iframeHeight,
          transform: buildViewportTransform(opts),
          transformOrigin: 'top left',
        }}
      />
      {debugOverlay}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  container: css`
    position: relative;
    width: 100%;
    height: 100%;
    overflow: hidden;
    background: ${theme.colors.background.canvas};
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

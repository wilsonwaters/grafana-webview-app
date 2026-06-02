import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, PanelProps } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import { buildViewportTransform } from '../viewport';
import { webViewPanelTestIds } from './testIds';

type Props = PanelProps<PanelOptions>;

/**
 * Web View panel — view-mode render slice (PC1).
 *
 * Renders the configured URL in a sandboxed iframe sized to the virtual
 * `iframeWidth`×`iframeHeight` dimensions, with the saved viewport applied via
 * a CSS transform (`scale(zoom) translate(-X, -Y)`, transform-origin top left).
 * The iframe is clipped by an `overflow: hidden` container and is
 * non-interactive (`pointer-events: none`) — the viewer sees exactly the region
 * the author configured.
 *
 * Direct mode only at this stage: `iframe.src` is set directly to the target
 * URL with no backend involvement. `loadMode: 'proxy'` is not yet wired (the
 * proxy stream supplies the proxy src); for now it renders the same direct
 * iframe — see note below.
 *
 * The static viewport render is identical in edit and view mode for now, so we
 * do NOT branch on editor context here (PC3 introduces the interactive editor).
 */
export function WebViewPanel({ options, width, height }: Props) {
  const styles = useStyles2(getStyles);
  // Normalise so the component is robust against partial/legacy saved options.
  const opts = normalizeOptions(options);

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

  // NOTE: loadMode 'proxy'/'auto' are not yet wired to the backend (proxy
  // stream). Until then we always load the URL directly into the iframe.
  const src = opts.url;

  const transform = buildViewportTransform(opts);

  return (
    <div
      className={styles.container}
      style={{ width, height, overflow: 'hidden' }}
      data-testid={webViewPanelTestIds.container}
    >
      <iframe
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
});

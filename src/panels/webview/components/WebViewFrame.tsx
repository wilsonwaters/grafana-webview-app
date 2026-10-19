import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { buildProxyFetchUrl, type ResolvedLoadMode } from '../loadMode';
import { iframeSandbox } from '../sandbox';
import { validateWebViewUrl } from '../url';
import { useBackendAvailable } from '../useBackendAvailable';
import { useProxyDocument } from '../useProxyDocument';

/** The `data-testid` of each element the frame can render. Each host passes its own set. */
export interface WebViewFrameTestIds {
  iframe: string;
  invalidUrl: string;
  backendLoading: string;
  proxyUnavailable: string;
  proxyLoading: string;
  proxyError: string;
}

export interface WebViewFrameProps {
  /** The `url` panel option as saved. It is validated here before any iframe is built. */
  url: string;
  /** The resolved load mode. */
  mode: ResolvedLoadMode;
  /** Newline-separated CSS selectors that the backend hides in proxy mode (CR5). */
  hideSelectors: string;
  /** Changing this value reloads the frame: a remount in direct mode, a refetch in proxy mode. */
  reloadKey: number;
  /** Accessible name of the iframe. */
  title: string;
  /** Size and viewport transform of the iframe. */
  frameStyle: React.CSSProperties;
  testIds: WebViewFrameTestIds;
}

/**
 * The one iframe renderer for the Web View panel and the editor preview.
 *
 * - URL safety (VR-1): the URL is validated first. An unsafe URL renders an
 *   accessible error and never an iframe, in every mode.
 * - Direct mode: `<iframe src={url}>`. It needs no backend.
 * - Proxy mode (FR5): waits for the backend probe (DF3), fetches the rewritten
 *   HTML from the backend proxy as text and renders it with `srcdoc`. Grafana
 *   sends `X-Frame-Options: deny` and `CSP: sandbox` on plugin resource
 *   responses, so the resource route is never loaded as an iframe `src`.
 * - Sandbox (VR-2): the value comes from `iframeSandbox`. A `srcdoc` document
 *   has the Grafana origin, so proxy mode gets `allow-scripts` only and its
 *   scripts run in an opaque origin.
 * - The panel never listens for `message` events: nothing posted by the framed
 *   page is trusted.
 */
export function WebViewFrame(props: WebViewFrameProps) {
  const styles = useStyles2(getStyles);
  const check = validateWebViewUrl(props.url);

  if (!check.valid) {
    return (
      <div className={styles.message} role="alert" data-testid={props.testIds.invalidUrl}>
        This URL cannot be displayed. {check.message}
      </div>
    );
  }

  if (props.mode === 'proxy') {
    return <ProxyFrame {...props} url={check.url} />;
  }

  return (
    <iframe
      // Remounting the iframe reloads the page (auto-refresh).
      key={props.reloadKey}
      title={props.title}
      src={check.url}
      data-testid={props.testIds.iframe}
      className={styles.iframe}
      sandbox={iframeSandbox('direct', check.url)}
      referrerPolicy="no-referrer"
      style={props.frameStyle}
    />
  );
}

/**
 * DF3 guard for proxy mode: the proxy needs the plugin backend. While the
 * shared probe runs, show a neutral placeholder; if the backend is unavailable,
 * show a fallback. It never falls back to a direct iframe: a URL is in proxy
 * mode because it blocks framing.
 */
function ProxyFrame(props: WebViewFrameProps) {
  const styles = useStyles2(getStyles);
  const { loading, backendAvailable } = useBackendAvailable();

  if (loading) {
    return (
      <div className={styles.message} data-testid={props.testIds.backendLoading}>
        Checking backend availability…
      </div>
    );
  }

  if (!backendAvailable) {
    return (
      <div className={styles.message} data-testid={props.testIds.proxyUnavailable}>
        This panel is configured to load its content through the backend proxy, which is currently unavailable. The
        content can’t be shown until the backend proxy is running.
      </div>
    );
  }

  return <ProxyDocumentFrame {...props} />;
}

/** Fetches the proxied HTML and renders it with `srcdoc` (FR5). */
function ProxyDocumentFrame(props: WebViewFrameProps) {
  const styles = useStyles2(getStyles);
  const proxyDocument = useProxyDocument(buildProxyFetchUrl(props.url, props.hideSelectors), props.reloadKey);

  if (proxyDocument.status === 'loading') {
    return (
      <div className={styles.message} role="status" data-testid={props.testIds.proxyLoading}>
        Loading the page through the backend proxy…
      </div>
    );
  }

  if (proxyDocument.status === 'error') {
    // The error body is shown as text only. It is never rendered as a document.
    return (
      <div className={styles.message} role="alert" data-testid={props.testIds.proxyError}>
        <div>
          The page could not be loaded through the backend proxy
          {proxyDocument.httpStatus !== undefined ? ` (HTTP ${proxyDocument.httpStatus})` : ''}.
        </div>
        {proxyDocument.message && <div className={styles.detail}>{proxyDocument.message}</div>}
      </div>
    );
  }

  return (
    <iframe
      // A new key for each response reloads the frame, even when the HTML is unchanged.
      key={proxyDocument.version}
      title={props.title}
      srcDoc={proxyDocument.html}
      data-testid={props.testIds.iframe}
      className={styles.iframe}
      sandbox={iframeSandbox('proxy', props.url)}
      referrerPolicy="no-referrer"
      style={props.frameStyle}
    />
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  iframe: css`
    border: none;
    /* The frame is not interactive: the panel is static, and the editor preview captures pan and zoom. */
    pointer-events: none;
  `,
  message: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(1)};
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: ${theme.spacing(2)};
    text-align: center;
    color: ${theme.colors.text.secondary};
  `,
  detail: css`
    font-family: ${theme.typography.fontFamilyMonospace};
    font-size: ${theme.typography.bodySmall.fontSize};
    word-break: break-word;
  `,
});

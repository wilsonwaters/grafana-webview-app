export const webViewPanelTestIds = {
  container: 'data-testid webview-panel-container',
  url: 'data-testid webview-panel-url',
  placeholder: 'data-testid webview-panel-placeholder',
  iframe: 'data-testid webview-panel-iframe',
  empty: 'data-testid webview-panel-empty',
  debugOverlay: 'data-testid webview-panel-debug-overlay',
  // DF3: shown in view mode when the resolved mode is proxy but the backend
  // probe settled unavailable — a clear fallback instead of a broken iframe.
  proxyUnavailable: 'data-testid webview-panel-proxy-unavailable',
  // DF3: neutral placeholder while the backend probe is in flight (proxy mode).
  backendLoading: 'data-testid webview-panel-backend-loading',
};

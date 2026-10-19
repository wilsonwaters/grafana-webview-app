export const viewportEditorTestIds = {
  /** The interactive preview container that captures mouse events. */
  preview: 'data-testid webview-viewport-editor-preview',
  /** The (pointer-events: none) iframe shown inside the preview. */
  iframe: 'data-testid webview-viewport-editor-iframe',
  /** The empty-URL hint shown when no URL is configured. */
  hint: 'data-testid webview-viewport-editor-hint',
  /** The error shown instead of the iframe when the URL is not an absolute http(s) URL (VR-1). */
  invalidUrl: 'data-testid webview-viewport-editor-invalid-url',
  /** Proxy mode: placeholder while the backend probe runs (DF3). */
  backendLoading: 'data-testid webview-viewport-editor-backend-loading',
  /** Proxy mode: fallback when the backend is unavailable (DF3). */
  proxyUnavailable: 'data-testid webview-viewport-editor-proxy-unavailable',
  /** Proxy mode: shown while the proxied HTML is being fetched (FR5). */
  proxyLoading: 'data-testid webview-viewport-editor-proxy-loading',
  /** Proxy mode: shown when the proxy request fails (FR5). */
  proxyError: 'data-testid webview-viewport-editor-proxy-error',
  /** The live X / Y / zoom readout. */
  readout: 'data-testid webview-viewport-editor-readout',
  /** Numeric input for viewportX. */
  inputX: 'data-testid webview-viewport-editor-input-x',
  /** Numeric input for viewportY. */
  inputY: 'data-testid webview-viewport-editor-input-y',
  /** Numeric input for viewportZoom. */
  inputZoom: 'data-testid webview-viewport-editor-input-zoom',
  /** URL text input. */
  inputUrl: 'data-testid webview-viewport-editor-input-url',
  /** Numeric input for iframeWidth. */
  inputWidth: 'data-testid webview-viewport-editor-input-width',
  /** Numeric input for iframeHeight. */
  inputHeight: 'data-testid webview-viewport-editor-input-height',
  /** "Reset view" button. */
  resetButton: 'data-testid webview-viewport-editor-reset',
};

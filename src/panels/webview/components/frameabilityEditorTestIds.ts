export const frameabilityEditorTestIds = {
  /** The "Test URL" button that triggers the /check-frameable request. */
  testButton: 'data-testid webview-frameability-test-button',
  /** The result/feedback region (Alert) shown after a check completes. */
  result: 'data-testid webview-frameability-result',
  /** The note (Alert) shown when the backend is unavailable. */
  backendUnavailableNote: 'data-testid webview-frameability-backend-unavailable-note',
  /** The hint shown when the URL is not a valid http(s) URL (the button is disabled). */
  invalidUrlHint: 'data-testid webview-frameability-invalid-url-hint',
  /** The hint shown in Auto mode when the saved verdict was measured for another URL. */
  untestedNote: 'data-testid webview-frameability-untested-note',
};

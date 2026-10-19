import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../types';
import { buildProxyFetchUrl, PROXY_RESOURCE_BASE, resolveLoadMode } from './loadMode';

function opts(overrides: Partial<PanelOptions> = {}): PanelOptions {
  return { ...DEFAULT_PANEL_OPTIONS, ...overrides };
}

describe('panels/webview/loadMode', () => {
  // ---------------------------------------------------------------------------
  // resolveLoadMode — Completion Criterion: "Auto resolves to detectedMode;
  // manual options override loadMode"
  // ---------------------------------------------------------------------------
  describe('resolveLoadMode', () => {
    test('manual direct → direct', () => {
      expect(resolveLoadMode(opts({ loadMode: 'direct' }))).toBe('direct');
    });

    test('manual proxy → proxy (overrides detectedMode)', () => {
      expect(resolveLoadMode(opts({ loadMode: 'proxy', detectedMode: 'direct' }))).toBe('proxy');
    });

    test('auto + detectedMode=direct → direct', () => {
      expect(resolveLoadMode(opts({ loadMode: 'auto', detectedMode: 'direct' }))).toBe('direct');
    });

    test('auto + detectedMode=proxy → proxy', () => {
      expect(resolveLoadMode(opts({ loadMode: 'auto', detectedMode: 'proxy' }))).toBe('proxy');
    });

    test('auto + detectedMode=null → direct (default when nothing detected)', () => {
      expect(resolveLoadMode(opts({ loadMode: 'auto', detectedMode: null }))).toBe('direct');
    });
  });

  // ---------------------------------------------------------------------------
  // buildProxyFetchUrl — FR5: the request path for the proxied HTML, incl. CR5
  // hide params. getBackendSrv() resolves it under config.appSubUrl.
  // ---------------------------------------------------------------------------
  describe('buildProxyFetchUrl', () => {
    test('builds the proxy resource path with the target url encoded', () => {
      const path = buildProxyFetchUrl('https://example.com/path?a=1&b=2', '');
      expect(path).toBe(`${PROXY_RESOURCE_BASE}?url=${encodeURIComponent('https://example.com/path?a=1&b=2')}`);
      // The raw upstream query separators must be encoded, not leak into ours.
      expect(path.startsWith(`${PROXY_RESOURCE_BASE}?url=https%3A%2F%2Fexample.com`)).toBe(true);
    });

    test('uses the single source-of-truth resource base', () => {
      const path = buildProxyFetchUrl('https://example.com', '');
      expect(path.startsWith(`${PROXY_RESOURCE_BASE}?`)).toBe(true);
      expect(PROXY_RESOURCE_BASE).toBe('/api/plugins/wilsonwaters-webview-app/resources/proxy');
    });

    test('the path is root-relative with no sub-url prefix (getBackendSrv adds config.appSubUrl)', () => {
      const path = buildProxyFetchUrl('https://example.com', '');
      expect(path.startsWith('/api/plugins/')).toBe(true);
    });

    test('empty hideSelectors → no hide params', () => {
      expect(buildProxyFetchUrl('https://example.com', '')).not.toContain('hide=');
    });

    test('multiple hideSelectors (newline-separated) → multiple hide params, blanks dropped and trimmed', () => {
      const path = buildProxyFetchUrl('https://example.com', '.ad\n\n  #banner  \n   \n.cookie-bar');
      const params = new URLSearchParams(path.split('?')[1]);
      expect(params.getAll('hide')).toEqual(['.ad', '#banner', '.cookie-bar']);
      expect(params.get('url')).toBe('https://example.com');
    });

    test('special characters in selectors are encoded', () => {
      const selector = 'div[data-x="a b"] > .c&d';
      const path = buildProxyFetchUrl('https://example.com', selector);
      // Reserved characters (`&`, `=`, `>`, brackets, quotes) must be encoded so the
      // selector cannot break out of the query. URLSearchParams uses
      // application/x-www-form-urlencoded, so spaces become `+`.
      expect(path).toContain(`hide=${encodeURIComponent(selector).replace(/%20/g, '+')}`);
      // The raw `&` separator from the selector must NOT leak as a query delimiter.
      expect(path).toContain('.c%26d');
      // Round-trips back to the original selector (decoding handles `+` as space).
      const params = new URLSearchParams(path.split('?')[1]);
      expect(params.getAll('hide')).toEqual([selector]);
    });
  });
});

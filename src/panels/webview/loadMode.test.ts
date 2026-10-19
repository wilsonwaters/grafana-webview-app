import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../types';
import { buildProxyFetchUrl, detectedModeForUrl, PROXY_RESOURCE_BASE, resolveLoadMode } from './loadMode';

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

    test('auto + a verdict measured for this URL → that verdict', () => {
      expect(
        resolveLoadMode(
          opts({ loadMode: 'auto', url: 'https://a.example', detectedMode: 'proxy', detectedForUrl: 'https://a.example' })
        )
      ).toBe('proxy');
    });

    test('auto + a verdict measured for another URL → not tested → direct (RV1-6)', () => {
      expect(
        resolveLoadMode(
          opts({ loadMode: 'auto', url: 'https://b.example', detectedMode: 'proxy', detectedForUrl: 'https://a.example' })
        )
      ).toBe('direct');
    });

    test('the URL comparison ignores surrounding whitespace', () => {
      expect(
        resolveLoadMode(
          opts({ loadMode: 'auto', url: '  https://a.example ', detectedMode: 'proxy', detectedForUrl: 'https://a.example' })
        )
      ).toBe('proxy');
    });

    test('auto + a verdict saved before detectedForUrl existed (null) → the verdict is kept', () => {
      expect(
        resolveLoadMode(opts({ loadMode: 'auto', url: 'https://a.example', detectedMode: 'proxy', detectedForUrl: null }))
      ).toBe('proxy');
    });

    test('manual proxy ignores a stale verdict', () => {
      expect(
        resolveLoadMode(
          opts({ loadMode: 'proxy', url: 'https://b.example', detectedMode: 'direct', detectedForUrl: 'https://a.example' })
        )
      ).toBe('proxy');
    });
  });

  describe('detectedModeForUrl', () => {
    test('null when nothing was detected', () => {
      expect(detectedModeForUrl(opts({ detectedMode: null, detectedForUrl: 'https://a.example' }))).toBeNull();
    });

    test('the verdict when it matches the URL, null when it does not', () => {
      const base = opts({ detectedMode: 'direct', detectedForUrl: 'https://a.example' });
      expect(detectedModeForUrl({ ...base, url: 'https://a.example' })).toBe('direct');
      expect(detectedModeForUrl({ ...base, url: 'https://a.example/other' })).toBeNull();
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

    test('selectors the proxy would refuse are not sent (CR5)', () => {
      const tooLong = '.' + 'a'.repeat(256);
      const hide = ['.ad', 'div:has(p)', 'p:contains(x)', '[href#=(ads)]', 'div:not(.a .b)', tooLong, '#banner'];
      const path = buildProxyFetchUrl('https://example.com', hide.join('\n'));
      const params = new URLSearchParams(path.split('?')[1]);
      expect(params.getAll('hide')).toEqual(['.ad', '#banner']);
    });

    test('at most 20 selectors are sent (CR5)', () => {
      const lines = Array.from({ length: 25 }, (_, i) => `.s${i + 1}`).join('\n');
      const params = new URLSearchParams(buildProxyFetchUrl('https://example.com', lines).split('?')[1]);
      expect(params.getAll('hide')).toHaveLength(20);
      expect(params.getAll('hide')[19]).toBe('.s20');
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

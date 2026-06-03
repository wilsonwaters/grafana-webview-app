import { config } from '@grafana/runtime';

import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../types';
import { buildProxySrc, PROXY_RESOURCE_BASE, resolveLoadMode } from './loadMode';

// Mock @grafana/runtime so `config.appSubUrl` is controllable per test. It
// defaults to '' (the root-served case), matching real Grafana when no sub-url
// is configured. Each test that needs a sub-path sets it and the afterEach
// restores it so other suites are unaffected.
jest.mock('@grafana/runtime', () => ({
  config: { appSubUrl: '' },
}));

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
  // buildProxySrc — Completion Criterion: "View-mode renders with the correct
  // src based on resolved mode" (proxy branch), incl. CR5 hide params.
  // ---------------------------------------------------------------------------
  describe('buildProxySrc', () => {
    // Restore the default ('' = root-served) after each test so a sub-path set
    // here cannot leak into other tests or suites sharing the mocked config.
    afterEach(() => {
      config.appSubUrl = '';
    });

    test('builds the proxy resource URL with the target url encoded', () => {
      const src = buildProxySrc(opts({ url: 'https://example.com/path?a=1&b=2' }));
      expect(src).toBe(
        `${PROXY_RESOURCE_BASE}?url=${encodeURIComponent('https://example.com/path?a=1&b=2')}`
      );
      // The raw upstream query separators must be encoded, not leak into ours.
      expect(src.startsWith(`${PROXY_RESOURCE_BASE}?url=https%3A%2F%2Fexample.com`)).toBe(true);
    });

    test('uses the single source-of-truth resource base', () => {
      const src = buildProxySrc(opts({ url: 'https://example.com' }));
      expect(src.startsWith(`${PROXY_RESOURCE_BASE}?`)).toBe(true);
      expect(PROXY_RESOURCE_BASE).toBe('/api/plugins/wilsonwaters-webview-app/resources/proxy');
    });

    test('default appSubUrl ("" = root-served) → root-relative resource path (unchanged)', () => {
      // Guard: the mocked default must be '' so the root case is byte-identical.
      expect(config.appSubUrl).toBe('');
      const src = buildProxySrc(opts({ url: 'https://example.com' }));
      expect(src).toBe(
        `${PROXY_RESOURCE_BASE}?url=${encodeURIComponent('https://example.com')}`
      );
    });

    test('appSubUrl set → resource path is prefixed with the sub-url (sub-path Grafana)', () => {
      config.appSubUrl = '/grafana';
      const src = buildProxySrc(opts({ url: 'https://example.com' }));
      expect(
        src.startsWith('/grafana/api/plugins/wilsonwaters-webview-app/resources/proxy?')
      ).toBe(true);
      expect(src).toBe(
        `/grafana${PROXY_RESOURCE_BASE}?url=${encodeURIComponent('https://example.com')}`
      );
    });

    test('empty hideSelectors → no hide params', () => {
      const src = buildProxySrc(opts({ url: 'https://example.com', hideSelectors: '' }));
      expect(src).not.toContain('hide=');
    });

    test('multiple hideSelectors (newline-separated) → multiple hide params, blanks dropped and trimmed', () => {
      const src = buildProxySrc(
        opts({ url: 'https://example.com', hideSelectors: '.ad\n\n  #banner  \n   \n.cookie-bar' })
      );
      const params = new URLSearchParams(src.split('?')[1]);
      expect(params.getAll('hide')).toEqual(['.ad', '#banner', '.cookie-bar']);
    });

    test('special characters in selectors are encoded', () => {
      const selector = 'div[data-x="a b"] > .c&d';
      const src = buildProxySrc(opts({ url: 'https://example.com', hideSelectors: selector }));
      // Reserved characters (`&`, `=`, `>`, brackets, quotes) must be encoded so the
      // selector cannot break out of the query. URLSearchParams uses
      // application/x-www-form-urlencoded, so spaces become `+`.
      expect(src).toContain(
        `hide=${encodeURIComponent(selector).replace(/%20/g, '+')}`
      );
      // The raw `&` separator from the selector must NOT leak as a query delimiter.
      expect(src).toContain('.c%26d');
      // Round-trips back to the original selector (decoding handles `+` as space).
      const params = new URLSearchParams(src.split('?')[1]);
      expect(params.getAll('hide')).toEqual([selector]);
    });
  });
});

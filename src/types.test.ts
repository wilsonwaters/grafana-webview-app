import { DEFAULT_PANEL_OPTIONS, MIN_REFRESH_INTERVAL_SEC, normalizeOptions, type PanelOptions } from './types';
import { VIEWPORT_ZOOM_MAX, VIEWPORT_ZOOM_MIN } from './panels/webview/viewport';

describe('DEFAULT_PANEL_OPTIONS', () => {
  test('url defaults to empty string', () => {
    expect(DEFAULT_PANEL_OPTIONS.url).toBe('');
  });

  test('loadMode defaults to auto', () => {
    expect(DEFAULT_PANEL_OPTIONS.loadMode).toBe('auto');
  });

  test('detectedMode defaults to null', () => {
    expect(DEFAULT_PANEL_OPTIONS.detectedMode).toBeNull();
  });

  test('detectedForUrl defaults to null', () => {
    expect(DEFAULT_PANEL_OPTIONS.detectedForUrl).toBeNull();
  });

  test('viewportX defaults to 0', () => {
    expect(DEFAULT_PANEL_OPTIONS.viewportX).toBe(0);
  });

  test('viewportY defaults to 0', () => {
    expect(DEFAULT_PANEL_OPTIONS.viewportY).toBe(0);
  });

  test('viewportZoom defaults to 1.0', () => {
    expect(DEFAULT_PANEL_OPTIONS.viewportZoom).toBe(1.0);
  });

  test('iframeWidth defaults to 1920', () => {
    expect(DEFAULT_PANEL_OPTIONS.iframeWidth).toBe(1920);
  });

  test('iframeHeight defaults to 1080', () => {
    expect(DEFAULT_PANEL_OPTIONS.iframeHeight).toBe(1080);
  });

  test('refreshIntervalSec defaults to 0', () => {
    expect(DEFAULT_PANEL_OPTIONS.refreshIntervalSec).toBe(0);
  });

  test('hideSelectors defaults to empty string', () => {
    expect(DEFAULT_PANEL_OPTIONS.hideSelectors).toBe('');
  });

  test('showDebugOverlay defaults to false', () => {
    expect(DEFAULT_PANEL_OPTIONS.showDebugOverlay).toBe(false);
  });
});

describe('normalizeOptions', () => {
  describe('empty partial → all defaults', () => {
    test('returns all default values when given an empty object', () => {
      const result = normalizeOptions({});
      expect(result).toEqual(DEFAULT_PANEL_OPTIONS);
    });
  });

  describe('partial override', () => {
    test('preserves provided url over default', () => {
      const result = normalizeOptions({ url: 'https://example.com' });
      expect(result.url).toBe('https://example.com');
    });

    test('preserves provided loadMode', () => {
      const result = normalizeOptions({ loadMode: 'proxy' });
      expect(result.loadMode).toBe('proxy');
    });

    test('preserves provided detectedMode', () => {
      const result = normalizeOptions({ detectedMode: 'direct' });
      expect(result.detectedMode).toBe('direct');
    });

    test('preserves provided showDebugOverlay=true', () => {
      const result = normalizeOptions({ showDebugOverlay: true });
      expect(result.showDebugOverlay).toBe(true);
    });

    test('preserves valid viewportX, viewportY, viewportZoom', () => {
      const result = normalizeOptions({ viewportX: 100, viewportY: 200, viewportZoom: 2.5 });
      expect(result.viewportX).toBe(100);
      expect(result.viewportY).toBe(200);
      expect(result.viewportZoom).toBe(2.5);
    });

    test('merges partial options, leaving unspecified fields as defaults', () => {
      const result = normalizeOptions({ url: 'https://example.com', iframeWidth: 1280 });
      expect(result.url).toBe('https://example.com');
      expect(result.iframeWidth).toBe(1280);
      // Remaining fields stay at defaults
      expect(result.loadMode).toBe('auto');
      expect(result.iframeHeight).toBe(1080);
    });
  });

  describe('viewportZoom clamping', () => {
    test('clamps zoom below minimum (0.1) up to 0.1', () => {
      const result = normalizeOptions({ viewportZoom: 0.0 });
      expect(result.viewportZoom).toBe(0.1);
    });

    test('clamps negative zoom up to 0.1', () => {
      const result = normalizeOptions({ viewportZoom: -1 });
      expect(result.viewportZoom).toBe(0.1);
    });

    test('clamps zoom above maximum (5.0) down to 5.0', () => {
      const result = normalizeOptions({ viewportZoom: 10 });
      expect(result.viewportZoom).toBe(5.0);
    });

    test('preserves zoom exactly at minimum boundary (0.1)', () => {
      const result = normalizeOptions({ viewportZoom: 0.1 });
      expect(result.viewportZoom).toBe(0.1);
    });

    test('preserves zoom exactly at maximum boundary (5.0)', () => {
      const result = normalizeOptions({ viewportZoom: 5.0 });
      expect(result.viewportZoom).toBe(5.0);
    });

    test('preserves zoom within valid range', () => {
      const result = normalizeOptions({ viewportZoom: 1.5 });
      expect(result.viewportZoom).toBe(1.5);
    });
  });

  describe('viewport offsets (RV1-10)', () => {
    test('keeps a negative viewportX (the page is moved right; no snap-back to 0)', () => {
      const result = normalizeOptions({ viewportX: -50 });
      expect(result.viewportX).toBe(-50);
    });

    test('preserves viewportX of zero', () => {
      const result = normalizeOptions({ viewportX: 0 });
      expect(result.viewportX).toBe(0);
    });

    test('keeps a negative viewportY', () => {
      const result = normalizeOptions({ viewportY: -100.5 });
      expect(result.viewportY).toBe(-100.5);
    });
  });

  describe('negative dimension rejection', () => {

    test('preserves viewportY of zero', () => {
      const result = normalizeOptions({ viewportY: 0 });
      expect(result.viewportY).toBe(0);
    });

    test('resets negative iframeWidth to default (1920)', () => {
      const result = normalizeOptions({ iframeWidth: -800 });
      expect(result.iframeWidth).toBe(1920);
    });

    test('resets negative iframeHeight to default (1080)', () => {
      const result = normalizeOptions({ iframeHeight: -600 });
      expect(result.iframeHeight).toBe(1080);
    });

    test('resets a zero width or height to the default (a 0 px page cannot be shown)', () => {
      const result = normalizeOptions({ iframeWidth: 0, iframeHeight: 0 });
      expect(result.iframeWidth).toBe(1920);
      expect(result.iframeHeight).toBe(1080);
    });

    test('resets negative refreshIntervalSec to default (0)', () => {
      const result = normalizeOptions({ refreshIntervalSec: -5 });
      expect(result.refreshIntervalSec).toBe(0);
    });

    test('preserves refreshIntervalSec of zero', () => {
      const result = normalizeOptions({ refreshIntervalSec: 0 });
      expect(result.refreshIntervalSec).toBe(0);
    });

    test('preserves positive refreshIntervalSec', () => {
      const result = normalizeOptions({ refreshIntervalSec: 30 });
      expect(result.refreshIntervalSec).toBe(30);
    });
  });

  describe('refresh interval minimum (RV1-12)', () => {
    test.each([
      [0.05, 5],
      [1, 5],
      [4.9, 5],
      [5, 5],
      [5.4, 5],
      [5.6, 6],
      [60, 60],
    ])('a positive interval %p becomes %p whole seconds (minimum MIN_REFRESH_INTERVAL_SEC)', (input, expected) => {
      expect(normalizeOptions({ refreshIntervalSec: input }).refreshIntervalSec).toBe(expected);
    });

    test('the minimum is 5 seconds', () => {
      expect(MIN_REFRESH_INTERVAL_SEC).toBe(5);
    });

    test('zero and negative values disable auto-refresh', () => {
      expect(normalizeOptions({ refreshIntervalSec: 0 }).refreshIntervalSec).toBe(0);
      expect(normalizeOptions({ refreshIntervalSec: -0.5 }).refreshIntervalSec).toBe(0);
    });
  });

  describe('zoom bounds come from viewport.ts (RV1-39)', () => {
    test('clamps with the VIEWPORT_ZOOM_MIN / VIEWPORT_ZOOM_MAX constants', () => {
      expect(normalizeOptions({ viewportZoom: -1 }).viewportZoom).toBe(VIEWPORT_ZOOM_MIN);
      expect(normalizeOptions({ viewportZoom: 1e9 }).viewportZoom).toBe(VIEWPORT_ZOOM_MAX);
    });
  });

  describe('coercion of hand-edited or provisioned JSON (RV1-33)', () => {
    /** Casts arbitrary stored JSON to the option type, as Grafana hands it to the panel. */
    const stored = (value: Record<string, unknown>) => value as unknown as Partial<PanelOptions>;

    test.each([null, undefined, 'abc', '', NaN, Infinity, -Infinity, {}, [], true])(
      'a non-numeric value %p falls back to the default for every numeric field',
      (bad) => {
        const result = normalizeOptions(
          stored({
            viewportX: bad,
            viewportY: bad,
            viewportZoom: bad,
            iframeWidth: bad,
            iframeHeight: bad,
            refreshIntervalSec: bad,
          })
        );
        expect(result.viewportX).toBe(0);
        expect(result.viewportY).toBe(0);
        expect(result.viewportZoom).toBe(1);
        expect(result.iframeWidth).toBe(1920);
        expect(result.iframeHeight).toBe(1080);
        expect(result.refreshIntervalSec).toBe(0);
      }
    );

    test('numeric strings are read as numbers', () => {
      const result = normalizeOptions(
        stored({ viewportX: '12', viewportY: ' -3 ', viewportZoom: '2.5', iframeWidth: '1280', iframeHeight: '720' })
      );
      expect(result).toMatchObject({ viewportX: 12, viewportY: -3, viewportZoom: 2.5, iframeWidth: 1280, iframeHeight: 720 });
    });

    test('an unknown loadMode falls back to auto', () => {
      expect(normalizeOptions(stored({ loadMode: 'iframe' })).loadMode).toBe('auto');
      expect(normalizeOptions(stored({ loadMode: null })).loadMode).toBe('auto');
      expect(normalizeOptions(stored({ loadMode: 'PROXY' })).loadMode).toBe('auto');
    });

    test('an unknown detectedMode falls back to null (not tested)', () => {
      expect(normalizeOptions(stored({ detectedMode: 'auto' })).detectedMode).toBeNull();
      expect(normalizeOptions(stored({ detectedMode: 1 })).detectedMode).toBeNull();
      expect(normalizeOptions(stored({ detectedMode: 'proxy' })).detectedMode).toBe('proxy');
    });

    test('non-string url and hideSelectors fall back to empty strings', () => {
      const result = normalizeOptions(stored({ url: 42, hideSelectors: ['.a'] }));
      expect(result.url).toBe('');
      expect(result.hideSelectors).toBe('');
    });

    test('showDebugOverlay is true only for the boolean true', () => {
      expect(normalizeOptions(stored({ showDebugOverlay: 'true' })).showDebugOverlay).toBe(false);
      expect(normalizeOptions(stored({ showDebugOverlay: 1 })).showDebugOverlay).toBe(false);
      expect(normalizeOptions(stored({ showDebugOverlay: true })).showDebugOverlay).toBe(true);
    });

    test('null or undefined input gives the defaults', () => {
      expect(normalizeOptions(null)).toEqual(DEFAULT_PANEL_OPTIONS);
      expect(normalizeOptions(undefined)).toEqual(DEFAULT_PANEL_OPTIONS);
    });

    test('unknown keys are not copied into the result', () => {
      const result = normalizeOptions(stored({ url: 'https://example.com', legacyField: 1 }));
      expect(result).not.toHaveProperty('legacyField');
    });
  });

  describe('full options round-trip', () => {
    test('normalizing already-valid full options returns them unchanged', () => {
      const full: PanelOptions = {
        url: 'https://grafana.com',
        loadMode: 'proxy',
        detectedMode: 'proxy',
        detectedForUrl: 'https://grafana.com',
        viewportX: 100,
        viewportY: 200,
        viewportZoom: 2.0,
        iframeWidth: 1280,
        iframeHeight: 720,
        refreshIntervalSec: 60,
        hideSelectors: '.ads, .nav',
        showDebugOverlay: true,
      };
      expect(normalizeOptions(full)).toEqual(full);
    });
  });
});

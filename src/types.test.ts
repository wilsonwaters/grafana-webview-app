import { DEFAULT_PANEL_OPTIONS, normalizeOptions, type PanelOptions } from './types';

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

  describe('negative dimension rejection', () => {
    test('resets negative viewportX to default (0)', () => {
      const result = normalizeOptions({ viewportX: -50 });
      expect(result.viewportX).toBe(0);
    });

    test('preserves viewportX of zero', () => {
      const result = normalizeOptions({ viewportX: 0 });
      expect(result.viewportX).toBe(0);
    });

    test('resets negative viewportY to default (0)', () => {
      const result = normalizeOptions({ viewportY: -100 });
      expect(result.viewportY).toBe(0);
    });

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

  describe('full options round-trip', () => {
    test('normalizing already-valid full options returns them unchanged', () => {
      const full: PanelOptions = {
        url: 'https://grafana.com',
        loadMode: 'proxy',
        detectedMode: 'proxy',
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

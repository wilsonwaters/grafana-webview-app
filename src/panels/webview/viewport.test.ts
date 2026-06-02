import {
  buildViewportTransform,
  clampZoom,
  panDelta,
  zoomAtCursor,
  VIEWPORT_ZOOM_MIN,
  VIEWPORT_ZOOM_MAX,
} from './viewport';

describe('panels/webview/viewport', () => {
  // -------------------------------------------------------------------------
  // PC1 — buildViewportTransform (unchanged)
  // -------------------------------------------------------------------------
  test('builds scale() translate() in the correct order with negated offsets', () => {
    expect(buildViewportTransform({ viewportX: 100, viewportY: 200, viewportZoom: 1.5 })).toBe(
      'scale(1.5) translate(-100px, -200px)'
    );
  });

  test('identity viewport (zoom 1, no offset)', () => {
    expect(buildViewportTransform({ viewportX: 0, viewportY: 0, viewportZoom: 1 })).toBe(
      'scale(1) translate(0px, 0px)'
    );
  });

  test('zoom-out below 1 is preserved', () => {
    expect(buildViewportTransform({ viewportX: 50, viewportY: 0, viewportZoom: 0.5 })).toBe(
      'scale(0.5) translate(-50px, 0px)'
    );
  });

  // -------------------------------------------------------------------------
  // PC2 — clampZoom
  // -------------------------------------------------------------------------
  describe('clampZoom', () => {
    test('value within range is returned unchanged', () => {
      expect(clampZoom(1.0)).toBe(1.0);
      expect(clampZoom(2.5)).toBe(2.5);
    });

    test('value at minimum boundary is returned as-is', () => {
      expect(clampZoom(VIEWPORT_ZOOM_MIN)).toBe(0.1);
    });

    test('value at maximum boundary is returned as-is', () => {
      expect(clampZoom(VIEWPORT_ZOOM_MAX)).toBe(5.0);
    });

    test('value below minimum is clamped to 0.1', () => {
      expect(clampZoom(0)).toBe(0.1);
      expect(clampZoom(-1)).toBe(0.1);
      expect(clampZoom(0.05)).toBe(0.1);
    });

    test('value above maximum is clamped to 5.0', () => {
      expect(clampZoom(10)).toBe(5.0);
      expect(clampZoom(5.001)).toBe(5.0);
    });
  });

  // -------------------------------------------------------------------------
  // PC2 — panDelta
  // -------------------------------------------------------------------------
  describe('panDelta', () => {
    test('at zoom 1, screen delta equals virtual delta 1:1', () => {
      expect(panDelta(100, 80, 1)).toEqual({ dx: 100, dy: 80 });
    });

    test('at zoom 2, a 100 px screen drag produces 50 virtual px (AC example)', () => {
      expect(panDelta(100, 0, 2)).toEqual({ dx: 50, dy: 0 });
    });

    test('at zoom 0.5, screen delta is doubled in virtual space', () => {
      expect(panDelta(30, 20, 0.5)).toEqual({ dx: 60, dy: 40 });
    });

    test('negative deltas (drag left/up) yield negative virtual offsets', () => {
      expect(panDelta(-50, -40, 1)).toEqual({ dx: -50, dy: -40 });
    });

    test('zero delta produces zero virtual change at any zoom', () => {
      expect(panDelta(0, 0, 2)).toEqual({ dx: 0, dy: 0 });
    });

    test('x and y are independent', () => {
      const result = panDelta(60, -30, 3);
      expect(result.dx).toBeCloseTo(20);
      expect(result.dy).toBeCloseTo(-10);
    });
  });

  // -------------------------------------------------------------------------
  // PC2 — zoomAtCursor
  // -------------------------------------------------------------------------
  describe('zoomAtCursor', () => {
    test('cursor at origin (0,0) — only zoom changes, offsets unchanged', () => {
      const result = zoomAtCursor({
        viewportX: 0,
        viewportY: 0,
        viewportZoom: 1,
        cursorX: 0,
        cursorY: 0,
        zoomFactor: 1.1,
      });
      expect(result.viewportZoom).toBeCloseTo(1.1);
      expect(result.viewportX).toBeCloseTo(0);
      expect(result.viewportY).toBeCloseTo(0);
    });

    test('cursor at (200, 100), zoom 1→2: point under cursor stays fixed (AC example)', () => {
      // Virtual point before: (200/1 + 0, 100/1 + 0) = (200, 100)
      // After zoom 2: newX = 200 - 200/2 = 100, newY = 100 - 100/2 = 50
      const result = zoomAtCursor({
        viewportX: 0,
        viewportY: 0,
        viewportZoom: 1,
        cursorX: 200,
        cursorY: 100,
        zoomFactor: 2,
      });
      expect(result.viewportZoom).toBeCloseTo(2);
      expect(result.viewportX).toBeCloseTo(100);
      expect(result.viewportY).toBeCloseTo(50);
    });

    test('invariant: virtual point under cursor is same before and after zoom', () => {
      const input = {
        viewportX: 50,
        viewportY: 30,
        viewportZoom: 1.5,
        cursorX: 120,
        cursorY: 90,
        zoomFactor: 1.2,
      };

      // Virtual point under cursor before zoom
      const virtualXBefore = input.cursorX / input.viewportZoom + input.viewportX;
      const virtualYBefore = input.cursorY / input.viewportZoom + input.viewportY;

      const result = zoomAtCursor(input);

      // Virtual point under cursor after zoom — must be identical
      const virtualXAfter = input.cursorX / result.viewportZoom + result.viewportX;
      const virtualYAfter = input.cursorY / result.viewportZoom + result.viewportY;

      expect(virtualXAfter).toBeCloseTo(virtualXBefore, 10);
      expect(virtualYAfter).toBeCloseTo(virtualYBefore, 10);
    });

    test('invariant holds when zooming out', () => {
      const input = {
        viewportX: 200,
        viewportY: 150,
        viewportZoom: 3,
        cursorX: 300,
        cursorY: 200,
        zoomFactor: 0.8,
      };

      const virtualXBefore = input.cursorX / input.viewportZoom + input.viewportX;
      const virtualYBefore = input.cursorY / input.viewportZoom + input.viewportY;

      const result = zoomAtCursor(input);

      const virtualXAfter = input.cursorX / result.viewportZoom + result.viewportX;
      const virtualYAfter = input.cursorY / result.viewportZoom + result.viewportY;

      expect(virtualXAfter).toBeCloseTo(virtualXBefore, 10);
      expect(virtualYAfter).toBeCloseTo(virtualYBefore, 10);
    });

    test('zoom clamped at maximum (5.0) — offsets still computed consistently', () => {
      const result = zoomAtCursor({
        viewportX: 0,
        viewportY: 0,
        viewportZoom: 4.9,
        cursorX: 100,
        cursorY: 50,
        zoomFactor: 2, // would produce 9.8 without clamp
      });
      expect(result.viewportZoom).toBe(5.0);
      // Offsets are computed using the clamped zoom value
      // virtualX = 100/4.9 + 0 ≈ 20.408, newX = 20.408 - 100/5 = 20.408 - 20 = 0.408
      expect(result.viewportX).toBeCloseTo(100 / 4.9 - 100 / 5, 5);
      expect(result.viewportY).toBeCloseTo(50 / 4.9 - 50 / 5, 5);
    });

    test('zoom clamped at minimum (0.1)', () => {
      const result = zoomAtCursor({
        viewportX: 0,
        viewportY: 0,
        viewportZoom: 0.15,
        cursorX: 0,
        cursorY: 0,
        zoomFactor: 0.5, // would produce 0.075 without clamp
      });
      expect(result.viewportZoom).toBe(0.1);
    });

    test('non-zero existing viewport — invariant still holds', () => {
      const input = {
        viewportX: 400,
        viewportY: 300,
        viewportZoom: 2,
        cursorX: 150,
        cursorY: 100,
        zoomFactor: 1.5,
      };

      const virtualXBefore = input.cursorX / input.viewportZoom + input.viewportX;
      const virtualYBefore = input.cursorY / input.viewportZoom + input.viewportY;

      const result = zoomAtCursor(input);

      const virtualXAfter = input.cursorX / result.viewportZoom + result.viewportX;
      const virtualYAfter = input.cursorY / result.viewportZoom + result.viewportY;

      expect(virtualXAfter).toBeCloseTo(virtualXBefore, 10);
      expect(virtualYAfter).toBeCloseTo(virtualYBefore, 10);
    });
  });
});

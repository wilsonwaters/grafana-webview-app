import { buildViewportTransform } from './viewport';

describe('panels/webview/viewport', () => {
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
});

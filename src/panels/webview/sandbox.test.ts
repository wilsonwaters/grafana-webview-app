import { iframeSandbox, SANDBOX_CROSS_ORIGIN, SANDBOX_OPAQUE_ORIGIN } from './sandbox';

const GRAFANA = 'https://grafana.example.net';

describe('panels/webview/sandbox iframeSandbox (VR-2)', () => {
  test('the two sandbox values are exact and grant nothing else', () => {
    expect(SANDBOX_OPAQUE_ORIGIN).toBe('allow-scripts');
    expect(SANDBOX_CROSS_ORIGIN).toBe('allow-scripts allow-same-origin');
    for (const value of [SANDBOX_OPAQUE_ORIGIN, SANDBOX_CROSS_ORIGIN]) {
      expect(value).not.toMatch(/allow-(popups|forms|modals|top-navigation|downloads|pointer-lock|presentation)/);
    }
  });

  describe('proxy mode never gets allow-same-origin', () => {
    test.each(['https://example.com/', `${GRAFANA}/`, `${GRAFANA}/api/user`, 'http://localhost/', 'not a url'])(
      '%s',
      (url) => {
        const sandbox = iframeSandbox('proxy', url, GRAFANA);
        expect(sandbox).toBe('allow-scripts');
        expect(sandbox).not.toContain('allow-same-origin');
      }
    );
  });

  describe('direct mode', () => {
    test('a cross-origin URL gets allow-scripts allow-same-origin', () => {
      expect(iframeSandbox('direct', 'https://example.com/', GRAFANA)).toBe('allow-scripts allow-same-origin');
    });

    test('a URL on another port, scheme or subdomain of Grafana is cross-origin', () => {
      expect(iframeSandbox('direct', 'https://grafana.example.net:8443/', GRAFANA)).toBe(SANDBOX_CROSS_ORIGIN);
      expect(iframeSandbox('direct', 'http://grafana.example.net/', GRAFANA)).toBe(SANDBOX_CROSS_ORIGIN);
      expect(iframeSandbox('direct', 'https://sub.grafana.example.net/', GRAFANA)).toBe(SANDBOX_CROSS_ORIGIN);
    });

    test.each([
      `${GRAFANA}/`,
      `${GRAFANA}/d/abc/dashboard`,
      'https://grafana.example.net:443/x',
      'HTTPS://GRAFANA.EXAMPLE.NET/x',
    ])('a same-origin URL gets allow-scripts only: %s', (url) => {
      const sandbox = iframeSandbox('direct', url, GRAFANA);
      expect(sandbox).toBe('allow-scripts');
      expect(sandbox).not.toContain('allow-same-origin');
    });

    test('an unparsable URL gets allow-scripts only (fail safe)', () => {
      expect(iframeSandbox('direct', 'not a url', GRAFANA)).toBe(SANDBOX_OPAQUE_ORIGIN);
      expect(iframeSandbox('direct', 'data:text/html,x', GRAFANA)).toBe(SANDBOX_OPAQUE_ORIGIN);
    });

    test('defaults the host origin to window.location.origin', () => {
      // jsdom serves the test page from http://localhost.
      expect(window.location.origin).toBe('http://localhost');
      expect(iframeSandbox('direct', 'http://localhost/api/user')).toBe(SANDBOX_OPAQUE_ORIGIN);
      expect(iframeSandbox('direct', 'https://example.com/')).toBe(SANDBOX_CROSS_ORIGIN);
    });
  });
});

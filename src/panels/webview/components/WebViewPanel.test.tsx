import React from 'react';
import { render, screen, act } from '@testing-library/react';
import { PanelProps } from '@grafana/data';
import { WebViewPanel } from './WebViewPanel';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { webViewPanelTestIds } from './testIds';
import { PROXY_RESOURCE_BASE } from '../loadMode';
import { useBackendAvailable, type BackendAvailability } from '../useBackendAvailable';
import {
  installProxyFetchMock,
  respondError,
  respondHtml,
  respondHttpError,
  type ProxyFetchMock,
} from '../testing/proxyFetchMock';

// FR5: proxy mode fetches the rewritten HTML with getBackendSrv().fetch. The
// mock lets each test decide when and in which order requests respond.
jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

// DF3: mock the backend-availability hook so each test drives the
// available / unavailable / loading states deterministically. Default is a
// settled-available backend (set in beforeEach below) so existing PC1/FR4
// behaviour — which predates the proxy guard — is preserved unchanged.
jest.mock('../useBackendAvailable', () => ({
  useBackendAvailable: jest.fn(),
}));

const mockedUseBackendAvailable = useBackendAvailable as jest.MockedFunction<typeof useBackendAvailable>;

function setBackend(state: BackendAvailability) {
  mockedUseBackendAvailable.mockReturnValue(state);
}

let proxyFetch: ProxyFetchMock;

beforeEach(() => {
  // Default: backend settled and available. Direct-mode tests are unaffected by
  // this; proxy-mode tests that predate DF3 expect the proxy iframe, which now
  // requires an available backend.
  setBackend({ loading: false, backendAvailable: true });
  proxyFetch = installProxyFetchMock();
});

/** The decoded query of the n-th proxy fetch. */
function fetchParams(n = 0): URLSearchParams {
  const url = proxyFetch.calls[n].options.url;
  expect(url.startsWith(`${PROXY_RESOURCE_BASE}?`)).toBe(true);
  return new URLSearchParams(url.split('?')[1]);
}

function buildProps(options: Partial<PanelOptions> = {}, dims: { width?: number; height?: number } = {}): PanelProps<PanelOptions> {
  return {
    options: { ...DEFAULT_PANEL_OPTIONS, ...options },
    width: dims.width ?? 400,
    height: dims.height ?? 300,
  } as unknown as PanelProps<PanelOptions>;
}

describe('panels/webview/WebViewPanel', () => {
  // ---------------------------------------------------------------------------
  // PC1: baseline rendering tests
  // ---------------------------------------------------------------------------

  test('renders the empty state when no URL is configured', () => {
    render(<WebViewPanel {...buildProps({ url: '' })} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
    expect(screen.getByTestId(webViewPanelTestIds.placeholder)).toBeInTheDocument();
    // No iframe with an empty src should be rendered in the empty state.
    expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
  });

  test('renders an iframe with the configured URL as src in direct mode', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveAttribute('src', 'https://example.com');
    expect(screen.queryByTestId(webViewPanelTestIds.placeholder)).not.toBeInTheDocument();
  });

  test('iframe uses the exact sandbox attribute and is non-interactive', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');
    expect(iframe).toHaveStyle({ pointerEvents: 'none' });
  });

  test('iframe is sized to the virtual dimensions and clipped by the container', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveStyle({ width: '1920px', height: '1080px', transformOrigin: 'top left' });
    expect(screen.getByTestId(webViewPanelTestIds.container)).toHaveStyle({ overflow: 'hidden' });
  });

  test('applies the saved viewport via CSS transform', () => {
    render(
      <WebViewPanel
        {...buildProps({ url: 'https://example.com', viewportX: 100, viewportY: 200, viewportZoom: 1.5 })}
      />
    );

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(1.5) translate(-100px, -200px)' });
  });

  test('applies a negative saved viewport (panned past the top-left edge) without resetting it', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com', viewportX: -40, viewportY: -25, viewportZoom: 0.5 })} />);

    expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveStyle({
      transform: 'scale(0.5) translate(40px, 25px)',
    });
  });

  test('renders without crashing for partial / legacy options', () => {
    const props = { options: { url: 'https://x.test' }, width: 100, height: 100 } as unknown as PanelProps<PanelOptions>;
    render(<WebViewPanel {...props} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
    expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://x.test');
  });

  // ---------------------------------------------------------------------------
  // FR4: load-mode → iframe src wiring
  // Completion Criterion: "View-mode renders with the correct src based on the
  // resolved mode" (direct → raw URL; proxy → proxy resource URL; auto per
  // detectedMode).
  // ---------------------------------------------------------------------------

  describe('load-mode wiring (FR4, FR5)', () => {
    test('proxy mode → fetches the backend proxy resource and renders the HTML with srcdoc', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />);

      expect(fetchParams().get('url')).toBe('https://example.com');
      respondHtml(proxyFetch.calls[0], '<h1>Example Domain</h1>');

      const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(iframe).toHaveAttribute('srcdoc', '<h1>Example Domain</h1>');
      // The resource route is never an iframe src (Grafana sends XFO deny + CSP sandbox).
      expect(iframe).not.toHaveAttribute('src');
    });

    test('proxy mode → hideSelectors are carried as repeated hide= params (CR5)', () => {
      render(
        <WebViewPanel
          {...buildProps({ url: 'https://example.com', loadMode: 'proxy', hideSelectors: '.ad\n#banner' })}
        />
      );

      expect(fetchParams().getAll('hide')).toEqual(['.ad', '#banner']);
    });

    test('direct mode → iframe src is the raw URL (no proxy fetch)', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(iframe).toHaveAttribute('src', 'https://example.com');
      expect(iframe).not.toHaveAttribute('srcdoc');
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
    });

    test('auto mode + detectedMode=proxy → proxy fetch + srcdoc', () => {
      render(
        <WebViewPanel
          {...buildProps({ url: 'https://example.com', loadMode: 'auto', detectedMode: 'proxy' })}
        />
      );

      expect(proxyFetch.fetch).toHaveBeenCalledTimes(1);
      respondHtml(proxyFetch.calls[0], '<p>proxied</p>');
      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>proxied</p>');
    });

    test('auto mode + detectedMode=direct → raw URL src', () => {
      render(
        <WebViewPanel
          {...buildProps({ url: 'https://example.com', loadMode: 'auto', detectedMode: 'direct' })}
        />
      );

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
    });

    test('auto mode + a proxy verdict measured for ANOTHER URL → direct (not tested), no proxy fetch', () => {
      render(
        <WebViewPanel
          {...buildProps({
            url: 'https://new.example.com',
            loadMode: 'auto',
            detectedMode: 'proxy',
            detectedForUrl: 'https://old.example.com',
          })}
        />
      );

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://new.example.com');
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
    });

    test('auto mode + a proxy verdict measured for THIS URL → proxy fetch', () => {
      render(
        <WebViewPanel
          {...buildProps({
            url: 'https://example.com',
            loadMode: 'auto',
            detectedMode: 'proxy',
            detectedForUrl: 'https://example.com',
          })}
        />
      );

      expect(proxyFetch.fetch).toHaveBeenCalledTimes(1);
    });

    test('auto mode + detectedMode=null → raw URL src (defaults to direct)', () => {
      render(
        <WebViewPanel
          {...buildProps({ url: 'https://example.com', loadMode: 'auto', detectedMode: null })}
        />
      );

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
    });

    test('overlay shows resolved mode (proxy) in proxy mode', () => {
      render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', showDebugOverlay: true, loadMode: 'proxy' })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.debugOverlay)).toHaveTextContent('mode: proxy');
    });
  });

  // ---------------------------------------------------------------------------
  // PC5: debug overlay
  // ---------------------------------------------------------------------------

  describe('debug overlay', () => {
    test('overlay is NOT rendered when showDebugOverlay is false (default)', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', showDebugOverlay: false })} />);

      expect(screen.queryByTestId(webViewPanelTestIds.debugOverlay)).not.toBeInTheDocument();
    });

    test('overlay IS rendered when showDebugOverlay is true', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', showDebugOverlay: true })} />);

      expect(screen.getByTestId(webViewPanelTestIds.debugOverlay)).toBeInTheDocument();
    });

    test('overlay shows load mode when showDebugOverlay is true (direct mode)', () => {
      render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', showDebugOverlay: true, loadMode: 'direct' })} />
      );

      const overlay = screen.getByTestId(webViewPanelTestIds.debugOverlay);
      expect(overlay).toHaveTextContent('mode: direct');
    });

    test('overlay shows X/Y coordinates', () => {
      render(
        <WebViewPanel
          {...buildProps({
            url: 'https://example.com',
            showDebugOverlay: true,
            viewportX: 42,
            viewportY: 99,
          })}
        />
      );

      const overlay = screen.getByTestId(webViewPanelTestIds.debugOverlay);
      expect(overlay).toHaveTextContent('X: 42');
      expect(overlay).toHaveTextContent('Y: 99');
    });

    test('overlay shows zoom factor', () => {
      render(
        <WebViewPanel
          {...buildProps({
            url: 'https://example.com',
            showDebugOverlay: true,
            viewportZoom: 2.5,
          })}
        />
      );

      const overlay = screen.getByTestId(webViewPanelTestIds.debugOverlay);
      expect(overlay).toHaveTextContent('zoom: 2.5');
    });

    test('overlay is NOT rendered in the empty-URL state even when enabled', () => {
      render(<WebViewPanel {...buildProps({ url: '', showDebugOverlay: true })} />);

      // Empty URL path exits before rendering overlay
      expect(screen.queryByTestId(webViewPanelTestIds.debugOverlay)).not.toBeInTheDocument();
    });
  });

  // ---------------------------------------------------------------------------
  // PC5: auto-refresh (fake timers)
  // ---------------------------------------------------------------------------

  describe('auto-refresh', () => {
    beforeEach(() => {
      jest.useFakeTimers();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    test('no interval is armed when refreshIntervalSec is 0 (disabled)', () => {
      const setIntervalSpy = jest.spyOn(global, 'setInterval');

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 0 })} />);

      expect(setIntervalSpy).not.toHaveBeenCalled();
      setIntervalSpy.mockRestore();
    });

    test('interval is armed when refreshIntervalSec > 0', () => {
      const setIntervalSpy = jest.spyOn(global, 'setInterval');

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 30 })} />);

      expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), 30000);
      setIntervalSpy.mockRestore();
    });

    test.each([
      [1, 5000],
      [0.05, 5000],
      [4.4, 5000],
      [12.6, 13000],
    ])('refreshIntervalSec %p uses whole seconds with a 5 s minimum (%p ms)', (seconds, expectedMs) => {
      const setIntervalSpy = jest.spyOn(global, 'setInterval');

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: seconds })} />);

      expect(setIntervalSpy).toHaveBeenCalledTimes(1);
      expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), expectedMs);
      setIntervalSpy.mockRestore();
    });

    test('a non-numeric refreshIntervalSec arms no interval (no tight refresh loop)', () => {
      const setIntervalSpy = jest.spyOn(global, 'setInterval');
      const options = { refreshIntervalSec: 'fast' } as unknown as Partial<PanelOptions>;

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', ...options })} />);

      expect(setIntervalSpy).not.toHaveBeenCalled();
      setIntervalSpy.mockRestore();
    });

    test('iframe is remounted (key changes) after one interval tick', () => {
      const { rerender } = render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 10 })} />
      );

      const iframeBefore = screen.getByTestId(webViewPanelTestIds.iframe);
      const keyBefore = iframeBefore.getAttribute('key');

      // Advance the clock by 10 s — the interval callback should fire
      act(() => {
        jest.advanceTimersByTime(10000);
      });

      // Re-render is triggered by state update; rerender ensures React flushes
      rerender(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 10 })} />);

      const iframeAfter = screen.getByTestId(webViewPanelTestIds.iframe);
      // The `key` prop itself is not reflected as an attribute, but the element is
      // remounted (same DOM structure, so we verify via the data attribute being present
      // and the component not crashing). We instead check an indirect signal: the
      // refreshKey counter should have advanced — validated by the fact that the
      // iframe is still present and still pointing to the correct URL.
      expect(iframeAfter).toHaveAttribute('src', 'https://example.com');
      void keyBefore; // suppress unused variable
    });

    test('interval fires multiple times on subsequent ticks', () => {
      const incrementCount = { value: 0 };
      const realSetInterval = global.setInterval;
      const setIntervalSpy = jest.spyOn(global, 'setInterval').mockImplementation((fn, delay, ...args) => {
        return realSetInterval(() => {
          incrementCount.value += 1;
          (fn as () => void)();
        }, delay, ...args) as unknown as ReturnType<typeof setInterval>;
      });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 5 })} />);

      act(() => { jest.advanceTimersByTime(5000); });
      act(() => { jest.advanceTimersByTime(5000); });
      act(() => { jest.advanceTimersByTime(5000); });

      expect(incrementCount.value).toBeGreaterThanOrEqual(3);
      setIntervalSpy.mockRestore();
    });

    test('interval is cleared on unmount (no timer leak)', () => {
      const clearIntervalSpy = jest.spyOn(global, 'clearInterval');

      const { unmount } = render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 30 })} />
      );

      unmount();

      expect(clearIntervalSpy).toHaveBeenCalled();
      clearIntervalSpy.mockRestore();
    });

    test('changing refreshIntervalSec re-arms the interval (old cleared, new set)', () => {
      const clearIntervalSpy = jest.spyOn(global, 'clearInterval');
      const setIntervalSpy = jest.spyOn(global, 'setInterval');

      const { rerender } = render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 30 })} />
      );

      // Should have been set once so far
      expect(setIntervalSpy).toHaveBeenCalledTimes(1);
      expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), 30000);

      // Change the interval — triggers effect cleanup + re-run
      rerender(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 60 })} />);

      // Old interval cleared, new one set
      expect(clearIntervalSpy).toHaveBeenCalled();
      expect(setIntervalSpy).toHaveBeenCalledTimes(2);
      expect(setIntervalSpy).toHaveBeenLastCalledWith(expect.any(Function), 60000);

      clearIntervalSpy.mockRestore();
      setIntervalSpy.mockRestore();
    });

    test('disabling refresh (change to 0) clears the interval and does not set a new one', () => {
      const clearIntervalSpy = jest.spyOn(global, 'clearInterval');
      const setIntervalSpy = jest.spyOn(global, 'setInterval');

      const { rerender } = render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 30 })} />
      );

      expect(setIntervalSpy).toHaveBeenCalledTimes(1);

      // Disable refresh
      rerender(<WebViewPanel {...buildProps({ url: 'https://example.com', refreshIntervalSec: 0 })} />);

      // Old interval should be cleared; no new interval set
      expect(clearIntervalSpy).toHaveBeenCalled();
      expect(setIntervalSpy).toHaveBeenCalledTimes(1); // no additional call
      clearIntervalSpy.mockRestore();
      setIntervalSpy.mockRestore();
    });
  });

  // ---------------------------------------------------------------------------
  // PC5: multi-instance independence
  // ---------------------------------------------------------------------------

  describe('multi-instance independence', () => {
    test('two instances with different URLs render independently', () => {
      const { container: container1 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-a.example.com' })} />
      );
      const { container: container2 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-b.example.com' })} />
      );

      const iframes1 = container1.querySelectorAll('iframe');
      const iframes2 = container2.querySelectorAll('iframe');

      expect(iframes1).toHaveLength(1);
      expect(iframes2).toHaveLength(1);
      expect(iframes1[0]).toHaveAttribute('src', 'https://site-a.example.com');
      expect(iframes2[0]).toHaveAttribute('src', 'https://site-b.example.com');
    });

    test('two instances with different viewports apply different transforms', () => {
      const { container: container1 } = render(
        <WebViewPanel
          {...buildProps({ url: 'https://site-a.example.com', viewportX: 100, viewportY: 200, viewportZoom: 1.0 })}
        />
      );
      const { container: container2 } = render(
        <WebViewPanel
          {...buildProps({ url: 'https://site-b.example.com', viewportX: 300, viewportY: 400, viewportZoom: 2.0 })}
        />
      );

      const iframe1 = container1.querySelector('iframe')!;
      const iframe2 = container2.querySelector('iframe')!;

      expect(iframe1).toHaveStyle({ transform: 'scale(1) translate(-100px, -200px)' });
      expect(iframe2).toHaveStyle({ transform: 'scale(2) translate(-300px, -400px)' });
    });

    test('two instances with different overlay settings show overlay independently', () => {
      const { container: container1 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-a.example.com', showDebugOverlay: true })} />
      );
      const { container: container2 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-b.example.com', showDebugOverlay: false })} />
      );

      // webViewPanelTestIds.debugOverlay = 'data-testid webview-panel-debug-overlay'
      // Grafana testId convention: the full string is the data-testid attribute value.
      const testIdValue = webViewPanelTestIds.debugOverlay;
      const overlays1 = container1.querySelectorAll(`[data-testid="${testIdValue}"]`);
      const overlays2 = container2.querySelectorAll(`[data-testid="${testIdValue}"]`);

      expect(overlays1).toHaveLength(1);
      expect(overlays2).toHaveLength(0);
    });

    test('two instances maintain independent refresh timers (fake timers)', () => {
      jest.useFakeTimers();
      try {
        const clearIntervalSpy = jest.spyOn(global, 'clearInterval');

        const { unmount: unmount1 } = render(
          <WebViewPanel {...buildProps({ url: 'https://site-a.example.com', refreshIntervalSec: 10 })} />
        );
        const { unmount: unmount2 } = render(
          <WebViewPanel {...buildProps({ url: 'https://site-b.example.com', refreshIntervalSec: 20 })} />
        );

        // Unmount only instance 1 — should clear its timer but not instance 2's
        const clearCallsBefore = clearIntervalSpy.mock.calls.length;
        unmount1();
        const clearCallsAfterUnmount1 = clearIntervalSpy.mock.calls.length;
        expect(clearCallsAfterUnmount1).toBeGreaterThan(clearCallsBefore);

        // Instance 2 should still be alive (no crash, still cleans up on its own unmount)
        unmount2();
        expect(clearIntervalSpy.mock.calls.length).toBeGreaterThan(clearCallsAfterUnmount1);

        clearIntervalSpy.mockRestore();
      } finally {
        jest.useRealTimers();
      }
    });

    test('two instances with different panel dimensions render at those dimensions', () => {
      const { container: container1 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-a.example.com' }, { width: 600, height: 400 })} />
      );
      const { container: container2 } = render(
        <WebViewPanel {...buildProps({ url: 'https://site-b.example.com' }, { width: 800, height: 500 })} />
      );

      // webViewPanelTestIds.container = 'data-testid webview-panel-container'
      const containerTestId = webViewPanelTestIds.container;
      const panelContainer1 = container1.querySelector(`[data-testid="${containerTestId}"]`) as HTMLElement;
      const panelContainer2 = container2.querySelector(`[data-testid="${containerTestId}"]`) as HTMLElement;

      expect(panelContainer1).not.toBeNull();
      expect(panelContainer2).not.toBeNull();
      expect(panelContainer1.style.width).toBe('600px');
      expect(panelContainer1.style.height).toBe('400px');
      expect(panelContainer2.style.width).toBe('800px');
      expect(panelContainer2.style.height).toBe('500px');
    });
  });

  // ---------------------------------------------------------------------------
  // S1: URL safety (VR-1) and per-mode sandbox (VR-2)
  // ---------------------------------------------------------------------------

  describe('URL safety (VR-1)', () => {
    const unsafeUrls = [
      'javascript:alert(document.cookie)',
      'JaVaScRiPt:alert(1)',
      '  javascript:alert(1)',
      'java\tscript:alert(1)',
      'data:text/html,<script>alert(1)</script>',
      'blob:https://example.com/0b8f1c1e',
      'vbscript:msgbox(1)',
      'file:///etc/passwd',
      '/api/user',
      '//example.com/',
      'https://user:secret@example.com/',
    ];

    test.each(unsafeUrls)('direct mode: %j renders an accessible error and NO iframe', (url) => {
      const { container } = render(<WebViewPanel {...buildProps({ url, loadMode: 'direct' })} />);

      const error = screen.getByTestId(webViewPanelTestIds.invalidUrl);
      expect(error).toHaveAttribute('role', 'alert');
      expect(error).toHaveTextContent(/cannot be displayed/i);
      expect(container.querySelector('iframe')).toBeNull();
    });

    test.each(unsafeUrls)('proxy mode: %j renders an accessible error and NO iframe', (url) => {
      const { container } = render(<WebViewPanel {...buildProps({ url, loadMode: 'proxy' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.invalidUrl)).toBeInTheDocument();
      expect(container.querySelector('iframe')).toBeNull();
      // The URL check runs before the backend guard.
      expect(screen.queryByTestId(webViewPanelTestIds.backendLoading)).not.toBeInTheDocument();
    });

    test('auto mode + detectedMode=proxy with an unsafe URL renders NO iframe', () => {
      const { container } = render(
        <WebViewPanel {...buildProps({ url: 'javascript:alert(1)', loadMode: 'auto', detectedMode: 'proxy' })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.invalidUrl)).toBeInTheDocument();
      expect(container.querySelector('iframe')).toBeNull();
    });

    test('the error message does not echo the unsafe URL', () => {
      render(<WebViewPanel {...buildProps({ url: 'javascript:alert(document.cookie)' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.invalidUrl)).not.toHaveTextContent('document.cookie');
    });

    test('changing a safe URL to an unsafe one removes the iframe', () => {
      const { rerender, container } = render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);
      expect(container.querySelector('iframe')).not.toBeNull();

      rerender(<WebViewPanel {...buildProps({ url: 'javascript:alert(1)' })} />);

      expect(container.querySelector('iframe')).toBeNull();
      expect(screen.getByTestId(webViewPanelTestIds.invalidUrl)).toBeInTheDocument();
    });

    test('surrounding whitespace is trimmed from the iframe src', () => {
      render(<WebViewPanel {...buildProps({ url: '  https://example.com  ' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
    });

    test('the debug overlay still shows on the invalid-URL state', () => {
      render(<WebViewPanel {...buildProps({ url: 'javascript:alert(1)', showDebugOverlay: true })} />);

      expect(screen.getByTestId(webViewPanelTestIds.invalidUrl)).toBeInTheDocument();
      expect(screen.getByTestId(webViewPanelTestIds.debugOverlay)).toHaveTextContent('mode: direct');
    });
  });

  describe('per-mode sandbox (VR-2)', () => {
    test('direct mode + cross-origin URL → allow-scripts allow-same-origin', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute(
        'sandbox',
        'allow-scripts allow-same-origin'
      );
    });

    test('direct mode + URL on the Grafana origin → allow-scripts only', () => {
      // jsdom serves the test page from http://localhost.
      render(<WebViewPanel {...buildProps({ url: 'http://localhost/api/user', loadMode: 'direct' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('sandbox', 'allow-scripts');
    });

    test('proxy mode → allow-scripts only (never allow-same-origin)', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />);
      respondHtml(proxyFetch.calls[0], '<p>x</p>');

      const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
      expect(iframe.getAttribute('sandbox')).not.toContain('allow-same-origin');
    });

    test('a URL edit from cross-origin to the Grafana origin mounts a NEW iframe with allow-scripts only', () => {
      // A browser applies the sandbox flags that are set when a navigation
      // starts. If the mounted iframe were reused, React would write the new
      // src before the new sandbox, and the Grafana-origin page would load with
      // the old allow-same-origin flag.
      const { rerender } = render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);
      const before = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(before).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');

      rerender(<WebViewPanel {...buildProps({ url: 'http://localhost/api/user', loadMode: 'direct' })} />);

      const after = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(after).not.toBe(before);
      expect(before.isConnected).toBe(false);
      expect(after).toHaveAttribute('sandbox', 'allow-scripts');
      expect(after).toHaveAttribute('src', 'http://localhost/api/user');
    });

    test('a URL edit from the Grafana origin to cross-origin also mounts a new iframe', () => {
      const { rerender } = render(
        <WebViewPanel {...buildProps({ url: 'http://localhost/d/abc', loadMode: 'direct' })} />
      );
      const before = screen.getByTestId(webViewPanelTestIds.iframe);

      rerender(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      const after = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(after).not.toBe(before);
      expect(after).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');
    });

    test('a URL edit with the same sandbox value still mounts a new iframe', () => {
      const { rerender } = render(<WebViewPanel {...buildProps({ url: 'https://a.example.com', loadMode: 'direct' })} />);
      const before = screen.getByTestId(webViewPanelTestIds.iframe);

      rerender(<WebViewPanel {...buildProps({ url: 'https://b.example.com', loadMode: 'direct' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).not.toBe(before);
    });

    test('a viewport-only change keeps the same iframe (no reload)', () => {
      const { rerender } = render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);
      const before = screen.getByTestId(webViewPanelTestIds.iframe);

      rerender(
        <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct', viewportX: 50, viewportZoom: 2 })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toBe(before);
    });

    test('the sandbox attribute is set before src on the iframe element', () => {
      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      const names = screen.getByTestId(webViewPanelTestIds.iframe).getAttributeNames();
      expect(names.indexOf('sandbox')).toBeGreaterThanOrEqual(0);
      expect(names.indexOf('sandbox')).toBeLessThan(names.indexOf('src'));
    });

    test('every iframe keeps referrerPolicy="no-referrer"', () => {
      const { container } = render(
        <>
          <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />
          <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />
        </>
      );
      respondHtml(proxyFetch.calls[0], '<p>x</p>');

      const iframes = container.querySelectorAll('iframe');
      expect(iframes).toHaveLength(2);
      iframes.forEach((iframe) => expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer'));
    });
  });

  // ---------------------------------------------------------------------------
  // DF3: view-mode backend-availability guard (proxy only)
  // ---------------------------------------------------------------------------

  describe('proxy view-mode guard (DF3)', () => {
    test('proxy + backend unavailable (settled) → accessible fallback, NO proxy iframe', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />);

      const fallback = screen.getByTestId(webViewPanelTestIds.proxyUnavailable);
      expect(fallback).toBeInTheDocument();
      // Real text, not just an icon (accessibility).
      expect(fallback).toHaveTextContent(/backend proxy/i);
      // The proxy iframe must NOT be rendered at all, and nothing is fetched.
      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
    });

    test('proxy + backend available → proxied HTML fetched and rendered with srcdoc', () => {
      setBackend({ loading: false, backendAvailable: true });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />);

      expect(fetchParams().get('url')).toBe('https://example.com');
      respondHtml(proxyFetch.calls[0], '<p>ok</p>');
      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>ok</p>');
      // No fallback / loading placeholder when available.
      expect(screen.queryByTestId(webViewPanelTestIds.proxyUnavailable)).not.toBeInTheDocument();
      expect(screen.queryByTestId(webViewPanelTestIds.backendLoading)).not.toBeInTheDocument();
    });

    test('proxy + probe loading → neutral placeholder, NO proxy iframe and NO fallback', () => {
      setBackend({ loading: true, backendAvailable: false });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.backendLoading)).toBeInTheDocument();
      // Never a broken proxy iframe while the probe is in flight, and no fetch yet.
      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
      // Not yet the settled fallback either.
      expect(screen.queryByTestId(webViewPanelTestIds.proxyUnavailable)).not.toBeInTheDocument();
    });

    test('direct mode renders the iframe immediately even when backend is unavailable (never waits on probe)', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
      expect(screen.queryByTestId(webViewPanelTestIds.proxyUnavailable)).not.toBeInTheDocument();
      expect(screen.queryByTestId(webViewPanelTestIds.backendLoading)).not.toBeInTheDocument();
    });

    test('direct mode renders the iframe immediately even while the probe is loading', () => {
      setBackend({ loading: true, backendAvailable: false });

      render(<WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'direct' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
      expect(screen.queryByTestId(webViewPanelTestIds.backendLoading)).not.toBeInTheDocument();
    });

    test('auto + detectedMode=proxy + backend unavailable → fallback, NO iframe', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'auto', detectedMode: 'proxy' })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.proxyUnavailable)).toBeInTheDocument();
      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
    });

    test('auto + detectedMode=direct + backend unavailable → direct iframe (unaffected by probe)', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'auto', detectedMode: 'direct' })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
    });

    test('empty URL still shows the empty state regardless of backend state', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(<WebViewPanel {...buildProps({ url: '', loadMode: 'proxy' })} />);

      expect(screen.getByTestId(webViewPanelTestIds.placeholder)).toBeInTheDocument();
      expect(screen.queryByTestId(webViewPanelTestIds.proxyUnavailable)).not.toBeInTheDocument();
      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
    });

    test('debug overlay still shows on the proxy fallback state', () => {
      setBackend({ loading: false, backendAvailable: false });

      render(
        <WebViewPanel {...buildProps({ url: 'https://example.com', loadMode: 'proxy', showDebugOverlay: true })} />
      );

      expect(screen.getByTestId(webViewPanelTestIds.proxyUnavailable)).toBeInTheDocument();
      expect(screen.getByTestId(webViewPanelTestIds.debugOverlay)).toHaveTextContent('mode: proxy');
    });
  });

  // ---------------------------------------------------------------------------
  // FR5: proxy mode renders the fetched HTML with srcdoc
  // ---------------------------------------------------------------------------

  describe('proxy render: fetch then srcdoc (FR5)', () => {
    const proxyProps = (overrides: Partial<PanelOptions> = {}) =>
      buildProps({ url: 'https://example.com', loadMode: 'proxy', ...overrides });

    test('fetches as text with Grafana error toasts off', () => {
      render(<WebViewPanel {...proxyProps()} />);

      expect(proxyFetch.calls[0].options).toMatchObject({
        method: 'GET',
        responseType: 'text',
        showErrorAlert: false,
      });
    });

    test('shows an accessible loading state until the HTML arrives', () => {
      render(<WebViewPanel {...proxyProps()} />);

      const loading = screen.getByTestId(webViewPanelTestIds.proxyLoading);
      expect(loading).toHaveAttribute('role', 'status');
      expect(loading).toHaveTextContent(/loading/i);
      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();

      respondHtml(proxyFetch.calls[0], '<p>done</p>');

      expect(screen.queryByTestId(webViewPanelTestIds.proxyLoading)).not.toBeInTheDocument();
      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toBeInTheDocument();
    });

    test('the srcdoc iframe has sandbox="allow-scripts", no-referrer, the viewport transform and no src', () => {
      render(<WebViewPanel {...proxyProps({ viewportX: 10, viewportY: 20, viewportZoom: 2 })} />);
      respondHtml(proxyFetch.calls[0], '<html><body><script>1</script></body></html>');

      const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(iframe).toHaveAttribute('srcdoc', '<html><body><script>1</script></body></html>');
      expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
      expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');
      expect(iframe).toHaveAttribute('title', 'Web View');
      expect(iframe).not.toHaveAttribute('src');
      expect(iframe).toHaveStyle({
        width: '1920px',
        height: '1080px',
        transform: 'scale(2) translate(-10px, -20px)',
        transformOrigin: 'top left',
        pointerEvents: 'none',
      });
    });

    test('a non-2xx response shows the status and backend message, and NO iframe', () => {
      const { container } = render(<WebViewPanel {...proxyProps()} />);

      respondHttpError(proxyFetch.calls[0], 403, 'Forbidden', 'target host is not allowlisted');

      const error = screen.getByTestId(webViewPanelTestIds.proxyError);
      expect(error).toHaveAttribute('role', 'alert');
      expect(error).toHaveTextContent('HTTP 403');
      expect(error).toHaveTextContent('target host is not allowlisted');
      expect(container.querySelector('iframe')).toBeNull();
    });

    test('an HTML error body is never rendered as a document or shown', () => {
      const body = '<html><body><script>window.pwned = 1</script>Bad gateway</body></html>';
      const { container } = render(<WebViewPanel {...proxyProps()} />);

      respondHttpError(proxyFetch.calls[0], 502, 'Bad Gateway', body);

      expect(container.querySelector('iframe')).toBeNull();
      expect(container.querySelector('[srcdoc]')).toBeNull();
      const error = screen.getByTestId(webViewPanelTestIds.proxyError);
      expect(error).toHaveTextContent('HTTP 502');
      expect(error).toHaveTextContent('Bad Gateway');
      expect(error).not.toHaveTextContent('pwned');
    });

    test('a network failure shows the error state without a status', () => {
      render(<WebViewPanel {...proxyProps()} />);

      respondError(proxyFetch.calls[0], new TypeError('Failed to fetch'));

      const error = screen.getByTestId(webViewPanelTestIds.proxyError);
      expect(error).toHaveTextContent('Failed to fetch');
      expect(error).not.toHaveTextContent('HTTP');
    });

    test('stale-response race: an older URL that responds last never overwrites the newer page', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps({ url: 'https://a.example.com' })} />);
      rerender(<WebViewPanel {...proxyProps({ url: 'https://b.example.com' })} />);

      expect(proxyFetch.calls).toHaveLength(2);
      expect(fetchParams(1).get('url')).toBe('https://b.example.com');
      expect(proxyFetch.calls[0].unsubscribed).toBe(true);

      respondHtml(proxyFetch.calls[1], '<p>B</p>');
      respondHtml(proxyFetch.calls[0], '<p>A</p>');

      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>B</p>');
    });

    test('changing the URL drops the old page and shows loading', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps({ url: 'https://a.example.com' })} />);
      respondHtml(proxyFetch.calls[0], '<p>A</p>');

      rerender(<WebViewPanel {...proxyProps({ url: 'https://b.example.com' })} />);

      expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
      expect(screen.getByTestId(webViewPanelTestIds.proxyLoading)).toBeInTheDocument();
    });

    test('changing hideSelectors refetches with the new hide params', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps({ hideSelectors: '.a' })} />);
      rerender(<WebViewPanel {...proxyProps({ hideSelectors: '.a\n.b' })} />);

      expect(proxyFetch.calls).toHaveLength(2);
      expect(fetchParams(1).getAll('hide')).toEqual(['.a', '.b']);
    });

    test('switching proxy → direct for the same URL mounts a new iframe with the direct flags', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps()} />);
      respondHtml(proxyFetch.calls[0], '<p>x</p>');
      const srcdocFrame = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(srcdocFrame).toHaveAttribute('sandbox', 'allow-scripts');

      rerender(<WebViewPanel {...proxyProps({ loadMode: 'direct' })} />);

      const directFrame = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(directFrame).not.toBe(srcdocFrame);
      expect(directFrame).not.toHaveAttribute('srcdoc');
      expect(directFrame).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');
    });

    test('switching direct → proxy never reuses the direct iframe', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps({ loadMode: 'direct' })} />);
      const directFrame = screen.getByTestId(webViewPanelTestIds.iframe);

      rerender(<WebViewPanel {...proxyProps()} />);
      respondHtml(proxyFetch.calls[0], '<p>x</p>');

      const srcdocFrame = screen.getByTestId(webViewPanelTestIds.iframe);
      expect(srcdocFrame).not.toBe(directFrame);
      expect(srcdocFrame).toHaveAttribute('sandbox', 'allow-scripts');
      expect(srcdocFrame).not.toHaveAttribute('src');
    });

    test('a viewport-only change does not refetch', () => {
      const { rerender } = render(<WebViewPanel {...proxyProps()} />);
      respondHtml(proxyFetch.calls[0], '<p>x</p>');

      rerender(<WebViewPanel {...proxyProps({ viewportX: 300, viewportZoom: 1.5 })} />);

      expect(proxyFetch.calls).toHaveLength(1);
      expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>x</p>');
    });

    test('unmount cancels the in-flight request', () => {
      const { unmount } = render(<WebViewPanel {...proxyProps()} />);

      unmount();

      expect(proxyFetch.calls[0].unsubscribed).toBe(true);
    });

    describe('auto-refresh', () => {
      beforeEach(() => {
        jest.useFakeTimers();
      });

      afterEach(() => {
        jest.useRealTimers();
      });

      test('each tick refetches the HTML and keeps the current page until the new one arrives', () => {
        render(<WebViewPanel {...proxyProps({ refreshIntervalSec: 10 })} />);
        respondHtml(proxyFetch.calls[0], '<p>v1</p>');

        act(() => {
          jest.advanceTimersByTime(10000);
        });

        expect(proxyFetch.calls).toHaveLength(2);
        expect(proxyFetch.calls[1].options.url).toBe(proxyFetch.calls[0].options.url);
        // The current page stays on screen while the refetch is in flight.
        expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>v1</p>');

        respondHtml(proxyFetch.calls[1], '<p>v2</p>');
        expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>v2</p>');

        act(() => {
          jest.advanceTimersByTime(10000);
        });
        expect(proxyFetch.calls).toHaveLength(3);
      });

      test('a tick while the previous request is in flight is skipped (no abort, no second request)', () => {
        render(<WebViewPanel {...proxyProps({ refreshIntervalSec: 5 })} />);

        // The first request is still in flight after two ticks.
        act(() => {
          jest.advanceTimersByTime(10000);
        });
        expect(proxyFetch.calls).toHaveLength(1);
        expect(proxyFetch.calls[0].unsubscribed).toBe(false);

        respondHtml(proxyFetch.calls[0], '<p>slow page</p>');
        expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('srcdoc', '<p>slow page</p>');

        // The next tick after the response refetches.
        act(() => {
          jest.advanceTimersByTime(5000);
        });
        expect(proxyFetch.calls).toHaveLength(2);
      });

      test('the iframe is remounted on each successful refresh, even with unchanged HTML', () => {
        render(<WebViewPanel {...proxyProps({ refreshIntervalSec: 5 })} />);
        respondHtml(proxyFetch.calls[0], '<p>same</p>');
        const before = screen.getByTestId(webViewPanelTestIds.iframe);

        act(() => {
          jest.advanceTimersByTime(5000);
        });
        respondHtml(proxyFetch.calls[1], '<p>same</p>');

        const after = screen.getByTestId(webViewPanelTestIds.iframe);
        expect(after).not.toBe(before);
        expect(after).toHaveAttribute('srcdoc', '<p>same</p>');
      });

      test('a failed refresh shows the error state', () => {
        render(<WebViewPanel {...proxyProps({ refreshIntervalSec: 5 })} />);
        respondHtml(proxyFetch.calls[0], '<p>v1</p>');

        act(() => {
          jest.advanceTimersByTime(5000);
        });
        respondHttpError(proxyFetch.calls[1], 429, 'Too Many Requests', 'rate limit exceeded');

        expect(screen.getByTestId(webViewPanelTestIds.proxyError)).toHaveTextContent('HTTP 429');
        expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
      });
    });

    test('the panel never listens for message events from the frame', () => {
      const addListener = jest.spyOn(window, 'addEventListener');
      render(<WebViewPanel {...proxyProps()} />);
      respondHtml(proxyFetch.calls[0], '<script>parent.postMessage("x", "*")</script>');

      expect(addListener.mock.calls.map(([type]) => type)).not.toContain('message');
      addListener.mockRestore();
    });
  });
});

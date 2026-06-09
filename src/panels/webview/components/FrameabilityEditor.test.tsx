import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { FrameabilityEditor } from './FrameabilityEditor';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { frameabilityEditorTestIds } from './frameabilityEditorTestIds';
import { useBackendAvailable, type BackendAvailability } from '../useBackendAvailable';

// Mock @grafana/runtime so the editor's getBackendSrv().get(...) call is fully
// controllable. Each test supplies its own `get` implementation (resolve or
// reject) to drive the loading → result / error state transitions.
jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

// DF2: mock the backend-availability hook directly so the Test URL button's
// enable/disable + note are driven independently of the /check-frameable `get`
// mock (whose call counts the existing tests assert on). Defaults to available.
jest.mock('../useBackendAvailable', () => ({
  useBackendAvailable: jest.fn(),
}));

const mockedGetBackendSrv = getBackendSrv as jest.MockedFunction<typeof getBackendSrv>;
const mockedUseBackendAvailable = useBackendAvailable as jest.MockedFunction<typeof useBackendAvailable>;

/** Drives the mocked useBackendAvailable hook for a test. */
function setBackend(state: BackendAvailability) {
  mockedUseBackendAvailable.mockReturnValue(state);
}

type Props = StandardEditorProps<PanelOptions['detectedMode'], unknown, PanelOptions>;

/**
 * Builds editor props plus a Grafana-faithful onChange that persists the bound
 * value to the `detectedMode` path on the shared options object.
 */
function buildProps(optionOverrides: Partial<PanelOptions> = {}): {
  props: Props;
  options: PanelOptions;
  onChange: jest.Mock;
} {
  const options: PanelOptions = {
    ...DEFAULT_PANEL_OPTIONS,
    url: 'https://example.com',
    ...optionOverrides,
  };
  const onChange = jest.fn((mode: PanelOptions['detectedMode']) => {
    options.detectedMode = mode;
  });
  const props = {
    value: options.detectedMode,
    onChange,
    context: { data: [], options },
    item: {} as Props['item'],
  } as unknown as Props;
  return { props, options, onChange };
}

/** Installs a getBackendSrv().get mock for the duration of a test. */
function mockGet(get: jest.Mock) {
  mockedGetBackendSrv.mockReturnValue({ get } as unknown as ReturnType<typeof getBackendSrv>);
  return get;
}

beforeEach(() => {
  mockedGetBackendSrv.mockReset();
  mockedUseBackendAvailable.mockReset();
  // Default for the FR3 tests below: backend available (button enabled as before).
  setBackend({ loading: false, backendAvailable: true });
});

describe('panels/webview/FrameabilityEditor', () => {
  // ---------------------------------------------------------------------------
  // CC: Button disabled / no-op when URL is empty
  // ---------------------------------------------------------------------------

  test('button is disabled and a hint is shown when no URL is configured', () => {
    const get = mockGet(jest.fn());
    const { props } = buildProps({ url: '' });
    render(<FrameabilityEditor {...props} />);

    const button = screen.getByTestId(frameabilityEditorTestIds.testButton);
    expect(button).toBeDisabled();
    expect(screen.getByText(/Enter a URL/i)).toBeInTheDocument();
    // Clicking a disabled button must not issue a request.
    fireEvent.click(button);
    expect(get).not.toHaveBeenCalled();
  });

  // ---------------------------------------------------------------------------
  // CC: Button triggers request to /check-frameable with the current URL
  // ---------------------------------------------------------------------------

  test('clicking the button calls /check-frameable with the current URL', async () => {
    const get = mockGet(jest.fn().mockResolvedValue({ frameable: true, reason: 'ok', recommendedMode: 'direct' }));
    const { props } = buildProps({ url: 'https://example.com' });
    render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));

    await waitFor(() => {
      expect(get).toHaveBeenCalledWith(
        '/api/plugins/wilsonwaters-webview-app/resources/check-frameable',
        { url: 'https://example.com' }
      );
    });
  });

  // ---------------------------------------------------------------------------
  // CC: Loading state renders during the request
  // ---------------------------------------------------------------------------

  test('shows a loading (disabled) state while the request is in flight', async () => {
    let resolveGet: (value: unknown) => void = () => undefined;
    const get = mockGet(
      jest.fn().mockImplementation(() => new Promise((resolve) => {
        resolveGet = resolve;
      }))
    );
    const { props } = buildProps();
    render(<FrameabilityEditor {...props} />);

    const button = screen.getByTestId(frameabilityEditorTestIds.testButton);
    fireEvent.click(button);

    // While the promise is pending the button is disabled and no result/error
    // alert is rendered yet.
    await waitFor(() => expect(button).toBeDisabled());
    expect(screen.queryByTestId(frameabilityEditorTestIds.result)).not.toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);

    // Resolve so the in-flight promise settles and React state updates flush.
    await act(async () => {
      resolveGet({ frameable: true, reason: 'ok', recommendedMode: 'direct' });
    });
  });

  // ---------------------------------------------------------------------------
  // CC: Direct result displayed + detectedMode written on success
  // ---------------------------------------------------------------------------

  test('renders the Direct result and persists detectedMode for a direct recommendation', async () => {
    mockGet(jest.fn().mockResolvedValue({
      frameable: true,
      reason: 'No blocking headers detected.',
      recommendedMode: 'direct',
    }));
    const { props, options, onChange } = buildProps();
    render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));

    const result = await screen.findByTestId(frameabilityEditorTestIds.result);
    expect(result).toHaveTextContent(/Direct/i);
    expect(result).toHaveTextContent('No blocking headers detected.');
    // detectedMode persisted on success.
    expect(onChange).toHaveBeenCalledWith('direct');
    expect(options.detectedMode).toBe('direct');
  });

  // ---------------------------------------------------------------------------
  // CC: Proxied result displayed + detectedMode written on success
  // ---------------------------------------------------------------------------

  test('renders the Proxied result with reason and persists detectedMode for a proxy recommendation', async () => {
    mockGet(jest.fn().mockResolvedValue({
      frameable: false,
      reason: 'X-Frame-Options: DENY',
      recommendedMode: 'proxy',
    }));
    const { props, options, onChange } = buildProps();
    render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));

    const result = await screen.findByTestId(frameabilityEditorTestIds.result);
    expect(result).toHaveTextContent(/Proxied/i);
    expect(result).toHaveTextContent('X-Frame-Options: DENY');
    expect(onChange).toHaveBeenCalledWith('proxy');
    expect(options.detectedMode).toBe('proxy');
  });

  // ---------------------------------------------------------------------------
  // CC: Error state on rejection (non-2xx / network) + detectedMode NOT written
  // ---------------------------------------------------------------------------

  test('renders the Error result with the server reason and does NOT persist detectedMode on rejection', async () => {
    mockGet(jest.fn().mockRejectedValue({
      status: 403,
      statusText: 'Forbidden',
      data: { message: 'URL is not on the allowlist.' },
    }));
    const { props, options, onChange } = buildProps();
    render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));

    const result = await screen.findByTestId(frameabilityEditorTestIds.result);
    expect(result).toHaveTextContent(/Error/i);
    expect(result).toHaveTextContent('URL is not on the allowlist.');
    // detectedMode must NOT be written on error.
    expect(onChange).not.toHaveBeenCalled();
    expect(options.detectedMode).toBeNull();
  });

  test('falls back to status/statusText when a rejection has no body reason', async () => {
    mockGet(jest.fn().mockRejectedValue({ status: 429, statusText: 'Too Many Requests' }));
    const { props } = buildProps();
    render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));

    const result = await screen.findByTestId(frameabilityEditorTestIds.result);
    expect(result).toHaveTextContent(/Error/i);
    expect(result).toHaveTextContent('429 Too Many Requests');
  });

  // ---------------------------------------------------------------------------
  // CC: stale-result hygiene — result resets to idle when the URL changes
  // ---------------------------------------------------------------------------

  test('clears the previous result when the URL changes (no stale recommendation)', async () => {
    mockGet(jest.fn().mockResolvedValue({ frameable: true, reason: 'ok', recommendedMode: 'direct' }));
    const { props, options } = buildProps({ url: 'https://a.example.com' });
    const { rerender } = render(<FrameabilityEditor {...props} />);

    fireEvent.click(screen.getByTestId(frameabilityEditorTestIds.testButton));
    expect(await screen.findByTestId(frameabilityEditorTestIds.result)).toBeInTheDocument();

    // Simulate the standard URL field changing; Grafana re-renders with new options.
    options.url = 'https://b.example.com';
    rerender(<FrameabilityEditor {...props} />);

    expect(screen.queryByTestId(frameabilityEditorTestIds.result)).not.toBeInTheDocument();
  });

  // ---------------------------------------------------------------------------
  // DF2: backend unavailable — Test URL disabled + note; no-op on click
  // ---------------------------------------------------------------------------

  test('disables Test URL and shows a note when the backend is unavailable', () => {
    setBackend({ loading: false, backendAvailable: false });
    const get = mockGet(jest.fn());
    const { props } = buildProps({ url: 'https://example.com' });
    render(<FrameabilityEditor {...props} />);

    const button = screen.getByTestId(frameabilityEditorTestIds.testButton);
    expect(button).toBeDisabled();
    expect(screen.getByTestId(frameabilityEditorTestIds.backendUnavailableNote)).toBeInTheDocument();
    // A click must not issue a request while the backend is down.
    fireEvent.click(button);
    expect(get).not.toHaveBeenCalled();
  });

  // ---------------------------------------------------------------------------
  // DF2: backend available — button enabled, no note (auto-re-enable)
  // ---------------------------------------------------------------------------

  test('enables Test URL and shows no backend note when the backend is available', () => {
    setBackend({ loading: false, backendAvailable: true });
    mockGet(jest.fn());
    const { props } = buildProps({ url: 'https://example.com' });
    render(<FrameabilityEditor {...props} />);

    expect(screen.getByTestId(frameabilityEditorTestIds.testButton)).toBeEnabled();
    expect(screen.queryByTestId(frameabilityEditorTestIds.backendUnavailableNote)).not.toBeInTheDocument();
  });

  // ---------------------------------------------------------------------------
  // DF2: probe still loading — no premature note, button not backend-disabled
  // ---------------------------------------------------------------------------

  test('does not show the backend note while the probe is still loading', () => {
    setBackend({ loading: true, backendAvailable: false });
    mockGet(jest.fn());
    const { props } = buildProps({ url: 'https://example.com' });
    render(<FrameabilityEditor {...props} />);

    expect(screen.queryByTestId(frameabilityEditorTestIds.backendUnavailableNote)).not.toBeInTheDocument();
    // Not disabled *because of* the backend while loading (URL is set).
    expect(screen.getByTestId(frameabilityEditorTestIds.testButton)).toBeEnabled();
  });
});

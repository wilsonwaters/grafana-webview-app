import { renderHook, waitFor } from '@testing-library/react';
import { getBackendSrv } from '@grafana/runtime';

import {
  HEALTH_RESOURCE_PATH,
  useBackendAvailable,
  __resetBackendAvailableCacheForTests,
} from './useBackendAvailable';

// Mock @grafana/runtime so the hook's getBackendSrv().get('/health') call is
// fully controllable. Each test installs its own `get` implementation to drive
// the loading → available / unavailable transitions (mirrors the
// FrameabilityEditor test setup).
jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

const mockedGetBackendSrv = getBackendSrv as jest.MockedFunction<typeof getBackendSrv>;

/** Installs a getBackendSrv().get mock for the duration of a test. */
function mockGet(get: jest.Mock) {
  mockedGetBackendSrv.mockReturnValue({ get } as unknown as ReturnType<typeof getBackendSrv>);
  return get;
}

beforeEach(() => {
  mockedGetBackendSrv.mockReset();
  // Reset the module-scoped probe so every test starts from a cold probe.
  __resetBackendAvailableCacheForTests();
});

describe('panels/webview/useBackendAvailable', () => {
  // ---------------------------------------------------------------------------
  // (1) Initial loading state
  // ---------------------------------------------------------------------------

  test('starts in a loading state with backendAvailable false', () => {
    // A pending (never-resolving) probe keeps the hook in its initial state.
    mockGet(jest.fn().mockReturnValue(new Promise<never>(() => undefined)));

    const { result } = renderHook(() => useBackendAvailable());

    expect(result.current.loading).toBe(true);
    expect(result.current.backendAvailable).toBe(false);
  });

  // ---------------------------------------------------------------------------
  // (2) 200 + { status: 'ok' } ⇒ available
  // ---------------------------------------------------------------------------

  test('resolves to backendAvailable true on 200 + {status:"ok"} and probes /health', async () => {
    const get = mockGet(jest.fn().mockResolvedValue({ status: 'ok' }));

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(true);
    // Probe hits the FR2 health resource path, no params.
    expect(get).toHaveBeenCalledWith(HEALTH_RESOURCE_PATH);
  });

  // ---------------------------------------------------------------------------
  // (3) Rejected promise (network / non-2xx) ⇒ unavailable
  // ---------------------------------------------------------------------------

  test('resolves to backendAvailable false when the probe rejects (non-2xx / network)', async () => {
    mockGet(jest.fn().mockRejectedValue({ status: 503, statusText: 'Service Unavailable' }));

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(false);
  });

  test('resolves to backendAvailable false on a 404 (backend not provisioned)', async () => {
    mockGet(jest.fn().mockRejectedValue({ status: 404, statusText: 'Not Found' }));

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(false);
  });

  // ---------------------------------------------------------------------------
  // (4) 200 with unexpected / degraded body ⇒ unavailable
  // ---------------------------------------------------------------------------

  test('resolves to backendAvailable false on 200 with a {status:"degraded"} body', async () => {
    mockGet(jest.fn().mockResolvedValue({ status: 'degraded' }));

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(false);
  });

  test('resolves to backendAvailable false on 200 with an unexpected body shape', async () => {
    mockGet(jest.fn().mockResolvedValue({ unexpected: true }));

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(false);
  });

  // ---------------------------------------------------------------------------
  // (5) Synchronous throw / timeout ⇒ unavailable
  // ---------------------------------------------------------------------------

  test('resolves to backendAvailable false when the probe throws/times out', async () => {
    mockGet(
      jest.fn().mockImplementation(() => Promise.reject(new Error('timeout of 0ms exceeded')))
    );

    const { result } = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.backendAvailable).toBe(false);
  });

  // ---------------------------------------------------------------------------
  // (6) Shared cache — two hook instances trigger exactly ONE probe
  // ---------------------------------------------------------------------------

  test('shares one probe across multiple hook instances (single /health request)', async () => {
    const get = mockGet(jest.fn().mockResolvedValue({ status: 'ok' }));

    // Two independent renders, as two panel/editor instances would mount.
    const first = renderHook(() => useBackendAvailable());
    const second = renderHook(() => useBackendAvailable());

    await waitFor(() => expect(first.result.current.loading).toBe(false));
    await waitFor(() => expect(second.result.current.loading).toBe(false));

    expect(first.result.current.backendAvailable).toBe(true);
    expect(second.result.current.backendAvailable).toBe(true);
    // The module-scoped cache collapses both onto one network request.
    expect(get).toHaveBeenCalledTimes(1);
  });

  test('does not re-probe on a subsequent mount within the same session', async () => {
    const get = mockGet(jest.fn().mockResolvedValue({ status: 'ok' }));

    const first = renderHook(() => useBackendAvailable());
    await waitFor(() => expect(first.result.current.loading).toBe(false));
    first.unmount();

    // A fresh mount (cache NOT reset) reuses the settled probe — no new request.
    const second = renderHook(() => useBackendAvailable());
    await waitFor(() => expect(second.result.current.loading).toBe(false));

    expect(second.result.current.backendAvailable).toBe(true);
    expect(get).toHaveBeenCalledTimes(1);
  });
});

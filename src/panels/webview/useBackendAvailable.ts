import { useEffect, useState } from 'react';
import { getBackendSrv } from '@grafana/runtime';

import { PLUGIN_ID } from './loadMode';

/**
 * Plugin resource path for the FR2 backend liveness probe.
 *
 * `GET /health` → 200 `{ "status": "ok" }` when the backend component of the
 * app plugin is provisioned, enabled and running. Built from {@link PLUGIN_ID}
 * so the plugin id is never hardcoded twice. `getBackendSrv()` resolves the
 * path under `config.appSubUrl`, so it has no manual sub-url prefix.
 */
export const HEALTH_RESOURCE_PATH = `/api/plugins/${PLUGIN_ID}/resources/health`;

/**
 * Delays before the probe runs again after a negative result: 30 s, 60 s,
 * then every 120 s (the last value is the cap).
 */
export const BACKEND_RETRY_DELAYS_MS: readonly number[] = [30_000, 60_000, 120_000];

/** Shape of the FR2 `/health` liveness body. Only `status: 'ok'` means alive. */
interface HealthResponse {
  status?: string;
}

/**
 * Result of the backend-availability probe.
 *
 * - `loading` is `true` until the first probe of the page session settles.
 * - `backendAvailable` is `false` while loading and is only ever `true` once a
 *   probe confirms a live backend (HTTP 200 + `{ status: 'ok' }`).
 */
export interface BackendAvailability {
  loading: boolean;
  backendAvailable: boolean;
}

/**
 * Module-scoped probe state, shared by every hook instance (all panels and the
 * editor) in the page session.
 *
 * - A positive result is cached for the session: the backend does not go away
 *   in a way the panel can recover from without a reload.
 * - A negative result (a timeout or a 5xx while the plugin restarts, or a
 *   backend that is not provisioned) is not final. The probe runs again after
 *   {@link BACKEND_RETRY_DELAYS_MS} while at least one hook is mounted, so a
 *   long-running kiosk dashboard recovers without a page reload. This replaces
 *   the Q12(a) "probe once, fixed per session" rule for negative results.
 * - Concurrent callers share one in-flight request.
 */
let lastResult: boolean | undefined;
let inFlight: Promise<boolean> | undefined;
let failureCount = 0;
let nextRetryAt = 0;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
const listeners = new Set<(available: boolean) => void>();

/**
 * Runs the probe, or joins the one in flight. Resolves to `true` ONLY on HTTP
 * 200 with the exact FR2 liveness body `{ status: 'ok' }`. Every other outcome
 * (network error, non-2xx, timeout, or an unexpected/`degraded` body) resolves
 * to `false`: not-provisioned, disabled and erroring backends are deliberately
 * the same, because degrading to direct-only is always safe. Never rejects.
 */
function probe(): Promise<boolean> {
  if (lastResult === true) {
    return Promise.resolve(true);
  }
  if (inFlight === undefined) {
    inFlight = getBackendSrv()
      .get<HealthResponse>(HEALTH_RESOURCE_PATH)
      .then((body) => body?.status === 'ok')
      .catch(() => false)
      .then((available) => {
        inFlight = undefined;
        lastResult = available;
        if (available) {
          failureCount = 0;
        } else {
          const delay = BACKEND_RETRY_DELAYS_MS[Math.min(failureCount, BACKEND_RETRY_DELAYS_MS.length - 1)];
          failureCount += 1;
          nextRetryAt = Date.now() + delay;
          scheduleRetry();
        }
        listeners.forEach((listener) => listener(available));
        return available;
      });
  }
  return inFlight;
}

/** Arms the retry timer for the last negative result, while any hook is mounted. */
function scheduleRetry(): void {
  if (retryTimer !== undefined || listeners.size === 0 || lastResult !== false || inFlight !== undefined) {
    return;
  }
  retryTimer = setTimeout(
    () => {
      retryTimer = undefined;
      if (listeners.size > 0) {
        void probe();
      }
    },
    Math.max(0, nextRetryAt - Date.now())
  );
}

function cancelRetry(): void {
  if (retryTimer !== undefined) {
    clearTimeout(retryTimer);
    retryTimer = undefined;
  }
}

/**
 * DF1 — single source of truth for backend availability.
 *
 * Reads the shared probe (see {@link probe}) and exposes a
 * `{ loading, backendAvailable }` flag. Starts in `loading` until the first
 * probe of the session settles; a hook that mounts later starts from the last
 * result. A negative result updates to available when a retry succeeds. DF2
 * (editor degradation) and DF3 (view-mode guard) consume this; it has no UI of
 * its own.
 */
export function useBackendAvailable(): BackendAvailability {
  const [state, setState] = useState<BackendAvailability>(() =>
    lastResult === undefined
      ? { loading: true, backendAvailable: false }
      : { loading: false, backendAvailable: lastResult }
  );

  useEffect(() => {
    let cancelled = false;
    const listener = (available: boolean) => {
      if (!cancelled) {
        // Keep the same object when nothing changed, so React skips the render.
        setState((prev) =>
          !prev.loading && prev.backendAvailable === available ? prev : { loading: false, backendAvailable: available }
        );
      }
    };
    listeners.add(listener);

    if (lastResult === undefined) {
      void probe();
    } else {
      // Report the current result (it can have changed since the first render),
      // and resume the retry schedule after a negative result.
      listener(lastResult);
      scheduleRetry();
    }

    return () => {
      cancelled = true;
      listeners.delete(listener);
      if (listeners.size === 0) {
        cancelRetry();
      }
    };
  }, []);

  return state;
}

/**
 * Test-only: clears the module-scoped probe state so each test starts from a
 * cold probe. Not part of the public hook contract: there is no user-facing
 * manual refetch.
 */
export function __resetBackendAvailableCacheForTests(): void {
  cancelRetry();
  lastResult = undefined;
  inFlight = undefined;
  failureCount = 0;
  nextRetryAt = 0;
  listeners.clear();
}

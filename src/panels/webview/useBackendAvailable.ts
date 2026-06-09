import { useEffect, useState } from 'react';
import { getBackendSrv } from '@grafana/runtime';

import { PLUGIN_ID } from './loadMode';

/**
 * Plugin resource path for the FR2 backend liveness probe.
 *
 * `GET /health` → 200 `{ "status": "ok" }` when the backend component of the
 * app plugin is provisioned, enabled and running. Built from {@link PLUGIN_ID}
 * so the plugin id is never hardcoded twice (mirrors PROXY_RESOURCE_BASE in
 * loadMode.ts). `getBackendSrv()` auto-prepends `config.appSubUrl`, so this is
 * the bare `/api/plugins/...` path with no manual sub-url prefix (matches the
 * FR1 check-frameable call in FrameabilityEditor).
 */
export const HEALTH_RESOURCE_PATH = `/api/plugins/${PLUGIN_ID}/resources/health`;

/** Shape of the FR2 `/health` liveness body. Only `status: 'ok'` means alive. */
interface HealthResponse {
  status?: string;
}

/**
 * Result of the backend-availability probe.
 *
 * - `loading` is `true` until the single per-session probe settles, then
 *   `false` forever.
 * - `backendAvailable` is `false` while loading and is only ever `true` once the
 *   probe confirms a live backend (HTTP 200 + `{ status: 'ok' }`).
 */
export interface BackendAvailability {
  loading: boolean;
  backendAvailable: boolean;
}

/**
 * Module-scoped cache of the in-flight / settled probe.
 *
 * Q12 — "probe once, fixed per session": the probe runs a SINGLE time per page
 * session and every hook instance (across all panels and the editor) shares the
 * same promise. We cache the promise itself (not just its result) so concurrent
 * mounts that race before the first probe settles still collapse onto one
 * network request. Once settled it stays settled — we never poll or re-probe.
 */
let probePromise: Promise<boolean> | undefined;

/**
 * Probes `/health` exactly once and caches the resolving promise at module
 * scope. Subsequent calls return the cached promise without issuing a new
 * request.
 *
 * Fail-safe (Q12): the promise resolves to `true` ONLY on HTTP 200 with the
 * exact FR2 liveness body `{ status: 'ok' }`. Every other outcome — network
 * error, non-2xx (getBackendSrv rejects), timeout, or an unexpected/`degraded`
 * body — resolves to `false`. Not-provisioned, disabled and erroring backends
 * are intentionally indistinguishable: degrading to direct-only is always safe.
 *
 * The promise never rejects, so callers don't need a `.catch`.
 */
function probeBackendAvailable(): Promise<boolean> {
  if (probePromise === undefined) {
    probePromise = getBackendSrv()
      .get<HealthResponse>(HEALTH_RESOURCE_PATH)
      .then((body) => body?.status === 'ok')
      .catch(() => false);
  }
  return probePromise;
}

/**
 * DF1 — single source of truth for backend availability.
 *
 * Reads the shared per-session probe (see {@link probeBackendAvailable}) and
 * exposes a `{ loading, backendAvailable }` flag. Starts in `loading` and
 * resolves to a fail-safe boolean — never a permanent `unknown`. DF2 (editor
 * degradation) and DF3 (view-mode guard) consume this; it has no UI of its own.
 */
export function useBackendAvailable(): BackendAvailability {
  const [state, setState] = useState<BackendAvailability>({ loading: true, backendAvailable: false });

  useEffect(() => {
    let cancelled = false;
    probeBackendAvailable().then((backendAvailable) => {
      if (!cancelled) {
        setState({ loading: false, backendAvailable });
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}

/**
 * Test-only: clears the module-scoped probe cache so each test starts from a
 * cold probe. NOT part of the public hook contract — there is no user-facing
 * manual refetch (Q12: the probe is fixed per session).
 */
export function __resetBackendAvailableCacheForTests(): void {
  probePromise = undefined;
}

import { useEffect, useRef, useState } from 'react';
import { getBackendSrv } from '@grafana/runtime';

/**
 * State of the proxied HTML document for one proxy-mode frame (FR5).
 *
 * - `loading` — the first response for the current request path has not arrived.
 * - `ready`   — `html` is the rewritten HTML from the backend proxy. `version`
 *               changes on every successful response, so a frame keyed on it
 *               reloads even when the HTML is unchanged.
 * - `error`   — the request failed. `httpStatus` is set for a non-2xx response;
 *               `message` is a short plain-text reason (it may be empty).
 */
export type ProxyDocumentState =
  | { status: 'loading' }
  | { status: 'ready'; html: string; version: number }
  | { status: 'error'; httpStatus?: number; message: string };

/** Longest error detail shown to the user, in characters. */
const MAX_ERROR_DETAIL_LENGTH = 300;

/** True for text that looks like an HTML document or fragment. Such text is never shown. */
function looksLikeHtml(text: string): boolean {
  return /^\s*</.test(text);
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === 'string' && value.trim().length > 0) {
      return value;
    }
  }
  return '';
}

/**
 * Turns a rejected `getBackendSrv().fetch` call into a status and a short,
 * plain-text message for the error state.
 *
 * `getBackendSrv` rejects with a `FetchError` (`status`, `statusText`, `data`).
 * For a text response, Grafana moves the body into `data.message` (or replaces
 * an HTML body with `"<status> <statusText>"`). The backend proxy writes its
 * denial reasons as plain text, for example "target host is not allowlisted".
 * The message is collapsed to one line and truncated. An HTML body is dropped:
 * the error body must never be rendered as a document.
 */
export function describeProxyError(err: unknown): { httpStatus?: number; message: string } {
  if (typeof err !== 'object' || err === null) {
    return { message: firstString(err) };
  }
  const e = err as { status?: unknown; statusText?: unknown; message?: unknown; data?: unknown };
  const httpStatus = typeof e.status === 'number' && e.status > 0 ? e.status : undefined;

  let detail = '';
  if (typeof e.data === 'string') {
    detail = e.data;
  } else if (typeof e.data === 'object' && e.data !== null) {
    const data = e.data as { message?: unknown; error?: unknown; reason?: unknown };
    detail = firstString(data.message, data.reason, data.error);
  }
  if (!detail || looksLikeHtml(detail)) {
    detail = firstString(e.statusText, e.message);
  }

  detail = detail.replace(/\s+/g, ' ').trim();
  if (looksLikeHtml(detail)) {
    detail = '';
  }
  if (detail.length > MAX_ERROR_DETAIL_LENGTH) {
    detail = `${detail.slice(0, MAX_ERROR_DETAIL_LENGTH)}…`;
  }
  return { httpStatus, message: detail };
}

/** Internal state: the request path the state belongs to, so a stale state is never shown. */
type InternalState = { fetchUrl: string } & ProxyDocumentState;

/**
 * Fetches the proxied HTML for `fetchUrl` as text (FR5).
 *
 * - Uses `getBackendSrv().fetch`, so the request carries Grafana's session and
 *   organisation headers and resolves under `config.appSubUrl`. Grafana's own
 *   error toast is turned off; the caller shows the error state.
 * - Refetches when `fetchUrl` changes (new URL or hide selectors) and when
 *   `reloadKey` changes (auto-refresh). On a reload the current document (or
 *   error) stays on screen until the new response arrives.
 * - Stale responses are ignored. The effect cleanup unsubscribes (which aborts
 *   the request) and marks the request inactive, so a response for an older
 *   path, an older reload or an unmounted component never updates the state.
 */
export function useProxyDocument(fetchUrl: string, reloadKey: number): ProxyDocumentState {
  const [state, setState] = useState<InternalState>({ fetchUrl, status: 'loading' });
  // Increments on every successful response; used as the frame key.
  const versionRef = useRef(0);

  useEffect(() => {
    let active = true;

    // No state reset here: the render below reports `loading` while the stored
    // state belongs to another path, and a reload of the same path keeps the
    // current state (document or error) until the new response arrives.
    const subscription = getBackendSrv()
      .fetch<string>({
        url: fetchUrl,
        method: 'GET',
        responseType: 'text',
        showErrorAlert: false,
        showSuccessAlert: false,
      })
      .subscribe({
        next: (response) => {
          if (!active) {
            return;
          }
          versionRef.current += 1;
          const html = typeof response.data === 'string' ? response.data : '';
          setState({ fetchUrl, status: 'ready', html, version: versionRef.current });
        },
        error: (err: unknown) => {
          if (!active) {
            return;
          }
          setState({ fetchUrl, status: 'error', ...describeProxyError(err) });
        },
      });

    return () => {
      active = false;
      subscription.unsubscribe();
    };
  }, [fetchUrl, reloadKey]);

  // Until the first response for a new path arrives, the stored state belongs
  // to the previous path. Report loading so the old document is never shown.
  if (state.fetchUrl !== fetchUrl) {
    return { status: 'loading' };
  }
  switch (state.status) {
    case 'ready':
      return { status: 'ready', html: state.html, version: state.version };
    case 'error':
      return { status: 'error', httpStatus: state.httpStatus, message: state.message };
    default:
      return { status: 'loading' };
  }
}

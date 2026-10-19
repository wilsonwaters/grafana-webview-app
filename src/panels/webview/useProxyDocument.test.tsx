import { renderHook } from '@testing-library/react';
import { describeProxyError, useProxyDocument } from './useProxyDocument';
import type { FetchResponse } from '@grafana/runtime';
import {
  deliverLateError,
  deliverLateResponse,
  installProxyFetchMock,
  respondError,
  respondHtml,
  respondHttpError,
  type ProxyFetchMock,
} from './testing/proxyFetchMock';

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

const PATH_A = '/api/plugins/wilsonwaters-webview-app/resources/proxy?url=https%3A%2F%2Fa.example';
const PATH_B = '/api/plugins/wilsonwaters-webview-app/resources/proxy?url=https%3A%2F%2Fb.example';

let mock: ProxyFetchMock;

beforeEach(() => {
  mock = installProxyFetchMock();
});

/** A response whose body records when it is read. A cancelled request must never read it. */
function trackedResponse(html: string): { response: FetchResponse<string>; wasRead: () => boolean } {
  let read = false;
  const response = {
    status: 200,
    ok: true,
    get data() {
      read = true;
      return html;
    },
  } as unknown as FetchResponse<string>;
  return { response, wasRead: () => read };
}

/** An error whose fields record when they are read. A cancelled request must never read it. */
function trackedError(): { error: unknown; wasRead: () => boolean } {
  let read = false;
  const error = {
    get status() {
      read = true;
      return 502;
    },
    get data() {
      read = true;
      return { message: 'late failure' };
    },
  };
  return { error, wasRead: () => read };
}

function renderDoc(fetchUrl: string, reloadKey = 0) {
  return renderHook(({ url, key }: { url: string; key: number }) => useProxyDocument(url, key), {
    initialProps: { url: fetchUrl, key: reloadKey },
  });
}

describe('panels/webview/useProxyDocument (FR5)', () => {
  test('fetches the path as text through getBackendSrv with Grafana error toasts off', () => {
    const { result } = renderDoc(PATH_A);

    expect(result.current).toEqual({ status: 'loading' });
    expect(mock.fetch).toHaveBeenCalledTimes(1);
    expect(mock.calls[0].options).toEqual({
      url: PATH_A,
      method: 'GET',
      responseType: 'text',
      showErrorAlert: false,
      showSuccessAlert: false,
    });
  });

  test('a 2xx response becomes the ready document', () => {
    const { result } = renderDoc(PATH_A);

    respondHtml(mock.calls[0], '<h1>Example Domain</h1>');

    expect(result.current).toEqual({ status: 'ready', html: '<h1>Example Domain</h1>', version: 1 });
  });

  test('a non-2xx response becomes an error with the status and the backend message', () => {
    const { result } = renderDoc(PATH_A);

    respondHttpError(mock.calls[0], 403, 'Forbidden', 'target host is not allowlisted\n');

    expect(result.current).toEqual({
      status: 'error',
      httpStatus: 403,
      message: 'target host is not allowlisted',
    });
  });

  test('stale response: an older path that responds last never overwrites the newer document', () => {
    const { result, rerender } = renderDoc(PATH_A);
    rerender({ url: PATH_B, key: 0 });

    expect(mock.calls).toHaveLength(2);
    // The request for the old path was cancelled.
    expect(mock.calls[0].unsubscribed).toBe(true);

    respondHtml(mock.calls[1], '<p>B</p>');
    respondHtml(mock.calls[0], '<p>A (stale)</p>');

    expect(result.current).toMatchObject({ status: 'ready', html: '<p>B</p>' });
  });

  test('stale response: an older path that fails last never replaces the newer document', () => {
    const { result, rerender } = renderDoc(PATH_A);
    rerender({ url: PATH_B, key: 0 });

    respondHtml(mock.calls[1], '<p>B</p>');
    respondHttpError(mock.calls[0], 502, 'Bad Gateway', 'upstream failed');

    expect(result.current).toMatchObject({ status: 'ready', html: '<p>B</p>' });
  });

  test('a new path shows loading at once, never the previous document', () => {
    const { result, rerender } = renderDoc(PATH_A);
    respondHtml(mock.calls[0], '<p>A</p>');

    rerender({ url: PATH_B, key: 0 });

    expect(result.current).toEqual({ status: 'loading' });
  });

  test('a reload refetches and keeps the current document until the new one arrives', () => {
    const { result, rerender } = renderDoc(PATH_A, 0);
    respondHtml(mock.calls[0], '<p>v1</p>');

    rerender({ url: PATH_A, key: 1 });

    expect(mock.calls).toHaveLength(2);
    expect(mock.calls[1].options.url).toBe(PATH_A);
    expect(result.current).toEqual({ status: 'ready', html: '<p>v1</p>', version: 1 });

    respondHtml(mock.calls[1], '<p>v2</p>');

    expect(result.current).toEqual({ status: 'ready', html: '<p>v2</p>', version: 2 });
  });

  test('a reload with unchanged HTML still gets a new version (the frame reloads)', () => {
    const { result, rerender } = renderDoc(PATH_A, 0);
    respondHtml(mock.calls[0], '<p>same</p>');
    rerender({ url: PATH_A, key: 1 });
    respondHtml(mock.calls[1], '<p>same</p>');

    expect(result.current).toEqual({ status: 'ready', html: '<p>same</p>', version: 2 });
  });

  test('a reload while a request is in flight is skipped: no second request and no cancellation', () => {
    const { result, rerender } = renderDoc(PATH_A, 0);
    rerender({ url: PATH_A, key: 1 });
    rerender({ url: PATH_A, key: 2 });

    expect(mock.fetch).toHaveBeenCalledTimes(1);
    expect(mock.calls[0].unsubscribed).toBe(false);

    // The slow request still completes and is shown.
    respondHtml(mock.calls[0], '<p>slow</p>');
    expect(result.current).toEqual({ status: 'ready', html: '<p>slow</p>', version: 1 });

    // The next tick after the response fetches again.
    rerender({ url: PATH_A, key: 3 });
    expect(mock.fetch).toHaveBeenCalledTimes(2);
  });

  test('a reload after a failed request fetches again', () => {
    const { rerender } = renderDoc(PATH_A, 0);
    respondHttpError(mock.calls[0], 502, 'Bad Gateway', 'upstream failed');

    rerender({ url: PATH_A, key: 1 });

    expect(mock.fetch).toHaveBeenCalledTimes(2);
  });

  test('a reload and a new path in the same render fetch the new path once', () => {
    const { rerender } = renderDoc(PATH_A, 0);
    rerender({ url: PATH_B, key: 1 });

    expect(mock.fetch).toHaveBeenCalledTimes(2);
    expect(mock.calls[0].unsubscribed).toBe(true);
    expect(mock.calls[1].options.url).toBe(PATH_B);
  });

  test('the first render never counts as a reload', () => {
    renderDoc(PATH_A, 7);

    expect(mock.fetch).toHaveBeenCalledTimes(1);
  });

  test('a failed reload shows the error', () => {
    const { result, rerender } = renderDoc(PATH_A, 0);
    respondHtml(mock.calls[0], '<p>v1</p>');
    rerender({ url: PATH_A, key: 1 });

    respondHttpError(mock.calls[1], 429, 'Too Many Requests', 'rate limit exceeded');

    expect(result.current).toEqual({ status: 'error', httpStatus: 429, message: 'rate limit exceeded' });
  });

  test('a successful reload after an error shows the document', () => {
    const { result, rerender } = renderDoc(PATH_A, 0);
    respondHttpError(mock.calls[0], 502, 'Bad Gateway', 'upstream failed');
    rerender({ url: PATH_A, key: 1 });

    // The error stays until the reload responds.
    expect(result.current).toMatchObject({ status: 'error', httpStatus: 502 });

    respondHtml(mock.calls[1], '<p>back</p>');

    expect(result.current).toMatchObject({ status: 'ready', html: '<p>back</p>' });
  });

  test('unmount cancels the request, and a response delivered after unmount is never read', () => {
    const { unmount } = renderDoc(PATH_A);

    unmount();
    expect(mock.calls[0].unsubscribed).toBe(true);

    // A transport that ignores the cancellation still delivers the response.
    // The hook must drop it before it reads the body.
    const late = trackedResponse('<p>late</p>');
    deliverLateResponse(mock.calls[0], late.response);
    expect(late.wasRead()).toBe(false);

    const lateError = trackedError();
    deliverLateError(mock.calls[0], lateError.error);
    expect(lateError.wasRead()).toBe(false);
  });

  test('a response for a cancelled path that still arrives never replaces the newer document', () => {
    const { result, rerender } = renderDoc(PATH_A);
    rerender({ url: PATH_B, key: 0 });
    respondHtml(mock.calls[1], '<p>B</p>');

    const late = trackedResponse('<p>A (cancelled)</p>');
    deliverLateResponse(mock.calls[0], late.response);
    const lateError = trackedError();
    deliverLateError(mock.calls[0], lateError.error);

    expect(late.wasRead()).toBe(false);
    expect(lateError.wasRead()).toBe(false);
    expect(result.current).toEqual({ status: 'ready', html: '<p>B</p>', version: 1 });
  });

  test('a network failure (no HTTP status) becomes an error without a status', () => {
    const { result } = renderDoc(PATH_A);

    respondError(mock.calls[0], new TypeError('Failed to fetch'));

    expect(result.current).toEqual({ status: 'error', httpStatus: undefined, message: 'Failed to fetch' });
  });
});

describe('panels/webview/describeProxyError', () => {
  test('uses data.message from a processed FetchError', () => {
    expect(
      describeProxyError({ status: 400, statusText: 'Bad Request', data: { message: 'invalid target URL: scheme' } })
    ).toEqual({ httpStatus: 400, message: 'invalid target URL: scheme' });
  });

  test('falls back to data.reason and data.error', () => {
    expect(describeProxyError({ status: 403, data: { reason: 'denied' } })).toEqual({
      httpStatus: 403,
      message: 'denied',
    });
    expect(describeProxyError({ status: 500, data: { error: 'boom' } })).toEqual({ httpStatus: 500, message: 'boom' });
  });

  test('uses a raw string body', () => {
    expect(describeProxyError({ status: 404, statusText: 'Not Found', data: 'no such page' })).toEqual({
      httpStatus: 404,
      message: 'no such page',
    });
  });

  test('never returns an HTML body: falls back to the status text', () => {
    const html = '<!doctype html><html><body><script>alert(1)</script>Not found</body></html>';
    expect(describeProxyError({ status: 404, statusText: 'Not Found', data: html })).toEqual({
      httpStatus: 404,
      message: 'Not Found',
    });
    expect(describeProxyError({ status: 502, data: { message: '  <html>bad gateway</html>' } })).toEqual({
      httpStatus: 502,
      message: '',
    });
  });

  test('collapses whitespace and truncates a long message', () => {
    const result = describeProxyError({ status: 500, data: 'line one\n\n  line two  ' + 'x'.repeat(1000) });
    expect(result.message.startsWith('line one line two x')).toBe(true);
    expect(result.message.length).toBe(301);
    expect(result.message.endsWith('…')).toBe(true);
  });

  test('ignores a non-positive status (cancelled request)', () => {
    expect(describeProxyError({ status: -1, statusText: 'Request was aborted', cancelled: true })).toEqual({
      httpStatus: undefined,
      message: 'Request was aborted',
    });
  });

  test('handles non-object errors', () => {
    expect(describeProxyError('plain failure')).toEqual({ message: 'plain failure' });
    expect(describeProxyError(undefined)).toEqual({ message: '' });
    expect(describeProxyError(null)).toEqual({ message: '' });
  });
});

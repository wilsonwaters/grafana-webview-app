/**
 * Test helper: a controllable `getBackendSrv().fetch` for proxy-mode tests (FR5).
 *
 * Each `fetch` call returns an Observable backed by its own Subject, so a test
 * decides when (and in which order) each request responds. The test file must
 * mock `@grafana/runtime` first:
 *
 * ```ts
 * jest.mock('@grafana/runtime', () => ({ getBackendSrv: jest.fn() }));
 * ```
 *
 * Not used by production code.
 */
import { act } from '@testing-library/react';
import { getBackendSrv, type BackendSrvRequest, type FetchResponse } from '@grafana/runtime';
import { Observable, Subject } from 'rxjs';

export interface ProxyFetchCall {
  options: BackendSrvRequest;
  subject: Subject<FetchResponse<string>>;
  /** True once the caller has unsubscribed (which aborts a real request). */
  unsubscribed: boolean;
}

export interface ProxyFetchMock {
  calls: ProxyFetchCall[];
  fetch: jest.Mock;
}

/** Installs the mock as the return value of the mocked `getBackendSrv`. */
export function installProxyFetchMock(): ProxyFetchMock {
  const calls: ProxyFetchCall[] = [];
  const fetch = jest.fn((options: BackendSrvRequest) => {
    const subject = new Subject<FetchResponse<string>>();
    const call: ProxyFetchCall = { options, subject, unsubscribed: false };
    calls.push(call);
    return new Observable<FetchResponse<string>>((subscriber) => {
      const inner = subject.subscribe(subscriber);
      return () => {
        call.unsubscribed = true;
        inner.unsubscribe();
      };
    });
  });
  (getBackendSrv as jest.Mock).mockReturnValue({ fetch });
  return { calls, fetch };
}

/** Completes `call` with a 200 text response. */
export function respondHtml(call: ProxyFetchCall, html: string): void {
  act(() => {
    call.subject.next({
      data: html,
      status: 200,
      statusText: 'OK',
      ok: true,
      headers: new Headers({ 'Content-Type': 'text/html; charset=utf-8' }),
      url: call.options.url,
      type: 'basic',
      redirected: false,
      config: call.options,
    } as FetchResponse<string>);
    call.subject.complete();
  });
}

/** Fails `call` the way `getBackendSrv` does for a non-2xx response with a text body. */
export function respondHttpError(call: ProxyFetchCall, status: number, statusText: string, body: string): void {
  act(() => {
    call.subject.error({
      status,
      statusText,
      // Grafana's processRequestError moves a text body into data.message.
      data: { message: body, error: statusText, response: body },
      config: call.options,
    });
  });
}

/** Fails `call` with an arbitrary error value. */
export function respondError(call: ProxyFetchCall, err: unknown): void {
  act(() => {
    call.subject.error(err);
  });
}

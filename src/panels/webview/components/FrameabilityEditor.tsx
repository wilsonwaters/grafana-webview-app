import React, { useCallback, useEffect, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Tooltip, useStyles2 } from '@grafana/ui';
import { PLUGIN_ID } from '../../../constants';
import { normalizeOptions, type PanelOptions } from '../../../types';
import { detectedModeForUrl } from '../loadMode';
import { validateWebViewUrl } from '../url';
import { useBackendAvailable } from '../useBackendAvailable';
import { frameabilityEditorTestIds } from './frameabilityEditorTestIds';

/**
 * Plugin resource path for the FR1 frameability check.
 *
 * `GET /check-frameable?url=<url>` →
 *   - 200 `{ frameable, reason, recommendedMode: 'direct' | 'proxy' }` when the
 *     security pipeline passes (see OPEN-QUESTIONS Q7);
 *   - a non-2xx status (400/403/429/405/…) whose body carries a `message`/reason
 *     on a security denial or upstream/network error.
 */
export const CHECK_FRAMEABLE_PATH = `/api/plugins/${PLUGIN_ID}/resources/check-frameable`;

/** Shape of a successful (HTTP 200) /check-frameable response. */
interface CheckFrameableResponse {
  frameable: boolean;
  reason: string;
  recommendedMode: 'direct' | 'proxy';
}

/**
 * Discriminated state of the Test URL control.
 * - `idle`    — no check has been run (or the URL changed since the last one).
 * - `loading` — a request is in flight.
 * - `result`  — a 200 response was received; `data` holds the recommendation.
 * - `error`   — the request rejected (non-2xx or network); `message` is shown.
 */
type CheckState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'result'; data: CheckFrameableResponse }
  | { status: 'error'; message: string };

/**
 * Extracts a human-readable reason from a rejected getBackendSrv request.
 *
 * getBackendSrv rejects with a `FetchError`-like object whose `data` is the
 * parsed JSON body. The FR1 backend puts the reason in `message` (falling back
 * to `reason`/`error`). We degrade gracefully to the HTTP statusText or a
 * generic message so the UI always shows something actionable.
 */
function extractErrorMessage(err: unknown): string {
  if (typeof err === 'string') {
    return err;
  }
  if (err && typeof err === 'object') {
    const e = err as {
      data?: { message?: string; reason?: string; error?: string };
      statusText?: string;
      status?: number;
      message?: string;
    };
    const fromData = e.data?.message ?? e.data?.reason ?? e.data?.error;
    if (fromData) {
      return fromData;
    }
    if (e.statusText) {
      return e.status ? `${e.status} ${e.statusText}` : e.statusText;
    }
    if (e.message) {
      return e.message;
    }
  }
  return 'The frameability check failed. Please try again.';
}

/**
 * FR3 — the "Test URL" frameability control rendered in the panel editor.
 *
 * Registered as a custom panel options editor (see `module.tsx`,
 * `builder.addCustomEditor`) bound to the `detectedMode` path. Grafana renders
 * custom option editors only in the edit pane, so this never appears in view
 * mode (detection is config-time only — FR is explicit that view mode never
 * re-detects).
 *
 * Behaviour:
 * - The button calls `/check-frameable` with the current, validated and trimmed
 *   `context.options.url` via `getBackendSrv().get(...)`, which encodes the
 *   `url` query param.
 * - States: idle → loading (the button is disabled) → result/error.
 * - 200 `recommendedMode:'direct'` → "Direct" success; `'proxy'` → "Proxied"
 *   info with the reason; rejection → "Error" with the server reason/message.
 * - On a SUCCESSFUL response, `recommendedMode` is persisted into
 *   `options.detectedMode` through the bound `onChange`, and the tested URL
 *   into `options.detectedForUrl`. Auto mode uses the verdict only while the
 *   URL is still the tested one. Nothing is written on error.
 * - The button is disabled (and a click is a no-op) when the URL is empty or
 *   not a valid http(s) URL.
 *
 * Stale-result hygiene: each click takes a new request id, and unmounting
 * invalidates every id. A response is applied (shown AND persisted) only when
 * its request is still the latest one and the URL is still the URL it tested;
 * otherwise it is dropped. So a verdict for one URL is never saved against
 * another, and a slow older response never overwrites a newer one.
 */
export function FrameabilityEditor({
  onChange,
  context,
}: StandardEditorProps<PanelOptions['detectedMode'], unknown, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const opts = normalizeOptions(context.options ?? {});
  // Only a valid http(s) URL is sent, and it is sent trimmed: the same URL
  // that the panel loads (VR-1).
  const check = validateWebViewUrl(opts.url);
  const url = check.valid ? check.url : opts.url;
  const hasUrl = url.length > 0;
  // A saved verdict that was measured for another URL. Auto mode ignores it.
  const verdictIsForOtherUrl =
    opts.loadMode === 'auto' && opts.detectedMode !== null && detectedModeForUrl(opts) === null;

  // DF2: the frameability check is a backend resource (/check-frameable). When
  // the plugin backend is unavailable the check cannot run, so the Test URL
  // button is disabled and an explanatory note is shown. While the probe is
  // still `loading` we do NOT degrade (avoid flashing the note); the button
  // simply stays enabled until the probe settles, then auto-degrades/recovers.
  const { loading: backendLoading, backendAvailable } = useBackendAvailable();
  const backendDown = !backendLoading && !backendAvailable;

  // The stored state is tagged with the URL it was produced for. When the URL
  // changes the previous result/error no longer applies, so we treat any
  // mismatched state as idle during render (the React-recommended way to adjust
  // state on a prop change — no setState-in-effect, no cascading renders). An
  // in-flight request started against the old URL is discarded on settle by the
  // same url comparison in the handler below.
  const [stored, setStored] = useState<{ url: string; state: CheckState }>({ url, state: { status: 'idle' } });
  const state: CheckState = stored.url === url ? stored.state : { status: 'idle' };

  // The id of the latest request. Unmounting bumps it, which invalidates every
  // request that is still in flight.
  const requestIdRef = useRef(0);
  useEffect(() => {
    return () => {
      requestIdRef.current += 1;
    };
  }, []);

  // onChange is bound to the `detectedMode` path, and `context.options` is the
  // live options object. Keep the latest references so the async handler
  // commits through the current callback and compares with the current URL.
  const onChangeRef = useRef(onChange);
  const optionsRef = useRef(context.options);
  useEffect(() => {
    onChangeRef.current = onChange;
    optionsRef.current = context.options;
  });

  const handleTest = useCallback(async () => {
    if (!check.valid || backendDown) {
      return;
    }
    const requestId = ++requestIdRef.current;
    const requestUrl = url;
    // True while this request is the latest one and the URL has not changed.
    const isCurrent = () =>
      requestId === requestIdRef.current && normalizeOptions(optionsRef.current ?? {}).url.trim() === requestUrl;

    setStored({ url: requestUrl, state: { status: 'loading' } });

    let data: CheckFrameableResponse;
    try {
      data = await getBackendSrv().get<CheckFrameableResponse>(CHECK_FRAMEABLE_PATH, { url: requestUrl });
    } catch (err) {
      if (isCurrent()) {
        // Nothing is persisted on error: a denial or failure has no useful mode.
        setStored({ url: requestUrl, state: { status: 'error', message: extractErrorMessage(err) } });
      }
      return;
    }
    if (!isCurrent()) {
      return;
    }
    setStored({ url: requestUrl, state: { status: 'result', data } });
    // Persist the verdict and the URL it is for, so view mode never re-detects.
    // The bound onChange can write only `detectedMode`. Grafana copies the live
    // options object when it applies the change, so `detectedForUrl` is set on
    // that object first and is saved in the same update.
    const options = optionsRef.current;
    if (options) {
      options.detectedForUrl = requestUrl;
    }
    onChangeRef.current(data.recommendedMode);
  }, [check.valid, url, backendDown]);

  const buttonDisabled = !check.valid || backendDown || state.status === 'loading';

  // The disabled button is wrapped in a Tooltip explaining *why* it is disabled
  // when the backend is down, so the reason is perceivable on hover/focus even
  // before the note below is read. (No tooltip in the normal/enabled case.)
  const button = (
    <Button
      variant="secondary"
      size="sm"
      icon="link"
      data-testid={frameabilityEditorTestIds.testButton}
      onClick={handleTest}
      disabled={buttonDisabled}
    >
      Test URL
    </Button>
  );

  return (
    <div className={styles.wrapper}>
      {backendDown ? (
        <Tooltip content="Backend unavailable — the URL test requires the plugin backend.">
          {/* Tooltip needs a focusable/hoverable wrapper around a disabled button. */}
          <span className={styles.tooltipTarget}>{button}</span>
        </Tooltip>
      ) : (
        button
      )}

      {backendDown && (
        <Alert
          severity="info"
          title="Backend unavailable — direct iframe only"
          data-testid={frameabilityEditorTestIds.backendUnavailableNote}
        >
          The proxy and URL test require the plugin backend.
        </Alert>
      )}

      {verdictIsForOtherUrl && state.status === 'idle' && (
        <div className={styles.hint} data-testid={frameabilityEditorTestIds.untestedNote}>
          The saved test result is for a different URL. Auto mode loads this URL directly until you test it.
        </div>
      )}

      {!hasUrl && !backendDown && (
        <div className={styles.hint}>Enter a URL in the &ldquo;URL&rdquo; field above to test it.</div>
      )}

      {hasUrl && !check.valid && !backendDown && (
        <div className={styles.hint} data-testid={frameabilityEditorTestIds.invalidUrlHint}>
          This URL cannot be tested. {check.message}
        </div>
      )}

      {state.status === 'result' && state.data.recommendedMode === 'direct' && (
        <Alert
          severity="success"
          title="Direct — frames without a proxy"
          data-testid={frameabilityEditorTestIds.result}
        >
          {state.data.reason}
        </Alert>
      )}

      {state.status === 'result' && state.data.recommendedMode === 'proxy' && (
        <Alert
          severity="info"
          title="Proxied — framing blocked, use the proxy"
          data-testid={frameabilityEditorTestIds.result}
        >
          {state.data.reason}
        </Alert>
      )}

      {state.status === 'error' && (
        <Alert severity="error" title="Error" data-testid={frameabilityEditorTestIds.result}>
          {state.message}
        </Alert>
      )}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(1)};
  `,
  hint: css`
    font-size: ${theme.typography.bodySmall.fontSize};
    color: ${theme.colors.text.secondary};
  `,
  // A disabled <button> does not emit hover/focus events, so wrap it in an
  // inline-block span that the Tooltip can anchor to. Sized to the button.
  tooltipTarget: css`
    display: inline-block;
  `,
});

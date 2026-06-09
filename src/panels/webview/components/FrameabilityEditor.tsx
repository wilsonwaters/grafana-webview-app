import React, { useCallback, useEffect, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Tooltip, useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
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
const CHECK_FRAMEABLE_PATH = '/api/plugins/wilsonwaters-webview-app/resources/check-frameable';

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
 * - The button calls `/check-frameable` with the current `context.options.url`
 *   via `getBackendSrv().get(...)`, letting it encode the `url` query param.
 * - States: idle → loading (button shows a spinner / disabled) → result/error.
 * - 200 `recommendedMode:'direct'` → "Direct" success; `'proxy'` → "Proxied"
 *   info with the reason; rejection → "Error" with the server reason/message.
 * - On a SUCCESSFUL response, `recommendedMode` is persisted into
 *   `options.detectedMode` through the bound `onChange`. `detectedMode` is NOT
 *   written on error.
 * - The button is disabled (and a click is a no-op) when the URL is empty.
 *
 * Stale-result hygiene: each click bumps a request id; a resolved/rejected
 * request only updates state if it is still the latest one and the component is
 * still mounted.
 */
export function FrameabilityEditor({
  onChange,
  context,
}: StandardEditorProps<PanelOptions['detectedMode'], unknown, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const opts = normalizeOptions(context.options ?? {});
  const url = opts.url.trim();
  const hasUrl = url.length > 0;

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

  // Guards against setting state after unmount.
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // onChange is bound to the `detectedMode` path; keep the latest reference so
  // the async handler always commits through the current callback.
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  });

  const handleTest = useCallback(async () => {
    if (!hasUrl || backendDown) {
      return;
    }
    // Capture the URL this request is for; if the URL changes (or the component
    // unmounts) before it settles, the result is stale and must be discarded.
    const requestUrl = url;
    setStored({ url: requestUrl, state: { status: 'loading' } });

    try {
      const data = await getBackendSrv().get<CheckFrameableResponse>(CHECK_FRAMEABLE_PATH, { url: requestUrl });
      if (!mountedRef.current) {
        return;
      }
      setStored({ url: requestUrl, state: { status: 'result', data } });
      // Persist the recommended mode so the auto-mode path doesn't re-detect.
      onChangeRef.current(data.recommendedMode);
    } catch (err) {
      if (!mountedRef.current) {
        return;
      }
      // Do NOT write detectedMode on error — a denial/error has no useful mode.
      setStored({ url: requestUrl, state: { status: 'error', message: extractErrorMessage(err) } });
    }
  }, [hasUrl, url, backendDown]);

  const buttonDisabled = !hasUrl || backendDown || state.status === 'loading';

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

      {!hasUrl && !backendDown && (
        <div className={styles.hint}>Enter a URL in the &ldquo;URL&rdquo; field above to test it.</div>
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

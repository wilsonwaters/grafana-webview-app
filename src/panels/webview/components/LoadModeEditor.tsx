import React, { useCallback } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, SelectableValue, StandardEditorProps } from '@grafana/data';
import { Alert, RadioButtonGroup, useStyles2 } from '@grafana/ui';
import { type PanelOptions } from '../../../types';
import { useBackendAvailable } from '../useBackendAvailable';
import { loadModeEditorTestIds } from './loadModeEditorTestIds';

type LoadMode = PanelOptions['loadMode'];

/**
 * The Auto/Direct/Proxy choices, mirroring the prior `addRadio` settings in
 * `module.tsx`. Auto and Proxy both depend on the backend (Auto can resolve to
 * proxy via the stored detection result), so both are disabled when the backend
 * is unavailable, leaving Direct as the only selectable option.
 */
const LOAD_MODE_OPTIONS: Array<SelectableValue<LoadMode> & { value: LoadMode }> = [
  { value: 'auto', label: 'Auto' },
  { value: 'direct', label: 'Direct' },
  { value: 'proxy', label: 'Proxy' },
];

/** Modes that require the plugin backend (the proxy resource). */
const BACKEND_DEPENDENT_MODES: ReadonlySet<LoadMode> = new Set<LoadMode>(['auto', 'proxy']);

/**
 * DF2 — the "Load mode" selector rendered in the panel editor.
 *
 * Registered as a custom panel options editor (see `module.tsx`,
 * `builder.addCustomEditor`) bound to the `loadMode` path. It replaces the
 * former `builder.addRadio({ path: 'loadMode' })` so it can consume the async
 * {@link useBackendAvailable} probe — a standard builder option cannot read
 * async hook state.
 *
 * Degradation behaviour (DF2):
 * - While the probe is `loading`, every option is shown enabled (neutral) and no
 *   note is flashed — the probe settles quickly and we avoid a jarring momentary
 *   "backend unavailable" message that then disappears.
 * - When `backendAvailable === false`, the proxy-dependent options (Auto and
 *   Proxy) are **omitted** so only `direct` is offered, and an `info` Alert
 *   explains that only direct iframing is possible. (We omit rather than rely on per-option `disabled`
 *   because `@grafana/ui`'s `RadioButtonGroup` does not propagate per-option
 *   `disabled` to the underlying input — so a disabled-styled option would still
 *   be selectable/focusable. Omitting makes Direct the genuinely-only choice and
 *   is accessible: there is no phantom-enabled control.)
 * - When `backendAvailable === true`, all options are shown and no note shows.
 *
 * Stored Auto or Proxy while degraded (RV1-28): we do NOT call
 * `onChange('direct')` on mount, because that would silently dirty an
 * already-saved panel (e.g. one saved with `proxy`) merely by opening its
 * editor while the backend happens to be down. The stored value is left
 * untouched until the author picks something. Direct is therefore shown
 * **unselected**, and the note names the saved mode, so a click on Direct is a
 * real change that persists `direct`. (Showing Direct as already selected made
 * that click a no-op.) The separate DF3 view-mode guard shows a "backend proxy
 * unavailable" message for a proxy-resolved panel, so a stored `proxy`/`auto`
 * value never proxies when the backend is gone.
 */
export function LoadModeEditor({
  value,
  onChange,
}: StandardEditorProps<LoadMode, unknown, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const { loading, backendAvailable } = useBackendAvailable();

  // Treat "still probing" as not-yet-degraded: keep options enabled and show no
  // note. Only a settled, definitively-unavailable backend triggers degradation.
  const degraded = !loading && !backendAvailable;

  const stored: LoadMode = value ?? 'auto';
  // When degraded, only Direct is offered. It is selected only when it is the
  // stored value; otherwise nothing is selected, so clicking Direct commits it.
  const displayed: LoadMode | undefined = degraded && stored !== 'direct' ? undefined : stored;

  // When degraded, omit the backend-dependent options entirely (see component
  // doc) so Direct is the only genuinely-selectable choice. Otherwise offer all.
  const options = degraded
    ? LOAD_MODE_OPTIONS.filter((opt) => !BACKEND_DEPENDENT_MODES.has(opt.value))
    : LOAD_MODE_OPTIONS;

  const handleChange = useCallback(
    (next: LoadMode) => {
      // Defensive: while degraded the backend-dependent options are not even
      // rendered, but guard the commit path so a programmatic/stale event can
      // never persist a mode the backend cannot honour.
      if (degraded && BACKEND_DEPENDENT_MODES.has(next)) {
        return;
      }
      onChange(next);
    },
    [degraded, onChange]
  );

  return (
    <div className={styles.wrapper}>
      <RadioButtonGroup<LoadMode>
        options={options}
        value={displayed}
        onChange={handleChange}
        data-testid={loadModeEditorTestIds.radioGroup}
        aria-label="Load mode"
      />

      {degraded && (
        <Alert
          severity="info"
          title="Backend unavailable — direct iframe only"
          data-testid={loadModeEditorTestIds.backendUnavailableNote}
        >
          The proxy and URL test require the plugin backend. Until it is available, only direct iframing is possible.
          {stored !== 'direct' && (
            <>
              {' '}
              This panel is saved with the {stored === 'auto' ? 'Auto' : 'Proxy'} load mode. Select Direct to load the URL
              directly.
            </>
          )}
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
});

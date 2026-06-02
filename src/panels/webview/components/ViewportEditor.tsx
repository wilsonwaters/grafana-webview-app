import React, { useCallback, useEffect, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import {
  buildViewportTransform,
  panDelta,
  VIEWPORT_ZOOM_MAX,
  VIEWPORT_ZOOM_MIN,
  zoomAtCursor,
} from '../viewport';
import { viewportEditorTestIds } from './viewportEditorTestIds';

/**
 * The live, interactive viewport state the editor manipulates. A subset of the
 * full panel options — only the three fields this editor positions.
 */
interface ViewportState {
  viewportX: number;
  viewportY: number;
  viewportZoom: number;
}

/**
 * Multiplicative wheel-zoom step. A single wheel "notch" zooms in/out by 10 %.
 * Cursor-anchored via {@link zoomAtCursor}.
 */
const WHEEL_ZOOM_STEP = 1.1;

/**
 * Config-mode interactive viewport editor (PC3).
 *
 * Registered as a custom panel options editor (see `module.tsx`,
 * `builder.addCustomEditor`). Grafana renders custom option editors only in the
 * panel edit pane, so this component is never shown in view mode — the view
 * (PC1 `WebViewPanel`) stays static and non-interactive. This is the Q3
 * resolution: there is no edit-vs-view detection inside the panel component.
 *
 * Interaction model (see research-context.md "Mouse Event Handling"):
 * - The CONTAINER captures all pointer events; the iframe has
 *   `pointer-events: none` so drags/wheels never reach the embedded page.
 * - Drag → pan: screen-pixel deltas are converted to virtual offsets via
 *   `panDelta` (zoom-aware) and added to `viewportX/Y`.
 * - Wheel → zoom: `zoomAtCursor` keeps the virtual point under the cursor fixed
 *   while changing zoom, clamped to [0.1, 5.0].
 *
 * Persistence note: a custom editor's `onChange` is bound to a single options
 * path (Grafana wires it as `value => onChange(item.path, value)`), so it cannot
 * set three sibling fields in one synchronous call without dropping updates.
 * This component therefore keeps the authoritative live state locally (driving
 * the preview + readout instantly) and flushes changed fields to the panel
 * options one-per-render via the bound `onChange` and per-field callbacks, each
 * reading the freshest committed options. See {@link useViewportPersistence}.
 */
export function ViewportEditor({ value, onChange, context }: StandardEditorProps<number, unknown, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const opts = normalizeOptions(context.options ?? {});

  // Authoritative live state for the preview + readout. Seeded from saved
  // options; kept in sync when the options change from outside this editor
  // (e.g. PC4 numeric inputs, dashboard reload). Local interactions update this
  // immediately for a lag-free preview, then persist asynchronously.
  const [viewport, setViewport] = useState<ViewportState>({
    viewportX: opts.viewportX,
    viewportY: opts.viewportY,
    viewportZoom: opts.viewportZoom,
  });

  const persist = useViewportPersistence(value, onChange, context.options);

  // Remember the last state we committed so the re-sync below can distinguish
  // our own writes from genuine external edits (e.g. PC4 numeric inputs,
  // dashboard reload) and avoid clobbering live interaction state.
  const committedRef = useRef<ViewportState>({
    viewportX: opts.viewportX,
    viewportY: opts.viewportY,
    viewportZoom: opts.viewportZoom,
  });

  // Re-sync from EXTERNAL option changes only. If the incoming options match
  // what we last committed, the change originated here and local state is
  // already correct — skip to avoid a feedback loop / stale clobber.
  useEffect(() => {
    const c = committedRef.current;
    if (opts.viewportX === c.viewportX && opts.viewportY === c.viewportY && opts.viewportZoom === c.viewportZoom) {
      return;
    }
    committedRef.current = {
      viewportX: opts.viewportX,
      viewportY: opts.viewportY,
      viewportZoom: opts.viewportZoom,
    };
    setViewport(committedRef.current);
  }, [opts.viewportX, opts.viewportY, opts.viewportZoom]);

  const containerRef = useRef<HTMLDivElement | null>(null);
  // Drag tracking. Null when not dragging.
  const dragRef = useRef<{ lastX: number; lastY: number } | null>(null);
  // Latest viewport, mirrored into a ref so the window-level move handler always
  // reads current values without re-binding listeners. Kept current both by an
  // effect (for external/state-driven updates) and synchronously inside the
  // event-driven `commit` (so rapid drag moves in one frame accumulate
  // correctly without waiting for a re-render).
  const viewportRef = useRef(viewport);
  useEffect(() => {
    viewportRef.current = viewport;
  }, [viewport]);

  const commit = useCallback(
    (next: ViewportState) => {
      // Synchronous ref updates (event-handler context): keep subsequent
      // same-frame drag moves accurate and mark this as our own write.
      viewportRef.current = next;
      committedRef.current = next;
      setViewport(next);
      persist(next);
    },
    [persist]
  );

  // Drag handling uses window listeners while a drag is active so the pan keeps
  // tracking even if the cursor leaves the (small) preview box.
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const drag = dragRef.current;
      if (!drag) {
        return;
      }
      // Dragging the content right should reveal content to the LEFT, i.e.
      // decrease viewportX — so the screen delta is negated before panDelta.
      const { dx, dy } = panDelta(drag.lastX - e.clientX, drag.lastY - e.clientY, viewportRef.current.viewportZoom);
      drag.lastX = e.clientX;
      drag.lastY = e.clientY;
      const current = viewportRef.current;
      commit({
        viewportX: current.viewportX + dx,
        viewportY: current.viewportY + dy,
        viewportZoom: current.viewportZoom,
      });
    };
    const onUp = () => {
      dragRef.current = null;
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, [commit]);

  const onMouseDown = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    e.preventDefault();
    dragRef.current = { lastX: e.clientX, lastY: e.clientY };
  }, []);

  const hasUrl = opts.url.trim().length > 0;

  // Wheel-zoom is attached as a NON-passive native listener so we can
  // preventDefault and stop the editor pane / page from scrolling while the
  // author zooms. React's synthetic onWheel is passive and cannot preventDefault.
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !hasUrl) {
      return;
    }
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const cursorX = e.clientX - rect.left;
      const cursorY = e.clientY - rect.top;
      const zoomFactor = e.deltaY < 0 ? WHEEL_ZOOM_STEP : 1 / WHEEL_ZOOM_STEP;
      commit(zoomAtCursor({ ...viewportRef.current, cursorX, cursorY, zoomFactor }));
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [commit, hasUrl]);

  return (
    <div className={styles.wrapper}>
      <div
        ref={containerRef}
        className={styles.preview}
        data-testid={viewportEditorTestIds.preview}
        onMouseDown={hasUrl ? onMouseDown : undefined}
        role="application"
        aria-label="Interactive viewport preview"
      >
        {hasUrl ? (
          <iframe
            title="Viewport preview"
            src={opts.url}
            data-testid={viewportEditorTestIds.iframe}
            className={styles.iframe}
            // SECURITY: keep this sandbox in lockstep with the view-mode panel.
            sandbox="allow-scripts allow-same-origin"
            referrerPolicy="no-referrer"
            style={{
              width: opts.iframeWidth,
              height: opts.iframeHeight,
              transform: buildViewportTransform(viewport),
              transformOrigin: 'top left',
            }}
          />
        ) : (
          <div className={styles.hint} data-testid={viewportEditorTestIds.hint}>
            Enter a URL to position the viewport.
          </div>
        )}
      </div>
      <div className={styles.readout} data-testid={viewportEditorTestIds.readout}>
        <span>X: {Math.round(viewport.viewportX)}</span>
        <span>Y: {Math.round(viewport.viewportY)}</span>
        <span>Zoom: {viewport.viewportZoom.toFixed(2)}×</span>
      </div>
      {hasUrl && (
        <div className={styles.help}>Drag to pan · scroll to zoom ({VIEWPORT_ZOOM_MIN}–{VIEWPORT_ZOOM_MAX}×)</div>
      )}
    </div>
  );
}

/**
 * Persists the three flat viewport option paths through the custom editor's
 * single bound `onChange`.
 *
 * A custom editor receives an `onChange` bound to ONE path — Grafana wires it as
 * `value => onChange(item.path, value)`, where that handler does
 * `onPanelOptionsChanged(setOptionImmutably(currentOptions, item.path, value))`.
 * `setOptionImmutably` deep-clones `currentOptions` first, so any sibling values
 * already present on `currentOptions` are carried into the new options object.
 *
 * We exploit that: write `viewportX`/`viewportY` directly onto the shared
 * `context.options` object (the same reference Grafana clones from), then fire
 * the bound `onChange` for `viewportZoom`. The clone therefore captures the
 * updated X/Y AND the new zoom in one immutable update — no lost-update race,
 * no multi-tick flush. The bound `onChange` is always called (even when zoom is
 * unchanged) so the change is committed and the dashboard marked dirty.
 */
function useViewportPersistence(
  _boundValue: number,
  onChange: (value?: number) => void,
  options: PanelOptions | undefined
) {
  const onChangeRef = useRef(onChange);
  const optionsRef = useRef(options);
  useEffect(() => {
    onChangeRef.current = onChange;
    optionsRef.current = options;
  });

  return useCallback((next: ViewportState) => {
    const current = optionsRef.current;
    if (current) {
      // Stage X/Y on the live options object so Grafana's immutable clone picks
      // them up alongside the zoom change below.
      current.viewportX = next.viewportX;
      current.viewportY = next.viewportY;
    }
    // Commit through the bound path (viewportZoom); this triggers the immutable
    // options update that persists all three values.
    onChangeRef.current(next.viewportZoom);
  }, []);
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(1)};
  `,
  preview: css`
    position: relative;
    width: 100%;
    height: 220px;
    overflow: hidden;
    border: 1px solid ${theme.colors.border.medium};
    border-radius: ${theme.shape.radius.default};
    background: ${theme.colors.background.canvas};
    cursor: grab;
    touch-action: none;
    user-select: none;
    &:active {
      cursor: grabbing;
    }
  `,
  iframe: css`
    border: none;
    /* The container captures pan/zoom; events must not reach the page. */
    pointer-events: none;
  `,
  hint: css`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: ${theme.spacing(2)};
    text-align: center;
    color: ${theme.colors.text.secondary};
  `,
  readout: css`
    display: flex;
    gap: ${theme.spacing(2)};
    font-family: ${theme.typography.fontFamilyMonospace};
    font-size: ${theme.typography.bodySmall.fontSize};
    color: ${theme.colors.text.primary};
  `,
  help: css`
    font-size: ${theme.typography.bodySmall.fontSize};
    color: ${theme.colors.text.secondary};
  `,
});

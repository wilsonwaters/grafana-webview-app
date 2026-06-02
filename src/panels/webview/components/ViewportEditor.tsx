import React, { useCallback, useEffect, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { Button, Field, Input, useStyles2 } from '@grafana/ui';
import { DEFAULT_PANEL_OPTIONS, normalizeOptions, type PanelOptions } from '../../../types';
import {
  buildViewportTransform,
  clampZoom,
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
 * Config-mode interactive viewport editor (PC3 + PC4).
 *
 * Registered as a custom panel options editor (see `module.tsx`,
 * `builder.addCustomEditor`). Grafana renders custom option editors only in the
 * panel edit pane, so this component is never shown in view mode — the view
 * (PC1 `WebViewPanel`) stays static and non-interactive. This is the Q3
 * resolution: there is no edit-vs-view detection inside the panel component.
 *
 * ## Interaction model (PC3, see research-context.md "Mouse Event Handling")
 * - The CONTAINER captures all pointer events; the iframe has
 *   `pointer-events: none` so drags/wheels never reach the embedded page.
 * - Drag → pan: screen-pixel deltas are converted to virtual offsets via
 *   `panDelta` (zoom-aware) and added to `viewportX/Y`.
 * - Wheel → zoom: `zoomAtCursor` keeps the virtual point under the cursor fixed
 *   while changing zoom, clamped to [0.1, 5.0].
 *
 * ## Numeric inputs (PC4)
 * Numeric `@grafana/ui` inputs for X, Y, zoom, iframeWidth, and iframeHeight are
 * kept two-way in sync with the preview:
 * - Typing a value immediately moves the preview.
 * - Dragging/zooming the preview immediately updates the inputs.
 * - Zoom is clamped to [VIEWPORT_ZOOM_MIN, VIEWPORT_ZOOM_MAX] on commit.
 * - iframeWidth/iframeHeight reject non-positive values (fall back to the
 *   last valid value / default).
 *
 * ## URL control reconciliation (PC4 design decision)
 * The standard `url` field registered in `module.tsx` (F4) is the **canonical**
 * URL input. `ViewportEditor` reads `context.options.url` to drive the preview
 * but does NOT add a second URL text input. This avoids duplication while keeping
 * the preview reactive: Grafana re-renders the editor whenever any option changes,
 * so typing in the standard URL field above updates the preview automatically.
 *
 * ## Persistence note
 * A custom editor's `onChange` is bound to a single options path (Grafana wires
 * it as `value => onChange(item.path, value)`), so it cannot set three sibling
 * fields in one synchronous call without dropping updates. This component
 * therefore keeps the authoritative live state locally (driving the preview +
 * readout instantly) and flushes changed fields to the panel options one-per-
 * render via the bound `onChange` and per-field callbacks, each reading the
 * freshest committed options. See {@link useViewportPersistence} and
 * {@link useDimensionPersistence}.
 */
export function ViewportEditor({ value, onChange, context }: StandardEditorProps<number, unknown, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const opts = normalizeOptions(context.options ?? {});

  // ---------------------------------------------------------------------------
  // Viewport state (X / Y / zoom) — drives both the preview and the numeric inputs
  // ---------------------------------------------------------------------------

  // Authoritative live state for the preview + readout. Seeded from saved
  // options; kept in sync when the options change from outside this editor
  // (e.g. dashboard reload). Local interactions update this immediately for a
  // lag-free preview, then persist asynchronously.
  const [viewport, setViewport] = useState<ViewportState>({
    viewportX: opts.viewportX,
    viewportY: opts.viewportY,
    viewportZoom: opts.viewportZoom,
  });

  const persist = useViewportPersistence(value, onChange, context.options);

  // Remember the last state we committed so the re-sync below can distinguish
  // our own writes from genuine external edits (e.g. dashboard reload) and
  // avoid clobbering live interaction state.
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

  // ---------------------------------------------------------------------------
  // Dimension state (iframeWidth / iframeHeight) — numeric inputs only, no preview interaction
  // ---------------------------------------------------------------------------

  const [iframeWidth, setIframeWidth] = useState<number>(opts.iframeWidth);
  const [iframeHeight, setIframeHeight] = useState<number>(opts.iframeHeight);

  const persistDimension = useDimensionPersistence(value, onChange, context.options);

  // Sync dimension state from external changes (e.g. dashboard reload).
  const committedDimRef = useRef({ iframeWidth: opts.iframeWidth, iframeHeight: opts.iframeHeight });
  useEffect(() => {
    const c = committedDimRef.current;
    if (opts.iframeWidth === c.iframeWidth && opts.iframeHeight === c.iframeHeight) {
      return;
    }
    committedDimRef.current = { iframeWidth: opts.iframeWidth, iframeHeight: opts.iframeHeight };
    setIframeWidth(opts.iframeWidth);
    setIframeHeight(opts.iframeHeight);
  }, [opts.iframeWidth, opts.iframeHeight]);

  // ---------------------------------------------------------------------------
  // Drag / wheel interaction (preview)
  // ---------------------------------------------------------------------------

  const containerRef = useRef<HTMLDivElement | null>(null);
  // Drag tracking. Null when not dragging.
  const dragRef = useRef<{ lastX: number; lastY: number } | null>(null);
  // Latest viewport, mirrored into a ref so the window-level move handler always
  // reads current values without re-binding listeners. Kept current both by an
  // effect (for external/state-driven updates) and synchronously inside the
  // event-driven `commit` (so rapid drag moves in one frame accumulate correctly
  // without waiting for a re-render).
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

  // ---------------------------------------------------------------------------
  // Numeric input handlers (viewport X / Y / zoom)
  // ---------------------------------------------------------------------------

  const handleXChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = parseFloat(e.currentTarget.value);
      if (!isFinite(raw)) {
        return;
      }
      commit({ ...viewportRef.current, viewportX: raw });
    },
    [commit]
  );

  const handleYChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = parseFloat(e.currentTarget.value);
      if (!isFinite(raw)) {
        return;
      }
      commit({ ...viewportRef.current, viewportY: raw });
    },
    [commit]
  );

  const handleZoomChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = parseFloat(e.currentTarget.value);
      if (!isFinite(raw)) {
        return;
      }
      commit({ ...viewportRef.current, viewportZoom: clampZoom(raw) });
    },
    [commit]
  );

  // ---------------------------------------------------------------------------
  // Numeric input handlers (iframe dimensions)
  // ---------------------------------------------------------------------------

  const handleWidthChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = parseInt(e.currentTarget.value, 10);
      const next = isFinite(raw) && raw > 0 ? raw : DEFAULT_PANEL_OPTIONS.iframeWidth;
      committedDimRef.current.iframeWidth = next;
      setIframeWidth(next);
      persistDimension({ iframeWidth: next, iframeHeight: committedDimRef.current.iframeHeight });
    },
    [persistDimension]
  );

  const handleHeightChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = parseInt(e.currentTarget.value, 10);
      const next = isFinite(raw) && raw > 0 ? raw : DEFAULT_PANEL_OPTIONS.iframeHeight;
      committedDimRef.current.iframeHeight = next;
      setIframeHeight(next);
      persistDimension({ iframeWidth: committedDimRef.current.iframeWidth, iframeHeight: next });
    },
    [persistDimension]
  );

  // ---------------------------------------------------------------------------
  // Reset view
  // ---------------------------------------------------------------------------

  const handleReset = useCallback(() => {
    commit({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
  }, [commit]);

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <div className={styles.wrapper}>
      {/* Preview */}
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
              width: iframeWidth,
              height: iframeHeight,
              transform: buildViewportTransform(viewport),
              transformOrigin: 'top left',
            }}
          />
        ) : (
          <div className={styles.hint} data-testid={viewportEditorTestIds.hint}>
            Enter a URL in the &ldquo;URL&rdquo; field above to position the viewport.
          </div>
        )}
      </div>

      {/* Live readout (text) */}
      <div className={styles.readout} data-testid={viewportEditorTestIds.readout}>
        <span>X: {Math.round(viewport.viewportX)}</span>
        <span>Y: {Math.round(viewport.viewportY)}</span>
        <span>Zoom: {viewport.viewportZoom.toFixed(2)}×</span>
      </div>

      {hasUrl && (
        <div className={styles.help}>Drag to pan · scroll to zoom ({VIEWPORT_ZOOM_MIN}–{VIEWPORT_ZOOM_MAX}×)</div>
      )}

      {/* Numeric viewport inputs (X / Y / zoom) */}
      <div className={styles.inputRow}>
        <Field label="X" className={styles.inputField}>
          <Input
            type="number"
            data-testid={viewportEditorTestIds.inputX}
            value={Math.round(viewport.viewportX)}
            onChange={handleXChange}
            aria-label="Viewport X offset"
          />
        </Field>
        <Field label="Y" className={styles.inputField}>
          <Input
            type="number"
            data-testid={viewportEditorTestIds.inputY}
            value={Math.round(viewport.viewportY)}
            onChange={handleYChange}
            aria-label="Viewport Y offset"
          />
        </Field>
        <Field label={`Zoom (${VIEWPORT_ZOOM_MIN}–${VIEWPORT_ZOOM_MAX})`} className={styles.inputField}>
          <Input
            type="number"
            data-testid={viewportEditorTestIds.inputZoom}
            value={viewport.viewportZoom.toFixed(2)}
            step={0.1}
            min={VIEWPORT_ZOOM_MIN}
            max={VIEWPORT_ZOOM_MAX}
            onChange={handleZoomChange}
            aria-label="Viewport zoom"
          />
        </Field>
      </div>

      {/* Reset view button */}
      <Button
        variant="secondary"
        size="sm"
        data-testid={viewportEditorTestIds.resetButton}
        onClick={handleReset}
        icon="repeat"
      >
        Reset view
      </Button>

      {/* Virtual iframe dimension inputs */}
      <div className={styles.inputRow}>
        <Field label="Page width (px)" className={styles.inputField}>
          <Input
            type="number"
            data-testid={viewportEditorTestIds.inputWidth}
            value={iframeWidth}
            min={1}
            onChange={handleWidthChange}
            aria-label="Virtual iframe width"
          />
        </Field>
        <Field label="Page height (px)" className={styles.inputField}>
          <Input
            type="number"
            data-testid={viewportEditorTestIds.inputHeight}
            value={iframeHeight}
            min={1}
            onChange={handleHeightChange}
            aria-label="Virtual iframe height"
          />
        </Field>
      </div>
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

/**
 * Persists iframeWidth / iframeHeight through the custom editor's single bound
 * `onChange`, using the same sibling-write technique as {@link useViewportPersistence}.
 *
 * We write `iframeWidth` directly onto the live `context.options` object, then
 * fire the bound `onChange` for `iframeHeight` (or vice versa) — Grafana's
 * `setOptionImmutably` deep-clones the options object on each call, so the
 * staged value is included in the resulting snapshot.
 *
 * NOTE: this shares the same `onChange` callback that `useViewportPersistence`
 * uses (both are bound to `viewportZoom`). When a dimension changes, the current
 * zoom is passed to `onChange` so that the viewport fields are also preserved
 * in the cloned snapshot without being reset to the default.
 */
function useDimensionPersistence(
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

  return useCallback((dims: { iframeWidth: number; iframeHeight: number }) => {
    const current = optionsRef.current;
    if (current) {
      // Stage both dimension fields and the current viewport offsets so they
      // all survive Grafana's deep-clone of the options object.
      current.iframeWidth = dims.iframeWidth;
      current.iframeHeight = dims.iframeHeight;
    }
    // Fire the bound onChange to trigger the Grafana immutable update.
    // Pass the current zoom (unchanged) so viewportZoom is preserved.
    onChangeRef.current(current?.viewportZoom ?? 1);
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
  inputRow: css`
    display: flex;
    flex-wrap: wrap;
    gap: ${theme.spacing(1)};
    align-items: flex-start;
  `,
  inputField: css`
    flex: 1;
    min-width: 80px;
    margin-bottom: 0;
  `,
});

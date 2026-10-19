import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { Button, Field, Input, useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import {
  buildViewportTransform,
  clampZoom,
  panDelta,
  VIEWPORT_ZOOM_MAX,
  VIEWPORT_ZOOM_MIN,
  zoomAtCursor,
} from '../viewport';
import { resolveLoadMode } from '../loadMode';
import { validateWebViewUrl } from '../url';
import { viewportEditorTestIds } from './viewportEditorTestIds';
import { WebViewFrame, type WebViewFrameTestIds } from './WebViewFrame';

/**
 * The live, interactive viewport state the editor manipulates. A subset of the
 * full panel options — only the three fields this editor positions.
 */
interface ViewportState {
  viewportX: number;
  viewportY: number;
  viewportZoom: number;
}

/** The editor preview's test ids for the elements that the shared frame renders. */
const frameTestIds: WebViewFrameTestIds = {
  iframe: viewportEditorTestIds.iframe,
  invalidUrl: viewportEditorTestIds.invalidUrl,
  backendLoading: viewportEditorTestIds.backendLoading,
  proxyUnavailable: viewportEditorTestIds.proxyUnavailable,
  proxyLoading: viewportEditorTestIds.proxyLoading,
  proxyError: viewportEditorTestIds.proxyError,
};

/**
 * Multiplicative wheel-zoom step. A single wheel "notch" zooms in/out by 10 %.
 * Cursor-anchored via {@link zoomAtCursor}.
 */
const WHEEL_ZOOM_STEP = 1.1;

/** Parses a whole number of pixels for the X / Y offsets. Negative values are valid. */
function parseOffset(text: string): number | undefined {
  const n = Number(text);
  return text.trim() !== '' && Number.isFinite(n) ? Math.round(n) : undefined;
}

/** Parses a zoom factor and clamps it to the allowed range. */
function parseZoom(text: string): number | undefined {
  const n = Number(text);
  return text.trim() !== '' && Number.isFinite(n) ? clampZoom(n) : undefined;
}

/** Parses a page dimension: a whole number of pixels, 1 or more. */
function parseDimension(text: string): number | undefined {
  const n = Math.round(Number(text));
  return text.trim() !== '' && Number.isFinite(n) && n >= 1 ? n : undefined;
}

const formatWhole = (value: number) => String(Math.round(value));
const formatZoom = (value: number) => value.toFixed(2);

/**
 * Config-mode interactive viewport editor (PC3 + PC4).
 *
 * Registered as a custom panel options editor (see `module.tsx`,
 * `builder.addCustomEditor`). Grafana renders custom option editors only in the
 * panel edit pane, so this component is never shown in view mode — the view
 * (PC1 `WebViewPanel`) stays static and non-interactive. This is the Q3
 * resolution: there is no edit-vs-view detection inside the panel component.
 *
 * ## Interaction model (PC3)
 * - The CONTAINER handles the pointer; the iframe has `pointer-events: none`
 *   so drags and wheels never reach the embedded page.
 * - Drag → pan (mouse, pen or touch): the container captures the pointer on
 *   `pointerdown`, so the drag keeps tracking outside the small preview and
 *   ends on `pointerup`, `pointercancel` or lost capture, also when the button
 *   is released outside the window. Screen-pixel deltas are converted to
 *   virtual offsets via `panDelta` (zoom-aware) and added to `viewportX/Y`.
 * - Wheel → zoom: `zoomAtCursor` keeps the virtual point under the cursor fixed
 *   while changing zoom, clamped with `clampZoom`. The wheel zooms only after
 *   the pointer has moved over the preview. When the options pane scrolls the
 *   preview under a resting pointer, the wheel keeps scrolling the pane.
 * - Keyboard: the X, Y and Zoom fields are the keyboard alternative to dragging
 *   and the wheel. The preview itself takes no focus.
 *
 * ## Numeric inputs (PC4)
 * The X, Y, zoom, page width and page height fields keep the text the author
 * types and commit it on blur or Enter (Escape restores the current value).
 * The value is parsed and limited only on commit, so a value such as `0.5` or
 * `-20` can be typed one character at a time:
 * - Zoom is clamped to [VIEWPORT_ZOOM_MIN, VIEWPORT_ZOOM_MAX].
 * - X and Y are whole pixels and may be negative.
 * - Width and height are whole pixels, 1 or more. Any other value (including an
 *   empty field) restores the last valid value.
 * - Dragging or zooming the preview, or Reset view, updates the fields at once
 *   and replaces any uncommitted text.
 *
 * ## URL control reconciliation (PC4 design decision)
 * The standard `url` field registered in `module.tsx` (F4) is the **canonical**
 * URL input. `ViewportEditor` reads `context.options.url` to drive the preview
 * but does NOT add a second URL text input. This avoids duplication while keeping
 * the preview reactive: Grafana re-renders the editor whenever any option changes,
 * so typing in the standard URL field above updates the preview automatically.
 *
 * ## Preview frame (VR-1, VR-2, FR5)
 * The preview uses the shared `WebViewFrame`, the same renderer as the
 * view-mode panel, with the resolved load mode. An unsafe URL shows an error
 * message in the preview (editor-time feedback) and never an iframe. Proxy mode
 * fetches the rewritten HTML and renders it with `srcdoc`, as in the panel. The
 * preview never auto-refreshes (`reloadKey` stays 0).
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
  const helpId = useId();

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
  // Pointer / wheel interaction (preview)
  // ---------------------------------------------------------------------------

  const containerRef = useRef<HTMLDivElement | null>(null);
  // The active drag. Null when not dragging.
  const dragRef = useRef<{ pointerId: number; lastX: number; lastY: number } | null>(null);
  // True once the pointer has moved over the preview. The wheel zooms only
  // then (see the wheel handler below).
  const wheelArmedRef = useRef(false);
  // Latest viewport, mirrored into a ref so the event handlers always read
  // current values. Kept current both by an effect (for external/state-driven
  // updates) and synchronously inside the event-driven `commit` (so rapid drag
  // moves in one frame accumulate correctly without waiting for a re-render).
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

  const hasUrl = opts.url.length > 0;
  // VR-1: only a validated http(s) URL is previewed (and can be positioned).
  // WebViewFrame validates again before it builds an iframe.
  const canPreview = validateWebViewUrl(opts.url).valid;

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!canPreview || e.button !== 0) {
        return;
      }
      e.preventDefault();
      // Capture the pointer: moves and the release are delivered to the preview
      // even when the pointer leaves it or the window.
      const el = e.currentTarget;
      if (typeof el.setPointerCapture === 'function') {
        el.setPointerCapture(e.pointerId);
      }
      dragRef.current = { pointerId: e.pointerId, lastX: e.clientX, lastY: e.clientY };
    },
    [canPreview]
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      wheelArmedRef.current = true;
      const drag = dragRef.current;
      if (!drag || drag.pointerId !== e.pointerId) {
        return;
      }
      if (e.buttons === 0) {
        // The button was released where no pointerup reached the preview.
        dragRef.current = null;
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
    },
    [commit]
  );

  const endDrag = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === e.pointerId) {
      dragRef.current = null;
    }
  }, []);

  const onPointerLeave = useCallback(() => {
    wheelArmedRef.current = false;
  }, []);

  // When anything on the page scrolls (for example the options pane), the
  // preview can move under a resting pointer. Disarm the wheel until the
  // pointer moves again, so the wheel keeps scrolling the pane.
  useEffect(() => {
    const disarm = () => {
      wheelArmedRef.current = false;
    };
    document.addEventListener('scroll', disarm, { capture: true, passive: true });
    return () => document.removeEventListener('scroll', disarm, { capture: true });
  }, []);

  // Wheel-zoom is attached as a NON-passive native listener so we can
  // preventDefault and stop the editor pane / page from scrolling while the
  // author zooms. React's synthetic onWheel is passive and cannot preventDefault.
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !canPreview) {
      return;
    }
    const onWheel = (e: WheelEvent) => {
      if (!wheelArmedRef.current) {
        // The pointer has not moved over the preview: let the pane scroll.
        return;
      }
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const cursorX = e.clientX - rect.left;
      const cursorY = e.clientY - rect.top;
      const zoomFactor = e.deltaY < 0 ? WHEEL_ZOOM_STEP : 1 / WHEEL_ZOOM_STEP;
      commit(zoomAtCursor({ ...viewportRef.current, cursorX, cursorY, zoomFactor }));
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [commit, canPreview]);

  // ---------------------------------------------------------------------------
  // Numeric input commits (viewport X / Y / zoom, iframe dimensions)
  // ---------------------------------------------------------------------------

  const commitX = useCallback((x: number) => commit({ ...viewportRef.current, viewportX: x }), [commit]);
  const commitY = useCallback((y: number) => commit({ ...viewportRef.current, viewportY: y }), [commit]);
  const commitZoom = useCallback((zoom: number) => commit({ ...viewportRef.current, viewportZoom: zoom }), [commit]);

  const commitWidth = useCallback(
    (width: number) => {
      committedDimRef.current.iframeWidth = width;
      setIframeWidth(width);
      persistDimension({ iframeWidth: width, iframeHeight: committedDimRef.current.iframeHeight });
    },
    [persistDimension]
  );

  const commitHeight = useCallback(
    (height: number) => {
      committedDimRef.current.iframeHeight = height;
      setIframeHeight(height);
      persistDimension({ iframeWidth: committedDimRef.current.iframeWidth, iframeHeight: height });
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
      {/* Preview. Pointer only; the fields below are the keyboard alternative. */}
      <div
        ref={containerRef}
        className={styles.preview}
        data-testid={viewportEditorTestIds.preview}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onLostPointerCapture={endDrag}
        onPointerLeave={onPointerLeave}
        role="group"
        aria-label="Viewport preview"
        aria-describedby={canPreview ? helpId : undefined}
      >
        {hasUrl ? (
          <WebViewFrame
            url={opts.url}
            mode={resolveLoadMode(opts)}
            hideSelectors={opts.hideSelectors}
            reloadKey={0}
            title="Viewport preview"
            testIds={frameTestIds}
            frameStyle={{
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

      {canPreview && (
        <div className={styles.help} id={helpId}>
          Drag to pan · scroll to zoom ({VIEWPORT_ZOOM_MIN}–{VIEWPORT_ZOOM_MAX}×). With the keyboard, use the X, Y and
          Zoom fields.
        </div>
      )}

      {/* Numeric viewport inputs (X / Y / zoom) */}
      <div className={styles.inputRow}>
        <NumericField
          label="X"
          ariaLabel="Viewport X offset"
          testId={viewportEditorTestIds.inputX}
          value={viewport.viewportX}
          format={formatWhole}
          parse={parseOffset}
          onCommit={commitX}
          className={styles.inputField}
        />
        <NumericField
          label="Y"
          ariaLabel="Viewport Y offset"
          testId={viewportEditorTestIds.inputY}
          value={viewport.viewportY}
          format={formatWhole}
          parse={parseOffset}
          onCommit={commitY}
          className={styles.inputField}
        />
        <NumericField
          label={`Zoom (${VIEWPORT_ZOOM_MIN}–${VIEWPORT_ZOOM_MAX})`}
          ariaLabel="Viewport zoom"
          testId={viewportEditorTestIds.inputZoom}
          value={viewport.viewportZoom}
          format={formatZoom}
          parse={parseZoom}
          onCommit={commitZoom}
          step={0.1}
          min={VIEWPORT_ZOOM_MIN}
          max={VIEWPORT_ZOOM_MAX}
          className={styles.inputField}
        />
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
        <NumericField
          label="Page width (px)"
          ariaLabel="Virtual iframe width"
          testId={viewportEditorTestIds.inputWidth}
          value={iframeWidth}
          format={formatWhole}
          parse={parseDimension}
          onCommit={commitWidth}
          min={1}
          className={styles.inputField}
        />
        <NumericField
          label="Page height (px)"
          ariaLabel="Virtual iframe height"
          testId={viewportEditorTestIds.inputHeight}
          value={iframeHeight}
          format={formatWhole}
          parse={parseDimension}
          onCommit={commitHeight}
          min={1}
          className={styles.inputField}
        />
      </div>
    </div>
  );
}

interface NumericFieldProps {
  /** Visible label. */
  label: string;
  /** Accessible name of the input. */
  ariaLabel: string;
  testId: string;
  /** The committed value. */
  value: number;
  format: (value: number) => string;
  /** Returns the value to commit, or undefined when the text is not valid. */
  parse: (text: string) => number | undefined;
  onCommit: (value: number) => void;
  step?: number;
  min?: number;
  max?: number;
  className?: string;
}

/**
 * A labelled number input that keeps the typed text and commits it on blur or
 * Enter (RV1-11, RV1-29). Escape, and any text that does not parse, restore the
 * committed value. When the committed value changes while the author is typing
 * (a drag, a zoom or Reset view), the new value replaces the typed text.
 */
function NumericField(props: NumericFieldProps) {
  const id = useId();
  const { value, format, parse, onCommit } = props;
  // `base` is the committed value when typing started.
  const [draft, setDraft] = useState<{ text: string; base: number } | null>(null);
  const editing = draft !== null && draft.base === value;

  const commitDraft = () => {
    if (editing) {
      const next = parse(draft.text);
      if (next !== undefined && next !== value) {
        onCommit(next);
      }
    }
    setDraft(null);
  };

  return (
    <Field label={props.label} htmlFor={id} className={props.className}>
      <Input
        id={id}
        type="number"
        data-testid={props.testId}
        aria-label={props.ariaLabel}
        value={editing ? draft.text : format(value)}
        step={props.step}
        min={props.min}
        max={props.max}
        onChange={(e) => setDraft({ text: e.currentTarget.value, base: value })}
        onBlur={commitDraft}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            commitDraft();
          } else if (e.key === 'Escape') {
            setDraft(null);
          }
        }}
      />
    </Field>
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

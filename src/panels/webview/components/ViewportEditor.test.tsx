import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { ViewportEditor } from './ViewportEditor';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { viewportEditorTestIds } from './viewportEditorTestIds';

type Props = StandardEditorProps<number, unknown, PanelOptions>;

function buildProps(optionOverrides: Partial<PanelOptions> = {}): {
  props: Props;
  options: PanelOptions;
  onChange: jest.Mock;
} {
  const options: PanelOptions = { ...DEFAULT_PANEL_OPTIONS, url: 'https://example.com', ...optionOverrides };
  // Faithfully model Grafana: a custom editor's bound onChange persists the
  // value to its path (viewportZoom) on the shared options object.
  const onChange = jest.fn((zoom: number) => {
    options.viewportZoom = zoom;
  });
  const props = {
    value: options.viewportZoom,
    onChange,
    context: { data: [], options },
    item: {} as Props['item'],
  } as unknown as Props;
  return { props, options, onChange };
}

// Anchor the container at the origin so cursor maths are deterministic.
beforeAll(() => {
  Element.prototype.getBoundingClientRect = jest.fn(
    () => ({ left: 0, top: 0, right: 300, bottom: 220, width: 300, height: 220, x: 0, y: 0, toJSON() {} } as DOMRect)
  );
});

describe('panels/webview/ViewportEditor', () => {
  // ---------------------------------------------------------------------------
  // PC3 tests (preserved)
  // ---------------------------------------------------------------------------

  test('shows the empty-URL hint and no iframe when no URL is configured', () => {
    const { props } = buildProps({ url: '' });
    render(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.hint)).toBeInTheDocument();
    expect(screen.queryByTestId(viewportEditorTestIds.iframe)).not.toBeInTheDocument();
  });

  test('renders the iframe at the configured URL with the saved viewport transform', () => {
    const { props } = buildProps({ viewportX: 100, viewportY: 200, viewportZoom: 1.5 });
    render(<ViewportEditor {...props} />);

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveAttribute('src', 'https://example.com');
    expect(iframe).toHaveStyle({ transform: 'scale(1.5) translate(-100px, -200px)' });
    expect(iframe).toHaveStyle({ pointerEvents: 'none' });
  });

  test('readout reflects the current X / Y / zoom values', () => {
    const { props } = buildProps({ viewportX: 42, viewportY: 7, viewportZoom: 2 });
    render(<ViewportEditor {...props} />);

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: 42');
    expect(readout).toHaveTextContent('Y: 7');
    expect(readout).toHaveTextContent('Zoom: 2.00×');
  });

  test('drag pans: mousedown→move→up updates X/Y in the readout and persists offsets', () => {
    const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    // Drag the content right+down by (30, 20) screen px. With the "drag right
    // reveals content to the left" convention this DECREASES X/Y at zoom 1.
    fireEvent.mouseDown(preview, { clientX: 200, clientY: 150 });
    fireEvent.mouseMove(window, { clientX: 230, clientY: 170 });
    fireEvent.mouseUp(window, { clientX: 230, clientY: 170 });

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: -30');
    expect(readout).toHaveTextContent('Y: -20');
    // X/Y are persisted onto the shared options object (custom editors can only
    // bind one path; siblings are written through the live options reference).
    expect(options.viewportX).toBe(-30);
    expect(options.viewportY).toBe(-20);
  });

  test('drag accounts for zoom: at zoom 2 a 100px screen drag = 50 virtual px', () => {
    const { props } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 2 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    fireEvent.mouseDown(preview, { clientX: 0, clientY: 0 });
    // Drag left by 100px (clientX 0 -> -100): reveals content to the right,
    // increasing X by 100/2 = 50.
    fireEvent.mouseMove(window, { clientX: -100, clientY: 0 });

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: 50');
  });

  test('wheel zooms in cursor-anchored and calls onChange with the new clamped zoom', () => {
    const { props, onChange } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    // Wheel up (deltaY < 0) at cursor (200, 100) zooms in by 1.1.
    fireEvent.wheel(preview, { deltaY: -100, clientX: 200, clientY: 100 });

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('Zoom: 1.10×');
    // Cursor-anchored: the virtual point under the cursor stays put, so the
    // offsets shift positive. X = 200 - 200/1.1 ≈ 18.
    expect(readout).toHaveTextContent('X: 18');
    expect(readout).toHaveTextContent('Y: 9');
    // Zoom is the bound path: committed via onChange.
    expect(onChange).toHaveBeenCalledWith(1.1);
  });

  test('wheel zoom clamps to the maximum of 5.0', () => {
    const { props, onChange } = buildProps({ viewportZoom: 4.9 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    // 4.9 * 1.1 = 5.39 -> clamped to 5.0.
    fireEvent.wheel(preview, { deltaY: -100, clientX: 0, clientY: 0 });

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 5.00×');
    expect(onChange).toHaveBeenCalledWith(5);
  });

  // ---------------------------------------------------------------------------
  // PC4 tests: numeric inputs two-way sync
  // ---------------------------------------------------------------------------

  test('numeric X input is rendered and reflects the current viewportX', () => {
    const { props } = buildProps({ viewportX: 123, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputX = screen.getByTestId(viewportEditorTestIds.inputX);
    expect(inputX).toBeInTheDocument();
    expect(inputX).toHaveValue(123);
  });

  test('numeric Y input is rendered and reflects the current viewportY', () => {
    const { props } = buildProps({ viewportX: 0, viewportY: 456, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputY = screen.getByTestId(viewportEditorTestIds.inputY);
    expect(inputY).toBeInTheDocument();
    expect(inputY).toHaveValue(456);
  });

  test('numeric zoom input is rendered and reflects the current viewportZoom', () => {
    const { props } = buildProps({ viewportZoom: 2.5 });
    render(<ViewportEditor {...props} />);

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    expect(inputZoom).toBeInTheDocument();
    // Value is rendered as fixed(2) string; toHaveValue parses it as a number
    expect(inputZoom).toHaveValue(2.5);
  });

  test('typing in X input updates the preview transform and persists the value', () => {
    const { props, options, onChange } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputX = screen.getByTestId(viewportEditorTestIds.inputX);
    fireEvent.change(inputX, { target: { value: '300' } });

    // Preview iframe transform should include the new X value
    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(1) translate(-300px, 0px)' });
    // Readout should update
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: 300');
    // Persisted via shared options object (X staged) and onChange fired
    expect(options.viewportX).toBe(300);
    expect(onChange).toHaveBeenCalled();
  });

  test('typing in Y input updates the preview transform and persists the value', () => {
    const { props, options, onChange } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputY = screen.getByTestId(viewportEditorTestIds.inputY);
    fireEvent.change(inputY, { target: { value: '200' } });

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(1) translate(0px, -200px)' });
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Y: 200');
    expect(options.viewportY).toBe(200);
    expect(onChange).toHaveBeenCalled();
  });

  test('typing in zoom input updates the preview transform and persists the value', () => {
    const { props, options, onChange } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    fireEvent.change(inputZoom, { target: { value: '2' } });

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(2) translate(0px, 0px)' });
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 2.00×');
    expect(options.viewportZoom).toBe(2);
    expect(onChange).toHaveBeenCalledWith(2);
  });

  test('zoom input clamps value exceeding 5.0 to 5.0', () => {
    const { props, options, onChange } = buildProps({ viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    fireEvent.change(inputZoom, { target: { value: '99' } });

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 5.00×');
    expect(options.viewportZoom).toBe(5);
    expect(onChange).toHaveBeenCalledWith(5);
  });

  test('zoom input clamps value below 0.1 to 0.1', () => {
    const { props, options } = buildProps({ viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    fireEvent.change(inputZoom, { target: { value: '0.001' } });

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 0.10×');
    expect(options.viewportZoom).toBe(0.1);
  });

  test('drag interaction updates the numeric X/Y inputs', () => {
    const { props } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    fireEvent.mouseDown(preview, { clientX: 200, clientY: 150 });
    fireEvent.mouseMove(window, { clientX: 230, clientY: 170 });
    fireEvent.mouseUp(window);

    // After drag right+down by (30,20), X/Y decrease by that amount.
    const inputX = screen.getByTestId(viewportEditorTestIds.inputX);
    const inputY = screen.getByTestId(viewportEditorTestIds.inputY);
    expect(inputX).toHaveValue(-30);
    expect(inputY).toHaveValue(-20);
  });

  test('wheel zoom interaction updates the numeric zoom input', () => {
    const { props } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    fireEvent.wheel(preview, { deltaY: -100, clientX: 0, clientY: 0 });

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    // Zoom in by 1.1: 1 * 1.1 = 1.1
    expect(inputZoom).toHaveValue(1.1);
  });

  // ---------------------------------------------------------------------------
  // PC4 tests: reset button
  // ---------------------------------------------------------------------------

  test('reset button is rendered', () => {
    const { props } = buildProps();
    render(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.resetButton)).toBeInTheDocument();
  });

  test('reset button sets viewport to X0/Y0/zoom1 and updates preview + inputs', () => {
    const { props, options, onChange } = buildProps({ viewportX: 500, viewportY: 300, viewportZoom: 3 });
    render(<ViewportEditor {...props} />);

    const resetBtn = screen.getByTestId(viewportEditorTestIds.resetButton);
    fireEvent.click(resetBtn);

    // Readout reset
    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: 0');
    expect(readout).toHaveTextContent('Y: 0');
    expect(readout).toHaveTextContent('Zoom: 1.00×');

    // Numeric inputs reset
    expect(screen.getByTestId(viewportEditorTestIds.inputX)).toHaveValue(0);
    expect(screen.getByTestId(viewportEditorTestIds.inputY)).toHaveValue(0);
    expect(screen.getByTestId(viewportEditorTestIds.inputZoom)).toHaveValue(1);

    // Preview transform reset
    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(1) translate(0px, 0px)' });

    // Persisted
    expect(options.viewportX).toBe(0);
    expect(options.viewportY).toBe(0);
    expect(onChange).toHaveBeenCalledWith(1);
  });

  // ---------------------------------------------------------------------------
  // PC4 tests: iframe dimension inputs
  // ---------------------------------------------------------------------------

  test('dimension inputs are rendered with default values 1920 and 1080', () => {
    const { props } = buildProps();
    render(<ViewportEditor {...props} />);

    const inputWidth = screen.getByTestId(viewportEditorTestIds.inputWidth);
    const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);
    expect(inputWidth).toBeInTheDocument();
    expect(inputHeight).toBeInTheDocument();
    expect(inputWidth).toHaveValue(1920);
    expect(inputHeight).toHaveValue(1080);
  });

  test('changing width input updates the iframe preview width and persists', () => {
    const { props, options } = buildProps();
    render(<ViewportEditor {...props} />);

    const inputWidth = screen.getByTestId(viewportEditorTestIds.inputWidth);
    fireEvent.change(inputWidth, { target: { value: '1280' } });

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ width: '1280px' });
    expect(options.iframeWidth).toBe(1280);
  });

  test('changing height input updates the iframe preview height and persists', () => {
    const { props, options } = buildProps();
    render(<ViewportEditor {...props} />);

    const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);
    fireEvent.change(inputHeight, { target: { value: '720' } });

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ height: '720px' });
    expect(options.iframeHeight).toBe(720);
  });

  test('non-positive width value is rejected and falls back to default 1920', () => {
    const { props, options } = buildProps({ iframeWidth: 1280 });
    render(<ViewportEditor {...props} />);

    const inputWidth = screen.getByTestId(viewportEditorTestIds.inputWidth);
    fireEvent.change(inputWidth, { target: { value: '0' } });

    expect(options.iframeWidth).toBe(1920);
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ width: '1920px' });
  });

  test('negative height value is rejected and falls back to default 1080', () => {
    const { props, options } = buildProps({ iframeHeight: 720 });
    render(<ViewportEditor {...props} />);

    const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);
    fireEvent.change(inputHeight, { target: { value: '-100' } });

    expect(options.iframeHeight).toBe(1080);
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ height: '1080px' });
  });

  // ---------------------------------------------------------------------------
  // PC4 tests: URL control reconciliation
  // ---------------------------------------------------------------------------

  test('no duplicate URL input exists inside ViewportEditor (URL managed by standard field)', () => {
    const { props } = buildProps({ url: 'https://example.com' });
    render(<ViewportEditor {...props} />);

    // There must be no URL input inside the ViewportEditor — the canonical URL
    // control is the standard `url` field registered in module.tsx (F4).
    expect(screen.queryByTestId(viewportEditorTestIds.inputUrl)).not.toBeInTheDocument();
  });

  test('preview reacts to URL change via re-render with updated context.options', () => {
    // Simulate Grafana re-rendering the editor after the standard URL field changes.
    const { props, options } = buildProps({ url: '' });
    const { rerender } = render(<ViewportEditor {...props} />);

    // Initially shows hint (no URL)
    expect(screen.getByTestId(viewportEditorTestIds.hint)).toBeInTheDocument();

    // Simulate URL being set via the standard field (Grafana updates context.options)
    options.url = 'https://example.com';
    rerender(<ViewportEditor {...props} />);

    // Preview iframe should now be visible
    expect(screen.queryByTestId(viewportEditorTestIds.hint)).not.toBeInTheDocument();
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toBeInTheDocument();
  });

  // ---------------------------------------------------------------------------
  // TC3 gap-fill: non-finite numeric input is rejected (no-op), leaving the
  // committed value untouched. Covers the `!isFinite(raw)` early-returns in the
  // X / Y / zoom change handlers (e.g. when the field is cleared to empty).
  // ---------------------------------------------------------------------------

  test('clearing the X input (empty → NaN) is a no-op and does not corrupt the value', () => {
    const { props, options, onChange } = buildProps({ viewportX: 120, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    onChange.mockClear();
    const inputX = screen.getByTestId(viewportEditorTestIds.inputX);
    // An empty string parses to NaN — the handler must bail out, not write NaN.
    fireEvent.change(inputX, { target: { value: '' } });

    // Readout still shows the previous valid value; nothing was persisted.
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: 120');
    expect(options.viewportX).toBe(120);
    expect(onChange).not.toHaveBeenCalled();
    // The preview transform is unchanged.
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({
      transform: 'scale(1) translate(-120px, 0px)',
    });
  });

  test('non-finite Y input is a no-op', () => {
    const { props, options, onChange } = buildProps({ viewportX: 0, viewportY: 77, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    onChange.mockClear();
    fireEvent.change(screen.getByTestId(viewportEditorTestIds.inputY), { target: { value: 'abc' } });

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Y: 77');
    expect(options.viewportY).toBe(77);
    expect(onChange).not.toHaveBeenCalled();
  });

  test('non-finite zoom input is a no-op (does not clamp NaN to a bound)', () => {
    const { props, options, onChange } = buildProps({ viewportZoom: 2 });
    render(<ViewportEditor {...props} />);

    onChange.mockClear();
    fireEvent.change(screen.getByTestId(viewportEditorTestIds.inputZoom), { target: { value: '' } });

    // Must remain at the prior valid zoom (not clamped to MIN via NaN).
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 2.00×');
    expect(options.viewportZoom).toBe(2);
    expect(onChange).not.toHaveBeenCalled();
  });

  // ---------------------------------------------------------------------------
  // TC3 gap-fill: EXTERNAL option changes (e.g. dashboard reload / undo) re-sync
  // local state. Covers the viewport and dimension re-sync effects which only
  // run when incoming options differ from what this editor last committed.
  // ---------------------------------------------------------------------------

  test('re-syncs viewport state when context.options change externally', () => {
    const { props, options } = buildProps({ viewportX: 10, viewportY: 20, viewportZoom: 1 });
    const { rerender } = render(<ViewportEditor {...props} />);

    // Simulate an external change (not originating from this editor), e.g. a
    // dashboard reload or undo — mutate options then re-render.
    options.viewportX = 333;
    options.viewportY = 444;
    options.viewportZoom = 2.5;
    rerender(<ViewportEditor {...props} />);

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: 333');
    expect(readout).toHaveTextContent('Y: 444');
    expect(readout).toHaveTextContent('Zoom: 2.50×');
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({
      transform: 'scale(2.5) translate(-333px, -444px)',
    });
    expect(screen.getByTestId(viewportEditorTestIds.inputX)).toHaveValue(333);
  });

  test('re-syncs iframe dimensions when context.options change externally', () => {
    const { props, options } = buildProps({ iframeWidth: 1920, iframeHeight: 1080 });
    const { rerender } = render(<ViewportEditor {...props} />);

    options.iframeWidth = 1024;
    options.iframeHeight = 768;
    rerender(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.inputWidth)).toHaveValue(1024);
    expect(screen.getByTestId(viewportEditorTestIds.inputHeight)).toHaveValue(768);
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({
      width: '1024px',
      height: '768px',
    });
  });

  test('a window mousemove with no active drag is ignored (no pan)', () => {
    const { props, options } = buildProps({ viewportX: 5, viewportY: 6, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    // No mousedown first: the window move handler must bail (drag ref is null)
    // and leave the viewport untouched.
    fireEvent.mouseMove(window, { clientX: 500, clientY: 500 });

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: 5');
    expect(readout).toHaveTextContent('Y: 6');
    expect(options.viewportX).toBe(5);
    expect(options.viewportY).toBe(6);
  });

  test('a re-render with UNCHANGED options does not clobber live interaction state', () => {
    const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    const { rerender } = render(<ViewportEditor {...props} />);

    // Drive a local interaction (drag) that this editor commits.
    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    fireEvent.mouseDown(preview, { clientX: 200, clientY: 150 });
    fireEvent.mouseMove(window, { clientX: 230, clientY: 170 });
    fireEvent.mouseUp(window);
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: -30');

    // Grafana re-renders with options reflecting our own committed write — the
    // re-sync effect must recognise this as our write and NOT reset live state.
    rerender(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: -30');
    expect(options.viewportX).toBe(-30);
  });
});

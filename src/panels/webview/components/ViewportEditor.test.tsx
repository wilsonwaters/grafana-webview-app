import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { ViewportEditor } from './ViewportEditor';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { viewportEditorTestIds } from './viewportEditorTestIds';
import { useBackendAvailable } from '../useBackendAvailable';
import {
  installProxyFetchMock,
  respondHtml,
  respondHttpError,
  type ProxyFetchMock,
} from '../testing/proxyFetchMock';

// FR5: in proxy mode the preview fetches the rewritten HTML (getBackendSrv) and
// waits for the backend probe (DF3), exactly like the view-mode panel.
jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));
jest.mock('../useBackendAvailable', () => ({
  useBackendAvailable: jest.fn(),
}));

const mockedUseBackendAvailable = useBackendAvailable as jest.MockedFunction<typeof useBackendAvailable>;
let proxyFetch: ProxyFetchMock;

beforeEach(() => {
  mockedUseBackendAvailable.mockReturnValue({ loading: false, backendAvailable: true });
  proxyFetch = installProxyFetchMock();
});

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

// jsdom has no PointerEvent and no pointer capture. A MouseEvent subclass with a
// pointerId is enough for React's pointer handlers.
beforeAll(() => {
  if (!('PointerEvent' in window)) {
    class PointerEventPolyfill extends MouseEvent {
      pointerId: number;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.pointerId = init.pointerId ?? 1;
      }
    }
    Object.defineProperty(window, 'PointerEvent', { value: PointerEventPolyfill, configurable: true });
  }
  Element.prototype.setPointerCapture = jest.fn();
  Element.prototype.releasePointerCapture = jest.fn();
  Element.prototype.hasPointerCapture = jest.fn(() => false);
});

/** Primary-button press on the preview. */
function pointerDown(el: Element, clientX: number, clientY: number, pointerId = 1) {
  fireEvent.pointerDown(el, { pointerId, button: 0, buttons: 1, clientX, clientY });
}

/** Pointer move with the button held (a drag step), delivered to the capturing preview. */
function pointerMove(el: Element, clientX: number, clientY: number, buttons = 1, pointerId = 1) {
  fireEvent.pointerMove(el, { pointerId, buttons, clientX, clientY });
}

/** Button release. */
function pointerUp(el: Element, pointerId = 1) {
  fireEvent.pointerUp(el, { pointerId, button: 0, buttons: 0 });
}

/**
 * Moves the pointer over the preview (which arms wheel zoom), then turns the
 * wheel. Returns false when the wheel event's default action was prevented.
 */
function wheelOver(el: Element, init: { deltaY: number; clientX: number; clientY: number }): boolean {
  fireEvent.pointerMove(el, { pointerId: 1, buttons: 0, clientX: init.clientX, clientY: init.clientY });
  return fireEvent.wheel(el, init);
}

/** Types `value` into a numeric field and commits it the way a user does: by leaving the field. */
function typeAndCommit(input: HTMLElement, value: string) {
  fireEvent.change(input, { target: { value } });
  fireEvent.blur(input);
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

  // ---------------------------------------------------------------------------
  // S1: URL safety (VR-1) and sandbox (VR-2) in the editor preview
  // ---------------------------------------------------------------------------

  test.each([
    'javascript:alert(document.cookie)',
    'JAVASCRIPT:alert(1)',
    ' javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'file:///etc/passwd',
    '/relative/path',
  ])('an unsafe URL %j shows an accessible error and NO iframe in the preview', (url) => {
    const { props } = buildProps({ url });
    const { container } = render(<ViewportEditor {...props} />);

    const error = screen.getByTestId(viewportEditorTestIds.invalidUrl);
    expect(error).toHaveAttribute('role', 'alert');
    expect(error).toHaveTextContent(/cannot be displayed/i);
    expect(container.querySelector('iframe')).toBeNull();
    // The empty-URL hint is for an empty URL only.
    expect(screen.queryByTestId(viewportEditorTestIds.hint)).not.toBeInTheDocument();
  });

  test('an unsafe URL disables drag panning in the preview', () => {
    const { props, onChange } = buildProps({ url: 'javascript:alert(1)' });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    pointerDown(preview, 100, 100);
    pointerMove(preview, 50, 50);
    pointerUp(preview);

    expect(onChange).not.toHaveBeenCalled();
  });

  test('preview iframe of a cross-origin URL gets allow-scripts allow-same-origin', () => {
    const { props } = buildProps({ url: 'https://example.com' });
    render(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute(
      'sandbox',
      'allow-scripts allow-same-origin'
    );
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute('referrerpolicy', 'no-referrer');
  });

  test('preview iframe of a URL on the Grafana origin gets allow-scripts only', () => {
    // jsdom serves the test page from http://localhost.
    const { props } = buildProps({ url: 'http://localhost/d/abc' });
    render(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute('sandbox', 'allow-scripts');
  });

  test('a URL edit from cross-origin to the Grafana origin mounts a NEW preview iframe with allow-scripts only', () => {
    const { props, options } = buildProps({ url: 'https://example.com' });
    const { rerender } = render(<ViewportEditor {...props} />);
    const before = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(before).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');

    options.url = 'http://localhost/d/abc';
    rerender(<ViewportEditor {...props} />);

    const after = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(after).not.toBe(before);
    expect(before.isConnected).toBe(false);
    expect(after).toHaveAttribute('sandbox', 'allow-scripts');
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
    pointerDown(preview, 200, 150);
    pointerMove(preview, 230, 170);
    pointerUp(preview);

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
    pointerDown(preview, 0, 0);
    // Drag left by 100px (clientX 0 -> -100): reveals content to the right,
    // increasing X by 100/2 = 50.
    pointerMove(preview, -100, 0);

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: 50');
  });

  test('wheel zooms in cursor-anchored and calls onChange with the new clamped zoom', () => {
    const { props, onChange } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    // Wheel up (deltaY < 0) at cursor (200, 100) zooms in by 1.1.
    wheelOver(preview, { deltaY: -100, clientX: 200, clientY: 100 });

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
    wheelOver(preview, { deltaY: -100, clientX: 0, clientY: 0 });

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
    typeAndCommit(inputX, '300');

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
    typeAndCommit(inputY, '200');

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
    typeAndCommit(inputZoom, '2');

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
    typeAndCommit(inputZoom, '99');

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 5.00×');
    expect(options.viewportZoom).toBe(5);
    expect(onChange).toHaveBeenCalledWith(5);
  });

  test('zoom input clamps value below 0.1 to 0.1', () => {
    const { props, options } = buildProps({ viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);
    typeAndCommit(inputZoom, '0.001');

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 0.10×');
    expect(options.viewportZoom).toBe(0.1);
  });

  test('drag interaction updates the numeric X/Y inputs', () => {
    const { props } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    pointerDown(preview, 200, 150);
    pointerMove(preview, 230, 170);
    pointerUp(preview);

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
    wheelOver(preview, { deltaY: -100, clientX: 0, clientY: 0 });

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
    typeAndCommit(inputWidth, '1280');

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ width: '1280px' });
    expect(options.iframeWidth).toBe(1280);
  });

  test('changing height input updates the iframe preview height and persists', () => {
    const { props, options } = buildProps();
    render(<ViewportEditor {...props} />);

    const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);
    typeAndCommit(inputHeight, '720');

    const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
    expect(iframe).toHaveStyle({ height: '720px' });
    expect(options.iframeHeight).toBe(720);
  });

  test('a non-positive width is rejected on commit and the last valid width is restored', () => {
    const { props, options, onChange } = buildProps({ iframeWidth: 1280 });
    render(<ViewportEditor {...props} />);
    onChange.mockClear();

    const inputWidth = screen.getByTestId(viewportEditorTestIds.inputWidth);
    typeAndCommit(inputWidth, '0');

    expect(onChange).not.toHaveBeenCalled();
    expect(options.iframeWidth).toBe(1280);
    expect(inputWidth).toHaveValue(1280);
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ width: '1280px' });
  });

  test('a negative height is rejected on commit and the last valid height is restored', () => {
    const { props, options } = buildProps({ iframeHeight: 720 });
    render(<ViewportEditor {...props} />);

    const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);
    typeAndCommit(inputHeight, '-100');

    expect(options.iframeHeight).toBe(720);
    expect(inputHeight).toHaveValue(720);
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ height: '720px' });
  });

  // ---------------------------------------------------------------------------
  // RV1-11 / RV1-29: fields keep the typed text and commit on blur or Enter
  // ---------------------------------------------------------------------------

  describe('typing into the numeric fields (RV1-11, RV1-29)', () => {
    /** Types `text` one character at a time, as a user does. */
    function typeChars(input: HTMLElement, text: string) {
      for (let i = 1; i <= text.length; i++) {
        fireEvent.change(input, { target: { value: text.slice(0, i) } });
      }
    }

    test('zoom 0.5 can be typed one character at a time; nothing is committed before blur', () => {
      const { props, options, onChange } = buildProps({ viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      onChange.mockClear();
      const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);

      typeChars(inputZoom, '0.5');

      // The field shows what was typed (not 0.10 after the first "0") and nothing is committed yet.
      expect(inputZoom).toHaveValue(0.5);
      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 1.00×');

      fireEvent.blur(inputZoom);

      expect(onChange).toHaveBeenLastCalledWith(0.5);
      expect(options.viewportZoom).toBe(0.5);
      expect(inputZoom).toHaveValue(0.5);
      expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Zoom: 0.50×');
    });

    test('zoom 2.5 typed one character at a time commits 2.5 on Enter', () => {
      const { props, options } = buildProps({ viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);

      typeChars(inputZoom, '2.5');
      fireEvent.keyDown(inputZoom, { key: 'Enter' });

      expect(options.viewportZoom).toBe(2.5);
      expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ transform: 'scale(2.5) translate(0px, 0px)' });
    });

    test('clearing the width while typing does not reset it to 1920; the new width commits on blur', () => {
      const { props, options, onChange } = buildProps({ iframeWidth: 1280 });
      render(<ViewportEditor {...props} />);
      onChange.mockClear();
      const inputWidth = screen.getByTestId(viewportEditorTestIds.inputWidth);

      fireEvent.change(inputWidth, { target: { value: '' } });
      expect(inputWidth).toHaveValue(null);
      expect(options.iframeWidth).toBe(1280);

      typeChars(inputWidth, '800');
      expect(onChange).not.toHaveBeenCalled();
      fireEvent.blur(inputWidth);

      expect(options.iframeWidth).toBe(800);
      expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({ width: '800px' });
    });

    test('an empty height restores the last valid height on blur', () => {
      const { props, options } = buildProps({ iframeHeight: 900 });
      render(<ViewportEditor {...props} />);
      const inputHeight = screen.getByTestId(viewportEditorTestIds.inputHeight);

      typeAndCommit(inputHeight, '');

      expect(inputHeight).toHaveValue(900);
      expect(options.iframeHeight).toBe(900);
    });

    test('a negative X offset can be typed (a lone "-" is kept while typing)', () => {
      const { props, options } = buildProps({ viewportX: 0 });
      render(<ViewportEditor {...props} />);
      const inputX = screen.getByTestId(viewportEditorTestIds.inputX);

      typeChars(inputX, '-25');
      fireEvent.keyDown(inputX, { key: 'Enter' });

      expect(options.viewportX).toBe(-25);
      expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: -25');
    });

    test('Escape restores the committed value without committing', () => {
      const { props, options, onChange } = buildProps({ viewportZoom: 2 });
      render(<ViewportEditor {...props} />);
      onChange.mockClear();
      const inputZoom = screen.getByTestId(viewportEditorTestIds.inputZoom);

      typeChars(inputZoom, '4');
      fireEvent.keyDown(inputZoom, { key: 'Escape' });
      fireEvent.blur(inputZoom);

      expect(inputZoom).toHaveValue(2);
      expect(options.viewportZoom).toBe(2);
      expect(onChange).not.toHaveBeenCalled();
    });

    test('a drag while text is typed replaces the typed text and the text is not committed later', () => {
      const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const inputX = screen.getByTestId(viewportEditorTestIds.inputX);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);

      typeChars(inputX, '300');
      pointerDown(preview, 100, 100);
      pointerMove(preview, 60, 100);
      pointerUp(preview);

      expect(inputX).toHaveValue(40);
      fireEvent.blur(inputX);
      expect(options.viewportX).toBe(40);
    });
  });

  // ---------------------------------------------------------------------------
  // RV1-27: pointer drag with capture, wheel only when the pointer is over the preview
  // ---------------------------------------------------------------------------

  describe('pointer and wheel handling (RV1-27)', () => {
    test('pointerdown captures the pointer so the drag keeps tracking outside the preview', () => {
      const { props } = buildProps();
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);

      fireEvent.pointerDown(preview, { pointerId: 7, button: 0, buttons: 1, clientX: 10, clientY: 10 });

      expect(Element.prototype.setPointerCapture).toHaveBeenLastCalledWith(7);
    });

    test.each([
      ['pointerup', (el: Element) => fireEvent.pointerUp(el, { pointerId: 1, buttons: 0 })],
      ['pointercancel', (el: Element) => fireEvent.pointerCancel(el, { pointerId: 1 })],
      ['lostpointercapture', (el: Element) => fireEvent.lostPointerCapture(el, { pointerId: 1 })],
    ])('%s ends the drag', (_name, end) => {
      const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);

      pointerDown(preview, 100, 100);
      pointerMove(preview, 90, 100);
      end(preview);
      pointerMove(preview, 0, 0);

      expect(options.viewportX).toBe(10);
      expect(options.viewportY).toBe(0);
    });

    test('a move with no button held ends a drag whose release was missed (released outside the window)', () => {
      const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);

      pointerDown(preview, 100, 100);
      pointerMove(preview, 90, 100);
      // The button came up outside the window and no pointerup arrived.
      pointerMove(preview, 50, 50, 0);
      // A later move with a button held (a new gesture elsewhere) does not pan.
      pointerMove(preview, 0, 0, 1);

      expect(options.viewportX).toBe(10);
      expect(options.viewportY).toBe(0);
    });

    test('a non-primary button does not start a drag', () => {
      const { props, onChange } = buildProps();
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);
      onChange.mockClear();

      fireEvent.pointerDown(preview, { pointerId: 1, button: 2, buttons: 2, clientX: 100, clientY: 100 });
      pointerMove(preview, 50, 50, 2);

      expect(onChange).not.toHaveBeenCalled();
    });

    test('the wheel zooms and is consumed only after the pointer moved over the preview', () => {
      const { props, onChange } = buildProps({ viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);
      onChange.mockClear();

      // The options pane scrolled the preview under a resting pointer: no pointermove.
      const notPrevented = fireEvent.wheel(preview, { deltaY: -100, clientX: 10, clientY: 10 });
      expect(notPrevented).toBe(true);
      expect(onChange).not.toHaveBeenCalled();

      // The author moves the pointer onto the preview: the wheel now zooms.
      expect(wheelOver(preview, { deltaY: -100, clientX: 10, clientY: 10 })).toBe(false);
      expect(onChange).toHaveBeenCalledTimes(1);
    });

    test('a page scroll disarms the wheel until the pointer moves again', () => {
      const { props, onChange } = buildProps({ viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);
      fireEvent.pointerMove(preview, { pointerId: 1, buttons: 0, clientX: 10, clientY: 10 });
      onChange.mockClear();

      fireEvent.scroll(document.body);

      expect(fireEvent.wheel(preview, { deltaY: -100, clientX: 10, clientY: 10 })).toBe(true);
      expect(onChange).not.toHaveBeenCalled();
    });

    test('leaving the preview disarms the wheel', () => {
      const { props, onChange } = buildProps({ viewportZoom: 1 });
      render(<ViewportEditor {...props} />);
      const preview = screen.getByTestId(viewportEditorTestIds.preview);
      fireEvent.pointerMove(preview, { pointerId: 1, buttons: 0, clientX: 10, clientY: 10 });
      fireEvent.pointerLeave(preview, { pointerId: 1 });
      onChange.mockClear();

      expect(fireEvent.wheel(preview, { deltaY: -100, clientX: 10, clientY: 10 })).toBe(true);
      expect(onChange).not.toHaveBeenCalled();
    });

    test('the preview is a labelled group, not an application, and points to the keyboard alternative', () => {
      const { props } = buildProps();
      render(<ViewportEditor {...props} />);

      expect(screen.queryByRole('application')).not.toBeInTheDocument();
      const preview = screen.getByRole('group', { name: 'Viewport preview' });
      expect(preview).toHaveAccessibleDescription(/use the X, Y and Zoom fields/);
    });

    test.each([
      ['Viewport X offset', 'X'],
      ['Viewport Y offset', 'Y'],
      ['Viewport zoom', 'Zoom (0.1–5)'],
      ['Virtual iframe width', 'Page width (px)'],
      ['Virtual iframe height', 'Page height (px)'],
    ])('the %s field has an accessible name and its visible label is associated', (name, label) => {
      const { props } = buildProps();
      render(<ViewportEditor {...props} />);

      const input = screen.getByRole('spinbutton', { name });
      expect(screen.getByText(label).closest('label')).toHaveAttribute('for', input.id);
    });
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
    typeAndCommit(inputX, '');

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
    typeAndCommit(screen.getByTestId(viewportEditorTestIds.inputY), 'abc');

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('Y: 77');
    expect(options.viewportY).toBe(77);
    expect(onChange).not.toHaveBeenCalled();
  });

  test('non-finite zoom input is a no-op (does not clamp NaN to a bound)', () => {
    const { props, options, onChange } = buildProps({ viewportZoom: 2 });
    render(<ViewportEditor {...props} />);

    onChange.mockClear();
    typeAndCommit(screen.getByTestId(viewportEditorTestIds.inputZoom), '');

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

  test('a pointer move with no active drag is ignored (no pan)', () => {
    const { props, options } = buildProps({ viewportX: 5, viewportY: 6, viewportZoom: 1 });
    render(<ViewportEditor {...props} />);
    const preview = screen.getByTestId(viewportEditorTestIds.preview);

    // No pointerdown first: the move handler must bail (drag ref is null) and
    // leave the viewport untouched, even with a button held.
    pointerMove(preview, 500, 500);

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: 5');
    expect(readout).toHaveTextContent('Y: 6');
    expect(options.viewportX).toBe(5);
    expect(options.viewportY).toBe(6);
  });

  test('dragging past the origin keeps the negative offsets after Grafana re-renders (RV1-10, no snap-back)', () => {
    const { props, options } = buildProps({ viewportX: 10, viewportY: 10, viewportZoom: 1 });
    const { rerender } = render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    pointerDown(preview, 100, 100);
    pointerMove(preview, 150, 140);
    pointerUp(preview);

    expect(options.viewportX).toBe(-40);
    expect(options.viewportY).toBe(-30);

    // Grafana re-renders the editor with the committed options.
    rerender(<ViewportEditor {...props} />);

    const readout = screen.getByTestId(viewportEditorTestIds.readout);
    expect(readout).toHaveTextContent('X: -40');
    expect(readout).toHaveTextContent('Y: -30');
    expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveStyle({
      transform: 'scale(1) translate(40px, 30px)',
    });
  });

  test('cursor-anchored zoom-out near the top-left edge keeps its negative offsets', () => {
    const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    const { rerender } = render(<ViewportEditor {...props} />);

    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    // Zoom out at (200, 100): the point under the cursor stays put, so X/Y go negative.
    wheelOver(preview, { deltaY: 100, clientX: 200, clientY: 100 });
    rerender(<ViewportEditor {...props} />);

    expect(options.viewportX).toBeLessThan(0);
    expect(options.viewportY).toBeLessThan(0);
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent(
      `X: ${Math.round(options.viewportX)}`
    );
  });

  test('a re-render with UNCHANGED options does not clobber live interaction state', () => {
    const { props, options } = buildProps({ viewportX: 0, viewportY: 0, viewportZoom: 1 });
    const { rerender } = render(<ViewportEditor {...props} />);

    // Drive a local interaction (drag) that this editor commits.
    const preview = screen.getByTestId(viewportEditorTestIds.preview);
    pointerDown(preview, 200, 150);
    pointerMove(preview, 230, 170);
    pointerUp(preview);
    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: -30');

    // Grafana re-renders with options reflecting our own committed write — the
    // re-sync effect must recognise this as our write and NOT reset live state.
    rerender(<ViewportEditor {...props} />);

    expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: -30');
    expect(options.viewportX).toBe(-30);
  });

  // ---------------------------------------------------------------------------
  // FR5: the preview renders proxy mode the same way as the panel
  // ---------------------------------------------------------------------------

  describe('proxy-mode preview (FR5)', () => {
    test('fetches the proxied HTML and renders it with srcdoc and sandbox="allow-scripts"', () => {
      const { props } = buildProps({ loadMode: 'proxy', viewportX: 10, viewportY: 20, viewportZoom: 2 });
      render(<ViewportEditor {...props} />);

      expect(screen.getByTestId(viewportEditorTestIds.proxyLoading)).toHaveAttribute('role', 'status');
      expect(proxyFetch.fetch).toHaveBeenCalledTimes(1);
      const params = new URLSearchParams(proxyFetch.calls[0].options.url.split('?')[1]);
      expect(params.get('url')).toBe('https://example.com');

      respondHtml(proxyFetch.calls[0], '<h1>Example Domain</h1>');

      const iframe = screen.getByTestId(viewportEditorTestIds.iframe);
      expect(iframe).toHaveAttribute('srcdoc', '<h1>Example Domain</h1>');
      expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
      expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');
      expect(iframe).toHaveAttribute('title', 'Viewport preview');
      expect(iframe).not.toHaveAttribute('src');
      expect(iframe).toHaveStyle({ transform: 'scale(2) translate(-10px, -20px)', pointerEvents: 'none' });
    });

    test('auto mode + detectedMode=proxy also previews through the proxy', () => {
      const { props } = buildProps({ loadMode: 'auto', detectedMode: 'proxy' });
      render(<ViewportEditor {...props} />);

      respondHtml(proxyFetch.calls[0], '<p>proxied</p>');

      expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute('srcdoc', '<p>proxied</p>');
    });

    test('a proxy error shows an accessible error and NO iframe', () => {
      const { props } = buildProps({ loadMode: 'proxy' });
      const { container } = render(<ViewportEditor {...props} />);

      respondHttpError(proxyFetch.calls[0], 403, 'Forbidden', 'target host is not allowlisted');

      const error = screen.getByTestId(viewportEditorTestIds.proxyError);
      expect(error).toHaveAttribute('role', 'alert');
      expect(error).toHaveTextContent('HTTP 403');
      expect(error).toHaveTextContent('target host is not allowlisted');
      expect(container.querySelector('iframe')).toBeNull();
    });

    test('backend unavailable → fallback, no fetch, no iframe (DF3)', () => {
      mockedUseBackendAvailable.mockReturnValue({ loading: false, backendAvailable: false });
      const { props } = buildProps({ loadMode: 'proxy' });
      const { container } = render(<ViewportEditor {...props} />);

      expect(screen.getByTestId(viewportEditorTestIds.proxyUnavailable)).toHaveTextContent(/backend proxy/i);
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
      expect(container.querySelector('iframe')).toBeNull();
    });

    test('backend probe in flight → neutral placeholder, no fetch (DF3)', () => {
      mockedUseBackendAvailable.mockReturnValue({ loading: true, backendAvailable: false });
      const { props } = buildProps({ loadMode: 'proxy' });
      render(<ViewportEditor {...props} />);

      expect(screen.getByTestId(viewportEditorTestIds.backendLoading)).toBeInTheDocument();
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
    });

    test('an unsafe URL in proxy mode is never fetched and renders NO iframe', () => {
      const { props } = buildProps({ loadMode: 'proxy', url: 'javascript:alert(1)' });
      const { container } = render(<ViewportEditor {...props} />);

      expect(screen.getByTestId(viewportEditorTestIds.invalidUrl)).toBeInTheDocument();
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
      expect(container.querySelector('iframe')).toBeNull();
    });

    test('panning the preview does not refetch the page', () => {
      const { props } = buildProps({ loadMode: 'proxy' });
      render(<ViewportEditor {...props} />);
      respondHtml(proxyFetch.calls[0], '<p>x</p>');

      const preview = screen.getByTestId(viewportEditorTestIds.preview);
      pointerDown(preview, 100, 100);
      pointerMove(preview, 50, 50);
      pointerUp(preview);

      expect(screen.getByTestId(viewportEditorTestIds.readout)).toHaveTextContent('X: 50');
      expect(proxyFetch.fetch).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute('srcdoc', '<p>x</p>');
    });

    test('direct mode preview never fetches', () => {
      const { props } = buildProps({ loadMode: 'direct' });
      render(<ViewportEditor {...props} />);

      expect(screen.getByTestId(viewportEditorTestIds.iframe)).toHaveAttribute('src', 'https://example.com');
      expect(proxyFetch.fetch).not.toHaveBeenCalled();
    });
  });
});

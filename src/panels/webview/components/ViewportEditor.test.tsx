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
});

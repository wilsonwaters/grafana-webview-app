import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { LoadModeEditor } from './LoadModeEditor';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { loadModeEditorTestIds } from './loadModeEditorTestIds';
import { useBackendAvailable, type BackendAvailability } from '../useBackendAvailable';

// DF2: mock the backend-availability hook so each test drives the
// available / unavailable / loading states deterministically.
jest.mock('../useBackendAvailable', () => ({
  useBackendAvailable: jest.fn(),
}));

const mockedUseBackendAvailable = useBackendAvailable as jest.MockedFunction<typeof useBackendAvailable>;

type Props = StandardEditorProps<PanelOptions['loadMode'], unknown, PanelOptions>;

function buildProps(optionOverrides: Partial<PanelOptions> = {}): {
  props: Props;
  options: PanelOptions;
  onChange: jest.Mock;
} {
  const options: PanelOptions = { ...DEFAULT_PANEL_OPTIONS, url: 'https://example.com', ...optionOverrides };
  // Faithfully model Grafana: a custom editor's bound onChange persists the
  // value to its path (loadMode) on the shared options object.
  const onChange = jest.fn((mode: PanelOptions['loadMode']) => {
    options.loadMode = mode;
  });
  const props = {
    value: options.loadMode,
    onChange,
    context: { data: [], options },
    item: {} as Props['item'],
  } as unknown as Props;
  return { props, options, onChange };
}

function setBackend(state: BackendAvailability) {
  mockedUseBackendAvailable.mockReturnValue(state);
}

/** Finds the radio input whose accessible label matches the option text. */
function radio(label: string): HTMLInputElement {
  return screen.getByRole('radio', { name: label }) as HTMLInputElement;
}

/** Like {@link radio} but returns null when the option is not rendered. */
function queryRadio(label: string): HTMLInputElement | null {
  return screen.queryByRole('radio', { name: label }) as HTMLInputElement | null;
}

beforeEach(() => {
  mockedUseBackendAvailable.mockReset();
});

describe('panels/webview/LoadModeEditor', () => {
  // ---------------------------------------------------------------------------
  // DF2: backend available — full Auto/Direct/Proxy, no note, value honoured
  // ---------------------------------------------------------------------------

  test('renders all options enabled with no note when the backend is available', () => {
    setBackend({ loading: false, backendAvailable: true });
    const { props } = buildProps({ loadMode: 'proxy' });
    render(<LoadModeEditor {...props} />);

    expect(radio('Auto')).toBeEnabled();
    expect(radio('Direct')).toBeEnabled();
    expect(radio('Proxy')).toBeEnabled();
    // Stored value honoured.
    expect(radio('Proxy')).toBeChecked();
    expect(screen.queryByTestId(loadModeEditorTestIds.backendUnavailableNote)).not.toBeInTheDocument();
  });

  test('commits the selected mode through onChange when available', () => {
    setBackend({ loading: false, backendAvailable: true });
    const { props, onChange } = buildProps({ loadMode: 'direct' });
    render(<LoadModeEditor {...props} />);

    fireEvent.click(radio('Proxy'));
    expect(onChange).toHaveBeenCalledWith('proxy');
  });

  // ---------------------------------------------------------------------------
  // DF2: backend unavailable — Proxy/Auto disabled, note shown, clamped Direct
  // ---------------------------------------------------------------------------

  test('omits backend-dependent options, clamps to Direct and shows a note when unavailable', () => {
    setBackend({ loading: false, backendAvailable: false });
    // A previously-saved proxy panel: the displayed selection clamps to Direct.
    const { props } = buildProps({ loadMode: 'proxy' });
    render(<LoadModeEditor {...props} />);

    // Auto and Proxy are not offered at all — Direct is the only choice.
    expect(queryRadio('Auto')).not.toBeInTheDocument();
    expect(queryRadio('Proxy')).not.toBeInTheDocument();
    expect(radio('Direct')).toBeInTheDocument();
    // Effective mode is Direct (display-only clamp).
    expect(radio('Direct')).toBeChecked();
    expect(screen.getByTestId(loadModeEditorTestIds.backendUnavailableNote)).toBeInTheDocument();
  });

  test('does NOT persist a clamp on mount when unavailable (saved value untouched)', () => {
    setBackend({ loading: false, backendAvailable: false });
    const { props, options, onChange } = buildProps({ loadMode: 'proxy' });
    render(<LoadModeEditor {...props} />);

    // Display-only clamp: the stored loadMode must remain 'proxy', and onChange
    // must not have fired merely from opening the editor.
    expect(onChange).not.toHaveBeenCalled();
    expect(options.loadMode).toBe('proxy');
  });

  // ---------------------------------------------------------------------------
  // DF2: probe still loading — no premature note, options not yet degraded
  // ---------------------------------------------------------------------------

  test('does not show the note while the probe is still loading', () => {
    setBackend({ loading: true, backendAvailable: false });
    const { props } = buildProps({ loadMode: 'auto' });
    render(<LoadModeEditor {...props} />);

    expect(screen.queryByTestId(loadModeEditorTestIds.backendUnavailableNote)).not.toBeInTheDocument();
    // Options remain enabled (neutral) until the probe settles.
    expect(radio('Auto')).toBeEnabled();
    expect(radio('Proxy')).toBeEnabled();
  });
});

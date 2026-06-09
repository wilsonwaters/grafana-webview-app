import React from 'react';
import { render } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { useBackendAvailable } from '../useBackendAvailable';

// DF2: defensive commit-guard coverage (gap-fill).
//
// While the backend is degraded, the Auto/Proxy options are not rendered, so
// the ONLY way a backend-dependent value can reach `handleChange` is a stale or
// programmatic event from the underlying control. The main LoadModeEditor test
// file exercises the rendered radios; here we mock RadioButtonGroup to capture
// its `onChange` and fire backend-dependent / direct values directly, asserting
// the guard swallows the former and commits the latter.
//
// The mock is file-local (top-level jest.mock) so it never affects the real
// RadioButtonGroup rendering used by LoadModeEditor.test.tsx.

let capturedOnChange: ((v: PanelOptions['loadMode']) => void) | undefined;

jest.mock('@grafana/ui', () => {
  const actual = jest.requireActual('@grafana/ui');
  return {
    ...actual,
    RadioButtonGroup: (props: { onChange: (v: PanelOptions['loadMode']) => void }) => {
      capturedOnChange = props.onChange;
      return null;
    },
  };
});

jest.mock('../useBackendAvailable', () => ({
  useBackendAvailable: jest.fn(),
}));

// Imported AFTER the mocks above so it picks up the mocked RadioButtonGroup.
import { LoadModeEditor } from './LoadModeEditor';

const mockedUseBackendAvailable = useBackendAvailable as jest.MockedFunction<typeof useBackendAvailable>;

type Props = StandardEditorProps<PanelOptions['loadMode'], unknown, PanelOptions>;

function buildProps(loadMode: PanelOptions['loadMode']): { props: Props; onChange: jest.Mock } {
  const options: PanelOptions = { ...DEFAULT_PANEL_OPTIONS, url: 'https://example.com', loadMode };
  const onChange = jest.fn();
  const props = {
    value: loadMode,
    onChange,
    context: { data: [], options },
    item: {} as Props['item'],
  } as unknown as Props;
  return { props, onChange };
}

beforeEach(() => {
  capturedOnChange = undefined;
  mockedUseBackendAvailable.mockReset();
});

describe('LoadModeEditor handleChange defensive guard (degraded)', () => {
  test('swallows a backend-dependent (proxy) value while degraded but commits direct', () => {
    mockedUseBackendAvailable.mockReturnValue({ loading: false, backendAvailable: false });
    const { props, onChange } = buildProps('proxy');
    render(<LoadModeEditor {...props} />);

    expect(capturedOnChange).toBeDefined();

    // A stale/programmatic backend-dependent value must be swallowed by the guard.
    capturedOnChange!('proxy');
    capturedOnChange!('auto');
    expect(onChange).not.toHaveBeenCalled();

    // Direct passes through the same path while degraded.
    capturedOnChange!('direct');
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('direct');
  });

  test('when available the guard is inert — a proxy value commits', () => {
    mockedUseBackendAvailable.mockReturnValue({ loading: false, backendAvailable: true });
    const { props, onChange } = buildProps('direct');
    render(<LoadModeEditor {...props} />);

    capturedOnChange!('proxy');
    expect(onChange).toHaveBeenCalledWith('proxy');
  });
});

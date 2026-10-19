import type { PanelOptionsEditorBuilder, StandardEditorContext } from '@grafana/data';
import type { PanelOptions } from '../../types';
import { plugin } from './module';

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

interface RecordedOption {
  /** The builder method, for example `addTextInput`. */
  method: string;
  path: string;
  name?: string;
  description?: string;
  defaultValue?: unknown;
  settings?: Record<string, unknown>;
}

/**
 * The options that the panel registers, keyed by path. The real builder needs
 * Grafana's editor registry, which only exists in a running Grafana, so a
 * recording builder stands in for it.
 */
function optionItems(): Map<string, RecordedOption> {
  const items = new Map<string, RecordedOption>();
  const builder: Record<string, unknown> = new Proxy(
    {},
    {
      get: (_target, method: string) => (config: Omit<RecordedOption, 'method'>) => {
        items.set(config.path, { method, ...config });
        return builder;
      },
    }
  );
  plugin.getPanelOptionsSupplier()(
    builder as unknown as PanelOptionsEditorBuilder<PanelOptions>,
    { data: [] } as unknown as StandardEditorContext<PanelOptions>
  );
  return items;
}

describe('panels/webview/module', () => {
  test('the auto-refresh interval is whole seconds and its description states the 5 s minimum (RV1-12)', () => {
    const item = optionItems().get('refreshIntervalSec');

    expect(item).toMatchObject({ method: 'addNumberInput', settings: { min: 0, integer: true } });
    expect(item?.description).toMatch(/minimum is 5 seconds/);
  });
});

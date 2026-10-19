import type { PanelModel, PanelOptionsEditorBuilder, StandardEditorContext } from '@grafana/data';
import type { PanelOptions } from '../../types';
import { plugin } from './module';
import { HideSelectorsEditor } from './components/HideSelectorsEditor';

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
  editor?: unknown;
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
  test('registers the detectedForUrl migration, also for panels whose version already matches', () => {
    const legacyPanel = { options: { url: 'https://a.example', detectedMode: 'proxy' } } as unknown as PanelModel;

    expect(plugin.shouldMigrate?.(legacyPanel)).toBe(true);
    expect(plugin.onPanelMigration?.(legacyPanel)).toEqual({
      url: 'https://a.example',
      detectedMode: 'proxy',
      detectedForUrl: 'https://a.example',
    });
    expect(plugin.shouldMigrate?.({ options: { detectedMode: null } } as unknown as PanelModel)).toBe(false);
  });

  test('hideSelectors has a multi-line editor that asks for one selector per line and states the limits (RV1-17, CR5)', () => {
    const item = optionItems().get('hideSelectors');

    expect(item).toMatchObject({
      method: 'addCustomEditor',
      name: 'Hide elements (proxy mode)',
      defaultValue: '',
      settings: { rows: 3 },
    });
    expect(item?.editor).toBe(HideSelectorsEditor);
    expect(item?.description).toMatch(/one per line/);
    expect(item?.description).toMatch(/proxy mode only/i);
    expect(item?.description).toMatch(/at most 20 selectors/i);
    expect(item?.description).toMatch(/:has\(\)/);
  });

  test('the auto-refresh interval is whole seconds and its description states the 5 s minimum (RV1-12)', () => {
    const item = optionItems().get('refreshIntervalSec');

    expect(item).toMatchObject({ method: 'addNumberInput', settings: { min: 0, integer: true } });
    expect(item?.description).toMatch(/minimum is 5 seconds/);
  });
});

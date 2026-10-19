import { normalizeOptions, type PanelOptions } from '../../types';
import { resolveLoadMode } from './loadMode';
import { migratePanelOptions, needsDetectedForUrl } from './migrations';

/** Stored dashboard JSON from before `detectedForUrl` existed. */
const legacy = (overrides: Record<string, unknown> = {}) =>
  ({ url: ' https://blocked.example ', loadMode: 'auto', detectedMode: 'proxy', viewportX: 12, ...overrides }) as Partial<PanelOptions>;

describe('panels/webview/migrations (detectedForUrl, RV1-6)', () => {
  test('a legacy verdict is recorded against the saved (trimmed) URL; other options are kept', () => {
    expect(migratePanelOptions(legacy())).toEqual({
      url: ' https://blocked.example ',
      loadMode: 'auto',
      detectedMode: 'proxy',
      viewportX: 12,
      detectedForUrl: 'https://blocked.example',
    });
  });

  test('a migrated legacy panel still renders through the proxy (existing dashboards keep working)', () => {
    expect(resolveLoadMode(normalizeOptions(migratePanelOptions(legacy())))).toBe('proxy');
  });

  test('after migration, a URL edit makes the legacy verdict stale', () => {
    const migrated = migratePanelOptions(legacy());
    expect(resolveLoadMode(normalizeOptions({ ...migrated, url: 'https://other.example' }))).toBe('direct');
  });

  test('the migration is idempotent and leaves a recorded URL unchanged', () => {
    const current = legacy({ detectedForUrl: 'https://tested.example' });
    expect(needsDetectedForUrl(current)).toBe(false);
    expect(migratePanelOptions(current)).toEqual(current);
    expect(migratePanelOptions(migratePanelOptions(legacy()))).toEqual(migratePanelOptions(legacy()));
  });

  test('a panel without a verdict needs no migration', () => {
    expect(needsDetectedForUrl(legacy({ detectedMode: null }))).toBe(false);
    expect(migratePanelOptions(legacy({ detectedMode: null }))).not.toHaveProperty('detectedForUrl');
  });

  test('missing or odd options are handled', () => {
    expect(needsDetectedForUrl(undefined)).toBe(false);
    expect(needsDetectedForUrl(null)).toBe(false);
    expect(migratePanelOptions(undefined)).toEqual({});
    expect(migratePanelOptions({ detectedMode: 'direct' })).toEqual({ detectedMode: 'direct', detectedForUrl: '' });
  });
});

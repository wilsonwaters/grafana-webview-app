import pluginJson from './plugin.json';
import { PLUGIN_ID } from './constants';
import { PROXY_RESOURCE_BASE } from './panels/webview/loadMode';
import { HEALTH_RESOURCE_PATH } from './panels/webview/useBackendAvailable';
import { CHECK_FRAMEABLE_PATH } from './panels/webview/components/FrameabilityEditor';

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

describe('constants', () => {
  test('PLUGIN_ID is the app id from src/plugin.json', () => {
    expect(PLUGIN_ID).toBe(pluginJson.id);
    expect(PLUGIN_ID).toBe('wilsonwaters-webview-app');
  });

  test('every plugin resource path is built from PLUGIN_ID', () => {
    const base = `/api/plugins/${pluginJson.id}/resources/`;
    expect(PROXY_RESOURCE_BASE).toBe(`${base}proxy`);
    expect(HEALTH_RESOURCE_PATH).toBe(`${base}health`);
    expect(CHECK_FRAMEABLE_PATH).toBe(`${base}check-frameable`);
  });
});

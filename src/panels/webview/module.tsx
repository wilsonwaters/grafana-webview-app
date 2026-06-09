import { PanelPlugin } from '@grafana/data';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../types';
import { WebViewPanel } from './components/WebViewPanel';
import { ViewportEditor } from './components/ViewportEditor';
import { FrameabilityEditor } from './components/FrameabilityEditor';
import { LoadModeEditor } from './components/LoadModeEditor';

/**
 * Registration for the nested Web View panel plugin.
 *
 * This panel is bundled inside the `wilsonwaters-webview-app` app plugin
 * (see ./plugin.json). The webpack build discovers this `module.tsx` via the
 * sibling `plugin.json` and emits it to `dist/panels/webview/module.js`.
 *
 * At this stage the panel is a registration/packaging placeholder only: the
 * options editor exposes the F2 `PanelOptions` fields and the component renders
 * a placeholder. Real viewport / proxy behaviour is added by later streams.
 */
export const plugin = new PanelPlugin<PanelOptions>(WebViewPanel).setPanelOptions((builder) => {
  builder
    .addTextInput({
      path: 'url',
      name: 'URL',
      description: 'The external web page to display in the panel.',
      defaultValue: DEFAULT_PANEL_OPTIONS.url,
    })
    // DF2: load-mode selector. Converted from a standard `addRadio` to a custom
    // editor so it can consume the async `useBackendAvailable` probe and degrade
    // gracefully (disable Auto/Proxy + clamp to Direct + show a note) when the
    // backend is unavailable. Same name/description/default and Auto/Direct/Proxy
    // choices as before; bound to `loadMode` (Grafana wires the custom editor's
    // onChange to this single path).
    .addCustomEditor({
      id: 'loadModeSelector',
      path: 'loadMode',
      name: 'Load mode',
      description: 'How the URL is loaded. Auto uses the mode from the Test URL result (defaults to Direct).',
      editor: LoadModeEditor,
      defaultValue: DEFAULT_PANEL_OPTIONS.loadMode,
    })
    .addNumberInput({
      path: 'refreshIntervalSec',
      name: 'Auto-refresh interval (seconds)',
      description: 'Reload the iframe at this interval. 0 disables auto-refresh.',
      defaultValue: DEFAULT_PANEL_OPTIONS.refreshIntervalSec,
      settings: {
        min: 0,
        integer: false,
        placeholder: '0 (disabled)',
      },
    })
    .addBooleanSwitch({
      path: 'showDebugOverlay',
      name: 'Show debug overlay',
      description: 'Show viewport / load-mode debug information on the panel.',
      defaultValue: DEFAULT_PANEL_OPTIONS.showDebugOverlay,
    })
    // PC3: interactive viewport positioning. Bound to `viewportZoom` (Grafana
    // wires a custom editor's onChange to this single path); the editor reads
    // the full options from `context.options` and writes X/Y/zoom. Grafana only
    // renders custom option editors in the edit pane, so the view-mode panel
    // stays static (Q3 resolution).
    .addCustomEditor({
      id: 'viewportPositioner',
      path: 'viewportZoom',
      name: 'Position viewport',
      description: 'Drag to pan and scroll to zoom the preview to position the viewport.',
      editor: ViewportEditor,
      defaultValue: DEFAULT_PANEL_OPTIONS.viewportZoom,
    })
    // FR3: "Test URL" frameability check. Bound to `detectedMode` (Grafana wires
    // a custom editor's onChange to this single path); the editor reads the
    // current `url` from `context.options`, calls `/check-frameable`, and on a
    // successful response persists the recommended mode into `detectedMode`.
    .addCustomEditor({
      id: 'frameabilityTester',
      path: 'detectedMode',
      name: 'Test URL',
      description: 'Check whether the URL can be framed directly or must be proxied.',
      editor: FrameabilityEditor,
      defaultValue: DEFAULT_PANEL_OPTIONS.detectedMode,
    });
});

import React from 'react';
import { PluginPage } from '@grafana/runtime';
import { appTestIds } from '../testIds';

/**
 * Minimal app root page.
 *
 * The scaffold's demo pages (Page One–Four) were removed in F4; the app's
 * user-facing surface is the admin Configuration page (see AppConfig) plus the
 * nested Web View panel registered in src/panels/webview. This root is a small
 * informational landing page so the app entry renders without error.
 */
export function AppRoot() {
  return (
    <PluginPage>
      <div data-testid={appTestIds.root.container}>
        The Web View app provides the &quot;Web View&quot; panel visualization. Add it to a
        dashboard panel to embed an external web page. Admin settings are available on the
        Configuration tab.
      </div>
    </PluginPage>
  );
}

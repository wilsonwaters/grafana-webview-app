import { test } from './fixtures';
import { assertThemedPanelRenders } from './tc4Helpers';

/**
 * TC4 — AC35 (light theme).
 *
 * plugin-e2e applies the theme via window.grafanaBootData.user.preferences, so
 * useStyles2 resolves the matching GrafanaTheme2 and the container/overlay styles
 * adapt. `userPreferences` is a worker-scoped option, so it must be set at the
 * top level of the file (not inside a describe).
 *
 * SCOPE NOTE (FR5 deferred — issue #102): this asserts only direct render + theme
 * adaptation; it never asserts that proxied content renders inside the panel.
 */
test.use({ userPreferences: { theme: 'light' } });

test('TC4 AC35: panel + debug overlay render correctly under the light theme', async ({
  panelEditPage,
  page,
}) => {
  test.setTimeout(180000);
  await assertThemedPanelRenders(panelEditPage, page, 'light');
});

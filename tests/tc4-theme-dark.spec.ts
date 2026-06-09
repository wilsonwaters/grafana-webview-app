import { test } from './fixtures';
import { assertThemedPanelRenders } from './tc4Helpers';

/**
 * TC4 — AC35 (dark theme).
 *
 * Companion to tc4-theme-light.spec.ts. plugin-e2e's `userPreferences.theme` is a
 * worker-scoped option, so each theme lives in its own top-level spec file.
 *
 * SCOPE NOTE (FR5 deferred — issue #102): asserts only direct render + theme
 * adaptation; never asserts that proxied content renders inside the panel.
 */
test.use({ userPreferences: { theme: 'dark' } });

test('TC4 AC35: panel + debug overlay render correctly under the dark theme', async ({
  panelEditPage,
  page,
}) => {
  test.setTimeout(180000);
  await assertThemedPanelRenders(panelEditPage, page, 'dark');
});

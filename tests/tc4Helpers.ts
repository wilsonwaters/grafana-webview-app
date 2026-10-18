import type { PanelEditPage } from '@grafana/plugin-e2e';
import { expect } from './fixtures';

const WEB_VIEW = 'Web View';

/**
 * Selects the Web View visualization in the editor of a new panel.
 *
 * Grafana 12.4.x opens the editor of a new panel with the visualization picker
 * already open, and the picker toggle is visible at the same time. plugin-e2e
 * 3.15's `setVisualization` clicks the toggle whenever it is visible, which
 * closes the picker, and then waits for the "All visualizations" tab until it
 * times out. When the picker is already open, select the visualization with the
 * plugin-e2e selectors. Otherwise, use `setVisualization`.
 */
async function selectWebViewVisualization(panelEditPage: PanelEditPage): Promise<void> {
  const { components, constants } = panelEditPage.ctx.selectors;
  const allVisualizationsTab = panelEditPage.getByGrafanaSelector(components.Tab.title(constants.Tab.title));
  const vizPickerToggle = panelEditPage.getByGrafanaSelector(components.PanelEditor.toggleVizPicker);

  // Wait until the editor shows the open picker or the toggle that opens it.
  await expect(allVisualizationsTab.or(vizPickerToggle).first()).toBeVisible({ timeout: 15000 });

  if (await allVisualizationsTab.isVisible()) {
    await allVisualizationsTab.click();
    await panelEditPage.getByGrafanaSelector(components.PluginVisualization.item(WEB_VIEW)).click();
  } else {
    await panelEditPage.setVisualization(WEB_VIEW);
  }
  await expect(panelEditPage.getVisualizationName()).toHaveText(WEB_VIEW);
}

/**
 * Selects the Web View visualization and waits for its options to render. Use
 * this in every spec instead of calling `panelEditPage.setVisualization`.
 *
 * Selecting the visualization can be slow on cold start (the visualization
 * picker can be slow to register the nested panel). Retry with backoff so specs
 * do not flake for reasons unrelated to what they assert.
 */
export async function setWebViewVisualization(panelEditPage: PanelEditPage, page: any): Promise<void> {
  let lastErr: unknown;
  for (let i = 0; i < 4; i++) {
    try {
      // Cap each attempt so a hung selection (it can occasionally stall for a
      // long time on a cold worker) is abandoned quickly and retried, rather than
      // consuming the whole per-test timeout in a single attempt.
      await Promise.race([
        selectWebViewVisualization(panelEditPage),
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error('setVisualization attempt timed out (25s)')), 25000)
        ),
      ]);
      // Confirm the editor actually rendered (the URL field is the first option).
      await expect(page.getByRole('textbox', { name: 'URL' })).toBeVisible({ timeout: 15000 });
      return;
    } catch (e) {
      lastErr = e;
      if (i < 3) {
        await page.waitForTimeout(1500);
      }
    }
  }
  throw new Error(`setVisualization('Web View') failed after retries: ${String(lastErr)}`);
}

/** The canonical framable host used across the suite. Allowlisted via provisioning. */
export const FRAMABLE_URL = 'https://example.com';

/**
 * AC35: render the Web View panel under the active theme and assert it renders
 * without breakage and the theme-aware styles applied. Shared by the light and
 * dark theme specs (each in its own file because plugin-e2e's
 * `userPreferences.theme` is a worker-scoped option and must be set top-level).
 */
export async function assertThemedPanelRenders(panelEditPage: any, page: any, theme: 'light' | 'dark'): Promise<void> {
  await setWebViewVisualization(panelEditPage, page);

  await page.getByRole('textbox', { name: 'URL' }).fill(FRAMABLE_URL);
  await page.keyboard.press('Tab');

  // The panel renders its iframe without breakage under this theme.
  const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
  await expect(iframe).toBeVisible({ timeout: 10000 });

  // Enable the theme-aware debug overlay.
  await page.getByRole('switch', { name: /show debug overlay/i }).check({ force: true });
  const overlay = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-debug-overlay');
  await expect(overlay).toBeVisible({ timeout: 10000 });
  await expect(overlay).toContainText('mode:');

  // The overlay background is driven by theme.colors.background.secondary. Assert
  // it resolves to a real, non-transparent colour — proving the theme-aware styles
  // applied rather than the element rendering unstyled.
  const bg = await overlay.evaluate((el: Element) => getComputedStyle(el as HTMLElement).backgroundColor);
  expect(bg).toMatch(/^rgba?\(/);
  expect(bg).not.toBe('rgba(0, 0, 0, 0)');
  expect(bg).not.toBe('transparent');

  await page.screenshot({ path: `/tmp/tc4-theme-${theme}.png`, fullPage: false });
}

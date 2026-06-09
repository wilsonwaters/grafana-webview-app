import { expect } from './fixtures';

/**
 * Shared helpers for the TC4 e2e specs.
 *
 * `setVisualization` is known-flaky on cold start (the visualization picker can
 * be slow to register the nested panel). Retry with backoff so specs do not flake
 * for reasons unrelated to what they assert.
 */
export async function setWebViewVisualization(panelEditPage: any, page: any): Promise<void> {
  let lastErr: unknown;
  for (let i = 0; i < 4; i++) {
    try {
      // Cap each attempt so a hung setVisualization (it can occasionally stall for
      // a long time on a cold worker) is abandoned quickly and retried, rather than
      // consuming the whole per-test timeout in a single attempt.
      await Promise.race([
        (async () => {
          await panelEditPage.setVisualization('Web View');
          await expect(panelEditPage.getVisualizationName()).toHaveText('Web View');
        })(),
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

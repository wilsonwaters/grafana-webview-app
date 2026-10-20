/**
 * PC5 runtime e2e tests: auto-refresh and the debug overlay.
 *
 * These tests drive the real Grafana UI to verify that the PC5 view-mode
 * behaviours work end-to-end in a real browser.
 *
 * The visualization is selected with the shared `setWebViewVisualization`
 * helper, which handles the Grafana 12.4 picker behaviour and cold-start
 * slowness. Retries are also configured in playwright.config.ts (2 on CI).
 */
import { test, expect } from './fixtures';
import { setWebViewVisualization } from './tc4Helpers';

test.describe('PC5: View-mode behaviours', () => {
  test('debug overlay shows mode, X, Y, zoom when enabled', async ({ panelEditPage, page }) => {
    await setWebViewVisualization(panelEditPage, page);

    // Set a URL so the panel renders an iframe rather than the placeholder.
    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    // Confirm the overlay is NOT shown by default.
    await expect(
      panelEditPage.panel.locator.getByTestId('data-testid webview-panel-debug-overlay')
    ).not.toBeVisible();

    // Enable the debug overlay via the panel options switch.
    const overlaySwitch = page.getByRole('switch', { name: /show debug overlay/i });
    await overlaySwitch.check({ force: true });

    // The overlay should now be visible inside the panel.
    const overlay = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-debug-overlay');
    await expect(overlay).toBeVisible({ timeout: 5000 });

    // Verify it contains the expected fields (mode, X, Y, zoom).
    await expect(overlay).toContainText('mode:');
    await expect(overlay).toContainText('X:');
    await expect(overlay).toContainText('Y:');
    await expect(overlay).toContainText('zoom:');
  });

  test('auto-refresh: each tick mounts a new iframe element (a fresh load)', async ({ panelEditPage, page }) => {
    test.setTimeout(120000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    // Wait for the initial iframe to appear and mark the element. React never
    // removes an attribute it did not set, so the mark stays until the element
    // is replaced.
    const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(iframe).toBeVisible({ timeout: 10000 });
    await iframe.evaluate((el) => el.setAttribute('data-e2e-mark', 'first'));

    // Set the shortest refresh interval (the minimum is 5 seconds).
    const intervalInput = page.getByRole('spinbutton', { name: /auto-refresh interval/i });
    await intervalInput.fill('5');
    await page.keyboard.press('Tab');

    // A tick replaces the marked element with a new one.
    await expect(iframe).not.toHaveAttribute('data-e2e-mark', 'first', { timeout: 15000 });
    await expect(iframe).toHaveAttribute('src', 'https://example.com');
  });
});

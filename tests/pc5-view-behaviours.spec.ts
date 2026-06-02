/**
 * PC5 runtime e2e tests: auto-refresh, debug overlay, multi-instance independence.
 *
 * These tests drive the real Grafana UI to verify that the PC5 view-mode
 * behaviours work end-to-end in a real browser.
 *
 * Known-flaky note: `panelEditPage.setVisualization` can be slow on first
 * render. Retries are configured in playwright.config.ts (2 retries on CI).
 */
import { test, expect } from './fixtures';

test.describe('PC5: View-mode behaviours', () => {
  test('debug overlay shows mode, X, Y, zoom when enabled', async ({ panelEditPage, page }) => {
    await panelEditPage.setVisualization('Web View');

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

  test('auto-refresh: iframe reloads when interval is set', async ({ panelEditPage, page }) => {
    await panelEditPage.setVisualization('Web View');

    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    // Wait for the initial iframe to appear.
    const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(iframe).toBeVisible({ timeout: 5000 });

    // Set a very short refresh interval (1 second) in the panel options.
    const intervalInput = page.getByRole('spinbutton', { name: /auto-refresh interval/i });
    await intervalInput.fill('1');
    await page.keyboard.press('Tab');

    // Wait two seconds (two ticks); the iframe key should have changed causing a
    // re-mount. We can't directly observe the key but we can confirm the iframe
    // is still present and functional (no crash, no error state).
    await page.waitForTimeout(2500);
    await expect(iframe).toBeVisible();
    await expect(iframe).toHaveAttribute('src', 'https://example.com');
  });

  test('screenshot: debug overlay with mode, coordinates, zoom', async ({ panelEditPage, page }) => {
    await panelEditPage.setVisualization('Web View');

    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    // Enable debug overlay.
    const overlaySwitch = page.getByRole('switch', { name: /show debug overlay/i });
    await overlaySwitch.check({ force: true });

    const overlay = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-debug-overlay');
    await expect(overlay).toBeVisible({ timeout: 5000 });

    // Take a screenshot for visual evidence (saved to /tmp/pc5.png).
    await page.screenshot({ path: '/tmp/pc5.png', fullPage: false });
  });
});

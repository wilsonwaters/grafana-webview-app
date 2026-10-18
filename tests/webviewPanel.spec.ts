import { test, expect } from './fixtures';
import { setWebViewVisualization } from './tc4Helpers';

test.describe('Web View panel registration', () => {
  test('appears in the visualization picker and renders the placeholder', async ({ panelEditPage, page }) => {
    // Selecting the visualization by name confirms the nested panel is registered
    // and discoverable in the picker.
    await setWebViewVisualization(panelEditPage, page);
    await expect(panelEditPage.getVisualizationName()).toHaveText('Web View');

    // The placeholder component must render without error when no URL is configured.
    // Target the placeholder test id specifically — matching on visible text is
    // ambiguous because the panel title also contains "Web View panel".
    await expect(
      panelEditPage.panel.locator.getByTestId('data-testid webview-panel-placeholder')
    ).toBeVisible();
  });

  test('renders a sandboxed iframe with the configured URL', async ({ panelEditPage, page }) => {
    await setWebViewVisualization(panelEditPage, page);

    // Set the URL option so the panel renders an iframe in direct mode.
    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(iframe).toBeVisible();
    await expect(iframe).toHaveAttribute('src', 'https://example.com');
    // SECURITY: the iframe must use exactly this sandbox value — never broaden it.
    await expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');
  });
});

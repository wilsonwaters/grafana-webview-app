import { test, expect } from './fixtures';
import { webViewPanelTestIds } from '../src/panels/webview/components/testIds';
import { frameabilityEditorTestIds } from '../src/panels/webview/components/frameabilityEditorTestIds';

/**
 * RV1-6: a Test URL verdict applies only to the URL it was measured for.
 *
 * The dashboard below is saved the way panels were saved before
 * `detectedForUrl` existed: Auto mode with a stored `proxy` verdict and no
 * record of the tested URL. The panel migration records the saved URL on load,
 * so the dashboard keeps rendering through the proxy, and a later URL edit
 * without a new test makes the verdict stale (Auto then loads directly).
 *
 * example.com is allowlisted by provisioning/plugins/apps.yaml.
 */
const DASHBOARD_UID = 'rv2-legacy-detected-mode';
const PANEL_TITLE = 'Legacy auto proxy';

test.describe('Test URL verdict and the tested URL (RV1-6)', () => {
  test.beforeEach(async ({ page }) => {
    const res = await page.request.post('/api/dashboards/db', {
      data: {
        overwrite: true,
        dashboard: {
          uid: DASHBOARD_UID,
          title: 'RV2 legacy detectedMode',
          schemaVersion: 39,
          panels: [
            {
              id: 1,
              type: 'wilsonwaters-webview-panel',
              title: PANEL_TITLE,
              gridPos: { x: 0, y: 0, w: 12, h: 10 },
              options: { url: 'https://example.com', loadMode: 'auto', detectedMode: 'proxy' },
            },
          ],
        },
      },
    });
    expect(res.ok()).toBeTruthy();
  });

  test('a saved Auto panel keeps its proxy verdict; editing the URL without a new test switches it to direct', async ({
    gotoDashboardPage,
    gotoPanelEditPage,
    page,
  }) => {
    test.setTimeout(120000);

    // Existing dashboard: still rendered through the proxy (fetch + srcdoc).
    const dashboardPage = await gotoDashboardPage({ uid: DASHBOARD_UID });
    const viewFrame = dashboardPage.getPanelByTitle(PANEL_TITLE).locator.getByTestId(webViewPanelTestIds.iframe);
    await expect(viewFrame).toHaveAttribute('srcdoc', /Example Domain/, { timeout: 30000 });
    expect(await viewFrame.getAttribute('src')).toBeNull();

    // Edit the URL without testing it again: the verdict no longer applies.
    const panelEditPage = await gotoPanelEditPage({ dashboard: { uid: DASHBOARD_UID }, id: '1' });
    const urlField = page.getByRole('textbox', { name: 'URL' });
    await expect(urlField).toHaveValue('https://example.com', { timeout: 15000 });
    await urlField.fill('https://example.com/?rv2=edited');
    await page.keyboard.press('Tab');

    const editFrame = panelEditPage.panel.locator.getByTestId(webViewPanelTestIds.iframe);
    await expect(editFrame).toHaveAttribute('src', 'https://example.com/?rv2=edited', { timeout: 15000 });
    await expect(page.getByTestId(frameabilityEditorTestIds.untestedNote)).toBeVisible();
  });
});

import type { APIRequestContext, Page } from '@playwright/test';
import { DashboardPage } from '@grafana/plugin-e2e';
import { test, expect } from './fixtures';
import pluginJson from '../src/plugin.json';
import { testIds } from '../src/components/testIds';
import { frameabilityEditorTestIds } from '../src/panels/webview/components/frameabilityEditorTestIds';
import { setWebViewVisualization } from './tc4Helpers';

/**
 * App Configuration page (#129): the admin manages the allowlist and limits in
 * the UI, and the backend uses the saved settings without a Grafana restart.
 *
 * Shared state: other specs need the provisioned example.com entry and need
 * not-allowlisted.example.org to stay denied. This spec adds only UI_DOMAIN,
 * and removes it again before and after each test. Saving the page keeps every
 * other entry and jsonData key.
 */
const UI_DOMAIN = 'example.net';
const SETTINGS_PATH = `/api/plugins/${pluginJson.id}/settings`;

type StoredDomain = { domain?: string };

/** Removes `domain` from the stored allowlist through the settings API. Keeps everything else. */
async function removeDomainViaApi(request: APIRequestContext, domain: string): Promise<void> {
  const res = await request.get(SETTINGS_PATH);
  expect(res.ok()).toBeTruthy();
  const settings = await res.json();
  const jsonData = settings.jsonData ?? {};
  const stored: StoredDomain[] = Array.isArray(jsonData.allowedDomains) ? jsonData.allowedDomains : [];
  const kept = stored.filter((entry) => entry?.domain !== domain);
  if (kept.length === stored.length) {
    return;
  }
  const post = await request.post(SETTINGS_PATH, {
    data: { enabled: settings.enabled, pinned: settings.pinned, jsonData: { ...jsonData, allowedDomains: kept } },
  });
  expect(post.ok()).toBeTruthy();
}

async function domainValues(page: Page): Promise<string[]> {
  const form = page.getByTestId(testIds.appConfig.form);
  return form
    .getByRole('textbox', { name: /^Domain/ })
    .evaluateAll((els) => els.map((el) => (el as HTMLInputElement).value));
}

test.describe('App configuration page', () => {
  // The tests change shared plugin settings, so they must not overlap.
  test.describe.configure({ mode: 'serial' });

  test.beforeEach(async ({ page }) => {
    await removeDomainViaApi(page.request, UI_DOMAIN);
  });

  test.afterEach(async ({ page }) => {
    await removeDomainViaApi(page.request, UI_DOMAIN);
  });

  test('shows the allowlist editor and the global limits, without the scaffold API fields', async ({
    appConfigPage,
    page,
  }) => {
    const form = page.getByTestId(testIds.appConfig.form);
    await expect(form.getByRole('group', { name: 'Domain allowlist' })).toBeVisible();
    await expect(form.getByRole('group', { name: 'Global limits' })).toBeVisible();

    // The provisioned entry is shown (provisioning/plugins/apps.yaml).
    await expect.poll(() => domainValues(page)).toContain('example.com');

    await expect(form.getByRole('textbox', { name: /Request timeout/ })).toHaveAttribute('placeholder', '10 (default)');
    await expect(form.getByRole('button', { name: 'Save settings' })).toBeEnabled();
    await expect(page.getByRole('textbox', { name: /API Key/i })).toHaveCount(0);
    await expect(page.getByRole('textbox', { name: /API Url/i })).toHaveCount(0);
  });

  test('blocks saving invalid input and shows the errors', async ({ appConfigPage, page }) => {
    const settingsPosts: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'POST' && req.url().includes(SETTINGS_PATH)) {
        settingsPosts.push(req.url());
      }
    });

    await page.getByTestId(testIds.appConfig.addDomain).click();
    const entry = page.getByTestId(testIds.appConfig.domainEntry).last();
    const domain = entry.getByRole('textbox', { name: /^Domain/ });
    await expect(domain).toBeFocused();
    await domain.fill(`https://${UI_DOMAIN}/page`);
    await entry.getByRole('textbox', { name: /Additional ports/ }).fill('70000');

    await page.getByRole('button', { name: 'Save settings' }).click();

    await expect(entry.getByText(/Remove the scheme/)).toBeVisible();
    await expect(entry.getByText(/Port 70000 is out of range/)).toBeVisible();
    await expect(page.getByRole('alert', { name: 'The settings were not saved' })).toBeVisible();
    await expect(domain).toBeFocused();
    await expect(domain).toHaveAttribute('aria-invalid', 'true');
    expect(settingsPosts).toEqual([]);
  });

  test('an entry added in the UI persists after reload and Test URL succeeds for it', async ({
    appConfigPage,
    page,
    selectors,
    grafanaVersion,
    request,
  }, testInfo) => {
    test.setTimeout(180000);
    expect(await domainValues(page)).not.toContain(UI_DOMAIN);

    // 1. Add the entry and save.
    await page.getByTestId(testIds.appConfig.addDomain).click();
    const entry = page.getByTestId(testIds.appConfig.domainEntry).last();
    await entry.getByRole('textbox', { name: /^Domain/ }).fill(UI_DOMAIN);

    const saveResponse = page.waitForResponse(
      (res) => res.url().includes(SETTINGS_PATH) && res.request().method() === 'POST'
    );
    // After a successful save the page reloads itself.
    const reloaded = page.waitForEvent('load');
    await page.getByRole('button', { name: 'Save settings' }).click();
    const response = await saveResponse;
    expect(response.ok()).toBeTruthy();
    // The page saves only jsonData: no secrets, no scaffold keys.
    const sent = response.request().postDataJSON();
    expect(sent).not.toHaveProperty('secureJsonData');
    expect(sent.jsonData).not.toHaveProperty('apiUrl');
    await reloaded;

    // 2. Open the page again: the new entry and the provisioned entry are both stored.
    await appConfigPage.goto();
    await expect.poll(() => domainValues(page)).toEqual(expect.arrayContaining(['example.com', UI_DOMAIN]));

    // 3. Without a Grafana restart, the backend allows the new domain.
    const dashboardPage = new DashboardPage({ page, selectors, grafanaVersion, request, testInfo });
    await dashboardPage.goto();
    const panelEditPage = await dashboardPage.addPanel();
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill(`https://${UI_DOMAIN}`);
    await page.keyboard.press('Tab');
    const testButton = page.getByTestId(frameabilityEditorTestIds.testButton);
    await testButton.scrollIntoViewIfNeeded();
    await testButton.click();

    const result = page.getByTestId(frameabilityEditorTestIds.result);
    await expect(result).toBeVisible({ timeout: 20000 });
    // A 200 verdict, not the 403 allowlist denial. Direct or Proxied depends on the network.
    await expect(result).not.toContainText(/allowlist/i);
    await expect(result).toContainText(/Direct|Proxied/);
  });
});

import type { Locator, Page } from '@playwright/test';
import { test, expect } from './fixtures';
import { webViewPanelTestIds } from '../src/panels/webview/components/testIds';
import { viewportEditorTestIds } from '../src/panels/webview/components/viewportEditorTestIds';
import { setWebViewVisualization, FRAMABLE_URL } from './tc4Helpers';

/**
 * FR5 — in-panel proxy render (issue #102).
 *
 * Proxy mode fetches the rewritten HTML from the backend `/proxy` resource and
 * renders it with `<iframe srcdoc sandbox="allow-scripts">`. The resource route
 * is never an iframe `src`, because Grafana sends `X-Frame-Options: deny` and
 * `Content-Security-Policy: sandbox` on plugin resource responses.
 *
 * `example.com` is allowlisted by provisioning/plugins/apps.yaml. If the
 * instance has no such allowlist entry (for example after appConfig.spec.ts
 * saved the settings), the proxy answers 403 and the test skips itself rather
 * than flake.
 */

/** Selects "Proxy" in the Load mode radio group. */
async function selectProxyMode(page: Page): Promise<void> {
  const radioGroup = page.getByRole('radiogroup', { name: 'Load mode' });
  await radioGroup.scrollIntoViewIfNeeded();
  await expect(radioGroup.getByRole('radio', { name: 'Proxy' })).toBeVisible();
  // The group can re-layout while the editor re-renders; see tc4-config-flow.spec.ts.
  await radioGroup.getByText('Proxy', { exact: true }).click({ force: true });
}

/**
 * Waits for the proxied frame or the proxy error state inside `scope`. Skips
 * the test when the host is not allowlisted on this instance.
 */
async function waitForProxyFrame(scope: Locator, ids: { iframe: string; proxyError: string }): Promise<Locator> {
  const iframe = scope.getByTestId(ids.iframe);
  const error = scope.getByTestId(ids.proxyError);
  await expect(iframe.or(error)).toBeVisible({ timeout: 30000 });
  if (await error.isVisible()) {
    const text = (await error.textContent()) ?? '';
    test.skip(/allowlist/i.test(text), 'example.com is not allowlisted on this instance — provisioned in CI');
    throw new Error(`proxy render failed: ${text}`);
  }
  return iframe;
}

/** Asserts the iframe is a sandboxed srcdoc frame (never the resource route as src). */
async function expectSandboxedSrcdocFrame(iframe: Locator): Promise<void> {
  await expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
  await expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');
  await expect(iframe).toHaveAttribute('srcdoc', /Example Domain/);
  expect(await iframe.getAttribute('src')).toBeNull();
}

/**
 * Asserts that the framed document is the proxied example.com page. The page
 * has `<title>Example Domain</title>` (it has no heading).
 */
async function expectProxiedExampleDomain(iframe: Locator): Promise<void> {
  // toHaveText reads innerText, which is empty for <title>; compare textContent.
  await expect(iframe.contentFrame().locator('title')).toHaveJSProperty('textContent', 'Example Domain');
}

test.describe('FR5 proxy render (fetch then srcdoc)', () => {
  test('proxy mode renders the proxied page in the panel and the editor preview', async ({ panelEditPage, page }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill(FRAMABLE_URL);
    await page.keyboard.press('Tab');
    await selectProxyMode(page);

    // Panel: the proxied page renders inside a sandboxed srcdoc frame.
    const panelFrame = await waitForProxyFrame(panelEditPage.panel.locator, webViewPanelTestIds);
    await expectSandboxedSrcdocFrame(panelFrame);
    await expectProxiedExampleDomain(panelFrame);

    // The framed document runs in an opaque origin: no access to Grafana.
    const frame = await (await panelFrame.elementHandle())!.contentFrame();
    expect(frame).not.toBeNull();
    const isolation = await frame!.evaluate(() => {
      let parentReadable = true;
      try {
        void window.parent.document.title;
      } catch {
        parentReadable = false;
      }
      return { origin: self.origin, parentReadable };
    });
    expect(isolation).toEqual({ origin: 'null', parentReadable: false });

    // Editor preview: rendered the same way.
    const preview = page.getByTestId(viewportEditorTestIds.preview);
    await preview.scrollIntoViewIfNeeded();
    const previewFrame = await waitForProxyFrame(preview, viewportEditorTestIds);
    await expectSandboxedSrcdocFrame(previewFrame);
    await expectProxiedExampleDomain(previewFrame);
  });

  test('a non-allowlisted host shows the proxy error state and no iframe', async ({ panelEditPage, page }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill('https://not-allowlisted.example.org');
    await page.keyboard.press('Tab');
    await selectProxyMode(page);

    const error = panelEditPage.panel.locator.getByTestId(webViewPanelTestIds.proxyError);
    await expect(error).toBeVisible({ timeout: 30000 });
    await expect(error).toHaveAttribute('role', 'alert');
    await expect(error).toContainText('HTTP 403');
    await expect(error).toContainText(/allowlist/i);
    await expect(panelEditPage.panel.locator.locator('iframe')).toHaveCount(0);
  });
});

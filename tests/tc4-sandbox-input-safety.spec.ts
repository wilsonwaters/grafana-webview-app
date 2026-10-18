import { test, expect } from './fixtures';
import { webViewPanelTestIds } from '../src/panels/webview/components/testIds';
import { frameabilityEditorTestIds } from '../src/panels/webview/components/frameabilityEditorTestIds';
import { viewportEditorTestIds } from '../src/panels/webview/components/viewportEditorTestIds';
import { setWebViewVisualization, FRAMABLE_URL } from './tc4Helpers';

/**
 * TC4 — sandbox hardening (AC30, VR-2), URL safety (VR-1) and editor-input
 * safety (AC31).
 *
 * Covers:
 *   - AC 30 / VR-2: a direct-mode iframe for a cross-origin URL (example.com is
 *     cross-origin to Grafana) uses EXACTLY
 *     sandbox="allow-scripts allow-same-origin" (never broadened) and is
 *     non-interactive (pointer-events: none). The per-mode sandbox is computed:
 *     a proxy-mode iframe gets sandbox="allow-scripts" only (sandbox.test.ts,
 *     WebViewPanel.test.tsx).
 *   - VR-1: an unsafe URL (javascript:) renders an error message, never an
 *     iframe, in the panel and in the editor preview.
 *   - AC 31 (scoped): the AUTHORITATIVE CSS-selector injection guard is
 *     server-side (CR5, cascadia-validated, unit-tested in rewrite_cr5_test.go)
 *     and applies to PROXIED HTML. What is observable in the editor is that a
 *     malicious selector-like / markup value entered in a text field does not
 *     break the editor and is not reflected as raw markup into Grafana's page.
 *     v1 exposes no dedicated hideSelectors field, so the URL field is used as
 *     the attacker-controlled text input for this safety assertion.
 *
 * Theme coverage (AC35) lives in tc4-theme-light.spec.ts / tc4-theme-dark.spec.ts
 * (separate files because userPreferences.theme is a worker-scoped option).
 */

test.describe('TC4 sandbox + direct render (AC30)', () => {
  test('view-mode iframe is sandboxed exactly and non-interactive', async ({ panelEditPage, page }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill(FRAMABLE_URL);
    await page.keyboard.press('Tab');

    const iframe = panelEditPage.panel.locator.getByTestId(webViewPanelTestIds.iframe);
    await expect(iframe).toBeVisible({ timeout: 10000 });

    // Direct render of a framable URL.
    await expect(iframe).toHaveAttribute('src', FRAMABLE_URL);

    // AC30 / VR-2: a cross-origin direct URL gets EXACTLY this — never broadened
    // (no allow-popups, allow-top-navigation, allow-forms, allow-modals, etc.).
    await expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');

    // AC30: the iframe is non-interactive in view mode.
    const pointerEvents = await iframe.evaluate((el) => getComputedStyle(el as HTMLElement).pointerEvents);
    expect(pointerEvents).toBe('none');

    // Defence-in-depth: the referrer policy is locked down too.
    await expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');

    // The viewport transform is applied (DIRECT render with the transform — the
    // observable view-mode behaviour that does NOT require proxy render).
    const transform = await iframe.evaluate((el) => (el as HTMLElement).style.transform);
    expect(transform).toContain('scale');
    expect(transform).toContain('translate');
  });
});

test.describe('TC4 URL safety (VR-1)', () => {
  test('a javascript: URL renders an error and no iframe in the panel and the editor preview', async ({
    panelEditPage,
    page,
  }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    // A dialog would mean the script ran. Fail the test if one opens.
    let dialogOpened = false;
    page.on('dialog', async (dialog) => {
      dialogOpened = true;
      await dialog.dismiss();
    });

    await page.getByRole('textbox', { name: 'URL' }).fill('javascript:alert(document.domain)');
    await page.keyboard.press('Tab');

    // Panel: an accessible error message, no iframe.
    const panelError = panelEditPage.panel.locator.getByTestId(webViewPanelTestIds.invalidUrl);
    await expect(panelError).toBeVisible({ timeout: 10000 });
    await expect(panelError).toHaveAttribute('role', 'alert');
    await expect(panelEditPage.panel.locator.locator('iframe')).toHaveCount(0);

    // Editor preview: the same error, no iframe.
    const preview = page.getByTestId(viewportEditorTestIds.preview);
    await preview.scrollIntoViewIfNeeded();
    await expect(preview.getByTestId(viewportEditorTestIds.invalidUrl)).toBeVisible();
    await expect(preview.locator('iframe')).toHaveCount(0);

    expect(dialogOpened).toBe(false);
  });
});

test.describe('TC4 editor-input safety (AC31)', () => {
  // NOTE: the authoritative AC31 guard (CSS-selector injection) is enforced
  // server-side during proxied HTML rewriting (CR5, cascadia-validated and
  // unit-tested in pkg/.../rewrite_cr5_test.go). This e2e is scoped to what is
  // observable in the editor: a malicious selector-like / markup value entered
  // into a text field must NOT break the editor and must NOT be reflected as
  // raw markup into Grafana's page.
  test('malicious selector/markup in an editor field does not break the editor or inject markup', async ({
    panelEditPage,
    page,
  }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    // A value combining CSS-injection and HTML/script-injection probes. v1 has no
    // dedicated hideSelectors field, so the URL field is the author-controlled
    // text input we exercise.
    const malicious = '"><img src=x onerror=alert(1)>}*{display:none}</style><script>alert(2)</script>';

    const urlField = page.getByRole('textbox', { name: 'URL' });
    await urlField.fill(malicious);
    await page.keyboard.press('Tab');

    // The editor remains functional: the URL field still holds the raw value
    // (treated as data, not interpreted).
    await expect(urlField).toHaveValue(malicious);

    // VR-1: the value is not an absolute http(s) URL, so the panel shows an
    // error instead of an iframe.
    await expect(panelEditPage.panel.locator.getByTestId(webViewPanelTestIds.invalidUrl)).toBeVisible({
      timeout: 10000,
    });
    await expect(panelEditPage.panel.locator.locator('iframe')).toHaveCount(0);

    // No raw markup injection: neither the injected <img> nor <script> probe is a
    // live element anywhere in Grafana's page DOM.
    expect(await page.locator('img[src="x"]').count()).toBe(0);
    expect(await page.locator('script:has-text("alert(2)")').count()).toBe(0);

    // A sibling editor control (the viewport-editor preview) still renders,
    // proving the malicious value did not break the editor's React tree.
    const preview = page.getByTestId('data-testid webview-viewport-editor-preview');
    await preview.scrollIntoViewIfNeeded();
    await expect(preview).toBeVisible();

    // The Test URL button is still operable (the editor did not crash).
    const testButton = page.getByTestId(frameabilityEditorTestIds.testButton);
    await expect(testButton).toBeEnabled();
  });
});

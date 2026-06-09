import { test, expect } from './fixtures';
import { frameabilityEditorTestIds } from '../src/panels/webview/components/frameabilityEditorTestIds';
import { setWebViewVisualization } from './tc4Helpers';

/**
 * TC4 — config-flow e2e (AC 34).
 *
 * Drives the panel-editor configuration journey end-to-end against a live
 * Grafana:
 *   1. URL entry (the standard F4 "URL" field).
 *   2. The FR3 "Test URL" frameability button — BOTH the success path (an
 *      allowlisted host → a non-error verdict Alert is shown) AND the
 *      security-boundary error path (a non-allowlisted host → an Error Alert,
 *      because /check-frameable returns 403 with the fail-closed allowlist).
 *   3. Load-mode selection (Auto / Direct / Proxy radio group).
 *
 * SCOPE NOTE (FR5 deferred — issue #102): in-panel PROXY-mode render does NOT
 * work in v1 (Grafana resource-route XFO/CSP). These tests therefore never
 * assert that proxied content renders inside the panel; the config flow is fully
 * observable WITHOUT proxy render (button verdict Alert, radio selection, etc.).
 *
 * Determinism: the success path needs an allowlisted host. provisioning/plugins/
 * apps.yaml allowlists `example.com` so CI exercises the HTTP-200 verdict path.
 * Locally the verdict text (Direct vs Proxied) depends on the actual upstream
 * fetch (which may be sandboxed/MITM'd), so we assert only that a *non-error*
 * verdict Alert appears — never depending on the network reaching example.com.
 * If the live instance has an empty allowlist (host not allowlisted), the
 * success assertion is skipped rather than flaked; the denied path still runs.
 */

/** A host deliberately NOT in the allowlist — guarantees a 403 allowlist denial. */
const NON_ALLOWLISTED_URL = 'https://not-allowlisted.example.org';
/** The canonical framable host; allowlisted via provisioning for the success path. */
const ALLOWLISTED_URL = 'https://example.com';

test.describe('TC4 config flow (AC34)', () => {
  test('Test URL: non-allowlisted host shows the security-boundary Error state', async ({
    panelEditPage,
    page,
  }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    // 1. URL entry.
    await page.getByRole('textbox', { name: 'URL' }).fill(NON_ALLOWLISTED_URL);
    await page.keyboard.press('Tab');

    // 2. Click "Test URL". With the fail-closed allowlist, /check-frameable
    //    returns 403 ("target host is not allowlisted"), which the editor renders
    //    as an Error Alert. This is the AC "allowlist-denied URL returns error in
    //    the UI" without needing any proxy render.
    const testButton = page.getByTestId(frameabilityEditorTestIds.testButton);
    await testButton.scrollIntoViewIfNeeded();
    await expect(testButton).toBeEnabled();
    await testButton.click();

    const result = page.getByTestId(frameabilityEditorTestIds.result);
    await expect(result).toBeVisible({ timeout: 15000 });
    // The error Alert is titled "Error" and carries the server's denial reason.
    await expect(result).toContainText('Error');
    await expect(result).toContainText(/allowlist/i);
  });

  test('Test URL: allowlisted host shows a non-error verdict (success path)', async ({
    panelEditPage,
    page,
  }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill(ALLOWLISTED_URL);
    await page.keyboard.press('Tab');

    const testButton = page.getByTestId(frameabilityEditorTestIds.testButton);
    await testButton.scrollIntoViewIfNeeded();
    await testButton.click();

    const result = page.getByTestId(frameabilityEditorTestIds.result);
    await expect(result).toBeVisible({ timeout: 20000 });

    const text = (await result.textContent()) ?? '';
    // If the live instance has an empty allowlist (no provisioned allowedDomains),
    // the host is denied → "Error". That is the denied path, covered by the test
    // above; skip the success assertion here rather than flake.
    test.skip(/allowlist/i.test(text), 'host not allowlisted on this instance — success path provisioned in CI');

    // Success path: a 200 verdict was rendered. The verdict is either "Direct"
    // (frameable) or "Proxied" (blocked / upstream-unreachable). Both are
    // non-error success/info Alerts. We assert it is NOT the Error state and that
    // a recognised verdict title is shown — without depending on the network
    // reaching example.com (the verdict may be Proxied if the fetch is sandboxed).
    expect(text).not.toContain('allowlist');
    await expect(result).toContainText(/Direct|Proxied/);
  });

  test('Load mode selection: Auto / Direct / Proxy radio group is selectable', async ({
    panelEditPage,
    page,
  }) => {
    test.setTimeout(180000);
    await setWebViewVisualization(panelEditPage, page);

    await page.getByRole('textbox', { name: 'URL' }).fill(ALLOWLISTED_URL);
    await page.keyboard.press('Tab');

    // Locate the Load-mode control by role. NOTE: @grafana/ui's RadioButtonGroup
    // does not forward the `data-testid` prop to the rendered DOM, so we target it
    // by its accessible role/name (radiogroup labelled "Load mode") rather than by
    // testId. All three options (Auto/Direct/Proxy) render because the backend
    // probe reports available on this instance.
    const radioGroup = page.getByRole('radiogroup', { name: 'Load mode' });
    await radioGroup.scrollIntoViewIfNeeded();
    await expect(radioGroup).toBeVisible();
    await expect(radioGroup.getByRole('radio', { name: 'Auto' })).toBeVisible();
    await expect(radioGroup.getByRole('radio', { name: 'Direct' })).toBeVisible();
    await expect(radioGroup.getByRole('radio', { name: 'Proxy' })).toBeVisible();

    // Select "Direct". @grafana/ui's RadioButtonGroup renders each option as a
    // visible <label> wrapping a hidden native <input>; selection is driven by
    // clicking the label. We `force` the click because the group can briefly
    // re-layout as the editor re-renders, which otherwise trips Playwright's
    // actionability "stable" wait on the label.
    await radioGroup.getByText('Direct', { exact: true }).click({ force: true });

    // Assert the selection took effect END-TO-END (the authoritative signal):
    // Direct mode loads the raw URL into the panel iframe, no proxy. This is more
    // robust than probing the radio's internal checked/aria state, which differs
    // across @grafana/ui versions.
    const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(iframe).toBeVisible({ timeout: 10000 });
    await expect(iframe).toHaveAttribute('src', ALLOWLISTED_URL);
  });
});

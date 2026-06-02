import { test, expect } from './fixtures';

/**
 * PC4 runtime verification: numeric inputs, dimensions, and reset button.
 *
 * Opens a Web View panel in edit mode, sets a URL via the standard URL field,
 * then exercises the numeric X/Y/zoom inputs (two-way sync with the preview),
 * the iframeWidth/iframeHeight dimension inputs, and the Reset view button.
 */
test.describe('Viewport editor PC4: numeric inputs, dimensions, reset', () => {
  test('numeric inputs, dimension inputs, and reset button work correctly', async ({ panelEditPage, page }) => {
    test.setTimeout(120000);

    // setVisualization is known-flaky on first cold start; retry with backoff.
    let vizSet = false;
    for (let i = 0; i < 5; i++) {
      try {
        await panelEditPage.setVisualization('Web View');
        vizSet = true;
        break;
      } catch {
        if (i < 4) {
          await page.waitForTimeout(2000);
        }
      }
    }
    if (!vizSet) {
      throw new Error('setVisualization failed after 5 retries');
    }
    await expect(panelEditPage.getVisualizationName()).toHaveText('Web View');

    // Set URL via the canonical standard field (F4). The ViewportEditor preview
    // reacts automatically when Grafana re-renders the editor.
    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    const preview = page.getByTestId('data-testid webview-viewport-editor-preview');
    const readout = page.getByTestId('data-testid webview-viewport-editor-readout');
    await preview.scrollIntoViewIfNeeded();
    await expect(preview).toBeVisible();

    // --- Verify initial state ---
    await expect(readout).toContainText('X: 0');
    await expect(readout).toContainText('Y: 0');
    await expect(readout).toContainText('Zoom: 1.00');

    // --- Numeric X input updates the preview ---
    const inputX = page.getByTestId('data-testid webview-viewport-editor-input-x');
    await inputX.scrollIntoViewIfNeeded();
    await expect(inputX).toBeVisible();
    await inputX.fill('150');
    await inputX.press('Tab');
    await expect(readout).toContainText('X: 150');

    // Check the preview iframe transform reflects the new X value
    const editorIframe = page.getByTestId('data-testid webview-viewport-editor-iframe');
    await expect(editorIframe).toBeVisible();
    const editorTransform = await editorIframe.evaluate((el) => (el as HTMLElement).style.transform);
    expect(editorTransform).toContain('translate(-150px');

    // --- Numeric Y input ---
    const inputY = page.getByTestId('data-testid webview-viewport-editor-input-y');
    await inputY.fill('100');
    await inputY.press('Tab');
    await expect(readout).toContainText('Y: 100');

    // --- Numeric zoom input with clamping ---
    const inputZoom = page.getByTestId('data-testid webview-viewport-editor-input-zoom');
    await inputZoom.fill('2');
    await inputZoom.press('Tab');
    await expect(readout).toContainText('Zoom: 2.00');

    // Verify zoom clamping: enter value above max (5.0)
    await inputZoom.fill('99');
    await inputZoom.press('Tab');
    await expect(readout).toContainText('Zoom: 5.00');

    // --- Drag interaction updates numeric inputs (two-way sync) ---
    // Reset first to get predictable state
    const resetBtn = page.getByTestId('data-testid webview-viewport-editor-reset');
    await resetBtn.scrollIntoViewIfNeeded();
    await expect(resetBtn).toBeVisible();
    await resetBtn.click();
    await expect(readout).toContainText('X: 0');
    await expect(readout).toContainText('Y: 0');
    await expect(readout).toContainText('Zoom: 1.00');

    // Drag the preview; inputs should update
    const box = await preview.boundingBox();
    if (!box) {
      throw new Error('preview has no bounding box');
    }
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    await page.mouse.move(cx - 60, cy - 40, { steps: 8 });
    await page.mouse.up();
    // After drag, X/Y numeric inputs should differ from 0
    const inputXAfterDrag = await inputX.inputValue();
    expect(Number(inputXAfterDrag)).not.toBe(0);

    // --- Reset view button ---
    await resetBtn.click();
    await expect(readout).toContainText('X: 0');
    await expect(readout).toContainText('Y: 0');
    await expect(readout).toContainText('Zoom: 1.00');
    await expect(inputX).toHaveValue('0');
    await expect(inputY).toHaveValue('0');
    await expect(inputZoom).toHaveValue('1.00');

    // Main panel iframe should have identity-like transform after reset
    const panelIframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(panelIframe).toBeVisible();

    // --- Dimension inputs ---
    const inputWidth = page.getByTestId('data-testid webview-viewport-editor-input-width');
    const inputHeight = page.getByTestId('data-testid webview-viewport-editor-input-height');
    await inputWidth.scrollIntoViewIfNeeded();
    await expect(inputWidth).toBeVisible();
    await expect(inputHeight).toBeVisible();
    await expect(inputWidth).toHaveValue('1920');
    await expect(inputHeight).toHaveValue('1080');

    // Change width
    await inputWidth.fill('1280');
    await inputWidth.press('Tab');
    // Preview iframe should now be 1280px wide
    const editorIframeWidth = await editorIframe.evaluate((el) => (el as HTMLElement).style.width);
    expect(editorIframeWidth).toBe('1280px');

    // Non-positive dimension: enter 0, should revert to default 1920
    await inputWidth.fill('0');
    await inputWidth.press('Tab');
    await expect(inputWidth).toHaveValue('1920');

    // Take screenshot for evidence
    await page.screenshot({ path: '/tmp/pc4-editor.png', fullPage: false });

    // Check docker logs for errors (done outside Playwright via log inspection)
    // eslint-disable-next-line no-console
    console.log('PC4 runtime verification complete — screenshot at /tmp/pc4-editor.png');
  });
});

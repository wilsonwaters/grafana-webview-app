import { test, expect } from './fixtures';

/**
 * PC3 runtime verification: the interactive config-mode viewport editor.
 * Opens a Web View panel in edit mode, sets a URL, then drags and wheel-zooms
 * the preview and asserts the live readout changes (and thus the saved viewport
 * / main panel update, since the readout is driven by the same committed state).
 */
test.describe('Viewport editor (PC3)', () => {
  test('drag pans and wheel zooms the preview, updating the live readout', async ({ panelEditPage, page }) => {
    test.setTimeout(120000);

    // setVisualization is known-flaky; retry a few times.
    for (let i = 0; i < 3; i++) {
      try {
        await panelEditPage.setVisualization('Web View');
        break;
      } catch {
        if (i === 2) {
          throw new Error('setVisualization failed after retries');
        }
      }
    }
    await expect(panelEditPage.getVisualizationName()).toHaveText('Web View');

    await page.getByRole('textbox', { name: 'URL' }).fill('https://example.com');
    await page.keyboard.press('Tab');

    const preview = page.getByTestId('data-testid webview-viewport-editor-preview');
    const readout = page.getByTestId('data-testid webview-viewport-editor-readout');
    await preview.scrollIntoViewIfNeeded();
    await expect(preview).toBeVisible();
    await expect(readout).toContainText('X: 0');
    await expect(readout).toContainText('Zoom: 1.00');

    const box = await preview.boundingBox();
    if (!box) {
      throw new Error('preview has no bounding box');
    }
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;

    // --- Drag to pan ---
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    await page.mouse.move(cx - 90, cy - 50, { steps: 10 });
    await page.mouse.up();

    // Dragging content up-left increases the viewport offsets away from 0.
    await expect(readout).not.toContainText('X: 0');
    await expect(readout).not.toContainText('Y: 0');
    const afterDrag = await readout.textContent();

    // --- Wheel to zoom ---
    await page.mouse.move(cx, cy);
    await page.mouse.wheel(0, -360); // zoom in
    await expect(readout).not.toContainText('Zoom: 1.00');
    const afterZoom = await readout.textContent();

    // The main panel render reflects the saved viewport (same transform helper).
    const iframe = panelEditPage.panel.locator.getByTestId('data-testid webview-panel-iframe');
    await expect(iframe).toBeVisible();
    const transform = await iframe.evaluate((el) => (el as HTMLElement).style.transform);

    await page.screenshot({ path: '/tmp/pc3-editor.png', fullPage: false });

    // eslint-disable-next-line no-console
    console.log('PC3_READOUT_AFTER_DRAG=', afterDrag);
    // eslint-disable-next-line no-console
    console.log('PC3_READOUT_AFTER_ZOOM=', afterZoom);
    // eslint-disable-next-line no-console
    console.log('PC3_PANEL_TRANSFORM=', transform);

    // Panel transform must reflect a non-identity viewport (pan + zoom applied).
    expect(transform).not.toBe('scale(1) translate(0px, 0px)');
    expect(transform).not.toContain('scale(1)');
  });
});

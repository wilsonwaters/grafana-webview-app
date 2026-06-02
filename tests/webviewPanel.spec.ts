import { test, expect } from './fixtures';

test.describe('Web View panel registration', () => {
  test('appears in the visualization picker and renders the placeholder', async ({ panelEditPage }) => {
    // Selecting the visualization by name confirms the nested panel is registered
    // and discoverable in the picker.
    await panelEditPage.setVisualization('Web View');
    await expect(panelEditPage.getVisualizationName()).toHaveText('Web View');

    // The placeholder component must render without error when no URL is configured.
    await expect(panelEditPage.panel.locator.getByText(/Web View panel/)).toBeVisible();
  });
});

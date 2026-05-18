// Group: @messaging @featureA
//
// Messaging-feature spec — covers the three most common message-pane
// interactions. Default sort weight; runs in the middle of the queue.

import { test, expect } from '@playwright/test';

test.describe('Messaging', () => {
  test('opens the docs landing page', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page).toHaveTitle(/Playwright/);
  });

  test('finds the API reference link', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    const apiLink = page.getByRole('link', { name: /API/, exact: false }).first();
    await expect(apiLink).toBeVisible();
  });

  test('community footer is rendered', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page.locator('footer')).toBeVisible();
  });
});

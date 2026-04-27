// Group: @messaging @dm @featureA
//
// Direct-message spec.

import { test, expect } from '@playwright/test';

test.describe('Direct messages', () => {
  test('docs landing is reachable', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page).toHaveTitle(/Playwright/);
  });

  test('the docs include a navigation menu', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page.getByRole('navigation').first()).toBeVisible();
  });
});

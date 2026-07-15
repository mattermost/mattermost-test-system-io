// Group: @messaging @group @featureA
//
// Group-messaging spec.

import { test, expect } from '@playwright/test';

test.describe('Group messaging', () => {
  test('docs landing renders for a fresh worker', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page).toHaveTitle(/Playwright/);
  });

  test('can navigate from docs to community', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await page.getByRole('link', { name: 'Community' }).click();
    await expect(page).toHaveURL(/community/);
  });
});

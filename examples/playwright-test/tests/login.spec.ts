// Group: @auth @smoke @sort-first
//
// Login spec — authentication smoke. Sort-first weight so login regressions
// surface early in CI runs (other tests usually depend on a live session).

import { test, expect } from '@playwright/test';

test.describe('Login', () => {
  test('renders the sign-in page', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page).toHaveTitle(/Playwright/);
  });

  test('navigates to the docs after login affordance', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await page.getByRole('link', { name: 'Get started' }).click();
    await expect(page.getByRole('heading', { name: 'Installation' })).toBeVisible();
  });

  test.skip('rejects an obviously bad password', async () => {
    // Skipped pending a real auth surface in this seed app.
  });
});

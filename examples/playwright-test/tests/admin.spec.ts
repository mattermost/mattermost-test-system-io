// Group: @admin @slow @sort-last
//
// Admin spec — slow scenarios involving privileged routes and bulk
// operations. Sort-last weight so the queue's tail is dominated by the
// long-pole tests; faster workers drain shorter specs first.

import { test, expect } from '@playwright/test';

test.describe('Admin', () => {
  test('admin landing renders', async ({ page }) => {
    await page.goto('https://playwright.dev/docs/intro');
    await expect(page).toHaveTitle(/Installation|Playwright/);
  });

  test('can deep-link into the API reference', async ({ page }) => {
    await page.goto('https://playwright.dev/docs/api/class-test');
    await expect(page.getByRole('heading', { name: /Test/, level: 1 })).toBeVisible();
  });
});

// Group: @search @smoke
//
// Search spec — the global-search box. Default weight; smoke-tagged so it
// runs on every PR.

import { test, expect } from '@playwright/test';

test.describe('Search', () => {
  test('navigation includes search affordance', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    // The search trigger varies across releases; assert at least one search
    // landmark is present rather than pinning a specific selector.
    const searchHits = page.getByRole('search').or(page.locator('[aria-label*="search" i]'));
    await expect(searchHits.first()).toBeVisible();
  });

  test('docs site exposes navigation', async ({ page }) => {
    await page.goto('https://playwright.dev/');
    await expect(page.getByRole('navigation').first()).toBeVisible();
  });
});

import { defineConfig, devices } from '@playwright/test';

/**
 * Read environment variables from file.
 * https://github.com/motdotla/dotenv
 */
// import dotenv from 'dotenv';
// import path from 'path';
// dotenv.config({ path: path.resolve(__dirname, '.env') });

/**
 * See https://playwright.dev/docs/test-configuration.
 */
export default defineConfig({
  testDir: './tests',
  /* Run tests in files in parallel */
  fullyParallel: true,
  /* Fail the build on CI if you accidentally left test.only in the source code. */
  forbidOnly: !!process.env.CI,
  /*
   * Playwright internal retries. Kept at 0 here because the orchestration
   * demo provides its own retest mechanism at the lease level — re-dispatching
   * a failing dispatch unit to a worker — and stacking Playwright's
   * per-run retries on top of that multiplies attempt counts (one failing
   * test reported as `retries+1` entries per orchestration lease, so a
   * permanently-failing test under retries=1 with 4 leases shows up as
   * 8 attempts in the dashboard). Enable Playwright's retries only when
   * running outside the orchestration loop.
   */
  retries: 0,
  /* Opt out of parallel tests on CI. */
  workers: process.env.CI ? 1 : undefined,
  /*
   * Reporter to use. See https://playwright.dev/docs/test-reporters
   *
   * When the orchestration demo invokes Playwright it sets
   * `TSIO_REPORTER_OUTPUT` to the path of a per-spec results file. We then
   * layer a custom reporter that writes the richer JSON shape the demo
   * needs (per-test-case attachments + paths) on top of `list` (so the
   * terminal still shows progress) and the standard `html` report
   * (kept around for inspection). The built-in `json` reporter is appended
   * last so its per-test JSON output (written to
   * `process.env.PLAYWRIGHT_JSON_OUTPUT_NAME`) is available for the demo's
   * canonical reports upload chain. It is positioned LAST so its per-test
   * output does not conflict with the custom reporter above.
   */
  reporter: process.env.TSIO_REPORTER_OUTPUT
    ? [['list'], ['./reporters/tsio-reporter.ts'], ['html', { open: 'never' }], ['json']]
    : [['html'], ['list']],
  /*
   * Per-run output directory for screenshots, traces, videos, etc. The
   * orchestration demo pins a unique directory per spec invocation via
   * `TSIO_OUTPUT_DIR` so concurrent workers don't clobber each other's
   * artifacts.
   */
  outputDir: process.env.TSIO_OUTPUT_DIR || undefined,
  /*
   * Keep failure artifacts on disk after the run so the demo (or the
   * developer) can inspect or upload them later. This is the Playwright
   * default; we set it explicitly so the contract is visible in the config.
   */
  preserveOutput: 'always',
  /* Shared settings for all the projects below. See https://playwright.dev/docs/api/class-testoptions. */
  use: {
    /* Base URL to use in actions like `await page.goto('')`. */
    // baseURL: 'http://localhost:3000',

    /* Collect trace when retrying the failed test. See https://playwright.dev/docs/trace-viewer */
    trace: 'on-first-retry',

    /*
     * Capture a screenshot only when a test fails. The orchestration demo
     * walks per-test-case attachments and uploads any image attachments
     * to the orchestration screenshots endpoint.
     */
    screenshot: 'only-on-failure',
  },

  /* Configure projects for major browsers */
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },

    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },

    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
    },

    /* Test against mobile viewports. */
    // {
    //   name: 'Mobile Chrome',
    //   use: { ...devices['Pixel 5'] },
    // },
    // {
    //   name: 'Mobile Safari',
    //   use: { ...devices['iPhone 12'] },
    // },

    /* Test against branded browsers. */
    // {
    //   name: 'Microsoft Edge',
    //   use: { ...devices['Desktop Edge'], channel: 'msedge' },
    // },
    // {
    //   name: 'Google Chrome',
    //   use: { ...devices['Desktop Chrome'], channel: 'chrome' },
    // },
  ],

  /* Run your local dev server before starting the tests */
  // webServer: {
  //   command: 'npm run start',
  //   url: 'http://localhost:3000',
  //   reuseExistingServer: !process.env.CI,
  // },
});

// Group: @flaky @random
//
// Coin-flip spec for exercising the retest-on-fail dispatch path. Each
// invocation passes or fails with probability RANDOM_PASS_PROB (default
// 0.5), so a first-pass failure has a high chance of passing on the
// retest. Pair with `NUM_WORKERS=2 RETEST=1` to see the retest pipeline
// in motion — a single worker is excluded from re-leasing a unit it just
// failed, so retests need at least two workers in flight.
//
// Override the failure probability via the env var, e.g.
//   RANDOM_PASS_PROB=0.2 NUM_WORKERS=2 RETEST=1 \
//     node scripts/orchestration-demo.js
// to bias toward failures.

import { test, expect } from '@playwright/test';

const PASS_PROB = parseFloat(process.env.RANDOM_PASS_PROB ?? '0.5');

test.describe('Random outcome', () => {
  test('coin flip A', async () => {
    const passed = Math.random() < PASS_PROB;
    expect(passed, `simulated random failure (pass_prob=${PASS_PROB})`).toBe(true);
  });

  test('coin flip B', async () => {
    const passed = Math.random() < PASS_PROB;
    expect(passed, `simulated random failure (pass_prob=${PASS_PROB})`).toBe(true);
  });
});

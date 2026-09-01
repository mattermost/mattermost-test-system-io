// W10 gates: six banned-pattern fixtures each rejected with the rule named;
// three legitimate stabilization diffs pass; report-only never fails; product
// files outside the roots are never checked.
//
// node --test scripts/lib/stabilization-ban-checker.test.js

'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { checkStabilizationDiff } = require('./stabilization-ban-checker.js');

function diff(path, added, removed = []) {
  const body = [
    ...removed.map((l) => `-${l}`),
    ...added.map((l) => `+${l}`),
  ].join('\n');
  return `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1,3 +1,3 @@\n${body}\n`;
}

// --- the six banned patterns ---

test('W10: bare wait rejected', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/a.spec.ts', [
    'await page.waitForTimeout(3000);',
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-bare-wait'));
});

test('W10: retry wrapper rejected', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/cypress/tests/integration/b_spec.js', [
    "cy.get('.x').should('be.visible') // flaky",
    'Cypress.Commands.add("retry", () => {})',
    'retries: 2',
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-retry-wrapper'));
});

test('W10: loosened assertion rejected (weak matcher in, strict out)', () => {
  const r = checkStabilizationDiff(
    diff('e2e-tests/playwright/specs/c.spec.ts', ['expect(status).toBeTruthy();'], [
      "expect(status).toBe('ready');",
    ]),
  );
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-loosened-assertion'));
});

test('W10: soft assertion rejected on its own', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/d.spec.ts', [
    'await expect.soft(page.locator(".badge")).toHaveText("2");',
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-loosened-assertion'));
});

test('W10: deleted assertion rejected', () => {
  const r = checkStabilizationDiff(
    diff('e2e-tests/cypress/tests/integration/e_spec.js', [
      "// removed the flaky assertion for now",
    ], [
      "cy.get('.sidebar').should('be.visible');",
      "expect(resp.status).to.eq(200);",
    ]),
  );
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-deleted-assertion'));
});

test('W10: skip tag rejected', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/f.spec.ts', [
    "test.skip('login flow', () => {});",
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-skip-tag'));
});

test('W10: raised timeout rejected in a spec', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/g.spec.ts', [
    "test('slow', async ({ page }) => { });",
    'test.setTimeout(60000);',
    'page.setDefaultTimeout(30000);',
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-raised-timeout'));
});

test('W10: raised timeout rejected in a config (global effect)', () => {
  const r = checkStabilizationDiff(diff('playwright.config.ts', [
    'use: { actionTimeout: 30000 }',
    'timeout: 120000',
  ]));
  assert.equal(r.passed, false);
  assert.ok(r.violations.some((v) => v.rule === 'ban-raised-timeout'));
});

// --- legitimate stabilization diffs must pass (false-positive guards) ---

test('W10 legitimate: fixed wait replaced with a polling assertion', () => {
  const r = checkStabilizationDiff(
    diff('e2e-tests/playwright/specs/login.spec.ts', [
      "await expect(page.locator('.dashboard')).toBeVisible({ timeout: 10000 });",
    ], [
      'await page.waitForTimeout(5000);',
    ]),
  );
  assert.equal(r.passed, true, JSON.stringify(r.violations));
});

test('W10 legitimate: added per-test cleanup (isolation fix)', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/state.spec.ts', [
    'beforeEach(() => { localStorage.clear(); });',
    'afterEach(async () => { await page.close(); });',
  ]));
  assert.equal(r.passed, true, JSON.stringify(r.violations));
});

test('W10 legitimate: fake timers for date-dependent flake', () => {
  const r = checkStabilizationDiff(diff('e2e-tests/playwright/specs/clock.spec.ts', [
    "await page.clock.setFixedTime(new Date('2026-01-15T12:00:00Z'));",
    'await page.clock.install();',
  ]));
  assert.equal(r.passed, true, JSON.stringify(r.violations));
});

test('W10 legitimate: deleting a duplicate flaky spec outright', () => {
  const r = checkStabilizationDiff(
    diff('e2e-tests/cypress/tests/integration/dup_spec.js', [], [
      "it('flaky duplicate', () => {});",
      "cy.get('.x').should('exist');",
    ]),
  );
  assert.equal(r.passed, true, JSON.stringify(r.violations)); // no added lines → not checked
});

// --- scope + mode rules ---

test('W10: product files outside the roots are never checked', () => {
  const r = checkStabilizationDiff(diff('webapp/src/components/x.tsx', [
    'await page.waitForTimeout(9999);',
    'test.skip();',
  ]));
  assert.equal(r.passed, true);
  assert.equal(r.skippedFiles, 1);
  assert.equal(r.checkedFiles, 0);
});

test('W10: report-only mode reports violations but never fails', () => {
  const r = checkStabilizationDiff(
    diff('e2e-tests/playwright/specs/bad.spec.ts', ['await page.waitForTimeout(2000);']),
    { reportOnly: true },
  );
  assert.equal(r.violations.length, 1);
  assert.equal(r.passed, true); // the point of report-only (W16 item 9)
});
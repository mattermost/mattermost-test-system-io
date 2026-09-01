import { test } from "node:test";
import * as assert from "node:assert/strict";
import { guardEditable, clampConcurrency, attemptsExhausted } from "./rails.ts";

test("guardEditable accepts e2e-tests paths only", () => {
  const ws = "/tmp/ws";
  assert.equal(guardEditable(ws, "e2e-tests/playwright/specs/a.spec.ts"), "/tmp/ws/e2e-tests/playwright/specs/a.spec.ts");
});

test("guardEditable rejects product code, config, and escapes", () => {
  assert.throws(() => guardEditable("/tmp/ws", "webapp/src/x.tsx"));
  assert.throws(() => guardEditable("/tmp/ws", "playwright.config.ts"));
  assert.throws(() => guardEditable("/tmp/ws", "../outside.ts"));
  assert.throws(() => guardEditable("/tmp/ws", "e2e-tests/../webapp/x.ts"));
});

test("clampConcurrency caps at the hard limit 5", () => {
  assert.equal(clampConcurrency(99), 5);
  assert.equal(clampConcurrency(2), 2);
  assert.equal(clampConcurrency(0), 1);
  assert.equal(clampConcurrency(-3), 1);
});

test("attemptsExhausted is inclusive", () => {
  assert.equal(attemptsExhausted(2, 3), false);
  assert.equal(attemptsExhausted(3, 3), true);
});

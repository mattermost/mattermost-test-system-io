import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  deadlineAtFrom,
  deadlineReached,
  parseDeadlineMinutes,
  specTimeoutMs,
  unitsLeftOf,
} from "./deadline.ts";

test("parseDeadlineMinutes: empty input turns the deadline off", () => {
  assert.equal(parseDeadlineMinutes(""), null);
  assert.equal(parseDeadlineMinutes("   "), null);
});

test("parseDeadlineMinutes: a positive integer is the minute count", () => {
  assert.equal(parseDeadlineMinutes("20"), 20);
  assert.equal(parseDeadlineMinutes(" 20 "), 20);
});

test("parseDeadlineMinutes: zero, negatives, fractions and words are rejected", () => {
  for (const bad of ["0", "-5", "1.5", "twenty"]) {
    assert.throws(() => parseDeadlineMinutes(bad), /deadline-minutes must be a positive integer/);
  }
});

test("deadlineAtFrom: off stays null, on is start plus minutes", () => {
  assert.equal(deadlineAtFrom(1_000, null), null);
  assert.equal(deadlineAtFrom(1_000, 2), 1_000 + 120_000);
});

test("deadlineReached: never when off, at and after the instant when on", () => {
  assert.equal(deadlineReached(null, Number.MAX_SAFE_INTEGER), false);
  assert.equal(deadlineReached(500, 499), false);
  assert.equal(deadlineReached(500, 500), true);
  assert.equal(deadlineReached(500, 900), true);
});

test("specTimeoutMs: the kill timer is the time left, never zero", () => {
  assert.equal(specTimeoutMs(null, 0), undefined);
  assert.equal(specTimeoutMs(10_000, 4_000), 6_000);
  assert.equal(specTimeoutMs(10_000, 10_000), 1);
  assert.equal(specTimeoutMs(10_000, 12_000), 1);
});

test("unitsLeftOf: counts pending and retest-eligible units, not leased ones", () => {
  assert.equal(unitsLeftOf({ pending: 7, retest_eligible: 2 }), 9);
});

import { test } from "node:test";
import * as assert from "node:assert/strict";
import { monthlyQuery, monthlyAttemptsUsed, withinBudget, budgetStopNotice } from "./budget.ts";

test("monthlyQuery scopes to this month + label + branch prefix", () => {
  const now = new Date("2026-09-15T10:00:00Z");
  const q = monthlyQuery("mattermost/mattermost", now);
  assert.match(q, /repo:mattermost\/mattermost/);
  assert.match(q, /label:"e2e-stabilization"/);
  // Counted by label, not head: — head-search semantics are undocumented and
  // branch names append dates (Opus minor 17).
  assert.match(q, /created:>=2026-09-01/);
});

test("monthlyAttemptsUsed reads total_count", async () => {
  const used = await monthlyAttemptsUsed(async () => ({ total_count: 7 }), "r");
  assert.equal(used, 7);
});

test("withinBudget is exclusive at the cap; stop notice names the numbers", () => {
  assert.equal(withinBudget(19, 20), true);
  assert.equal(withinBudget(20, 20), false);
  assert.match(budgetStopNotice(20, 20), /20\/20/);
});

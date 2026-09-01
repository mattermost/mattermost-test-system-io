import { test } from "node:test";
import * as assert from "node:assert/strict";
import { runLoop, type LoopDeps } from "./loop.ts";
import type { QueueEntry } from "./types.ts";

const BASE = { baseURL: "https://x", repo: "mattermost/mattermost", depth: 10, concurrency: 2, maxAttemptsPerTest: 3, monthlyBudget: 20, dryRun: false };

function entry(testID: string, promoted = false): QueueEntry {
  return { test_id: testID, promoted };
}

function deps(over: Partial<LoopDeps> = {}): LoopDeps {
  return {
    async fetchQueue() { return { promoted: [], ranked: [entry("MM-T1")] }; },
    async monthlyAttemptsUsed() { return 0; },
    async openPRCount() { return 0; },
    async attemptsForTest() { return 0; },
    async repair() { return { summary: "timing race; polling assertion", editedFiles: ["e2e-tests/a.spec.ts"], routed: false }; },
    async selfCheck() { return { passed: true, violations: [] }; },
    async openPR() { return { branch: "stabilization/mm-t1", prNumber: 42 }; },
    async routeToOwner() { return "test-infra"; },
    async recordAttempt() {},
    ...over,
  };
}

test("happy path: budget ok, repair passes self-check, PR opened, attempt recorded", async () => {
  const recorded: string[] = [];
  const d = deps({ async recordAttempt(_t, n, outcome) { recorded.push(`${n}:${outcome}`); } });
  const actions = await runLoop(d, BASE);
  assert.equal(actions.length, 1);
  assert.equal(actions[0]!.kind, "fix_pr");
  assert.deepEqual(recorded, ["1:pr_opened"]);
});

test("budget exhausted stops before touching the queue", async () => {
  let queueTouched = false;
  const d = deps({
    async monthlyAttemptsUsed() { return 20; },
    async fetchQueue() { queueTouched = true; return { promoted: [], ranked: [] }; },
  });
  const actions = await runLoop(d, BASE);
  assert.equal(actions[0]!.kind, "budget_exhausted");
  assert.equal(queueTouched, false);
});

test("zero free concurrency slots skips everything", async () => {
  const d = deps({ async openPRCount() { return 2; } });
  const actions = await runLoop(d, BASE);
  assert.equal(actions[0]!.kind, "skipped");
});

test("attempts cap escalates with a diagnosis instead of a 4th PR", async () => {
  const d = deps({ async attemptsForTest() { return 3; } });
  const actions = await runLoop(d, BASE);
  const a = actions[0]!;
  assert.equal(a.kind, "attempts_exhausted");
  if (a.kind === "attempts_exhausted") assert.match(a.diagnosis, /needs a human/);
});

test("product-bug diagnosis routes, never fixes", async () => {
  const d = deps({
    async repair() { return { summary: "product bug", editedFiles: [], routed: true, routingReason: "wrong product state" }; },
  });
  const actions = await runLoop(d, BASE);
  const a = actions[0]!;
  assert.equal(a.kind, "routed");
  if (a.kind === "routed") assert.match(a.reason, /wrong product state/);
});

test("self-check rejection stops the push and records the rejection", async () => {
  const recorded: string[] = [];
  const d = deps({
    async selfCheck() {
      return { passed: false, violations: [{ rule: "ban-bare-wait", file: "e2e-tests/a.spec.ts", message: "x" }] };
    },
    async recordAttempt(_t, n, outcome) { recorded.push(`${n}:${outcome}`); },
  });
  const actions = await runLoop(d, BASE);
  assert.equal(actions[0]!.kind, "skipped");
  assert.match(actions[0]!.reason, /ban-bare-wait/);
  assert.deepEqual(recorded, ["1:rejected_by_self_check"]);
});

test("dry-run investigates but opens nothing", async () => {
  let opened = false;
  const d = deps({ async openPR() { opened = true; return { branch: "b", prNumber: 1 }; } });
  const actions = await runLoop(d, { ...BASE, dryRun: true });
  assert.equal(opened, false);
  assert.equal(actions[0]!.kind, "skipped");
  assert.match(actions[0]!.reason, /dry-run/);
});

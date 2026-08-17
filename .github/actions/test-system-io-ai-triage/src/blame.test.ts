import { test } from "node:test";
import * as assert from "node:assert/strict";
import { attribute, finishBlame, kindOf, resolveSuspectRange } from "./blame.ts";

test("kindOf: flake vs bug vs unknown", () => {
  assert.equal(kindOf("FLAKY_TEST"), "flaky");
  assert.equal(kindOf("FLAKY_INFRA"), "flaky");
  assert.equal(kindOf("PR_REGRESSION"), "bug");
  assert.equal(kindOf("MAIN_REGRESSION"), "bug");
  assert.equal(kindOf("INCONCLUSIVE"), "unknown");
});

test("resolveSuspectRange needs last pass and failing since", () => {
  assert.equal(resolveSuspectRange(undefined).resolvable, false);
  assert.equal(
    resolveSuspectRange({
      runs: 4,
      passed: 0,
      failed: 4,
      flaky: 0,
      skipped: 0,
      flips: 0,
      failure_rate: 1,
      flake_rate: 0,
      failing_since_commit: "aaa",
      series: [],
    }).resolvable,
    false,
  );
  const r = resolveSuspectRange({
    runs: 8,
    passed: 4,
    failed: 4,
    flaky: 0,
    skipped: 0,
    flips: 1,
    failure_rate: 0.5,
    flake_rate: 0,
    last_pass_commit: "passsha",
    failing_since_commit: "failsha",
    series: [],
  });
  assert.equal(r.resolvable, true);
  assert.equal(r.lastPass, "passsha");
});

test("attribute names the author when exactly one non-merge commit", () => {
  const a = attribute([
    {
      sha: "abc1234deadbeef",
      parents: [{ sha: "parent" }],
      author: { login: "alice" },
      commit: { message: "break the button\n\nbody" },
    },
  ]);
  assert.equal(a.confident, true);
  assert.equal(a.commits[0]!.author, "alice");
  assert.equal(a.commits[0]!.message, "break the button");
});

test("attribute drops merge commits and refuses a wide range", () => {
  const merges = attribute([{ sha: "m", parents: [{}, {}], commit: { message: "Merge" } }]);
  assert.equal(merges.confident, false);

  const many = attribute(
    Array.from({ length: 12 }, (_, i) => ({
      sha: `sha${i}`,
      parents: [{}],
      author: { login: `u${i}` },
      commit: { message: `c${i}` },
    })),
  );
  assert.equal(many.confident, false);
  assert.equal(many.commits.length, 8);
});

test("finishBlame names the author for a single-commit bug", () => {
  const b = finishBlame({
    verdict: "PR_REGRESSION",
    history: {
      runs: 8,
      passed: 7,
      failed: 1,
      flaky: 0,
      skipped: 0,
      flips: 1,
      failure_rate: 0.12,
      flake_rate: 0,
      last_pass_commit: "p",
      failing_since_commit: "f",
      series: [],
    },
    attributed: {
      confident: true,
      reason: "exactly one commit landed between the last pass and the first failure",
      commits: [{ sha: "abc1234deadbeef", author: "alice", message: "break the button" }],
    },
  });
  assert.equal(b.kind, "bug");
  assert.equal(b.confident, true);
  assert.equal(b.suspect?.author, "alice");
  assert.equal(b.suspect?.sha, "abc1234deadbeef");
});

test("finishBlame does not name an author for a flake", () => {
  const b = finishBlame({
    verdict: "FLAKY_TEST",
    history: {
      runs: 8,
      passed: 4,
      failed: 4,
      flaky: 0,
      skipped: 0,
      flips: 3,
      failure_rate: 0.5,
      flake_rate: 0,
      last_pass_commit: "p",
      failing_since_commit: "f",
      series: [],
    },
  });
  assert.equal(b.kind, "flaky");
  assert.equal(b.suspect, undefined);
});

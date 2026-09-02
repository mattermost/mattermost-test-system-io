/**
 * pr.ts against a REAL git repo + bare remote (round-3 task 6). The two
 * round-2 blockers both lived in pr.ts, which had no test file, and
 * loop.test.ts stubbed resetWorkspace to a no-op — so the git sequence that
 * produced those blockers was never executed. These tests run the actual
 * gitDeps/resetWorkspace/stageEdits/commitAndPush against a temp repo.
 */
import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { execFileSync } from "node:child_process";
import {
  gitDeps,
  resetWorkspace,
  stageEdits,
  commitAndPush,
  branchName,
  openStabilizationPR,
  attemptsForTest,
  type GitDeps,
} from "./pr.ts";

function sh(cwd: string, args: string[]): string {
  return execFileSync("git", ["-C", cwd, ...args], { encoding: "utf8" });
}

/** Create a temp local repo + bare remote, both on `master`, with e2e-tests/. */
function setupRepo(): { dir: string; remote: string; git: GitDeps } {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tsio-pr-"));
  const remote = path.join(root, "remote.git");
  const dir = path.join(root, "local");
  fs.mkdirSync(remote);
  fs.mkdirSync(dir);

  sh(remote, ["init", "--bare", "-b", "master"]);
  sh(dir, ["init", "-b", "master"]);
  sh(dir, ["config", "user.email", "test@example.com"]);
  sh(dir, ["config", "user.name", "test"]);
  fs.mkdirSync(path.join(dir, "e2e-tests"));
  fs.writeFileSync(path.join(dir, "README.md"), "base\n");
  fs.writeFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "original\n");
  sh(dir, ["add", "."]);
  sh(dir, ["commit", "-m", "initial", "--no-verify"]);
  sh(dir, ["remote", "add", "origin", remote]);
  sh(dir, ["push", "origin", "master"]);

  return { dir, remote, git: gitDeps(dir) };
}

function log(dir: string, ref: string): string[] {
  return sh(dir, ["log", "--format=%s", ref]).trim().split("\n").filter(Boolean);
}

test("two entries editing the same file produce two independent single-commit branches", () => {
  const { dir, git } = setupRepo();

  // Entry 1.
  resetWorkspace(git, "master");
  fs.writeFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "entry 1\n");
  stageEdits(git);
  commitAndPush(git, "stabilization/t1", "entry 1");

  // Entry 2 — must branch off a pristine master, not entry 1's branch.
  resetWorkspace(git, "master");
  fs.writeFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "entry 2\n");
  stageEdits(git);
  commitAndPush(git, "stabilization/t2", "entry 2");

  const t1 = log(dir, "stabilization/t1");
  const t2 = log(dir, "stabilization/t2");
  // Each branch has exactly the initial commit + its own single commit.
  assert.equal(t1.length, 2, `t1 log = ${t1.join(", ")}`);
  assert.equal(t2.length, 2, `t2 log = ${t2.join(", ")}`);
  assert.equal(t1[0], "entry 1");
  assert.equal(t2[0], "entry 2");
  // Neither branch contains the other's commit.
  assert.ok(!t1.includes("entry 2"), "t1 must not contain entry 2's commit");
  assert.ok(!t2.includes("entry 1"), "t2 must not contain entry 1's commit");
  // The file content on each branch is independent.
  assert.equal(sh(dir, ["show", "stabilization/t1:e2e-tests/a.spec.ts"]), "entry 1\n");
  assert.equal(sh(dir, ["show", "stabilization/t2:e2e-tests/a.spec.ts"]), "entry 2\n");
});

test("a self-check rejection on entry 1 does not poison entry 2 (staged index cleared)", () => {
  const { dir, git } = setupRepo();

  // Entry 1: a banned edit is staged, then rejected — the staged edit must not
  // survive into entry 2 (round-2 major 3).
  resetWorkspace(git, "master");
  fs.writeFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "banned edit\n");
  stageEdits(git);
  assert.notEqual(sh(dir, ["diff", "--cached", "--name-only"]).trim(), "");

  // Entry 2's reset must clear the staged index.
  resetWorkspace(git, "master");
  assert.equal(sh(dir, ["diff", "--cached", "--name-only"]).trim(), "", "staged edit leaked into entry 2");
  assert.equal(fs.readFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "utf8"), "original\n");
});

test("resetWorkspace preserves untracked non-ignored files outside e2e-tests/", () => {
  const { dir, git } = setupRepo();

  // A composite job's downloaded artifact / generated fixture, outside the
  // loop's edit root, must survive the reset (round-3 major 2).
  fs.writeFileSync(path.join(dir, "fixture.txt"), "downloaded artifact\n");
  // An uncommitted edit to a tracked file outside e2e-tests/ must survive too.
  fs.writeFileSync(path.join(dir, "README.md"), "edited by a prior step\n");

  resetWorkspace(git, "master");

  assert.equal(fs.readFileSync(path.join(dir, "fixture.txt"), "utf8"), "downloaded artifact\n");
  assert.equal(fs.readFileSync(path.join(dir, "README.md"), "utf8"), "edited by a prior step\n");
});

test("resetWorkspace still discards e2e-tests/ residue (tracked + untracked)", () => {
  const { dir, git } = setupRepo();

  fs.writeFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "dirty edit\n");
  fs.writeFileSync(path.join(dir, "e2e-tests", "new.spec.ts"), "untracked\n");

  resetWorkspace(git, "master");

  assert.equal(fs.readFileSync(path.join(dir, "e2e-tests", "a.spec.ts"), "utf8"), "original\n");
  assert.ok(!fs.existsSync(path.join(dir, "e2e-tests", "new.spec.ts")), "untracked e2e-tests file survived");
});

test("a missing origin/<base> produces a clear error, not a mid-loop crash", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tsio-pr-noorigin-"));
  const dir = path.join(root, "local");
  fs.mkdirSync(dir);
  sh(dir, ["init", "-b", "master"]);
  sh(dir, ["config", "user.email", "test@example.com"]);
  sh(dir, ["config", "user.name", "test"]);
  fs.writeFileSync(path.join(dir, "README.md"), "x\n");
  sh(dir, ["add", "."]);
  sh(dir, ["commit", "-m", "initial", "--no-verify"]);
  // No `origin` remote at all.

  const git = gitDeps(dir);
  assert.throws(
    () => resetWorkspace(git, "master"),
    /workspace reset failed \(is origin\/master available\?\)/,
  );
});

test("branchName slugs the test id and appends a date", () => {
  const name = branchName("MM-T1234", new Date("2026-09-02T00:00:00Z"));
  assert.equal(name, "stabilization/mm-t1234-2026-09-02");
});

test("openStabilizationPR posts the PR and labels it", async () => {
  const calls: Array<[string, string, unknown]> = [];
  const api = async (method: string, p: string, body?: unknown) => {
    calls.push([method, p, body]);
    if (p === "/repos/r/pulls") return { number: 7, html_url: "https://x/7" };
    return {};
  };
  const n = await openStabilizationPR(api as never, "r", "b", "t", "body", "e2e-stabilization", "master");
  assert.equal(n, 7);
  assert.equal(calls[0]![0], "POST");
  assert.equal(calls[0]![1], "/repos/r/pulls");
  assert.equal(calls[1]![1], "/repos/r/issues/7/labels");
});

test("attemptsForTest counts prior loop PRs by title", async () => {
  const api = async () => ({ total_count: 3 });
  const n = await attemptsForTest(api as never, "r", "MM-T1");
  assert.equal(n, 3);
});

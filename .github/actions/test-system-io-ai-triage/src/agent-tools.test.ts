/**
 * Round-6 Task 1: the two new evidence tools. Verifies (a) get_pr_diff and
 * get_test_source are registered in the tool schema the model sees, and
 * (b) the shipped fetch+cap path in main.ts — a huge PR diff is truncated to
 * 200KB with its changed-file paths preserved, and a huge spec is truncated
 * to 100KB. Only globalThis.fetch is stubbed; the cap logic executes.
 */
import { test, mock } from "node:test";
import * as assert from "node:assert/strict";

mock.module("@actions/core", {
  exports: {
    getInput: () => "",
    getIDToken: async () => "bearer",
    setOutput: () => {},
    setFailed: () => {},
    info: () => {},
    warning: () => {},
    notice: () => {},
    setSecret: () => {},
  },
});

const { TOOLS } = await import("./agent.ts");
const { getPrDiff, getTestSource } = await import("./main.ts");

test("TOOLS registers get_pr_diff and get_test_source", () => {
  const names = TOOLS.map((t) => t.name);
  assert.ok(names.includes("get_pr_diff"), "get_pr_diff must be a callable tool");
  assert.ok(names.includes("get_test_source"), "get_test_source must be a callable tool");
  const diff = TOOLS.find((t) => t.name === "get_pr_diff")!;
  assert.match(diff.description, /MANDATORY before any FLAKY_\*/);
});

test("getPrDiff returns the diff unchanged when under the cap", async () => {
  const diff =
    "diff --git a/.github/workflows/e2e.yml b/.github/workflows/e2e.yml\n+testcontainers\n";
  globalThis.fetch = (async () => new Response(diff, { status: 200 })) as typeof fetch;
  const out = await getPrDiff("tok", "mattermost/mattermost", 123);
  assert.equal(out, diff);
});

test("getPrDiff truncates a huge diff to 200KB and preserves changed-file paths", async () => {
  const big =
    "diff --git a/.github/workflows/e2e.yml b/.github/workflows/e2e.yml\n" + "x".repeat(300 * 1024);
  globalThis.fetch = (async () => new Response(big, { status: 200 })) as typeof fetch;
  const out = await getPrDiff("tok", "mattermost/mattermost", 123);
  assert.ok(out.includes("truncated"), "must note the truncation");
  assert.ok(out.includes(".github/workflows/e2e.yml"), "must preserve the changed-file path");
  assert.ok(Buffer.byteLength(out) <= 200 * 1024 + 4096, "must stay near the 200KB cap");
});

test("getPrDiff returns empty when there is no PR number", async () => {
  const out = await getPrDiff("tok", "mattermost/mattermost", undefined);
  assert.equal(out, "");
});

test("getTestSource returns the source unchanged when under the cap", async () => {
  const src = "test('save', async () => { await page.click('#save'); });\n";
  globalThis.fetch = (async () => new Response(src, { status: 200 })) as typeof fetch;
  const out = await getTestSource("tok", "mattermost/mattermost", "e2e-tests/a.spec.ts", "abc123");
  assert.equal(out, src);
});

test("getTestSource truncates a huge spec to 100KB", async () => {
  const big = "x".repeat(150 * 1024);
  globalThis.fetch = (async () => new Response(big, { status: 200 })) as typeof fetch;
  const out = await getTestSource("tok", "mattermost/mattermost", "e2e-tests/a.spec.ts", "abc123");
  assert.ok(out.includes("truncated"), "must note the truncation");
  assert.ok(Buffer.byteLength(out) <= 100 * 1024 + 64, "must stay near the 100KB cap");
});

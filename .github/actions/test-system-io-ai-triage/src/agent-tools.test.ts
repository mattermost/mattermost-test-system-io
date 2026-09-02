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

// ---------------------------------------------------------------------------
// R7-F — get_test_source path re-rooting.
//
// TSIO stores the framework's own `file`, and Playwright's reporter emits it
// relative to `testDir` ('specs' in the monorepo). So evidence carries
// `functional/channels/drafts.spec.ts` while the repo path is
// `e2e-tests/playwright/specs/functional/channels/drafts.spec.ts`.
// contents/<evidence path> 404s for EVERY spec, so get_test_source always
// returned "could not fetch source" — half of round 6's "give the model the
// evidence" fix was silently inert, and the prompt told the model to read a
// file it could never see.
//
// The existing tests here covered TOOLS registration and byte caps but never
// path resolution, which is exactly why this survived.
// ---------------------------------------------------------------------------
import { testSourceCandidates } from "./main.ts";

test("R7-F: a Playwright spec path is re-rooted under the spec dir", () => {
  const got = testSourceCandidates("functional/channels/drafts.spec.ts");
  assert.ok(
    got.includes("e2e-tests/playwright/specs/functional/channels/drafts.spec.ts"),
    `candidates ${JSON.stringify(got)} must include the real repo path`,
  );
  assert.equal(
    got[0],
    "e2e-tests/playwright/specs/functional/channels/drafts.spec.ts",
    "the most likely candidate must be tried first",
  );
});

test("R7-F: a Cypress spec path is re-rooted too", () => {
  const got = testSourceCandidates("channels/messaging/post.spec.js");
  assert.ok(got.some((c) => c.startsWith("e2e-tests/cypress/tests/integration/")));
});

test("R7-F: an already repo-relative path is left alone and not duplicated", () => {
  const p = "e2e-tests/playwright/specs/functional/channels/drafts.spec.ts";
  assert.deepEqual(testSourceCandidates(p), [p]);
});

test("R7-F: leading ./ and / are normalised", () => {
  for (const p of ["./functional/a.spec.ts", "/functional/a.spec.ts"]) {
    const got = testSourceCandidates(p);
    assert.ok(
      got.includes("e2e-tests/playwright/specs/functional/a.spec.ts"),
      `${p} -> ${JSON.stringify(got)}`,
    );
    assert.ok(!got.some((c) => c.includes("//") || c.startsWith("/")), "no malformed candidates");
  }
});

test("R7-F: a non-spec path still gets tried verbatim — this fetch is read-only", () => {
  // The fixer refuses non-spec paths because it WRITES. Reading a product
  // source the model explicitly asked for is harmless, so it is not refused.
  const got = testSourceCandidates("webapp/channels/src/components/drafts/drafts.tsx");
  assert.deepEqual(got, ["webapp/channels/src/components/drafts/drafts.tsx"]);
});

test("R7-F: empty input yields no candidates rather than fetching the repo root", () => {
  assert.deepEqual(testSourceCandidates(""), []);
  assert.deepEqual(testSourceCandidates("  ".trim()), []);
});

test("R7-F: candidates are unique, so one 404 is not retried twice", () => {
  const got = testSourceCandidates("functional/a.spec.ts");
  assert.equal(new Set(got).size, got.length);
});

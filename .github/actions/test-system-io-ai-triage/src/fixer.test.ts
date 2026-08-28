import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { test } from "node:test";
import type { Decision, EvidenceCluster, FixTarget } from "./types.ts";
import {
  autofixState,
  collectFixTargets,
  isFixable,
  MAX_AUTOFIX_COMMITS_PER_PR,
  runFixer,
  type FixerContext,
} from "./fixer.ts";

function decision(over: Partial<Decision> = {}): Decision {
  return {
    verdict: "TEST_DEBT",
    confidence: 0.88,
    reason: "test drives product into unsupported state",
    citations: ["error_message", "screenshot"],
    waived: false,
    source: "model",
    check_state: "failure",
    kind: "bug",
    member_count: 1,
    ...over,
  };
}

function cluster(over: Partial<EvidenceCluster> = {}): EvidenceCluster {
  return {
    signature: "abcd1234efgh5678",
    label: "MM-T5795 join refused",
    member_count: 1,
    representative: {
      full_title: "MM-T5795 User can be added by admin after attribute added",
      file: "e2e-tests/playwright/specs/abac/join_channel.spec.ts",
      status: "failed",
      retry_count: 1,
      error_message: "User does not have required attributes to join the channel",
      screenshots: [{ s3_key: "orchestration/x.png" }],
      suggested: {
        verdict: "TEST_DEBT",
        confidence: 0.9,
        needs_ai: true,
        reason: "",
        citations: [],
      },
      ...over,
    } as EvidenceCluster["representative"],
    suggested: { verdict: "TEST_DEBT", confidence: 0.9, needs_ai: true, reason: "", citations: [] },
    ...over,
  } as EvidenceCluster;
}

test("TEST_DEBT on a pre-existing spec is fixable", () => {
  assert.equal(isFixable(decision(), cluster(), []), true);
});

test("refusal-blocked flake verdicts are fixable (test drives unsupported state)", () => {
  assert.equal(
    isFixable(
      decision({
        verdict: "FLAKY_INFRA",
        kind: "flaky",
        confidence: 0.93,
        refusal: true,
      }),
      cluster(),
      [],
    ),
    true,
  );
});

test("the author's own new specs are never auto-fixed (demo sentinel protection)", () => {
  const c = cluster();
  c.representative.file = "e2e-tests/playwright/specs/fuzz/ai_triage_demo.spec.ts";
  assert.equal(
    isFixable(decision({ confidence: 0.97 }), c, [
      "e2e-tests/playwright/specs/fuzz/ai_triage_demo.spec.ts",
    ]),
    false,
  );
});

test("product regressions are not fixable by editing tests", () => {
  assert.equal(isFixable(decision({ verdict: "PR_REGRESSION" }), cluster(), []), false);
  assert.equal(isFixable(decision({ verdict: "MAIN_REGRESSION" }), cluster(), []), false);
});

test("low-confidence and waived decisions are not fixable", () => {
  assert.equal(isFixable(decision({ confidence: 0.82 }), cluster(), []), false);
  assert.equal(isFixable(decision({ waived: true }), cluster(), []), false);
});

test("non test-framework paths are excluded", () => {
  const c = cluster();
  c.representative.file = "server/cmd/mattermost/main.go";
  assert.equal(isFixable(decision(), c, []), false);
});

test("collectFixTargets caps at max and skips ineligible", () => {
  const clusters = [
    cluster(),
    cluster({ signature: "22222222" }),
    cluster({ signature: "33333333" }),
  ];
  const decisions = [
    decision(),
    decision({ confidence: 0.5 }), // ineligible
    decision({ confidence: 0.9 }),
  ];
  const targets = collectFixTargets(clusters, decisions, [], 2);
  assert.equal(targets.length, 2);
  assert.equal(targets[0]!.signature, "abcd1234efgh5678");
  assert.equal(targets[1]!.signature, "33333333");
  assert.equal(targets[0]!.screenshots.join(","), "orchestration/x.png");
});

// ---------------------------------------------------------------------------
// Fixer loop-guard fixtures: a scratch git repo standing in for the PR checkout
// ---------------------------------------------------------------------------

function mkFixRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fixer-test-"));
  execFileSync("git", ["init", "-q"], { cwd: dir });
  execFileSync("git", ["config", "user.email", "t@t"], { cwd: dir });
  execFileSync("git", ["config", "user.name", "t"], { cwd: dir });
  fs.mkdirSync(path.join(dir, "e2e-tests/playwright/specs/abac"), { recursive: true });
  fs.writeFileSync(
    path.join(dir, "e2e-tests/playwright/specs/abac/join_channel.spec.ts"),
    "test('seed', () => {});\n",
  );
  execFileSync("git", ["add", "-A"], { cwd: dir });
  execFileSync("git", ["commit", "-q", "-m", "seed"], { cwd: dir });
  return dir;
}

function mkctx(): FixerContext {
  return {
    apiKey: "unused",
    model: "unused",
    workspace: mkFixRepo(),
    token: "unused",
    repository: "o/r",
    prBranch: "pr",
    prNumber: 1,
    baseURL: "http://localhost",
    maxTargets: 2,
  };
}

function target(over: Partial<FixTarget> = {}): FixTarget {
  return {
    signature: "abcd1234efgh5678",
    external_test_id: "MM-T5795",
    full_title: "MM-T5795 User can be added by admin after attribute added",
    file: "e2e-tests/playwright/specs/abac/join_channel.spec.ts",
    error_message: "User does not have required attributes to join the channel",
    reason: "test drives product into unsupported state",
    confidence: 0.9,
    screenshots: [],
    ...over,
  };
}

test("autofixState reads the loop-guard counters from git history", () => {
  const ctx = mkctx();
  execFileSync(
    "git",
    ["commit", "--allow-empty", "-m", "fix(e2e-test): [ai-triage autofix] stabilize prior"],
    { cwd: ctx.workspace },
  );
  const st = autofixState(ctx.workspace);
  assert.equal(st.commits, 1);
  assert.deepEqual(st.files, []);
});

test("runFixer skips specs a previous autofix already touched (one attempt per spec)", async () => {
  const ctx = mkctx();
  execFileSync("git", ["commit", "--allow-empty", "-m", "x"], { cwd: ctx.workspace });
  // Pretend an earlier autofix commit touched the target spec.
  fs.writeFileSync(
    path.join(ctx.workspace, "e2e-tests/playwright/specs/abac/join_channel.spec.ts"),
    "old content\n",
  );
  execFileSync("git", ["add", "--", "e2e-tests"], { cwd: ctx.workspace });
  execFileSync("git", ["commit", "-m", "fix(e2e-test): [ai-triage autofix] stabilize MM-T5795"], {
    cwd: ctx.workspace,
  });

  const results = await runFixer([target()], ctx);
  assert.equal(results.length, 1);
  assert.equal(results[0]!.status, "skipped");
  assert.equal(results[0]!.skip_code, "already_autofixed");
  assert.match(results[0]!.summary, /previous autofix/);
});

test("runFixer refuses everything once the branch hits the autofix commit cap", async () => {
  const ctx = mkctx();
  for (let i = 0; i < MAX_AUTOFIX_COMMITS_PER_PR; i++) {
    execFileSync(
      "git",
      ["commit", "--allow-empty", "-m", `fix(e2e-test): [ai-triage autofix] fix ${i}`],
      { cwd: ctx.workspace },
    );
  }

  const results = await runFixer([target()], ctx);
  assert.equal(results.length, 1);
  assert.equal(results[0]!.status, "skipped");
  assert.equal(results[0]!.skip_code, "branch_cap");
  assert.match(results[0]!.summary, /loop guard/);
});

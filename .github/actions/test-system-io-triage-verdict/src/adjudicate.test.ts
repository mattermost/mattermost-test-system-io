import { describe, expect, it } from "vitest";
import {
  adjudicate,
  applyAdjudication,
  buildPack,
  decide,
  finalVerdict,
  worthAdjudicating,
  type Answer,
  type Pack,
  type TsioPack,
} from "./adjudicate";
import type { Verdict } from "./decision";

const tsioPack = (index: number, cls: string, file = "e2e/specs/login.ts"): TsioPack => ({
  index,
  test: { title: "login works", file, lane: "ios" },
  error: "Timed out waiting for element by id: login_button",
  engine: { class: cls, reason: "r", signature_matches_trunk_dominant_failure: false },
  trunk_history_14d: { runs: 10, fails: 0, flaky: 0, consecutive_fails_at_head: 0 },
  cross_pr_failures_14d: {
    id: "cross_pr",
    other_prs_where_this_test_failed: ["#1", "#2"],
    other_pr_runs_where_it_passed: 30,
  },
  pr: { number: 9, repository: "o/r" },
  other_failures_in_same_run: [],
});
const finding = (cls: string, blocking: boolean) => ({
  file: "e2e/specs/login.ts",
  full_title: "login works",
  class: cls,
  blocking,
  reason: "r",
  pr: { failure_locus: "" },
});
const verdict = (v: string, classes: [string, boolean][], infra = 0): Verdict => ({
  id: "v1",
  verdict: v,
  mode: "enforce",
  confidence: 1,
  counts: {
    blocking: classes.filter(([, b]) => b).length,
    exonerated: classes.filter(([, b]) => !b).length,
    infra,
  },
  findings: classes.map(([c, b]) => finding(c, b)),
  markdown: {
    check_summary: "check",
    pr_comment: "comment",
    status_description: "FAILURE: 1 blocking",
  },
});
const answer = (cause: Answer["cause"], confidence: number, cited: string[]): Answer => ({
  cause,
  confidence,
  cited_evidence: cited,
  explanation: "because",
});

describe("decide", () => {
  it("unblocks a borderline finding only with confidence and checkable citation", () => {
    expect(
      decide("REGRESSION", answer("flaky_environment", 0.9, ["cross_pr"]), 0.85).decision,
    ).toBe("adjudicator_unblock");
    expect(decide("REGRESSION", answer("flaky_environment", 0.9, ["error"]), 0.85).decision).toBe(
      "engine",
    );
    expect(
      decide("REGRESSION", answer("flaky_environment", 0.8, ["cross_pr"]), 0.85).blocking,
    ).toBe(true);
    expect(
      decide("REGRESSION", answer("bug_on_master", 0.9, ["trunk_history_14d"]), 0.85).blocking,
    ).toBe(false);
    expect(decide("OWNED_BY_PR", answer("caused_by_pr", 0.99, ["hunk_0"]), 0.85).blocking).toBe(
      true,
    );
  });
  it("vetoes an engine exoneration only at 0.9 with a cited hunk", () => {
    expect(decide("FLAKY_CONFIRMED", answer("caused_by_pr", 0.95, ["hunk_0"]), 0.85).decision).toBe(
      "adjudicator_veto",
    );
    expect(decide("FLAKY_CONFIRMED", answer("caused_by_pr", 0.95, ["error"]), 0.85).blocking).toBe(
      false,
    );
    expect(decide("FLAKY_CONFIRMED", answer("caused_by_pr", 0.89, ["hunk_0"]), 0.85).blocking).toBe(
      false,
    );
    expect(decide("BROKEN_ON_TRUNK", answer("flaky_environment", 0.3, []), 0.85).blocking).toBe(
      false,
    );
  });
  it("falls back to the engine when the model is unavailable", () => {
    expect(decide("REGRESSION", null, 0.85)).toEqual({ blocking: true, decision: "unavailable" });
    expect(decide("FLAKY_CROSS_PR", null, 0.85)).toEqual({
      blocking: false,
      decision: "unavailable",
    });
    expect(decide("INFRA", answer("flaky_environment", 1, ["cross_pr"]), 0.85).blocking).toBe(true);
  });
});

describe("buildPack", () => {
  it("attaches hunks for files named in the error or spec, and all hunks for small PRs", () => {
    const files = [
      { filename: "app/screens/login/login_button.tsx", patch: "@@ -1 +1 @@ x" },
      { filename: "app/utils/unrelated.ts", patch: "@@ -2 +2 @@ y" },
    ];
    const pack = buildPack(tsioPack(0, "REGRESSION"), files, "Fix login");
    expect(pack.pr.title).toBe("Fix login");
    expect(pack.pr.spec_file_changed_by_pr).toBe(false);
    expect(pack.diff_hunks_of_files_named_in_error.map((h) => h.id)).toEqual(["hunk_0", "hunk_1"]);
    const many = Array.from({ length: 20 }, (_, i) => ({ filename: `app/f${i}.ts`, patch: "p" }));
    many.push({ filename: "e2e/specs/login.ts", patch: "spec" });
    const big = buildPack(tsioPack(0, "REGRESSION"), many, "");
    expect(big.pr.spec_file_changed_by_pr).toBe(true);
    expect(big.diff_hunks_of_files_named_in_error).toEqual([
      { id: "hunk_0", file: "e2e/specs/login.ts", patch: "spec" },
    ]);
    expect(big.pr.changed_file_count).toBe(21);
  });
});

describe("adjudicate", () => {
  it("applies the matrix per finding and derives the final verdict", async () => {
    const v = verdict("FAILURE", [
      ["REGRESSION", true],
      ["FLAKY_CONFIRMED", false],
      ["INFRA", true],
    ]);
    const packs: Pack[] = [0, 1].map((i) => buildPack(tsioPack(i, v.findings[i].class), [], ""));
    const asked: number[] = [];
    const result = await adjudicate({
      verdict: v,
      packs,
      model: "m",
      minConfidence: 0.85,
      ask: async (pack) => {
        asked.push(pack.index);
        return answer("flaky_environment", 0.92, ["cross_pr", "error"]);
      },
    });
    expect(asked.sort()).toEqual([0, 1]);
    expect(result.findings.map((f) => [f.decision, f.blocking])).toEqual([
      ["adjudicator_unblock", false],
      ["engine", false],
      ["engine", true],
    ]);
    expect(result.final_verdict).toBe("FAILURE");
    expect(result.blocking).toBe(1);
  });
  it("unblocks the run when every blocking finding is exonerated with evidence", async () => {
    const v = verdict("FAILURE", [
      ["REGRESSION", true],
      ["REGRESSION", true],
    ]);
    const packs = [0, 1].map((i) => buildPack(tsioPack(i, "REGRESSION"), [], ""));
    const result = await adjudicate({
      verdict: v,
      packs,
      model: "m",
      minConfidence: 0.85,
      ask: async () => answer("flaky_environment", 0.9, ["cross_pr"]),
    });
    expect(result.final_verdict).toBe("SUCCESS");
    applyAdjudication(v, result);
    expect(v.verdict).toBe("SUCCESS");
    expect(v.counts).toEqual({ blocking: 0, exonerated: 2, infra: 0 });
    expect(v.findings.every((f) => !f.blocking)).toBe(true);
    expect(v.markdown.pr_comment).toContain("Second judge (m)");
    expect(v.markdown.pr_comment).toContain("unblocked");
    expect(v.markdown.status_description).toBe(
      "SUCCESS: 0 blocking, 2 exonerated; AI second judge changed 2",
    );
  });
  it("never asks the model about ownership of the failing spec itself", async () => {
    const v = verdict("FAILURE", [["OWNED_BY_PR", true]]);
    const packs = [
      buildPack(tsioPack(0, "OWNED_BY_PR"), [{ filename: "e2e/specs/login.ts", patch: "p" }], ""),
    ];
    let calls = 0;
    const result = await adjudicate({
      verdict: v,
      packs,
      model: "m",
      minConfidence: 0.85,
      ask: async () => {
        calls++;
        return answer("flaky_environment", 1, ["cross_pr"]);
      },
    });
    expect(calls).toBe(0);
    expect(result.findings[0]).toMatchObject({ decision: "engine", blocking: true, cause: "" });
  });
  it("keeps the engine decision when the model errors", async () => {
    const v = verdict("FAILURE", [["REGRESSION", true]]);
    const warnings: string[] = [];
    const result = await adjudicate({
      verdict: v,
      packs: [buildPack(tsioPack(0, "REGRESSION"), [], "")],
      model: "m",
      minConfidence: 0.85,
      ask: async () => {
        throw new Error("boom");
      },
      warn: (m) => warnings.push(m),
    });
    expect(result.findings[0]).toMatchObject({ decision: "unavailable", blocking: true });
    expect(result.final_verdict).toBe("FAILURE");
    expect(warnings[0]).toContain("boom");
  });
});

describe("finalVerdict / worthAdjudicating", () => {
  it("keeps INCOMPLETE and infra-driven ACTION_REQUIRED", () => {
    expect(finalVerdict(verdict("INCOMPLETE", [["REGRESSION", true]]), 0)).toBe("INCOMPLETE");
    expect(finalVerdict(verdict("ACTION_REQUIRED", [["INFRA", true]], 1), 0)).toBe(
      "ACTION_REQUIRED",
    );
    expect(finalVerdict(verdict("ACTION_REQUIRED", [["FLAKY_CONFIRMED", false]]), 0)).toBe(
      "SUCCESS",
    );
    expect(finalVerdict(verdict("FAILURE", [["REGRESSION", true]]), 0)).toBe("SUCCESS");
    expect(worthAdjudicating(verdict("FAILURE", [["INFRA", true]]))).toBe(false);
    expect(worthAdjudicating(verdict("FAILURE", [["REGRESSION", true]]))).toBe(true);
    expect(worthAdjudicating(verdict("SUCCESS", [["FLAKY_CONFIRMED", false]]))).toBe(true);
    expect(worthAdjudicating(verdict("INCOMPLETE", [["REGRESSION", true]]))).toBe(false);
  });
});

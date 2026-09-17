import Anthropic from "@anthropic-ai/sdk";
import type { Verdict } from "./decision";

export const ADJUDICABLE = new Set([
  "BROKEN_ON_TRUNK",
  "FLAKY_CONFIRMED",
  "FLAKY_CROSS_PR",
  "QUARANTINED",
  "REGRESSION",
  "REGRESSION_CLUSTER",
  "REGRESSION_AREA",
  "OWNED_BY_PR",
  "INSUFFICIENT_DATA",
  "FLAKY_SUSPICIOUS",
]);

/** True when the second judge could change the outcome of this run. */
export function worthAdjudicating(v: Verdict): boolean {
  if (v.verdict === "INCOMPLETE" || v.findings.length === 0) return false;
  if (v.verdict === "ACTION_REQUIRED" && (v.counts.infra ?? 0) > 0) return false;
  return v.findings.some((f) => ADJUDICABLE.has(f.class));
}

/** Apply an adjudication to the verdict the rest of the action publishes. */
export function applyAdjudication(v: Verdict, a: Adjudication): void {
  const before = v.verdict;
  v.verdict = a.final_verdict;
  v.counts = { ...v.counts, blocking: a.blocking, exonerated: a.exonerated };
  for (const f of a.findings) {
    const target = v.findings[f.index];
    if (target) target.blocking = f.blocking;
  }
  const section = renderAdjudication(
    a,
    before,
    v.findings.map((f) => f.full_title),
  );
  v.markdown = {
    check_summary: `${v.markdown.check_summary}\n${section}`,
    pr_comment: `${v.markdown.pr_comment}\n${section}`,
    status_description: statusDescription(a),
  };
  v.adjudication = {
    model: a.model,
    final_verdict: a.final_verdict,
    blocking: a.blocking,
    exonerated: a.exonerated,
  };
}

export function statusDescription(a: Adjudication): string {
  const changed = a.findings.filter((f) => f.decision.startsWith("adjudicator_")).length;
  const tail = changed > 0 ? `; AI second judge changed ${changed}` : "; AI second judge agreed";
  return `${a.final_verdict}: ${a.blocking} blocking, ${a.exonerated} exonerated${tail}`.slice(
    0,
    140,
  );
}

/** Evidence pack as served by TSIO (`GET /triage/verdicts/{id}/evidence`). */
export interface TsioPack {
  index: number;
  test: { title: string; file: string; lane: string };
  error: string;
  engine: { class: string; reason: string; signature_matches_trunk_dominant_failure: boolean };
  trunk_history_14d: {
    runs: number;
    fails: number;
    flaky: number;
    consecutive_fails_at_head: number;
  };
  cross_pr_failures_14d: {
    id: string;
    other_prs_where_this_test_failed: string[];
    other_pr_runs_where_it_passed: number;
  };
  pr: { number: number | null; repository: string };
  other_failures_in_same_run: { class: string; title: string }[];
}
/** Pack after the producer adds what only GitHub knows. */
export interface Pack extends TsioPack {
  pr: TsioPack["pr"] & {
    title: string;
    changed_file_count: number;
    changed_files: string[];
    spec_file_changed_by_pr: boolean;
  };
  diff_hunks_of_files_named_in_error: { id: string; file: string; patch: string }[];
}
export interface Answer {
  cause: "caused_by_pr" | "flaky_environment" | "bug_on_master" | "test_bug";
  confidence: number;
  cited_evidence: string[];
  explanation: string;
}
export interface AdjudicatedFinding {
  index: number;
  class: string;
  cause: Answer["cause"] | "";
  confidence: number;
  cited_evidence: string[];
  explanation: string;
  decision: "engine" | "adjudicator_unblock" | "adjudicator_veto" | "unavailable";
  blocking: boolean;
}
export interface Adjudication {
  model: string;
  min_confidence: number;
  final_verdict: string;
  blocking: number;
  exonerated: number;
  findings: AdjudicatedFinding[];
}

export const EXONERATED = new Set([
  "BROKEN_ON_TRUNK",
  "FLAKY_CONFIRMED",
  "FLAKY_CROSS_PR",
  "QUARANTINED",
]);
export const BORDERLINE = new Set([
  "REGRESSION",
  "REGRESSION_CLUSTER",
  "REGRESSION_AREA",
  "OWNED_BY_PR",
  "INSUFFICIENT_DATA",
  "FLAKY_SUSPICIOUS",
]);
export const VETO_MIN = 0.9;

// Prompt and schema are byte-identical to scripts/triage_adjudicate.py, which is
// what the backtest measured. Change them there first and re-score.
export const SYSTEM = `You are the second judge in an automated CI triage system for end-to-end test failures on pull requests.
A deterministic engine has already classified each failing test from statistics (trunk history, other PRs' history,
file ownership). You adjudicate ONE finding at a time using the evidence pack and answer a single question:
what caused this test to fail on this PR run?

Causes:
- caused_by_pr: the PR's code change plausibly produces this failure (the error is about behavior, selectors, data or
  flows the diff touches; or the failing test/helper is modified by the PR).
- flaky_environment: the failure is environmental or timing-related and unrelated to the diff (server/API health,
  device or emulator problems, timeouts waiting for UI that the PR does not touch, the same failure recurring on
  unrelated PRs). This includes failures that also occurred on other PRs the author had nothing to do with.
- bug_on_master: the same failure already occurs on the trunk branch (master/main) before this PR, so the PR inherits it.
- test_bug: the test itself is wrong or brittle in a way the PR did not introduce (stale assertion, race in the test).

Rules:
- Cite only evidence items by their id from the pack. Never invent PR numbers, files or errors.
- caused_by_pr requires a concrete link between the error and the diff: name the changed file or hunk that explains it.
  A PR touching many files is not by itself evidence; a PR touching the failing spec, a helper in the stack, or the
  feature under test is.
- flaky_environment with high confidence requires either recurrence on other PRs (evidence id starting with cross_pr)
  or an error text that is clearly infrastructural (server not healthy, cannot connect, device/emulator failure, app crash on
  launch) together with a diff that does not touch that area.
- If the evidence is genuinely insufficient, answer with confidence below 0.6 rather than guessing.
- Be precise and terse in the explanation: one paragraph a developer can act on.`;

export const SCHEMA = {
  type: "object",
  properties: {
    cause: {
      type: "string",
      enum: ["caused_by_pr", "flaky_environment", "bug_on_master", "test_bug"],
    },
    confidence: { type: "number", description: "0 to 1" },
    cited_evidence: {
      type: "array",
      items: { type: "string" },
      description: "up to 6 evidence ids from the pack",
    },
    explanation: { type: "string", description: "one short paragraph a developer can act on" },
  },
  required: ["cause", "confidence", "cited_evidence", "explanation"],
  additionalProperties: false,
} as const;

export function evidenceIds(pack: Pack): string[] {
  return [
    "test",
    "error",
    "engine",
    "trunk_history_14d",
    "cross_pr",
    "pr.changed_files",
    ...pack.diff_hunks_of_files_named_in_error.map((h) => h.id),
  ];
}

/** Merge GitHub-only evidence (diff) into a TSIO pack. */
export function buildPack(
  base: TsioPack,
  compareFiles: { filename: string; patch?: string }[],
  prTitle: string,
): Pack {
  const names = compareFiles.map((f) => f.filename);
  const text = `${base.error}\n${base.test.file}`.toLowerCase();
  const small = compareFiles.length <= 8;
  const hunks: Pack["diff_hunks_of_files_named_in_error"] = [];
  for (const f of compareFiles) {
    const baseName = f.filename.split("/").pop() ?? "";
    const stem = baseName.split(".")[0] ?? "";
    const named =
      f.filename === base.test.file ||
      (stem.length > 3 && text.includes(stem.toLowerCase())) ||
      base.error.includes(baseName);
    if ((named || small) && f.patch) {
      hunks.push({
        id: `hunk_${hunks.length}`,
        file: f.filename,
        patch: f.patch.slice(0, named ? 4000 : 2500),
      });
    }
    if (hunks.length >= 8) break;
  }
  return {
    ...base,
    pr: {
      ...base.pr,
      title: prTitle,
      changed_file_count: names.length,
      changed_files: names.slice(0, 200),
      spec_file_changed_by_pr: names.includes(base.test.file),
    },
    diff_hunks_of_files_named_in_error: hunks,
  };
}

/** The decision matrix from docs/triage.md. Returns the blocking decision for one finding. */
export function decide(
  cls: string,
  answer: Answer | null,
  minConfidence: number,
): { blocking: boolean; decision: AdjudicatedFinding["decision"] } {
  if (!answer) return { blocking: !EXONERATED.has(cls), decision: "unavailable" };
  const cited = answer.cited_evidence;
  const citesHunk = cited.some((c) => c.startsWith("hunk_"));
  const citesCross = cited.includes("cross_pr");
  const conf = Math.max(0, Math.min(1, answer.confidence));
  if (EXONERATED.has(cls)) {
    if (answer.cause === "caused_by_pr" && conf >= VETO_MIN && citesHunk)
      return { blocking: true, decision: "adjudicator_veto" };
    return { blocking: false, decision: "engine" };
  }
  if (BORDERLINE.has(cls)) {
    const environmental = answer.cause !== "caused_by_pr";
    if (
      environmental &&
      conf >= minConfidence &&
      (citesCross || citesHunk || answer.cause === "bug_on_master")
    )
      return { blocking: false, decision: "adjudicator_unblock" };
    return { blocking: true, decision: "engine" };
  }
  return { blocking: true, decision: "engine" };
}

export async function askModel(client: Anthropic, model: string, pack: Pack): Promise<Answer> {
  const user =
    "Evidence pack (JSON). Valid evidence ids to cite: " +
    evidenceIds(pack).join(", ") +
    "\n\n" +
    JSON.stringify(pack, null, 1);
  const response = await client.messages.create({
    model,
    max_tokens: 2000,
    system: [{ type: "text", text: SYSTEM, cache_control: { type: "ephemeral" } }],
    messages: [{ role: "user", content: user }],
    output_config: {
      format: { type: "json_schema", schema: SCHEMA },
      // Haiku 4.5 has no effort control; current Opus/Sonnet models do.
      ...(model.startsWith("claude-haiku") ? {} : { effort: "medium" }),
    },
  });
  if (response.stop_reason !== "end_turn" && response.stop_reason !== "stop_sequence")
    throw new Error(`adjudicator stop_reason=${response.stop_reason}`);
  const text = response.content.find((b) => b.type === "text");
  if (!text || text.type !== "text") throw new Error("adjudicator returned no text block");
  const parsed = JSON.parse(text.text) as Answer;
  if (
    !["caused_by_pr", "flaky_environment", "bug_on_master", "test_bug"].includes(parsed.cause) ||
    typeof parsed.confidence !== "number" ||
    !Array.isArray(parsed.cited_evidence)
  )
    throw new Error("adjudicator answer failed validation");
  return parsed;
}

/** Final verdict after the matrix: the engine keeps INCOMPLETE and infra-driven ACTION_REQUIRED. */
export function finalVerdict(engine: Verdict, blocking: number): string {
  if (engine.verdict === "INCOMPLETE") return "INCOMPLETE";
  if (
    engine.verdict === "ACTION_REQUIRED" &&
    ((engine.counts.infra ?? 0) > 0 || engine.findings.length === 0)
  )
    return "ACTION_REQUIRED";
  return blocking > 0 ? "FAILURE" : "SUCCESS";
}

export interface AdjudicateOptions {
  verdict: Verdict;
  packs: Pack[];
  model: string;
  minConfidence: number;
  concurrency?: number;
  ask: (pack: Pack) => Promise<Answer>;
  warn?: (message: string) => void;
}

/** Run the second judge over the adjudicable findings and apply the matrix to the whole run. */
export async function adjudicate(o: AdjudicateOptions): Promise<Adjudication> {
  const byIndex = new Map(o.packs.map((p) => [p.index, p]));
  const findings: AdjudicatedFinding[] = o.verdict.findings.map((f, index) => ({
    index,
    class: f.class,
    cause: "",
    confidence: 0,
    cited_evidence: [],
    explanation: "",
    decision: "engine",
    blocking: !EXONERATED.has(f.class),
  }));
  const queue = o.verdict.findings
    .map((f, index) => ({ f, index, pack: byIndex.get(index) }))
    // Ownership of the failing spec itself is the engine's call, never the model's.
    .filter((x) => x.pack && !(x.f.class === "OWNED_BY_PR" && x.pack.pr.spec_file_changed_by_pr));
  const workers = Array.from({ length: Math.max(1, o.concurrency ?? 4) }, async () => {
    for (;;) {
      const item = queue.shift();
      if (!item) return;
      const target = findings[item.index];
      try {
        const answer = await o.ask(item.pack!);
        const d = decide(item.f.class, answer, o.minConfidence);
        Object.assign(target, {
          cause: answer.cause,
          confidence: Math.max(0, Math.min(1, answer.confidence)),
          cited_evidence: answer.cited_evidence.slice(0, 6),
          explanation: answer.explanation.slice(0, 900),
          decision: d.decision,
          blocking: d.blocking,
        });
      } catch (error) {
        o.warn?.(`adjudicator unavailable for finding ${item.index}: ${String(error)}`);
        const d = decide(item.f.class, null, o.minConfidence);
        Object.assign(target, { decision: d.decision, blocking: d.blocking });
      }
    }
  });
  await Promise.all(workers);
  const blocking = findings.filter((f) => f.blocking).length;
  return {
    model: o.model,
    min_confidence: o.minConfidence,
    final_verdict: finalVerdict(o.verdict, blocking),
    blocking,
    exonerated: findings.length - blocking,
    findings,
  };
}

/** Markdown appended to the PR comment and check summary. */
export function renderAdjudication(
  a: Adjudication,
  engineVerdict: string,
  titles: string[],
): string {
  const lines = [
    "",
    `### Second judge (${a.model})`,
    "",
    `Engine: **${engineVerdict}** → final: **${a.final_verdict}** (${a.blocking} blocking, ${a.exonerated} exonerated).`,
    "",
  ];
  for (const f of a.findings) {
    if (!f.cause) continue;
    const mark =
      f.decision === "adjudicator_unblock"
        ? "unblocked"
        : f.decision === "adjudicator_veto"
          ? "vetoed"
          : f.blocking
            ? "still blocking"
            : "agreed";
    lines.push(
      `- **${escapeMd(titles[f.index] ?? `#${f.index}`)}** — ${f.cause.replace("_", " ")} (${Math.round(f.confidence * 100)}%, ${mark}): ${escapeMd(f.explanation)}`,
    );
  }
  return lines.join("\n");
}

function escapeMd(s: string): string {
  return s
    .replace(/[<>|`]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "|": "&#124;", "`": "&#96;" })[c] ?? c)
    .replace(/\r?\n/g, " ");
}

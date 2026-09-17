/**
 * Score the AI second judge offline against a backtest.
 *
 * Uses the production code path (`../adjudicate`: prompt, schema, pack merge,
 * decision matrix) over the findings of every selected backtest run. Evidence
 * that TSIO serves in production (full error, cross-PR history) is read from
 * the backtest database via psql; the PR diff comes from the gh compare cache
 * written by the backtest. Model responses are cached by evidence hash so
 * re-scoring after a threshold change is free.
 *
 *   ANTHROPIC_API_KEY=... npm run adjudicate-offline -- --backtest backtest-mobile.refined.json \
 *     --gh-cache gh-cache.json --out adjudicated-mobile.json [--model claude-haiku-4-5] [--dry-run]
 *     [--dump-packs packs.jsonl] [--import-responses responses.jsonl] [--limit N] [--buckets A,B]
 */
import { appendFileSync, writeFileSync } from "node:fs";
import {
  adjudicate,
  askModel,
  buildPack,
  cacheKey,
  evidenceIds,
  MAX_ADJUDICATED_FINDINGS,
  SCHEMA,
  SYSTEM,
  type Answer,
  type Pack,
  type TsioPack,
} from "../adjudicate";
import { AnthropicClient } from "../anthropic";
import type { Verdict } from "../decision";
import {
  FileCache,
  GREEN_VERDICTS,
  log,
  parseArgs,
  psqlMany,
  q,
  readJSON,
  readJSONL,
  writeJSON,
  type BacktestRow,
} from "./lib";

const FAILED = "('failed','timedOut','interrupted')";

interface RunScope {
  gid: string;
  lane: string;
}
/** Report group id and lane are shared by every finding of a run: one query per run. */
function runScope(r: BacktestRow): RunScope {
  const [gid, lane] = psqlMany([
    `select id from report_groups where repository=${q(r.repo)} and name=${q(r.name)} and commit_sha like ${q(r.sha + "%")} order by created_at desc limit 1`,
    `select coalesce(environment_metadata->>'lane','') from report_groups where repository=${q(r.repo)} and name=${q(r.name)} limit 1`,
  ]);
  return { gid, lane };
}
/** Full error text and cross-PR history for one finding, in a single psql process. */
function findingEvidence(
  r: BacktestRow,
  scope: RunScope,
  titlePrefix: string,
): { error: string; fails: string[]; passes: number } {
  const asOf = Date.parse(r.as_of);
  const fmt = (ms: number) =>
    new Date(ms)
      .toISOString()
      .replace("T", " ")
      .replace(/\.\d{3}Z$/, "");
  const errorSQL = scope.gid
    ? `select coalesce(c.error_message,'') from test_cases c join test_identities i on i.id=c.identity_id
      join suites s on s.id=c.suite_id join reports rp on rp.id=s.report_id
      where rp.report_group_id='${scope.gid}' and left(i.normalized_title,${titlePrefix.length})=${q(titlePrefix)} and c.status in ${FAILED}
      order by c.ordinal limit 1`
    : "select ''";
  const crossSQL = `select o.gh_pr_number, o.status, left(o.commit_sha,7), to_char(o.observed_at,'MM-DD') from test_observations o join test_identities i on i.id=o.identity_id
      where i.repository=${q(r.repo)} and o.lane=${q(scope.lane)} and o.branch_kind='pr' and o.gh_pr_number<>${r.pr}
      and left(i.normalized_title,${titlePrefix.length})=${q(titlePrefix)} and o.observed_at between '${fmt(asOf - 14 * 86400000)}' and '${fmt(asOf)}' and not o.is_infra_stub
      order by o.observed_at desc`;
  const [error, rows] = psqlMany([errorSQL, crossSQL]);
  // One entry per other PR (its newest failure; rows are newest-first), so a
  // single noisy PR cannot fill the list and hide distinct recurrence.
  const fails: string[] = [];
  const seenPRs = new Set<string>();
  let passes = 0;
  for (const line of rows.split("\n").filter(Boolean)) {
    const [pr, status, sha, day] = line.split("|");
    if (["failed", "timedOut", "interrupted"].includes(status)) {
      if (seenPRs.has(pr)) continue;
      seenPRs.add(pr);
      fails.push(`PR ${pr} (${sha}, ${day})`);
    } else if (status === "passed") passes++;
  }
  return { error: error.slice(0, 2500), fails, passes };
}

/** The statistical half of the pack, as TSIO's evidence endpoint would serve it. */
function tsioPack(
  r: BacktestRow,
  scope: RunScope,
  index: number,
  others: { class: string; title: string }[],
): TsioPack {
  const f = r.findings[index];
  const evidence = findingEvidence(r, scope, f.title);
  const error = evidence.error || f.excerpt;
  const { fails, passes } = evidence;
  return {
    index,
    test: { title: f.title, file: f.file, lane: r.name },
    error,
    engine: {
      class: f.class,
      reason: f.reason,
      signature_matches_trunk_dominant_failure: f.match ?? false,
    },
    trunk_history_14d: {
      runs: f.trunk.runs ?? 0,
      fails: f.trunk.fails ?? 0,
      flaky: f.trunk.flaky ?? 0,
      consecutive_fails_at_head: f.trunk.consecutive_fails ?? 0,
    },
    cross_pr_failures_14d: {
      id: "cross_pr",
      other_prs_where_this_test_failed: fails.slice(0, 12),
      other_pr_runs_where_it_passed: passes,
    },
    pr: { number: r.pr, repository: r.repo },
    other_failures_in_same_run: others,
  };
}

interface Decision {
  title: string;
  class: string;
  blocking: boolean;
  by: string;
  adjudicator?: Answer;
}
interface OutRow {
  repo: string;
  pr: number;
  name: string;
  sha: string;
  run_at: string;
  truth: string;
  verdict: string;
  classes: Record<string, number>;
  adjudicated_verdict: string;
  decisions: Decision[];
}

async function main(): Promise<void> {
  const { opts } = parseArgs(process.argv.slice(2));
  const backtest = opts.backtest;
  const ghCachePath = opts["gh-cache"];
  const out = opts.out;
  if (!backtest || !ghCachePath || !out)
    throw new Error("--backtest, --gh-cache and --out are required");
  const model = opts.model ?? "claude-haiku-4-5";
  const minConfidence = Number(opts["min-confidence"] ?? 0.85);
  const buckets = new Set(
    (
      opts.buckets ?? "LIKELY_REGRESSION,RECURRING_ELSEWHERE,WAIVED,RERUN_PASSED,FIXED_BY_AUTHOR"
    ).split(","),
  );
  const limit = Number(opts.limit ?? 0);
  const cache = new FileCache(opts.cache ?? ".triage-adjudicate-cache.json");
  let dryRun = opts["dry-run"] === "true";
  if (opts["import-responses"]) {
    let n = 0;
    for (const rec of readJSONL<{ key: string; verdict: Answer }>(opts["import-responses"])) {
      cache.set(rec.key, rec.verdict);
      n++;
    }
    log(`imported ${n} responses`);
    dryRun = true; // score from the cache; anything missing stays engine-decided
  }
  const dump = opts["dump-packs"];
  if (dump) {
    writeFileSync(
      dump,
      JSON.stringify({ header: true, model, system: SYSTEM, schema: SCHEMA }) + "\n",
    );
    dryRun = true;
  }
  const rows = readJSON<BacktestRow[]>(backtest, []);
  const ghCache = new FileCache(ghCachePath);
  const client = dryRun
    ? null
    : new AnthropicClient({ apiKey: process.env.ANTHROPIC_API_KEY ?? "", maxRetries: 4 });

  const dumped = new Set<string>();
  let calls = 0;
  const ask = async (pack: Pack): Promise<Answer> => {
    const key = cacheKey(model, pack);
    const hit = cache.get<Answer>(key);
    if (hit) return hit;
    if (dump && !dumped.has(key)) {
      dumped.add(key);
      appendFileSync(dump, JSON.stringify({ key, pack, evidence_ids: evidenceIds(pack) }) + "\n");
    }
    if (!client) throw new Error("not cached (dry run)");
    const answer = await askModel(client, model, pack);
    calls++;
    cache.set(key, answer);
    return answer;
  };

  let selected = rows.filter((r) => buckets.has(r.truth) && r.findings.length);
  if (limit) selected = selected.slice(0, limit);
  log(`${selected.length} runs to adjudicate`);
  const results: OutRow[] = [];
  let estTokens = 0;
  for (const [n, r] of selected.entries()) {
    const cmpKey = ghCache.keys().find((k) => k.startsWith(`cmp:${r.repo}:${r.sha}`));
    const cmp = cmpKey
      ? (ghCache.get<{ files?: { filename: string; patch?: string }[] }>(cmpKey) ?? {})
      : {};
    const files = (cmp.files ?? []).map((f) => ({ filename: f.filename, patch: f.patch }));
    const title = ghCache.get<{ title?: string }>(`pr:${r.repo}:${r.pr}`)?.title ?? "";
    const others = r.findings
      .map((x) => ({ class: x.class, title: x.title.slice(0, 80) }))
      .slice(0, 12);
    const verdict: Verdict = {
      id: `${r.pr}:${r.name}:${r.sha}`,
      verdict: r.verdict,
      mode: "enforce",
      confidence: r.confidence ?? 0,
      counts: {
        blocking: r.counts.blocking ?? 0,
        exonerated: r.counts.exonerated ?? 0,
        infra: r.counts.infra ?? 0,
      },
      findings: r.findings.map((f) => ({
        file: f.file,
        full_title: f.title,
        class: f.class,
        blocking: false,
        reason: f.reason,
        pr: { failure_locus: "" },
      })),
      markdown: { check_summary: "", pr_comment: "", status_description: "" },
    };
    const packs: Pack[] = [];
    let scope: RunScope | null = null;
    for (const [index, f] of r.findings.entries()) {
      // Build packs only for the findings adjudicate() will actually ask about.
      if (packs.length >= MAX_ADJUDICATED_FINDINGS) break;
      if (["NEW_TEST", "INFRA"].includes(f.class)) continue;
      scope ??= runScope(r);
      const pack = buildPack(tsioPack(r, scope, index, others), files, title);
      if (f.class === "OWNED_BY_PR" && pack.pr.spec_file_changed_by_pr) continue;
      estTokens += Math.floor(JSON.stringify(pack).length / 4) + 300;
      packs.push(pack);
    }
    const result = await adjudicate({
      verdict,
      packs,
      model,
      minConfidence,
      ask,
      warn: dryRun ? undefined : log,
    });
    results.push({
      repo: r.repo,
      pr: r.pr,
      name: r.name,
      sha: r.sha,
      run_at: r.run_at,
      truth: r.truth,
      verdict: r.verdict,
      classes: r.classes,
      adjudicated_verdict: result.final_verdict,
      decisions: result.findings.map((f) => ({
        title: r.findings[f.index].title,
        class: f.class,
        blocking: f.blocking,
        by: f.decision === "unavailable" ? "engine" : f.decision,
        adjudicator: f.cause
          ? {
              cause: f.cause,
              confidence: f.confidence,
              cited_evidence: f.cited_evidence,
              explanation: f.explanation,
            }
          : undefined,
      })),
    });
    log(
      `[${n + 1}/${selected.length}] PR ${r.pr} ${r.name} ${r.sha} ${r.truth}: engine=${r.verdict} adjudicated=${result.final_verdict}`,
    );
  }
  writeJSON(out, results, true);
  if (dump) log(`wrote ${dumped.size} packs to ${dump}`);
  if (dryRun && !opts["import-responses"]) {
    log(
      `dry run: ~${estTokens.toLocaleString()} input tokens across ${results.reduce((s, r) => s + r.decisions.length, 0)} findings (${results.length} runs)`,
    );
    return;
  }
  const truths = [...new Set(results.map((r) => r.truth))].sort();
  console.log("\n| Ground truth | runs | engine green | adjudicated green |\n|---|---|---|---|");
  for (const t of truths) {
    const rs = results.filter((r) => r.truth === t);
    console.log(
      `| ${t} | ${rs.length} | ${rs.filter((r) => GREEN_VERDICTS.has(r.verdict)).length} | ${rs.filter((r) => GREEN_VERDICTS.has(r.adjudicated_verdict)).length} |`,
    );
  }
  console.log(`\nAPI calls this run: ${calls}`);
}

main().catch((error) => {
  log(String(error));
  process.exit(1);
});

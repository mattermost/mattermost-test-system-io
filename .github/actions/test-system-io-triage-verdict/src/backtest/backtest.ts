/**
 * Backtest the triage engine against historical PR runs.
 *
 * For every completed PR report group in a TSIO instance (normally one seeded
 * from production with `tsioctl db import-remote`):
 *   1. ground truth from GitHub via `gh` (PR state, label events, later runs of
 *      the same PR, merge base and changed files);
 *   2. an admin-only `as_of` replay verdict, so the engine only sees the trunk
 *      evidence that existed when the run finished;
 *   3. a ground-truth bucket per run and the agreement report.
 *
 * Buckets: GREEN (no failures), WAIVED (verified-flaky label while this run was
 * the PR's latest), OVERRIDDEN (blanket skip label), RERUN_PASSED (same commit
 * passed later), FIXED_BY_AUTHOR (a later commit passed), UNRESOLVED.
 *
 *   TSIO_API_KEY=... TSIO_ADMIN_KEY=... npm run backtest -- \
 *     --base-url http://localhost:8080/api/v1 --repository mattermost/mattermost-mobile \
 *     --since 2026-08-01T00:00:00Z --out backtest-mobile.md [--cache gh-cache.json] [--limit N]
 */
import { writeFileSync } from "node:fs";
import {
  BASE_REF,
  contextFor,
  FileCache,
  gh,
  GREEN_VERDICTS,
  laneFor,
  log,
  OVERRIDE_LABELS,
  parseArgs,
  tsio,
  VERIFIED_LABELS,
  writeJSON,
  type BacktestRow,
} from "./lib";

interface Group {
  id: string;
  repository: string;
  name: string;
  status: string;
  created_at: string;
  last_upload_at?: string;
  commit: string;
  gh_run_id: string;
  gh_run_attempt?: string;
  gh_pr_number?: number;
  test_stats?: { failed?: number; passed?: number; flaky?: number };
}
interface Compare {
  merge_base_commit?: { sha: string };
  files?: { filename: string }[];
}
interface EngineFinding {
  class: string;
  full_title?: string;
  file?: string;
  reason?: string;
  trunk?: Record<string, number>;
  pr?: { error_excerpt?: string; signature_match?: boolean };
}
interface EngineVerdict {
  verdict: string;
  reason?: string;
  findings: EngineFinding[];
  counts: Record<string, number>;
  confidence: number;
  error?: string;
  metadata_only?: boolean;
}
/** GitHub evidence is required for ground truth; a failed lookup stops the backtest instead of being cached as empty. */
function required<T>(value: T | null, what: string): T {
  if (value === null)
    throw new Error(
      `GitHub lookup failed for ${what}; fix gh auth or rate limit and re-run (successful lookups are cached)`,
    );
  return value;
}

type Stored = {
  v: EngineVerdict;
  baseSha: string;
  changed: string[];
  asOf: string;
  runEnd: string;
};

async function main(): Promise<void> {
  const { opts } = parseArgs(process.argv.slice(2));
  const baseURL = opts["base-url"] ?? "http://localhost:8080/api/v1";
  const repo = opts.repository;
  const since = opts.since;
  const out = opts.out;
  if (!repo || !since || !out) throw new Error("--repository, --since and --out are required");
  const limit = Number(opts.limit ?? 0);
  const key = process.env.TSIO_API_KEY;
  const admin = process.env.TSIO_ADMIN_KEY;
  if (!key || !admin) throw new Error("TSIO_API_KEY and TSIO_ADMIN_KEY are required");
  const cache = new FileCache(opts.cache ?? ".triage-backtest-cache.json");
  const sinceMs = Date.parse(since);

  // 1. every completed group for the repository, newest first.
  const groups: Group[] = [];
  for (let offset = 0; offset < 100000; offset += 200) {
    const { body } = await tsio<{ reports: Group[]; total: number }>(
      baseURL,
      `/reports?limit=200&offset=${offset}`,
    );
    const rows = body.reports;
    for (const g of rows)
      if (g.repository === repo && g.status === "completed" && Date.parse(g.created_at) >= sinceMs)
        groups.push(g);
    if (
      !rows.length ||
      offset + 200 >= body.total ||
      Date.parse(rows[rows.length - 1].created_at) < sinceMs
    )
      break;
  }
  let prGroups = groups
    .filter((g) => g.gh_pr_number)
    .sort((a, b) => a.created_at.localeCompare(b.created_at));
  const byPR = new Map<string, Group[]>();
  for (const g of prGroups) {
    // sibling history must come from the full list, before any --limit
    const k = `${g.gh_pr_number}:${g.name}`;
    byPR.set(k, [...(byPR.get(k) ?? []), g]);
  }
  if (limit) prGroups = prGroups.slice(-limit);
  log(
    `${groups.length} groups, ${prGroups.length} PR runs across ${new Set(prGroups.map((g) => g.gh_pr_number)).size} PRs`,
  );

  // Pass 1: verdicts. "failed" for ground truth means failed *after* retries and
  // orchestration retests (the engine's terminal-failure count), not the raw
  // shard count in test_stats, which counts a failure that a retest recovered.
  const verdicts = new Map<string, Stored>();
  for (const [i, g] of prGroups.entries()) {
    const sha = g.commit;
    const cmp = await cache.remember<Compare>(`cmp:${repo}:${sha}`, async () =>
      required(gh<Compare>(`repos/${repo}/compare/${BASE_REF[repo]}...${sha}`), `compare ${sha}`),
    );
    const baseSha = cmp.merge_base_commit?.sha ?? "";
    const changed = (cmp.files ?? []).map((f) => f.filename);
    const runEnd = g.last_upload_at ?? g.created_at;
    const asOf = new Date(Date.parse(runEnd) + 5 * 60000).toISOString().replace(/\.\d{3}Z$/, "Z");
    const request = {
      composite_identity: {
        repository: repo,
        commit_sha: sha,
        gh_run_id: g.gh_run_id,
        gh_run_attempt: g.gh_run_attempt || "1",
        name: g.name,
      },
      context: contextFor(repo, g.name),
      base_ref: BASE_REF[repo],
      base_sha: baseSha,
      lane: laneFor(g.name),
      changed_files: changed,
      as_of: asOf,
    };
    const { status, body } = await tsio<EngineVerdict & { error?: string }>(
      baseURL,
      "/triage/verdicts",
      {
        method: "POST",
        body: request,
        headers: { "X-API-Key": key, "X-Admin-Key": admin },
      },
    );
    let v: EngineVerdict =
      status >= 400
        ? {
            verdict: `HTTP ${status}`,
            findings: [],
            counts: {},
            confidence: 0,
            error: String(body.error ?? "").slice(0, 200),
          }
        : body;
    const rawFailed = g.test_stats?.failed ?? 0;
    if (
      v.verdict === "ACTION_REQUIRED" &&
      (v.reason ?? "").includes("No test observations") &&
      rawFailed === 0
    )
      // Metadata-only import of a green production run: no cases to judge, green for gating.
      v = {
        verdict: "SUCCESS",
        findings: [],
        counts: { failed: 0, passed: g.test_stats?.passed ?? 0 },
        confidence: 1,
        metadata_only: true,
      };
    verdicts.set(g.id, { v, baseSha, changed, asOf, runEnd });
    log(
      `[${i + 1}/${prGroups.length}] verdict PR ${g.gh_pr_number} ${g.name} ${sha.slice(0, 7)} raw_failed=${rawFailed} effective_failed=${v.counts?.failed} ${v.verdict}`,
    );
  }
  const effectiveFailed = (g: Group) => {
    const c = verdicts.get(g.id)?.v.counts ?? {};
    return "failed" in c ? c.failed : (g.test_stats?.failed ?? 0);
  };

  // Pass 2: ground truth and report rows.
  const rows: BacktestRow[] = [];
  for (const [i, g] of prGroups.entries()) {
    const pr = g.gh_pr_number!;
    const sha = g.commit;
    const stats = g.test_stats ?? {};
    const failed = effectiveFailed(g);
    const { v, baseSha, changed, asOf, runEnd } = verdicts.get(g.id)!;
    const info = await cache.remember<{ state?: string; merged_at?: string | null }>(
      `pr:${repo}:${pr}`,
      async () => required(gh(`repos/${repo}/pulls/${pr}`), `pull ${pr}`),
    );
    const events = await cache.remember<
      { event: string; created_at: string; label?: { name: string } }[]
    >(`events:${repo}:${pr}`, async () =>
      required(
        gh<{ event: string; created_at: string; label?: { name: string } }[]>(
          `repos/${repo}/issues/${pr}/events?per_page=100`,
          true,
        ),
        `events ${pr}`,
      ).filter((e) => e.event === "labeled"),
    );
    const siblings = byPR.get(`${pr}:${g.name}`) ?? [];
    const later = siblings.filter((s) => s.created_at > g.created_at);
    const nextAt = later[0]?.created_at ?? null;
    const labelInWindow = (names: string[]) =>
      events.find(
        (e) =>
          names.includes(e.label?.name ?? "") &&
          e.created_at >= runEnd &&
          (nextAt === null || e.created_at < nextAt),
      )?.created_at ?? null;
    const waived = labelInWindow(VERIFIED_LABELS[repo] ?? []);
    const overridden = labelInWindow(OVERRIDE_LABELS);
    const laterSame = later.some((s) => s.commit === sha && effectiveFailed(s) === 0);
    const laterOther = later.some((s) => s.commit !== sha && effectiveFailed(s) === 0);
    const truth =
      failed === 0
        ? "GREEN"
        : waived
          ? "WAIVED"
          : overridden
            ? "OVERRIDDEN"
            : laterSame
              ? "RERUN_PASSED"
              : laterOther
                ? "FIXED_BY_AUTHOR"
                : "UNRESOLVED";
    const classes: Record<string, number> = {};
    for (const f of v.findings ?? []) classes[f.class] = (classes[f.class] ?? 0) + 1;
    const wouldUnblock = GREEN_VERDICTS.has(v.verdict);
    // A run whose verdict request failed is not scored either way.
    const agree = v.error
      ? null
      : truth === "GREEN" || truth === "WAIVED" || truth === "RERUN_PASSED"
        ? wouldUnblock
        : truth === "FIXED_BY_AUTHOR"
          ? !wouldUnblock
          : null;
    rows.push({
      repo,
      pr,
      name: g.name,
      sha: sha.slice(0, 7),
      run_at: g.created_at,
      state: info.state ?? null,
      merged: Boolean(info.merged_at),
      failed,
      raw_failed: stats.failed ?? 0,
      flaky: stats.flaky ?? 0,
      truth,
      verdict: v.verdict,
      confidence: v.confidence ?? null,
      classes,
      counts: v.counts ?? {},
      agree,
      error: v.error ?? null,
      as_of: asOf,
      base_sha: baseSha.slice(0, 7),
      changed: changed.length,
      waived_at: waived,
      findings: (v.findings ?? []).map((f) => ({
        class: f.class,
        title: (f.full_title ?? "").slice(0, 120),
        file: f.file ?? "",
        reason: (f.reason ?? "").slice(0, 160),
        trunk: {
          runs: f.trunk?.runs ?? null,
          fails: f.trunk?.fails ?? null,
          flaky: f.trunk?.flaky ?? null,
          consecutive_fails: f.trunk?.consecutive_fails ?? null,
        },
        excerpt: (f.pr?.error_excerpt ?? "").slice(0, 120),
        match: f.pr?.signature_match ?? null,
      })),
    });
    log(
      `[${i + 1}/${prGroups.length}] PR ${pr} ${g.name} ${sha.slice(0, 7)} failed=${failed} truth=${truth} verdict=${v.verdict} ${JSON.stringify(classes)}`,
    );
  }

  // 3. report
  const lines = [
    `# Triage backtest: ${repo} since ${since}`,
    "",
    `Runs: ${rows.length} PR runs, ${new Set(rows.map((r) => r.pr)).size} PRs. Verdicts computed with \`as_of\` = run end + 5 min.`,
    "",
  ];
  const summary = new Map<string, number>();
  for (const r of rows)
    summary.set(`${r.truth}|${r.verdict}`, (summary.get(`${r.truth}|${r.verdict}`) ?? 0) + 1);
  lines.push(
    "## Ground truth vs verdict",
    "",
    "| Ground truth | Verdict | Runs |",
    "|---|---|---|",
  );
  for (const k of [...summary.keys()].sort())
    lines.push(`| ${k.split("|")[0]} | ${k.split("|")[1]} | ${summary.get(k)} |`);
  const scored = rows.filter((r) => r.agree !== null && r.truth !== "GREEN");
  if (scored.length)
    lines.push(
      "",
      `Agreement on scoreable failing runs (WAIVED/RERUN_PASSED should unblock, FIXED_BY_AUTHOR should block): ${scored.filter((r) => r.agree).length}/${scored.length}`,
      "",
    );
  const falseExonerations = rows.filter((r) => r.truth === "FIXED_BY_AUTHOR" && r.agree === false);
  lines.push(
    "",
    `False exonerations (author fixed it, engine would have unblocked): ${falseExonerations.length}`,
    "",
  );
  lines.push(
    "## Failing runs",
    "",
    "| Repo | PR | Lane | SHA | Run at | Failed | Ground truth | Verdict | Classes | Would unblock |",
    "|---|---|---|---|---|---|---|---|---|---|",
  );
  for (const r of [...rows].sort(
    (a, b) => a.truth.localeCompare(b.truth) || a.run_at.localeCompare(b.run_at),
  )) {
    if (r.truth === "GREEN") continue;
    const cls = Object.keys(r.classes)
      .sort()
      .map((k) => `${k}×${r.classes[k]}`)
      .join(", ");
    lines.push(
      `| ${r.repo.split("/").pop()} | [${r.pr}](https://github.com/${r.repo}/pull/${r.pr}) | ${r.name} | ${r.sha} | ${r.run_at.slice(0, 16)} | ${r.failed} | ${r.truth} | ${r.verdict} | ${cls} | ${GREEN_VERDICTS.has(r.verdict) ? "yes" : "no"} |`,
    );
  }
  const green = rows.filter((r) => r.truth === "GREEN");
  const badGreen = green.filter((r) => !GREEN_VERDICTS.has(r.verdict));
  lines.push(
    "",
    `Green runs: ${green.length}; engine disagreed on ${badGreen.length}: ` +
      badGreen
        .slice(0, 20)
        .map((r) => `PR ${r.pr} ${r.name} → ${r.verdict}`)
        .join(", "),
    "",
  );
  writeFileSync(out, lines.join("\n"));
  writeJSON(out.replace(/\.md$/, "") + ".json", rows, true);
  log(`wrote ${out}`);
}

main().catch((error) => {
  log(String(error));
  process.exit(1);
});

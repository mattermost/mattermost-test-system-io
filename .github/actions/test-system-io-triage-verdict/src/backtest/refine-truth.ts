/**
 * Refine FIXED_BY_AUTHOR by whether the failing tests were unique to this PR.
 *
 * For each FIXED_BY_AUTHOR run, count how many of its failing tests also failed
 * on at least one other PR (same lane) within ±7 days of the run, using raw
 * observations only (no engine rules). If every failing test recurs elsewhere
 * the "fix" was most likely a flake not recurring -> RECURRING_ELSEWHERE; if at
 * least one failing test failed only on this PR -> LIKELY_REGRESSION; INFRA-only
 * runs -> FIXED_BY_AUTHOR_INFRA.
 *
 *   npm run refine-truth -- backtest-mobile.json backtest-mobile.refined.json
 */
import {
  log,
  parseArgs,
  psql,
  q,
  readJSON,
  writeJSON,
  type BacktestRow,
  GREEN_VERDICTS,
} from "./lib";

function main(): void {
  const { positional } = parseArgs(process.argv.slice(2));
  const [input, output] = positional;
  if (!input || !output) throw new Error("usage: refine-truth <backtest.json> <refined.json>");
  const rows = readJSON<BacktestRow[]>(input, []);
  for (const r of rows) {
    if (r.truth !== "FIXED_BY_AUTHOR") continue;
    const titles = r.findings.filter((f) => f.class !== "INFRA").map((f) => f.title);
    if (!titles.length) {
      r.truth = "FIXED_BY_AUTHOR_INFRA";
      continue;
    }
    const sql = `select count(*) filter (where n_other>0), count(*) from (
      select i.id, count(distinct o.gh_pr_number) filter (where o.gh_pr_number<>${r.pr} and o.status in ('failed','timedOut','interrupted')) n_other
      from test_identities i join test_observations o on o.identity_id=i.id join report_groups g on g.id=o.report_group_id
      where i.repository=${q(r.repo)} and g.name=${q(r.name)} and o.branch_kind='pr'
        and o.observed_at between timestamptz '${r.run_at}' - interval '7 days' and timestamptz '${r.run_at}' + interval '7 days'
        and i.normalized_title = ANY(ARRAY[${titles.map(q).join(",")}])
      group by i.id) x`;
    const [recurring, total] = psql(sql)
      .split("|")
      .map((s) => Number(s || 0));
    r.recurring_tests = recurring;
    r.failing_tests = total;
    r.truth = total && recurring === total ? "RECURRING_ELSEWHERE" : "LIKELY_REGRESSION";
  }
  writeJSON(output, rows, true);
  const counts = new Map<string, number>();
  for (const r of rows)
    if (/^(LIKELY|RECURRING|FIXED)/.test(r.truth)) {
      const k = `${r.truth} green=${GREEN_VERDICTS.has(r.verdict)}`;
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
  for (const k of [...counts.keys()].sort()) log(`${k}: ${counts.get(k)}`);
}

main();

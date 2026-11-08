/**
 * Longest-first ordering for dispatch units, driven by the per-file durations
 * of the last complete run of the same lane on the base branch.
 *
 * Source: the public read API only (no bearer). `GET /api/v1/reports` filtered
 * by repository + lane name (see durationLaneNames: a base-branch run of a lane
 * is stored under its own name), then `GET /api/v1/reports/{id}/suites` for the
 * newest matching group. `/reports` filters only by repository, commit and name
 * (reports/handlers.go List), so branch, framework and status are matched here.
 * Any failure throws; the caller logs it and keeps the alphabetical order.
 *
 * Ordering is deterministic for a given duration snapshot: known durations
 * descend, unknown ones follow alphabetically, and ties break by spec path. The
 * dispatch-units hash covers order (orchestration/runs.go HashUnits), so a begin
 * retry matches the stored run only when it reads the same snapshot. A base-branch
 * run that completes between a begin attempt and its retry changes the order and
 * turns the retry into a 409 conflict. A fresh workflow attempt has a new
 * identity, so it is not affected.
 */

export interface DurationHistory {
  /** Spec file path as the suite reports it (`file_path`) -> total test time in ms. */
  durations: Map<string, number>;
  runId: string;
  commit: string;
  /** The lane name the durations were read from (see durationLaneNames). */
  name: string;
}

export interface FetchDurationHistoryOptions {
  baseURL: string;
  repository: string;
  name: string;
  framework: string;
  branch: string;
  timeoutMs: number;
  fetchImpl?: typeof fetch;
}

/**
 * Newest-first window of report groups scanned per lane name. A base-branch lane name holds almost only that
 * branch's runs, so a short window finds one. The PR's own name, the last fallback, mostly holds PR runs; the API
 * caps `limit` at 200. Each listed group costs the server a stats aggregation, so the window stays small where it can.
 */
export const DURATION_LOOKUP_LIMIT = 200;
export const BASE_LANE_LOOKUP_LIMIT = 20;
/** A report group whose every expected report landed (migrations 000019: in_progress, completed, incomplete). */
const GROUP_COMPLETED = "completed";
const SUBPATH = "-subpath";

/**
 * The lane names a base-branch run of this lane can be stored under, in the order to try. The run name is the
 * commit-status context without `e2e-test/`, with `/` as `-` (mattermost's e2e-tests-*-template.yml
 * ci/composite-identity), and a master run's context adds `/master` before a subpath deployment's `/subpath`
 * (e2e-tests-playwright.yml build-vars). So PR lane `playwright-full-enterprise` reads `playwright-full-enterprise-master`,
 * and `playwright-full-enterprise-subpath` reads `playwright-full-enterprise-master-subpath`, then the non-subpath
 * master lane: the specs are the same files, only the deployment differs. The lane's own name comes last, for a
 * caller whose base-branch runs keep it.
 */
export function durationLaneNames(name: string, branch: string): string[] {
  const seg = `-${branch.replace(/\//g, "-")}`;
  const subpath = name.endsWith(SUBPATH);
  const stem = subpath ? name.slice(0, -SUBPATH.length) : name;
  const baseStem = stem.endsWith(seg) ? stem : `${stem}${seg}`;
  const names = subpath ? [`${baseStem}${SUBPATH}`, baseStem] : [baseStem];
  if (!names.includes(name)) names.push(name);
  return names;
}

interface ReportSummaryLike {
  id: string;
  branch: string;
  commit: string;
  framework: string;
  status: string;
  gh_run_id: string;
}

interface SuiteRowLike {
  file_path?: string | null;
  duration_ms?: number | null;
}

/** GETs url and parses its JSON body; throws on a non-200 status, a network error, or after timeoutMs. */
async function getJSON<T>(fetchImpl: typeof fetch, url: string, timeoutMs: number): Promise<T> {
  const res = await fetchImpl(url, {
    headers: { Accept: "application/json" },
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (res.status !== 200) {
    throw new Error(`GET ${new URL(url).pathname} returned HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}

/**
 * Per-file durations from the newest completed run of this lane on opts.branch, tried under each
 * name durationLaneNames gives. Throws when no such run, or no duration, is found.
 */
export async function fetchDurationHistory(
  opts: FetchDurationHistoryOptions,
): Promise<DurationHistory> {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const names = durationLaneNames(opts.name, opts.branch);
  let group: ReportSummaryLike | undefined;
  let name = "";
  for (const candidate of names) {
    const limit = candidate === opts.name ? DURATION_LOOKUP_LIMIT : BASE_LANE_LOOKUP_LIMIT;
    const query = new URLSearchParams({
      repository: opts.repository,
      name: candidate,
      limit: String(limit),
    });
    const list = await getJSON<{ reports?: ReportSummaryLike[] }>(
      fetchImpl,
      `${opts.baseURL}/api/v1/reports?${query.toString()}`,
      opts.timeoutMs,
    );
    group = (list.reports ?? []).find(
      (r) =>
        r.branch === opts.branch && r.framework === opts.framework && r.status === GROUP_COMPLETED,
    );
    if (group) {
      name = candidate;
      break;
    }
  }
  if (!group) {
    throw new Error(
      `no completed ${opts.framework} run on ${opts.branch} under ${names.map((n) => `"${n}"`).join(", ")}`,
    );
  }

  const suites = await getJSON<{ suites?: SuiteRowLike[] }>(
    fetchImpl,
    `${opts.baseURL}/api/v1/reports/${encodeURIComponent(group.id)}/suites`,
    opts.timeoutMs,
  );
  // The server sets each suite row's duration_ms to the sum of that row's own
  // cases (apps/server/internal/ingest/consolidate.go). Nested describe rows
  // inherit the file path and hold their own cases, so a file's total is the
  // sum over all of its rows. That is total test time, not wall-clock.
  const durations = new Map<string, number>();
  for (const s of suites.suites ?? []) {
    if (!s.file_path || typeof s.duration_ms !== "number" || s.duration_ms <= 0) continue;
    durations.set(s.file_path, (durations.get(s.file_path) ?? 0) + s.duration_ms);
  }
  if (durations.size === 0) {
    throw new Error(`run ${group.gh_run_id} on ${opts.branch} has no per-file durations`);
  }
  return { durations, runId: group.gh_run_id, commit: group.commit, name };
}

/**
 * Duration for a dispatch spec path. The suite file path and the spec path can
 * differ in their leading directories, so an exact match wins, else the longest
 * suffix match on a `/` boundary.
 */
export function durationOf(
  spec: string,
  durations: ReadonlyMap<string, number>,
): number | undefined {
  const exact = durations.get(spec);
  if (exact !== undefined) return exact;
  let best: number | undefined;
  let bestLen = -1;
  for (const [file, ms] of durations) {
    if (spec.endsWith("/" + file) || file.endsWith("/" + spec)) {
      if (file.length > bestLen) {
        best = ms;
        bestLen = file.length;
      }
    }
  }
  return best;
}

/**
 * Longest first. Specs without history follow alphabetically, and ties break
 * by key. With no history at all the input order is returned unchanged.
 */
export function orderLongestFirst<T>(
  items: readonly T[],
  keyOf: (item: T) => string,
  durations: ReadonlyMap<string, number>,
): T[] {
  if (durations.size === 0) return [...items];
  return [...items].sort((a, b) => {
    const ka = keyOf(a);
    const kb = keyOf(b);
    const da = durationOf(ka, durations);
    const db = durationOf(kb, durations);
    if (da !== undefined && db !== undefined && da !== db) return db - da;
    if (da !== undefined && db === undefined) return -1;
    if (da === undefined && db !== undefined) return 1;
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
}

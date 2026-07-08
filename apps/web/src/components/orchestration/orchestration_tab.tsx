/**
 * Composition root for the Orchestration tab.
 *
 * Fetches the run snapshot for the supplied composite identity and renders
 * the same visual structure as the Report Group tab: a `ReportSummary`
 * header followed by a per-spec list whose row layout mirrors
 * `TestSuitesView` (ordinal, chevron, status icon, file icon, spec path,
 * right-side metadata). The data underneath is per-dispatch-unit (one row
 * per spec file, with attempt history and worker info), but the visual
 * tokens are shared with the Reports tab so the two feel like one family.
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Loader2,
  AlertTriangle,
  Inbox,
  CheckCircle2,
  XCircle,
  MinusCircle,
  Clock,
  ChevronDown,
  ChevronRight,
  RotateCcw,
  FileCode,
  Search,
  X,
} from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { useClientConfig, useOrchestrationRun } from '@/services/api';
import { subscribeToOrchestrationRun } from '@/services/websocket';
import {
  compositeIdentityKey,
  type CompositeIdentity,
  type RunSnapshot,
  type SnapshotAttempt,
  type SnapshotTestCase,
  type SnapshotUnit,
  type TestCaseStatus,
  type UnitState,
} from '@/types/orchestration';
import { ReportSummary } from '@/components/report_summary';
import { PaginationBar } from '@/components/ui/pagination_bar';
import { ScreenshotGallery } from '@/components/ui/screenshot-gallery';
import { HighlightText, workerSlot } from '@/components/test_suites';

interface OrchestrationTabProps {
  identity: CompositeIdentity;
}

function formatDuration(ms: number | null | undefined): string {
  if (ms == null) return '—';
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
  const m = Math.floor(s / 60);
  const rs = Math.round(s - m * 60);
  return `${m}m ${rs}s`;
}

/**
 * Aggregate the per-test status across every entry that ran for a single
 * test (across ALL orchestration leases AND any Playwright internal
 * retries). The rule:
 *
 *   - any passed AND any failed     → flaky (counted as a green outcome)
 *   - any passed (no failures)      → passed
 *   - any failed (no passes)        → failed
 *   - only skipped (no pass/fail)   → skipped
 *
 * This is the canonical "if any attempt passed, the test is at-worst
 * flaky" rule the Reports tab uses for its overall stats. Failures
 * include 'failed', 'timedOut', and 'interrupted'; an explicit 'flaky'
 * status from Playwright is treated the same as passed-with-history.
 */
function aggregateTestStatus(
  entries: SnapshotTestCase[],
): 'passed' | 'failed' | 'flaky' | 'skipped' {
  let everPassed = false;
  let everFailed = false;
  let everSkipped = false;
  for (const e of entries) {
    if (e.status === 'passed' || e.status === 'flaky') everPassed = true;
    else if (e.status === 'failed' || e.status === 'timedOut' || e.status === 'interrupted')
      everFailed = true;
    else if (e.status === 'skipped') everSkipped = true;
  }
  if (everPassed && everFailed) return 'flaky';
  if (everPassed) return 'passed';
  if (everFailed) return 'failed';
  if (everSkipped) return 'skipped';
  return 'failed';
}

/**
 * Walk every test entry across ALL orchestration attempts on a unit and
 * return per-test aggregate statuses keyed by full_title. Used both for
 * spec-row chip counts and for the run-level summary.
 */
function aggregateUnitTests(
  unit: SnapshotUnit,
): Map<string, 'passed' | 'failed' | 'flaky' | 'skipped'> {
  const byTitle = new Map<string, SnapshotTestCase[]>();
  for (const a of unit.attempts) {
    for (const tc of a.test_cases ?? []) {
      const list = byTitle.get(tc.full_title);
      if (list) list.push(tc);
      else byTitle.set(tc.full_title, [tc]);
    }
  }
  const out = new Map<string, 'passed' | 'failed' | 'flaky' | 'skipped'>();
  for (const [title, entries] of byTitle) {
    out.set(title, aggregateTestStatus(entries));
  }
  return out;
}

/**
 * Run-level passed / failed / flaky / skipped counts based on the
 * any-passed-AND-any-failed rule. Mirrors what the Reports tab summary
 * line shows.
 */
function computeTestCaseCounts(units: SnapshotUnit[]): {
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  total: number;
} {
  let passed = 0;
  let failed = 0;
  let flaky = 0;
  let skipped = 0;
  for (const u of units) {
    for (const status of aggregateUnitTests(u).values()) {
      if (status === 'passed') passed++;
      else if (status === 'failed') failed++;
      else if (status === 'flaky') flaky++;
      else skipped++;
    }
  }
  return { passed, failed, flaky, skipped, total: passed + failed + flaky + skipped };
}

/**
 * Wall-clock duration partitioned into setup, first-pass, and retest.
 *
 * Each phase's window is (latest reported_at) − (earliest created_at)
 * across the attempts in that phase. Setup is the run-begin → first
 * checkout gap (cloud-init, server start, framework prepare). Attempt
 * index 0 on a unit is first-pass; subsequent indices are retests.
 *
 * The math has to be wall-clock per phase, not Σ test_case.duration_ms,
 * because the latter is CPU-time across parallel workers and overstates
 * elapsed time by roughly the worker count — N workers each running for
 * D minutes in parallel sum to N·D of CPU but only D elapsed.
 *
 * Attempts without a reported_at are excluded from the latest-end
 * calculation so in-flight values stay monotonic until the worker
 * reports.
 */
function computeTestDurations(
  units: SnapshotUnit[],
  runStartedAt: string,
): {
  setupDurationMs: number;
  durationMs: number;
  retestDurationMs: number;
  /** First-attempt earliest start, ISO. Drives the timeline's "First test" segment. */
  firstTestAt: string | null;
  /** First retest-attempt earliest start, ISO. Null when no retest happened. */
  firstRetestAt: string | null;
  /** Latest reported_at across all attempts, ISO. Drives the timeline's "Last test" segment. */
  lastTestAt: string | null;
} {
  let firstStart = Number.POSITIVE_INFINITY;
  let firstEnd = 0;
  let retestStart = Number.POSITIVE_INFINITY;
  let retestEnd = 0;
  for (const u of units) {
    for (let i = 0; i < u.attempts.length; i++) {
      const a = u.attempts[i]!;
      const startMs = Date.parse(a.created_at);
      const endMs = a.reported_at ? Date.parse(a.reported_at) : NaN;
      const isRetest = i > 0;
      if (Number.isFinite(startMs)) {
        if (isRetest) retestStart = Math.min(retestStart, startMs);
        else firstStart = Math.min(firstStart, startMs);
      }
      if (Number.isFinite(endMs)) {
        if (isRetest) retestEnd = Math.max(retestEnd, endMs);
        else firstEnd = Math.max(firstEnd, endMs);
      }
    }
  }
  const runStartMs = Date.parse(runStartedAt);
  const setupDurationMs =
    Number.isFinite(runStartMs) && Number.isFinite(firstStart)
      ? Math.max(0, firstStart - runStartMs)
      : 0;
  const durationMs =
    Number.isFinite(firstStart) && firstEnd > 0 ? Math.max(0, firstEnd - firstStart) : 0;
  const retestDurationMs =
    Number.isFinite(retestStart) && retestEnd > 0 ? Math.max(0, retestEnd - retestStart) : 0;
  const firstTestAt = Number.isFinite(firstStart) ? new Date(firstStart).toISOString() : null;
  const firstRetestAt = Number.isFinite(retestStart) ? new Date(retestStart).toISOString() : null;
  const lastEndMs = Math.max(firstEnd, retestEnd);
  const lastTestAt = lastEndMs > 0 ? new Date(lastEndMs).toISOString() : null;
  return {
    setupDurationMs,
    durationMs,
    retestDurationMs,
    firstTestAt,
    firstRetestAt,
    lastTestAt,
  };
}

interface SpecRow {
  unit: SnapshotUnit;
  /**
   * Aggregate state derived from per-test statuses (any-passed AND
   * any-failed → flaky; failed wins over skipped). Pending/leased pass
   * straight through from unit.state since there are no test_cases yet.
   */
  effectiveState: UnitState;
  /** True when the spec is flaky (any test had a pass + a failure). */
  flaky: boolean;
  /** Per-test status counts within this unit. */
  testCounts: { passed: number; failed: number; flaky: number; skipped: number; total: number };
  /** All attempts on this unit, oldest-first. */
  attempts: SnapshotAttempt[];
  /** Most recent attempt, when one exists. */
  latest: SnapshotAttempt | null;
}

/**
 * Build one spec row per dispatch unit, deriving aggregate state from
 * per-test statuses. The row turns yellow/flaky when any test in it
 * passed at least once, regardless of how the unit's final lease ended;
 * red only when every test failed every time. Pending/leased units
 * (no test_cases yet) keep their unit.state-derived display.
 */
function buildSpecRows(units: SnapshotUnit[]): SpecRow[] {
  return units.map((u) => {
    const attempts = u.attempts;
    const latest: SnapshotAttempt | null =
      attempts.length > 0 ? attempts[attempts.length - 1]! : null;
    const aggregated = aggregateUnitTests(u);
    let passed = 0;
    let failed = 0;
    let flakyTests = 0;
    let skipped = 0;
    for (const s of aggregated.values()) {
      if (s === 'passed') passed++;
      else if (s === 'failed') failed++;
      else if (s === 'flaky') flakyTests++;
      else skipped++;
    }
    const total = passed + failed + flakyTests + skipped;
    let effectiveState: UnitState;
    let flaky = false;
    // Active lifecycle states (pending / leased / abandoned) win over any
    // prior test_case outcome. Without this, a unit that just got
    // re-leased for a retest would still show its previous failed result
    // instead of the blue spinner — the dashboard couldn't tell that the
    // unit is currently running again.
    if (u.state === 'pending' || u.state === 'leased' || u.state === 'abandoned') {
      effectiveState = u.state;
    } else if (total === 0) {
      // Terminal state but no test_cases (rare — late report not yet
      // ingested). Fall back to the unit's stored state.
      effectiveState = u.state;
    } else if (failed > 0) {
      effectiveState = 'completed_fail';
    } else if (flakyTests > 0) {
      effectiveState = 'completed_pass';
      flaky = true;
    } else if (passed > 0) {
      effectiveState = 'completed_pass';
    } else {
      effectiveState = 'completed_skipped';
    }
    return {
      unit: u,
      effectiveState,
      flaky,
      testCounts: { passed, failed, flaky: flakyTests, skipped, total },
      attempts,
      latest,
    };
  });
}

function mapAttemptStatusToUnitState(s: TestCaseStatus): UnitState {
  switch (s) {
    case 'passed':
    case 'flaky':
      return 'completed_pass';
    case 'skipped':
      return 'completed_skipped';
    case 'failed':
    case 'timedOut':
    case 'interrupted':
      return 'completed_fail';
  }
}

interface StatusIconBundle {
  Icon: typeof CheckCircle2;
  className: string;
  spin?: boolean;
}

/**
 * Map a unit's effective state to the same icon palette the Test Suites
 * row uses (CheckCircle2 / XCircle / AlertTriangle / MinusCircle), plus
 * Clock for not-yet-dispatched and a spinning Loader2 for in-flight. When
 * `flaky` is true (passed-after-retest) the icon is yellow AlertTriangle
 * — same convention the Reports tab uses for Playwright's flaky enum.
 *
 * `isRetest` shifts the in-flight color from blue → orange so a unit that
 * is being run for at least the second time stands out from a fresh
 * first-attempt lease.
 */
function statusIconForRow(
  state: UnitState,
  flaky: boolean,
  isRetest: boolean = false,
): StatusIconBundle {
  if (flaky && state === 'completed_pass') {
    return { Icon: AlertTriangle, className: 'text-yellow-500 dark:text-yellow-400' };
  }
  switch (state) {
    case 'pending':
      return { Icon: Clock, className: 'text-gray-400 dark:text-gray-500' };
    case 'leased':
      return isRetest
        ? { Icon: Loader2, className: 'text-orange-500 dark:text-orange-400', spin: true }
        : { Icon: Loader2, className: 'text-blue-500 dark:text-blue-400', spin: true };
    case 'completed_pass':
      return { Icon: CheckCircle2, className: 'text-green-500' };
    case 'completed_fail':
      return { Icon: XCircle, className: 'text-red-500' };
    case 'completed_skipped':
      return { Icon: MinusCircle, className: 'text-gray-400' };
    case 'abandoned':
      return { Icon: AlertTriangle, className: 'text-amber-500 dark:text-amber-400' };
  }
}

/**
 * Pulls the describe-block title out of a test's full_title for display
 * under the spec path. The Playwright reporter's full_title format is
 *   "<project> > <file> > <describe...> > <test title>"
 * where <file> can be either the basename ("foo.spec.ts") or a path
 * relative to the playwright config root ("dir/foo.spec.ts"). We anchor
 * on the spec-file extension to skip past the project + file segments
 * and return everything between them and the leaf test title. Returns
 * "" when the test isn't inside a describe block, in which case the
 * caller should omit the second line entirely instead of repeating the
 * spec path.
 */
function deriveSuiteTitleFromRow(row: SpecRow): string {
  const tc = row.latest?.test_cases?.[0];
  if (!tc || !tc.full_title) return '';
  // Strip the leaf test title from the end. Playwright joins describe/it
  // segments with " > "; Cypress's Mochawesome reporter joins them with a
  // single space. Falling back to the older split-only path keeps the
  // function defensive when title is empty.
  let suite = tc.full_title;
  if (tc.title) {
    const playwrightSuffix = ' > ' + tc.title;
    if (suite.endsWith(playwrightSuffix)) {
      suite = suite.slice(0, -playwrightSuffix.length);
    } else if (suite === tc.title) {
      return '';
    } else if (suite.endsWith(' ' + tc.title)) {
      suite = suite.slice(0, -(tc.title.length + 1));
    }
  }
  const parts = suite.split(' > ');
  const fileIdx = parts.findIndex((p) => /\.spec\.[tj]sx?$/.test(p));
  if (fileIdx >= 0) {
    return parts.slice(fileIdx + 1).join(' > ');
  }
  return suite;
}

interface SpecListRowProps {
  row: SpecRow;
  rowNumber: number;
  /** Active normalized (lowercased) search string to highlight in matched text. */
  searchQuery: string;
}

function SpecListRow({ row, rowNumber, searchQuery }: SpecListRowProps) {
  const [expanded, setExpanded] = useState(false);
  // A leased unit that already has a prior attempt is a retest in flight
  // — a worker is currently re-running this spec because an earlier lease
  // for it ended in failure or got abandoned.
  const isRetestInFlight = row.effectiveState === 'leased' && row.unit.lease_count > 1;

  // Live elapsed counter while the unit is leased. Ticks every 1s so the
  // user sees the wall-clock time the worker has been running this spec.
  // Once the lease releases, `actual_duration_ms` from the attempt takes
  // over (rendered below).
  const leaseIssuedAtMs = useMemo(() => {
    const t = row.unit.current_lease?.issued_at;
    if (!t) return null;
    const ms = Date.parse(t);
    return Number.isFinite(ms) ? ms : null;
  }, [row.unit.current_lease?.issued_at]);
  const isLeased = row.effectiveState === 'leased' && leaseIssuedAtMs != null;
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    if (!isLeased) return;
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [isLeased]);
  const liveElapsedMs =
    isLeased && leaseIssuedAtMs != null ? Math.max(0, nowMs - leaseIssuedAtMs) : null;
  const { Icon, className, spin } = statusIconForRow(
    row.effectiveState,
    row.flaky,
    isRetestInFlight,
  );
  // The spec row is expandable when there are tests to detail. A pending
  // unit (no test_cases yet) just renders a non-expandable status row.
  const canExpand = row.testCounts.total > 0;

  // Derive the suite title (test.describe block name) from the latest
  // attempt's first test_case full_title — matches what the Reports tab
  // renders below the spec path. Falls back to nothing when there's no
  // describe block, in which case the subline is omitted.
  const suiteTitle = deriveSuiteTitleFromRow(row);

  // Per-test-case counts for the right-side badges. Mirrors the Reports
  // row's `2 specs / ✓1 / ✗1` indicator layout — counts come from the
  // aggregated per-test statuses across every attempt on this unit.
  const { passed, failed, flaky, skipped, total } = row.testCounts;

  return (
    <div
      className={`-mx-2 px-2 rounded-lg transition-colors ${
        expanded ? 'bg-blue-50 dark:bg-blue-900/20' : ''
      }`}
    >
      <button
        type="button"
        onClick={() => canExpand && setExpanded((v) => !v)}
        disabled={!canExpand}
        className={`w-full py-2.5 pr-3 text-left transition-colors ${
          canExpand
            ? expanded
              ? 'cursor-pointer hover:bg-blue-100/50 dark:hover:bg-blue-900/30'
              : 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700/50'
            : 'cursor-default'
        }`}
      >
        <div className="flex items-center justify-between">
          <div className="flex min-w-0 items-center gap-2">
            <span className="w-6 flex-shrink-0 text-right text-sm text-gray-400 dark:text-gray-500">
              {rowNumber}
            </span>
            {canExpand ? (
              <ChevronRight
                className={`h-4 w-4 flex-shrink-0 text-gray-400 transition-transform dark:text-gray-500 ${
                  expanded ? 'rotate-90' : ''
                }`}
              />
            ) : (
              <span className="h-4 w-4 flex-shrink-0" />
            )}
            <Icon className={`h-4 w-4 flex-shrink-0 ${className} ${spin ? 'animate-spin' : ''}`} />
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 truncate text-sm font-medium text-gray-900 dark:text-white">
                <FileCode className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
                <span className="truncate">
                  <HighlightText text={row.unit.spec_path} search={searchQuery} />
                </span>
                {row.attempts.length > 0 && (
                  <span className="ml-1 inline-flex flex-shrink-0 items-center gap-0.5">
                    {row.attempts.map((a, i) => {
                      const siblingJobNames = row.attempts.map((att) => att.gh_job_name);
                      const slot = workerSlot(a.gh_job_name, i + 1, siblingJobNames);
                      const inFlight = !a.status && !a.expired;
                      const passed = a.status === 'passed' || a.status === 'flaky';
                      const failed =
                        a.expired ||
                        a.status === 'failed' ||
                        a.status === 'timedOut' ||
                        a.status === 'interrupted';
                      const color = inFlight
                        ? 'bg-blue-200 text-blue-700 dark:bg-blue-800 dark:text-blue-200'
                        : passed
                          ? 'bg-green-200 text-green-700 dark:bg-green-800 dark:text-green-200'
                          : failed
                            ? 'bg-red-200 text-red-700 dark:bg-red-800 dark:text-red-200'
                            : 'bg-gray-200 text-gray-700 dark:bg-gray-700 dark:text-gray-200';
                      return (
                        <span
                          key={a.id}
                          className={`inline-flex h-4 min-w-4 items-center justify-center rounded px-0.5 text-[10px] font-semibold ${color}`}
                          title={a.gh_job_name || `Attempt ${i + 1}`}
                        >
                          {slot}
                        </span>
                      );
                    })}
                  </span>
                )}
              </p>
              {suiteTitle && (
                <p className="truncate text-sm text-gray-500 dark:text-gray-400">
                  <HighlightText text={suiteTitle} search={searchQuery} />
                </p>
              )}
            </div>
          </div>
          <div className="flex flex-shrink-0 items-center gap-3 text-sm">
            {liveElapsedMs != null ? (
              <span
                className="inline-flex items-center gap-1 text-blue-600 dark:text-blue-400"
                title="Time elapsed since the worker leased this spec"
              >
                <Clock className="h-3 w-3" />
                {formatDuration(liveElapsedMs)}
              </span>
            ) : (
              (() => {
                // Per-attempt wall-clock split. First-pass is the first
                // orchestration lease — its `actual_duration_ms` already
                // covers all Playwright `--retries` reruns within that
                // single invocation. Retest is the sum of every later
                // orchestration lease's wall-clock. Showing them as
                // `first + retest` makes it visible that a flaky spec
                // burned both windows.
                const attempts = row.attempts;
                if (attempts.length === 0) return null;
                const firstMs = attempts[0]!.actual_duration_ms ?? null;
                let retestMs = 0;
                for (let i = 1; i < attempts.length; i++) {
                  const d = attempts[i]!.actual_duration_ms;
                  if (d != null) retestMs += d;
                }
                if (firstMs == null && retestMs === 0) return null;
                // Threshold against first-pass + retest so a flaky spec
                // that burned both windows surfaces on the slow-spec
                // band even when each individual lease is under the
                // threshold. 2–3 min orange, 3 min+ red.
                const totalMs = (firstMs ?? 0) + retestMs;
                const cls =
                  totalMs >= 180_000
                    ? 'text-red-600 dark:text-red-400'
                    : totalMs >= 120_000
                      ? 'text-orange-600 dark:text-orange-400'
                      : 'text-gray-500 dark:text-gray-400';
                const slowSuffix =
                  totalMs >= 180_000
                    ? ' — long-running spec (>3m)'
                    : totalMs >= 120_000
                      ? ' — slow spec (2–3m)'
                      : '';
                return (
                  <span
                    className={`inline-flex items-center gap-1 ${cls}`}
                    title={
                      (retestMs > 0
                        ? 'First-pass lease + orchestration retest lease(s) (each lease wall-clock includes its own Playwright --retries reruns)'
                        : 'First-pass lease wall-clock (includes any Playwright --retries reruns within the lease)') +
                      slowSuffix
                    }
                  >
                    <Clock className="h-3 w-3" />
                    {firstMs != null ? formatDuration(firstMs) : '—'}
                    {retestMs > 0 && (
                      <span className="opacity-70">
                        {' + '}
                        {formatDuration(retestMs)}
                      </span>
                    )}
                  </span>
                );
              })()
            )}
            {total > 0 && (
              <span className="text-gray-600 dark:text-gray-300">
                {total} {total === 1 ? 'test' : 'tests'}
              </span>
            )}
            {passed > 0 && (
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                {passed}
              </span>
            )}
            {flaky > 0 && (
              <span
                className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400"
                title="Passed after at least one failed lease (flaky)"
              >
                <AlertTriangle className="h-3 w-3" />
                {flaky}
              </span>
            )}
            {failed > 0 && (
              <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                <XCircle className="h-3 w-3" />
                {failed}
              </span>
            )}
            {skipped > 0 && (
              <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                <MinusCircle className="h-3 w-3" />
                {skipped}
              </span>
            )}
          </div>
        </div>
      </button>
      {expanded && (
        <div className="mb-3 ml-6 border-l-2 border-gray-200 pl-4 dark:border-gray-600">
          <TestCasesList row={row} rowNumber={rowNumber} searchQuery={searchQuery} />
        </div>
      )}
    </div>
  );
}

/**
 * Renders the per-attempt failure screenshots. Reads `test_cases[].attachments.screenshots[]`
 * and resolves each storage key via /files/{key}, the same redirect path
 * used for Report Group screenshots.
 */
/**
 * Per-test-case list rendered inside an expanded SpecListRow. Mirrors the
 * Reports tab's `7.1`, `7.2`, ... numbering and per-test expansion: each
 * test row is clickable when it has multiple attempts (Playwright internal
 * retries OR orchestration retests) or carries an error. Expanding shows
 * each attempt with status, duration, the error stack in a dark pre block,
 * and any per-attempt screenshot thumbnails.
 *
 * Data: walks every orchestration attempt on the unit, collects entries
 * per `full_title`, and orders them chronologically (the unit's attempts
 * are already oldest-first). A test that ran twice across two leases —
 * once failing, once passing — surfaces as one row labelled "flaky" with a
 * 2-attempt expansion. A test that ran twice and failed both times shows
 * one "failed" row with a 2-attempt expansion.
 */
/**
 * One per-test entry collected for the expanded row. Bundles the test
 * case payload with the worker job name from the parent attempt and
 * the orchestration-attempt index it came from. `attemptIdx === 0` is
 * the first lease on the unit (first-pass — possibly with Playwright
 * `--retries` re-runs producing multiple test_cases at idx 0); any
 * higher index is a retest from the orchestration retest queue, which
 * usually means a different worker.
 */
interface TestCaseEntry {
  tc: SnapshotTestCase;
  ghJobName: string;
  attemptIdx: number;
}

function TestCasesList({
  row,
  rowNumber,
  searchQuery,
}: {
  row: SpecRow;
  rowNumber: number;
  searchQuery: string;
}) {
  const byTitle = new Map<string, TestCaseEntry[]>();
  const order: string[] = [];
  for (let attemptIdx = 0; attemptIdx < row.attempts.length; attemptIdx++) {
    const a = row.attempts[attemptIdx]!;
    for (const tc of a.test_cases ?? []) {
      const entry: TestCaseEntry = { tc, ghJobName: a.gh_job_name, attemptIdx };
      const list = byTitle.get(tc.full_title);
      if (list) {
        list.push(entry);
      } else {
        byTitle.set(tc.full_title, [entry]);
        order.push(tc.full_title);
      }
    }
  }
  if (order.length === 0) return null;
  return (
    <div className="space-y-2 py-2">
      {order.map((fullTitle, idx) => (
        <TestCaseRow
          key={fullTitle}
          entries={byTitle.get(fullTitle)!}
          rowNumber={rowNumber}
          subIndex={idx + 1}
          searchQuery={searchQuery}
        />
      ))}
    </div>
  );
}

function TestCaseRow({
  entries,
  rowNumber,
  subIndex,
  searchQuery,
}: {
  entries: TestCaseEntry[];
  rowNumber: number;
  subIndex: number;
  searchQuery: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const final = entries[entries.length - 1]!.tc;
  const everFailed = entries.some(
    ({ tc }) => tc.status === 'failed' || tc.status === 'timedOut' || tc.status === 'interrupted',
  );
  // Final pass after an earlier failure → flaky. Otherwise inherit final.
  const flaky = final.status === 'flaky' || (final.status === 'passed' && everFailed);
  const effectiveState: UnitState = flaky
    ? 'completed_pass'
    : mapAttemptStatusToUnitState(final.status);
  const ai = statusIconForRow(effectiveState, flaky);
  const TestIcon = ai.Icon;

  const hasMultiple = entries.length > 1;
  const hasErrors = entries.some(({ tc }) => tc.error_message || tc.error_stack);
  const hasScreenshots = entries.some(({ tc }) => (tc.attachments?.screenshots?.length ?? 0) > 0);
  const canExpand = hasMultiple || hasErrors || hasScreenshots;

  return (
    <div className="text-sm">
      <div
        className={`flex items-center gap-2 py-1 ${
          canExpand
            ? 'cursor-pointer rounded -mx-1 px-1 hover:bg-gray-50 dark:hover:bg-gray-800'
            : ''
        }`}
        role={canExpand ? 'button' : undefined}
        tabIndex={canExpand ? 0 : undefined}
        onClick={() => canExpand && setExpanded((v) => !v)}
        onKeyDown={(e) => {
          if (canExpand && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault();
            setExpanded((v) => !v);
          }
        }}
      >
        {canExpand ? (
          expanded ? (
            <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
          )
        ) : (
          <span className="w-3.5 flex-shrink-0" />
        )}
        <span className="w-10 flex-shrink-0 text-right text-xs font-medium text-gray-400 dark:text-gray-500">
          {rowNumber}.{subIndex}
        </span>
        <TestIcon
          className={`h-3.5 w-3.5 flex-shrink-0 ${ai.className} ${ai.spin ? 'animate-spin' : ''}`}
        />
        <span className="flex-1 truncate text-gray-900 dark:text-gray-100">
          <HighlightText text={final.title} search={searchQuery} />
        </span>
        {hasMultiple ? (
          <span className="inline-flex items-center gap-1 text-xs text-orange-600 dark:text-orange-400">
            <RotateCcw className="h-3 w-3" />
            {entries.length} attempts
          </span>
        ) : (
          final.duration_ms != null && (
            <span className="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-500">
              <Clock className="h-3 w-3" />
              {formatDuration(final.duration_ms)}
            </span>
          )
        )}
      </div>
      {expanded && (
        <div className="ml-16 mt-1 space-y-2 border-l-2 border-gray-200 pl-3 dark:border-gray-600">
          {entries.map(({ tc, ghJobName, attemptIdx }, i) => {
            const isFailure =
              tc.status === 'failed' || tc.status === 'timedOut' || tc.status === 'interrupted';
            const isSkipped = tc.status === 'skipped';
            const isPassed = tc.status === 'passed' || tc.status === 'flaky';
            const AttemptIcon = isSkipped ? MinusCircle : isPassed ? CheckCircle2 : XCircle;
            const attemptColor = isSkipped
              ? 'text-gray-400'
              : isPassed
                ? 'text-green-500'
                : 'text-red-500';
            // Phase tag: anything from the unit's first orchestration
            // attempt is "first-pass" (Playwright `--retries` reruns inside
            // the same lease still count as first-pass — the lease only
            // cycles via the orchestration retest queue). Higher attempt
            // indices are orchestration-level retests, which usually land
            // on a different worker.
            const isRetestPhase = attemptIdx > 0;
            return (
              <div key={`${tc.full_title}-${i}`} className="space-y-1">
                <div className="flex items-center gap-2 text-xs">
                  <AttemptIcon className={`h-3 w-3 flex-shrink-0 ${attemptColor}`} />
                  <span className="font-medium text-gray-700 dark:text-gray-300">
                    {/* Chronological label across the whole unit history.
                        Each retest dispatch resets Playwright's internal
                        `retry_count` back to 0, so labelling by that value
                        would surface duplicate "Attempt 1" rows for the
                        same test. The list-position index is monotonic
                        across leases AND retries, so it reads naturally
                        as 1, 2, 3, … in execution order. */}
                    Attempt {i + 1}
                  </span>
                  {ghJobName && (
                    <span className="text-xs text-gray-500 dark:text-gray-500">({ghJobName})</span>
                  )}
                  {isRetestPhase && (
                    <span
                      className="inline-flex items-center rounded bg-orange-100 px-1 text-[10px] font-medium text-orange-700 dark:bg-orange-900/40 dark:text-orange-300"
                      title="Orchestration-level retest (re-leased to a worker after the first lease ended in failure)"
                    >
                      retest
                    </span>
                  )}
                  {tc.duration_ms != null && (
                    <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-500">
                      <Clock className="h-3 w-3" />
                      {formatDuration(tc.duration_ms)}
                    </span>
                  )}
                </div>
                {isFailure && (tc.error_stack || tc.error_message) && (
                  <div className="ml-5 overflow-hidden rounded border border-red-200 bg-gray-900 dark:border-red-800">
                    <pre className="overflow-x-auto whitespace-pre-wrap p-3 font-mono text-xs text-gray-100">
                      {tc.error_stack || tc.error_message}
                    </pre>
                  </div>
                )}
                {(tc.attachments?.screenshots?.length ?? 0) > 0 && (
                  <div className="ml-5">
                    <ScreenshotGallery
                      screenshots={(tc.attachments?.screenshots ?? []).map((s, idx) => ({
                        path: s.relative_path ?? s.key,
                        s3_key: s.key,
                        content_type: 'image/png',
                        retry: tc.retry_count ?? 0,
                        missing: false,
                        sequence: idx,
                      }))}
                    />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/**
 * Two filter families share this state:
 *   - `spec_*` filters apply at the dispatch-unit level — driven by the
 *     title-bar chips, which match a row only when the suite's overall
 *     outcome equals the selected status.
 *   - `test_*` filters apply at the test-case level — driven by the
 *     right-side stat pills, which match a row when it contains at least
 *     one test of the selected status (mirrors the Reports tab).
 */
type SpecListFilter =
  | 'all'
  | 'spec_passed'
  | 'spec_failed'
  | 'spec_in_progress'
  | 'spec_retest_in_progress'
  | 'spec_skipped'
  | 'test_passed'
  | 'test_failed'
  | 'test_flaky'
  | 'test_skipped';

// 500ms debounce, matching the Reports tab so typing in either view feels
// the same.
const SEARCH_DEBOUNCE_MS = 500;
// Same per-page count the Reports tab uses, so a run with hundreds of
// dispatch units paginates instead of scrolling forever in a single list.
const PAGE_SIZE = 100;

function SpecList({ run }: { run: RunSnapshot }) {
  const units = run.units ?? [];
  const allRows = useMemo(() => buildSpecRows(units), [units]);
  const tcCounts = useMemo(() => computeTestCaseCounts(units), [units]);

  const [statusFilter, setStatusFilter] = useState<SpecListFilter>('all');
  // Three-stage search pipeline mirroring the Reports tab:
  //   - searchQuery: the live input value (drives the controlled input)
  //   - debouncedSearch: searchQuery after a 500ms idle window
  //   - effectiveSearch: the value the filter actually consumes; gated by
  //     `minSearchLength` so single-character typos don't churn the list.
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [effectiveSearch, setEffectiveSearch] = useState('');
  const searchInputRef = useRef<HTMLInputElement>(null);

  const { data: clientConfig } = useClientConfig();
  const minSearchLength = clientConfig?.search_min_length ?? 2;

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearch(searchQuery.trim());
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Promote debounced → effective only when the query meets the min length
  // (or has been cleared). This is the same gate the Reports tab uses; the
  // orchestration view is purely client-side so there is no API to wait
  // for, but the gating still avoids flashing partial matches mid-typing.
  useEffect(() => {
    if (debouncedSearch.length === 0 || debouncedSearch.length >= minSearchLength) {
      setEffectiveSearch(debouncedSearch);
    } else {
      setEffectiveSearch('');
    }
  }, [debouncedSearch, minSearchLength]);

  const normalizedSearch = useMemo(() => effectiveSearch.toLowerCase(), [effectiveSearch]);
  const isSearching = searchQuery.trim() !== effectiveSearch;

  // Page index, 1-based to match the Reports tab. Reset to 1 whenever the
  // filtered set changes shape (status filter or search) so the user
  // doesn't land on a now-empty page.
  const [currentPage, setCurrentPage] = useState(1);
  useEffect(() => {
    setCurrentPage(1);
  }, [statusFilter, normalizedSearch]);

  const filteredRows = useMemo(() => {
    const q = normalizedSearch;
    const matched = allRows.filter((row) => {
      if (statusFilter !== 'all') {
        // "In progress" means a worker has the unit checked out right
        // now (`leased`) — distinct from `pending` (queued, awaiting
        // checkout) and `abandoned` (lease expired, awaiting re-dispatch).
        // First-pass leases (`lease_count <= 1`) and retests (>1) get
        // separate filter chips.
        const firstPassInFlight = row.effectiveState === 'leased' && row.unit.lease_count <= 1;
        const retestInFlight = row.effectiveState === 'leased' && row.unit.lease_count > 1;
        const matches =
          // Spec-file-level: the suite's overall outcome equals the chip
          (statusFilter === 'spec_passed' && row.effectiveState === 'completed_pass') ||
          (statusFilter === 'spec_failed' && row.effectiveState === 'completed_fail') ||
          (statusFilter === 'spec_in_progress' && firstPassInFlight) ||
          (statusFilter === 'spec_retest_in_progress' && retestInFlight) ||
          (statusFilter === 'spec_skipped' && row.effectiveState === 'completed_skipped') ||
          // Test-case-level: any test in the suite has the chosen status
          (statusFilter === 'test_passed' && row.testCounts.passed > 0) ||
          (statusFilter === 'test_failed' && row.testCounts.failed > 0) ||
          (statusFilter === 'test_flaky' && row.testCounts.flaky > 0) ||
          (statusFilter === 'test_skipped' && row.testCounts.skipped > 0);
        if (!matches) return false;
      }
      if (q) {
        if (row.unit.spec_path.toLowerCase().includes(q)) return true;
        for (const a of row.attempts) {
          for (const tc of a.test_cases ?? []) {
            if (tc.title.toLowerCase().includes(q) || tc.full_title.toLowerCase().includes(q)) {
              return true;
            }
          }
        }
        return false;
      }
      return true;
    });
    // Bubble currently-running specs to the top so they're visible without
    // scrolling 100+ queued rows. Stable within each group → keeps the
    // controller's dispatch order otherwise.
    return matched
      .map((row, idx) => ({ row, idx }))
      .sort((a, b) => {
        const ai = a.row.effectiveState === 'leased' ? 0 : 1;
        const bi = b.row.effectiveState === 'leased' ? 0 : 1;
        if (ai !== bi) return ai - bi;
        return a.idx - b.idx;
      })
      .map((x) => x.row);
  }, [allRows, statusFilter, normalizedSearch]);

  const totalPages = Math.max(1, Math.ceil(filteredRows.length / PAGE_SIZE));
  const paginatedRows = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return filteredRows.slice(start, start + PAGE_SIZE);
  }, [filteredRows, currentPage]);

  if (allRows.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-gray-200 bg-gray-50 py-10 text-gray-400 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-500">
        <Inbox className="mb-2 h-8 w-8" />
        <p className="text-sm">No spec files dispatched</p>
      </div>
    );
  }

  const headerCount = allRows.length;

  // Spec-file-level pass/fail counts for the header summary chip. Flaky
  // suites land in effectiveState='completed_pass' so they roll into the
  // passed bucket here, mirroring the run-level "flaky counts as passed"
  // rule used elsewhere in this view.
  const specPassed = allRows.filter((r) => r.effectiveState === 'completed_pass').length;
  const specFailed = allRows.filter((r) => r.effectiveState === 'completed_fail').length;
  // Truly running suites only — a worker has them checked out (`leased`).
  // Split first-pass leases (lease_count == 1) from retests (> 1) so the
  // user can tell at a glance whether the in-flight count is fresh work
  // or a retest pass cleaning up earlier failures. `pending` and
  // `abandoned` rows render with their own visual cues (gray clock and
  // amber warning) and do not roll into either count.
  const specInProgress = allRows.filter(
    (r) => r.effectiveState === 'leased' && r.unit.lease_count <= 1,
  ).length;
  const specRetestInProgress = allRows.filter(
    (r) => r.effectiveState === 'leased' && r.unit.lease_count > 1,
  ).length;
  const specSkipped = allRows.filter((r) => r.effectiveState === 'completed_skipped').length;

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
      <div className="mb-4 flex items-center gap-4">
        <h3 className="flex flex-shrink-0 items-center gap-2 text-sm font-medium text-gray-900 dark:text-white">
          <button
            type="button"
            onClick={() => {
              setStatusFilter('all');
              setSearchQuery('');
              setDebouncedSearch('');
              setEffectiveSearch('');
            }}
            title="Show all suites"
            className={`cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-gray-700 ${
              statusFilter === 'all' && !normalizedSearch ? 'bg-gray-200 dark:bg-gray-600' : ''
            }`}
          >
            {headerCount} {headerCount === 1 ? 'spec' : 'specs'}
          </button>
          {specPassed > 0 && (
            <button
              type="button"
              onClick={() =>
                setStatusFilter(statusFilter === 'spec_passed' ? 'all' : 'spec_passed')
              }
              title="Filter passed suites"
              className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-sm text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20 ${
                statusFilter === 'spec_passed' ? 'bg-green-100 dark:bg-green-900/40' : ''
              }`}
            >
              <CheckCircle2 className="h-3 w-3" />
              {specPassed}
            </button>
          )}
          {specFailed > 0 && (
            <button
              type="button"
              onClick={() =>
                setStatusFilter(statusFilter === 'spec_failed' ? 'all' : 'spec_failed')
              }
              title="Filter failed suites"
              className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-sm text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20 ${
                statusFilter === 'spec_failed' ? 'bg-red-100 dark:bg-red-900/40' : ''
              }`}
            >
              <XCircle className="h-3 w-3" />
              {specFailed}
            </button>
          )}
          {specInProgress > 0 && (
            <button
              type="button"
              onClick={() =>
                setStatusFilter(statusFilter === 'spec_in_progress' ? 'all' : 'spec_in_progress')
              }
              title="Filter running suites (first-pass leases)"
              className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-sm text-blue-600 transition-colors hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20 ${
                statusFilter === 'spec_in_progress' ? 'bg-blue-100 dark:bg-blue-900/40' : ''
              }`}
            >
              <Loader2 className="h-3 w-3 animate-spin" />
              {specInProgress}
            </button>
          )}
          {specRetestInProgress > 0 && (
            <button
              type="button"
              onClick={() =>
                setStatusFilter(
                  statusFilter === 'spec_retest_in_progress' ? 'all' : 'spec_retest_in_progress',
                )
              }
              title="Filter retests in flight (lease_count > 1)"
              className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-sm text-orange-600 transition-colors hover:bg-orange-50 dark:text-orange-400 dark:hover:bg-orange-900/20 ${
                statusFilter === 'spec_retest_in_progress'
                  ? 'bg-orange-100 dark:bg-orange-900/40'
                  : ''
              }`}
            >
              <Loader2 className="h-3 w-3 animate-spin" />
              {specRetestInProgress}
            </button>
          )}
          {specSkipped > 0 && (
            <button
              type="button"
              onClick={() =>
                setStatusFilter(statusFilter === 'spec_skipped' ? 'all' : 'spec_skipped')
              }
              title="Filter skipped suites"
              className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-sm text-gray-500 transition-colors hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700 ${
                statusFilter === 'spec_skipped' ? 'bg-gray-200 dark:bg-gray-600' : ''
              }`}
            >
              <MinusCircle className="h-3 w-3" />
              {specSkipped}
            </button>
          )}
        </h3>
        <div className="flex-1" />
        {tcCounts.total > 0 && (
          <div className="flex flex-shrink-0 items-center gap-2">
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setStatusFilter('all')}
                className={`cursor-pointer rounded px-2 py-0.5 text-sm font-medium transition-colors ${
                  statusFilter === 'all'
                    ? 'bg-gray-200 text-gray-900 dark:bg-gray-600 dark:text-white'
                    : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
                }`}
              >
                {tcCounts.total} {tcCounts.total === 1 ? 'test' : 'tests'}
              </button>
              <button
                type="button"
                onClick={() => setStatusFilter('test_passed')}
                className={`cursor-pointer rounded px-2 py-0.5 text-sm transition-colors ${
                  statusFilter === 'test_passed'
                    ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-400'
                    : 'text-green-600 hover:bg-green-50 dark:text-green-500 dark:hover:bg-green-900/20'
                }`}
              >
                <span className="inline-flex items-center gap-1">
                  <CheckCircle2 className="h-3 w-3" />
                  {tcCounts.passed}
                </span>
              </button>
              {tcCounts.failed > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('test_failed')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-sm transition-colors ${
                    statusFilter === 'test_failed'
                      ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400'
                      : 'text-red-600 hover:bg-red-50 dark:text-red-500 dark:hover:bg-red-900/20'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <XCircle className="h-3 w-3" />
                    {tcCounts.failed}
                  </span>
                </button>
              )}
              {tcCounts.flaky > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('test_flaky')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-sm transition-colors ${
                    statusFilter === 'test_flaky'
                      ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-400'
                      : 'text-yellow-600 hover:bg-yellow-50 dark:text-yellow-500 dark:hover:bg-yellow-900/20'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <AlertTriangle className="h-3 w-3" />
                    {tcCounts.flaky}
                  </span>
                </button>
              )}
              {tcCounts.skipped > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('test_skipped')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-sm transition-colors ${
                    statusFilter === 'test_skipped'
                      ? 'bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-200'
                      : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <MinusCircle className="h-3 w-3" />
                    {tcCounts.skipped}
                  </span>
                </button>
              )}
            </div>
          </div>
        )}
      </div>
      <div className="relative mb-4 inline-block w-[21rem]">
        {isSearching ? (
          <Loader2 className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-blue-500" />
        ) : (
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400 dark:text-gray-500" />
        )}
        <input
          ref={searchInputRef}
          type="text"
          placeholder="Search tests..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="h-8 w-full rounded border border-gray-200 bg-white pl-7 pr-7 text-sm text-gray-900 placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500 dark:focus:border-blue-400 dark:focus:ring-blue-400"
        />
        {searchQuery && (
          <button
            type="button"
            onClick={() => {
              setSearchQuery('');
              setDebouncedSearch('');
              setEffectiveSearch('');
              searchInputRef.current?.focus();
            }}
            className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-gray-600 dark:hover:text-gray-300"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      {totalPages > 1 && (
        <PaginationBar
          currentPage={currentPage}
          totalPages={totalPages}
          totalItems={filteredRows.length}
          pageSize={PAGE_SIZE}
          onPageChange={setCurrentPage}
          itemLabel="spec"
          position="top"
        />
      )}
      <div className="divide-y divide-gray-100 dark:divide-gray-700">
        {filteredRows.length === 0 ? (
          <div className="py-6 text-center text-xs text-gray-400 dark:text-gray-500">
            No matching dispatch units
          </div>
        ) : (
          paginatedRows.map((row) => {
            const originalIdx = allRows.indexOf(row);
            return (
              <SpecListRow
                key={row.unit.id}
                row={row}
                rowNumber={originalIdx + 1}
                searchQuery={normalizedSearch}
              />
            );
          })
        )}
      </div>
      {totalPages > 1 && (
        <PaginationBar
          currentPage={currentPage}
          totalPages={totalPages}
          totalItems={filteredRows.length}
          pageSize={PAGE_SIZE}
          onPageChange={setCurrentPage}
          itemLabel="spec"
          position="bottom"
        />
      )}
    </div>
  );
}

export function OrchestrationTab({ identity }: OrchestrationTabProps) {
  const { data: run, isLoading, error } = useOrchestrationRun(identity);
  const queryClient = useQueryClient();

  // Subscribe to live orchestration events so rows transition in real time.
  // Each event triggers a refetch of the run snapshot — cheap (single GET
  // returning the full units+attempts payload) and keeps the merge logic on
  // the server side so the client only ever renders authoritative state.
  //
  // Subscribe with the FULL identity from the snapshot, not the URL's
  // identity. The URL's commit_sha is typically a 7-char prefix; the Hub's
  // identity matcher requires exact equality on all 5 fields, so the
  // publisher's 40-char identity would not route to a short-SHA subscriber.
  // The query-key invalidation still uses the original URL identity since
  // that's what `useOrchestrationRun` was keyed with.
  const fullIdentity = run
    ? {
        repository: run.repository,
        commit_sha: run.commit_sha,
        gh_run_id: run.gh_run_id,
        name: run.name,
        gh_run_attempt: run.gh_run_attempt ?? '1',
      }
    : null;
  useEffect(() => {
    if (!fullIdentity) return;
    const queryKey = ['orchestration', 'run', compositeIdentityKey(identity)];
    const unsubscribe = subscribeToOrchestrationRun(fullIdentity, () => {
      queryClient.invalidateQueries({ queryKey });
    });
    return unsubscribe;
    // identity is the URL-derived (possibly short-SHA) form, used only as
    // the React Query cache key. fullIdentity carries the canonical full SHA
    // and changes only when the snapshot resolves to a different run.
  }, [
    fullIdentity?.repository,
    fullIdentity?.commit_sha,
    fullIdentity?.gh_run_id,
    fullIdentity?.name,
    fullIdentity?.gh_run_attempt,
    identity,
    queryClient,
  ]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-950/30">
        <div className="flex items-center gap-2 text-sm text-red-700 dark:text-red-400">
          <AlertTriangle className="h-4 w-4" />
          Failed to load orchestration run:{' '}
          {error instanceof Error ? error.message : 'Unknown error'}
        </div>
      </div>
    );
  }

  if (!run) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
        <Inbox className="mb-3 h-12 w-12" />
        <p className="text-sm">No orchestration run found</p>
        <p className="mt-1 text-xs">
          This report group was not produced via orchestrated dispatch.
        </p>
      </div>
    );
  }

  // Test-case-level counts for the canonical Reports-style summary line.
  // Walks every unit's LATEST attempt, dedupes test_cases by full_title
  // (Playwright's internal retries surface as duplicate full_titles), and
  // buckets the FINAL status. A test that had at least one failed entry
  // before its final passed entry is counted as flaky — this catches
  // Playwright's internal retry-then-pass flow that the tsio reporter
  // records as separate per-result rows. Pending/leased units contribute
  // nothing (no test_cases yet); total grows as each unit completes.
  const tc = computeTestCaseCounts(run.units ?? []);
  const { setupDurationMs, durationMs, retestDurationMs, firstTestAt, firstRetestAt, lastTestAt } =
    computeTestDurations(run.units ?? [], run.started_at);
  // Overall verdict is Passed / Failed / Timed Out / nothing (still in
  // flight). Flaky is per-test-case detail, not a run-level outcome — a
  // run with flaky-but-eventually-passed tests rolls up as Passed.
  const testStatus: 'passed' | 'failed' | 'timed_out' | undefined =
    run.status === 'in_progress'
      ? undefined
      : run.status === 'timed_out'
        ? 'timed_out'
        : tc.failed > 0
          ? 'failed'
          : 'passed';

  // RunStatus values are already a strict subset of ProgressStatus, so the
  // map is a pass-through. Kept explicit so a future RunStatus addition
  // surfaces as a type error here rather than silently misrendering.
  const progressStatus: 'in_progress' | 'completed' | 'timed_out' =
    run.status === 'in_progress'
      ? 'in_progress'
      : run.status === 'timed_out'
        ? 'timed_out'
        : 'completed';

  return (
    <div className="space-y-6">
      <ReportSummary
        testStatus={testStatus}
        name={run.name}
        passed={tc.passed}
        failed={tc.failed}
        flaky={tc.flaky}
        skipped={tc.skipped}
        total={tc.total}
        setupDurationMs={setupDurationMs > 0 ? setupDurationMs : null}
        durationMs={durationMs > 0 ? durationMs : null}
        retestDurationMs={retestDurationMs > 0 ? retestDurationMs : null}
        beginAt={run.started_at}
        firstTestAt={firstTestAt}
        firstRetestAt={firstRetestAt}
        lastTestAt={lastTestAt}
        createdAt={run.started_at}
        framework={run.framework}
        progressStatus={progressStatus}
        // Spec-count chip: terminal units / total. The Combine and
        // Dispatch tabs use this in place of the Completed/Incomplete
        // badge — mirrors the Reports tab's report-count chip pattern.
        specsCount={
          (run.counts.completed_pass ?? 0) +
          (run.counts.completed_fail ?? 0) +
          (run.counts.completed_skipped ?? 0) +
          (run.counts.abandoned ?? 0)
        }
        totalSpecs={run.total_units}
        repository={run.repository}
        branch={run.branch}
        commit={run.commit_sha}
        ghPrNumber={run.gh_pr_number}
        ghRunId={run.gh_run_id}
      />

      <SpecList run={run} />
    </div>
  );
}

import { useEffect, useMemo } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import {
  useConsolidatedResults,
  useReportDetail,
  useReportSuites,
  useOrchestrationRun,
  useOrchestrationRuns,
  fetchReportDetail,
  fetchReportSuites,
} from '@/services/api';
import { Breadcrumb } from '@/components/breadcrumb';
import { TestSuitesView } from '@/components/test_suites_view';
import { RunAttemptSelector } from '@/components/run_attempt_selector';
import { Loader2, Inbox, AlertTriangle, Layers, ListTree, GitMerge } from 'lucide-react';
import { ContributingReportsList } from '@/components/contributing_reports_list';
import { ReportSummary, resolveEffectiveReportStatus } from '@/components/report_summary';
import { isRetestName } from '@/components/report_card_parts';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { OrchestrationTab } from '@/components/orchestration/orchestration_tab';
import type { CompositeIdentity } from '@/types/orchestration';

function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

export function FilteredReportPage() {
  const { repo, branch, commit, name } = useParams<{
    repo: string;
    branch: string;
    commit: string;
    name: string;
  }>();

  const [search_params, setSearchParams] = useSearchParams();
  const run_attempt_param = search_params.get('run_attempt');
  const run_attempt = run_attempt_param ? parseInt(run_attempt_param, 10) : undefined;
  const gid = search_params.get('gid') || undefined;
  const gh_run_id_param = search_params.get('gh_run_id') || undefined;
  const gh_run_attempt_param = search_params.get('gh_run_attempt') || undefined;

  // Step 1: Get consolidated results to find contributing report IDs
  const { data, isLoading, error } = useConsolidatedResults(
    repo || '',
    branch || '',
    commit || '',
    name || '',
    run_attempt,
    gid,
  );

  // Step 2: Fetch all contributing reports details
  const contributingIds = data?.contributing_reports ?? [];
  const allReportQueries = useQueries({
    queries: contributingIds.map((id) => ({
      queryKey: ['report-detail', id],
      queryFn: () => fetchReportDetail(id),
      enabled: !!id,
    })),
  });

  // Find the latest contributing report (most recent created_at) for header metadata
  const latestReportId = useMemo(() => {
    let latestId = '';
    let latestTime = '';
    for (const q of allReportQueries) {
      if (!q.data) continue;
      if (!latestTime || q.data.created_at > latestTime) {
        latestTime = q.data.created_at;
        latestId = q.data.id;
      }
    }
    return latestId || (data?.contributing_reports?.[0] ?? '');
  }, [allReportQueries, data]);

  const { data: report } = useReportDetail(latestReportId);
  const { data: suitesData, isLoading: isLoadingSuites } = useReportSuites(latestReportId);

  // Auto-resolve gh_run_id when neither the URL nor the latest contributing
  // report carries one. Hits /orchestration/runs to find every run matching
  // the display identity (repo trailing-segment + branch + commit + name)
  // and stamps a chosen run's gh_run_id into the URL. Selection rule:
  //   - prefer the most recent run still in progress so a bare URL during a
  //     live run lands on the live dispatch view (no reports uploaded yet
  //     means the consolidated path has nothing to render),
  //   - else the most recent run overall,
  //   - 0 matches → no-op, the existing empty state takes over.
  // Disabled once any source provides a gh_run_id.
  const need_resolve = !gh_run_id_param && !report?.gh_run_id;
  const { data: candidate_runs } = useOrchestrationRuns(
    {
      repository: repo || '',
      commit_sha: commit || '',
      name: name || '',
      branch: branch || undefined,
    },
    need_resolve,
  );

  const auto_resolved_run = useMemo(() => {
    if (!need_resolve || !candidate_runs || candidate_runs.length === 0) return undefined;
    const inProgress = candidate_runs.filter((r) => r.status === 'in_progress');
    const pool = inProgress.length > 0 ? inProgress : candidate_runs;
    return [...pool].sort((a, b) =>
      a.started_at < b.started_at ? 1 : a.started_at > b.started_at ? -1 : 0,
    )[0];
  }, [need_resolve, candidate_runs]);

  useEffect(() => {
    if (!auto_resolved_run) return;
    const next = new URLSearchParams(search_params);
    next.set('gh_run_id', auto_resolved_run.gh_run_id);
    if (auto_resolved_run.gh_run_attempt && !gh_run_attempt_param) {
      next.set('gh_run_attempt', auto_resolved_run.gh_run_attempt);
    }
    setSearchParams(next, { replace: true });
  }, [auto_resolved_run, search_params, setSearchParams, gh_run_attempt_param]);

  // Composite identity for the orchestration query. `gh_run_id` may arrive
  // from the URL search param (preferred, set by direct links), the latest
  // contributing report's metadata once the report fetch resolves, or the
  // single auto-resolved candidate from /orchestration/runs (stamped into the
  // URL by the effect above on its next render). Only when all three sources
  // are absent does the orchestration query stay disabled.
  const auto_resolved_run_id = auto_resolved_run?.gh_run_id;
  const orchestrationIdentity = useMemo<CompositeIdentity>(
    () => ({
      repository: repo || '',
      commit_sha: commit || '',
      gh_run_id: gh_run_id_param || report?.gh_run_id || auto_resolved_run_id || '',
      name: name || '',
      gh_run_attempt: gh_run_attempt_param ?? '1',
      branch: branch || undefined,
    }),
    [
      repo,
      commit,
      gh_run_id_param,
      report?.gh_run_id,
      auto_resolved_run_id,
      name,
      gh_run_attempt_param,
      branch,
    ],
  );

  const { data: orchestrationRun } = useOrchestrationRun(orchestrationIdentity);

  // Sum report entries across every contributing group, split by retest vs
  // numbered shard so the summary can render the "4+1 reports" form.
  const reportCountSplit = useMemo(() => {
    let numbered = 0;
    let retest = 0;
    for (const q of allReportQueries) {
      if (!q.data?.reports) continue;
      for (const e of q.data.reports) {
        if (isRetestName(e.gh_job_name) || isRetestName(e.display_name)) {
          retest++;
        } else {
          numbered++;
        }
      }
    }
    return { numbered, retest };
  }, [allReportQueries]);

  // Step 3: Fetch suites for ALL contributing reports (for chip status indicators)
  const allSuitesQueries = useQueries({
    queries: contributingIds.map((id) => ({
      queryKey: ['report', id, 'suites'],
      queryFn: () => fetchReportSuites(id),
      enabled: !!id,
    })),
  });

  // Map each per-shard report_id → display_name by flattening every contributing
  // group's reports[] entries. Used to enrich consolidated spec history with
  // the shard the attempt came from.
  const shardNamesByReportId = useMemo(() => {
    const map = new Map<string, string>();
    for (const q of allReportQueries) {
      if (!q.data?.reports) continue;
      for (const entry of q.data.reports) {
        map.set(entry.id, entry.display_name);
      }
    }
    return map;
  }, [allReportQueries]);

  // crossShardHistory[full_title] = list of per-shard attempts for that spec,
  // joined to the shard display_name. TestSuitesView renders a per-spec
  // "Across shards" section when the list has more than one entry (or any
  // failed entry for a currently-passing spec).
  const crossShardHistory = useMemo(() => {
    const map = new Map<string, import('@/types').CrossShardAttempt[]>();
    if (!data?.specs) return map;
    for (const spec of data.specs) {
      if (!spec.history || spec.history.length === 0) continue;
      const attempts = spec.history.map((h) => ({
        report_id: h.report_id,
        display_name: shardNamesByReportId.get(h.report_id) || '',
        status: h.status,
        duration_ms: h.duration_ms,
        error_message: h.error_message,
        error_stack: h.error_stack,
        errors_json: h.errors_json,
        created_at: h.created_at,
        run_attempt: h.run_attempt,
        screenshots: h.screenshots,
      }));
      map.set(spec.full_title, attempts);
    }
    return map;
  }, [data?.specs, shardNamesByReportId]);

  // Build per-report test result status from suites across ALL contributing reports
  const reportTestStatus = useMemo(() => {
    const map = new Map<string, 'passed' | 'failed' | 'flaky'>();
    for (const q of allSuitesQueries) {
      if (!q.data?.suites) continue;
      for (const suite of q.data.suites) {
        if (!suite.report_id) continue;
        const current = map.get(suite.report_id) || 'passed';
        if (suite.failed_count > 0) {
          map.set(suite.report_id, 'failed');
        } else if ((suite.flaky_count ?? 0) > 0 && current !== 'failed') {
          map.set(suite.report_id, 'flaky');
        } else if (!map.has(suite.report_id)) {
          map.set(suite.report_id, 'passed');
        }
      }
    }
    return map;
  }, [allSuitesQueries]);

  // Group reports by run ID, then by run attempt within each run
  const reportsByRun = (() => {
    type ReportChipEntry = { id: string; reportId: string; display_name: string; status: string };
    type AttemptGroup = { attempt: string; reports: ReportChipEntry[] };
    type RunGroup = { runId: string; createdAt: string; attempts: AttemptGroup[] };

    const runMap = new Map<
      string,
      { createdAt: string; attemptMap: Map<string, ReportChipEntry[]> }
    >();

    for (const q of allReportQueries) {
      if (!q.data) continue;
      const runId = q.data.gh_run_id || 'unknown';
      const attempt = q.data.gh_run_attempt || '1';

      if (!runMap.has(runId)) {
        runMap.set(runId, { createdAt: q.data.created_at, attemptMap: new Map() });
      }
      const run = runMap.get(runId)!;
      if (!run.attemptMap.has(attempt)) run.attemptMap.set(attempt, []);
      for (const entry of q.data.reports) {
        run.attemptMap.get(attempt)!.push({
          id: entry.id,
          reportId: q.data.id,
          display_name: entry.display_name,
          status: entry.status,
        });
      }
    }

    // Build sorted run groups: runs sorted by createdAt descending, attempts ascending
    const groups: RunGroup[] = [];
    for (const [runId, { createdAt, attemptMap }] of runMap) {
      const attempts: AttemptGroup[] = [...attemptMap.keys()]
        .sort((a, b) => parseInt(a) - parseInt(b))
        .map((attempt) => ({ attempt, reports: attemptMap.get(attempt)! }));
      groups.push({ runId, createdAt, attempts });
    }
    groups.sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
    return groups;
  })();

  // Tab control. The default is "combine" — it routes to the dispatch
  // view when orchestration data exists and falls back to reports when
  // only the framework's report group is available, so the user lands
  // on whichever single source of truth applies.
  const reportGroupHasData = !!data && data.total_specs > 0;
  const orchestrationHasData = !!orchestrationRun;
  const tabFromUrl = search_params.get('tab');
  const isValidTab =
    tabFromUrl === 'combine' || tabFromUrl === 'dispatch' || tabFromUrl === 'reports';
  const activeTab = isValidTab ? tabFromUrl : 'combine';

  // Tabs are hidden by default — the page renders only the Combine view,
  // which is the merged single-source-of-truth picture. They're revealed
  // when the user opts into comparison via `?compare=1`, or implicitly
  // when an explicit `?tab=...` deep-link is followed (so existing links
  // still land on the requested tab without also passing `compare=1`).
  const showTabs = search_params.get('compare') === '1' || isValidTab;

  const handleTabChange = (next: string) => {
    const sp = new URLSearchParams(search_params);
    sp.set('tab', next);
    setSearchParams(sp, { replace: true });
  };

  const bothMissing =
    !isLoading && !error && data && data.total_specs === 0 && !orchestrationHasData;

  const orchestrationIdentityIsResolvable = !!orchestrationIdentity.gh_run_id;

  const contributingReportsNode = (
    <ContributingReportsList
      reportsByRun={reportsByRun}
      reportTestStatus={reportTestStatus}
      repository={report?.repository}
      formatDate={formatDate}
    />
  );

  // Reports tab body, hoisted into a closure so the Combine tab can fall
  // back to it verbatim when no orchestration data is available. Closes
  // over the page-scoped data/loading flags rather than receiving props.
  const renderReportsBody = () => (
    <div className="space-y-6">
      {/* Header with report metadata */}
      {report && (
        <ReportSummary
          // Overall verdict is one of: Passed, Failed, Timed Out, or
          // nothing (run hasn't started or is still in flight). Flaky
          // lives at the test-case level, not the run level — a run with
          // any flaky-but-eventually-passed test still rolls up as
          // Passed. When an active orchestration is present its
          // lifecycle wins over the consolidated report aggregate.
          testStatus={(() => {
            if (orchestrationRun) {
              if (orchestrationRun.status === 'in_progress') return undefined;
              if (orchestrationRun.status === 'timed_out') return 'timed_out';
              // Terminal: 'completed'. RunCounts rolls flaky-passed
              // units into completed_pass, so any failures wins.
              const c = orchestrationRun.counts;
              if ((c.completed_fail ?? 0) > 0) return 'failed';
              return 'passed';
            }
            if (data?.overall_status === 'failed') return 'failed';
            // A consolidated rollup with no recorded specs has nothing
            // to verdict on yet — the in-flight reports row exists but
            // no test_cases have been ingested. Skip the badge so the
            // header doesn't claim "Passed" with 0 / 0.
            if (
              (data?.total_specs ?? 0) > 0 &&
              (data?.overall_status === 'passed' || data?.overall_status === 'flaky')
            ) {
              return 'passed';
            }
            return undefined;
          })()}
          name={name}
          passed={data?.passed ?? 0}
          failed={data?.failed ?? 0}
          flaky={data?.flaky ?? 0}
          skipped={data?.skipped ?? 0}
          total={data?.total_specs ?? 0}
          durationMs={data?.wall_clock_ms ?? data?.duration_ms ?? null}
          retestDurationMs={data?.retest_wall_clock_ms}
          createdAt={report.created_at}
          framework={report.framework}
          reportCount={reportCountSplit.numbered}
          retestReportCount={reportCountSplit.retest}
          progressStatus={(() => {
            // When an active orchestration is present, its lifecycle is
            // the source of truth — the consolidated report record can
            // appear `completed` early when an older run at this commit
            // already finished, even though the new run is still pending.
            // For the report-side path, defer to the eager 10-min heuristic
            // so a stuck shard surfaces as `incomplete` before the server
            // reaps it.
            if (orchestrationRun?.status === 'timed_out') return 'timed_out';
            if (orchestrationRun?.status === 'in_progress') return 'in_progress';
            return resolveEffectiveReportStatus(report.status, report.last_upload_at);
          })()}
          repository={report.repository}
          branch={report.branch}
          commit={report.commit}
          ghPrNumber={report.gh_pr_number}
          ghRunId={report.gh_run_id}
        />
      )}

      {/* Test suites view */}
      {isLoadingSuites ? (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
        </div>
      ) : (
        latestReportId && (
          <TestSuitesView
            reportId={latestReportId}
            suites={suitesData?.suites || []}
            title={`${name} — ${branch}/${commit}`}
            reports={suitesData?.reports}
            crossShardHistory={crossShardHistory}
            orchestrationUnits={orchestrationRun?.units}
          />
        )
      )}

      {contributingReportsNode}
    </div>
  );

  // Combine view body. Picks the dispatch view when orchestration data
  // exists, falls back to the reports body when only reports exist, and
  // an empty state when neither does. Hoisted so it can be rendered
  // either standalone (default, no tabs) or inside the Combine tab.
  const renderCombineBody = () =>
    orchestrationIdentityIsResolvable && orchestrationHasData ? (
      <>
        <OrchestrationTab identity={orchestrationIdentity} />
        {contributingReportsNode}
      </>
    ) : reportGroupHasData ? (
      renderReportsBody()
    ) : (
      <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
        <Inbox className="h-12 w-12 mb-3" />
        <p className="text-sm">Nothing to combine yet</p>
        <p className="text-xs mt-1">Awaiting orchestration or report uploads for this run.</p>
      </div>
    );

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="mb-4">
        <Breadcrumb
          items={[
            { label: 'Reports', to: '/reports' },
            { label: repo || '', to: `/reports/${encodeURIComponent(repo || '')}` },
            {
              label: branch || '',
              to: `/reports/${encodeURIComponent(repo || '')}/${encodeURIComponent(branch || '')}`,
            },
            {
              label: commit || '',
              to: `/reports/${encodeURIComponent(repo || '')}/${encodeURIComponent(branch || '')}/${encodeURIComponent(commit || '')}`,
            },
            { label: name || '' },
          ]}
        />
      </div>

      {/* Run attempt selector */}
      {data && data.available_run_attempts.length > 1 && (
        <div className="mb-4">
          <RunAttemptSelector
            available_attempts={data.available_run_attempts}
            current_attempt={run_attempt}
          />
        </div>
      )}

      {/* Loading state */}
      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {/* Error state */}
      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-950/30">
          <div className="flex items-center gap-2 text-sm text-red-700 dark:text-red-400">
            <AlertTriangle className="h-4 w-4" />
            {error instanceof Error && error.message.includes('ambiguous')
              ? 'The short SHA is ambiguous — please use the full 40-character SHA.'
              : `Failed to load results: ${error instanceof Error ? error.message : 'Unknown error'}`}
          </div>
        </div>
      )}

      {/* Empty state — only when BOTH sources are empty (no reports AND no orchestration run) */}
      {bothMissing && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No test results or orchestration run found for {repo}/{branch}/{commit}/{name}
          </p>
        </div>
      )}

      {/* Default view: just the Combine body, no tab bar. */}
      {!isLoading && !error && !bothMissing && !showTabs && renderCombineBody()}

      {/* Tabbed view: Combine + Dispatch + Reports — opt-in via ?compare=1
          (or implicit via an explicit ?tab= deep-link). */}
      {!isLoading && !error && !bothMissing && showTabs && (
        <Tabs value={activeTab} onValueChange={handleTabChange}>
          <TabsList>
            <TabsTrigger value="combine">
              <GitMerge className="h-3.5 w-3.5" />
              Combine
            </TabsTrigger>
            <TabsTrigger value="dispatch">
              <Layers className="h-3.5 w-3.5" />
              Dispatch
            </TabsTrigger>
            <TabsTrigger value="reports">
              <ListTree className="h-3.5 w-3.5" />
              Reports
            </TabsTrigger>
          </TabsList>

          <TabsContent value="combine">{renderCombineBody()}</TabsContent>

          <TabsContent value="dispatch">
            {orchestrationIdentityIsResolvable ? (
              <>
                <OrchestrationTab identity={orchestrationIdentity} />
                {contributingReportsNode}
              </>
            ) : (
              <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
                <Inbox className="h-12 w-12 mb-3" />
                <p className="text-sm">Dispatch not applicable</p>
                <p className="text-xs mt-1">
                  No <code className="font-mono">gh_run_id</code> available for this view.
                </p>
              </div>
            )}
          </TabsContent>

          <TabsContent value="reports">
            {data && data.total_specs > 0 ? (
              renderReportsBody()
            ) : (
              <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
                <Inbox className="h-12 w-12 mb-3" />
                <p className="text-sm">No reports uploaded</p>
                <p className="text-xs mt-1">
                  {orchestrationHasData
                    ? 'Awaiting test report uploads for this run.'
                    : `No test results found for ${repo}/${branch}/${commit}/${name}`}
                </p>
              </div>
            )}
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}

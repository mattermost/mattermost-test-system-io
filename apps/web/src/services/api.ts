import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type {
  ClientConfig,
  ServerInfo,
  ReportListResponse,
  RawReportListResponse,
  TestSuiteListResponse,
  ReportDetail,
  GroupedReportsResponse,
  ConsolidatedResultsResponse,
} from '@/types';
import {
  compositeIdentityKey,
  type BeginRunRequest,
  type CompositeIdentity,
  type Divergence,
  type OrchestrationAttempt,
  type RunSnapshot,
  type TestCaseStatus,
} from '@/types/orchestration';

export const API_URL = import.meta.env.VITE_API_URL || '/api/v1';

// Error handling
class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const errorData = await response.json().catch(() => ({
      error: 'UNKNOWN_ERROR',
      message: response.statusText,
    }));
    throw new ApiError(
      response.status,
      errorData.error || 'UNKNOWN_ERROR',
      errorData.message || response.statusText,
    );
  }
  return response.json();
}

// Client config
export function useClientConfig() {
  return useQuery({
    queryKey: ['client-config'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/config`);
      return handleResponse<ClientConfig>(response);
    },
    staleTime: 5 * 60 * 1000, // 5 minutes
  });
}

// Server info (version, build metadata)
export function useServerInfo() {
  return useQuery({
    queryKey: ['server-info'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/info`);
      return handleResponse<ServerInfo>(response);
    },
    staleTime: 5 * 60 * 1000, // 5 minutes
  });
}

// Grouped reports for landing page. Polls every 5s while any run is
// reporting an in-progress orchestration so the inline "Live" pill on
// each row reflects worker progress without requiring a manual refresh.
export function useGroupedReports() {
  return useQuery({
    queryKey: ['reports-grouped'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/reports/grouped`);
      return handleResponse<GroupedReportsResponse>(response);
    },
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const anyInProgress = data.groups.some((g) =>
        g.runs.some((r) => r.status === 'in_progress' || r.orchestration?.status === 'in_progress'),
      );
      return anyInProgress ? 5000 : false;
    },
  });
}

// Consolidated results for filtered view
export function useConsolidatedResults(
  repo: string,
  branch: string,
  commit: string,
  name: string,
  runAttempt?: number,
  gid?: string,
) {
  return useQuery({
    queryKey: ['reports-consolidated', repo, branch, commit, name, runAttempt, gid],
    queryFn: async () => {
      const params = new URLSearchParams({
        repository: repo,
        branch,
        commit,
        name,
      });
      if (runAttempt !== undefined) {
        params.set('run_attempt', String(runAttempt));
      }
      if (gid) {
        params.set('gid', gid);
      }
      const response = await fetch(`${API_URL}/reports/consolidated?${params}`);
      return handleResponse<ConsolidatedResultsResponse>(response);
    },
    enabled: !!repo && !!branch && !!commit && !!name,
  });
}

// Individual reports list API
export interface IndividualReportSummary {
  id: string;
  short_id: string;
  report_group_id: string;
  name: string;
  status: string;
  gh_job_id?: string;
  gh_job_name?: string;
  repository?: string;
  branch?: string;
  commit?: string;
  test_stats?: {
    total: number;
    passed: number;
    failed: number;
    skipped: number;
    flaky: number;
    duration_ms?: number;
    wall_clock_ms?: number;
  };
  orchestration?: import('@/types').OrchestrationSummary;
  duration_ms?: number;
  created_at: string;
}

export interface IndividualReportListResponse {
  reports: IndividualReportSummary[];
  total: number;
  limit: number;
  offset: number;
}

export function useIndividualReports(page = 1, limit = 100) {
  return useQuery({
    queryKey: ['reports-individual', page, limit],
    queryFn: async () => {
      const offset = (page - 1) * limit;
      const response = await fetch(`${API_URL}/reports/individual?limit=${limit}&offset=${offset}`);
      return handleResponse<IndividualReportListResponse>(response);
    },
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const anyInProgress = data.reports.some(
        (r) => r.status === 'in_progress' || r.orchestration?.status === 'in_progress',
      );
      return anyInProgress ? 5000 : false;
    },
  });
}

// Reports list API (report groups)
export function useReports(page = 1, limit = 100) {
  return useQuery({
    queryKey: ['reports', page, limit],
    queryFn: async () => {
      const offset = (page - 1) * limit;
      const response = await fetch(`${API_URL}/reports?limit=${limit}&offset=${offset}`);
      const rawData = await handleResponse<RawReportListResponse>(response);

      // Transform to expected format with pagination object
      const totalPages = Math.ceil(rawData.total / limit);
      const currentPage = Math.floor(rawData.offset / limit) + 1;

      return {
        reports: rawData.reports,
        pagination: {
          page: currentPage,
          limit: rawData.limit,
          total: rawData.total,
          total_pages: totalPages,
        },
      } as ReportListResponse;
    },
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const anyInProgress = data.reports.some(
        (r) => r.status === 'in_progress' || r.orchestration?.status === 'in_progress',
      );
      return anyInProgress ? 5000 : false;
    },
  });
}

export async function fetchReportSuites(id: string): Promise<TestSuiteListResponse> {
  const response = await fetch(`${API_URL}/reports/${id}/suites`);
  return handleResponse<TestSuiteListResponse>(response);
}

export function useReportSuites(id: string) {
  return useQuery({
    queryKey: ['report', id, 'suites'],
    queryFn: () => fetchReportSuites(id),
    enabled: !!id,
  });
}

// Report detail API
export async function fetchReportDetail(id: string): Promise<ReportDetail> {
  const response = await fetch(`${API_URL}/reports/${id}`);
  return handleResponse<ReportDetail>(response);
}

export function useReportDetail(id: string) {
  return useQuery({
    queryKey: ['report-detail', id],
    queryFn: () => fetchReportDetail(id),
    enabled: !!id,
  });
}

// Search types - grouped by suite
export interface SearchMatchedTestCase {
  test_case_id: string;
  title: string;
  full_title: string;
  status: string;
  match_tokens: string[];
}

export interface SearchSuiteResult {
  suite_id: string;
  suite_title: string;
  suite_file_path: string | null;
  report_id: string;
  matches: SearchMatchedTestCase[];
}

export interface SearchResponse {
  query: string;
  search_min_length: number;
  total_matches: number;
  results: SearchSuiteResult[];
}

// Search API function
export async function searchTestCases(
  reportId: string,
  query: string,
  limit = 100,
): Promise<SearchResponse> {
  const params = new URLSearchParams({
    q: query,
    limit: String(limit),
  });
  const response = await fetch(`${API_URL}/reports/${reportId}/search?${params}`);
  return handleResponse<SearchResponse>(response);
}

// Search React Query hook
// Note: Debouncing should be done in the component before calling this hook
export function useSearchTestCases(
  reportId: string,
  query: string,
  minSearchLength: number,
  limit = 100,
) {
  return useQuery({
    queryKey: ['search-test-cases', reportId, query, limit],
    queryFn: () => searchTestCases(reportId, query, limit),
    enabled: !!reportId && query.length >= minSearchLength,
    staleTime: 60 * 1000, // 1 minute cache
  });
}

// ─── Orchestration ─────────────────────────────────────────────────────────

/** Build the composite-identity query string used by /orchestration/status. */
function orchestrationStatusUrl(identity: CompositeIdentity): string {
  const params = new URLSearchParams({
    repository: identity.repository,
    commit_sha: identity.commit_sha,
    gh_run_id: identity.gh_run_id,
    name: identity.name,
  });
  if (identity.gh_run_attempt !== undefined) {
    params.set('gh_run_attempt', identity.gh_run_attempt);
  }
  return `${API_URL}/orchestration/status?${params}`;
}

/**
 * Fetch the current orchestration run snapshot for a composite identity.
 *
 * Returns `null` on 404 so the consuming page can resolve on either source —
 * orchestration may not exist for legacy runs. Other non-2xx responses raise
 * the project's standard ApiError.
 */
export async function fetchOrchestrationRun(
  identity: CompositeIdentity,
): Promise<RunSnapshot | null> {
  const response = await fetch(orchestrationStatusUrl(identity));
  if (response.status === 404) {
    return null;
  }
  return handleResponse<RunSnapshot>(response);
}

/** React Query hook for the orchestration run snapshot.
 *
 * Live updates normally arrive via the orchestration WebSocket subscription
 * which invalidates this query. The poll below is a safety net: if the WS
 * drops a frame or reconnects mid-event, we still catch up within a few
 * seconds without forcing the user to reload. The interval is disabled
 * once the run reaches a terminal state.
 */
export function useOrchestrationRun(identity: CompositeIdentity) {
  return useQuery<RunSnapshot | null>({
    queryKey: ['orchestration', 'run', compositeIdentityKey(identity)],
    queryFn: () => fetchOrchestrationRun(identity),
    enabled:
      !!identity.repository && !!identity.commit_sha && !!identity.gh_run_id && !!identity.name,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return 5000; // first-fetch retry until the run is found
      return data.status === 'in_progress' ? 5000 : false;
    },
  });
}

/**
 * Dev/admin mutation: begin a new orchestration run.
 *
 * Production runs are opened by CI controllers, not by the UI. This hook is
 * exposed primarily for local dev/admin tooling that may want to drive the
 * orchestrator from the browser.
 */
export async function beginOrchestrationRun(body: BeginRunRequest): Promise<RunSnapshot> {
  const response = await fetch(`${API_URL}/orchestration/begin`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<RunSnapshot>(response);
}

export function useBeginRun() {
  const queryClient = useQueryClient();
  return useMutation<RunSnapshot, Error, BeginRunRequest>({
    mutationFn: beginOrchestrationRun,
    onSuccess: (snapshot) => {
      queryClient.setQueryData(['orchestration', 'run', compositeIdentityKey(snapshot)], snapshot);
    },
  });
}

// ─── Divergence ────────────────────────────────────────────────────────────

/**
 * Map a consolidated-spec status string (which may include framework-specific
 * variants the API returns) onto the orchestration `TestCaseStatus` enum.
 * Returns null when the value is something we cannot meaningfully compare —
 * the caller should drop those rows out of divergence detection.
 */
function normaliseConsolidatedStatus(s: string | undefined | null): TestCaseStatus | null {
  if (!s) return null;
  switch (s) {
    case 'passed':
    case 'failed':
    case 'skipped':
    case 'flaky':
    case 'timedOut':
    case 'interrupted':
      return s;
    default:
      return null;
  }
}

/** Per-spec orchestration verdict — most-recent non-expired attempt wins. */
function reduceAttemptsToVerdicts(
  attempts: OrchestrationAttempt[] | undefined,
): Map<string, TestCaseStatus> {
  const out = new Map<string, TestCaseStatus>();
  if (!attempts) return out;
  // Sort ascending by reported_at (or deadline as fallback) so later writes
  // overwrite earlier ones — leaving the latest verdict per spec_path.
  const ordered = [...attempts].sort((a, b) => {
    const aT = a.reported_at ?? a.deadline;
    const bT = b.reported_at ?? b.deadline;
    return new Date(aT).getTime() - new Date(bT).getTime();
  });
  for (const a of ordered) {
    if (!a.spec_path) continue;
    if (a.expired) continue;
    if (!a.status) continue;
    out.set(a.spec_path, a.status);
  }
  return out;
}

/** Build the `full_title` -> `file_path` map a consolidated payload exposes. */
function consolidatedStatusBySpecPath(
  consolidated: ConsolidatedResultsResponse | null | undefined,
  fullTitleToSpecPath: Map<string, string> | undefined,
): Map<string, TestCaseStatus> {
  const out = new Map<string, TestCaseStatus>();
  if (!consolidated || !fullTitleToSpecPath) return out;
  for (const spec of consolidated.specs) {
    const specPath = fullTitleToSpecPath.get(spec.full_title);
    if (!specPath) continue;
    const status = normaliseConsolidatedStatus(spec.status);
    if (!status) continue;
    out.set(specPath, status);
  }
  return out;
}

/**
 * Compute per-spec divergences between the orchestration-recorded verdicts
 * and the artifact-derived consolidated verdicts. Pure function so callers
 * can also drive it from synthetic test fixtures.
 */
export function computeDivergences(
  attempts: OrchestrationAttempt[] | undefined,
  consolidated: ConsolidatedResultsResponse | null | undefined,
  fullTitleToSpecPath: Map<string, string> | undefined,
): Divergence[] {
  const orchVerdicts = reduceAttemptsToVerdicts(attempts);
  const artifactVerdicts = consolidatedStatusBySpecPath(consolidated, fullTitleToSpecPath);
  const out: Divergence[] = [];
  for (const [specPath, orchStatus] of orchVerdicts) {
    const artStatus = artifactVerdicts.get(specPath);
    if (!artStatus) continue;
    if (artStatus !== orchStatus) {
      out.push({
        spec_path: specPath,
        orchestration_status: orchStatus,
        artifact_status: artStatus,
      });
    }
  }
  // Stable order so renders/tests are deterministic.
  out.sort((a, b) => (a.spec_path < b.spec_path ? -1 : 1));
  return out;
}

/**
 * Inputs for `useDivergences`. The hook composes data the page already has
 * — the orchestration snapshot's attempt history and the existing
 * consolidated test_cases query — so no new endpoint is required.
 *
 * `fullTitleToSpecPath` lets the caller map the consolidated payload's
 * `full_title` keys onto the `spec_path` orchestration uses; without it the
 * hook returns an empty array (the two sources cannot be joined).
 */
export interface UseDivergencesArgs {
  identity: CompositeIdentity;
  attempts?: OrchestrationAttempt[];
  consolidated?: ConsolidatedResultsResponse | null;
  fullTitleToSpecPath?: Map<string, string>;
  /** Skip computation entirely (useful when prerequisites have not loaded). */
  enabled?: boolean;
}

/**
 * React Query hook that computes per-spec divergences between the
 * orchestration view and the canonical artifact view for a composite
 * identity. Resolves to `null` when prerequisites are missing (which the UI
 * treats as "no divergence to show"); callers needing finer granularity can
 * call {@link computeDivergences} directly.
 *
 * Implementation note: divergences are computed client-side rather than via
 * a server endpoint. Both data sources are already loaded by the existing
 * report-group page, and per-spec joining is cheap.
 */
export function useDivergences({
  identity,
  attempts,
  consolidated,
  fullTitleToSpecPath,
  enabled = true,
}: UseDivergencesArgs) {
  return useQuery<Divergence[] | null>({
    queryKey: [
      'orchestration',
      'divergences',
      compositeIdentityKey(identity),
      attempts?.length ?? 0,
      consolidated?.specs.length ?? 0,
      fullTitleToSpecPath?.size ?? 0,
    ],
    queryFn: () => {
      if (!attempts || attempts.length === 0) return [];
      if (!consolidated || consolidated.specs.length === 0) return [];
      if (!fullTitleToSpecPath || fullTitleToSpecPath.size === 0) return [];
      return computeDivergences(attempts, consolidated, fullTitleToSpecPath);
    },
    enabled:
      enabled &&
      !!identity.repository &&
      !!identity.commit_sha &&
      !!identity.gh_run_id &&
      !!identity.name,
    // Pure derivation: never stale until inputs change.
    staleTime: Infinity,
  });
}

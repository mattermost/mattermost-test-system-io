/**
 * Component tests for `FilteredReportPage`.
 *
 * Mocks the React Query hooks the page consumes (`useConsolidatedResults`,
 * `useOrchestrationRun`, etc.) to drive the four states the page can land in:
 * report-only, orchestration-only, both-empty (404-style), and explicit
 * `?tab=dispatch` URL override.
 *
 * The page reads URL params via `react-router-dom`, so each render is wrapped
 * in a `MemoryRouter` with a route at `/reports/:repo/:branch/:commit/:name`
 * and an initial entry that supplies values plus an optional query string.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import type { ConsolidatedResultsResponse, ReportDetail, TestSuiteListResponse } from '@/types';
import type { RunSnapshot } from '@/types/orchestration';

// Stub every hook the page reaches into. Each test reassigns the
// implementation via `mockReturnValue` so the same module-scoped vi.fn()
// can simulate the four scenarios under test.
const useConsolidatedResultsMock = vi.fn();
const useReportDetailMock = vi.fn();
const useReportSuitesMock = vi.fn();
const useOrchestrationRunMock = vi.fn();
const fetchReportDetailMock = vi.fn();
const fetchReportSuitesMock = vi.fn();

vi.mock('@/services/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/api')>();
  return {
    ...actual,
    useConsolidatedResults: (...args: unknown[]) => useConsolidatedResultsMock(...args),
    useReportDetail: (...args: unknown[]) => useReportDetailMock(...args),
    useReportSuites: (...args: unknown[]) => useReportSuitesMock(...args),
    useOrchestrationRun: (...args: unknown[]) => useOrchestrationRunMock(...args),
    fetchReportDetail: (...args: unknown[]) => fetchReportDetailMock(...args),
    fetchReportSuites: (...args: unknown[]) => fetchReportSuitesMock(...args),
    // The TestSuitesView reaches for `useClientConfig`; stub it with a static
    // shape so the suites view is happy to render in jsdom without firing
    // real fetches.
    useClientConfig: () => ({ data: { search_min_length: 2 } }),
  };
});

// `useQueries` is used internally to fan out per-contributing-report fetches.
// Stub it to return an empty array so the component does not actually fire
// network calls during tests.
vi.mock('@tanstack/react-query', async () => {
  const actual =
    await vi.importActual<typeof import('@tanstack/react-query')>('@tanstack/react-query');
  return {
    ...actual,
    useQueries: () => [],
  };
});

import { FilteredReportPage } from '../filtered_report_page';

function emptyConsolidated(): ConsolidatedResultsResponse {
  return {
    filters: {
      repository: 'mattermost/mattermost',
      target_name: 'playwright-e2e',
      commit_sha: 'abcdef0123456789abcdef0123456789abcdef01',
      tool_name: 'playwright',
    },
    overall_status: 'unknown',
    total_specs: 0,
    passed: 0,
    failed: 0,
    skipped: 0,
    flaky: 0,
    contributing_reports: [],
    latest_commit_sha: 'abcdef0123456789abcdef0123456789abcdef01',
    latest_run_attempt: 1,
    available_run_attempts: [1],
    specs: [],
  };
}

function populatedConsolidated(): ConsolidatedResultsResponse {
  return {
    ...emptyConsolidated(),
    overall_status: 'passed',
    total_specs: 5,
    passed: 5,
    contributing_reports: ['rep-1'],
    specs: [],
  };
}

function reportDetail(): ReportDetail {
  return {
    id: 'rep-1',
    short_id: 'rep1',
    name: 'playwright-e2e',
    framework: 'playwright',
    status: 'completed',
    repository: 'mattermost/mattermost',
    branch: 'main',
    commit: 'abcdef0123456789abcdef0123456789abcdef01',
    gh_run_id: '12345',
    gh_run_attempt: '1',
    created_at: '2026-04-25T10:00:00Z',
    reports: [],
  } as unknown as ReportDetail;
}

function emptySuites(): TestSuiteListResponse {
  return { suites: [], reports: [] } as unknown as TestSuiteListResponse;
}

function orchestrationSnapshot(): RunSnapshot {
  return {
    repository: 'mattermost/mattermost',
    commit_sha: 'abcdef0123456789abcdef0123456789abcdef01',
    gh_run_id: '12345',
    name: 'playwright-e2e',
    gh_run_attempt: '1',
    status: 'in_progress',
    total_units: 4,
    started_at: '2026-04-25T10:00:00Z',
    deadline: '2026-04-25T11:00:00Z',
    counts: {
      pending: 4,
      leased: 0,
      completed_pass: 0,
      completed_fail: 0,
      completed_skipped: 0,
      abandoned: 0,
    },
  };
}

interface RenderOptions {
  search?: string;
}

function renderPage({ search = '' }: RenderOptions = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const initialEntry = `/reports/mattermost%2Fmattermost/main/abcdef0123456789abcdef0123456789abcdef01/playwright-e2e${search}`;
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/reports/:repo/:branch/:commit/:name" element={<FilteredReportPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('FilteredReportPage', () => {
  beforeEach(() => {
    useConsolidatedResultsMock.mockReset();
    useReportDetailMock.mockReset();
    useReportSuitesMock.mockReset();
    useOrchestrationRunMock.mockReset();
    fetchReportDetailMock.mockReset();
    fetchReportSuitesMock.mockReset();

    // Sensible defaults; tests override per-scenario.
    useReportDetailMock.mockReturnValue({ data: undefined });
    useReportSuitesMock.mockReturnValue({ data: emptySuites(), isLoading: false });
  });

  it('hides tabs by default and renders the combine body when the report group has data', () => {
    useConsolidatedResultsMock.mockReturnValue({
      data: populatedConsolidated(),
      isLoading: false,
      error: null,
    });
    useReportDetailMock.mockReturnValue({ data: reportDetail() });
    useOrchestrationRunMock.mockReturnValue({ data: null });

    renderPage();

    expect(screen.queryByRole('tab', { name: /Combine/i })).toBeNull();
    expect(screen.queryByRole('tab', { name: /Dispatch/i })).toBeNull();
    expect(screen.queryByRole('tab', { name: /Reports/i })).toBeNull();
  });

  it('hides tabs by default when only the orchestration run has data', () => {
    useConsolidatedResultsMock.mockReturnValue({
      data: emptyConsolidated(),
      isLoading: false,
      error: null,
    });
    useOrchestrationRunMock.mockReturnValue({ data: orchestrationSnapshot() });

    renderPage();

    expect(screen.queryByRole('tab', { name: /Combine/i })).toBeNull();
    expect(screen.queryByRole('tab', { name: /Dispatch/i })).toBeNull();
  });

  it('shows tabs when ?compare=1 is set, with combine selected by default', () => {
    useConsolidatedResultsMock.mockReturnValue({
      data: populatedConsolidated(),
      isLoading: false,
      error: null,
    });
    useReportDetailMock.mockReturnValue({ data: reportDetail() });
    useOrchestrationRunMock.mockReturnValue({ data: orchestrationSnapshot() });

    renderPage({ search: '?compare=1' });

    const combineTab = screen.getByRole('tab', { name: /Combine/i });
    const dispatchTab = screen.getByRole('tab', { name: /Dispatch/i });
    const reportsTab = screen.getByRole('tab', { name: /Reports/i });
    expect(combineTab).toBeInTheDocument();
    expect(dispatchTab).toBeInTheDocument();
    expect(reportsTab).toBeInTheDocument();
    expect(combineTab.getAttribute('aria-selected')).toBe('true');
    expect(dispatchTab.getAttribute('aria-selected')).toBe('false');
    expect(reportsTab.getAttribute('aria-selected')).toBe('false');
  });

  it('renders the empty state when neither source has data', () => {
    useConsolidatedResultsMock.mockReturnValue({
      data: emptyConsolidated(),
      isLoading: false,
      error: null,
    });
    useOrchestrationRunMock.mockReturnValue({ data: null });

    renderPage();

    expect(screen.getByText('No matching reports')).toBeInTheDocument();
    // Tabs must not render in the empty state.
    expect(screen.queryByRole('tab', { name: /Combine/i })).toBeNull();
    expect(screen.queryByRole('tab', { name: /Dispatch/i })).toBeNull();
    expect(screen.queryByRole('tab', { name: /Reports/i })).toBeNull();
  });

  it('honours ?tab=dispatch in the URL and reveals the tabs implicitly', () => {
    useConsolidatedResultsMock.mockReturnValue({
      data: populatedConsolidated(),
      isLoading: false,
      error: null,
    });
    useReportDetailMock.mockReturnValue({ data: reportDetail() });
    useOrchestrationRunMock.mockReturnValue({ data: orchestrationSnapshot() });

    renderPage({ search: '?tab=dispatch' });

    const dispatchTab = screen.getByRole('tab', { name: /Dispatch/i });
    expect(dispatchTab.getAttribute('aria-selected')).toBe('true');
  });
});

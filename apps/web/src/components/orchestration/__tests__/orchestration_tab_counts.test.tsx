import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import type { RunSnapshot, TestCaseStatus } from '@/types/orchestration';

const { useRun } = vi.hoisted(() => ({ useRun: vi.fn() }));
vi.mock('@/services/api', () => ({
  useOrchestrationRun: useRun,
  useClientConfig: () => ({ data: {} }),
}));
vi.mock('@/services/websocket', () => ({ subscribeToOrchestrationRun: () => () => {} }));
vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));
vi.mock('@/components/report_summary', () => ({
  ReportSummary: ({ passed, failed, flaky, skipped }: Record<string, number>) => (
    <output data-testid="counts">{JSON.stringify({ passed, failed, flaky, skipped })}</output>
  ),
}));

import { OrchestrationTab } from '../orchestration_tab';

function snapshot(statuses: TestCaseStatus[]): RunSnapshot {
  return {
    repository: 'mattermost/mattermost',
    commit_sha: 'abc1234',
    gh_run_id: '123',
    name: 'cypress-full',
    status: 'completed',
    total_units: 1,
    started_at: '2026-09-08T00:00:00Z',
    last_activity_at: '2026-09-08T00:01:00Z',
    idle_timeout_ms: 600000,
    counts: {
      pending: 0,
      leased: 0,
      completed_pass: 1,
      completed_fail: 0,
      completed_skipped: 0,
      abandoned: 0,
    },
    units: [
      {
        id: 'unit',
        dispatch_seq: 0,
        spec_path: 'pasted_image_url_preview_spec.js',
        state: 'completed_pass',
        lease_count: statuses.length,
        fail_count: 0,
        outcome_set_at: null,
        current_lease: null,
        attempts: statuses.map((status, index) => ({
          id: `attempt-${index}`,
          lease_id: `lease-${index}`,
          spec_path: 'pasted_image_url_preview_spec.js',
          status,
          actual_duration_ms: 100,
          error_message: null,
          reported_at: '2026-09-08T00:01:00Z',
          created_at: '2026-09-08T00:00:00Z',
          late_report: false,
          expired: false,
          gh_job_id: `job-${index}`,
          gh_job_name: `worker-${index}`,
          test_cases: [
            {
              title: 'MM-T1',
              full_title: 'MM-T1',
              status,
              retry_count: status === 'flaky' ? 1 : 0,
            },
          ],
        })),
      },
    ],
  };
}

describe('OrchestrationTab test counts', () => {
  it.each<{ statuses: TestCaseStatus[]; expected: string }>([
    { statuses: ['flaky'], expected: 'flaky' },
    { statuses: ['flaky', 'passed'], expected: 'flaky' },
    { statuses: ['failed', 'passed'], expected: 'flaky' },
    { statuses: ['timedOut', 'passed'], expected: 'flaky' },
    { statuses: ['passed'], expected: 'passed' },
    { statuses: ['failed'], expected: 'failed' },
    { statuses: ['skipped'], expected: 'skipped' },
  ])('preserves $statuses as $expected', ({ statuses, expected }) => {
    const run = snapshot(statuses);
    useRun.mockReturnValue({ data: run, isLoading: false, error: null });
    render(<OrchestrationTab identity={run} />);
    expect(JSON.parse(screen.getByTestId('counts').textContent!)).toEqual({
      passed: 0,
      failed: 0,
      flaky: 0,
      skipped: 0,
      [expected]: 1,
    });
  });
});

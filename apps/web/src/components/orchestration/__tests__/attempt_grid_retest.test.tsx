import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { AttemptGrid } from '@/components/orchestration/attempt_grid';
import type { CompositeIdentity, OrchestrationAttempt, RunSnapshot } from '@/types/orchestration';

const identity: CompositeIdentity = {
  repository: 'mattermost/x',
  commit_sha: 'deadbeef',
  gh_run_id: 'r1',
  name: 'playwright',
};

const run: RunSnapshot = {
  ...identity,
  status: 'completed',
  total_units: 1,
  started_at: '2025-01-01T00:00:00Z',
  last_activity_at: '2025-01-01T00:15:00Z',
  idle_timeout_ms: 600000,
  terminal_at: '2025-01-01T00:15:00Z',
  counts: {
    pending: 0,
    leased: 0,
    completed_pass: 1,
    completed_fail: 0,
    completed_skipped: 0,
    abandoned: 0,
  },
};

function makeAttempt(overrides: Partial<OrchestrationAttempt>): OrchestrationAttempt {
  return {
    lease_id: '00000000-0000-0000-0000-000000000001',
    gh_job_name: 'playwright-shard-A',
    gh_job_id: 'job-A',
    deadline: '2025-01-01T00:10:00Z',
    expired: false,
    late_report: false,
    status: 'failed',
    reported_at: '2025-01-01T00:05:00Z',
    spec_path: 'tests/login.spec.ts',
    ...overrides,
  };
}

describe('AttemptGrid retest indicators', () => {
  it('renders the retest icon when an attempt has is_retest=true', () => {
    const attempts: OrchestrationAttempt[] = [
      makeAttempt({
        is_retest: true,
        status: 'passed',
        gh_job_name: 'playwright-shard-B',
        gh_job_id: 'job-B',
      }),
    ];
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    expect(screen.getByTestId('retest-icon')).toBeInTheDocument();
  });

  it('infers retest from a second-or-later attempt on the same spec', () => {
    const attempts: OrchestrationAttempt[] = [
      makeAttempt({
        lease_id: '00000000-0000-0000-0000-000000000001',
        gh_job_id: 'job-A',
        status: 'failed',
        reported_at: '2025-01-01T00:05:00Z',
      }),
      makeAttempt({
        lease_id: '00000000-0000-0000-0000-000000000002',
        gh_job_name: 'playwright-shard-B',
        gh_job_id: 'job-B',
        status: 'passed',
        reported_at: '2025-01-01T00:10:00Z',
      }),
    ];
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    // Exactly one of the two attempts should render the retest icon — the
    // second one. The first attempt is the original first-pass dispatch.
    const icons = screen.getAllByTestId('retest-icon');
    expect(icons).toHaveLength(1);
  });

  it('does not render a retest icon when there is only one attempt', () => {
    const attempts: OrchestrationAttempt[] = [makeAttempt({ status: 'passed', is_retest: false })];
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    expect(screen.queryByTestId('retest-icon')).not.toBeInTheDocument();
  });

  it('renders the full attempt count even when several attempts are retests', () => {
    const attempts: OrchestrationAttempt[] = [
      makeAttempt({ gh_job_id: 'job-A', status: 'failed', reported_at: '2025-01-01T00:05:00Z' }),
      makeAttempt({
        lease_id: '00000000-0000-0000-0000-000000000002',
        gh_job_id: 'job-B',
        status: 'failed',
        reported_at: '2025-01-01T00:10:00Z',
        is_retest: true,
      }),
      makeAttempt({
        lease_id: '00000000-0000-0000-0000-000000000003',
        gh_job_id: 'job-C',
        status: 'passed',
        reported_at: '2025-01-01T00:15:00Z',
        is_retest: true,
      }),
    ];
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    expect(screen.getByText('3 recorded')).toBeInTheDocument();
    expect(screen.getAllByTestId('retest-icon')).toHaveLength(2);
  });
});

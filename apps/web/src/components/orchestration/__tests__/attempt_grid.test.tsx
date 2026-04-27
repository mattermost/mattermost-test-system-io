/**
 * Component tests for `AttemptGrid`.
 *
 * Pure data-driven; no WebSocket required. Asserts that multiple attempts
 * render in chronological order, that the late-report and expired flags
 * are visually surfaced, and that screenshot attachments render as `<img>`
 * tags pointing at `/files/{key}`.
 */

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import type { CompositeIdentity, OrchestrationAttempt, RunSnapshot } from '@/types/orchestration';
import { AttemptGrid } from '../attempt_grid';

const identity: CompositeIdentity = {
  repository: 'mattermost/mattermost',
  commit_sha: 'abcdef0123456789abcdef0123456789abcdef01',
  gh_run_id: '99999',
  name: 'playwright-e2e',
  gh_run_attempt: '1',
};

const run: RunSnapshot = {
  ...identity,
  status: 'completed',
  total_units: 1,
  started_at: '2026-04-25T10:00:00Z',
  deadline: '2026-04-25T11:00:00Z',
  terminal_at: '2026-04-25T10:45:00Z',
  counts: {
    pending: 0,
    leased: 0,
    completed_pass: 1,
    completed_fail: 0,
    completed_skipped: 0,
    abandoned: 0,
  },
};

const attempts: OrchestrationAttempt[] = [
  {
    // attempt 1: clean pass, with one screenshot attachment
    lease_id: 'lease-1',
    gh_job_name: 'shard-1',
    gh_job_id: 'job-1',
    deadline: '2026-04-25T10:15:00Z',
    expired: false,
    late_report: false,
    status: 'passed',
    actual_duration_ms: 1500,
    reported_at: '2026-04-25T10:14:00Z',
    test_cases: [
      {
        title: 'login flow',
        full_title: 'Login spec > login flow',
        status: 'passed',
        retry_count: 0,
        duration_ms: 1500,
        ordinal: 1,
        attachments: {
          screenshots: [{ key: 'shot-key-1', relative_path: 'screenshots/login.png' }],
        },
      },
    ],
  },
  {
    // attempt 2: failed-then-late-report
    lease_id: 'lease-2',
    gh_job_name: 'shard-2',
    gh_job_id: 'job-2',
    deadline: '2026-04-25T10:25:00Z',
    expired: false,
    late_report: true,
    status: 'failed',
    actual_duration_ms: 30_000,
    reported_at: '2026-04-25T10:30:00Z',
    test_cases: [
      {
        title: 'signup flow',
        full_title: 'Signup spec > signup flow',
        status: 'failed',
        retry_count: 0,
        duration_ms: 30_000,
        error_message: 'expected element not visible',
        ordinal: 1,
      },
    ],
  },
  {
    // attempt 3: expired without report
    lease_id: 'lease-3',
    gh_job_name: 'shard-3',
    gh_job_id: 'job-3',
    deadline: '2026-04-25T10:40:00Z',
    expired: true,
    late_report: false,
    status: null,
    reported_at: null,
  },
];

describe('AttemptGrid', () => {
  it('renders one row per attempt for a spec with multiple attempts', () => {
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    // List shows the recorded count and one <li> per attempt.
    expect(screen.getByText('3 recorded')).toBeInTheDocument();
    expect(screen.getByText('shard-1')).toBeInTheDocument();
    expect(screen.getByText('shard-2')).toBeInTheDocument();
    expect(screen.getByText('shard-3')).toBeInTheDocument();
  });

  it('visually surfaces the late-report flag', () => {
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    const lateBadge = screen.getByText('late');
    expect(lateBadge).toBeInTheDocument();
    // The flag must live on the row that actually carries late_report=true.
    const lateRow = screen.getByText('shard-2').closest('li');
    expect(lateRow).not.toBeNull();
    expect(within(lateRow as HTMLElement).getByText('late')).toBe(lateBadge);
  });

  it('visually surfaces the expired flag', () => {
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    const expiredBadge = screen.getByText('expired');
    expect(expiredBadge).toBeInTheDocument();
    const expiredRow = screen.getByText('shard-3').closest('li');
    expect(expiredRow).not.toBeNull();
    expect(within(expiredRow as HTMLElement).getByText('expired')).toBe(expiredBadge);
  });

  it('renders an <img> with src "/files/{key}" for screenshot attachments', () => {
    render(<AttemptGrid identity={identity} run={run} attempts={attempts} />);
    // The first attempt has the screenshot; expand the row so test_cases render.
    const passedRow = screen.getByText('shard-1').closest('li');
    expect(passedRow).not.toBeNull();
    const expandToggle = within(passedRow as HTMLElement).getByRole('button');
    fireEvent.click(expandToggle);

    const img = within(passedRow as HTMLElement).getByRole('img') as HTMLImageElement;
    // Use the attribute directly so jsdom does not eagerly resolve to an
    // absolute URL the way `img.src` does.
    expect(img.getAttribute('src')).toBe('/files/shot-key-1');
    expect(img.getAttribute('alt')).toBe('screenshots/login.png');
  });
});

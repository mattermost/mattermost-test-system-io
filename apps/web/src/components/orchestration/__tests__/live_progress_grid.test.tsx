/**
 * Component tests for `LiveProgressGrid`.
 *
 * Mocks `subscribeToOrchestrationRun` so we can drive synthetic WebSocket
 * events at the component and assert that per-unit row state updates in
 * place across `unit.leased`, `unit.completed`, and `lease.expired` events.
 */

import { act, render, screen, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type {
  CompositeIdentity,
  OrchestrationEvent,
  OrchestrationUnit,
  RunSnapshot,
} from '@/types/orchestration';

// Capture the live event callback the component registers so each test can
// drive it directly. Reset between tests via beforeEach so a stale listener
// from one case can never leak into the next.
let registeredCallback: ((event: OrchestrationEvent) => void) | null = null;
const unsubscribeSpy = vi.fn();
const subscribeSpy = vi.fn(
  (_identity: CompositeIdentity, callback: (event: OrchestrationEvent) => void) => {
    registeredCallback = callback;
    return unsubscribeSpy;
  },
);

vi.mock('@/services/websocket', () => ({
  subscribeToOrchestrationRun: (
    identity: CompositeIdentity,
    callback: (event: OrchestrationEvent) => void,
  ) => subscribeSpy(identity, callback),
}));

import { LiveProgressGrid } from '../live_progress_grid';

const identity: CompositeIdentity = {
  repository: 'mattermost/mattermost',
  commit_sha: 'abcdef0123456789abcdef0123456789abcdef01',
  gh_run_id: '12345',
  name: 'playwright-e2e',
  gh_run_attempt: '1',
};

const run: RunSnapshot = {
  ...identity,
  status: 'in_progress',
  total_units: 3,
  started_at: '2026-04-25T10:00:00Z',
  last_activity_at: '2026-04-25T10:00:00Z',
  idle_timeout_ms: 600000,
  counts: {
    pending: 3,
    leased: 0,
    completed_pass: 0,
    completed_fail: 0,
    completed_skipped: 0,
    abandoned: 0,
  },
};

function makeUnit(unit_id: string, dispatch_seq: number, spec_path: string): OrchestrationUnit {
  return {
    unit_id,
    dispatch_seq,
    spec_path,
    state: 'pending',
    lease_count: 0,
    fail_count: 0,
  };
}

const baseUnits: OrchestrationUnit[] = [
  makeUnit('u-1', 1, 'spec/login.spec.ts'),
  makeUnit('u-2', 2, 'spec/signup.spec.ts'),
  makeUnit('u-3', 3, 'spec/teams.spec.ts'),
];

describe('LiveProgressGrid', () => {
  beforeEach(() => {
    registeredCallback = null;
    unsubscribeSpy.mockClear();
    subscribeSpy.mockClear();
  });

  it('renders a row per submitted dispatch unit', () => {
    render(<LiveProgressGrid identity={identity} run={run} units={baseUnits} />);
    const rows = screen.getAllByRole('row');
    // 1 header row + 3 unit rows
    expect(rows).toHaveLength(1 + baseUnits.length);
    expect(screen.getByText('spec/login.spec.ts')).toBeInTheDocument();
    expect(screen.getByText('spec/signup.spec.ts')).toBeInTheDocument();
    expect(screen.getByText('spec/teams.spec.ts')).toBeInTheDocument();
    // Every row starts in the Pending state.
    expect(screen.getAllByText('Pending')).toHaveLength(3);
  });

  it('updates a row from pending to leased when an orchestration.unit.leased event arrives', () => {
    render(<LiveProgressGrid identity={identity} run={run} units={baseUnits} />);
    expect(subscribeSpy).toHaveBeenCalledTimes(1);
    expect(registeredCallback).not.toBeNull();

    // Capture the original DOM node for the affected row so we can assert it
    // was patched in place rather than remounted.
    const leasedRowBefore = screen.getByText('spec/login.spec.ts').closest('tr');
    expect(leasedRowBefore).not.toBeNull();

    act(() => {
      registeredCallback!({
        type: 'orchestration.unit.leased',
        identity,
        timestamp: '2026-04-25T10:01:00Z',
        payload: {
          gh_job_name: 'shard-1',
          gh_job_id: 'job-1',
          unit_ids: ['u-1'],
          deadline: '2026-04-25T10:30:00Z',
        },
      });
    });

    const leasedRowAfter = screen.getByText('spec/login.spec.ts').closest('tr');
    expect(leasedRowAfter).toBe(leasedRowBefore);
    expect(within(leasedRowAfter as HTMLElement).getByText('Leased')).toBeInTheDocument();
    expect(within(leasedRowAfter as HTMLElement).getByText('shard-1')).toBeInTheDocument();
    // Untouched rows stay pending.
    expect(screen.getAllByText('Pending')).toHaveLength(2);
  });

  it('updates a row to the completed_* state when an orchestration.unit.completed event arrives', () => {
    render(<LiveProgressGrid identity={identity} run={run} units={baseUnits} />);
    expect(registeredCallback).not.toBeNull();

    // Lease u-2 first so the completed transition has a meaningful starting state.
    act(() => {
      registeredCallback!({
        type: 'orchestration.unit.leased',
        identity,
        timestamp: '2026-04-25T10:02:00Z',
        payload: {
          gh_job_name: 'shard-2',
          gh_job_id: 'job-2',
          unit_ids: ['u-2'],
          deadline: '2026-04-25T10:30:00Z',
        },
      });
    });

    act(() => {
      registeredCallback!({
        type: 'orchestration.unit.completed',
        identity,
        timestamp: '2026-04-25T10:05:00Z',
        payload: {
          unit_id: 'u-2',
          outcome: 'completed_pass',
          late_report: false,
          attempts_count: 1,
        },
      });
    });

    const passedRow = screen.getByText('spec/signup.spec.ts').closest('tr');
    expect(passedRow).not.toBeNull();
    expect(within(passedRow as HTMLElement).getByText('Passed')).toBeInTheDocument();
  });

  it('returns a unit to pending when an orchestration.lease.expired event arrives', () => {
    render(<LiveProgressGrid identity={identity} run={run} units={baseUnits} />);
    expect(registeredCallback).not.toBeNull();

    act(() => {
      registeredCallback!({
        type: 'orchestration.unit.leased',
        identity,
        timestamp: '2026-04-25T10:03:00Z',
        payload: {
          gh_job_name: 'shard-3',
          gh_job_id: 'job-3',
          unit_ids: ['u-3'],
          deadline: '2026-04-25T10:30:00Z',
        },
      });
    });

    let teamsRow = screen.getByText('spec/teams.spec.ts').closest('tr');
    expect(within(teamsRow as HTMLElement).getByText('Leased')).toBeInTheDocument();

    act(() => {
      registeredCallback!({
        type: 'orchestration.lease.expired',
        identity,
        timestamp: '2026-04-25T10:31:00Z',
        payload: {
          gh_job_name: 'shard-3',
          gh_job_id: 'job-3',
          released_at: '2026-04-25T10:31:00Z',
          reclaimed_unit_ids: ['u-3'],
        },
      });
    });

    teamsRow = screen.getByText('spec/teams.spec.ts').closest('tr');
    expect(within(teamsRow as HTMLElement).getByText('Pending')).toBeInTheDocument();
  });
});

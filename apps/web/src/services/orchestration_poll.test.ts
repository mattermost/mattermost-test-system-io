import { describe, expect, it } from 'vitest';

import type { RunSnapshot } from '@/types/orchestration';

import { orchestrationRefetchInterval } from './api';

function run(status: RunSnapshot['status']): RunSnapshot {
  return { status } as RunSnapshot;
}

describe('orchestrationRefetchInterval', () => {
  it('polls while the run is in progress', () => {
    expect(orchestrationRefetchInterval(run('in_progress'), 1)).toBe(5000);
  });

  it('stops once the run is terminal', () => {
    expect(orchestrationRefetchInterval(run('completed'), 5)).toBe(false);
    expect(orchestrationRefetchInterval(run('timed_out'), 5)).toBe(false);
  });

  it('retries briefly while no run exists yet', () => {
    expect(orchestrationRefetchInterval(null, 0)).toBe(5000);
    expect(orchestrationRefetchInterval(null, 11)).toBe(5000);
  });

  it('gives up after the null-poll limit (no infinite 404 polling)', () => {
    expect(orchestrationRefetchInterval(null, 12)).toBe(false);
    expect(orchestrationRefetchInterval(null, 50)).toBe(false);
    expect(orchestrationRefetchInterval(undefined, 12)).toBe(false);
  });
});

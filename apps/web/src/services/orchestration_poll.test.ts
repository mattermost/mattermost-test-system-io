import { describe, expect, it } from 'vitest';

import type { RunSnapshot } from '@/types/orchestration';

import { nextNullStreak, orchestrationRefetchInterval } from './api';

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

  it('gives up after the consecutive-null limit (no infinite 404 polling)', () => {
    expect(orchestrationRefetchInterval(null, 12)).toBe(false);
    expect(orchestrationRefetchInterval(null, 50)).toBe(false);
    expect(orchestrationRefetchInterval(undefined, 12)).toBe(false);
  });
});

describe('nextNullStreak', () => {
  it('increments on each null/undefined result', () => {
    expect(nextNullStreak(0, null)).toBe(1);
    expect(nextNullStreak(1, undefined)).toBe(2);
    expect(nextNullStreak(11, null)).toBe(12);
  });

  it('resets to 0 whenever a snapshot is returned', () => {
    expect(nextNullStreak(9, run('in_progress'))).toBe(0);
    expect(nextNullStreak(50, run('completed'))).toBe(0);
  });

  it('success→null keeps polling (streak counts from the reset, not cumulatively)', () => {
    // Many successful in-progress polls, then the run disappears (null).
    let streak = 0;
    for (let i = 0; i < 30; i++) streak = nextNullStreak(streak, run('in_progress'));
    expect(streak).toBe(0);
    streak = nextNullStreak(streak, null); // first miss after successes
    expect(streak).toBe(1);
    // A single miss must NOT stop polling despite 30 prior successful fetches.
    expect(orchestrationRefetchInterval(null, streak)).toBe(5000);
  });
});

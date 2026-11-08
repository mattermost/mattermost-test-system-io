/**
 * Worker deadline arithmetic (`deadline-minutes` input). Kept free of runtime
 * imports so `node --test` loads it directly.
 *
 * A worker past its deadline stops leasing. A spec still running at the
 * deadline is killed, and its lease is reported interrupted. This way the
 * shard upload always runs before the step's timeout-minutes.
 */

/** `deadline-minutes` input: empty means off (null). Otherwise a positive integer. */
export function parseDeadlineMinutes(raw: string): number | null {
  const v = raw.trim();
  if (v === "") return null;
  const n = Number(v);
  if (!Number.isInteger(n) || n <= 0) {
    throw new Error(`deadline-minutes must be a positive integer or empty, got "${raw}"`);
  }
  return n;
}

/** The epoch ms at which the worker stops, minutes after startedAt; null when there is no deadline. */
export function deadlineAtFrom(startedAt: number, minutes: number | null): number | null {
  return minutes === null ? null : startedAt + minutes * 60_000;
}

/** Whether the worker's deadline has passed at now; never when there is no deadline. */
export function deadlineReached(deadlineAt: number | null, now: number): boolean {
  return deadlineAt !== null && now >= deadlineAt;
}

/** Kill timer for one spec run: time left until the deadline. Undefined when off. */
export function specTimeoutMs(deadlineAt: number | null, now: number): number | undefined {
  if (deadlineAt === null) return undefined;
  return Math.max(1, deadlineAt - now);
}

/** Units not yet done by any worker, from a /checkout or /complete `counts` snapshot. */
export function unitsLeftOf(counts: { pending: number; retest_eligible: number }): number {
  return counts.pending + counts.retest_eligible;
}

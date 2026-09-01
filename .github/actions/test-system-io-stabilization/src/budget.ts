/**
 * W14 budget — enforced, not measured. The monthly attempt count is derived
 * from the repository's own PR history (label + branch prefix): the PRs ARE
 * the attempts, so no extra state to keep honest.
 */
import type * as core from "@actions/core";

export interface GitHubSearchFn {
  (query: string): Promise<{ total_count: number }>;
}

// Counted by LABEL, not head: — head search semantics are undocumented as
// prefix-matching, and the branch name appends a date (Opus minor 17). The
// label is applied by the loop itself on every PR it opens.
export const STABILIZATION_PR_QUERY = 'is:pr label:"e2e-stabilization"';

/** Monthly attempts used: stabilization PRs created since the 1st of the month. */
export function monthlyQuery(repo: string, now = new Date()): string {
  const monthStart = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1))
    .toISOString()
    .slice(0, 10);
  return `repo:${repo} ${STABILIZATION_PR_QUERY} created:>=${monthStart}`;
}

export async function monthlyAttemptsUsed(search: GitHubSearchFn, repo: string, now?: Date): Promise<number> {
  const res = await search(monthlyQuery(repo, now));
  return res.total_count;
}

/** True when the loop may take another queue item this month. */
export function withinBudget(used: number, budget: number): boolean {
  return used < budget;
}

export function budgetStopNotice(used: number, budget: number): string {
  return `stabilization monthly budget exhausted: ${used}/${budget} attempts — loop stops taking new queue items and posts instead of failing silently`;
}

export async function assertBudget(
  search: GitHubSearchFn,
  repo: string,
  budget: number,
  log: (m: string) => void,
  now?: Date,
): Promise<boolean> {
  const used = await monthlyAttemptsUsed(search, repo, now);
  if (!withinBudget(used, budget)) {
    log(budgetStopNotice(used, budget));
    return false;
  }
  return true;
}

export const _unused = undefined as unknown as typeof core;

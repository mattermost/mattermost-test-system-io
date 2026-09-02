/**
 * The orchestrator — pure enough to unit-test: every effect (git, GitHub,
 * Claude, ledger, queue fetch) is injected. Rails enforced in order:
 * budget FIRST (a spent month stops before any work), then attempts-per-test
 * (escalate with the diagnosis instead of a 4th PR), then concurrency (the
 * working set is the open-PR count).
 */
import type { QueueEntry, LoopAction } from "./types.ts";
import { STABILIZATION_LABEL, BRANCH_PREFIX } from "./types.ts";
import type { QueueFetchResult } from "./queue.ts";
import { withinBudget, budgetStopNotice } from "./budget.ts";
import { attemptsExhausted } from "./rails.ts";


export interface LoopDeps {
  fetchQueue(baseURL: string, repo: string, depth: number): Promise<QueueFetchResult>;
  monthlyAttemptsUsed(repo: string): Promise<number>;
  openPRCount(repo: string): Promise<number>;
  attemptsForTest(repo: string, testID: string): Promise<number>;
  repair(entry: QueueEntry): Promise<{ summary: string; editedFiles: string[]; routed: boolean; routingReason?: string }>;
  /**
   * R2-4 + round-1 M3: reset the workspace to the pristine base BEFORE each
   * entry's repair — fetch the explicit remote ref, hard-checkout it, drop
   * staged/tracked/untracked residue. Fixes three failures at once: the
   * dirty-tree checkout abort (entry 2 + entry 1's branch), the staged-index
   * poisoning (a self-check rejection leaving entry 1's banned edit staged
   * for entry 2), and co-mingled commits.
   */
  resetWorkspace(): void | Promise<void>;
  /** M14/M16: stage e2e-tests/ edits so the self-check sees exactly what
   * will be committed (untracked included). */
  stageEdits(): void | Promise<void>;
  /** M16: true when the staged diff is non-empty after staging. */
  hasStagedChanges(): boolean | Promise<boolean>;
  selfCheck(): { passed: boolean; violations: Array<{ rule: string; file: string; message: string }> };
  openPR(entry: QueueEntry, summary: string): Promise<{ branch: string; prNumber: number }>;
  routeToOwner(entry: QueueEntry, reason: string): Promise<string>;
  recordAttempt(testID: string, attempt: number, outcome: string, diagnosis: string): Promise<void>;
}

export interface LoopConfig {
  baseURL: string;
  repo: string;
  depth: number;
  concurrency: number;
  maxAttemptsPerTest: number;
  monthlyBudget: number;
  dryRun: boolean;
}

export async function runLoop(deps: LoopDeps, cfg: LoopConfig): Promise<LoopAction[]> {
  const actions: LoopAction[] = [];

  // Budget first — enforced before touching the queue.
  const used = await deps.monthlyAttemptsUsed(cfg.repo);
  if (!withinBudget(used, cfg.monthlyBudget)) {
    return [{ kind: "budget_exhausted", used, budget: cfg.monthlyBudget }];
  }

  // Concurrency: open loop PRs count against the working set.
  const open = await deps.openPRCount(cfg.repo);
  const slots = Math.max(0, cfg.concurrency - open);
  if (slots === 0) {
    return [{ kind: "skipped", testID: "-", reason: `concurrency ${cfg.concurrency} already open` }];
  }

  // M11: ONE fetch; promoted first, then ranked, deduped by test_id — the
  // previous double-fetch could open two PRs for a test present in both
  // lists and blow the attempts cap in a single run.
  const fetched = await deps.fetchQueue(cfg.baseURL, cfg.repo, cfg.depth);
  if (!fetched.ok) {
    // Round-3 major 1: a failed fetch must NOT emit the same terminal action
    // a genuinely empty queue emits — that made a misconfigured OIDC audience
    // report "nothing to do" and exit 0 forever.
    return [{ kind: "queue_unavailable", status: fetched.status }];
  }
  const seen = new Set<string>();
  const queue: QueueEntry[] = [];
  for (const entry of [...fetched.promoted, ...fetched.ranked]) {
    if (seen.has(entry.test_id)) continue;
    seen.add(entry.test_id);
    queue.push(entry);
  }

  let taken = 0;
  for (const entry of queue) {
    if (taken >= slots) break;
    const testID = entry.test_id;

    // Fresh base for every entry — see resetWorkspace. Dry-run touches
    // nothing: no reset, no clean, no commit, no push (round-3 major 2).
    if (!cfg.dryRun) {
      await deps.resetWorkspace();
    }

    // Attempts-per-test cap: escalate with the diagnosis, never a 4th PR.
    const prior = await deps.attemptsForTest(cfg.repo, testID);
    if (attemptsExhausted(prior, cfg.maxAttemptsPerTest)) {
      actions.push({
        kind: "attempts_exhausted",
        testID,
        attempts: prior,
        diagnosis: `${prior} prior stabilization PR(s) — needs a human; the queue keeps the promotion for visibility`,
      });
      taken++;
      continue;
    }

    const result = await deps.repair(entry);
    if (result.routed) {
      const owner = await deps.routeToOwner(entry, result.routingReason ?? "diagnosed as a product bug");
      actions.push({ kind: "routed", testID, owner, reason: result.routingReason ?? "product bug" });
      taken++;
      continue;
    }

    if (cfg.dryRun) {
      actions.push({ kind: "skipped", testID, reason: `dry-run: would open PR (${result.editedFiles.length} file(s))` });
      taken++;
      continue;
    }

    // M14: stage FIRST — the self-check reads --cached and the commit takes
    // exactly what the check saw (untracked files included).
    await deps.stageEdits();

    // M16: a no-op repair (agent changed nothing) must not kill the run —
    // record the attempt and move on; the old path died on an empty commit.
    if (!(await deps.hasStagedChanges())) {
      await deps.recordAttempt(testID, prior + 1, "no_changes", result.summary);
      actions.push({ kind: "skipped", testID, reason: "repair produced no changes — recorded, not retried blindly" });
      taken++;
      continue;
    }

    // W10 self-check BEFORE the push — the loop never fights its own rules.
    const self = await deps.selfCheck();
    if (!self.passed) {
      await deps.recordAttempt(testID, prior + 1, "rejected_by_self_check",
        self.violations.map((v) => `${v.rule} in ${v.file}`).join("; "));
      actions.push({ kind: "skipped", testID, reason: `self-check rejected the diff: ${self.violations[0]?.rule}` });
      taken++;
      continue;
    }

    const pr = await deps.openPR(entry, result.summary);
    await deps.recordAttempt(testID, prior + 1, "pr_opened", result.summary);
    actions.push({ kind: "fix_pr", testID, branch: pr.branch, prNumber: pr.prNumber });
    taken++;
  }

  if (actions.length === 0) {
    actions.push({ kind: "skipped", testID: "-", reason: "queue empty" });
  }
  void budgetStopNotice; void STABILIZATION_LABEL; void BRANCH_PREFIX;
  return actions;
}

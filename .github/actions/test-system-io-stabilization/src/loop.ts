/**
 * The orchestrator — pure enough to unit-test: every effect (git, GitHub,
 * Claude, ledger, queue fetch) is injected. Rails enforced in order:
 * budget FIRST (a spent month stops before any work), then attempts-per-test
 * (escalate with the diagnosis instead of a 4th PR), then concurrency (the
 * working set is the open-PR count).
 */
import type { QueueEntry, LoopAction } from "./types.ts";
import { STABILIZATION_LABEL, BRANCH_PREFIX } from "./types.ts";
import { withinBudget, budgetStopNotice } from "./budget.ts";
import { attemptsExhausted } from "./rails.ts";
import { routeVerdict } from "./routing_deps.ts";

export interface LoopDeps {
  fetchQueue(baseURL: string, repo: string, depth: number): Promise<{ promoted: QueueEntry[]; ranked: QueueEntry[] }>;
  monthlyAttemptsUsed(repo: string): Promise<number>;
  openPRCount(repo: string): Promise<number>;
  attemptsForTest(repo: string, testID: string): Promise<number>;
  repair(entry: QueueEntry): Promise<{ summary: string; editedFiles: string[]; routed: boolean; routingReason?: string }>;
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

  const queue = [...(await deps.fetchQueue(cfg.baseURL, cfg.repo, cfg.depth)).promoted,
                 ...(await deps.fetchQueue(cfg.baseURL, cfg.repo, cfg.depth)).ranked];

  let taken = 0;
  for (const entry of queue) {
    if (taken >= slots) break;
    const testID = entry.test_id;

    // W11: product bugs route, the loop never fixes them.
    const routing = routeVerdict({
      verdict: entry.promotion_source === "release-guard" || entry.promoted ? "MAIN_REGRESSION" : "TEST_DEBT",
      suspectFiles: [],
    });
    if (routing.action === "route" && !entry.promoted) {
      const owner = await deps.routeToOwner(entry, routing.reason);
      actions.push({ kind: "routed", testID, owner, reason: routing.reason });
      taken++;
      continue;
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

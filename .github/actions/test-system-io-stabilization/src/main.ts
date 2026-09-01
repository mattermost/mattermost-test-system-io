/**
 * Wiring only — every rule lives in the tested modules. The caller workflow
 * MUST provide: a checkout of the repo under repair, permissions
 * id-token:write (ledger), contents:write + pull-requests:write (PR),
 * and the repo's default branch checked out.
 */
import * as core from "@actions/core";
import * as path from "node:path";
import { runLoop, type LoopDeps, type LoopConfig } from "./loop.ts";
import { pickQueue, queueURL } from "./queue.ts";
import { clampConcurrency } from "./rails.ts";
import {
  branchName,
  commitAndPush,
  gitDeps,
  openStabilizationPR,
  attemptsForTest,
  stageEdits as stageEditsForCommit,
} from "./pr.ts";
import { repairSpec } from "./agent.ts";
import { checkOwnDiff } from "./self_check.ts";
import { recordAttempt } from "./ledger.ts";
import { STABILIZATION_LABEL, BRANCH_PREFIX, type QueueEntry } from "./types.ts";

const STAGING_URL = "https://staging-test-io.test.mattermost.com";
const PRODUCTION_URL = "https://test-io.test.mattermost.com";

async function gh(method: string, p: string, body?: unknown): Promise<Record<string, unknown>> {
  const token = core.getInput("github-token");
  const res = await fetch(p.startsWith("http") ? p : `https://api.github.com${p}`, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: "application/vnd.github+json",
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`github ${method} ${p}: ${res.status} ${await res.text()}`);
  return (await res.json()) as Record<string, unknown>;
}

async function postLedger(baseURL: string, body: unknown): Promise<void> {
  try {
    const bearer = await core.getIDToken(core.getInput("oidc-audience") || "mattermost-test-system-io");
    const res = await fetch(`${baseURL}/api/v1/triage/verdicts`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
      body: JSON.stringify(body),
    });
    if (!res.ok) core.warning(`ledger write skipped: status ${res.status}`);
  } catch (err) {
    core.warning(`ledger write skipped (no OIDC token): ${(err as Error).message}`);
  }
}

async function main(): Promise<void> {
  const baseURL = core.getInput("use-staging") === "true" ? STAGING_URL : PRODUCTION_URL;
  const repo = core.getInput("repo");
  const workspace = process.env.GITHUB_WORKSPACE ?? process.cwd();
  const depth = Number(core.getInput("queue-depth")) || 10;
  const baseBranch = core.getInput("base-branch") || "master";
  const dryRun = core.getInput("dry-run") === "true";
  const model = core.getInput("claude-model") || "claude-sonnet-4-6";

  const cfg: LoopConfig = {
    baseURL,
    repo,
    depth,
    concurrency: clampConcurrency(Number(core.getInput("concurrency")) || 2),
    maxAttemptsPerTest: Number(core.getInput("max-attempts-per-test")) || 3,
    monthlyBudget: Number(core.getInput("monthly-attempt-budget")) || 20,
    dryRun,
  };

  const git = gitDeps(workspace);
  const commitSHA = git.run(["rev-parse", "HEAD"]).trim();

  const deps: LoopDeps = {
    // R2-2: B7 moved the queue behind RequireAuth — fetch with the same
    // OIDC bearer the ledger writes use, or the loop dies on its first call.
    async fetchQueue(b, r) {
      const url = queueURL(b, r);
      try {
        const bearer = await core.getIDToken(core.getInput("oidc-audience") || "mattermost-test-system-io");
        const res = await fetch(url, { headers: { Authorization: `Bearer ${bearer}` } });
        if (!res.ok) throw new Error(`queue fetch: ${res.status}`);
        return (await res.json()) as { promoted: QueueEntry[]; ranked: QueueEntry[] };
      } catch (err) {
        core.warning(`queue fetch failed (${(err as Error).message}) — standing down this run`);
        return { promoted: [], ranked: [] };
      }
    },
    async monthlyAttemptsUsed(r) {
      const monthStart = new Date();
      monthStart.setUTCDate(1);
      const res = await gh(
        "GET",
        `/search/issues?q=${encodeURIComponent(
          `repo:${r} is:pr label:"${STABILIZATION_LABEL}" created:>=${monthStart.toISOString().slice(0, 10)}`,
        )}&per_page=1`,
      );
      return Number(res.total_count ?? 0);
    },
    async openPRCount(r) {
      const res = await gh(
        "GET",
        `/search/issues?q=${encodeURIComponent(
          `repo:${r} is:pr is:open label:"${STABILIZATION_LABEL}"`,
        )}&per_page=1`,
      );
      return Number(res.total_count ?? 0);
    },
    attemptsForTest: (r, testID) => attemptsForTest(gh as never, r, testID),
    async repair(entry) {
      const stats = `runs=${entry.runs ?? "?"} failed=${entry.failed ?? "?"} flaky=${entry.flaky ?? "?"} flips=${entry.flips ?? "?"} failure_rate=${entry.failure_rate ?? "?"}${
        entry.promotion_reason ? ` promotion=${entry.promotion_reason}` : ""
      }`;
      const evidenceURL = `${baseURL.replace(/^https?:\/\//, "")}`;
      return repairSpec({
        workspace,
        apiKey: core.getInput("anthropic-api-key"),
        model,
        testID: entry.test_id,
        titles: entry.titles ?? [],
        failureStats: stats,
        evidenceURL: `https://${evidenceURL}/`,
      });
    },
    selfCheck() {
      return checkOwnDiff(workspace);
    },
    // R2-4: pristine base per entry. Fetch the EXPLICIT remote ref —
    // `git fetch origin master` writes only FETCH_HEAD and never creates
    // refs/remotes/origin/master, which actions/checkout's narrow refspec
    // may not have created either.
    async resetWorkspace() {
      git.run(["fetch", "origin", `+refs/heads/${baseBranch}:refs/remotes/origin/${baseBranch}`]);
      git.run(["checkout", "-f", "-B", baseBranch, `refs/remotes/origin/${baseBranch}`]);
      git.run(["reset", "--hard"]);
      git.run(["clean", "-fd"]);
    },
    stageEdits() {
      stageEditsForCommit(git);
    },
    hasStagedChanges() {
      try {
        return git.run(["diff", "--cached", "--name-only"]).trim() !== "";
      } catch {
        return false;
      }
    },
    async openPR(entry, summary) {
      const branch = branchName(entry.test_id);
      commitAndPush(git, branch, `stabilization: ${entry.test_id}\n\n${summary}`);
      const title = `stabilization: ${entry.test_id}`;
      const body = [
        `## Flaky test stabilization: \`${entry.test_id}\``,
        "",
        `**Root cause + fix:** ${summary}`,
        "",
        entry.promotion_reason ? `**Queue promotion:** ${entry.promotion_reason}` : "",
        "",
        "Automated by the Test System IO stabilization loop. Human review REQUIRED —",
        "the loop never merges. The W10 ban checker already enforced its own diff;",
        "the semantic residue (weaker assertions rewritten by hand) is the reviewer's job.",
        "",
        `Queue: ${queueURL(baseURL, repo)}`,
      ].join("\n");
      const prNumber = await openStabilizationPR(gh as never, repo, branch, title, body, STABILIZATION_LABEL, baseBranch);
      return { branch, prNumber };
    },
    async routeToOwner(entry, reason) {
      const issue = await gh("POST", `/repos/${repo}/issues`, {
        title: `[stabilization] ${entry.test_id} needs a human`,
        body: `The loop routed instead of fixing: ${reason}\n\nTest: ${entry.test_id}`,
        labels: [`${STABILIZATION_LABEL}-routed`],
      });
      void issue;
      return "test-infra"; // W0 #23: no e2e-tests CODEOWNERS entry yet — honest fallback
    },
    async recordAttempt(testID, attempt, outcome, diagnosis) {
      await recordAttempt(postLedger, baseURL, {
        repository: repo,
        commitSHA,
        ghRunID: process.env.GITHUB_RUN_ID ?? "0",
        testID,
        attempt,
        outcome,
        diagnosis,
      });
    },
  };

  const actions = await runLoop(deps, cfg);
  for (const a of actions) {
    switch (a.kind) {
      case "fix_pr":
        core.info(`opened stabilization PR #${a.prNumber} (${a.branch}) for ${a.testID}`);
        core.setOutput(`pr_${a.testID}`, String(a.prNumber));
        break;
      case "routed":
        core.info(`routed ${a.testID} to ${a.owner}: ${a.reason}`);
        break;
      case "budget_exhausted":
        core.notice(`budget ${a.used}/${a.budget} — loop stopped`);
        break;
      case "attempts_exhausted":
        core.notice(`${a.testID}: ${a.diagnosis}`);
        break;
      case "skipped":
        core.info(`skipped ${a.testID}: ${a.reason}`);
        break;
    }
  }
  core.setOutput("actions", JSON.stringify(actions));
  void path; void pickQueue; void BRANCH_PREFIX;
}

main().catch((err) => {
  core.setFailed(`stabilization loop: ${(err as Error).message}`);
});

/**
 * PR lifecycle — branch, commit, push, open. The loop NEVER merges and never
 * pushes to master; review is mandatory by construction.
 */
import { execFileSync } from "node:child_process";

export interface GitDeps {
  run(args: string[], opts?: { input?: string }): string;
}

export function gitDeps(workspace: string): GitDeps {
  return {
    run: (args, opts) =>
      execFileSync("git", ["-C", workspace, ...args], {
        encoding: "utf8",
        input: opts?.input,
        maxBuffer: 32 * 1024 * 1024,
      }),
  };
}

/**
 * R2-4 + round-3 major 2: reset the workspace to the pristine base BEFORE each
 * entry. Scoped to e2e-tests/ — the loop only ever writes there, so a
 * composite job's untracked non-ignored files and uncommitted edits to tracked
 * files OUTSIDE e2e-tests/ must survive (the old `reset --hard` + `clean -fd`
 * silently discarded them). A missing origin/<base> is a clear error, not a
 * mid-loop crash.
 */
export function resetWorkspace(git: GitDeps, baseBranch: string): void {
  try {
    git.run(["fetch", "origin", `+refs/heads/${baseBranch}:refs/remotes/origin/${baseBranch}`]);
    git.run(["checkout", "-B", baseBranch, `refs/remotes/origin/${baseBranch}`]);
    git.run(["restore", "--staged", "--worktree", "--", "e2e-tests/"]);
    git.run(["clean", "-fd", "--", "e2e-tests/"]);
  } catch (err) {
    throw new Error(
      `workspace reset failed (is origin/${baseBranch} available?): ${(err as Error).message}`,
    );
  }
}

export function branchName(testID: string, now = new Date()): string {
  const slug = testID.toLowerCase().replace(/[^a-z0-9._-]+/g, "-");
  return `stabilization/${slug}-${now.toISOString().slice(0, 10)}`;
}

/**
 * R2-4: the workspace reset (resetWorkspace) is what puts HEAD on a pristine
 * base BEFORE each entry — this fn no longer fetches or force-checkouts at
 * commit time (that aborted on a dirty tree and died when the narrow
 * checkout refspec had never created origin/<base>). It branches off
 * whatever clean base resetWorkspace left, so PR #2 can never contain PR
 * #1's commit and no unreviewed edit rides along.
 */
export function stageEdits(git: GitDeps): void {
  git.run(["add", "--", "e2e-tests/"]);
}

export function commitAndPush(
  git: GitDeps,
  branch: string,
  message: string,
): void {
  git.run(["checkout", "-B", branch]);
  git.run(["commit", "-m", message, "--no-verify"]);
  git.run(["push", "origin", `HEAD:${branch}`]);
}

export interface GitHubApiFn {
  (method: string, path: string, body?: unknown): Promise<{ number?: number; html_url?: string }>;
}

export async function openStabilizationPR(
  api: GitHubApiFn,
  repo: string,
  branch: string,
  title: string,
  body: string,
  label: string,
  baseBranch: string,
): Promise<number> {
  const pr = await api("POST", `/repos/${repo}/pulls`, {
    title,
    body,
    head: branch,
    base: baseBranch,
  });
  if (!pr.number) throw new Error(`PR opened but no number returned for ${branch}`);
  await api("POST", `/repos/${repo}/issues/${pr.number}/labels`, { labels: [label] });
  return pr.number;
}

export async function attemptsForTest(
  api: GitHubApiFn,
  repo: string,
  testID: string,
): Promise<number> {
  // Prior loop PRs for this test, counted by the PR TITLE (the branch name
  // appends a date, and head: search semantics are not documented as prefix
  // matching — the title is the loop's own stable convention:
  // "stabilization: <testID>").
  const q = `repo:${repo} is:pr label:"e2e-stabilization" in:title "stabilization: ${testID}"`;
  const res = await api(
    "GET",
    `/search/issues?q=${encodeURIComponent(q)}&per_page=1`,
  );
  return Number((res as unknown as { total_count?: number }).total_count ?? 0);
}

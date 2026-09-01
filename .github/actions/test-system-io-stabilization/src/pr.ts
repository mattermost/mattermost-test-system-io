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

export function branchName(testID: string, now = new Date()): string {
  const slug = testID.toLowerCase().replace(/[^a-z0-9._-]+/g, "-");
  return `stabilization/${slug}-${now.toISOString().slice(0, 10)}`;
}

/**
 * M12: every stabilization PR branches from origin/master, NOT from the
 * current HEAD. The loop runs serially in one workspace — branching from
 * HEAD made PR #2 contain PR #1's commit, and approving #2 approved #1's
 * unreviewed edit. M14: stage e2e-tests/ FIRST so the self-check's
 * --cached diff sees exactly what will be committed (untracked files
 * included, banned or not).
 */
export function stageEdits(git: GitDeps): void {
  git.run(["add", "--", "e2e-tests/"]);
}

export function commitAndPush(
  git: GitDeps,
  branch: string,
  message: string,
): void {
  git.run(["fetch", "origin", "master"]);
  git.run(["checkout", "-B", branch, "origin/master"]);
  // Branch switched — the staged index follows the switch; re-stage in case
  // the checkout touched paths, then commit only what the self-check saw.
  git.run(["add", "--", "e2e-tests/"]);
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
): Promise<number> {
  const pr = await api("POST", `/repos/${repo}/pulls`, {
    title,
    body,
    head: branch,
    base: "master",
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

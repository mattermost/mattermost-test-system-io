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

export function commitAndPush(
  git: GitDeps,
  branch: string,
  message: string,
): void {
  git.run(["checkout", "-b", branch]);
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
  // Prior loop PRs for this test = existing branches with its slug.
  const slug = testID.toLowerCase().replace(/[^a-z0-9._-]+/g, "-");
  const res = await api(
    "GET",
    `/search/issues?q=repo:${repo}+is:pr+label:"e2e-stabilization"+head:"stabilization/${slug}"&per_page=1`,
  );
  return Number((res as unknown as { total_count?: number }).total_count ?? 0);
}

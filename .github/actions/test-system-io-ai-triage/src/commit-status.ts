/**
 * Set a GitHub commit status via `POST /repos/{owner}/{repo}/statuses/{sha}`.
 *
 * Used by the summary action to flip the `pending` status the begin
 * action pushed to a terminal state (`success`, `failure`, or `error`).
 * `target_url` continues to point at the Test System IO report page so
 * the commit-status row stays the single entrypoint into the dashboard.
 *
 * Failures here are warnings only — the run has already finished by the
 * time this fires, so a flaky GitHub API moment must not break the job.
 * Transient 5xx / 429 retries are handled by `@octokit/plugin-retry`.
 */
import * as core from "@actions/core";
import { GitHub, getOctokitOptions } from "@actions/github/lib/utils";
import { retry } from "@octokit/plugin-retry";

// GitHub caps commit-status descriptions at 140 chars.
const DESCRIPTION_MAX = 140;

const RetryingOctokit = GitHub.plugin(retry);

// Source the state union from Octokit's typings so the four-string set
// stays in sync with the API spec without a hand-maintained literal.
type CreateCommitStatusParams = Parameters<
  InstanceType<typeof RetryingOctokit>["rest"]["repos"]["createCommitStatus"]
>[0];
export type CommitStatusState = NonNullable<CreateCommitStatusParams>["state"];

export interface CommitStatusArgs {
  token: string;
  owner: string;
  repo: string;
  sha: string;
  state: CommitStatusState;
  context: string;
  description: string;
  targetURL: string;
}

export async function setCommitStatus(args: CommitStatusArgs): Promise<void> {
  const { token, owner, repo, sha, state, context, description, targetURL } = args;
  if (!token) {
    core.warning("update-commit-status: github-token not provided; skipping status update.");
    return;
  }
  // GitHub auto-masks secrets sourced from `secrets.*`; this is belt-and-
  // suspenders for cases where the caller wires a non-secret token.
  core.setSecret(token);
  try {
    const octokit = new RetryingOctokit(getOctokitOptions(token));
    await octokit.rest.repos.createCommitStatus({
      owner,
      repo,
      sha,
      state,
      context,
      description: truncateDescription(description),
      target_url: targetURL,
    });
    core.info(`update-commit-status: ${context} = ${state}`);
  } catch (e) {
    core.warning(`update-commit-status: ${(e as Error).message}`);
  }
}

function truncateDescription(s: string): string {
  if (s.length <= DESCRIPTION_MAX) return s;
  core.warning(
    `update-commit-status: description is ${s.length} chars; truncating to GitHub's ${DESCRIPTION_MAX}-char limit.`,
  );
  return `${s.slice(0, DESCRIPTION_MAX - 1)}…`;
}

/**
 * Replay — measure the classifier on runs TSIO has already ingested.
 *
 * The collection window (spec §7 step 1) merges the server and leaves the
 * calling repository's workflows alone, so no CI job ever calls this action and
 * no verdict is ever written. History accumulates; accuracy does not. That is
 * the one number blocking the gate.
 *
 * Replay closes it without a shadow flag and without a second code path that
 * only exists to be inert: it walks failed runs the server already holds and
 * puts each one through the SAME pieces a live run uses — `fetchEvidence`, the
 * deterministic classifier, `investigate`, `decide`, `writeLedger`. The rows it
 * writes are real. `check_state` and `waived` are real. Nothing flips, because
 * no CI job is listening.
 *
 * Two deliberate differences from a live run, both of which exist so the
 * measurement cannot be flattering:
 *
 *   - `mode` is always "gate". Replay is asking "what WOULD the gate have
 *     done", so waiving must be permitted; a shadow-mode replay would record
 *     `waived=false` everywhere and measure nothing.
 *   - the ledger batch is marked `replay: true`, so GET /triage/accuracy counts
 *     it separately. A replay verdict is decided with later runs of the same
 *     test already in the database — folding it into the live figure would
 *     overstate what CI actually does.
 *
 * No commit status is set and no PR comment is posted. Those are the two things
 * a developer would see, and a measurement must not reach into a PR that closed
 * weeks ago.
 */
import * as core from "@actions/core";

import { investigate } from "./agent.ts";
import {
  attachBlame,
  compareCommits,
  fetchEvidence,
  getPrDiff,
  getTestSource,
  listChangedFiles,
  writeLedger,
} from "./main.ts";
import { decide } from "./policy.ts";
import { retryFetch, parseJSON } from "./retry-fetch.ts";
import type { CompositeIdentity, Decision } from "./types.ts";

const PRODUCTION_URL = "https://test-system-io.internal.mattermost.com";
const STAGING_URL = "https://test-system-io-staging.internal.mattermost.com";

/** Cap per cluster, same as a live run: the model is the expensive part. */
const MAX_AGENT_CLUSTERS = 8;

interface ReplayCandidate {
  group_id: string;
  repository: string;
  branch: string;
  commit_sha: string;
  gh_run_id: string;
  gh_pr_number: number | null;
  name: string;
  failed: number;
}

export async function runReplay(): Promise<void> {
  const baseURL = core.getInput("use-staging") === "true" ? STAGING_URL : PRODUCTION_URL;
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const repo = core.getInput("replay-repo", { required: true });
  const baseline = core.getInput("baseline-branch") || "main";
  const branch = core.getInput("replay-branch");
  const days = clampInt(core.getInput("replay-days"), 30, 1, 180);
  const limit = clampInt(core.getInput("replay-limit"), 20, 1, 500);
  const githubToken = core.getInput("github-token");
  const anthropicKey = core.getInput("anthropic-api-key");
  const model = core.getInput("claude-model") || "claude-sonnet-4-6";

  if (!anthropicKey) {
    // Without a key the model never runs, every ambiguous cluster stays on its
    // history suggestion, and the batch measures the deterministic half only.
    // That is a legitimate run — it is what the classifier alone can decide —
    // but it must be stated, because the number it produces is not the AI's.
    core.warning(
      "no anthropic-api-key: replay will record deterministic verdicts only, " +
        "and the resulting accuracy figure is the classifier's, not the model's",
    );
  }

  const candidates = await fetchCandidates(baseURL, repo, branch, days, limit);
  core.info(`replay: ${candidates.length} candidate run(s) for ${repo} over ${days}d`);
  if (candidates.length === 0) {
    core.info("nothing to replay — every ingested failing run already has a ledger row");
    return;
  }

  let adjudicated = 0;
  let ledgerFailures = 0;

  for (const c of candidates) {
    const identity: CompositeIdentity = {
      repository: c.repository,
      branch: c.branch,
      commit_sha: c.commit_sha,
      gh_run_id: c.gh_run_id,
      gh_run_attempt: "1",
      name: c.name,
    };

    let pack;
    try {
      pack = await fetchEvidence(baseURL, identity, c.group_id, baseline);
    } catch (err) {
      // One unreadable run must not end the batch: the point of replay is
      // volume, and a group that 404s (deleted report, pruned artifact) is a
      // fact about that run, not a failure of the job.
      core.warning(`skipping ${c.group_id}: ${(err as Error).message}`);
      continue;
    }

    const runType = pack.group.gh_pr_number ? "PR" : "MAIN";
    const changedFiles = await listChangedFiles(
      githubToken,
      pack.group.repository,
      pack.group.gh_pr_number,
    );

    const decisions: Decision[] = [];
    for (const cluster of pack.clusters || []) {
      const recovered =
        cluster.representative.retry_count > 0 || cluster.representative.status === "flaky";
      const needsAI = cluster.suggested.needs_ai || recovered;
      let ai = undefined;
      if (needsAI && anthropicKey && decisions.length < MAX_AGENT_CLUSTERS) {
        try {
          ai = await investigate(cluster, {
            baseURL,
            apiKey: anthropicKey,
            model,
            group: pack.group,
            baselineBranch: baseline,
            changedFiles,
            compareCommits: (base, head) =>
              compareCommits(githubToken, pack.group.repository, base, head),
            getPrDiff: () => getPrDiff(githubToken, pack.group.repository, pack.group.gh_pr_number),
            getTestSource: (path, sha) =>
              getTestSource(githubToken, pack.group.repository, path, sha),
          });
        } catch (err) {
          core.warning(`agent failed on ${cluster.signature}: ${(err as Error).message}`);
        }
      }

      const d = decide({
        failure: cluster.representative,
        runType,
        branch: pack.group.branch || "",
        changedFiles,
        ai,
        // Always "gate": replay asks what the gate WOULD have done.
        mode: "gate",
      });
      d.member_count = cluster.member_count;
      decisions.push(await attachBlame(d, cluster, githubToken, pack.group.repository));
    }

    if (decisions.length === 0) continue;

    const ok = await writeLedger(baseURL, audience, pack, decisions, model, true);
    if (!ok) {
      // A live run refuses to flip a check when the ledger write fails. Replay
      // flips nothing, so there is nothing to refuse — but an unrecorded
      // verdict is an unmeasured one, so it is counted and reported.
      ledgerFailures++;
      core.warning(`ledger write failed for ${c.group_id} — this run stays unmeasured`);
      continue;
    }
    adjudicated++;
    const waived = decisions.filter((d) => d.waived).length;
    core.info(
      `${c.group_id} ${runType} ${c.commit_sha.slice(0, 8)}: ` +
        `${decisions.length} cluster(s), ${waived} would have been waived`,
    );
  }

  core.info(
    `replay complete: ${adjudicated}/${candidates.length} run(s) recorded` +
      (ledgerFailures > 0 ? `, ${ledgerFailures} ledger failure(s)` : ""),
  );
  core.setOutput("replayed", String(adjudicated));
  core.setOutput("ledger_failures", String(ledgerFailures));
  if (ledgerFailures > 0) {
    core.setFailed(`${ledgerFailures} run(s) could not be recorded`);
  }
}

async function fetchCandidates(
  baseURL: string,
  repo: string,
  branch: string,
  days: number,
  limit: number,
): Promise<ReplayCandidate[]> {
  const params = new URLSearchParams({
    repo,
    days: String(days),
    limit: String(limit),
  });
  if (branch) params.set("branch", branch);
  const res = await retryFetch(
    `${baseURL}/api/v1/triage/replay/candidates?${params.toString()}`,
    {},
    "triage/replay/candidates",
  );
  if (!res.ok) {
    throw new Error(`triage/replay/candidates HTTP ${res.status} ${await res.text()}`);
  }
  const body = await parseJSON<{ candidates?: ReplayCandidate[] }>(
    res,
    "triage/replay/candidates",
  );
  return body.candidates ?? [];
}

/** Parses an action input that must land inside a range, falling back rather
 * than failing: a bad `replay-days` should not lose the whole batch. */
export function clampInt(raw: string, dflt: number, min: number, max: number): number {
  const n = Number.parseInt(raw, 10);
  if (!Number.isInteger(n)) return dflt;
  if (n < min) return min;
  if (n > max) return max;
  return n;
}

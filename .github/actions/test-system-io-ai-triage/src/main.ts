/**
 * Investigate clustered E2E failures via TSIO APIs and artifacts.
 * Never reruns tests — 300 identical failures are one cluster, one agent.
 */

import * as fs from "node:fs";
import * as core from "@actions/core";
import { GitHub, getOctokitOptions } from "@actions/github/lib/utils";
import { retry } from "@octokit/plugin-retry";
import { investigate } from "./agent.ts";
import {
  attribute,
  finishBlame,
  kindOf,
  resolveSuspectRange,
  type CompareCommit,
} from "./blame.ts";
import { setCommitStatus, type CommitStatusState } from "./commit-status.ts";
import { decide, rollup } from "./policy.ts";
import { buildReportURL } from "./report_url.ts";
import { retryFetch } from "./retry-fetch.ts";
import type { CompositeIdentity, Decision, EvidenceCluster, EvidencePack } from "./types.ts";

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";
const MAX_AGENT_CLUSTERS = 8;
const RetryingOctokit = GitHub.plugin(retry);

export async function run(): Promise<void> {
  const baseURL = core.getInput("use-staging") === "true" ? STAGING_URL : PRODUCTION_URL;
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const identity = parseIdentity(core.getInput("composite-identity", { required: true }));
  const groupID = core.getInput("group-id");
  const baseline = core.getInput("baseline-branch") || "main";
  const runType = core.getInput("run-type") || "PR";
  const mode = (core.getInput("mode") || "shadow").toLowerCase();
  const contextName = core.getInput("commit-status-context") || "e2e-test/ai-triage";
  const githubToken = core.getInput("github-token");
  const anthropicKey = core.getInput("anthropic-api-key");
  const model = core.getInput("claude-model") || "claude-sonnet-4-6";

  const reportURL = buildReportURL(baseURL, identity);
  core.setOutput("report_url", reportURL);

  const pack = await fetchEvidence(baseURL, identity, groupID, baseline);
  core.info(
    `evidence: group=${pack.group.id} failures=${pack.failure_count} clusters=${pack.cluster_count}` +
      (pack.truncated ? " truncated=true" : ""),
  );

  const changedFiles = await listChangedFiles(
    githubToken,
    pack.group.repository,
    pack.group.gh_pr_number,
  );
  const decisions: Decision[] = [];

  for (const cluster of pack.clusters || []) {
    let ai = undefined;
    if (cluster.suggested.needs_ai && anthropicKey && agentCalls(decisions) < MAX_AGENT_CLUSTERS) {
      core.info(
        `agent: ${cluster.signature} ×${cluster.member_count} (${cluster.label.slice(0, 80)})`,
      );
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
        });
      } catch (err) {
        core.warning(`agent failed: ${(err as Error).message}; failing closed`);
      }
    } else if (cluster.suggested.needs_ai && !anthropicKey) {
      core.info(`no anthropic key; leaving cluster ${cluster.signature} on history suggestion`);
    }

    const d = decide({
      failure: cluster.representative,
      runType,
      branch: pack.group.branch || identity.branch || "",
      changedFiles,
      ai,
    });
    d.member_count = cluster.member_count;
    const blamed = await attachBlame(d, cluster, githubToken, pack.group.repository);
    decisions.push(blamed);
    core.info(
      `${cluster.signature} ×${cluster.member_count}: kind=${blamed.kind} ${blamed.verdict} ` +
        `waived=${blamed.waived}` +
        (blamed.suspect_author ? ` author=@${blamed.suspect_author}` : ""),
    );
  }

  const summary = rollup(decisions);
  await writeLedger(baseURL, audience, pack, decisions, model);
  await writeStepSummary(pack.clusters || [], decisions, summary, reportURL);

  if (githubToken) {
    const [owner, repo] = splitRepo(pack.group.repository);
    await setCommitStatus({
      token: githubToken,
      owner,
      repo,
      sha: pack.group.commit_sha,
      state: summary.state as CommitStatusState,
      context: contextName,
      description: summary.description,
      targetURL: reportURL,
    });
  }

  core.setOutput("state", summary.state);
  core.setOutput("waived", String(summary.waived));
  core.setOutput("verdict", summary.verdict);
  core.setOutput("description", summary.description);

  if (mode === "gate" && summary.state === "failure") {
    core.setFailed(summary.description);
  }
}

function agentCalls(decisions: Decision[]): number {
  return decisions.filter((d) => d.source === "model").length;
}

async function attachBlame(
  d: Decision,
  cluster: EvidenceCluster,
  githubToken: string,
  repository: string,
): Promise<Decision> {
  d.kind = kindOf(d.verdict);
  if (d.kind !== "bug") return d;

  const range = resolveSuspectRange(cluster.representative.history);
  if (!range.resolvable || !range.lastPass || !range.failingSince || !githubToken) {
    return d;
  }
  try {
    const commits = await compareCommits(
      githubToken,
      repository,
      range.lastPass,
      range.failingSince,
    );
    const blamed = finishBlame({
      verdict: d.verdict,
      history: cluster.representative.history,
      range,
      attributed: attribute(commits),
    });
    if (blamed.suspect) {
      d.suspect_sha = blamed.suspect.sha;
      d.suspect_author = blamed.suspect.author || undefined;
      d.reason =
        `${d.reason} — ${blamed.reason}: ${blamed.suspect.sha.slice(0, 7)}` +
        (blamed.suspect.author ? ` @${blamed.suspect.author}` : "");
    } else if (blamed.candidates.length > 0) {
      d.reason = `${d.reason} — ${blamed.reason}`;
    }
  } catch (err) {
    core.warning(`blame: ${(err as Error).message}`);
  }
  return d;
}

function parseIdentity(raw: string): CompositeIdentity {
  let parsed: CompositeIdentity;
  try {
    parsed = JSON.parse(raw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  if (!parsed.repository || !parsed.commit_sha || !parsed.gh_run_id || !parsed.name) {
    throw new Error("composite-identity needs repository, commit_sha, gh_run_id, name");
  }
  parsed.gh_run_attempt = String(parsed.gh_run_attempt || "1");
  return parsed;
}

async function fetchEvidence(
  baseURL: string,
  identity: CompositeIdentity,
  groupID: string,
  baseline: string,
): Promise<EvidencePack> {
  const params = new URLSearchParams({
    baseline_branch: baseline,
    window: "30d",
    elsewhere_window: "24h",
  });
  if (groupID) {
    params.set("group_id", groupID);
  } else {
    params.set("repository", identity.repository);
    params.set("commit_sha", identity.commit_sha);
    params.set("gh_run_id", identity.gh_run_id);
    params.set("name", identity.name);
    params.set("gh_run_attempt", identity.gh_run_attempt);
  }
  const url = `${baseURL}/api/v1/triage/evidence?${params.toString()}`;
  const res = await retryFetch(url, {}, "triage/evidence");
  if (res.status === 404) {
    throw new Error(
      `no report group for ${identity.repository} ${identity.commit_sha} ${identity.name}`,
    );
  }
  if (!res.ok) {
    throw new Error(`triage/evidence HTTP ${res.status} ${await res.text()}`);
  }
  return (await res.json()) as EvidencePack;
}

async function listChangedFiles(
  token: string,
  repository: string,
  prNumber?: number,
): Promise<string[]> {
  if (!token || !prNumber) return [];
  const [owner, repo] = splitRepo(repository);
  try {
    const octokit = new RetryingOctokit(getOctokitOptions(token));
    const files: string[] = [];
    for await (const page of octokit.paginate.iterator(octokit.rest.pulls.listFiles, {
      owner,
      repo,
      pull_number: prNumber,
      per_page: 100,
    })) {
      for (const f of page.data) files.push(f.filename);
      if (files.length >= 300) break;
    }
    core.info(`pr #${prNumber}: ${files.length} changed file(s)`);
    return files;
  } catch (err) {
    core.warning(`listChangedFiles: ${(err as Error).message}`);
    return [];
  }
}

async function compareCommits(
  token: string,
  repository: string,
  base: string,
  head: string,
): Promise<CompareCommit[]> {
  if (!token) return [];
  const [owner, repo] = splitRepo(repository);
  const octokit = new RetryingOctokit(getOctokitOptions(token));
  const res = await octokit.rest.repos.compareCommits({ owner, repo, base, head });
  return (res.data.commits || []).map((c) => ({
    sha: c.sha,
    parents: c.parents,
    author: c.author,
    commit: c.commit,
  }));
}

async function writeLedger(
  baseURL: string,
  audience: string,
  pack: EvidencePack,
  decisions: Decision[],
  model: string,
): Promise<void> {
  if (decisions.length === 0) return;
  let bearer: string;
  try {
    bearer = await core.getIDToken(audience);
    core.setSecret(bearer);
  } catch (err) {
    core.warning(`ledger skipped (no OIDC token): ${(err as Error).message}`);
    return;
  }

  const body = {
    repository: pack.group.repository,
    branch: pack.group.branch,
    commit_sha: pack.group.commit_sha,
    gh_run_id: pack.group.gh_run_id,
    gh_pr_number: pack.group.gh_pr_number,
    model,
    verdicts: decisions.map((d, i) => {
      const c = pack.clusters[i]!;
      const testID = c.representative.external_test_id;
      return {
        external_test_id: testID,
        cluster_signature: c.signature,
        member_count: c.member_count,
        verdict: d.verdict,
        confidence: d.confidence,
        root_cause: d.reason,
        evidence: d.citations.map((cit) => ({ citation: cit })),
        suspect_commit: d.suspect_sha,
        check_state: d.check_state,
        waived: d.waived,
      };
    }),
  };

  const res = await retryFetch(
    `${baseURL}/api/v1/triage/verdicts`,
    {
      method: "POST",
      headers: {
        authorization: `Bearer ${bearer}`,
        "content-type": "application/json",
      },
      body: JSON.stringify(body),
    },
    "triage/verdicts",
  );
  if (!res.ok) {
    core.warning(`ledger write HTTP ${res.status} ${await res.text()}`);
  }
}

async function writeStepSummary(
  clusters: EvidenceCluster[],
  decisions: Decision[],
  summary: ReturnType<typeof rollup>,
  reportURL: string,
): Promise<void> {
  const lines = [
    `## E2E flake triage`,
    ``,
    `**Outcome:** \`${summary.description}\` — [report](${reportURL})`,
    ``,
    `No rerun. Cost scales with distinct error signatures, not failure count.`,
    ``,
    `| Kind | Cluster | n | Verdict | Author | Waived | Why |`,
    `|---|---|---:|---|---|---|---|`,
  ];
  for (let i = 0; i < decisions.length; i++) {
    const d = decisions[i]!;
    const c = clusters[i]!;
    const author = d.suspect_author
      ? `@${d.suspect_author} (\`${(d.suspect_sha || "").slice(0, 7)}\`)`
      : "—";
    lines.push(
      `| ${d.kind} | \`${c.signature.slice(0, 8)}\` ${c.label.replace(/\|/g, " ").slice(0, 60)} | ${d.member_count} | ${d.verdict} | ${author} | ${d.waived ? "yes" : "no"} | ${d.reason.replace(/\|/g, " ").slice(0, 140)} |`,
    );
  }
  if (decisions.length === 0) {
    lines.push(`| — | — | — | — | — | — | no failures |`);
  }
  const file = process.env.GITHUB_STEP_SUMMARY;
  if (file) fs.appendFileSync(file, lines.join("\n") + "\n");
}

function splitRepo(repository: string): [string, string] {
  const [owner, repo] = repository.split("/");
  if (!owner || !repo) throw new Error(`repository ${repository} is not owner/repo`);
  return [owner, repo];
}

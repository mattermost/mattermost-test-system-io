/**
 * Classify a run's failures from TSIO evidence (history, screenshots, logs)
 * plus the PR diff. Never reruns tests.
 */

import * as fs from "node:fs";
import * as core from "@actions/core";
import { GitHub, getOctokitOptions } from "@actions/github/lib/utils";
import { retry } from "@octokit/plugin-retry";
import { adjudicate } from "./claude.ts";
import { setCommitStatus, type CommitStatusState } from "./commit-status.ts";
import { decide, rollup } from "./policy.ts";
import { buildReportURL } from "./report_url.ts";
import { retryFetch } from "./retry-fetch.ts";
import type { CompositeIdentity, Decision, EvidencePack, EvidenceFailure } from "./types.ts";

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";
const MAX_AI_FAILURES = 8;
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
    `evidence: group=${pack.group.id} failures=${pack.failures.length} lookups=${pack.lookups}` +
      (pack.truncated ? " truncated=true" : ""),
  );

  const changedFiles = await listChangedFiles(
    githubToken,
    pack.group.repository,
    pack.group.gh_pr_number,
  );
  const decisions: Decision[] = [];

  for (const failure of pack.failures) {
    let ai = undefined;
    if (
      failure.suggested.needs_ai &&
      anthropicKey &&
      decisions.filter((d) => d.source === "model").length < MAX_AI_FAILURES
    ) {
      core.info(`claude: ${failure.full_title}`);
      ai = await adjudicate(failure, { baseURL, apiKey: anthropicKey, model, changedFiles });
    } else if (failure.suggested.needs_ai && !anthropicKey) {
      core.info(`no anthropic key; leaving ${failure.full_title} on history suggestion`);
    }
    const d = decide({
      failure,
      runType,
      branch: pack.group.branch || identity.branch || "",
      changedFiles,
      ai,
    });
    decisions.push(d);
    core.info(
      `${failure.full_title}: ${d.verdict} conf=${d.confidence} waived=${d.waived} source=${d.source}`,
    );
  }

  const summary = rollup(decisions);
  await writeLedger(baseURL, audience, pack, decisions, model);
  await writeStepSummary(pack.failures, decisions, summary, reportURL);

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
      const f = pack.failures[i]!;
      return {
        external_test_id: f.external_test_id,
        cluster_signature: f.external_test_id ? undefined : slug(f.full_title),
        member_count: 1,
        verdict: d.verdict,
        confidence: d.confidence,
        root_cause: d.reason,
        evidence: d.citations.map((c) => ({ citation: c })),
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
  failures: EvidenceFailure[],
  decisions: Decision[],
  summary: ReturnType<typeof rollup>,
  reportURL: string,
): Promise<void> {
  const lines = [
    `## E2E flake triage`,
    ``,
    `**Outcome:** \`${summary.description}\` — [report](${reportURL})`,
    ``,
    `| Test | Verdict | Conf | Waived | Source | Why |`,
    `|---|---|---:|---|---|---|`,
  ];
  for (let i = 0; i < decisions.length; i++) {
    const d = decisions[i]!;
    const f = failures[i]!;
    const title = (f.external_test_id || f.full_title).replace(/\|/g, "\\|");
    lines.push(
      `| ${title} | ${d.verdict} | ${d.confidence.toFixed(2)} | ${d.waived ? "yes" : "no"} | ${d.source} | ${d.reason.replace(/\|/g, " ").slice(0, 160)} |`,
    );
  }
  if (decisions.length === 0) {
    lines.push(`| — | — | — | — | — | no failures |`);
  }
  const file = process.env.GITHUB_STEP_SUMMARY;
  if (file) fs.appendFileSync(file, lines.join("\n") + "\n");
}

function splitRepo(repository: string): [string, string] {
  const [owner, repo] = repository.split("/");
  if (!owner || !repo) throw new Error(`repository ${repository} is not owner/repo`);
  return [owner, repo];
}

function slug(title: string): string {
  return (
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 80) || "untitled"
  );
}

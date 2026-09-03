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
import {
  setCommitStatus,
  listLatestCommitStatuses,
  type CommitStatusState,
} from "./commit-status.ts";
import {
  contextsToUpdate,
  originalStatusDescription,
  parseContextList,
  parseRunCounts,
  type RunCounts,
} from "./flip.ts";
import { formatTriageComment, upsertTriageComment } from "./triage-comment.ts";
import { decide, rollup, mayFlipChecks } from "./policy.ts";
import { repoRelSpecCandidates } from "./spec-paths.ts";
import { buildReportURL } from "./report_url.ts";
import { retryFetch, parseJSON } from "./retry-fetch.ts";
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
  // Gating is owned by the calling workflow, not by server state: an
  // unrecognised value is a fail-closed event — shadow, never gate.
  const mode = (core.getInput("mode") || "shadow").toLowerCase() === "gate" ? "gate" : "shadow";
  const contextName = core.getInput("commit-status-context") || "e2e-test/ai-triage";
  const originalContexts = parseContextList(core.getInput("original-commit-status-contexts"));
  const githubToken = core.getInput("github-token");
  // PAT for privileged GitHub ops (PR comments) — the reusable-workflow GITHUB_TOKEN
  // is often capped at contents:read + statuses, which cannot post comments.
  const prToken = core.getInput("pr-token") || githubToken;
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
    // Retry-recovered clusters (server sets needs_ai=false for them) MUST also
    // get AI adjudication — recovery alone cannot distinguish flake from a
    // timing-sensitive product bug. Human triagers look at history, other PRs,
    // and the screenshot; the agent must too.
    const recovered =
      cluster.representative.retry_count > 0 || cluster.representative.status === "flaky";
    const needsAI = cluster.suggested.needs_ai || recovered;
    let ai = undefined;
    if (needsAI && anthropicKey && agentCalls(decisions) < MAX_AGENT_CLUSTERS) {
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
          getPrDiff: () => getPrDiff(githubToken, pack.group.repository, pack.group.gh_pr_number),
          getTestSource: (path, sha) =>
            getTestSource(githubToken, pack.group.repository, path, sha),
        });
      } catch (err) {
        core.warning(`agent failed: ${(err as Error).message}; failing closed`);
      }
    } else if (needsAI && !anthropicKey) {
      core.info(`no anthropic key; leaving cluster ${cluster.signature} on history suggestion`);
    }

    const d = decide({
      failure: cluster.representative,
      runType,
      branch: pack.group.branch || identity.branch || "",
      changedFiles,
      ai,
      mode,
    });
    d.member_count = cluster.member_count;
    const blamed = await attachBlame(d, cluster, githubToken, pack.group.repository);
    decisions.push(blamed);
    core.info(
      `${cluster.signature} ×${cluster.member_count}: kind=${blamed.kind} ${blamed.verdict} ` +
        `waived=${blamed.waived} conf=${blamed.confidence} ` +
        `cites=${blamed.citations.join(",") || "-"} ` +
        `reason=${blamed.reason}` +
        (blamed.chronic ? ` [CHRONIC]` : "") +
        (blamed.borderline ? ` [BORDERLINE — needs eyeball]` : "") +
        (blamed.suspect_author ? ` author=@${blamed.suspect_author}` : ""),
    );
  }

  const summary = rollup(decisions);
  const ledgerOK = await writeLedger(baseURL, audience, pack, decisions, model);
  const flip = mayFlipChecks(mode, ledgerOK);
  if (!flip.allowed) {
    // B2/B3: refuse to green anything the ledger did not record. The original
    // checks stay red; the run fails loudly instead of waiving silently.
    core.setFailed(flip.reason ?? "ledger write failed — refusing to flip");
    return;
  }
  if (flip.reason) core.notice(flip.reason);
  await writeStepSummary(pack.clusters || [], decisions, summary, reportURL);



  // Round-2 major 6: EVERY status write belongs to the gate. In shadow mode a
  // failed ledger was tolerated and this block still posted
  // summary.state=success — a green e2e-test/*-prefixed row with no ledger
  // row. Shadow observes and comments; it writes no check rows at all.
  if (githubToken && mode === "gate") {
    const [owner, repo] = splitRepo(pack.group.repository);
    // When callers name the original platform check, rewrite that row and skip
    // a separate e2e-test/ai-triage-* failure — PR Checks stay one row per suite.
    const postTriageRow = Boolean(contextName) && originalContexts.length === 0;
    if (postTriageRow) {
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
    } else if (contextName && originalContexts.length > 0) {
      // Neutralize any prior red e2e-test/ai-triage-* on this SHA; signal lives on originals.
      await setCommitStatus({
        token: githubToken,
        owner,
        repo,
        sha: pack.group.commit_sha,
        state: "success",
        context: contextName,
        description: summary.waived
          ? `waived on ${originalContexts[0]}`
          : `see ${originalContexts[0]}`,
        targetURL: reportURL,
      });
    }

    const statusRows =
      mode === "gate" && decisions.length > 0
        ? await listLatestCommitStatuses({
            token: githubToken,
            owner,
            repo,
            sha: pack.group.commit_sha,
          })
        : [];

    const targets = contextsToUpdate({
      mode,
      hasFailures: decisions.length > 0,
      explicit: originalContexts,
      discovered: statusRows,
      triageContext: contextName,
    });
    const descByContext = new Map(statusRows.map((s) => [s.context, s.description]));
    // Unique counts from TSIO's deduped rollup beat anything parsed from a
    // status description (which folds flaky into passed and drops skipped).
    const tsioCounts = await fetchReportCounts(baseURL, pack.group.id);
    for (const ctx of targets) {
      const counts = tsioCounts ?? parseRunCounts(descByContext.get(ctx));
      const description = originalStatusDescription({
        counts,
        failureCount: pack.failure_count,
        waived: summary.waived,
        verdict: summary.verdict,
      });
      await setCommitStatus({
        token: githubToken,
        owner,
        repo,
        sha: pack.group.commit_sha,
        state: summary.waived ? "success" : "failure",
        context: ctx,
        description,
        targetURL: reportURL,
      });
    }
    if (targets.length > 0) {
      core.info(
        `updated original check(s) → ${summary.waived ? "success" : "failure"}: ${targets.join(", ")}`,
      );
    }
    core.setOutput("flipped_contexts", summary.waived ? targets.join(",") : "");
  } else {
    core.setOutput("flipped_contexts", "");
  }

  // MVP #1: regressions must reach the PR author — commit statuses and the
  // Actions page are invisible to authors. One idempotent comment, @-tagging
  // the PR author only when this PR is the suspect. All-waived stays silent.
  // Shadow mode comments too (observational) — shadow-mode dogfooding is worthless
  // if developers see nothing for its whole 4-week run.
  if (
    githubToken &&
    identity.gh_pr_number &&
    decisions.some((d) => d.verdict === "PR_REGRESSION" || d.verdict === "MAIN_REGRESSION")
  ) {
    const [owner, repo] = splitRepo(pack.group.repository);
    let prAuthor: string | undefined;
    try {
      const prRes = await fetch(
        `https://api.github.com/repos/${owner}/${repo}/pulls/${identity.gh_pr_number}`,
        {
          headers: { authorization: `Bearer ${prToken}`, accept: "application/vnd.github+json" },
        },
      );
      if (prRes.ok) {
        prAuthor = ((await prRes.json()) as { user?: { login?: string } }).user?.login;
      }
    } catch (err) {
      core.warning(`PR author lookup failed: ${(err as Error).message}`);
    }
    const commentBody = formatTriageComment({
      prAuthor,
      decisions,
      clusters: pack.clusters || [],
      reportURL,
      runConfig: pack.group?.environment_metadata,
      mode,
    });
    if (commentBody) {
      const url = await upsertTriageComment({
        token: prToken,
        owner,
        repo,
        prNumber: Number(identity.gh_pr_number),
        body: commentBody,
      });
      if (url) core.info(`triage verdict comment: ${url}`);
    }
  }

  core.setOutput("state", summary.state);
  core.setOutput("waived", String(summary.waived));
  core.setOutput("verdict", summary.verdict);
  core.setOutput("description", summary.description);

  // Fail the Actions job only when nothing annotated the merge-blocking row
  // (shadow/discover mode). Named originals stay red via commit status instead.
  if (mode === "gate" && summary.state === "failure" && originalContexts.length === 0) {
    core.setFailed(summary.description);
  }
}

function agentCalls(decisions: Decision[]): number {
  return decisions.filter((d) => d.source === "model").length;
}

async function fetchReportCounts(baseURL: string, groupID: string): Promise<RunCounts | undefined> {
  if (!groupID) return undefined;
  try {
    const res = await retryFetch(`${baseURL}/api/v1/reports/${groupID}`, {}, "reports/:id");
    if (!res.ok) {
      core.warning(`report stats HTTP ${res.status}; falling back to status description`);
      return undefined;
    }
    const report = await parseJSON<{
      orchestration?: { tests?: RunCounts };
      test_stats?: RunCounts;
    }>(res, "reports/:id");
    const t = report.orchestration?.tests || report.test_stats;
    if (!t || typeof t.passed !== "number" || typeof t.failed !== "number") return undefined;
    return { passed: t.passed, failed: t.failed, flaky: t.flaky, skipped: t.skipped };
  } catch (err) {
    core.warning(`report stats: ${(err as Error).message}; falling back to status description`);
    return undefined;
  }
}

export async function attachBlame(
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

export async function fetchEvidence(
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
  return await parseJSON<EvidencePack>(res, "triage/evidence");
}

export async function listChangedFiles(
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

export async function compareCommits(
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

const MAX_DIFF_BYTES = 200 * 1024;
const MAX_SOURCE_BYTES = 100 * 1024;

/**
 * Fetch the PR's unified diff, capped at 200KB. When truncated, the changed
 * file paths are still listed up front — for the ABAC shape the paths alone
 * (.github/workflows, testcontainers) carry most of the signal.
 */
export async function getPrDiff(
  token: string,
  repository: string,
  prNumber?: number,
): Promise<string> {
  if (!token || !prNumber) return "";
  const [owner, repo] = splitRepo(repository);
  try {
    const res = await fetch(`https://api.github.com/repos/${owner}/${repo}/pulls/${prNumber}`, {
      headers: {
        authorization: `Bearer ${token}`,
        accept: "application/vnd.github.v3.diff",
      },
    });
    if (!res.ok) {
      core.warning(`getPrDiff: HTTP ${res.status}`);
      return "";
    }
    const diff = await res.text();
    if (Buffer.byteLength(diff) <= MAX_DIFF_BYTES) return diff;
    const paths = [...diff.matchAll(/^diff --git a\/(.+?) b\//gm)].map((m) => m[1]);
    const header =
      `[diff truncated to ${MAX_DIFF_BYTES} bytes]\n` +
      `Changed files (${paths.length}):\n${paths.map((p) => `- ${p}`).join("\n")}\n\n`;
    return header + diff.slice(0, MAX_DIFF_BYTES) + "\n... (truncated)";
  } catch (err) {
    core.warning(`getPrDiff: ${(err as Error).message}`);
    return "";
  }
}

/**
 * Fetch a file's source at a commit SHA (raw), capped at 100KB.
 */
/**
 * Candidate repo-relative paths to try for a spec, most likely first.
 *
 * THE BUG THIS FIXES. TSIO ingests the framework's own JSON, and Playwright's
 * reporter emits `file` relative to its configured `testDir` (`specs` in the
 * monorepo). So the evidence path is `functional/channels/drafts.spec.ts`,
 * while the repo path is
 * `e2e-tests/playwright/specs/functional/channels/drafts.spec.ts`. Fetching
 * `contents/<evidence path>` therefore 404s for EVERY Playwright and Cypress
 * spec, and get_test_source returned "could not fetch source" every time —
 * while the prompt told the model to read a spec it could never see. Half of
 * round 6's "give the model the evidence" fix was silently inert.
 *
 * The re-rooting lives in spec-paths.ts as repoRelSpecCandidates rather than
 * being inlined here, because it encodes a fact about the report format that
 * more than one caller has needed.
 *
 * The raw path is kept as a last resort because this is a read-only fetch:
 * a product source the model explicitly asked for is harmless to read, and
 * occasionally the right thing.
 */
export function testSourceCandidates(path: string): string[] {
  const norm = path.replace(/^\.\//, "").replace(/^\/+/, "");
  if (!norm) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const c of [...repoRelSpecCandidates(norm), norm]) {
    if (c && !seen.has(c)) {
      seen.add(c);
      out.push(c);
    }
  }
  return out;
}

export async function getTestSource(
  token: string,
  repository: string,
  path: string,
  sha: string,
): Promise<string> {
  if (!token || !path || !sha) return "";
  const [owner, repo] = splitRepo(repository);
  const candidates = testSourceCandidates(path);
  const tried: string[] = [];
  for (const candidate of candidates) {
    try {
      const res = await fetch(
        `https://api.github.com/repos/${owner}/${repo}/contents/${candidate}?ref=${sha}`,
        {
          headers: {
            authorization: `Bearer ${token}`,
            accept: "application/vnd.github.v3.raw",
          },
        },
      );
      if (res.status === 404) {
        // Expected while walking candidate roots — not worth a warning each.
        tried.push(`${candidate} (404)`);
        continue;
      }
      if (!res.ok) {
        tried.push(`${candidate} (HTTP ${res.status})`);
        continue;
      }
      const src = await res.text();
      if (candidate !== path) {
        core.info(`getTestSource: re-rooted ${path} -> ${candidate}`);
      }
      if (Buffer.byteLength(src) > MAX_SOURCE_BYTES) {
        return src.slice(0, MAX_SOURCE_BYTES) + "\n... (truncated)";
      }
      return src;
    } catch (err) {
      tried.push(`${candidate} (${(err as Error).message})`);
    }
  }
  core.warning(
    `getTestSource: no candidate resolved for ${path}@${sha} — tried ${tried.join(", ")}`,
  );
  return "";
}

/**
 * B2/B3: the ledger write is a GATE, not a log line. Every waiver must be
 * recorded before anything greens; a check flip with no ledger row is the
 * "silently waived" failure mode this system exists to prevent. Returns
 * false on any failure; the caller must refuse to flip when this is false in
 * gate mode (and may only tolerate the skip in shadow mode).
 */
export async function writeLedger(
  baseURL: string,
  audience: string,
  pack: EvidencePack,
  decisions: Decision[],
  model: string,
  /** Marks the batch as measured offline by the replay job. Replay rows are a
   * real ledger entry that nothing reads to flip a check, and they are counted
   * separately from live verdicts by GET /triage/accuracy. */
  replay = false,
): Promise<boolean> {
  if (decisions.length === 0) return true;
  let bearer: string;
  try {
    bearer = await core.getIDToken(audience);
    core.setSecret(bearer);
  } catch (err) {
    core.warning(`ledger skipped (no OIDC token): ${(err as Error).message}`);
    return false;
  }

  const body = {
    repository: pack.group.repository,
    branch: pack.group.branch,
    commit_sha: pack.group.commit_sha,
    gh_run_id: pack.group.gh_run_id,
    gh_pr_number: pack.group.gh_pr_number,
    model,
    replay,
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

  let res: Awaited<ReturnType<typeof retryFetch>>;
  try {
    res = await retryFetch(
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
  } catch (err) {
    // Round-2 major 6: retryFetch THROWS past its retries (network error,
    // exhausted 5xx). That must be a ledger failure (gate refuses, shadow
    // tolerates) — not an unhandled crash that masks the refusal.
    core.warning(`ledger write failed: ${(err as Error).message}`);
    return false;
  }
  if (!res.ok) {
    core.warning(`ledger write HTTP ${res.status} ${await res.text()}`);
    return false;
  }
  return true;
}

async function writeStepSummary(
  clusters: EvidenceCluster[],
  decisions: Decision[],
  summary: ReturnType<typeof rollup>,
  reportURL: string,
): Promise<void> {
  // Explicit callout: a product bug is never the test's fault to fix.
  const productBugs = decisions.filter(
    (d) => d.verdict === "PR_REGRESSION" || d.verdict === "MAIN_REGRESSION",
  );
  const testBugs = decisions.filter((d) => d.kind === "bug" && !productBugs.includes(d));
  const label =
    productBugs.length > 0
      ? `🔴 **PRODUCT BUG** — code broke the product; AI will not touch this. Needs a human.`
      : testBugs.length > 0
        ? `🟡 **TEST BUG** — test-side issue; needs a test-infra fix.`
        : `✅ **NO REGRESSION** — all failures waived as flake/infra.`;
  const lines = [
    `## E2E flake triage`,
    ``,
    label,
    ``,
    `**Outcome:** \`${summary.description}\` — [report](${reportURL})`,
    ``,
    `No rerun. Cost scales with distinct error signatures, not failure count.`,
    ``,
    `| Classification | Cluster | n | Verdict | Author | Waived | Why |`,
    `|---|---|---:|---|---|---|---|`,
  ];
  for (let i = 0; i < decisions.length; i++) {
    const d = decisions[i]!;
    const c = clusters[i]!;
    const author = d.suspect_author
      ? `@${d.suspect_author} (\`${(d.suspect_sha || "").slice(0, 7)}\`)`
      : "—";
    const flag = d.chronic ? " ⚠️ chronic" : d.borderline ? " ⚖️ borderline" : "";
    const classification = productBugs.includes(d)
      ? "🔴 product bug"
      : d.kind === "bug"
        ? "🟡 test bug"
        : "flake/infra";
    lines.push(
      `| ${classification} | \`${c.signature.slice(0, 8)}\` ${c.label.replace(/\|/g, " ").slice(0, 60)} | ${d.member_count} | ${d.verdict}${flag} | ${author} | ${d.waived ? "yes" : "no"} | ${d.reason.replace(/\|/g, " ").slice(0, 140)} |`,
    );
  }
  if (decisions.length === 0) {
    lines.push(`| — | — | — | — | — | — | no failures |`);
  }
  if (productBugs.length > 0 || testBugs.some((d) => !d.waived)) {
    lines.push(``, `### Needs a human`, ``);
    for (const d of productBugs) {
      lines.push(
        `- 🔴 **${d.verdict}** — ${d.reason.replace(/\n/g, " ").slice(0, 300)}` +
          (d.suspect_author ? ` (suspect: @${d.suspect_author})` : "") +
          ` — [report](${reportURL})`,
      );
    }
    if (testBugs.some((d) => !d.waived)) {
      lines.push(
        `- 🟡 ${testBugs.filter((d) => !d.waived).length} unwaived test bug(s) — a maintainer can override via \`/e2e-triage-override\`.`,
      );
    }
  }
  const file = process.env.GITHUB_STEP_SUMMARY;
  if (file) fs.appendFileSync(file, lines.join("\n") + "\n");
}

function splitRepo(repository: string): [string, string] {
  const [owner, repo] = repository.split("/");
  if (!owner || !repo) throw new Error(`repository ${repository} is not owner/repo`);
  return [owner, repo];
}

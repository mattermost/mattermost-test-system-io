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
import {
  collectBisectTargets,
  collectFixTargets,
  runFixer,
  MAX_FIX_TARGETS,
  type FixerContext,
  type FixResult,
} from "./fixer.ts";
import { repoRelSpecCandidates } from "./spec-paths.ts";
import { buildReportURL } from "./report_url.ts";
import { retryFetch, parseJSON } from "./retry-fetch.ts";
import type {
  CompositeIdentity,
  Decision,
  EvidenceCluster,
  EvidencePack,
  FixTarget,
} from "./types.ts";

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

  // Fix mode: a checked-out workspace + triage's fixable-cluster JSON from the
  // triage pass. The agent edits the spec in the workspace, pushes to the PR
  // branch, and the standard CI loop validates the fix.
  const fixClusters = core.getInput("fix-clusters");
  if (fixClusters) {
    await runFixMode(baseURL, audience, fixClusters, {
      apiKey: anthropicKey,
      model,
      workspace: core.getInput("workspace") || process.env.GITHUB_WORKSPACE || ".",
      token: githubToken,
      repository: identity.repository,
      prBranch: core.getInput("pr-branch", { required: true }),
      prNumber: identity.gh_pr_number ? Number(identity.gh_pr_number) : undefined,
      baseURL,
      maxTargets: Number(core.getInput("autofix-max")) || MAX_FIX_TARGETS,
    });
    return;
  }

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

  // Export test-bug clusters the fixer may repair (TEST_DEBT / refusal-blocked
  // flakes on pre-existing specs). The ai-autofix job consumes this JSON.
  core.setOutput(
    "fixable_clusters",
    JSON.stringify(collectFixTargets(pack.clusters || [], decisions, changedFiles)),
  );

  // MVP #2: confidently-attributed master regressions go to the bisect
  // pipeline (finds the culprit commit on master, root-causes, tags author).
  core.setOutput(
    "bisect_clusters",
    JSON.stringify(collectBisectTargets(pack.clusters || [], decisions)),
  );

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

/**
 * Fix mode: repair each target in the PR checkout, push, and comment on the
 * PR with the diffs so a human can review what the agent changed.
 */
async function runFixMode(
  baseURL: string,
  audience: string,
  fixClusters: string,
  ctx: FixerContext,
): Promise<void> {
  let targets;
  try {
    targets = JSON.parse(fixClusters) as FixTarget[];
  } catch (err) {
    core.setFailed(`fix-clusters is not valid JSON: ${(err as Error).message}`);
    return;
  }
  if (!ctx.apiKey) {
    core.warning("no anthropic key; autofix skipped");
    return;
  }
  targets = targets.slice(0, ctx.maxTargets);
  core.info(
    `autofix: ${targets.length} cluster(s) — ${targets.map((t) => t.signature.slice(0, 8)).join(", ")}`,
  );
  // The unified-ci dispatch names synthetic dashboard branches (pr-N); the
  // fixer must push to the PR's REAL head branch, so resolve it then.
  if (/^pr-\d+$/.test(ctx.prBranch) && ctx.prNumber && ctx.token) {
    try {
      const res = await fetch(
        `https://api.github.com/repos/${ctx.repository}/pulls/${ctx.prNumber}`,
        {
          headers: { authorization: `Bearer ${ctx.token}`, accept: "application/vnd.github+json" },
        },
      );
      if (res.ok) {
        const head = ((await res.json()) as { head?: { ref?: string } }).head?.ref;
        if (head) {
          core.info(`fixer: synthetic branch ${ctx.prBranch} → real PR head ${head}`);
          ctx.prBranch = head;
        }
      }
    } catch (err) {
      core.warning(`fixer: PR head lookup failed (${(err as Error).message})`);
    }
  }
  // Skip anything the agent has already failed on MaxFixAttempts times. The
  // branch-level loop guard inside runFixer stops an AI<->CI ping-pong within
  // one PR; this is the across-cycles version, and without it the loop spends
  // its whole budget re-attempting the same unfixable test while the rest of
  // the queue waits behind it.
  const exhausted = await filterExhaustedTargets(baseURL, ctx.repository, targets);
  if (exhausted.skipped.length > 0) {
    core.notice(
      `handing ${exhausted.skipped.length} test(s) to a human — the agent has already ` +
        `failed ${MAX_ATTEMPTS_BEFORE_HUMAN}x on each: ${exhausted.skipped.join(", ")}`,
    );
    core.setOutput("needs_human_tests", exhausted.skipped.join(","));
  }
  targets = exhausted.targets;
  if (targets.length === 0) {
    core.info("nothing left to attempt — every target is with a human");
    core.setOutput("fixed_count", "0");
    return;
  }

  const results = await runFixer(targets, ctx);

  for (const r of results) {
    core.info(`autofix ${r.signature.slice(0, 8)}: ${r.status} — ${r.summary.slice(0, 200)}`);
  }
  const fixed = results.filter((r) => r.status === "fixed");
  const blocked = results.filter((r) => r.status === "skipped" && r.skip_code);
  core.setOutput("fixed_count", String(fixed.length));
  core.setOutput("fixed_signatures", fixed.map((r) => r.signature).join(","));
  core.setOutput("needs_human", blocked.map((r) => r.signature).join(","));

  // Record every attempt, including the ones that did not work — especially
  // those. An unsuccessful attempt with no record is the loop's worst state:
  // it will pick the same test again next cycle, and the human who eventually
  // takes it starts from nothing.
  await recordFixAttempts(baseURL, audience, ctx, targets, results);

  writeFixSummary(results);

  if (ctx.prNumber && ctx.token && results.length > 0) {
    await commentFixes(ctx, ctx.prNumber, results);
  }
}

/** Matches MaxFixAttempts on the server; the server is the authority and the
 * loop re-reads the tally each run, so this is only for the log line. */
const MAX_ATTEMPTS_BEFORE_HUMAN = 3;

/**
 * Drop targets the agent has already failed on too many times. The server owns
 * the threshold and computes `needs_human` per test; this only asks.
 *
 * A read failure keeps every target: refusing to attempt anything because the
 * tally was unreadable would turn a monitoring blip into a stalled loop, and
 * the branch-level guard still bounds the damage.
 */
async function filterExhaustedTargets(
  baseURL: string,
  repository: string,
  targets: FixTarget[],
): Promise<{ targets: FixTarget[]; skipped: string[] }> {
  let needsHuman: Set<string>;
  try {
    const res = await retryFetch(
      `${baseURL}/api/v1/triage/stabilization/queue?repo=${encodeURIComponent(repository)}`,
      {},
      "triage/stabilization/queue",
    );
    if (!res.ok) throw new Error(`status ${res.status}`);
    const body = await parseJSON<{
      ranked?: Array<{ test_id: string; fix_attempts?: { needs_human?: boolean } }>;
    }>(res, "triage/stabilization/queue");
    needsHuman = new Set(
      (body.ranked ?? [])
        .filter((e) => e.fix_attempts?.needs_human === true)
        .map((e) => e.test_id),
    );
  } catch (err) {
    core.warning(`could not read fix-attempt history (${(err as Error).message}) — attempting all`);
    return { targets, skipped: [] };
  }

  const kept: FixTarget[] = [];
  const skipped: string[] = [];
  for (const t of targets) {
    const id = t.external_test_id;
    if (id && needsHuman.has(id)) skipped.push(id);
    else kept.push(t);
  }
  return { targets: kept, skipped };
}

/**
 * Record what each attempt did. Best-effort per target: a failed record must
 * not fail the job, because the fix itself may already be pushed — but it is
 * warned about, since an unrecorded failure is one the loop will repeat.
 */
async function recordFixAttempts(
  baseURL: string,
  audience: string,
  ctx: FixerContext,
  targets: FixTarget[],
  results: FixResult[],
): Promise<void> {
  const byId = new Map(targets.map((t) => [t.signature, t.external_test_id]));
  let bearer: string;
  try {
    bearer = await core.getIDToken(audience);
    core.setSecret(bearer);
  } catch (err) {
    core.warning(`fix attempts not recorded (no OIDC token): ${(err as Error).message}`);
    return;
  }

  for (const r of results) {
    const testID = byId.get(r.signature);
    if (!testID) continue; // suite-level cluster: nothing stable to key on
    const outcome =
      r.status === "fixed" ? "fixed" : r.status === "skipped" && r.skip_code ? "blocked" : "failed";
    try {
      const res = await retryFetch(
        `${baseURL}/api/v1/triage/stabilization/attempts`,
        {
          method: "POST",
          headers: { authorization: `Bearer ${bearer}`, "content-type": "application/json" },
          body: JSON.stringify({
            test_id: testID,
            repository: ctx.repository,
            outcome,
            // The server requires this for anything but a fix: it is what the
            // next person to look at the test reads first.
            detail: r.summary || `${r.status} with no summary`,
            cluster_signature: r.signature,
          }),
        },
        "triage/stabilization/attempts",
      );
      if (!res.ok) core.warning(`attempt record HTTP ${res.status} for ${testID}`);
    } catch (err) {
      core.warning(`attempt record failed for ${testID}: ${(err as Error).message}`);
    }
  }
}

async function commentFixes(
  ctx: FixerContext,
  prNumber: number,
  results: FixResult[],
): Promise<void> {
  const [owner, repo] = splitRepo(ctx.repository);
  const octokit = new RetryingOctokit(getOctokitOptions(ctx.token));
  const fixed = results.filter((r) => r.status === "fixed");
  const blocked = results.filter((r) => r.status === "skipped" && r.skip_code);
  const other = results.filter(
    (r) => !(r.status === "fixed" || (r.status === "skipped" && r.skip_code)),
  );
  const lines = [
    `## 🤖 AI test autofix — ${fixed.length}/${results.length} fixed`,
    ``,
    `Fixes were pushed to this PR branch (never a new PR) and will be validated by the next E2E run + re-triage.`,
    ``,
  ];
  if (fixed.length > 0) {
    lines.push(`### What the AI fixed`, ``);
    for (const r of fixed) {
      lines.push(
        `#### ✅ \`${r.signature.slice(0, 8)}\` — commit \`${(r.commit_sha || "").slice(0, 7)}\``,
      );
      lines.push(r.summary.slice(0, 1200));
      if (r.files.length > 0) lines.push(`Files: ${r.files.map((f) => `\`${f}\``).join(", ")}`);
      if (r.diff)
        lines.push(
          `<details><summary>diff</summary>\n\n\`\`\`diff\n${r.diff.slice(0, 20000)}\n\`\`\`\n</details>`,
        );
      lines.push(``);
    }
  }
  if (blocked.length > 0) {
    lines.push(`### 🔒 Needs human review (autofix loop guard)`, ``);
    for (const r of blocked) {
      lines.push(`- \`${r.signature.slice(0, 8)}\` — ${r.summary.slice(0, 400)}`);
    }
    lines.push(``);
  }
  if (other.length > 0) {
    lines.push(`### ⚠️ Not fixed — needs human`, ``);
    for (const r of other) {
      lines.push(
        `- ${r.status === "failed" ? "❌" : "⏭️"} \`${r.signature.slice(0, 8)}\` — ${r.summary.slice(0, 400)}`,
      );
    }
    lines.push(``);
  }
  try {
    const c = await octokit.rest.issues.createComment({
      owner,
      repo,
      issue_number: prNumber,
      body: lines.join("\n"),
    });
    core.setOutput("comment_url", c.data.html_url);
  } catch (err) {
    core.warning(`PR comment failed: ${(err as Error).message}`);
  }
}

/**
 * Unique per-test counts for the report group. Prefers the orchestration
 * rollup (one row per dispatched unit — retries collapse into flaky) and
 * falls back to test_stats, then to undefined (caller parses descriptions).
 */
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
 * The re-rooting lives in spec-paths.ts as repoRelSpecCandidates — both this
 * read-only path and the fixer's write-path check need it, so it belongs to
 * neither of them.
 *
 * The raw path is kept as a last resort because this is a read-only fetch:
 * unlike the fixer, which must refuse to WRITE outside e2e-tests/**, reading a
 * product source the model explicitly asked for is harmless and occasionally
 * the right thing.
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
  // Explicit callout: product bugs can never be autofixed — they need a human.
  const productBugs = decisions.filter(
    (d) => d.verdict === "PR_REGRESSION" || d.verdict === "MAIN_REGRESSION",
  );
  const testBugs = decisions.filter((d) => d.kind === "bug" && !productBugs.includes(d));
  const label =
    productBugs.length > 0
      ? `🔴 **PRODUCT BUG** — code broke the product; AI will not touch this. Needs a human.`
      : testBugs.length > 0
        ? `🟡 **TEST BUG** — test-side issue; eligible clusters go to AI autofix.`
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
        `- 🟡 ${testBugs.filter((d) => !d.waived).length} unwaived test bug(s) — the AI autofix job will attempt repair on pre-existing specs, or a maintainer can override via \`/e2e-triage-override\`.`,
      );
    }
  }
  const file = process.env.GITHUB_STEP_SUMMARY;
  if (file) fs.appendFileSync(file, lines.join("\n") + "\n");
}

/**
 * Fix-mode summary: what the AI changed, what the loop guard refused, and
 * what still needs a human — on the Actions run summary page.
 */
function writeFixSummary(results: FixResult[]): void {
  const fixed = results.filter((r) => r.status === "fixed");
  const blocked = results.filter((r) => r.status === "skipped" && r.skip_code);
  const other = results.filter(
    (r) => !(r.status === "fixed" || (r.status === "skipped" && r.skip_code)),
  );
  const lines = [
    `## 🤖 AI test autofix`,
    ``,
    fixed.length > 0
      ? `✅ **${fixed.length} test fix(es) pushed to the PR branch** — the next E2E run is the validation.`
      : `No fixes pushed this run.`,
    ``,
  ];
  for (const r of fixed) {
    lines.push(
      `- ✅ \`${r.signature.slice(0, 8)}\` — commit \`${(r.commit_sha || "").slice(0, 7)}\` — ${r.files.join(", ")}\n  ${r.summary.replace(/\n/g, " ").slice(0, 300)}`,
    );
  }
  if (blocked.length > 0) {
    lines.push(``, `### Needs a human (loop guard)`, ``);
    for (const r of blocked) {
      lines.push(`- 🔒 \`${r.signature.slice(0, 8)}\` — ${r.summary.slice(0, 300)}`);
    }
  }
  if (other.length > 0) {
    lines.push(``, `### Not fixed`, ``);
    for (const r of other) {
      lines.push(
        `- ${r.status === "failed" ? "❌" : "⏭️"} \`${r.signature.slice(0, 8)}\` — ${r.summary.slice(0, 300)}`,
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

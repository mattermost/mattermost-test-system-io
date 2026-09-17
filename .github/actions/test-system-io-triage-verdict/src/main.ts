import * as core from "@actions/core";
import * as github from "@actions/github";
import {
  annotation,
  conclusion,
  manualStatus,
  overrideDescription,
  shouldPostStatus,
  statusState,
  upsertComment,
  type Verdict,
} from "./decision";
import { AnthropicClient } from "./anthropic";
import {
  adjudicate,
  applyAdjudication,
  askModel,
  buildPack,
  worthAdjudicating,
  type Adjudication,
  type TsioPack,
} from "./adjudicate";

interface Identity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  name: string;
  gh_pr_number?: number | string;
}
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export async function run(): Promise<void> {
  const identity = JSON.parse(core.getInput("composite-identity", { required: true })) as Identity;
  const [owner, repo] = identity.repository.split("/");
  const attempt = Number(identity.gh_run_attempt);
  if (
    !owner ||
    !repo ||
    !identity.commit_sha ||
    !identity.name ||
    !/^\d+$/.test(identity.gh_run_id) ||
    !Number.isSafeInteger(attempt) ||
    attempt < 1
  )
    throw new Error("Invalid composite identity");
  const context = core.getInput("context", { required: true });
  const baseURL =
    core.getInput("use-staging") === "true"
      ? "https://staging-test-io.test.mattermost.com"
      : "https://test-io.test.mattermost.com";
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const wait = Number(core.getInput("wait-for-completion-ms") || 600000);
  if (!Number.isSafeInteger(wait) || wait < 0 || wait > 3600000)
    throw new Error("wait-for-completion-ms must be between 0 and 3600000");
  const deadline = Date.now() + wait;
  const post = async (path: string, body: unknown, timeout = 70000) => {
    const oidc = await core.getIDToken(audience);
    core.setSecret(oidc);
    return fetch(`${baseURL}/api/v1/triage${path}`, {
      method: "POST",
      redirect: "error",
      signal: AbortSignal.timeout(timeout),
      headers: { Authorization: `Bearer ${oidc}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  };
  // Transient gateway errors (502/503/504) and network failures are retried
  // with backoff, like the summary action's 504 handling, so a blip never
  // turns an enforced required status red. 4xx and other 5xx are not retried.
  const postWithRetry = async (path: string, body: unknown, timeout: number) => {
    let last: unknown;
    for (let attempt = 0; attempt < 3; attempt++) {
      if (attempt > 0) await sleep(Math.min(2000 * 2 ** (attempt - 1), deadline - Date.now()));
      try {
        const response = await post(path, body, timeout);
        if (![502, 503, 504].includes(response.status) || attempt === 2) return response;
        last = new Error(`TSIO verdict failed (${response.status})`);
      } catch (error) {
        last = error;
      }
      if (Date.now() >= deadline) break;
    }
    throw last;
  };
  // Second judge: an LLM adjudicates borderline findings from TSIO's evidence
  // pack plus the PR diff. It never sees the GitHub token, TSIO never sees the
  // Anthropic key, and any failure leaves the engine verdict untouched.
  const secondJudge = async (v: Verdict) => {
    const apiKey = core.getInput("anthropic-api-key");
    // Only PR runs are adjudicated: the question is whether the PR caused the failure.
    if (!apiKey || !Number(identity.gh_pr_number || 0)) return;
    if (core.getInput("adjudicate") === "false" || !worthAdjudicating(v)) return;
    core.setSecret(apiKey);
    const model = core.getInput("adjudicator-model") || "claude-haiku-4-5";
    const minConfidence = Number(core.getInput("adjudicate-min-confidence") || 0.85);
    if (!(minConfidence >= 0.5 && minConfidence <= 1))
      throw new Error("adjudicate-min-confidence must be between 0.5 and 1");
    try {
      const oidc = await core.getIDToken(audience);
      core.setSecret(oidc);
      const response = await fetch(
        `${baseURL}/api/v1/triage/verdicts/${encodeURIComponent(v.id)}/evidence`,
        {
          redirect: "error",
          signal: AbortSignal.timeout(30000),
          headers: { Authorization: `Bearer ${oidc}` },
        },
      );
      if (!response.ok) throw new Error(`TSIO evidence failed (${response.status})`);
      const { packs } = (await response.json()) as { packs: TsioPack[] };
      if (!Array.isArray(packs) || packs.length === 0) return;
      let files: { filename: string; patch?: string }[] = [];
      let title = "";
      const base = core.getInput("base-sha") || core.getInput("base-ref");
      if (base) {
        const api = github.getOctokit(core.getInput("github-token", { required: true }));
        const compare = await api.rest.repos.compareCommitsWithBasehead({
          owner,
          repo,
          basehead: `${base}...${identity.commit_sha}`,
          per_page: 100,
        });
        files = (compare.data.files ?? []).map((f) => ({ filename: f.filename, patch: f.patch }));
        const pull = Number(identity.gh_pr_number || 0);
        if (pull) title = (await api.rest.pulls.get({ owner, repo, pull_number: pull })).data.title;
      }
      const client = new AnthropicClient({ apiKey, maxRetries: 2, timeoutMs: 60000 });
      const result = await adjudicate({
        verdict: v,
        packs: packs.map((p) => buildPack(p, files, title)),
        model,
        minConfidence,
        ask: (pack) => askModel(client, model, pack),
        warn: (message) => core.warning(message),
      });
      if (result.findings.every((f) => !f.cause)) {
        core.warning("Second judge unavailable for every finding; engine verdict stands");
        return;
      }
      applyAdjudication(v, result);
      await recordAdjudication(v.id, result);
    } catch (error) {
      core.warning(`Second judge skipped: ${String(error)}`);
    }
  };
  const recordAdjudication = async (id: string, a: Adjudication) => {
    try {
      const response = await post(`/verdicts/${encodeURIComponent(id)}/adjudication`, a, 30000);
      if (!response.ok) throw new Error(`status ${response.status}`);
    } catch (error) {
      core.warning(`Recording adjudication failed: ${String(error)}`);
    }
  };
  let verdict: Verdict;
  for (;;) {
    const remaining = Math.max(0, deadline - Date.now());
    const response = await postWithRetry(
      "/verdicts",
      {
        composite_identity: {
          repository: identity.repository,
          commit_sha: identity.commit_sha,
          gh_run_id: identity.gh_run_id,
          gh_run_attempt: String(attempt),
          name: identity.name,
        },
        context,
        base_ref: core.getInput("base-ref"),
        base_sha: core.getInput("base-sha"),
        lane: core.getInput("lane"),
        changed_files: core
          .getInput("changed-files")
          .split(/\r?\n/)
          .map((s) => s.trim())
          .filter(Boolean),
        wait_for_completion_ms: Math.min(60000, remaining),
      },
      Math.min(60000, remaining) + 10000,
    );
    if (response.status === 202) {
      const pending = (await response.json()) as { retry_after_ms?: number };
      if (Date.now() >= deadline) throw new Error("Timed out waiting for a terminal TSIO verdict");
      await sleep(
        Math.min(Math.max(100, pending.retry_after_ms || 1000), 5000, deadline - Date.now()),
      );
      continue;
    }
    if (response.status !== 200 && response.status !== 409)
      throw new Error(`TSIO verdict failed (${response.status})`);
    verdict = (await response.json()) as Verdict;
    if (
      !["off", "shadow", "enforce"].includes(verdict.mode) ||
      !verdict.id ||
      !verdict.markdown ||
      !verdict.counts ||
      !Array.isArray(verdict.findings)
    )
      throw new Error("Invalid TSIO verdict response");
    break;
  }
  const url = `${baseURL}/triage/verdicts/${encodeURIComponent(verdict.id)}`;
  const engineVerdict = verdict.verdict;
  if (verdict.mode !== "off") await secondJudge(verdict);
  let state = statusState(verdict.verdict);
  let description = verdict.markdown.status_description.slice(0, 140);
  const outputs = () => {
    for (const [key, value] of Object.entries({
      verdict: verdict.verdict,
      "engine-verdict": engineVerdict,
      adjudicated: verdict.adjudication ? "true" : "false",
      mode: verdict.mode,
      confidence: verdict.confidence,
      "verdict-url": url,
      "status-state": state,
      "status-description": description,
      "blocking-count": verdict.counts.blocking,
      "exonerated-count": verdict.counts.exonerated,
    }))
      core.setOutput(key, value);
  };
  outputs();
  if (verdict.mode === "off") return;
  const postStatus = shouldPostStatus(verdict.mode, core.getInput("post-commit-status") || "auto");
  let stale = false;
  const optional = async (work: () => Promise<void>) => {
    try {
      await work();
    } catch (error) {
      core.warning(String(error));
    }
  };
  try {
    const token = core.getInput("github-token", { required: true });
    core.setSecret(token);
    const api = github.getOctokit(token);
    // Run identity belongs to the producer workflow; its repository can differ from the tested repository.
    const workflowRepo = github.context.repo;
    const current = async () => {
      const response = await api.rest.actions.getWorkflowRun({
        ...workflowRepo,
        run_id: Number(identity.gh_run_id),
      });
      if ((response.data.run_attempt || 1) > attempt) {
        stale = true;
        core.warning("Ignoring stale workflow attempt");
        return false;
      }
      return true;
    };
    if (!(await current())) return;
    const pull = Number(identity.gh_pr_number || 0);
    const honorOverride = async () => {
      let label = "";
      if (pull) {
        const labels = await api.paginate(api.rest.issues.listLabelsOnIssue, {
          owner,
          repo,
          issue_number: pull,
          per_page: 100,
        });
        // Only a *blanket* bypass label may be honored live. E2E/Override is
        // re-applied per push by the mobile label manager, so its presence is a
        // decision about this head SHA. E2E/Verified and "E2E Tests/verified"
        // are per-SHA verifications that persist on the PR after later pushes;
        // honoring them here would silently waive every subsequent commit. They
        // are recognized only through the per-SHA "(verified)" status marker.
        const supported = ["E2E/Override", core.getInput("override-label")].filter(Boolean);
        label = labels.find((l) => supported.includes(l.name))?.name || "";
      }
      const statuses = await api.paginate(api.rest.repos.listCommitStatusesForRef, {
        owner,
        repo,
        ref: identity.commit_sha,
        per_page: 100,
      });
      // Label-derived waivers require a live label. Only the dispatch workflow's
      // native (verified) marker survives later raw results, until an explicit reset.
      const history = statuses.filter((s) => s.context === context);
      const reset = history.findIndex(
        (s) => s.state === "pending" && /removed|reset|revoked/i.test(s.description || ""),
      );
      const latest = (reset < 0 ? history : history.slice(0, reset)).find(manualStatus);
      if (!label && latest && manualStatus(latest)) label = "manual-status-verified";
      state = statusState(verdict.verdict);
      description = verdict.markdown.status_description.slice(0, 140);
      core.setOutput("human-override", "false");
      if (!label) {
        outputs();
        return;
      }
      state = "success";
      description = overrideDescription(label, github.context.actor);
      core.setOutput("human-override", "true");
      outputs();
      await optional(async () => {
        const response = await post(`/verdicts/${encodeURIComponent(verdict.id)}/override`, {
          actor: github.context.actor,
          label,
          resulting_state: "success",
        });
        if (!response.ok) throw new Error(`Recording human override failed (${response.status})`);
      });
    };
    await honorOverride();
    await optional(async () => {
      if (core.getInput("post-check-run") !== "false") {
        const name = `e2e-triage/${context}`;
        const externalId = `tsio:${identity.gh_run_id}:${attempt}:${context}`;
        const checks = await api.paginate(api.rest.checks.listForRef, {
          owner,
          repo,
          ref: identity.commit_sha,
          check_name: name,
          per_page: 100,
        });
        const existing = checks.find((c) => c.external_id === externalId);
        const annotations = verdict.findings
          .filter((f) => f.file && !f.file.startsWith("/") && !f.file.split("/").includes(".."))
          .map((finding) => annotation(finding, baseURL));
        const output = {
          title: `${verdict.mode}: ${verdict.verdict}`,
          summary: verdict.markdown.check_summary.slice(0, 65000),
          annotations: annotations.slice(0, 50),
        };
        const payload = {
          owner,
          repo,
          name,
          status: "completed" as const,
          conclusion:
            state === "success" && statusState(verdict.verdict) !== "success"
              ? ("success" as const)
              : conclusion(verdict.verdict),
          details_url: url,
          output,
        };
        const check = existing
          ? await api.rest.checks.update({ ...payload, check_run_id: existing.id })
          : await api.rest.checks.create({
              ...payload,
              head_sha: identity.commit_sha,
              external_id: externalId,
            });
        for (let offset = 50; offset < annotations.length; offset += 50)
          await api.rest.checks.update({
            owner,
            repo,
            check_run_id: check.data.id,
            output: { ...output, annotations: annotations.slice(offset, offset + 50) },
          });
      }
    });
    await optional(async () => {
      if (pull && core.getInput("post-pr-comment") !== "false" && (await current())) {
        await upsertComment(
          {
            list: () =>
              api.paginate(api.rest.issues.listComments, {
                owner,
                repo,
                issue_number: pull,
                per_page: 100,
              }),
            create: (body) =>
              api.rest.issues.createComment({ owner, repo, issue_number: pull, body }),
            update: (comment_id, body) =>
              api.rest.issues.updateComment({ owner, repo, comment_id, body }),
          },
          context,
          verdict.markdown.pr_comment,
          identity.gh_run_id,
          attempt,
        );
      }
    });
    if (postStatus && (await current())) {
      // Re-read escape hatches immediately before the required status mutation.
      await honorOverride();
      await api.rest.repos.createCommitStatus({
        owner,
        repo,
        sha: identity.commit_sha,
        context,
        state,
        description,
        target_url: url,
      });
    }
  } catch (error) {
    core.warning(String(error));
  } finally {
    if (!stale && postStatus && state !== "success")
      core.setFailed(`TSIO triage: ${verdict.verdict}`);
  }
}

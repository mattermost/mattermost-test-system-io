import * as core from "@actions/core";
import type { ClaudeVerdict, EvidenceCluster, EvidenceGroup } from "./types.ts";
import { parseVerdict } from "./claude.ts";
import { attribute, type CompareCommit } from "./blame.ts";
import { retryFetch } from "./retry-fetch.ts";

const MAX_ROUNDS = 6;
const MAX_BYTES = 2 * 1024 * 1024;

export interface AgentContext {
  baseURL: string;
  apiKey: string;
  model: string;
  group: EvidenceGroup;
  baselineBranch: string;
  changedFiles: string[];
  compareCommits: (base: string, head: string) => Promise<CompareCommit[]>;
  /** Fetch the PR's changed files + patch (capped). Empty string when unavailable. */
  getPrDiff: () => Promise<string>;
  /** Fetch a file's source at a commit SHA (capped). Empty string when unavailable. */
  getTestSource: (path: string, sha: string) => Promise<string>;
}

export const TOOLS = [
  {
    name: "get_history",
    description:
      "GET /api/v1/tests/history — outcome series on the baseline branch. MANDATORY before any FLAKY_* verdict: compare past errors and outcomes for this exact test. Use this to see if the test was already failing, flipping, or clean, and how often it flaked.",
    input_schema: {
      type: "object",
      properties: { test_id: { type: "string" } },
      required: ["test_id"],
    },
  },
  {
    name: "get_failing_elsewhere",
    description:
      "GET /api/v1/tests/failing-elsewhere — is the same test failing on other open PRs right now? MANDATORY before any FLAKY_* verdict: the same failure on a different diff is strong evidence against PR_REGRESSION.",
    input_schema: {
      type: "object",
      properties: { test_id: { type: "string" } },
      required: ["test_id"],
    },
  },
  {
    name: "get_screenshot",
    description:
      "Fetch a TSIO failure screenshot (/files/{s3_key}) when keys are listed. Prefer viewing one when available; not required if error/stack already explain the failure.",
    input_schema: {
      type: "object",
      properties: { s3_key: { type: "string" } },
      required: ["s3_key"],
    },
  },
  {
    name: "blame_commits",
    description:
      "List commits between last_pass_commit and failing_since_commit. Only after you have classified this as a bug.",
    input_schema: {
      type: "object",
      properties: {
        last_pass_commit: { type: "string" },
        failing_since_commit: { type: "string" },
      },
      required: ["last_pass_commit", "failing_since_commit"],
    },
  },
  {
    name: "get_pr_diff",
    description:
      "Fetch this PR's changed files and patch. MANDATORY before any FLAKY_* verdict on a PR run: you cannot judge whether a failure is the PR's fault without reading what the PR changed. Paths alone (e.g. .github/workflows, testcontainers) carry most of the signal even when a patch is truncated.",
    input_schema: { type: "object", properties: {}, required: [] },
  },
  {
    name: "get_test_source",
    description:
      "Fetch the failing spec's source at the run's commit. Use this to see what the test actually does — a selector timeout is a race or a real break depending on what the test asserts and how it waits.",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string" },
        sha: { type: "string" },
      },
      required: ["path"],
    },
  },
];

export async function investigate(
  cluster: EvidenceCluster,
  ctx: AgentContext,
): Promise<ClaudeVerdict> {
  const prompt = buildPrompt(cluster, ctx);
  const messages: Array<{ role: string; content: unknown }> = [{ role: "user", content: prompt }];

  for (let round = 0; round < MAX_ROUNDS; round++) {
    const body = await callClaude(ctx, messages);
    const blocks = body.content || [];
    const toolUses = blocks.filter((b) => b.type === "tool_use");
    const text = blocks.find((b) => b.type === "text")?.text || "";

    if (toolUses.length === 0) {
      try {
        return parseVerdict(text);
      } catch (err) {
        if (round === MAX_ROUNDS - 1) throw err;
        core.warning(
          `agent JSON unusable (${(err as Error).message}); asking once more for JSON only`,
        );
        messages.push({ role: "assistant", content: blocks });
        messages.push({
          role: "user",
          content:
            "Your last reply was not valid JSON. Reply with ONLY one JSON object matching the schema. No markdown, no prose.",
        });
        continue;
      }
    }

    messages.push({ role: "assistant", content: blocks });
    const results: unknown[] = [];
    for (const tu of toolUses) {
      results.push(
        await runTool(
          String(tu.name),
          (tu.input || {}) as Record<string, string>,
          cluster,
          ctx,
          String(tu.id),
        ),
      );
    }
    messages.push({ role: "user", content: results });
  }
  core.warning(`agent hit ${MAX_ROUNDS} tool rounds; failing closed`);
  return { verdict: "INCONCLUSIVE", confidence: 0, reason: "agent did not finish", citations: [] };
}

function buildPrompt(cluster: EvidenceCluster, ctx: AgentContext): string {
  const f = cluster.representative;
  const shots = (f.screenshots || []).map((s) => s.s3_key).join(", ") || "(none)";
  const hist = f.history
    ? `runs=${f.history.runs} failed=${f.history.failed} flaky=${f.history.flaky} flips=${f.history.flips} last_pass=${f.history.last_pass_commit ?? "none"} failing_since=${f.history.failing_since_commit ?? "none"} series=${f.history.series.join(",")}`
    : f.history_error || "not loaded — call get_history";
  // W9 — the run configuration this failure executed under, and any keys that
  // differ from the last passing run for this test. A config delta is strong
  // infra-flake evidence: check whether the screenshot/error matches running
  // under the changed flag before calling anything a product bug.
  // R7-B — the baseline-vs-this-PR failure rate comparison. This is the
  // evidence that separates "flaked again" from "broke for real": a test at
  // 40% on master going 3-of-3 on this PR has shifted; one at 40% on both has
  // not. Stated as a citation the model can quote, and enforced independently
  // as a policy gate in canWaive — the prompt line is for the reason text, not
  // for the decision.
  const rs = f.rate_shift;
  const shift = rs?.ok
    ? `rate_shift: baseline ${rs.baseline_failed}/${rs.baseline_runs} (${(rs.baseline_rate * 100).toFixed(0)}%) vs this PR ${rs.pr_failed}/${rs.pr_runs} (${(rs.pr_rate * 100).toFixed(0)}%), p=${rs.p_value.toFixed(3)} at alpha=${rs.alpha} → shifted=${rs.shifted}`
    : "rate_shift=(not computable — no PR runs or baseline too small)";
  const env = ctx.group.environment_metadata
    ? `run_config=${JSON.stringify(ctx.group.environment_metadata)}`
    : "run_config=(not captured)";
  const delta =
    (f.config_delta ?? []).length > 0
      ? `config_delta_vs_last_passing_run=${f.config_delta!.join(", ")}`
      : "";
  return `You investigate ONE clustered E2E failure exactly as a careful human triager would: read the error and stack, view the failure screenshot, check this test's PAST failures on the baseline branch, check whether the same test is failing on other PRs right now, and read what this PR changed. Then decide. Do not ask for a rerun. 300 identical failures are still one cause.

Call TSIO tools as needed, then decide. You already have error/stack (and often screenshots) in this prompt — that IS evidence.

Return ONLY JSON when done:
{"verdict":"FLAKY_TEST|FLAKY_INFRA|FLAKY_SERVER|PR_REGRESSION|MAIN_REGRESSION|TEST_DEBT|INCONCLUSIVE","confidence":0.0,"reason":"...","gist":"...","citations":["error_message","screenshot",...],"suspect_sha":"optional","suspect_author":"optional","chronic":false,"product_refusal":false}

"gist" is the ONE sentence (≤120 chars, plain language, no citation tags) that humans read in the PR comment: what the test saw and what it means. Example: "Badge shows 3 mentions after unchecking suppress — wrong product state, not a timing race."

kind mapping: FLAKY_* = flake (no author). PR_REGRESSION / MAIN_REGRESSION / TEST_DEBT / BUILD_OR_ENV_ERROR = bug (name the commit/author via blame_commits).

DETERMINISTIC CLASSIFICATION — identical evidence must yield the identical verdict. Classify by the FIRST matching rule:
1. Screenshot or error shows the product deliberately refusing the action (red error banner, "you cannot save…", "would remove your access", permission/authorization dialog) → bug verdict (TEST_DEBT or PR_REGRESSION), product_refusal=true. The server answered correctly — never FLAKY_*.
2. Screenshot or error shows a WRONG PRODUCT STATE (wrong data persisted, corrupted content, broken layout, incorrect business logic) → bug verdict (PR_REGRESSION if the PR overlaps the area, else MAIN_REGRESSION).
3. Blank/unrendered page, mid-load capture, environment/bootstrap/login timeout, emulator/device signals → FLAKY_INFRA.
4. Network transport failures (DNS, ECONNREFUSED, 5xx, socket hang up) → FLAKY_SERVER.
5. UI timing race with CORRECT product state in the screenshot (element rendered but too slow, animation/transition race) → FLAKY_TEST.
6. Only if no rule matches and evidence is contradictory → INCONCLUSIVE.
Do not oscillate between FLAKY_INFRA/FLAKY_SERVER/FLAKY_TEST for the same error signature — apply the table.

RATE-SHIFT RULE (the "rate_shift" line above): a high historical failure rate is NOT on its own a reason to call a flake. What matters is whether THIS commit's failure count is explained by that rate. If rate_shift shows shifted=true, this test is failing materially more often here than its own baseline explains — "it flakes anyway" does not account for that, so prefer PR_REGRESSION (or MAIN_REGRESSION on a MAIN run) and cite "rate_shift". Note that a FLAKY_* verdict on a shifted rate is REFUSED by policy regardless of your confidence, so returning one only discards your reasoning; say what you actually think caused it instead.

kind mapping: FLAKY_* = flake (no author). PR_REGRESSION / MAIN_REGRESSION / TEST_DEBT / BUILD_OR_ENV_ERROR = bug (name the commit/author via blame_commits).

RETRY-RECOVERY RULE (status=flaky or retry_count>0 — the test failed once then passed on retry with no code change):
- Recovery is NECESSARY but NOT SUFFICIENT for FLAKY_*. A timing-sensitive product bug also passes on retry.
- Before ANY FLAKY_* verdict you MUST call get_history AND get_failing_elsewhere, and on a PR run get_pr_diff, and view the screenshot when keys are listed. Cite them ("history", "failing_elsewhere", "pr_diff", "screenshot"). A flake verdict without having looked at the change is a guess.
- Waive FLAKY_* only when recovery is corroborated by at least ONE of: past flaky/recovered outcomes in baseline history, the same test failing-and-recovering on other PRs, or a pure timing/timeout error signature with no wrong product state.
- Recovery + screenshot or error showing a WRONG PRODUCT STATE (wrong data, corrupted content, broken layout, incorrect business logic) is a BUG — return PR_REGRESSION or MAIN_REGRESSION, not flake.
- If get_history shows this test flaked/recovered ≥3 times in the last 20 baseline runs, set "chronic":true and start the reason with "chronic flake (n/20)" — a human must track it even though it is waived.
- If the error/stack shows the product DELIBERATELY refusing the action ("you cannot save…", "would remove your access", permission/authorization rejections), that is NOT flake — the server answered correctly. Set "product_refusal":true and return TEST_DEBT; flake waivers for such errors are blocked by policy.

Rules:
- NEVER return INCONCLUSIVE when error_message, error_stack, or screenshot keys are present. Pick FLAKY_* or a bug verdict.
- Empty history (runs=0) is normal on staging / new tests — NOT a reason for INCONCLUSIVE. Cite "empty_history" and still decide from error/screenshots. But know what happens next: with fewer than 3 baseline runs, a FLAKY_* verdict is REFUSED by policy unless the run also recovered on retry (status=flaky / retry_count>0), because on no history a flake call is a guess. So on a brand-new test that did NOT recover, say what you actually think broke it — a flake verdict there only discards your reasoning.
- Screenshots: view one when keys are listed; if keys are "(none)", decide from error/stack alone and cite those.
- confidence ≥0.85 with two citations (e.g. error_message + screenshot, or error_message + empty_history).
- If history shows already failing on the baseline, MAIN_REGRESSION — not this PR.
- PR_REGRESSION only when this PR changed product code or the failing spec that explains the failure. Files under .github/, detox/e2e/support/, detox/utils/, *.md are CI/harness — they do NOT make a UI timeout/login flake into PR_REGRESSION.
- If the PR only touches CI/harness and the failure is setup/login/timeout/emulator, prefer FLAKY_INFRA or FLAKY_SERVER.
- If the PR diff overlaps the failing product/spec area, do not call a flake — even if it recovered on retry. Read the diff (get_pr_diff) and the failing spec (get_test_source) before deciding a PR failure is a flake.

Cluster: ${cluster.signature} (${cluster.member_count} tests) — ${cluster.label}
Representative: ${f.full_title}
File: ${f.file || "unknown"}
Status: ${f.status} (retry_count=${f.retry_count}${f.retry_count > 0 ? " — recovered in this run, apply the RETRY-RECOVERY RULE above" : ""})
external_test_id: ${f.external_test_id || "none"}
Error: ${(f.error_message || "").slice(0, 3000) || "(none)"}
Stack: ${(f.error_stack || "").slice(0, 2000) || "(none)"}
History: ${hist}
${shift}
${env}${delta ? "\n" + delta : ""}
Other PRs failing: ${f.distinct_prs ?? "unknown"}
Screenshot keys (get_screenshot): ${shots}
PR changed files: ${ctx.changedFiles.slice(0, 40).join(", ") || "(none)"}
Deterministic hint: ${cluster.suggested.verdict} — ${cluster.suggested.reason} (hint citations: ${cluster.suggested.citations.join(", ") || "none"})`;
}

type ClaudeBlock = { type: string; text?: string; name?: string; id?: string; input?: unknown };

async function callClaude(
  ctx: AgentContext,
  messages: Array<{ role: string; content: unknown }>,
): Promise<{ content?: ClaudeBlock[]; stop_reason?: string }> {
  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-api-key": ctx.apiKey,
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify({
      model: ctx.model,
      max_tokens: 2048,
      tools: TOOLS,
      messages,
    }),
  });
  if (!res.ok) {
    throw new Error(`claude HTTP ${res.status} ${await res.text().catch(() => "")}`);
  }
  return (await res.json()) as { content?: ClaudeBlock[]; stop_reason?: string };
}

async function runTool(
  name: string,
  input: Record<string, string>,
  cluster: EvidenceCluster,
  ctx: AgentContext,
  toolUseId: string,
): Promise<Record<string, unknown>> {
  try {
    if (name === "get_history") {
      const testID = input.test_id || cluster.representative.external_test_id || "";
      const qs = new URLSearchParams({
        test_id: testID,
        repo: ctx.group.repository,
        branch: ctx.baselineBranch,
        limit: "20",
      });
      const data = await getJSON(`${ctx.baseURL}/api/v1/tests/history?${qs}`);
      return toolText(toolUseId, JSON.stringify(data).slice(0, 8000));
    }
    if (name === "get_failing_elsewhere") {
      const testID = input.test_id || cluster.representative.external_test_id || "";
      const qs = new URLSearchParams({
        test_id: testID,
        repo: ctx.group.repository,
        window: "24h",
      });
      if (ctx.group.gh_pr_number) qs.set("exclude_pr", String(ctx.group.gh_pr_number));
      const data = await getJSON(`${ctx.baseURL}/api/v1/tests/failing-elsewhere?${qs}`);
      return toolText(toolUseId, JSON.stringify(data).slice(0, 4000));
    }
    if (name === "get_screenshot") {
      const key = input.s3_key;
      const allowed = (cluster.representative.screenshots || []).some((s) => s.s3_key === key);
      if (!allowed) return toolText(toolUseId, `screenshot ${key} is not on this cluster`);
      const img = await loadShot(ctx.baseURL, key);
      if (!img) return toolText(toolUseId, `could not fetch ${key}`);
      return {
        type: "tool_result",
        tool_use_id: toolUseId,
        content: [
          { type: "image", source: { type: "base64", media_type: img.mediaType, data: img.data } },
          { type: "text", text: `screenshot ${key}` },
        ],
      };
    }
    if (name === "blame_commits") {
      const lastPass = input.last_pass_commit;
      const failingSince = input.failing_since_commit;
      if (!lastPass || !failingSince) {
        return toolText(toolUseId, "last_pass_commit and failing_since_commit are required");
      }
      const commits = await ctx.compareCommits(lastPass, failingSince);
      return toolText(toolUseId, JSON.stringify(attribute(commits)));
    }
    if (name === "get_pr_diff") {
      const diff = await ctx.getPrDiff();
      if (!diff) return toolText(toolUseId, "PR diff unavailable (no PR number or token)");
      return toolText(toolUseId, diff);
    }
    if (name === "get_test_source") {
      const path = input.path || cluster.representative.file || "";
      const sha = input.sha || ctx.group.commit_sha || "";
      if (!path) return toolText(toolUseId, "no file path for this cluster");
      const src = await ctx.getTestSource(path, sha);
      if (!src) return toolText(toolUseId, `could not fetch source for ${path}@${sha}`);
      return toolText(toolUseId, src);
    }
    return toolText(toolUseId, `unknown tool ${name}`);
  } catch (err) {
    return toolText(toolUseId, `tool error: ${(err as Error).message}`);
  }
}

function toolText(id: string, text: string): Record<string, unknown> {
  return { type: "tool_result", tool_use_id: id, content: text };
}

async function getJSON(url: string): Promise<unknown> {
  const res = await retryFetch(url, {}, "triage-agent");
  if (!res.ok) throw new Error(`HTTP ${res.status} for ${url}`);
  const ct = res.headers.get("content-type") || "";
  if (!ct.includes("json")) throw new Error(`not JSON (${ct}) for ${url}`);
  return res.json();
}

export async function loadShot(
  baseURL: string,
  key: string,
): Promise<{ mediaType: "image/png" | "image/jpeg"; data: string } | undefined> {
  const url = `${baseURL}/files/${key.split("/").map(encodeURIComponent).join("/")}`;
  const res = await retryFetch(url, { redirect: "follow" }, "triage-screenshot");
  if (!res.ok) return undefined;
  const buf = Buffer.from(await res.arrayBuffer());
  if (buf.length === 0 || buf.length > MAX_BYTES) return undefined;
  if (buf[0] === 0x89 && buf[1] === 0x50)
    return { mediaType: "image/png", data: buf.toString("base64") };
  if (buf[0] === 0xff && buf[1] === 0xd8)
    return { mediaType: "image/jpeg", data: buf.toString("base64") };
  return undefined;
}

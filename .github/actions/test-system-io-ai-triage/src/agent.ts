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
}

const TOOLS = [
  {
    name: "get_history",
    description:
      "GET /api/v1/tests/history — outcome series on the baseline branch. Use this to see if the test was already failing, flipping, or clean.",
    input_schema: {
      type: "object",
      properties: { test_id: { type: "string" } },
      required: ["test_id"],
    },
  },
  {
    name: "get_failing_elsewhere",
    description:
      "GET /api/v1/tests/failing-elsewhere — is the same test failing on other open PRs right now?",
    input_schema: {
      type: "object",
      properties: { test_id: { type: "string" } },
      required: ["test_id"],
    },
  },
  {
    name: "get_screenshot",
    description:
      "Fetch a TSIO failure screenshot (/files/{s3_key}) so you can see the UI. Call this before calling a flake.",
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
  return `You investigate ONE clustered E2E failure. Do not ask for a rerun. 300 identical failures are still one cause.

Call TSIO tools to collect what you need, look at screenshots, then decide.

Return ONLY JSON when done:
{"verdict":"FLAKY_TEST|FLAKY_INFRA|FLAKY_SERVER|PR_REGRESSION|MAIN_REGRESSION|TEST_DEBT|INCONCLUSIVE","confidence":0.0,"reason":"...","citations":["screenshot","history",...],"suspect_sha":"optional","suspect_author":"optional"}

kind mapping: FLAKY_* = flake (no author). PR_REGRESSION / MAIN_REGRESSION / TEST_DEBT / BUILD_OR_ENV_ERROR = bug (name the commit/author via blame_commits). INCONCLUSIVE if unsure.

Rules:
- Look at at least one screenshot before calling a flake.
- Prefer INCONCLUSIVE over a flake waiver.
- confidence 0.85+ needs two citations.
- If history says already failing on the baseline, it is MAIN_REGRESSION, not this PR.
- If the PR diff overlaps the failing area, do not call a flake.

Cluster: ${cluster.signature} (${cluster.member_count} tests) — ${cluster.label}
Representative: ${f.full_title}
File: ${f.file || "unknown"}
Status: ${f.status}
external_test_id: ${f.external_test_id || "none"}
Error: ${(f.error_message || "").slice(0, 3000) || "(none)"}
Stack: ${(f.error_stack || "").slice(0, 2000) || "(none)"}
History: ${hist}
Other PRs failing: ${f.distinct_prs ?? "unknown"}
Screenshot keys (get_screenshot): ${shots}
PR changed files: ${ctx.changedFiles.slice(0, 40).join(", ") || "(none)"}
Deterministic hint: ${cluster.suggested.verdict} — ${cluster.suggested.reason}`;
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

async function loadShot(
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

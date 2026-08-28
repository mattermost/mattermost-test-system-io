/**
 * AI test autofix: repair test-bug clusters the triage agent proved are NOT
 * product regressions (TEST_DEBT, or refusal-blocked flakes). A Claude agent
 * reads the failing spec, edits it under e2e-tests/**, and the resulting
 * commit is pushed to the PR branch — the standard CI loop (fresh E2E run +
 * re-triage) is the validation. Fail closed: anything suspicious is skipped.
 *
 * Safety rails: only e2e-tests/** may be written; ≤ MAX_FIX_TARGETS clusters
 * per run; ≤ MAX_EDIT_FILES files and ≤ MAX_DIFF_BYTES per fix; push falls
 * back to a PR comment on any git failure.
 *
 * Loop prevention (the fixer must never fight CI against itself): fixes are
 * pushed to the PR's own branch — the fixer NEVER opens a PR — and a fixed
 * spec lands in the PR diff, where the changed-files rule makes it permanently
 * ineligible for a second attempt. A git-log guard enforces that even if the
 * changed-files snapshot is stale, plus a hard cap on autofix commits per
 * branch. Everything the guard blocks is surfaced as "needs human review".
 */

import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import * as core from "@actions/core";
import { loadShot } from "./agent.ts";
import type { Decision, EvidenceCluster, FixTarget } from "./types.ts";

export const MAX_FIX_TARGETS = 2;
const MAX_EDIT_FILES = 4;
const MAX_DIFF_BYTES = 60 * 1024;
const MAX_ROUNDS = 12;
/** Max autofix commits per PR branch, lifetime — the hard loop breaker. */
export const MAX_AUTOFIX_COMMITS_PER_PR = 4;
const ALLOWED_PREFIXES = ["e2e-tests/"];

/**
 * Framework spec roots, repo-relative. TSIO ingests Playwright/Cypress JSON
 * with paths relative to the framework's spec dir (e.g. playwright's testDir
 * e2e-tests/playwright/specs), so evidence files like
 * "functional/channels/team_settings/team_settings_policy_editor.spec.ts"
 * must be re-rooted before any e2e-tests/** path check or workspace read.
 */
export const SPEC_ROOTS = ["e2e-tests/playwright/specs/", "e2e-tests/cypress/tests/integration/"];

/**
 * Re-root a report's spec path to repo-relative. Deterministic (no fs): every
 * candidate root lives under e2e-tests/, so any mapping stays inside the
 * writable prefix. When several candidate files could exist, the fixer
 * resolves the real one against the checkout (resolveSpecFile).
 */
export function repoRelSpecCandidates(file: string): string[] {
  const norm = file.replace(/^\.\//, "").replace(/^\/+/, "");
  if (norm.startsWith("e2e-tests/")) return [norm];
  // Only re-root plausible spec files — anything else (product sources,
  // stray data) must never become a fixer target.
  if (!/\.(spec|test)\.(ts|tsx|js|mjs)$/.test(norm) && !/_spec\.(js|ts)$/.test(norm)) {
    return [];
  }
  return SPEC_ROOTS.map((root) => root + norm);
}

export interface FixerContext {
  apiKey: string;
  model: string;
  workspace: string;
  token: string;
  repository: string;
  prBranch: string;
  prNumber?: number;
  baseURL: string;
  maxTargets: number;
}

export interface FixResult {
  signature: string;
  status: "fixed" | "skipped" | "failed";
  summary: string;
  files: string[];
  /** Why the fixer refused to run (loop guard) — drives "needs human" callouts. */
  skip_code?: "already_autofixed" | "branch_cap";
  diff?: string;
  commit_sha?: string;
}

/**
 * Loop-guard state: how many autofix commits the branch already carries and
 * which files a previous attempt touched. Read from git so it survives across
 * runs (each autofix push re-enters this job on the fresh checkout).
 */
export function autofixState(cwd: string): { commits: number; files: string[] } {
  try {
    // -F: the marker contains regex metacharacters ([ ]) — match literally.
    const commits = Number(
      git(cwd, ["rev-list", "--count", "-F", "--grep=[ai-triage autofix]", "HEAD"]),
    );
    const names = git(cwd, ["log", "-F", "--grep=[ai-triage autofix]", "--name-only", "--format="]);
    return {
      commits,
      files: names
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean),
    };
  } catch (err) {
    core.warning(`autofix state unavailable (${(err as Error).message}); assuming fresh branch`);
    return { commits: 0, files: [] };
  }
}

/**
 * Clusters the fixer may repair: an unwaived, confidently-adjudicated test
 * bug whose spec predates the PR (never touch the author's own new tests —
 * that is how the intentional demo sentinel would get "fixed" away).
 */
export function isFixable(d: Decision, cluster: EvidenceCluster, changedFiles: string[]): boolean {
  if (d.waived) return false;
  if (d.kind !== "bug" && !d.refusal) return false;
  if (d.verdict === "PR_REGRESSION" || d.verdict === "MAIN_REGRESSION") return false;
  if (d.confidence < 0.85) return false;
  const file = cluster.representative.file;
  if (!file) return false;
  // Report paths are spec-root-relative; compare every repo-rooted candidate
  // against the PR diff (exact or suffix — changedFiles are repo-rooted).
  const candidates = repoRelSpecCandidates(file);
  if (candidates.length === 0) return false;
  if (!candidates.every((c) => ALLOWED_PREFIXES.some((p) => c.startsWith(p)))) return false;
  if (changedFiles.some((f) => candidates.some((c) => f === c || f.endsWith(c) || c.endsWith(f)))) {
    return false;
  }
  return true;
}

export function collectFixTargets(
  clusters: EvidenceCluster[],
  decisions: Decision[],
  changedFiles: string[],
  max = MAX_FIX_TARGETS,
): FixTarget[] {
  const targets: FixTarget[] = [];
  for (let i = 0; i < decisions.length && targets.length < max; i++) {
    const d = decisions[i]!;
    const c = clusters[i]!;
    if (!isFixable(d, c, changedFiles)) continue;
    const f = c.representative;
    // file stays spec-root-relative (as ingested); fixOne re-roots it against
    // the checkout where the real framework root can be verified on disk.
    targets.push({
      signature: c.signature,
      external_test_id: f.external_test_id,
      full_title: f.full_title,
      file: f.file || "",
      error_message: f.error_message,
      error_stack: f.error_stack,
      reason: d.reason,
      confidence: d.confidence,
      screenshots: (f.screenshots || []).map((s) => s.s3_key),
    });
  }
  return targets;
}

export async function runFixer(targets: FixTarget[], ctx: FixerContext): Promise<FixResult[]> {
  const results: FixResult[] = [];
  const state = autofixState(ctx.workspace);
  core.info(
    `fixer loop guard: ${state.commits}/${MAX_AUTOFIX_COMMITS_PER_PR} autofix commits on branch, ` +
      `${state.files.length} spec file(s) already attempted`,
  );
  for (const target of targets) {
    if (state.commits >= MAX_AUTOFIX_COMMITS_PER_PR) {
      results.push({
        signature: target.signature,
        status: "skipped",
        skip_code: "branch_cap",
        summary:
          `loop guard: this branch already carries ${state.commits} autofix commits ` +
          `(cap ${MAX_AUTOFIX_COMMITS_PER_PR}) — pausing to avoid an AI↔CI fix loop; needs human review`,
        files: [],
      });
      continue;
    }
    // One attempt per spec per PR: a previous autofix commit already touched
    // this file, so the current run is the validation of that fix. Re-fixing
    // blind would loop CI against itself — hand it to a human instead.
    const touched = state.files.some(
      (f) => f === target.file || target.file.endsWith(f) || f.endsWith(target.file),
    );
    if (touched) {
      results.push({
        signature: target.signature,
        status: "skipped",
        skip_code: "already_autofixed",
        summary:
          "a previous autofix already edited this spec on this PR — the latest E2E run is the " +
          "validation of that fix; if it still fails, needs human review (spec is now in the " +
          "PR diff and protected)",
        files: [],
      });
      continue;
    }
    results.push(await fixOne(target, ctx));
  }
  return results;
}

async function fixOne(target: FixTarget, ctx: FixerContext): Promise<FixResult> {
  const rel = resolveSpecFile(ctx.workspace, target.file);
  if (!rel) {
    return {
      signature: target.signature,
      status: "skipped",
      summary: `file not found in checkout under any known spec root: ${target.file}`,
      files: [],
    };
  }
  const abs = path.resolve(ctx.workspace, rel);

  core.info(`fixer: ${target.signature} — ${target.full_title.slice(0, 100)} (${rel})`);
  let summary: string;
  try {
    summary = await fixWithAgent(target, ctx, rel);
  } catch (err) {
    return {
      signature: target.signature,
      status: "failed",
      summary: `agent: ${(err as Error).message}`,
      files: [],
    };
  }

  const diff = git(ctx.workspace, ["diff", "--", ...ALLOWED_PREFIXES]);
  const changed = diff
    .split("\n")
    .filter((l) => l.startsWith("+++ b/"))
    .map((l) => l.slice(6));
  if (!diff.trim() || changed.length === 0) {
    return {
      signature: target.signature,
      status: "skipped",
      summary: summary || "agent made no edits",
      files: [],
    };
  }
  if (changed.length > MAX_EDIT_FILES || Buffer.byteLength(diff) > MAX_DIFF_BYTES) {
    git(ctx.workspace, ["checkout", "--", ...ALLOWED_PREFIXES]);
    return {
      signature: target.signature,
      status: "skipped",
      summary: `diff too large (${changed.length} files, ${Buffer.byteLength(diff)}B) — reverted`,
      files: changed,
    };
  }

  const msg =
    `fix(e2e-test): [ai-triage autofix] stabilize ${target.full_title.slice(0, 80)}\n\n` +
    `Cluster ${target.signature} (confidence ${target.confidence}).\n` +
    `Triage root cause: ${target.reason.slice(0, 400)}\n\n` +
    `${summary.slice(0, 1200)}\n\n` +
    `Generated by the TSIO AI triage fixer; validated by the next E2E run.`;
  git(ctx.workspace, ["config", "user.email", "ai-triage-bot@mattermost.com"]);
  git(ctx.workspace, ["config", "user.name", "ai-triage-bot"]);
  git(ctx.workspace, ["add", "--", ...ALLOWED_PREFIXES]);
  git(ctx.workspace, ["commit", "-m", msg]);

  try {
    git(ctx.workspace, [
      "push",
      `https://x-access-token:${ctx.token}@github.com/${ctx.repository}.git`,
      `HEAD:refs/heads/${ctx.prBranch}`,
    ]);
  } catch (err) {
    git(ctx.workspace, ["reset", "--hard", "HEAD~1"]);
    return {
      signature: target.signature,
      status: "failed",
      summary: `push failed: ${(err as Error).message}`,
      files: changed,
    };
  }
  const sha = git(ctx.workspace, ["rev-parse", "HEAD"]);
  core.info(`fixer: pushed ${sha.slice(0, 7)} to ${ctx.prBranch} (${changed.join(", ")})`);
  return {
    signature: target.signature,
    status: "fixed",
    summary,
    files: changed,
    diff,
    commit_sha: sha,
  };
}

// ---------------------------------------------------------------------------
// Agent
// ---------------------------------------------------------------------------

const FIX_TOOLS = [
  {
    name: "read_file",
    description: "Read a file from the repo workspace (e2e-tests/** only).",
    input_schema: {
      type: "object",
      properties: { path: { type: "string" } },
      required: ["path"],
    },
  },
  {
    name: "list_dir",
    description: "List entries of a workspace directory (e2e-tests/** only).",
    input_schema: {
      type: "object",
      properties: { path: { type: "string" } },
      required: ["path"],
    },
  },
  {
    name: "write_file",
    description:
      "Overwrite a file with the full new content (e2e-tests/** only). Keep the test's intent; fix the setup/assertions that hit unsupported product states.",
    input_schema: {
      type: "object",
      properties: { path: { type: "string" }, content: { type: "string" } },
      required: ["path", "content"],
    },
  },
  {
    name: "get_screenshot",
    description: "View a failure screenshot captured for this test.",
    input_schema: {
      type: "object",
      properties: { s3_key: { type: "string" } },
      required: ["s3_key"],
    },
  },
];

/**
 * Map a report's spec-root-relative path to a repo-relative path that exists
 * in the checkout. Returns null when no candidate exists on disk (the caller
 * skips instead of guessing). Repo-relative results are always under
 * e2e-tests/, the fixer's writable prefix.
 */
export function resolveSpecFile(workspace: string, file: string): string | null {
  for (const candidate of repoRelSpecCandidates(file)) {
    if (fs.existsSync(path.resolve(workspace, candidate))) return candidate;
  }
  return null;
}

async function fixWithAgent(
  target: FixTarget,
  ctx: FixerContext,
  specFile: string,
): Promise<string> {
  const messages: Array<{ role: string; content: unknown }> = [
    { role: "user", content: fixerPrompt(target, specFile) },
  ];

  for (let round = 0; round < MAX_ROUNDS; round++) {
    const res = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-api-key": ctx.apiKey,
        "anthropic-version": "2023-06-01",
      },
      body: JSON.stringify({
        model: ctx.model,
        max_tokens: 8192,
        tools: FIX_TOOLS,
        messages,
      }),
    });
    if (!res.ok) throw new Error(`claude HTTP ${res.status}`);
    const body = (await res.json()) as {
      content?: Array<{ type: string; text?: string; name?: string; id?: string; input?: unknown }>;
    };
    const blocks = body.content || [];
    const toolUses = blocks.filter((b) => b.type === "tool_use");
    const text = blocks.find((b) => b.type === "text")?.text || "";

    if (toolUses.length === 0) {
      try {
        const parsed = JSON.parse(text.replace(/^```(?:json)?|```$/g, "").trim()) as {
          summary?: string;
        };
        return parsed.summary || text.slice(0, 1000);
      } catch {
        return text.slice(0, 1000);
      }
    }

    messages.push({ role: "assistant", content: blocks });
    const results: unknown[] = [];
    for (const tu of toolUses) {
      const name = String(tu.name);
      const input = (tu.input || {}) as Record<string, string>;
      let payload: unknown;
      try {
        if (name === "read_file") {
          payload = fs.readFileSync(guard(input.path, ctx.workspace), "utf8").slice(0, 48000);
        } else if (name === "list_dir") {
          payload = fs.readdirSync(guard(input.path, ctx.workspace)).join("\n").slice(0, 4000);
        } else if (name === "write_file") {
          const p = guard(input.path, ctx.workspace);
          fs.writeFileSync(p, input.content ?? "");
          payload = `wrote ${input.path} (${Buffer.byteLength(input.content || "")} bytes)`;
        } else if (name === "get_screenshot") {
          const img = await loadShot(ctx.baseURL, input.s3_key);
          if (!img) {
            payload = `could not fetch ${input.s3_key}`;
          } else {
            payload = {
              type: "tool_result",
              tool_use_id: String(tu.id),
              content: [
                {
                  type: "image",
                  source: { type: "base64", media_type: img.mediaType, data: img.data },
                },
                { type: "text", text: `screenshot ${input.s3_key}` },
              ],
            };
            results.push(payload);
            continue;
          }
        } else {
          payload = `unknown tool ${name}`;
        }
      } catch (err) {
        payload = `tool error: ${(err as Error).message}`;
      }
      results.push({ type: "tool_result", tool_use_id: String(tu.id), content: String(payload) });
    }
    messages.push({ role: "user", content: results });
  }
  throw new Error(`fixer hit ${MAX_ROUNDS} rounds without finishing`);
}

function fixerPrompt(t: FixTarget, specFile: string): string {
  return `You repair ONE failing E2E test in this repo checkout. The triage stage already proved this is a TEST bug, not a product regression, so fix the TEST CODE — never invent product workarounds that change what is being verified.

Diagnosis from triage (root cause, confidence ${t.confidence}):
${t.reason.slice(0, 1500)}

Failing test: ${t.full_title}
Spec file: ${specFile}
Error: ${(t.error_message || "").slice(0, 2500)}
Stack: ${(t.error_stack || "").slice(0, 1500)}

Fix principles, in order of preference:
1. If the test drives the product into a state the product correctly refuses (self-lockout, missing attributes/permissions), make the test create a SUPPORTED state: align policy rules/attributes with the acting user, or seed the user's attributes before acting. The assertion still verifies real product behavior.
2. If the test asserts before asynchronous product work settles (policy/index sync, propagation), wait deterministically for the settled state using the repo's existing helpers (polling APIs, expect.poll, UI indicators). Never a bare fixed sleep longer than what helpers already use.
3. If state leaks from a previous test (leftover modal/dialog/selection), clean it up in this test's setup or the suite's beforeEach.
4. Keep the test's original intent and assertions otherwise intact. Do not delete coverage, do not lower expectations, do not skip the test.

Workflow: read the spec file, read nearby helpers/support files it imports, then write the fix. Prefer the smallest change that removes the unsupported state or the race. When done, reply with ONLY JSON: {"summary":"what you changed and why the failure cannot recur","confidence":0.0}`;
}

function guard(rel: string | undefined, workspace: string): string {
  if (!rel) throw new Error("path required");
  const abs = path.resolve(workspace, rel);
  if (!abs.startsWith(path.resolve(workspace) + path.sep)) {
    throw new Error(`path escapes workspace: ${rel}`);
  }
  const norm = path.relative(workspace, abs).split(path.sep).join("/");
  if (!ALLOWED_PREFIXES.some((p) => norm.startsWith(p))) {
    throw new Error(`only ${ALLOWED_PREFIXES.join(", ")} paths are writable, got ${norm}`);
  }
  return abs;
}

function git(cwd: string, args: string[]): string {
  return execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    maxBuffer: 10 * 1024 * 1024,
  }).trim();
}

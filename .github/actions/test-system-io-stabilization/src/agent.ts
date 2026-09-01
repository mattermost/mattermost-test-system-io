/**
 * The repair agent — Anthropic messages API with a local tool loop, exactly
 * the shape the ai-triage fixer uses, but every write passes guardEditable
 * (e2e-tests/** only). The prompt carries the A-E flaky root-cause taxonomy
 * (async timing / shared state / isolation / env sensitivity / race) and the
 * matching fix recipes, so repairs target causes instead of adding waits.
 */
import * as fs from "node:fs";
import * as path from "node:path";
import { guardEditable } from "./rails.ts";

const MAX_ROUNDS = 12;

const TOOLS = [
  {
    name: "read_file",
    description: "Read a file inside e2e-tests/** (the only editable root).",
    input_schema: {
      type: "object",
      properties: { path: { type: "string" } },
      required: ["path"],
    },
  },
  {
    name: "edit_file",
    description:
      "Replace old_text with new_text in an e2e-tests/** file. Smallest unique old_text that covers the change.",
    input_schema: {
      type: "object",
      properties: { path: { type: "string" }, old_text: { type: "string" }, new_text: { type: "string" } },
      required: ["path", "old_text", "new_text"],
    },
  },
];

export interface RepairCtx {
  workspace: string;
  apiKey: string;
  model: string;
  testID: string;
  titles: string[];
  failureStats: string;
  evidenceURL: string;
}

export interface RepairResult {
  summary: string;
  editedFiles: string[];
  routed: boolean;
  routingReason?: string;
}

const TAXONOMY_PROMPT = `
Diagnose this test's flakiness by root cause, in this order, and fix the CAUSE:

A. Async timing — fixed sleeps or assertions before async work settles.
   Fix: replace sleeps with polling assertions (expect(locator).toBeVisible, expect.poll, cy.wait UNTIL); never add waitForTimeout.
B. Shared mutable state — module-level variables, singletons, mocks not reset.
   Fix: beforeEach/afterEach cleanup (clearAllMocks, useRealTimers, localStorage.clear); reset module state per test.
C. Test isolation — passes alone, fails in suite; order dependence.
   Fix: per-test setup/teardown; do not depend on tests that ran before.
D. Environment sensitivity — timezone, locale, feature flags, viewport.
   Fix: pin the environment (clock.setFixedTime, pinned locale, explicit flags) — do not branch around it.
E. Race conditions — Promise.all/parallel ops racing a UI transition.
   Fix: sequence the operations or await the settled state; never raise timeouts.

HARD RULES:
- You may only edit files under e2e-tests/. Anything else: refuse and say so.
- NEVER: add waitForTimeout/cy.wait(ms), raise timeouts, add retries, add .skip, delete or loosen assertions, use expect.soft. The W10 ban checker rejects all of these before any push — do not waste the attempt.
- If the evidence shows a PRODUCT bug (wrong product state, product refusing the action), do NOT fix. Set "routed": true with the reason — the loop files it to the owning team instead.
- Prefer the smallest edit that removes the cause.

Return ONLY JSON: {"summary":"one sentence, <=200 chars, the root cause and the fix","edited_files":["e2e-tests/..."],"routed":false,"routing_reason":"..."} or routed=true with no edits.`;

export async function repairSpec(ctx: RepairCtx): Promise<RepairResult> {
  const titles = ctx.titles.slice(0, 5).join(" | ") || ctx.testID;
  const messages: Array<{ role: string; content: unknown }> = [
    {
      role: "user",
      content: `Stabilize ONE flaky E2E test.\n\nTest: ${ctx.testID}\nTitles: ${titles}\nFailure profile: ${ctx.failureStats}\nEvidence report: ${ctx.evidenceURL}\n${TAXONOMY_PROMPT}`,
    },
  ];

  for (let round = 0; round < MAX_ROUNDS; round++) {
    const res = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-api-key": ctx.apiKey,
        "anthropic-version": "2023-06-01",
      },
      body: JSON.stringify({ model: ctx.model, max_tokens: 16384, tools: TOOLS, messages }),
    });
    if (!res.ok) throw new Error(`claude HTTP ${res.status}`);
    const body = (await res.json()) as {
      stop_reason?: string;
      content?: Array<{ type: string; text?: string; name?: string; id?: string; input?: unknown }>;
    };
    const blocks = body.content || [];
    const toolUses = blocks.filter((b) => b.type === "tool_use");
    const text = blocks.find((b) => b.type === "text")?.text || "";

    if (toolUses.length === 0) {
      try {
        // The model speaks snake_case per the prompt; normalize to camelCase.
        const parsed = JSON.parse(text.replace(/^```(?:json)?|```$/g, "").trim()) as Record<string, unknown>;
        return {
          summary: String(parsed.summary ?? text.slice(0, 200)),
          editedFiles: (parsed.edited_files as string[] | undefined) ?? [],
          routed: Boolean(parsed.routed),
          routingReason: (parsed.routing_reason ?? parsed.routingReason) as string | undefined,
        };
      } catch {
        return { summary: text.slice(0, 200), editedFiles: [], routed: false };
      }
    }

    messages.push({ role: "assistant", content: blocks });
    const results: unknown[] = [];
    for (const tu of toolUses) {
      const input = (tu.input || {}) as Record<string, string>;
      let payload: unknown;
      try {
        if (tu.name === "read_file") {
          const p = guardEditable(ctx.workspace, input.path ?? "");
          payload = fs.readFileSync(p, "utf8").slice(0, 48000);
        } else if (tu.name === "edit_file") {
          payload = applyEdit(ctx.workspace, input.path ?? "", input.old_text ?? "", input.new_text ?? "");
        } else {
          payload = `unknown tool ${tu.name}`;
        }
      } catch (err) {
        payload = `error: ${(err as Error).message}`;
      }
      results.push({ type: "tool_result", tool_use_id: String(tu.id), content: String(payload) });
    }
    messages.push({ role: "user", content: results });
  }
  throw new Error("repair agent hit max rounds without a verdict");
}

function applyEdit(workspace: string, target: string, oldText: string, newText: string): string {
  const p = guardEditable(workspace, target);
  const content = fs.readFileSync(p, "utf8");
  if (!content.includes(oldText)) {
    throw new Error(`old_text not found in ${target} — provide the smallest unique excerpt`);
  }
  const occurrences = content.split(oldText).length - 1;
  if (occurrences > 1) {
    throw new Error(`old_text is not unique (${occurrences} hits) — narrow it`);
  }
  fs.writeFileSync(p, content.replace(oldText, newText));
  return `edited ${path.relative(path.resolve(workspace), p)}`;
}

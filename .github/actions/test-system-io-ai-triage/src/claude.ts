import * as core from "@actions/core";
import type { ClaudeVerdict, EvidenceFailure } from "./types.ts";

const MAX_SHOTS = 3;
const MAX_BYTES = 2 * 1024 * 1024;
const MAX_ERROR = 4000;

export async function adjudicate(
  failure: EvidenceFailure,
  args: {
    baseURL: string;
    apiKey: string;
    model: string;
    changedFiles: string[];
  },
): Promise<ClaudeVerdict | undefined> {
  const images = await loadScreenshots(args.baseURL, failure.screenshots.slice(0, MAX_SHOTS));
  const prompt = buildPrompt(failure, args.changedFiles, images.length);
  try {
    const raw = await callClaude({
      apiKey: args.apiKey,
      model: args.model,
      prompt,
      images,
    });
    return parseVerdict(raw);
  } catch (err) {
    core.warning(`claude: ${(err as Error).message}; leaving ${failure.full_title} INCONCLUSIVE`);
    return undefined;
  }
}

function buildPrompt(failure: EvidenceFailure, changedFiles: string[], imageCount: number): string {
  const hist = failure.history
    ? `runs=${failure.history.runs} passed=${failure.history.passed} failed=${failure.history.failed} flaky=${failure.history.flaky} flips=${failure.history.flips} failure_rate=${failure.history.failure_rate} series=${failure.history.series.join(",")} failing_since=${failure.history.failing_since_commit ?? "none"}`
    : failure.history_error || "no history";
  const error = (failure.error_message || "").slice(0, MAX_ERROR);
  const stack = (failure.error_stack || "").slice(0, MAX_ERROR);
  return `You classify one E2E test failure. Do not rerun anything. Decide from the evidence.

Return ONLY JSON: {"verdict":"FLAKY_TEST|FLAKY_INFRA|FLAKY_SERVER|PR_REGRESSION|MAIN_REGRESSION|TEST_DEBT|INCONCLUSIVE","confidence":0.0,"reason":"...","citations":["..."]}

Rules:
- FLAKY_* if the screenshot/error shows a timing, animation, keyboard, network blip, or known-unstable locator, and the PR diff is unrelated.
- PR_REGRESSION if the failure matches the PR's changed files or the UI state is a real product bug.
- MAIN_REGRESSION if history shows it already failing on the baseline branch.
- INCONCLUSIVE if you cannot tell. Prefer INCONCLUSIVE over a flake waiver.
- confidence 0.85+ only with two citations (screenshot, error_message, history, diff, ...).
- ${imageCount} screenshot(s) attached, in failure order.

Test: ${failure.full_title}
File: ${failure.file || "unknown"}
Status: ${failure.status}
History: ${hist}
Other PRs currently failing: ${failure.distinct_prs ?? "unknown"}
Deterministic suggestion: ${failure.suggested.verdict} (${failure.suggested.reason})
Changed files: ${changedFiles.slice(0, 40).join(", ") || "(none)"}
Error: ${error || "(none)"}
Stack: ${stack || "(none)"}`;
}

async function loadScreenshots(
  baseURL: string,
  shots: EvidenceFailure["screenshots"],
): Promise<Array<{ mediaType: "image/png" | "image/jpeg"; data: string }>> {
  const out: Array<{ mediaType: "image/png" | "image/jpeg"; data: string }> = [];
  for (const s of shots) {
    const url = s.url.startsWith("http") ? s.url : `${baseURL}${s.url}`;
    try {
      const res = await fetch(url, { redirect: "follow" });
      if (!res.ok) continue;
      const buf = Buffer.from(await res.arrayBuffer());
      if (buf.length === 0 || buf.length > MAX_BYTES) continue;
      const mediaType = sniffMedia(buf, s.s3_key);
      if (!mediaType) continue;
      out.push({ mediaType, data: buf.toString("base64") });
    } catch (err) {
      core.warning(`screenshot ${s.s3_key}: ${(err as Error).message}`);
    }
  }
  return out;
}

function sniffMedia(buf: Buffer, key: string): "image/png" | "image/jpeg" | undefined {
  if (buf.length >= 8 && buf[0] === 0x89 && buf[1] === 0x50) return "image/png";
  if (buf.length >= 3 && buf[0] === 0xff && buf[1] === 0xd8) return "image/jpeg";
  if (key.endsWith(".png")) return "image/png";
  if (key.endsWith(".jpg") || key.endsWith(".jpeg")) return "image/jpeg";
  return undefined;
}

async function callClaude(args: {
  apiKey: string;
  model: string;
  prompt: string;
  images: Array<{ mediaType: "image/png" | "image/jpeg"; data: string }>;
}): Promise<string> {
  const content: Array<Record<string, unknown>> = [];
  for (const img of args.images) {
    content.push({
      type: "image",
      source: { type: "base64", media_type: img.mediaType, data: img.data },
    });
  }
  content.push({ type: "text", text: args.prompt });

  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-api-key": args.apiKey,
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify({
      model: args.model,
      max_tokens: 1024,
      messages: [{ role: "user", content }],
    }),
  });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} ${await res.text().catch(() => "")}`);
  }
  const body = (await res.json()) as { content?: Array<{ type: string; text?: string }> };
  const text = body.content?.find((c) => c.type === "text")?.text;
  if (!text) throw new Error("empty model response");
  return text;
}

const VERDICTS = new Set([
  "PR_REGRESSION",
  "MAIN_REGRESSION",
  "FLAKY_TEST",
  "FLAKY_INFRA",
  "FLAKY_SERVER",
  "BUILD_OR_ENV_ERROR",
  "TEST_DEBT",
  "INCONCLUSIVE",
]);

export function parseVerdict(raw: string): ClaudeVerdict {
  const json = extractJSON(raw);
  const verdict = String(json.verdict || "INCONCLUSIVE");
  const confidence = Number(json.confidence);
  const reason = String(json.reason || "").slice(0, 500);
  const citations = Array.isArray(json.citations)
    ? json.citations
        .map((c) => String(c))
        .filter(Boolean)
        .slice(0, 8)
    : [];
  return {
    verdict: VERDICTS.has(verdict) ? verdict : "INCONCLUSIVE",
    confidence: Number.isFinite(confidence) ? Math.min(1, Math.max(0, confidence)) : 0,
    reason: reason || "model returned no reason",
    citations,
  };
}

function extractJSON(raw: string): Record<string, unknown> {
  const start = raw.indexOf("{");
  const end = raw.lastIndexOf("}");
  if (start < 0 || end <= start) throw new Error("model response was not JSON");
  return JSON.parse(raw.slice(start, end + 1)) as Record<string, unknown>;
}

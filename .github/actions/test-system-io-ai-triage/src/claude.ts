import type { ClaudeVerdict } from "./types.ts";

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
  const suspectSha = json.suspect_sha ? String(json.suspect_sha) : undefined;
  const suspectAuthor = json.suspect_author ? String(json.suspect_author) : undefined;
  return {
    verdict: VERDICTS.has(verdict) ? verdict : "INCONCLUSIVE",
    confidence: Number.isFinite(confidence) ? Math.min(1, Math.max(0, confidence)) : 0,
    reason: reason || "model returned no reason",
    citations,
    suspect_sha: suspectSha,
    suspect_author: suspectAuthor,
  };
}

function extractJSON(raw: string): Record<string, unknown> {
  const start = raw.indexOf("{");
  const end = raw.lastIndexOf("}");
  if (start < 0 || end <= start) throw new Error("model response was not JSON");
  return JSON.parse(raw.slice(start, end + 1)) as Record<string, unknown>;
}

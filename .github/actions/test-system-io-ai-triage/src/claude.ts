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
  const gist = json.gist ? String(json.gist).slice(0, 160) : undefined;
  return {
    verdict: VERDICTS.has(verdict) ? verdict : "INCONCLUSIVE",
    confidence: Number.isFinite(confidence) ? Math.min(1, Math.max(0, confidence)) : 0,
    reason: reason || "model returned no reason",
    gist: gist || undefined,
    citations,
    suspect_sha: suspectSha,
    suspect_author: suspectAuthor,
    chronic: json.chronic === true,
    product_refusal: json.product_refusal === true,
  };
}

function extractJSON(raw: string): Record<string, unknown> {
  const fenced = raw.match(/```(?:json)?\s*([\s\S]*?)```/i);
  const candidate = (fenced?.[1] || raw).trim();
  const start = candidate.indexOf("{");
  const end = candidate.lastIndexOf("}");
  if (start < 0 || end <= start) throw new Error("model response was not JSON");
  const slice = candidate.slice(start, end + 1);
  try {
    return JSON.parse(slice) as Record<string, unknown>;
  } catch (first) {
    // Models sometimes emit trailing commas or smart quotes; try a light clean-up.
    const cleaned = slice
      .replace(/[“”]/g, '"')
      .replace(/[‘’]/g, "'")
      .replace(/,\s*([}\]])/g, "$1");
    try {
      return JSON.parse(cleaned) as Record<string, unknown>;
    } catch {
      throw first;
    }
  }
}

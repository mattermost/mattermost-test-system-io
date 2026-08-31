/**
 * MVP triage verdict → PR comment. One comment per PR (idempotent via a
 * marker; updated in place so retries never spam). Tags the PR author only
 * for PR_REGRESSION — blame-based suspect authors name the TEST's author,
 * which would misdirect a product bug. MAIN_REGRESSION explicitly says the
 * culprit lives on master (bisect attribution lands with MVP #2).
 * Comment failures never fail the triage job (fail-open for reporting).
 */
import type { Decision, EvidenceCluster } from "./types.ts";
import * as core from "@actions/core";

export const VERDICT_COMMENT_MARKER = "<!-- tsio:ai-triage-verdict -->";

export function formatTriageComment(args: {
  prAuthor?: string;
  decisions: Decision[];
  clusters: EvidenceCluster[];
  reportURL: string;
}): string | null {
  // Unblocking path: all-waived must stay silent — silence IS the feature.
  if (args.decisions.length === 0) return null;
  const productBugs = args.decisions.filter(
    (d) => d.verdict === "PR_REGRESSION" || d.verdict === "MAIN_REGRESSION",
  );
  if (productBugs.length === 0) return null;

  const prRegressions = productBugs.filter((d) => d.verdict === "PR_REGRESSION");
  const mainRegressions = productBugs.filter((d) => d.verdict === "MAIN_REGRESSION");
  const waived = args.decisions.filter((d) => d.waived).length;

  const lines: string[] = [VERDICT_COMMENT_MARKER, `## 🤖 E2E AI triage`, ``];

  // The gist: one sentence per headline, no stories.
  if (prRegressions.length > 0) {
    const tag = args.prAuthor ? `@${args.prAuthor}` : "PR author";
    lines.push(
      `${tag} — ${prRegressions.length} cluster(s) look caused by this PR. Fix here; ` +
        `if the triage is wrong, a maintainer can \`/e2e-triage-override\`.`,
    );
  }
  if (mainRegressions.length > 0) {
    lines.push(
      `**${mainRegressions.length} cluster(s) look like an existing bug on master, not this PR** — ` +
        `bisect is queued and will tag the culprit author. Maintainer shortcut: \`/e2e-triage-override\`.`,
    );
  }
  lines.push(``);

  for (let i = 0; i < args.decisions.length; i++) {
    const d = args.decisions[i]!;
    if (d.verdict !== "PR_REGRESSION" && d.verdict !== "MAIN_REGRESSION") continue;
    const c = args.clusters[i]!;
    lines.push(
      `- \`${c.signature.slice(0, 8)}\` **${clusterTitle(c, 64)}** ×${d.member_count} — ${d.verdict}` +
        `${d.suspect_author ? `, suspect @${d.suspect_author}` : ""} (${d.gist || firstSentence(d.reason, 120)})`,
    );
  }

  lines.push(
    ``,
    `<details><summary>All ${args.decisions.length} cluster(s) (${waived} waived as flaky)</summary>`,
    ``,
  );
  lines.push(`| Cluster | Verdict | Waived | Gist |`, `|---|---|:--:|---|`);
  for (let i = 0; i < args.decisions.length; i++) {
    const d = args.decisions[i]!;
    const c = args.clusters[i]!;
    lines.push(
      `| \`${c.signature.slice(0, 8)}\` | ${d.verdict}\`${Math.round(d.confidence * 100)}%\` | ${d.waived ? "✅" : "—"} | ${(
        d.gist || firstSentence(d.reason, 120)
      ).replace(/\|/g, " ")} |`,
    );
  }
  lines.push(``, `</details>`, ``, `[Full report with screenshots](${args.reportURL})`);
  return lines.join("\n");
}

/** First sentence, cut at a word boundary — never mid-word "half" text. */
export function firstSentence(text: string, max: number): string {
  const clean = text.replace(/\s+/g, " ").trim();
  if (clean.length <= max) return clean;
  const cut = clean.slice(0, max);
  const sp = cut.lastIndexOf(" ");
  return `${sp > max * 0.5 ? cut.slice(0, sp) : cut}…`;
}

function clusterTitle(c: EvidenceCluster, max: number): string {
  const label = (c.label || "").replace(/\|/g, " ").trim();
  if (label.length <= max) return label;
  const sp = label.lastIndexOf(" ", max);
  return `${sp > max * 0.5 ? label.slice(0, sp) : label.slice(0, max)}…`;
}

/**
 * Post or update the single triage-verdict comment. Never throws — comment
 * problems are reported as warnings, the classification itself is already on
 * the commit status (fail-closed there).
 */
export async function upsertTriageComment(args: {
  token: string;
  owner: string;
  repo: string;
  prNumber: number;
  body: string;
  apiURL?: string;
}): Promise<string | null> {
  const api = (args.apiURL || "https://api.github.com").replace(/\/$/, "");
  const headers = {
    authorization: `Bearer ${args.token}`,
    "content-type": "application/json",
    accept: "application/vnd.github+json",
  };
  try {
    // One comment per PR: find the marker and update in place so workflow
    // retries and later SHAs refresh the same comment instead of flooding.
    const listRes = await fetch(
      `${api}/repos/${args.owner}/${args.repo}/issues/${args.prNumber}/comments?per_page=100`,
      { headers },
    );
    if (!listRes.ok) throw new Error(`list comments HTTP ${listRes.status}`);
    const comments = (await listRes.json()) as Array<{ id: number; body: string }>;
    const existing = comments.find((c) => c.body.includes(VERDICT_COMMENT_MARKER));

    const res = await fetch(
      existing
        ? `${api}/repos/${args.owner}/${args.repo}/issues/comments/${existing.id}`
        : `${api}/repos/${args.owner}/${args.repo}/issues/${args.prNumber}/comments`,
      {
        method: existing ? "PATCH" : "POST",
        headers,
        body: JSON.stringify({ body: args.body }),
      },
    );
    if (!res.ok) throw new Error(`upsert comment HTTP ${res.status} ${await res.text()}`);
    const created = (await res.json()) as { html_url?: string };
    return created.html_url ?? null;
  } catch (err) {
    core.warning(`triage comment failed: ${(err as Error).message}`);
    return null;
  }
}

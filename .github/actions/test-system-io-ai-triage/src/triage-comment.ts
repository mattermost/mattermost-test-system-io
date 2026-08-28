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

  const lines: string[] = [VERDICT_COMMENT_MARKER, `## 🤖 E2E triage — human action needed`, ``];

  if (prRegressions.length > 0) {
    const tag = args.prAuthor ? `@${args.prAuthor}` : "PR author";
    lines.push(
      `**${tag}: the failures below look caused by this PR's changes.** ` +
        `Fix them in this PR before merge. If the triage is wrong (test was already broken ` +
        `before this PR), a maintainer can override with \`/e2e-triage-override\`.` +
        (prRegressions.some((d) => d.suspect_author)
          ? ` Suspect test-file authors (spec last touched): ${[
              ...new Set(
                prRegressions
                  .map((d) => d.suspect_author)
                  .filter(Boolean)
                  .map((a) => `@${a}`),
              ),
            ].join(", ")}.`
          : ""),
      ``,
    );
  }
  if (mainRegressions.length > 0) {
    lines.push(
      `**${mainRegressions.length} failure cluster(s) look like an existing bug on master** — ` +
        `not caused by this PR (same failures occur on PRs without these changes / on master). ` +
        `Culprit-commit attribution (git bisect) is queued; the bisect report will tag the responsible author. ` +
        `Meanwhile, a maintainer can \`/e2e-triage-override\` to unblock this PR if the red check is a known issue.`,
      ``,
    );
  }

  lines.push(
    `| Classification | Cluster | n | Verdict | Suspect | Waived | Why |`,
    `|---|---|---:|---|---|---|---|`,
  );
  for (let i = 0; i < args.decisions.length; i++) {
    const d = args.decisions[i]!;
    const c = args.clusters[i]!;
    const classification =
      d.verdict === "PR_REGRESSION"
        ? `🔴 your PR`
        : d.verdict === "MAIN_REGRESSION"
          ? `🔴 master`
          : d.kind === "bug"
            ? `🟡 test bug`
            : `flake/infra`;
    const suspect = d.suspect_author
      ? `@${d.suspect_author} (\`${(d.suspect_sha || "").slice(0, 7)}\`)`
      : "—";
    lines.push(
      `| ${classification} | \`${c.signature.slice(0, 8)}\` ${c.label.replace(/\|/g, " ").slice(0, 60)} | ${d.member_count} | ${d.verdict} | ${suspect} | ${d.waived ? "yes" : "no"} | ${d.reason.replace(/\|/g, " ").slice(0, 140)} |`,
    );
  }

  lines.push(``, `[Full report with screenshots](${args.reportURL})`);
  return lines.join("\n");
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

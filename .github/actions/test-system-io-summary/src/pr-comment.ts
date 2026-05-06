/**
 * Find-or-replace a PR comment keyed by a marker line in its body.
 *
 * GitHub treats `<!-- ... -->` as HTML and renders it invisibly, so the
 * marker is a stable, hidden anchor for "this is the comment for run
 * `cypress-full-enterprise@9b74c5a`". A re-run on the same commit
 * replaces the existing comment instead of stacking; a different
 * (run name, sha) pair gets its own comment.
 *
 * Failures here are warnings only — the caller's load-bearing work
 * (orchestration begin / summary) already happened before this runs.
 */
import * as core from "@actions/core";

interface ListedComment {
  id: number;
  body?: string;
}

export interface PostOrUpdateArgs {
  token: string;
  owner: string;
  repo: string;
  prNumber: number;
  marker: string;
  body: string;
  // When true, only the first page of PR comments (100 rows) is
  // scanned for the marker. If the marker is past page 1, a new
  // comment is posted instead of updating the existing one. Trades
  // exact replacement on busy PRs for a single fixed API call.
  singlePage?: boolean;
}

const GITHUB_API = "https://api.github.com";

export async function postOrUpdatePRComment(args: PostOrUpdateArgs): Promise<void> {
  const { token, owner, repo, prNumber, marker, body, singlePage } = args;
  if (!token) {
    core.warning("post-pr-comment: github-token not provided; skipping comment.");
    return;
  }
  // GitHub auto-masks values sourced from `secrets.*`; this is belt-and-
  // suspenders for cases where the caller wires a non-secret token.
  core.setSecret(token);
  if (!body.includes(marker)) {
    core.warning(
      "post-pr-comment: body does not contain marker; skipping to avoid orphaned comments.",
    );
    return;
  }
  try {
    const existingId = await findCommentByMarker(
      token,
      owner,
      repo,
      prNumber,
      marker,
      !!singlePage,
    );
    if (existingId != null) {
      await patchComment(token, owner, repo, existingId, body);
      core.info(`post-pr-comment: updated comment ${existingId}`);
    } else {
      const createdId = await postComment(token, owner, repo, prNumber, body);
      core.info(`post-pr-comment: created comment ${createdId}`);
    }
  } catch (e) {
    core.warning(`post-pr-comment: ${(e as Error).message}`);
  }
}

async function findCommentByMarker(
  token: string,
  owner: string,
  repo: string,
  prNumber: number,
  marker: string,
  singlePage: boolean,
): Promise<number | null> {
  // 100/page is the max GitHub allows. Callers can opt to stop after
  // page 1 (singlePage) when they prefer a fixed-cost lookup over an
  // exact replacement guarantee.
  let url: string | null =
    `${GITHUB_API}/repos/${owner}/${repo}/issues/${prNumber}/comments?per_page=100`;
  while (url) {
    const res: Response = await fetch(url, { headers: ghHeaders(token) });
    if (!res.ok) {
      throw new Error(`list comments failed: ${res.status} ${await safeText(res)}`);
    }
    const list = (await res.json()) as ListedComment[];
    for (const c of list) {
      if (c.body && c.body.includes(marker)) return c.id;
    }
    if (singlePage) break;
    url = nextPageURL(res.headers.get("link"));
  }
  return null;
}

async function patchComment(
  token: string,
  owner: string,
  repo: string,
  commentId: number,
  body: string,
): Promise<void> {
  const res = await fetch(`${GITHUB_API}/repos/${owner}/${repo}/issues/comments/${commentId}`, {
    method: "PATCH",
    headers: { ...ghHeaders(token), "content-type": "application/json" },
    body: JSON.stringify({ body }),
  });
  if (!res.ok) {
    throw new Error(`patch comment ${commentId} failed: ${res.status} ${await safeText(res)}`);
  }
}

async function postComment(
  token: string,
  owner: string,
  repo: string,
  prNumber: number,
  body: string,
): Promise<number> {
  const res = await fetch(`${GITHUB_API}/repos/${owner}/${repo}/issues/${prNumber}/comments`, {
    method: "POST",
    headers: { ...ghHeaders(token), "content-type": "application/json" },
    body: JSON.stringify({ body }),
  });
  if (!res.ok) {
    throw new Error(`create comment failed: ${res.status} ${await safeText(res)}`);
  }
  const created = (await res.json()) as { id: number };
  return created.id;
}

function ghHeaders(token: string): Record<string, string> {
  return {
    accept: "application/vnd.github+json",
    authorization: `Bearer ${token}`,
    "x-github-api-version": "2022-11-28",
  };
}

function nextPageURL(link: string | null): string | null {
  if (!link) return null;
  // Link header format: <url1>; rel="next", <url2>; rel="last"
  for (const part of link.split(",")) {
    const m = part.match(/<([^>]+)>;\s*rel="next"/);
    if (m) return m[1] ?? null;
  }
  return null;
}

async function safeText(res: Response): Promise<string> {
  try {
    return (await res.text()).slice(0, 200);
  } catch {
    return "<unreadable body>";
  }
}

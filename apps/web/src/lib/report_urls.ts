/** Strip refs/heads/ or refs/tags/. Mirrors server stripRefPrefix. */
export function stripRefPrefix(branch: string): string {
  return branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
}

/** Parse pr-N from a branch or GitHub ref. Mirrors server parsePRBranch. */
export function parsePRBranch(branch: string): number | undefined {
  const short = stripRefPrefix(branch);
  const prMatch = short.match(/^pr-(\d+)/i) || branch.match(/^refs\/pull\/(\d+)\//);
  if (!prMatch) return undefined;
  const n = parseInt(prMatch[1]!, 10);
  return n > 0 ? n : undefined;
}

const COMMIT_SHA_RE = /^[0-9a-f]{7,40}$/i;

/** Encode a git ref for a single URL path segment (slashes → ~). */
export function encodeBranchPathSegment(branch: string): string {
  return stripRefPrefix(branch).replace(/\//g, '~');
}

/** Decode a branch path segment from the URL back to a git ref. */
export function decodeBranchPathSegment(segment: string): string {
  try {
    return decodeURIComponent(segment).replace(/~/g, '/');
  } catch {
    return segment.replace(/~/g, '/');
  }
}

/** decodeURIComponent that returns the raw segment on malformed percent-encoding. */
function decodeURIComponentSafe(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

export function repositoryDisplayName(repository: string): string {
  const tail = repository.split('/').pop() || repository;
  if (tail === 'mattermost-mobile') return 'mobile';
  if (tail === 'mattermost-desktop') return 'desktop';
  return tail;
}

export function shortSHA(commit: string): string {
  return commit.slice(0, 7);
}

export type ConsolidatedReportLinkInput = {
  repository: string;
  branch: string;
  commit: string;
  name: string;
};

/** Consolidated report path: /reports/{repo}/{branch}/{sha}/{name}. */
export function buildConsolidatedReportPath(input: ConsolidatedReportLinkInput): string {
  const repoName = repositoryDisplayName(input.repository);
  const branchSegment = encodeBranchPathSegment(input.branch);
  const path = `/reports/${encodeURIComponent(repoName)}/${encodeURIComponent(branchSegment)}/${shortSHA(input.commit)}/${encodeURIComponent(input.name)}`;
  return path;
}

export type ParsedReportPath =
  | { mode: 'consolidated'; branch: string; commit: string; name: string }
  | { mode: 'commit'; branch: string; commit: string }
  | { mode: 'branch'; branch: string };

/** Parse /reports/:repo/<splat> where the branch may contain slashes. */
export function parseReportPathSplat(splat: string): ParsedReportPath | null {
  const trimmed = splat.replace(/^\/+|\/+$/g, '');
  if (!trimmed) return null;
  const parts = trimmed.split('/');

  if (parts.length >= 3) {
    const name = decodeURIComponentSafe(parts[parts.length - 1]!);
    const commit = parts[parts.length - 2]!;
    if (COMMIT_SHA_RE.test(commit)) {
      const branch = parts.slice(0, -2).map(decodeBranchPathSegment).join('/');
      return { mode: 'consolidated', branch, commit, name };
    }
  }
  if (parts.length >= 2) {
    const commit = parts[parts.length - 1]!;
    if (COMMIT_SHA_RE.test(commit)) {
      const branch = parts.slice(0, -1).map(decodeBranchPathSegment).join('/');
      return { mode: 'commit', branch, commit };
    }
  }
  return { mode: 'branch', branch: parts.map(decodeBranchPathSegment).join('/') };
}

/** Append gh_run_id when the server path omits it (legacy rows). */
export function ensureRunQueryParams(
  urlPath: string,
  ghRunId?: string | null,
  ghRunAttempt?: string | null,
): string {
  if (!ghRunId || urlPath.includes('gh_run_id=')) return urlPath;
  const params = new URLSearchParams();
  params.set('gh_run_id', ghRunId);
  params.set('gh_run_attempt', ghRunAttempt || '1');
  const sep = urlPath.includes('?') ? '&' : '?';
  return `${urlPath}${sep}${params}`;
}

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

/** Branch path segment for consolidated report URLs. */
export function reportBranchSegment(branch: string, ghPrNumber?: number | null): string {
  if (ghPrNumber != null && ghPrNumber > 0) {
    return `pr-${ghPrNumber}`;
  }
  const pr = parsePRBranch(branch);
  if (pr != null) return `pr-${pr}`;
  return stripRefPrefix(branch);
}

export function repositoryDisplayName(repository: string): string {
  return repository.split('/').pop() || repository;
}

export function shortSHA(commit: string): string {
  return commit.slice(0, 7);
}

export type ConsolidatedReportLinkInput = {
  repository: string;
  branch: string;
  commit: string;
  name: string;
  gh_pr_number?: number | null;
  gh_run_id?: string | null;
  gh_run_attempt?: string | null;
};

/** Consolidated report path: /reports/{repo}/{branch}/{sha}/{name}[?gh_run_id=…]. */
export function buildConsolidatedReportPath(input: ConsolidatedReportLinkInput): string {
  const repoName = repositoryDisplayName(input.repository);
  const branchSegment = reportBranchSegment(input.branch, input.gh_pr_number);
  const path = `/reports/${encodeURIComponent(repoName)}/${encodeURIComponent(branchSegment)}/${shortSHA(input.commit)}/${encodeURIComponent(input.name)}`;
  if (!input.gh_run_id) return path;
  const params = new URLSearchParams();
  params.set('gh_run_id', input.gh_run_id);
  params.set('gh_run_attempt', input.gh_run_attempt || '1');
  return `${path}?${params}`;
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

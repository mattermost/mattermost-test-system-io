/** Canonical run labels and their uploaded report_group.name members (mobile). */
const RUN_FAMILIES: Record<string, string[]> = {
  'mobile-pr': ['mobile-detox-pr', 'mobile-maestro-pr'],
  'mobile-master': ['mobile-detox-master', 'mobile-maestro-master'],
  'mobile-cmt': ['mobile-cmt-detox', 'mobile-cmt-maestro'],
};

export function canonicalRunName(groupName: string): string {
  for (const [canon, members] of Object.entries(RUN_FAMILIES)) {
    if (members.includes(groupName)) return canon;
  }
  return groupName;
}

export function isRunFamilyMember(groupName: string): boolean {
  return canonicalRunName(groupName) !== groupName;
}

export function runConsolidatedHref(report: {
  repository: string;
  branch: string;
  commit: string;
  name: string;
  gh_run_id?: string;
  gh_run_attempt?: string;
  gh_pr_number?: number;
}): string | null {
  const canon = canonicalRunName(report.name);
  if (canon === report.name) return null;

  const repoName = report.repository.split('/').pop() || report.repository;
  const shortBranch = report.branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
  const prMatch =
    shortBranch.match(/^pr-(\d+)/i) || report.branch.match(/^refs\/pull\/(\d+)\//);
  const branchSegment =
    report.gh_pr_number != null
      ? `pr-${report.gh_pr_number}`
      : prMatch
        ? `pr-${prMatch[1]}`
        : shortBranch;
  const shortSha = report.commit.slice(0, 7);
  const params = new URLSearchParams();
  if (report.gh_run_id) params.set('gh_run_id', report.gh_run_id);
  if (report.gh_run_attempt) params.set('gh_run_attempt', report.gh_run_attempt);
  const qs = params.toString();
  return `/reports/${encodeURIComponent(repoName)}/${encodeURIComponent(branchSegment)}/${shortSha}/${encodeURIComponent(canon)}${qs ? `?${qs}` : ''}`;
}

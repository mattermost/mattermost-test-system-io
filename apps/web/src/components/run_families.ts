/** Canonical run labels and their uploaded report_group.name members (mobile). */
import { buildConsolidatedReportPath } from '@/lib/report_urls';

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

  return buildConsolidatedReportPath({
    repository: report.repository,
    branch: report.branch,
    commit: report.commit,
    name: canon,
    gh_pr_number: report.gh_pr_number,
    gh_run_id: report.gh_run_id,
    gh_run_attempt: report.gh_run_attempt,
  });
}

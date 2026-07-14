import { buildConsolidatedReportPath } from '@/lib/report_urls';

const RUN_FAMILIES: Record<string, string[]> = {
  'mobile-pr': ['mobile-detox-pr', 'mobile-maestro-pr'],
  // mobile-main is canonical; include legacy mobile-master* names for old uploads.
  'mobile-main': [
    'mobile-detox-main',
    'mobile-maestro-main',
    'mobile-master',
    'mobile-detox-master',
    'mobile-maestro-master',
  ],
  'cmt-mobile': ['mobile-cmt-detox', 'mobile-cmt-maestro', 'mobile-cmt'],
};

export function canonicalRunName(groupName: string): string {
  for (const [canon, members] of Object.entries(RUN_FAMILIES)) {
    if (groupName === canon || members.includes(groupName)) return canon;
  }
  return groupName;
}

export function runConsolidatedHref(report: {
  repository: string;
  branch: string;
  commit: string;
  name: string;
}): string | null {
  const canon = canonicalRunName(report.name);
  if (canon === report.name && !RUN_FAMILIES[report.name]) return null;
  return buildConsolidatedReportPath({
    repository: report.repository,
    branch: report.branch,
    commit: report.commit,
    name: canon,
  });
}

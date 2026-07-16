import { buildConsolidatedReportPath, ensureRunQueryParams } from '@/lib/report_urls';

/** Redirect member report-detail pages to the consolidated run_group URL. */
export function runConsolidatedHref(report: {
  repository: string;
  branch: string;
  commit: string;
  name: string;
  run_group?: string | null;
  gh_run_id?: string | null;
  gh_run_attempt?: string | null;
}): string | null {
  const runGroup = report.run_group?.trim();
  if (!runGroup || runGroup === report.name) return null;
  const path = buildConsolidatedReportPath({
    repository: report.repository,
    branch: report.branch,
    commit: report.commit,
    name: runGroup,
  });
  return ensureRunQueryParams(path, report.gh_run_id, report.gh_run_attempt);
}

import { useParams } from 'react-router-dom';
import { parseReportPathSplat } from '@/lib/report_urls';
import { FilteredReportPage } from '@/pages/filtered_report_page';
import { FilteredReportsPage } from '@/pages/filtered_reports_page';

export function ReportPathRouter() {
  const { repo, '*': splat } = useParams<{ repo: string; '*': string }>();
  const parsed = parseReportPathSplat(splat || '');

  if (!repo || !parsed) {
    return <FilteredReportsPage repo={repo} />;
  }

  switch (parsed.mode) {
    case 'consolidated':
      return (
        <FilteredReportPage
          repo={repo}
          branch={parsed.branch}
          commit={parsed.commit}
          name={parsed.name}
        />
      );
    case 'commit':
      return <FilteredReportsPage repo={repo} branch={parsed.branch} commit={parsed.commit} />;
    case 'branch':
      return <FilteredReportsPage repo={repo} branch={parsed.branch} />;
  }
}

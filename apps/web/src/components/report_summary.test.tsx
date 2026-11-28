import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { ReportSummary } from './report_summary';

describe('ReportSummary KPI links', () => {
  it('renders Repo KPIs and Lane KPIs links when repository and name are provided', () => {
    render(
      <ReportSummary
        name="mobile-main-detox-android"
        passed={10}
        failed={1}
        flaky={0}
        skipped={0}
        total={11}
        repository="mattermost/mattermost-mobile"
      />,
    );

    const repoLink = screen.getByRole('link', { name: 'Repo KPIs' });
    expect(repoLink).toHaveAttribute('href', '/kpi?repo=mattermost%2Fmattermost-mobile');

    const laneLink = screen.getByRole('link', { name: 'Lane KPIs' });
    expect(laneLink).toHaveAttribute(
      'href',
      '/kpi?lane=mobile-main-detox-android&repo=mattermost%2Fmattermost-mobile',
    );
  });

  it('omits Lane KPIs when name is omitted', () => {
    render(
      <ReportSummary
        passed={5}
        failed={0}
        flaky={0}
        skipped={0}
        total={5}
        repository="mattermost/desktop"
      />,
    );

    expect(screen.getByRole('link', { name: 'Repo KPIs' })).toHaveAttribute(
      'href',
      '/kpi?repo=mattermost%2Fdesktop',
    );
    expect(screen.queryByRole('link', { name: 'Lane KPIs' })).not.toBeInTheDocument();
  });
});

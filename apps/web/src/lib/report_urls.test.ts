import { describe, expect, it } from 'vitest';
import {
  parsePRBranch,
  reportBranchSegment,
  repositoryDisplayName,
  shortSHA,
  stripRefPrefix,
  buildConsolidatedReportPath,
  ensureRunQueryParams,
} from './report_urls';

describe('stripRefPrefix', () => {
  it('strips refs/heads and refs/tags', () => {
    expect(stripRefPrefix('refs/heads/main')).toBe('main');
    expect(stripRefPrefix('refs/tags/v1')).toBe('v1');
    expect(stripRefPrefix('feature')).toBe('feature');
  });
});

describe('parsePRBranch', () => {
  it('parses pr-N branch segments', () => {
    expect(parsePRBranch('pr-3891')).toBe(3891);
    expect(parsePRBranch('PR-42')).toBe(42);
  });

  it('parses refs/pull/N/ head refs', () => {
    expect(parsePRBranch('refs/pull/123/merge')).toBe(123);
  });

  it('returns undefined for non-PR branches', () => {
    expect(parsePRBranch('tsio-spike')).toBeUndefined();
    expect(parsePRBranch('pr-0')).toBeUndefined();
  });
});

describe('reportBranchSegment', () => {
  it('prefers gh_pr_number over branch parsing', () => {
    expect(reportBranchSegment('refs/heads/feature', 3891)).toBe('pr-3891');
  });

  it('falls back to branch parsing', () => {
    expect(reportBranchSegment('pr-3891')).toBe('pr-3891');
    expect(reportBranchSegment('refs/heads/main')).toBe('main');
  });
});

describe('repositoryDisplayName', () => {
  it('returns the repo slug', () => {
    expect(repositoryDisplayName('mattermost/desktop')).toBe('desktop');
  });
});

describe('buildConsolidatedReportPath', () => {
  it('includes gh_run_id query params', () => {
    expect(
      buildConsolidatedReportPath({
        repository: 'mattermost/desktop',
        branch: 'tsio-spike',
        commit: '29b47e7dcda38b98726f2abeafc4682bf945f440',
        name: 'desktop-pr',
        gh_pr_number: 3891,
        gh_run_id: '837585694163',
        gh_run_attempt: '1',
      }),
    ).toBe(
      '/reports/desktop/pr-3891/29b47e7/desktop-pr?gh_run_id=837585694163&gh_run_attempt=1',
    );
  });
});

describe('ensureRunQueryParams', () => {
  it('appends gh_run_id when missing', () => {
    expect(ensureRunQueryParams('/reports/desktop/main/abc/desktop-master', '99', '2')).toBe(
      '/reports/desktop/main/abc/desktop-master?gh_run_id=99&gh_run_attempt=2',
    );
  });
});

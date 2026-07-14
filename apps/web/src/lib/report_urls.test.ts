import { describe, expect, it } from 'vitest';
import {
  parsePRBranch,
  encodeBranchPathSegment,
  decodeBranchPathSegment,
  parseReportPathSplat,
  repositoryDisplayName,
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

describe('encodeBranchPathSegment', () => {
  it('replaces slashes with tilde for URL paths', () => {
    expect(encodeBranchPathSegment('feat/tsio-mobile-reporting')).toBe(
      'feat~tsio-mobile-reporting',
    );
    expect(encodeBranchPathSegment('refs/heads/release-2.40')).toBe('release-2.40');
  });
});

describe('parseReportPathSplat', () => {
  it('parses consolidated paths when branch contains slashes', () => {
    expect(parseReportPathSplat('feat/tsio-mobile-reporting/074d2d0/mobile-pr')).toEqual({
      mode: 'consolidated',
      branch: 'feat/tsio-mobile-reporting',
      commit: '074d2d0',
      name: 'mobile-pr',
    });
  });

  it('parses consolidated paths with tilde-encoded branch', () => {
    expect(parseReportPathSplat('feat~tsio-mobile-reporting/074d2d0/mobile-pr')).toEqual({
      mode: 'consolidated',
      branch: 'feat/tsio-mobile-reporting',
      commit: '074d2d0',
      name: 'mobile-pr',
    });
  });

  it('keeps the raw report name when percent-encoding is malformed', () => {
    expect(parseReportPathSplat('main/074d2d0/bad%name')).toEqual({
      mode: 'consolidated',
      branch: 'main',
      commit: '074d2d0',
      name: 'bad%name',
    });
  });
});

describe('buildConsolidatedReportPath', () => {
  it('builds path-only desktop PR URLs', () => {
    expect(
      buildConsolidatedReportPath({
        repository: 'mattermost/desktop',
        branch: 'tsio-spike',
        commit: 'cbe461edcda38b98726f2abeafc4682bf945f440',
        name: 'desktop-pr',
      }),
    ).toBe('/reports/desktop/tsio-spike/cbe461e/desktop-pr');
  });

  it('builds path-only mobile PR URLs with tilde branch encoding', () => {
    expect(
      buildConsolidatedReportPath({
        repository: 'mattermost/mattermost-mobile',
        branch: 'feat/tsio-mobile-reporting',
        commit: 'abc1234deadbeef',
        name: 'mobile-pr',
      }),
    ).toBe('/reports/mobile/feat~tsio-mobile-reporting/abc1234/mobile-pr');
  });
});

describe('repositoryDisplayName', () => {
  it('maps repo slugs to display tails', () => {
    expect(repositoryDisplayName('mattermost/desktop')).toBe('desktop');
    expect(repositoryDisplayName('mattermost/mattermost-mobile')).toBe('mobile');
  });
});

describe('parsePRBranch', () => {
  it('parses pr-N for metadata only', () => {
    expect(parsePRBranch('pr-3891')).toBe(3891);
  });
});

describe('ensureRunQueryParams', () => {
  it('appends gh_run_id when missing (legacy rows)', () => {
    expect(ensureRunQueryParams('/reports/desktop/main/abc/desktop-master', '99', '2')).toBe(
      '/reports/desktop/main/abc/desktop-master?gh_run_id=99&gh_run_attempt=2',
    );
  });
});

describe('decodeBranchPathSegment', () => {
  it('restores slashes from tilde encoding', () => {
    expect(decodeBranchPathSegment('feat~tsio-mobile-reporting')).toBe(
      'feat/tsio-mobile-reporting',
    );
  });
});

import { describe, expect, it } from 'vitest';
import {
  workerSlot,
  splitTrailingNumber,
  dedupeSuitesByReportAndPath,
  isFlakyTestSpec,
} from './utils';

describe('splitTrailingNumber', () => {
  it('splits a name ending in digits', () => {
    expect(splitTrailingNumber('orch-worker-3')).toEqual({ base: 'orch-worker', digits: '3' });
  });

  it('returns null when there is no trailing numeric suffix', () => {
    expect(splitTrailingNumber('e2e-on-ubuntu-latest')).toBeNull();
  });
});

describe('workerSlot', () => {
  it('returns the real slot when a sibling shares the same base label', () => {
    const names = ['orch-worker-1', 'orch-worker-2', 'orch-worker-3'];
    expect(workerSlot('orch-worker-2', 99, names)).toBe(2);
  });

  // Regression: e2e-on-macos-26 / e2e-on-windows-2022 have no sibling sharing
  // their base ("e2e-on-macos", "e2e-on-windows") — the trailing number is an
  // OS version, not a shard index, and must not be misreported as one.
  it('falls back when the trailing digits are not a shared shard index', () => {
    const names = ['e2e-on-ubuntu-latest', 'e2e-on-macos-26', 'e2e-on-windows-2022'];
    expect(workerSlot('e2e-on-macos-26', 42, names)).toBe(42);
    expect(workerSlot('e2e-on-windows-2022', 42, names)).toBe(42);
    expect(workerSlot('e2e-on-ubuntu-latest', 42, names)).toBe(42);
  });

  it('falls back when no siblings are provided at all', () => {
    expect(workerSlot('orch-worker-2', 7)).toBe(7);
  });

  it('falls back for a null/undefined name', () => {
    expect(workerSlot(null, 5, ['orch-worker-1'])).toBe(5);
    expect(workerSlot(undefined, 5, ['orch-worker-1'])).toBe(5);
  });

  it('does not match itself as its own sibling', () => {
    // A single-entry "group" of one, even if that one name repeats in the
    // array by reference equality, must not treat itself as a sibling.
    const names = ['e2e-on-macos-26'];
    expect(workerSlot('e2e-on-macos-26', 1, names)).toBe(1);
  });
});

describe('dedupeSuitesByReportAndPath', () => {
  // Regression: a real per-platform failure (ubuntu) was silently hidden
  // whenever a different platform's later-uploaded run of the identical
  // spec file (macos) passed — file_path-only dedup treated them as the
  // same suite and kept only the later, passing one.
  it('keeps failures from independent shards even when a later shard with the same file_path passes', () => {
    const suites = [
      {
        report_name: 'e2e-on-ubuntu-latest',
        file_path: 'user_attributes.test.ts',
        failed_count: 1,
      },
      { report_name: 'e2e-on-macos-26', file_path: 'user_attributes.test.ts', failed_count: 0 },
    ];
    const result = dedupeSuitesByReportAndPath(suites);
    expect(result).toHaveLength(2);
    expect(result).toEqual(expect.arrayContaining(suites));
  });

  // Regression: popout_windows.test.ts has file-level tests (one fails) AND a
  // `describe` block (all skipped) in the same file, so Playwright emits two
  // suite rows sharing file_path. A keep-latest strategy would let the later
  // describe clobber the file-level failure (hidden on the dashboard).
  // Merging sums the counts so the failure survives, and keeps the earliest
  // row's identity/start_time so the per-file row sorts where the file ran.
  it('merges a file-level suite and a later describe suite of the same shard, summing counts so a file-level failure is not hidden', () => {
    const suites = [
      {
        report_name: 'e2e-on-macos-26',
        file_path: 'popout_windows.test.ts',
        tests_count: 4,
        passed_count: 1,
        failed_count: 1,
        skipped_count: 2,
        duration_ms: 4000,
        start_time: '2026-07-08T11:27:44Z',
      },
      {
        report_name: 'e2e-on-macos-26',
        file_path: 'popout_windows.test.ts',
        tests_count: 2,
        passed_count: 0,
        failed_count: 0,
        skipped_count: 2,
        duration_ms: 0,
        start_time: '2026-07-08T11:27:59Z',
      },
    ];
    const result = dedupeSuitesByReportAndPath(suites);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({
      report_name: 'e2e-on-macos-26',
      file_path: 'popout_windows.test.ts',
      tests_count: 6,
      passed_count: 1,
      failed_count: 1,
      skipped_count: 4,
      duration_ms: 4000,
      start_time: '2026-07-08T11:27:44Z',
    });
  });

  it('passes through entries with no file_path unchanged', () => {
    const suites = [{ report_name: 'e2e-on-ubuntu-latest', file_path: undefined, failed_count: 0 }];
    expect(dedupeSuitesByReportAndPath(suites)).toEqual(suites);
  });
});

describe('isFlakyTestSpec', () => {
  it('treats Playwright ingest status "flaky" as flaky', () => {
    expect(
      isFlakyTestSpec(true, [
        { status: 'flaky' },
        { status: 'flaky' },
      ]),
    ).toBe(true);
  });

  it('treats failed-then-passed retries as flaky', () => {
    expect(
      isFlakyTestSpec(true, [
        { status: 'failed' },
        { status: 'passed' },
      ]),
    ).toBe(true);
  });

  it('does not mark a clean pass as flaky', () => {
    expect(isFlakyTestSpec(true, [{ status: 'passed' }])).toBe(false);
  });
});

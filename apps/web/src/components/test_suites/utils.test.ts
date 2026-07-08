import { describe, expect, it } from 'vitest';
import { workerSlot, splitTrailingNumber, dedupeSuitesByReportAndPath } from './utils';

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

  it('still dedupes true retries of the same shard, keeping the latest', () => {
    const suites = [
      { report_name: 'e2e-on-ubuntu-latest', file_path: 'flaky.test.ts', failed_count: 1 },
      { report_name: 'e2e-on-ubuntu-latest', file_path: 'flaky.test.ts', failed_count: 0 },
    ];
    const result = dedupeSuitesByReportAndPath(suites);
    expect(result).toEqual([suites[1]]);
  });

  it('passes through entries with no file_path unchanged', () => {
    const suites = [{ report_name: 'e2e-on-ubuntu-latest', file_path: undefined, failed_count: 0 }];
    expect(dedupeSuitesByReportAndPath(suites)).toEqual(suites);
  });
});

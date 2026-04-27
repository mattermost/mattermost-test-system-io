import type { ReportStats } from '@/types';

/**
 * Two filter families share this state on the Test Suites view:
 *   - `spec_*` filters apply at the suite-file level — driven by the
 *     title-bar chips, which match a suite only when its overall outcome
 *     equals the selected status.
 *   - `test_*` filters apply at the test-case level — driven by the
 *     right-side stat pills, which match a suite when it contains at
 *     least one test of the selected status.
 */
export type StatusFilter =
  | 'all'
  | 'spec_passed'
  | 'spec_failed'
  | 'test_passed'
  | 'test_failed'
  | 'test_flaky'
  | 'test_skipped';

export type StatVariant = 'default' | 'success' | 'error' | 'warning' | 'muted';

export interface StatPillProps {
  label: string;
  value: number;
  variant: StatVariant;
  isActive: boolean;
  onClick: () => void;
}

export interface ProgressBarProps {
  stats: ReportStats;
}

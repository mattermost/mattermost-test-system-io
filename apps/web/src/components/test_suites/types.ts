import type { ReportStats } from '@/types';

export type StatusFilter = 'all' | 'passed' | 'failed' | 'flaky' | 'skipped';

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

import { CheckCircle2, XCircle, Clock } from 'lucide-react';
import type { TestStats } from '../../types';

interface StatusIconProps {
  testStats?: TestStats;
}

export function StatusIcon({ testStats }: StatusIconProps) {
  if (!testStats) {
    return <Clock className="h-4 w-4 text-gray-400" />;
  }
  return testStats.failed === 0 ? (
    <CheckCircle2 className="h-4 w-4 text-green-500" />
  ) : (
    <XCircle className="h-4 w-4 text-red-500" />
  );
}

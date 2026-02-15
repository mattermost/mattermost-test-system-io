import { CheckCircle2, AlertCircle, Loader2 } from 'lucide-react';

interface JobsStatusIconProps {
  allComplete: boolean;
  isTimedOut: boolean;
}

export function JobsStatusIcon({ allComplete, isTimedOut }: JobsStatusIconProps) {
  if (allComplete) {
    return <CheckCircle2 className="h-3 w-3 flex-shrink-0 text-green-500" />;
  }
  if (isTimedOut) {
    return <AlertCircle className="h-3 w-3 flex-shrink-0 text-red-500" />;
  }
  return <Loader2 className="h-3 w-3 flex-shrink-0 text-blue-500 animate-spin" />;
}

interface JobsProgressProps {
  complete: number;
  expected: number;
  allComplete: boolean;
  isTimedOut: boolean;
  showLabel?: boolean;
}

export function JobsProgress({
  complete,
  expected,
  allComplete,
  isTimedOut,
  showLabel,
}: JobsProgressProps) {
  const colorClass = allComplete
    ? 'text-green-600 dark:text-green-400'
    : isTimedOut
      ? 'text-red-600 dark:text-red-400'
      : '';

  return (
    <span className={colorClass}>
      {complete}/{expected}
      {showLabel && <span className="hidden lg:inline"> {expected === 1 ? 'job' : 'jobs'}</span>}
    </span>
  );
}

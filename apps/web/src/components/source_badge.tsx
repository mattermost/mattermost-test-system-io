import { GitCommit } from 'lucide-react';

interface SourceBadgeProps {
  commit_sha: string;
  run_attempt: number;
}

/** Per-spec source badge — shown only when result is NOT from the latest commit/attempt. */
export function SourceBadge({ commit_sha, run_attempt }: SourceBadgeProps) {
  const short_sha = commit_sha.length >= 7 ? commit_sha.slice(0, 7) : commit_sha;

  return (
    <span className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-mono text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
      <GitCommit className="h-3 w-3" />
      {short_sha} #{run_attempt}
    </span>
  );
}

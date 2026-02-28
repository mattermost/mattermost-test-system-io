import { GitBranch, GitCommit, GitPullRequest, FolderGit2 } from 'lucide-react';

interface GitMetadataFields {
  repository: string;
  branch: string;
  commit: string;
  pr_number?: number;
}

interface GitMetadataMobileProps {
  report: GitMetadataFields;
}

export function GitMetadataMobile({ report }: GitMetadataMobileProps) {
  return (
    <div className="flex flex-col items-center gap-0.5 text-xs">
      {report.repository && (
        <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
          <FolderGit2 className="hidden min-[480px]:inline h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
          <span className="truncate max-w-[80px] min-[480px]:max-w-[100px]">
            {report.repository.split('/').pop()}
          </span>
        </span>
      )}
      {report.pr_number && (
        <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
          <GitPullRequest className="hidden min-[480px]:inline h-3 w-3" />#{report.pr_number}
        </span>
      )}
    </div>
  );
}

interface GitMetadataDesktopProps {
  report: GitMetadataFields;
}

export function GitMetadataDesktop({ report }: GitMetadataDesktopProps) {
  const hasRepoOrPr = report.repository || report.pr_number;
  const hasBranchOrCommit = report.branch || report.commit;

  if (!hasRepoOrPr && !hasBranchOrCommit) return null;

  return (
    <>
      {/* sm to md: inline layout */}
      <div className="flex md:hidden flex-wrap items-center gap-x-2 gap-y-0.5 text-xs min-w-0">
        {report.repository && (
          <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
            <FolderGit2 className="h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
            <span className="truncate">{report.repository.split('/').pop()}</span>
          </span>
        )}
        {report.pr_number && (
          <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
            <GitPullRequest className="h-3 w-3" />#{report.pr_number}
          </span>
        )}
      </div>
      {/* md+: two stacks side by side (Repo+PR | Branch+Commit) */}
      <div className="hidden md:flex items-start gap-3 text-xs min-w-0 overflow-hidden">
        {/* Repo + PR stack */}
        {hasRepoOrPr && (
          <div className="flex flex-col gap-0.5 min-w-0 overflow-hidden lg:w-48">
            {report.repository && (
              <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
                <FolderGit2 className="h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
                <span className="truncate">{report.repository}</span>
              </span>
            )}
            {report.pr_number && (
              <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300 w-fit">
                <GitPullRequest className="h-3 w-3" />#{report.pr_number}
              </span>
            )}
          </div>
        )}
        {/* Branch + Commit stack */}
        {hasBranchOrCommit && (
          <div className="flex flex-col gap-0.5 flex-shrink-0">
            {report.branch && (
              <span className="inline-flex items-center gap-1 rounded-md bg-blue-50 px-1.5 py-0.5 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300 min-w-0">
                <GitBranch className="h-3 w-3 flex-shrink-0" />
                <span className="truncate max-w-24">{report.branch}</span>
              </span>
            )}
            {report.commit && (
              <span className="hidden lg:inline-flex items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-gray-600 dark:bg-gray-700 dark:text-gray-300">
                <GitCommit className="h-3 w-3" />
                {report.commit.slice(0, 7)}
              </span>
            )}
          </div>
        )}
      </div>
    </>
  );
}

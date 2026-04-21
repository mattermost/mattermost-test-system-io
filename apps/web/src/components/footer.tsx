import { useServerInfo } from '@/services/api';

// Inline GitHub mark. lucide-react dropped brand icons at v1, so we embed the
// one we actually need rather than pull a brand-icon dep for a single glyph.
function GithubIcon({ className }: { className?: string }) {
  return (
    <svg
      className={className}
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 .5C5.73.5.67 5.56.67 11.83c0 5.01 3.24 9.24 7.74 10.74.57.1.78-.25.78-.55 0-.27-.01-1-.02-1.96-3.15.68-3.81-1.52-3.81-1.52-.52-1.31-1.26-1.66-1.26-1.66-1.03-.7.08-.69.08-.69 1.14.08 1.74 1.17 1.74 1.17 1.01 1.73 2.65 1.23 3.3.94.1-.73.4-1.23.72-1.51-2.52-.29-5.18-1.26-5.18-5.61 0-1.24.44-2.25 1.17-3.05-.12-.29-.51-1.44.11-3 0 0 .96-.31 3.15 1.17.91-.25 1.89-.38 2.86-.39.97.01 1.95.14 2.86.39 2.19-1.48 3.15-1.17 3.15-1.17.62 1.56.23 2.71.11 3 .73.8 1.17 1.81 1.17 3.05 0 4.36-2.66 5.32-5.2 5.6.41.35.77 1.03.77 2.08 0 1.5-.01 2.71-.01 3.08 0 .3.2.66.79.55 4.49-1.5 7.73-5.73 7.73-10.74C23.33 5.56 18.27.5 12 .5z" />
    </svg>
  );
}

export function Footer() {
  const { data: info } = useServerInfo();

  if (!info) {
    return null;
  }

  const { server_version, environment, repo_url, commit_sha, build_time } = info;

  const shortSha = commit_sha ? commit_sha.slice(0, 7) : '';

  return (
    <footer className="border-t border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
      <div className="mx-auto max-w-7xl px-4 py-3 sm:px-6 lg:px-8">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>v{server_version}</span>
            {shortSha && (
              <span title={commit_sha}>
                {repo_url ? (
                  <a
                    href={`${repo_url}/commit/${commit_sha}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="hover:text-gray-700 hover:underline dark:hover:text-gray-300"
                  >
                    {shortSha}
                  </a>
                ) : (
                  shortSha
                )}
              </span>
            )}
            <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-gray-800 dark:text-gray-300">
              {environment}
            </span>
            {build_time && <span>{build_time}</span>}
          </div>
          {repo_url && (
            <a
              href={repo_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 hover:text-gray-700 dark:hover:text-gray-300"
            >
              <GithubIcon className="h-3.5 w-3.5" />
              <span className="hover:underline">{repo_url.replace('https://github.com/', '')}</span>
            </a>
          )}
        </div>
      </div>
    </footer>
  );
}

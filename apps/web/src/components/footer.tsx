import { Github } from 'lucide-react';
import { useServerInfo } from '@/services/api';

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
              <Github className="h-3.5 w-3.5" />
              <span className="hover:underline">{repo_url.replace('https://github.com/', '')}</span>
            </a>
          )}
        </div>
      </div>
    </footer>
  );
}

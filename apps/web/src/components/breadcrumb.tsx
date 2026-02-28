import { ChevronRight } from 'lucide-react';
import { Link } from 'react-router-dom';

interface BreadcrumbProps {
  repo: string;
  target_name: string;
  commit_sha: string;
  tool_name: string;
}

export function Breadcrumb({ repo, target_name, commit_sha, tool_name }: BreadcrumbProps) {
  const items = [
    { label: 'Reports', to: '/reports' },
    { label: repo },
    { label: target_name },
    { label: commit_sha },
    { label: tool_name },
  ];

  return (
    <nav className="flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400">
      {items.map((item, i) => (
        <span key={i} className="flex items-center gap-1">
          {i > 0 && <ChevronRight className="h-3 w-3" />}
          {item.to ? (
            <Link to={item.to} className="text-blue-600 hover:underline dark:text-blue-400">
              {item.label}
            </Link>
          ) : (
            <span className="font-medium text-gray-700 dark:text-gray-300">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}

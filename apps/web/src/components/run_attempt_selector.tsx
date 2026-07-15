import { useSearchParams } from 'react-router-dom';

interface RunAttemptSelectorProps {
  available_attempts: number[];
  current_attempt?: number;
}

export function RunAttemptSelector({
  available_attempts,
  current_attempt,
}: RunAttemptSelectorProps) {
  const [, set_search_params] = useSearchParams();

  if (available_attempts.length <= 1) {
    return null;
  }

  function handle_select(attempt: number | undefined) {
    set_search_params((prev) => {
      const next = new URLSearchParams(prev);
      if (attempt === undefined) {
        next.delete('run_attempt');
      } else {
        next.set('run_attempt', String(attempt));
      }
      return next;
    });
  }

  return (
    <div className="flex items-center gap-2 text-sm">
      <span className="text-gray-500 dark:text-gray-400">Run attempt:</span>
      <div className="flex gap-1">
        <button
          type="button"
          onClick={() => handle_select(undefined)}
          className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
            current_attempt === undefined
              ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300'
              : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-400 dark:hover:bg-gray-600'
          }`}
        >
          Latest
        </button>
        {available_attempts.map((attempt) => (
          <button
            key={attempt}
            type="button"
            onClick={() => handle_select(attempt)}
            className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
              current_attempt === attempt
                ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300'
                : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-400 dark:hover:bg-gray-600'
            }`}
          >
            #{attempt}
          </button>
        ))}
      </div>
    </div>
  );
}

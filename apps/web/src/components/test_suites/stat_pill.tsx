import type { StatPillProps, StatVariant } from './types';

const variants: Record<StatVariant, string> = {
  default: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200',
  success: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-400',
  error: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400',
  warning: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-400',
  muted: 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400',
};

export function StatPill({ label, value, variant, isActive, onClick }: StatPillProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors ${variants[variant]} ${
        isActive
          ? 'ring-1 ring-blue-500 ring-offset-1 dark:ring-offset-gray-800'
          : 'hover:opacity-80'
      }`}
    >
      <span className="font-semibold">{value}</span>
      <span className="opacity-70">{label}</span>
    </button>
  );
}

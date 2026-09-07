/** Live run indicator. */
export function LivePulseDot({ className = '' }: { className?: string }) {
  return (
    <span
      className={`relative inline-flex h-5 w-5 items-center justify-center ${className}`}
      aria-hidden="true"
    >
      <span className="absolute h-3 w-3 rounded-full bg-blue-500 opacity-75 animate-ping dark:bg-blue-400" />
      <span className="relative h-2.5 w-2.5 rounded-full bg-blue-500 dark:bg-blue-400" />
    </span>
  );
}

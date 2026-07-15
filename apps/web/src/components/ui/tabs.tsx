/**
 * Lightweight tabs primitive (no third-party dependency).
 *
 * Mirrors the shadcn/Radix Tabs surface enough for our needs: a controlled
 * `value` + `onValueChange` pair, a `TabsList` with `TabsTrigger` buttons, and
 * `TabsContent` panels that render only when active. Keeps semantics simple
 * (no roving focus / arrow-key cycling) since the consumers only have two
 * tabs and rely on URL-driven state.
 */

import { createContext, useContext, type ReactNode } from 'react';
import { cn } from '@/lib/utils';

interface TabsContextValue {
  value: string;
  onValueChange: (value: string) => void;
}

const TabsContext = createContext<TabsContextValue | null>(null);

function useTabsContext(component: string): TabsContextValue {
  const ctx = useContext(TabsContext);
  if (!ctx) {
    throw new Error(`<${component}> must be rendered inside <Tabs>`);
  }
  return ctx;
}

interface TabsProps {
  value: string;
  onValueChange: (value: string) => void;
  className?: string;
  children: ReactNode;
}

export function Tabs({ value, onValueChange, className, children }: TabsProps) {
  return (
    <TabsContext.Provider value={{ value, onValueChange }}>
      <div className={cn('w-full', className)}>{children}</div>
    </TabsContext.Provider>
  );
}

interface TabsListProps {
  className?: string;
  children: ReactNode;
}

export function TabsList({ className, children }: TabsListProps) {
  return (
    <div
      role="tablist"
      className={cn(
        'inline-flex items-center gap-1 border-b border-gray-200 dark:border-gray-700',
        className,
      )}
    >
      {children}
    </div>
  );
}

interface TabsTriggerProps {
  value: string;
  className?: string;
  children: ReactNode;
}

export function TabsTrigger({ value, className, children }: TabsTriggerProps) {
  const { value: active, onValueChange } = useTabsContext('TabsTrigger');
  const isActive = active === value;
  return (
    <button
      type="button"
      role="tab"
      aria-selected={isActive}
      onClick={() => onValueChange(value)}
      className={cn(
        'inline-flex items-center gap-2 px-4 py-2 -mb-px text-sm font-medium transition-colors border-b-2',
        isActive
          ? 'border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-300'
          : 'border-transparent text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200',
        className,
      )}
    >
      {children}
    </button>
  );
}

interface TabsContentProps {
  value: string;
  className?: string;
  children: ReactNode;
}

export function TabsContent({ value, className, children }: TabsContentProps) {
  const { value: active } = useTabsContext('TabsContent');
  if (active !== value) return null;
  return (
    <div role="tabpanel" className={cn('pt-4', className)}>
      {children}
    </div>
  );
}

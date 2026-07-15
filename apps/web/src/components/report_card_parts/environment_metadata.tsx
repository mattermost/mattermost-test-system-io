import { useState } from 'react';
import { ChevronDown, ChevronRight, Server, Wrench } from 'lucide-react';
import type { ReportEnvironmentMetadata } from '@/types';

interface MetadataSectionProps {
  title: string;
  icon: React.ReactNode;
  data: Record<string, unknown>;
  color: string;
}

function format_value(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return JSON.stringify(value);
}

function MetadataSection({ title, icon, data, color }: MetadataSectionProps) {
  const [expanded, set_expanded] = useState(false);
  const entries = Object.entries(data).filter(([, v]) => v !== null && v !== undefined);

  if (entries.length === 0) return null;

  // Build a short summary from the first 2-3 key values
  const summary = entries
    .slice(0, 3)
    .map(([, v]) => format_value(v))
    .filter(Boolean)
    .join(' · ');

  return (
    <div>
      <button
        type="button"
        onClick={() => set_expanded(!expanded)}
        className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs ${color}`}
      >
        {expanded ? (
          <ChevronDown className="h-3 w-3 flex-shrink-0" />
        ) : (
          <ChevronRight className="h-3 w-3 flex-shrink-0" />
        )}
        {icon}
        <span className="font-medium">{title}</span>
        {!expanded && <span className="truncate text-gray-500 dark:text-gray-400">{summary}</span>}
      </button>

      {expanded && (
        <dl className="ml-8 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 pb-2 text-xs">
          {entries.map(([key, value]) => (
            <div key={key} className="contents">
              <dt className="text-gray-500 dark:text-gray-400">{key}</dt>
              <dd className="font-mono text-gray-700 dark:text-gray-300">{format_value(value)}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

interface EnvironmentMetadataDisplayProps {
  metadata: ReportEnvironmentMetadata;
}

export function EnvironmentMetadataDisplay({ metadata }: EnvironmentMetadataDisplayProps) {
  const has_tool = metadata.tool && Object.keys(metadata.tool).length > 0;
  const has_server = metadata.server && Object.keys(metadata.server).length > 0;

  if (!has_tool && !has_server) return null;

  return (
    <div className="rounded-lg border border-gray-200 bg-gray-50/50 dark:border-gray-700 dark:bg-gray-800/30">
      {has_tool && (
        <MetadataSection
          title="Tool"
          icon={<Wrench className="h-3 w-3" />}
          data={metadata.tool as Record<string, unknown>}
          color="text-blue-700 dark:text-blue-400"
        />
      )}
      {has_server && (
        <MetadataSection
          title="Server"
          icon={<Server className="h-3 w-3" />}
          data={metadata.server as Record<string, unknown>}
          color="text-violet-700 dark:text-violet-400"
        />
      )}
    </div>
  );
}

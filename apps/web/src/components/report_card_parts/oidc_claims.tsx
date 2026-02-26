import {
  ChevronDown,
  ChevronRight,
  Shield,
  GitBranch,
  GitCommit,
  User,
  Workflow,
  Play,
} from 'lucide-react';
import { useState } from 'react';
import type { ReportOidcClaims } from '@/types';

interface OidcClaimsProps {
  claims: ReportOidcClaims;
}

/** Extract a short branch/tag name from a full ref. */
function short_ref(ref_value?: string): string | undefined {
  if (!ref_value) return undefined;
  return ref_value.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
}

/** Display OIDC provenance claims for a report. */
export function OidcClaimsSection({ claims }: OidcClaimsProps) {
  const [expanded, set_expanded] = useState(false);
  const branch = short_ref(claims.ref);

  return (
    <div className="rounded-lg border border-emerald-200 bg-emerald-50/50 dark:border-emerald-800 dark:bg-emerald-950/30">
      <button
        type="button"
        onClick={() => set_expanded(!expanded)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium text-emerald-800 dark:text-emerald-300"
      >
        <Shield className="h-4 w-4 flex-shrink-0" />
        <span>OIDC Provenance</span>
        <span className="ml-1 rounded bg-emerald-200 px-1.5 py-0.5 text-xs dark:bg-emerald-800">
          {claims.resolved_role}
        </span>
        {expanded ? (
          <ChevronDown className="ml-auto h-4 w-4" />
        ) : (
          <ChevronRight className="ml-auto h-4 w-4" />
        )}
      </button>

      {!expanded && (
        <div className="flex flex-wrap items-center gap-2 px-3 pb-2 text-xs text-emerald-700 dark:text-emerald-400">
          {claims.repository && (
            <span className="inline-flex items-center gap-1">
              <GitBranch className="h-3 w-3" />
              {claims.repository}
            </span>
          )}
          {claims.actor && (
            <span className="inline-flex items-center gap-1">
              <User className="h-3 w-3" />
              {claims.actor}
            </span>
          )}
          {claims.workflow && (
            <span className="inline-flex items-center gap-1">
              <Workflow className="h-3 w-3" />
              {claims.workflow}
            </span>
          )}
        </div>
      )}

      {expanded && (
        <div className="border-t border-emerald-200 px-3 py-2 dark:border-emerald-800">
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            {claims.repository && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Repository</dt>
                <dd className="font-mono text-gray-800 dark:text-gray-200">{claims.repository}</dd>
              </>
            )}
            {claims.actor && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Actor</dt>
                <dd className="font-mono text-gray-800 dark:text-gray-200">{claims.actor}</dd>
              </>
            )}
            {branch && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Branch</dt>
                <dd className="inline-flex items-center gap-1 font-mono text-gray-800 dark:text-gray-200">
                  <GitBranch className="h-3 w-3 text-emerald-500" />
                  {branch}
                </dd>
              </>
            )}
            {claims.sha && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Commit</dt>
                <dd className="inline-flex items-center gap-1 font-mono text-gray-800 dark:text-gray-200">
                  <GitCommit className="h-3 w-3 text-emerald-500" />
                  {claims.sha.slice(0, 7)}
                </dd>
              </>
            )}
            {claims.workflow && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Workflow</dt>
                <dd className="inline-flex items-center gap-1 font-mono text-gray-800 dark:text-gray-200">
                  <Workflow className="h-3 w-3 text-emerald-500" />
                  {claims.workflow}
                </dd>
              </>
            )}
            {claims.event_name && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Event</dt>
                <dd className="inline-flex items-center gap-1 font-mono text-gray-800 dark:text-gray-200">
                  <Play className="h-3 w-3 text-emerald-500" />
                  {claims.event_name}
                </dd>
              </>
            )}
            {claims.run_id && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">Run</dt>
                <dd className="font-mono text-gray-800 dark:text-gray-200">
                  #{claims.run_number || claims.run_id}
                  {claims.run_attempt && claims.run_attempt !== '1' && (
                    <span className="ml-1 text-gray-500">(attempt {claims.run_attempt})</span>
                  )}
                </dd>
              </>
            )}
            {claims.head_ref && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">PR Head</dt>
                <dd className="font-mono text-gray-800 dark:text-gray-200">{claims.head_ref}</dd>
              </>
            )}
            {claims.base_ref && (
              <>
                <dt className="text-emerald-600 dark:text-emerald-500">PR Base</dt>
                <dd className="font-mono text-gray-800 dark:text-gray-200">{claims.base_ref}</dd>
              </>
            )}
          </dl>
        </div>
      )}
    </div>
  );
}

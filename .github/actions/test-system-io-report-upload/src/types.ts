/**
 * Wire types for the report-upload action. Subset of the types used by
 * the orchestrated worker — only what /reports/begin, /reports/register,
 * and /reports/upload need.
 */

export interface CompositeIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  name: string;
  branch?: string;
  gh_pr_number?: number | string;
}

export interface ReportsBeginBody {
  repository: string;
  commit: string;
  gh_run_id: string;
  gh_run_attempt: string;
  framework: string;
  name: string;
  branch?: string;
  gh_pr_number?: number | string;
  total_reports_expected: number;
}

export interface ReportsBeginResponseBody {
  report_id: string;
}

export interface ReportsRegisterResponseBody {
  upload_id: string;
}

export interface UploadPart {
  absPath: string;
  relPath: string;
  size: number;
  contentType?: string;
}

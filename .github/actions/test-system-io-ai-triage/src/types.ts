export interface CompositeIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  name: string;
  branch?: string;
  gh_pr_number?: number | string;
}

export interface HistorySummary {
  runs: number;
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  flips: number;
  failure_rate: number;
  flake_rate: number;
  last_pass_commit?: string;
  failing_since_commit?: string;
  series: string[];
}

export interface Amnesty {
  granted: boolean;
  reason: string;
}

export interface Suggestion {
  verdict: string;
  confidence: number;
  needs_ai: boolean;
  reason: string;
  citations: string[];
}

export interface Screenshot {
  s3_key: string;
  screenshot_type?: string;
  url: string;
}

export interface EvidenceFailure {
  external_test_id?: string;
  full_title: string;
  title: string;
  file?: string;
  status: string;
  retry_count: number;
  duration_ms: number;
  error_message?: string;
  error_stack?: string;
  screenshots: Screenshot[];
  history?: HistorySummary;
  history_error?: string;
  distinct_prs?: number;
  distinct_branches?: number;
  amnesty?: Amnesty;
  suggested: Suggestion;
}

export interface EvidenceGroup {
  id: string;
  repository: string;
  branch: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  gh_pr_number?: number;
  framework: string;
  name: string;
  status: string;
}

export interface EvidencePack {
  group: EvidenceGroup;
  failures: EvidenceFailure[];
  truncated: boolean;
  lookups: number;
  max_lookups: number;
}

export interface Decision {
  verdict: string;
  confidence: number;
  reason: string;
  citations: string[];
  waived: boolean;
  source: "history" | "model" | "policy";
  check_state: "success" | "failure";
}

export interface ClaudeVerdict {
  verdict: string;
  confidence: number;
  reason: string;
  citations: string[];
}

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
  /** W9 — captured run-config keys that differ from the last passing run for this test. */
  config_delta?: string[];
  /** R7-B — baseline vs this-PR failure rate for this test; drives the rate-shift gate. */
  rate_shift?: RateShift;
  /** R7-L3 — the live quarantine for this test, if any. Only ever set on PR runs. */
  quarantine?: Quarantine;
}

/**
 * R7-L3 — an explicit, owned, expiring quarantine.
 *
 * The server only includes this while it is live, and computes `active` itself
 * so the action never re-derives the expiry rule. Branch on `active` and
 * nothing else.
 */
export interface Quarantine {
  id: string;
  external_test_id: string;
  owner: string;
  reason: string;
  created_by: string;
  expires_at: string;
  active: boolean;
  days_remaining: number;
  applied_count: number;
}

/**
 * R7-B — the baseline-vs-current failure rate comparison for one test.
 *
 * `ok` false means the comparison was not computable (no PR context, baseline
 * too small, too few PR runs). That is NOT evidence of no shift: an absent
 * signal must only ever decline to refuse a waiver, never justify one.
 */
export interface RateShift {
  ok: boolean;
  baseline_runs: number;
  baseline_failed: number;
  baseline_rate: number;
  pr_runs: number;
  pr_failed: number;
  pr_rate: number;
  /** P(X >= pr_failed | n=pr_runs, p=baseline_rate). */
  p_value: number;
  /** The gate's answer: the rate rose by more than baseline flakiness explains. */
  shifted: boolean;
  alpha: number;
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
  /** W9 — captured run configuration (flags, edition, env); see comment on report group. */
  environment_metadata?: Record<string, unknown>;
}

export interface EvidenceCluster {
  signature: string;
  label: string;
  member_count: number;
  members: Array<{ external_test_id?: string; full_title: string; status: string }>;
  representative: EvidenceFailure;
  suggested: Suggestion;
}

export interface EvidencePack {
  group: EvidenceGroup;
  failure_count: number;
  cluster_count: number;
  clusters: EvidenceCluster[];
  truncated: boolean;
  lookups: number;
  max_lookups: number;
}

export interface Decision {
  verdict: string;
  confidence: number;
  reason: string;
  /** One plain-language sentence (≤120 chars) a human reads in the PR comment. */
  gist?: string;
  citations: string[];
  waived: boolean;
  source: "history" | "model" | "policy";
  check_state: "success" | "failure";
  kind: "flaky" | "bug" | "unknown";
  member_count: number;
  suspect_sha?: string;
  suspect_author?: string;
  chronic?: boolean;
  /** Waived at near-minimum confidence — flagged for human review. */
  borderline?: boolean;
  /**
   * Product deliberately refused the action (error text or model-observed
   * screenshot evidence). Blocks flake waivers; the fix belongs in the test.
   */
  refusal?: boolean;
}

export interface ClaudeVerdict {
  verdict: string;
  confidence: number;
  reason: string;
  /** One plain-language sentence (≤120 chars) a human reads in the PR comment. */
  gist?: string;
  citations: string[];
  suspect_sha?: string;
  suspect_author?: string;
  /** True when baseline history shows this test flaking repeatedly. */
  chronic?: boolean;
  /**
   * True when the screenshot/error shows the product deliberately refusing
   * the action (rejection banner, permission dialog). Blocks flake waivers.
   */
  product_refusal?: boolean;
}


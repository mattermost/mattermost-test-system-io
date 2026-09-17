export type TriageClass = 'healthy' | 'flaky' | 'broken' | 'unstable' | 'unknown' | 'retired';
/** One `test_health` row as returned by `GET /triage/tests/{id}` (no identity join). */
export interface TriageHealthRow {
  identity_id: string;
  lane: string;
  base_ref: string;
  classification: TriageClass;
  window_runs: number;
  pass_count: number;
  fail_count: number;
  flaky_count: number;
  instability_rate: number;
  consecutive_fails: number;
  computed_at: string;
}
/** `GET /triage/health` rows: a health row joined with its identity and recent history. */
export interface TriageHealth extends TriageHealthRow {
  repository: string;
  framework: string;
  file: string;
  full_title: string;
  mm_t_id: string;
  history?: string[];
}
export interface TriageIdentity {
  id: string;
  repository: string;
  framework: string;
  file: string;
  full_title: string;
  mm_t_id: string | null;
  first_seen_at: string;
  last_seen_at: string;
}
export interface Quarantine {
  id: string;
  identity_id: string;
  lane: string | null;
  base_ref: string;
  source: string;
  reason: string;
  status: string;
  issue_url: string | null;
  created_at: string;
  expires_at: string | null;
}
export interface Observation {
  id: string;
  identity_id: string;
  report_group_id: string;
  status: string;
  branch_kind: string;
  branch: string;
  lane: string;
  commit_sha: string;
  observed_at: string;
  retry_count: number;
  error_excerpt?: string;
  failure_locus?: string;
  is_infra_stub: boolean;
}
export interface TriageFinding {
  identity_id: string;
  file: string;
  full_title: string;
  lane: string;
  class: string;
  blocking: boolean;
  reason: string;
  trunk: {
    runs: number;
    fails: number;
    flaky: number;
    instability_rate: number;
    consecutive_fails: number;
  };
  pr: {
    fail_count: number;
    error_excerpt: string;
    failure_locus: string;
    signature_match: boolean;
  };
  links: {
    tsio_case_url: string;
    tsio_history_url: string;
    quarantine_id?: string;
    issue_url?: string;
  };
}
export interface TriageVerdict {
  id: string;
  report_group_id: string;
  mode: 'off' | 'shadow' | 'enforce';
  verdict: string;
  reason?: string;
  confidence: number;
  computed_at: string;
  engine_version: string;
  counts: {
    passed: number;
    failed: number;
    flaky: number;
    skipped: number;
    exonerated: number;
    blocking: number;
    infra: number;
  };
  findings: TriageFinding[];
  thresholds_used: Record<string, number | string>;
  human_override?: { actor: string; label: string; at: string; resulting_state: string };
}
export interface CursorPage<T> {
  items: T[];
  next_cursor: string;
}

/**
 * TypeScript types for the Test Shard Orchestration API.
 *
 * Mirrors the canonical OpenAPI definitions in apps/server/api/openapi.yaml.
 *
 * The composite identity tuple
 *   (repository, commit_sha, gh_run_id, name, gh_run_attempt)
 * addresses every orchestration resource on the API. The server's internal
 * UUIDs (run_id, lease_id) are not part of the worker-facing contract and
 * remain optional in these shapes.
 */

// ─── Identity ──────────────────────────────────────────────────────────────

export interface CompositeIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  name: string;
  /** Defaults to "1" on the server when omitted. */
  gh_run_attempt?: string;
  /** Test framework label. Server accepts "playwright" or "cypress". */
  framework?: 'playwright' | 'cypress';
  branch?: string;
  gh_pr_number?: number;
}

export interface WorkerIdentity {
  gh_job_name: string;
  gh_job_id: string;
}

// ─── Status & state enums ──────────────────────────────────────────────────

export type RunStatus = 'in_progress' | 'completed' | 'timed_out';

export type UnitState =
  | 'pending'
  | 'leased'
  | 'completed_pass'
  | 'completed_fail'
  | 'completed_skipped'
  | 'abandoned';

export type TestCaseStatus = 'passed' | 'failed' | 'skipped' | 'flaky' | 'timedOut' | 'interrupted';

// ─── Run snapshot (GET /orchestration/status response) ─────────────────────

export interface RunCounts {
  pending: number;
  leased: number;
  completed_pass: number;
  completed_fail: number;
  completed_skipped: number;
  abandoned: number;
  /**
   * Subset of completed_fail still within retest budget. The run is
   * `completed` only when this is zero alongside pending and leased.
   */
  retest_eligible?: number;
  /** Convenience field; not in the OpenAPI schema but commonly derived. */
  total?: number;
}

export interface RunSnapshot extends CompositeIdentity {
  status: RunStatus;
  total_units: number;
  started_at: string;
  /**
   * Bumped to now() on every successful checkout / complete. Combined
   * with `idle_timeout_ms` by the server-side reaper to decide when an
   * in-progress run transitions to `timed_out`.
   */
  last_activity_at: string;
  /** Inactivity window in milliseconds; run is reaped when no activity within this window. */
  idle_timeout_ms: number;
  terminal_at?: string | null;
  counts: RunCounts;
  /**
   * Per-dispatch-unit state, ordered by dispatch_seq. Each unit addresses
   * exactly one spec file. Returned by /begin (all pending) and /status.
   * Optional only for forward-compat with older server versions that did
   * not yet emit it.
   */
  units?: SnapshotUnit[];
}

/**
 * Per-spec attempt in the run-status payload, denormalized with the worker
 * name from its parent lease so the dashboard can render "spec X was run
 * by worker Y" without a per-row lookup. `test_cases` carries the
 * framework-specific per-test-case detail the worker supplied on /complete,
 * including attachments (screenshots, traces, etc.) that the dashboard
 * resolves through /files/{key}.
 */
export interface SnapshotAttempt {
  id: string;
  lease_id: string;
  spec_path: string;
  status: TestCaseStatus | null;
  actual_duration_ms: number | null;
  error_message: string | null;
  reported_at: string | null;
  late_report: boolean;
  expired: boolean;
  created_at: string;
  gh_job_name: string;
  gh_job_id: string;
  test_cases: SnapshotTestCase[] | null;
}

/**
 * Subset of SpecResultTestCase that the dashboard renders. Not every field
 * the worker can send is needed here — only the bits the per-attempt UI
 * surfaces. Extra unmapped fields are tolerated by TypeScript at runtime.
 */
export interface SnapshotTestCase {
  title: string;
  full_title: string;
  status: TestCaseStatus;
  retry_count?: number;
  duration_ms?: number | null;
  ordinal?: number;
  error_message?: string | null;
  error_stack?: string | null;
  attachments?: SnapshotTestCaseAttachments | null;
}

/**
 * The worker's `/complete` request rewrites local image paths into
 * { screenshots: [{ key, relative_path }] } before sending. The dashboard
 * resolves each `key` via /files/{key} (the public download path used by
 * Report Group views).
 */
export interface SnapshotTestCaseAttachments {
  screenshots?: Array<{ key: string; relative_path?: string | null }>;
}

/** Minimal lease info shown while a unit is in the leased state. */
export interface SnapshotCurrentLease {
  id: string;
  gh_job_name: string;
  gh_job_id: string;
  issued_at: string;
  deadline: string;
}

/**
 * One dispatch_units row in the run-status payload. Each unit addresses a
 * single spec file. `current_lease` is populated only while
 * `state === 'leased'`. `attempts` is oldest-first.
 */
export interface SnapshotUnit {
  id: string;
  dispatch_seq: number;
  spec_path: string;
  state: UnitState;
  lease_count: number;
  fail_count: number;
  outcome_set_at: string | null;
  current_lease: SnapshotCurrentLease | null;
  attempts: SnapshotAttempt[];
}

// ─── Per-unit / per-lease / per-attempt detail ─────────────────────────────

export interface OrchestrationUnit {
  unit_id: string;
  dispatch_seq: number;
  spec_path: string;
  state: UnitState;
  lease_count: number;
  fail_count: number;
}

export interface OrchestrationLease {
  /** Internal UUID; surfaced only on the UI's run-detail view, not over the worker API. */
  lease_id?: string;
  gh_job_name: string;
  gh_job_id: string;
  issued_at: string;
  deadline: string;
  released_at?: string | null;
  release_reason?: 'completed' | 'expired' | 'run_timed_out' | null;
}

export interface TestCaseDetail {
  title: string;
  full_title: string;
  status: TestCaseStatus;
  retry_count: number;
  duration_ms?: number | null;
  error_message?: string | null;
  error_stack?: string | null;
  annotations?: unknown[];
  attachments?: Record<string, unknown> | null;
  ordinal: number;
}

export interface OrchestrationAttempt {
  /** Internal UUID; FK back to OrchestrationLease. */
  lease_id: string;
  gh_job_name: string;
  gh_job_id: string;
  /**
   * The spec the attempt covers. Each row in the `attempts` table is keyed
   * by `(lease_id, spec_path)`; the worker contract sends one attempt row
   * per spec_path in its lease.
   */
  spec_path?: string;
  deadline: string;
  expired: boolean;
  late_report: boolean;
  /**
   * True when this attempt was issued as a retest dispatch (i.e. its parent
   * unit had already failed at least once). Mirrors the `is_retest` flag the
   * worker receives on /checkout and the corresponding event payload.
   */
  is_retest?: boolean;
  /**
   * Total accumulated failures on this attempt's dispatch unit at the time
   * the attempt was created. Surfaced primarily on retest attempts where
   * the orchestrator returns the prior fail_count alongside the dispatch.
   */
  fail_count?: number;
  status: TestCaseStatus | null;
  actual_duration_ms?: number | null;
  error_message?: string | null;
  error_stack?: string | null;
  test_cases?: TestCaseDetail[];
  reported_at?: string | null;
}

// ─── Cross-source divergence ───────────────────────────────────────────────

/**
 * A per-spec disagreement between the orchestration-recorded outcome and
 * the canonical artifact-derived outcome (consolidated test_cases). The UI
 * surfaces these so reviewers can immediately spot specs where the two
 * sources of truth differ.
 */
export interface Divergence {
  spec_path: string;
  orchestration_status: TestCaseStatus;
  artifact_status: TestCaseStatus;
}

// ─── Live progress events ──────────────────────────────────────────────────

export type OrchestrationEventType =
  | 'orchestration.run.started'
  | 'orchestration.unit.leased'
  | 'orchestration.unit.completed'
  | 'orchestration.lease.expired'
  | 'orchestration.run.completed'
  | 'orchestration.run.timed_out';

export interface RunStartedPayload {
  total_units: number;
  deadline: string;
  lease_timeout_ms: number;
}

export interface UnitLeasedPayload {
  gh_job_name: string;
  gh_job_id: string;
  unit_ids: string[];
  deadline: string;
  is_retest?: boolean;
}

export interface UnitCompletedPayload {
  unit_id: string;
  outcome: UnitState;
  late_report: boolean;
  attempts_count: number;
}

export interface LeaseExpiredPayload {
  gh_job_name: string;
  gh_job_id: string;
  released_at: string;
  reclaimed_unit_ids: string[];
}

export interface RunCompletedPayload {
  terminal_at: string;
  counts: RunCounts;
}

export interface RunTimedOutPayload {
  terminal_at: string;
  counts: RunCounts;
  abandoned_count: number;
}

export interface OrchestrationEventEnvelope<T extends OrchestrationEventType, P> {
  type: T;
  identity: CompositeIdentity;
  timestamp: string;
  payload: P;
}

export type OrchestrationRunStartedEvent = OrchestrationEventEnvelope<
  'orchestration.run.started',
  RunStartedPayload
>;
export type OrchestrationUnitLeasedEvent = OrchestrationEventEnvelope<
  'orchestration.unit.leased',
  UnitLeasedPayload
>;
export type OrchestrationUnitCompletedEvent = OrchestrationEventEnvelope<
  'orchestration.unit.completed',
  UnitCompletedPayload
>;
export type OrchestrationLeaseExpiredEvent = OrchestrationEventEnvelope<
  'orchestration.lease.expired',
  LeaseExpiredPayload
>;
export type OrchestrationRunCompletedEvent = OrchestrationEventEnvelope<
  'orchestration.run.completed',
  RunCompletedPayload
>;
export type OrchestrationRunTimedOutEvent = OrchestrationEventEnvelope<
  'orchestration.run.timed_out',
  RunTimedOutPayload
>;

/** Discriminated union of all orchestration event shapes delivered over the WebSocket. */
export type OrchestrationEvent =
  | OrchestrationRunStartedEvent
  | OrchestrationUnitLeasedEvent
  | OrchestrationUnitCompletedEvent
  | OrchestrationLeaseExpiredEvent
  | OrchestrationRunCompletedEvent
  | OrchestrationRunTimedOutEvent;

// ─── Helpers ───────────────────────────────────────────────────────────────

/**
 * Return a deterministic string key for a CompositeIdentity, suitable for
 * React Query cache keys, Map lookups, and routing. The shape mirrors the
 * five identity components in canonical order; missing optional values fall
 * back to the same defaults the server uses.
 */
export function compositeIdentityKey(id: CompositeIdentity): string {
  const attempt = id.gh_run_attempt ?? '1';
  return `${id.repository}/${id.commit_sha}/${id.gh_run_id}/${id.name}/${attempt}`;
}

/**
 * Return true when two identities address the same orchestration run. Uses
 * the canonical key form, so optional `gh_run_attempt` defaulting is consistent.
 */
export function compositeIdentityEquals(a: CompositeIdentity, b: CompositeIdentity): boolean {
  return compositeIdentityKey(a) === compositeIdentityKey(b);
}

/**
 * Worker /checkout response. Surfaces `is_retest` so a worker (and the UI
 * mocking a worker, in tests) can distinguish a normal first-pass dispatch
 * from a retest re-dispatch.
 */
export interface CheckoutResponse {
  units: Array<{
    unit_id: string;
    dispatch_seq: number;
    spec_path: string;
    fail_count?: number;
  }>;
  queue_empty: boolean;
  is_retest: boolean;
  lease_id?: string;
  deadline?: string;
}

/** Begin-run request body. Exposed for the dev/admin mutation hook. */
export interface BeginRunRequest extends CompositeIdentity {
  playwright_project?: string;
  lease_timeout_ms?: number;
  idle_timeout_ms?: number;
  retest_on_fail?: boolean;
  retest_budget?: number;
  dispatch_units: Array<{ spec_path: string }>;
}

/**
 * Shared types for the worker action. Mirrors the Test System IO API
 * payloads on the wire so the strict-mode compiler can catch
 * mismatches at build time instead of at orchestration time.
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

export type TestStatus = "passed" | "failed" | "flaky" | "skipped" | "timedOut" | "interrupted";

export interface TestCaseResult {
  title: string;
  full_title: string;
  status: TestStatus;
  retry_count: number;
  duration_ms: number;
  ordinal: number;
  error_message?: string;
  error_stack?: string;
  attachments?: { screenshots: { key: string; relative_path?: string }[] };
}

export interface SpecResult {
  spec_path: string;
  status: TestStatus;
  actual_duration_ms: number;
  test_cases: TestCaseResult[];
  error_message?: string;
  error_stack?: string;
}

export interface CheckoutUnit {
  spec_path: string;
  // FIFO dispatch order (0-indexed), for log visibility.
  dispatch_seq: number;
}

// Mirrors /status's "counts" object. Present on every /checkout response.
export interface QueueCounts {
  pending: number;
  leased: number;
  completed_pass: number;
  completed_fail: number;
  completed_skipped: number;
  abandoned: number;
  retest_eligible: number;
  total: number;
}

// Snapshot of the server's shared pgxpool.Pool counters (server-wide, not
// scoped to this run).
export interface DbPoolStats {
  total_conns: number;
  acquired_conns: number;
  idle_conns: number;
  max_conns: number;
  empty_acquire_count: number;
}

// Worker-presence snapshot for this run. `active` holds an unreleased
// lease; `seen_total` has ever held one, active or released.
export interface WorkerCounts {
  active: number;
  seen_total: number;
}

export interface CheckoutResponseBody {
  queue_empty: boolean;
  is_retest?: boolean;
  retry_after_ms?: number;
  units?: CheckoutUnit[];
  counts?: QueueCounts;
  db_pool?: DbPoolStats;
  workers?: WorkerCounts;
  error?: string;
  message?: string;
}

export interface CompleteResponseBody {
  unit_states_changed?: { new_state: string }[];
  // Same fields as CheckoutResponseBody — present on pass, fail, and
  // idempotent replays alike.
  counts?: QueueCounts;
  db_pool?: DbPoolStats;
  workers?: WorkerCounts;
}

export interface ReportsRegisterResponseBody {
  report_id: string;
  upload_id: string;
}

export interface UploadPart {
  absPath: string;
  relPath: string;
  size: number;
  contentType?: string;
}

/**
 * One per-iteration archived results dir, captured after each Playwright
 * invocation so the worker can upload the accumulated artifacts at
 * queue-empty without racing against the next iteration overwriting
 * `playwright/results/`.
 */
export interface InvocationRecord {
  specPath: string;
  iterDir: string;
  playwrightJsonPath: string;
  /** Extra JSON reports from the same iteration (Cypress multi-spec batches). */
  additionalJsonPaths?: string[];
}

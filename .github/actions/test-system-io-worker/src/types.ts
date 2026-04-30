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
}

export interface CheckoutResponseBody {
  queue_empty: boolean;
  is_retest?: boolean;
  retry_after_ms?: number;
  units?: CheckoutUnit[];
  error?: string;
  message?: string;
}

export interface CompleteResponseBody {
  unit_states_changed?: { new_state: string }[];
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
}

/**
 * Custom Playwright reporter for the orchestration demo.
 *
 * Emits a richer JSON file than `--reporter=json` so the demo can:
 *   - walk per-result `attachments[]` with absolute paths (the built-in JSON
 *     reporter drops attachment file paths in some cases),
 *   - read per-test-case status, retries, durations, errors,
 *   - upload screenshots to the orchestration screenshots endpoint and
 *     reference them by storage key on the matching `/complete` request.
 *
 * Output file path: `process.env.TSIO_REPORTER_OUTPUT` (defaults to
 * `tsio-results.json` under the runner's cwd if unset).
 *
 * The shape this writes is consumed by `scripts/orchestration-demo.js`.
 *
 *   {
 *     "duration_ms": number,
 *     "output_dir": string | null,
 *     "specs": [
 *       {
 *         "spec_path": "tests/foo.spec.ts",
 *         "status": "passed" | "failed" | "skipped" | "flaky" | "timedOut" | "interrupted",
 *         "actual_duration_ms": number,
 *         "error_message"?: string,
 *         "error_stack"?: string,
 *         "test_cases": [
 *           {
 *             "title": string,
 *             "full_title": string,
 *             "status": "passed" | "failed" | "skipped" | "flaky" | "timedOut" | "interrupted",
 *             "retry_count": number,
 *             "duration_ms": number,
 *             "ordinal": number,
 *             "error_message"?: string,
 *             "error_stack"?: string,
 *             "attachments": [
 *               { "name": string, "content_type": string, "path": string | null, "body_base64"?: string }
 *             ]
 *           }
 *         ]
 *       }
 *     ]
 *   }
 *
 * `printsToStdio()` returns false so co-running terminal reporters (e.g.
 * `list`) keep their output.
 */

import * as fs from 'fs';
import * as path from 'path';
import type {
  Reporter,
  FullConfig,
  FullResult,
  Suite,
  TestCase,
  TestResult,
} from '@playwright/test/reporter';

type TsioStatus =
  | 'passed'
  | 'failed'
  | 'skipped'
  | 'flaky'
  | 'timedOut'
  | 'interrupted';

interface TsioAttachment {
  name: string;
  content_type: string;
  path: string | null;
  body_base64?: string;
}

interface TsioTestCase {
  title: string;
  full_title: string;
  status: TsioStatus;
  retry_count: number;
  duration_ms: number;
  ordinal: number;
  error_message?: string;
  error_stack?: string;
  attachments: TsioAttachment[];
}

interface TsioSpecAccumulator {
  spec_path: string;
  test_cases: TsioTestCase[];
  total_duration_ms: number;
  // First failing test case's error, used for spec-level error summary.
  first_error?: { message?: string; stack?: string };
  // Sequential ordinal counter, one shared across all test cases in a spec.
  ordinal: number;
}

// Rank used to compute the spec-level aggregate. `skipped` ranks BELOW
// `passed` so a file with a mix of skipped + passed aggregates to passed
// (the file ran, some test cases were `test.skip()`'d — the file's outcome
// is success, not "skipped"). Only when EVERY test case in the file is
// skipped does the spec-level aggregate end up as skipped.
const STATUS_RANK: Record<TsioStatus, number> = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

function worstStatus(statuses: TsioStatus[]): TsioStatus {
  if (statuses.length === 0) return 'skipped';
  let worst: TsioStatus = statuses[0];
  for (const s of statuses) {
    if (STATUS_RANK[s] > STATUS_RANK[worst]) worst = s;
  }
  return worst;
}

function mapResultStatus(
  status: TestResult['status'],
): Exclude<TsioStatus, 'flaky'> {
  switch (status) {
    case 'passed':
      return 'passed';
    case 'failed':
      return 'failed';
    case 'skipped':
      return 'skipped';
    case 'timedOut':
      return 'timedOut';
    case 'interrupted':
      return 'interrupted';
    default:
      return 'failed';
  }
}

export default class TsioReporter implements Reporter {
  private outputFile: string;
  // Project root: the directory containing playwright.config.ts. Tests
  // resolve as `tests/foo.spec.ts` relative to this, matching what the
  // orchestrator records as spec_path. Distinct from config.rootDir, which
  // Playwright sets to the resolved testDir (./tests) — relative-pathing
  // against that drops the leading `tests/` segment.
  private projectRoot: string = process.cwd();
  private projectOutputDir: string | null = null;
  private startedAt: number = Date.now();
  private specs = new Map<string, TsioSpecAccumulator>();

  constructor() {
    const envOut = process.env.TSIO_REPORTER_OUTPUT;
    this.outputFile = envOut && envOut.length > 0
      ? path.resolve(envOut)
      : path.resolve(process.cwd(), 'tsio-results.json');
  }

  printsToStdio(): boolean {
    return false;
  }

  onBegin(config: FullConfig, _suite: Suite): void {
    this.projectRoot = config.configFile
      ? path.dirname(config.configFile)
      : config.rootDir;
    this.startedAt = Date.now();
    const projects = config.projects ?? [];
    if (projects.length > 0 && projects[0].outputDir) {
      this.projectOutputDir = projects[0].outputDir;
    }
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    const specPath = path
      .relative(this.projectRoot, test.location.file)
      .split(path.sep)
      .join('/');

    let spec = this.specs.get(specPath);
    if (!spec) {
      spec = {
        spec_path: specPath,
        test_cases: [],
        total_duration_ms: 0,
        ordinal: 0,
      };
      this.specs.set(specPath, spec);
    }

    const titleParts = test
      .titlePath()
      .filter((s): s is string => typeof s === 'string' && s.length > 0);
    const fullTitle = titleParts.join(' > ');

    // Per the task spec: record each attempt's per-result status. The
    // demo aggregates spec-level flaky/etc. itself.
    const status: TsioStatus = mapResultStatus(result.status);

    const attachments: TsioAttachment[] = [];
    for (const a of result.attachments ?? []) {
      const att: TsioAttachment = {
        name: a.name,
        content_type: a.contentType,
        path: a.path ? path.resolve(a.path) : null,
      };
      if (!att.path && a.body && Buffer.isBuffer(a.body)) {
        att.body_base64 = a.body.toString('base64');
      }
      attachments.push(att);
    }

    const errorMessage = result.errors?.[0]?.message;
    const errorStack = result.errors?.[0]?.stack;

    const tc: TsioTestCase = {
      title: test.title,
      full_title: fullTitle,
      status,
      retry_count: result.retry,
      duration_ms: result.duration,
      ordinal: spec.ordinal++,
      attachments,
    };
    if (errorMessage) tc.error_message = errorMessage;
    if (errorStack) tc.error_stack = errorStack;

    spec.test_cases.push(tc);
    spec.total_duration_ms += result.duration;

    if (
      !spec.first_error &&
      (status === 'failed' || status === 'timedOut' || status === 'interrupted')
    ) {
      spec.first_error = { message: errorMessage, stack: errorStack };
    }
  }

  onEnd(_result: FullResult): void {
    const specs = [];
    for (const acc of this.specs.values()) {
      const status = worstStatus(acc.test_cases.map((t) => t.status));
      const entry: Record<string, unknown> = {
        spec_path: acc.spec_path,
        status,
        actual_duration_ms: Math.round(acc.total_duration_ms),
        test_cases: acc.test_cases,
      };
      if (
        (status === 'failed' || status === 'timedOut' || status === 'interrupted') &&
        acc.first_error
      ) {
        if (acc.first_error.message) entry.error_message = acc.first_error.message;
        if (acc.first_error.stack) entry.error_stack = acc.first_error.stack;
      }
      specs.push(entry);
    }

    const report = {
      duration_ms: Date.now() - this.startedAt,
      output_dir: this.projectOutputDir,
      specs,
    };

    const dir = path.dirname(this.outputFile);
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(this.outputFile, JSON.stringify(report, null, 2));
  }
}

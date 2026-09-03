// Playwright built-in JSON reporter → orchestration SpecResult parser.
// Ported from playwright.ts's aggregateSpec()/mapStatus() (kept in sync by
// hand, not a shared import — that action's tsup build bundles src/index.ts).
//
// Walks the suite tree (suites[].specs[].tests[].results[]). A test that
// fails then passes via --retries is "flaky", not a hard failure. Spec
// status is the worst per-test outcome, ranked:
//
//   skipped < passed < flaky < interrupted < timedOut < failed

'use strict';

const RANKS = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

function mapStatus(s) {
  switch (s) {
    case 'expected':
    case 'passed':
      return 'passed';
    case 'unexpected':
    case 'failed':
      return 'failed';
    case 'flaky':
      return 'flaky';
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

function fileMatches(file, specPath) {
  if (!file) return false;
  if (file === specPath) return true;
  if (file.endsWith('/' + specPath)) return true;
  if (specPath.endsWith('/' + file)) return true;
  return false;
}

// aggregateSpec returns the /complete SpecResult shape for one spec_path.
// fallbackDurationMs is used only when all per-test durations are zero/missing.
function aggregateSpec(json, specPath, fallbackDurationMs) {
  const cases = [];
  let totalMs = 0;
  let worst = 'skipped';

  function visit(suite, ancestors, currentFile) {
    const here = suite.title ? [...ancestors, suite.title] : ancestors;
    const suiteFile = suite.file || currentFile;
    if (fileMatches(suiteFile, specPath)) {
      for (const s of suite.specs || []) {
        const specTitle = [...here, s.title];
        for (const t of s.tests || []) {
          let everPassed = false;
          let everFailed = false;
          let everTimedOut = false;
          let everInterrupted = false;
          let everSkipped = false;
          for (const r of t.results || []) {
            const status = mapStatus(r.status);
            if (status === 'passed' || status === 'flaky') everPassed = true;
            else if (status === 'failed') everFailed = true;
            else if (status === 'timedOut') everTimedOut = true;
            else if (status === 'interrupted') everInterrupted = true;
            else if (status === 'skipped') everSkipped = true;

            const tc = {
              title: s.title,
              full_title: specTitle.join(' > '),
              status,
              retry_count: r.retry || 0,
              // Server requires integer ms; Playwright emits fractional ms.
              duration_ms: Math.round(r.duration || 0),
              ordinal: cases.length,
            };
            const err = (r.errors && r.errors[0]) || r.error;
            tc.error_message = err && err.message ? err.message : null;
            tc.error_stack = err && err.stack ? err.stack : null;
            cases.push(tc);
            totalMs += tc.duration_ms;
          }

          let testOutcome = null;
          if (everPassed && (everFailed || everTimedOut || everInterrupted)) testOutcome = 'flaky';
          else if (everPassed) testOutcome = 'passed';
          else if (everInterrupted) testOutcome = 'interrupted';
          else if (everTimedOut) testOutcome = 'timedOut';
          else if (everFailed) testOutcome = 'failed';
          else if (everSkipped) testOutcome = 'skipped';
          if (testOutcome == null) continue;
          if (RANKS[testOutcome] > RANKS[worst]) worst = testOutcome;
        }
      }
    }
    for (const sub of suite.suites || []) visit(sub, here, suiteFile);
  }
  for (const s of (json && json.suites) || []) visit(s, [], '');

  if (cases.length === 0) {
    return { spec_path: specPath, status: 'skipped', actual_duration_ms: 0, test_cases: [] };
  }

  const out = {
    spec_path: specPath,
    status: worst,
    actual_duration_ms: Math.round(totalMs || fallbackDurationMs || 0),
    test_cases: cases,
  };
  const firstFail = cases.find(
    (c) => c.status === 'failed' || c.status === 'timedOut' || c.status === 'interrupted',
  );
  if (firstFail && firstFail.error_message) out.error_message = firstFail.error_message;
  if (firstFail && firstFail.error_stack) out.error_stack = firstFail.error_stack;
  return out;
}

// collectSpecFiles collects every distinct spec `file` in the suite tree.
function collectSpecFiles(json) {
  const files = new Set();
  function visit(suite) {
    if (suite.file) files.add(suite.file);
    for (const sub of suite.suites || []) visit(sub);
  }
  for (const s of (json && json.suites) || []) visit(s);
  return [...files];
}

module.exports = {
  aggregateSpec,
  collectSpecFiles,
  // Exposed for unit-test fixture coverage.
  mapStatus,
};

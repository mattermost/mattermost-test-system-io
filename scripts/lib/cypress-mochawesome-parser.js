// Cypress Mochawesome → orchestration test_cases parser.
//
// Walks a Mochawesome JSON tree (results[].suites[]...tests[]) and emits
// the per-test shape the orchestration /complete endpoint expects. Status
// mapping precedence:
//
//   pending: true                              → 'skipped'
//   state: 'failed'                            → 'failed'
//   state: 'passed' AND attempts.length > 1    → 'flaky'
//   state: 'passed'                            → 'passed'
//   any other (parse anomaly)                  → 'interrupted'
//
// The aggregate spec status is derived from the per-test array using the
// same precedence rules:
//
//   any failed                                 → 'failed'
//   only skipped                               → 'skipped'
//   any flaky and zero failed                  → 'flaky'
//   only passed                                → 'passed'
//   empty array (parse failure / no tests)    → 'interrupted'
//
// Pure JS, no deps. Consumed by both scripts/orchestration-demo-cypress.js
// and the dispatcher action's Cypress adapter.

'use strict';

function statusForTest(test) {
  const attempts = Array.isArray(test.attempts) ? test.attempts.length : 0;
  if (test.pending === true) return 'skipped';
  if (test.state === 'failed') return 'failed';
  if (test.state === 'passed' && attempts > 1) return 'flaky';
  if (test.state === 'passed') return 'passed';
  return 'interrupted';
}

function collectTests(jsonObj) {
  const out = [];
  let ordinal = 0;

  function walkSuite(suite) {
    for (const t of suite.tests ?? []) {
      const attempts = Array.isArray(t.attempts) ? t.attempts.length : 0;
      // `attachments` is intentionally omitted: the orchestration schema
      // declares it as `nullable: true, type: object` (e.g.
      // `{ screenshots: [{ key, relative_path }, ...] }`), and the
      // dispatcher only populates it after a successful POST to
      // /api/v1/orchestration/screenshots. Emitting it as an empty
      // array here trips the schema validator with 400.
      out.push({
        title: t.title || '',
        full_title: t.fullTitle || t.title || '',
        status: statusForTest(t),
        retry_count: Math.max(0, attempts - 1),
        duration_ms: typeof t.duration === 'number' ? t.duration : 0,
        error_message: t.err && t.err.message ? t.err.message : null,
        error_stack: t.err && (t.err.estack || t.err.stack) ? t.err.estack || t.err.stack : null,
        annotations: null,
        ordinal: ordinal++,
      });
    }
    for (const inner of suite.suites ?? []) walkSuite(inner);
  }

  for (const top of jsonObj.results ?? []) {
    walkSuite(top);
  }
  return out;
}

function aggregateSpecStatus(testCases) {
  if (testCases.length === 0) return 'interrupted';
  let hasFailed = false;
  let hasFlaky = false;
  let hasPassed = false;
  let hasSkipped = false;
  for (const c of testCases) {
    if (c.status === 'failed') hasFailed = true;
    else if (c.status === 'flaky') hasFlaky = true;
    else if (c.status === 'passed') hasPassed = true;
    else if (c.status === 'skipped') hasSkipped = true;
  }
  if (hasFailed) return 'failed';
  if (!hasPassed && !hasFlaky && hasSkipped) return 'skipped';
  if (hasFlaky) return 'flaky';
  if (hasPassed) return 'passed';
  return 'interrupted';
}

// parseMochawesomeJson takes a parsed Mochawesome JSON object and returns
// the aggregate status + per-test array ready for direct attachment to
// an orchestration /complete results[] item. The shape matches the
// dispatcher contract.
function parseMochawesomeJson(jsonObj) {
  const testCases = collectTests(jsonObj || {});
  return {
    aggregateStatus: aggregateSpecStatus(testCases),
    testCases,
  };
}

module.exports = {
  parseMochawesomeJson,
  // Exposed for unit-test fixture coverage.
  collectTests,
  aggregateSpecStatus,
  statusForTest,
};

// Unit tests for the Cypress Mochawesome parser. Runs under node's
// stdlib test runner — `node --test scripts/lib/cypress-mochawesome-parser.test.js`
// — so no test-framework dep is needed.
//
// Coverage includes:
//   1. Synthetic fixtures pinning each branch of the status mapping.
//   2. A real Mochawesome JSON from seed/cypress-ci/ asserting the
//      aggregate counts the parser produces match the file's stats
//      object that mochawesome itself computed.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const {
  parseMochawesomeJson,
  collectTests,
  aggregateSpecStatus,
  statusForTest,
} = require('./cypress-mochawesome-parser');

// ─── Synthetic fixtures ────────────────────────────────────────────────────

test('statusForTest maps pending to skipped', () => {
  assert.equal(statusForTest({ pending: true }), 'skipped');
});

test('statusForTest maps failed state to failed', () => {
  assert.equal(statusForTest({ state: 'failed' }), 'failed');
});

test('statusForTest maps passed-with-multiple-attempts to flaky', () => {
  assert.equal(
    statusForTest({ state: 'passed', attempts: [{}, {}] }),
    'flaky',
  );
});

test('statusForTest maps passed-without-retries to passed', () => {
  assert.equal(statusForTest({ state: 'passed', attempts: [{}] }), 'passed');
});

test('statusForTest maps unknown state to interrupted', () => {
  assert.equal(statusForTest({}), 'interrupted');
  assert.equal(statusForTest({ state: 'something-else' }), 'interrupted');
});

test('aggregateSpecStatus precedence: any failed wins', () => {
  assert.equal(
    aggregateSpecStatus([{ status: 'passed' }, { status: 'failed' }, { status: 'flaky' }]),
    'failed',
  );
});

test('aggregateSpecStatus: only-skipped maps to skipped', () => {
  assert.equal(
    aggregateSpecStatus([{ status: 'skipped' }, { status: 'skipped' }]),
    'skipped',
  );
});

test('aggregateSpecStatus: any-flaky-no-failed maps to flaky', () => {
  assert.equal(
    aggregateSpecStatus([{ status: 'passed' }, { status: 'flaky' }]),
    'flaky',
  );
});

test('aggregateSpecStatus: all-passed maps to passed', () => {
  assert.equal(
    aggregateSpecStatus([{ status: 'passed' }, { status: 'passed' }]),
    'passed',
  );
});

test('aggregateSpecStatus: empty array maps to interrupted', () => {
  assert.equal(aggregateSpecStatus([]), 'interrupted');
});

test('collectTests walks nested describe blocks and assigns ordinals', () => {
  const fixture = {
    results: [
      {
        suites: [
          {
            tests: [
              { title: 't1', fullTitle: 'outer t1', state: 'passed', duration: 10 },
            ],
            suites: [
              {
                tests: [
                  { title: 't2', fullTitle: 'outer inner t2', state: 'passed', duration: 20 },
                  { title: 't3', fullTitle: 'outer inner t3', pending: true },
                ],
              },
            ],
          },
        ],
      },
    ],
  };
  const cases = collectTests(fixture);
  assert.equal(cases.length, 3);
  assert.deepEqual(
    cases.map((c) => c.ordinal),
    [0, 1, 2],
  );
  assert.equal(cases[0].title, 't1');
  assert.equal(cases[1].full_title, 'outer inner t2');
  assert.equal(cases[2].status, 'skipped');
});

test('collectTests preserves err message and stack', () => {
  const cases = collectTests({
    results: [
      {
        suites: [
          {
            tests: [
              {
                title: 'fails',
                state: 'failed',
                duration: 50,
                err: { message: 'boom', estack: 'AssertionError\n  at foo' },
              },
            ],
          },
        ],
      },
    ],
  });
  assert.equal(cases.length, 1);
  assert.equal(cases[0].error_message, 'boom');
  assert.equal(cases[0].error_stack, 'AssertionError\n  at foo');
});

test('collectTests treats parse-anomaly attempts.length safely', () => {
  // A malformed Mochawesome with attempts: undefined should still parse.
  const cases = collectTests({
    results: [{ suites: [{ tests: [{ title: 't', state: 'passed', duration: 1 }] }] }],
  });
  assert.equal(cases.length, 1);
  assert.equal(cases[0].retry_count, 0);
});

test('parseMochawesomeJson handles empty input', () => {
  const out = parseMochawesomeJson({});
  assert.equal(out.aggregateStatus, 'interrupted');
  assert.deepEqual(out.testCases, []);
});

test('parseMochawesomeJson handles null input safely', () => {
  const out = parseMochawesomeJson(null);
  assert.equal(out.aggregateStatus, 'interrupted');
  assert.deepEqual(out.testCases, []);
});

// ─── Real fixture round-trip (against seed/cypress-ci/) ────────────────────

const SEED_FIXTURE = path.resolve(
  __dirname,
  '..',
  '..',
  'seed',
  'cypress-ci',
  'cypress-full--results-0',
  'results',
  'mochawesome-report',
  'json',
  'tests',
  'integration',
  'channels',
  'message_forwarding',
  'forward_message_from_gm_spec.js.json',
);

test('seed-fixture round-trip: parsed counts match mochawesome stats', { skip: !fs.existsSync(SEED_FIXTURE) }, () => {
  const raw = fs.readFileSync(SEED_FIXTURE, 'utf8');
  const json = JSON.parse(raw);
  const { aggregateStatus, testCases } = parseMochawesomeJson(json);

  // Mochawesome's own .stats object is the source of truth for the
  // fixture's expected counts. Compare against the parser's per-test
  // status histogram.
  const expected = json.stats;
  const actual = {
    passed: testCases.filter((c) => c.status === 'passed').length,
    failed: testCases.filter((c) => c.status === 'failed').length,
    flaky: testCases.filter((c) => c.status === 'flaky').length,
    skipped: testCases.filter((c) => c.status === 'skipped').length,
  };

  assert.equal(testCases.length, expected.tests, 'total test count');
  assert.equal(actual.failed, expected.failures, 'failure count');
  // Mochawesome counts a passed-on-retry test as a single pass; the
  // parser splits it into the `flaky` bucket. So the parser's
  // (passed + flaky) should equal mochawesome's `passes`.
  assert.equal(
    actual.passed + actual.flaky,
    expected.passes,
    'passed (incl. flaky) count matches mochawesome.passes',
  );
  assert.equal(actual.skipped, expected.pending, 'skipped count matches mochawesome.pending');

  // The aggregate status should reflect the underlying outcomes.
  if (expected.failures > 0) {
    assert.equal(aggregateStatus, 'failed');
  } else if (actual.flaky > 0) {
    assert.equal(aggregateStatus, 'flaky');
  } else if (actual.passed > 0) {
    assert.equal(aggregateStatus, 'passed');
  }
});

// Unit tests for the Detox/Jest results parser. Runs under node's stdlib
// test runner — `node --test scripts/lib/detox-jest-results-parser.test.js`
// — so no test-framework dep is needed.
//
// Coverage includes:
//   1. Synthetic fixtures pinning each branch of the status mapping.
//   2. A real jest-results.json from .local/mattermost-mobile-ci/ asserting
//      the aggregate counts the parser produces match the file's own
//      numPassedTests/numFailedTests totals. Skipped (not failed) when that
//      gitignored scratch data isn't present locally.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const { aggregateSpec, collectSpecFiles, normalizeSpecPath, detoxStatus } = require('./detox-jest-results-parser');

// ─── Synthetic fixtures ────────────────────────────────────────────────────

test('detoxStatus maps passed/failed straight through', () => {
  assert.equal(detoxStatus('passed'), 'passed');
  assert.equal(detoxStatus('failed'), 'failed');
});

test('detoxStatus maps pending/skipped/todo to skipped', () => {
  assert.equal(detoxStatus('pending'), 'skipped');
  assert.equal(detoxStatus('skipped'), 'skipped');
  assert.equal(detoxStatus('todo'), 'skipped');
});

test('detoxStatus maps unknown/undefined to skipped', () => {
  assert.equal(detoxStatus('focused'), 'skipped');
  assert.equal(detoxStatus(undefined), 'skipped');
});

test('aggregateSpec: all passed -> spec status passed, durations summed', () => {
  const result = aggregateSpec(
    {
      name: '/x/file.e2e.ts',
      assertionResults: [
        { title: 'a', fullName: 'suite a', status: 'passed', duration: 100 },
        { title: 'b', fullName: 'suite b', status: 'passed', duration: 200 },
      ],
    },
    'products/file.e2e.ts',
  );
  assert.equal(result.status, 'passed');
  assert.equal(result.actual_duration_ms, 300);
  assert.equal(result.test_cases.length, 2);
  assert.equal(result.spec_path, 'products/file.e2e.ts');
});

test('aggregateSpec: a failing case makes the whole spec failed, error surfaced', () => {
  const result = aggregateSpec(
    {
      assertionResults: [
        { title: 'a', fullName: 'suite a', status: 'passed', duration: 10 },
        {
          title: 'b',
          fullName: 'suite b',
          status: 'failed',
          duration: 20,
          failureMessages: ['Error: boom\n  at foo'],
        },
      ],
    },
    'products/file.e2e.ts',
  );
  assert.equal(result.status, 'failed');
  assert.equal(result.error_message, 'Error: boom\n  at foo');
  assert.equal(result.error_stack, result.error_message);
});

test('aggregateSpec: pending/todo cases map to skipped and do not dominate a passed spec', () => {
  const result = aggregateSpec(
    {
      assertionResults: [
        { title: 'a', fullName: 'suite a', status: 'passed', duration: 10 },
        { title: 'b', fullName: 'suite b', status: 'pending', duration: 0 },
      ],
    },
    'products/file.e2e.ts',
  );
  assert.equal(result.status, 'passed');
  assert.equal(result.test_cases[1].status, 'skipped');
});

test('aggregateSpec: empty assertionResults -> spec-level skipped, no test_cases', () => {
  const result = aggregateSpec({ assertionResults: [] }, 'products/file.e2e.ts');
  assert.equal(result.status, 'skipped');
  assert.deepEqual(result.test_cases, []);
});

test('aggregateSpec: missing duration falls back to 0', () => {
  const result = aggregateSpec(
    { assertionResults: [{ title: 'a', status: 'passed' }] },
    'products/file.e2e.ts',
  );
  assert.equal(result.test_cases[0].duration_ms, 0);
});

test('normalizeSpecPath strips everything up to e2e/test/', () => {
  assert.equal(
    normalizeSpecPath(
      '/home/runner/work/mattermost-mobile/mattermost-mobile/detox/e2e/test/products/channels/x.e2e.ts',
    ),
    'products/channels/x.e2e.ts',
  );
});

test('normalizeSpecPath falls back to basename when no e2e/test/ marker is present', () => {
  assert.equal(normalizeSpecPath('some/other/path/x.e2e.ts'), 'x.e2e.ts');
});

test('collectSpecFiles returns every testResults[].name', () => {
  const files = collectSpecFiles({
    testResults: [{ name: '/a.e2e.ts' }, { name: '/b.e2e.ts' }],
  });
  assert.deepEqual(files, ['/a.e2e.ts', '/b.e2e.ts']);
});

test('collectSpecFiles handles empty/malformed input safely', () => {
  assert.deepEqual(collectSpecFiles({}), []);
  assert.deepEqual(collectSpecFiles(null), []);
});

// ─── Real fixture round-trip (against .local/mattermost-mobile-ci/) ────────

const REAL_FIXTURE = path.resolve(
  __dirname,
  '..',
  '..',
  '.local',
  'mattermost-mobile-ci',
  'ios-results-j4tvhrpzsi-9',
  'jest-results.json',
);

test('real-fixture round-trip: parsed counts match jest-results.json totals', { skip: !fs.existsSync(REAL_FIXTURE) }, () => {
  const json = JSON.parse(fs.readFileSync(REAL_FIXTURE, 'utf8'));

  let passed = 0;
  let failed = 0;
  let skipped = 0;
  for (const fileEntry of json.testResults) {
    const specPath = normalizeSpecPath(fileEntry.name);
    const result = aggregateSpec(fileEntry, specPath);
    for (const tc of result.test_cases) {
      if (tc.status === 'passed') passed += 1;
      else if (tc.status === 'failed') failed += 1;
      else if (tc.status === 'skipped') skipped += 1;
    }
  }

  assert.equal(passed, json.numPassedTests, 'passed count matches numPassedTests');
  assert.equal(failed, json.numFailedTests, 'failed count matches numFailedTests');
  assert.equal(passed + failed + skipped, json.numTotalTests, 'total count matches numTotalTests');
});

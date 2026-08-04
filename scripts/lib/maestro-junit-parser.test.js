// Unit tests for the Maestro merged-JUnit parser. Runs under node's stdlib
// test runner — `node --test scripts/lib/maestro-junit-parser.test.js` —
// so no test-framework dep is needed.
//
// Coverage includes:
//   1. Synthetic fixtures pinning each branch of the status mapping and the
//      regex-based XML extraction (attributes, self-closing testcases,
//      <failure>/<error> children, entity-escaped text).
//   2. Real maestro-report.xml files from .local/mattermost-mobile-ci/
//      asserting the parsed testcase count matches an independent
//      `<testcase` occurrence count in the raw XML. Skipped (not failed)
//      when that gitignored scratch data isn't present locally.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const {
  parseMaestroReport,
  aggregateSpec,
  collectSpecFiles,
  normalizeSpecPath,
  maestroStatus,
} = require('./maestro-junit-parser');

// ─── Synthetic fixtures ────────────────────────────────────────────────────

test('maestroStatus maps SUCCESS/PASSED to passed', () => {
  assert.equal(maestroStatus('SUCCESS'), 'passed');
  assert.equal(maestroStatus('PASSED'), 'passed');
});

test('maestroStatus maps FAILED/ERROR to failed', () => {
  assert.equal(maestroStatus('FAILED'), 'failed');
  assert.equal(maestroStatus('ERROR'), 'failed');
});

test('maestroStatus maps SKIPPED/WARNING/unknown/undefined to skipped', () => {
  assert.equal(maestroStatus('SKIPPED'), 'skipped');
  assert.equal(maestroStatus('WARNING'), 'skipped');
  assert.equal(maestroStatus('something-unknown'), 'skipped');
  assert.equal(maestroStatus(undefined), 'skipped');
});

test('parseMaestroReport: single testsuite/testcase, matches the real merged-report shape', () => {
  const xml = `<?xml version='1.0' encoding='UTF-8'?>
<testsuites>
  <testsuite name="detox/maestro/flows/timezone/clock_display.yml" device="" tests="1" failures="0" errors="0" skipped="0" time="118.0">
    <testcase id="clock_display" name="clock_display" classname="detox/maestro/flows/timezone/clock_display.yml" file="detox/maestro/flows/timezone/clock_display.yml" time="118.0" status="SUCCESS">
    </testcase>
  </testsuite>
</testsuites>`;
  const parsed = parseMaestroReport(xml);
  assert.equal(parsed.testsuites.length, 1);
  assert.equal(parsed.testsuites[0].name, 'detox/maestro/flows/timezone/clock_display.yml');
  assert.equal(parsed.testsuites[0].testcases.length, 1);
  assert.equal(parsed.testsuites[0].testcases[0].status, 'SUCCESS');
});

test('parseMaestroReport: multiple testsuites, self-closing testcase', () => {
  const xml = `<testsuites>
  <testsuite name="flows/a.yml" time="1.0">
    <testcase id="a" name="a" classname="flows/a.yml" time="1.0" status="SUCCESS"/>
  </testsuite>
  <testsuite name="flows/b.yml" time="2.0">
    <testcase id="b" name="b" classname="flows/b.yml" time="2.0" status="FAILED">
      <failure message="element not found">assertVisible failed</failure>
    </testcase>
  </testsuite>
</testsuites>`;
  const parsed = parseMaestroReport(xml);
  assert.equal(parsed.testsuites.length, 2);
  assert.equal(parsed.testsuites[1].testcases[0].failureMessage, 'element not found');
});

test('parseMaestroReport: unescapes XML entities in failure text', () => {
  const xml = `<testsuites>
  <testsuite name="flows/c.yml" time="1.0">
    <testcase id="c" name="c" classname="flows/c.yml" time="1.0" status="FAILED">
      <failure>expected &quot;a &amp; b&quot; &lt;visible&gt;</failure>
    </testcase>
  </testsuite>
</testsuites>`;
  const parsed = parseMaestroReport(xml);
  assert.equal(parsed.testsuites[0].testcases[0].failureMessage, 'expected "a & b" <visible>');
});

test('aggregateSpec: passing testcase -> spec status passed, duration seconds->ms', () => {
  const result = aggregateSpec(
    { name: 'flows/a.yml', testcases: [{ id: 'a', name: 'a', classname: 'flows/a.yml', time: '1.5', status: 'SUCCESS' }] },
    'flows/a.yml',
  );
  assert.equal(result.status, 'passed');
  assert.equal(result.actual_duration_ms, 1500);
  assert.equal(result.test_cases.length, 1);
});

test('aggregateSpec: a failing case makes the whole spec failed, error surfaced', () => {
  const result = aggregateSpec(
    {
      testcases: [
        { id: 'b', name: 'b', classname: 'flows/b.yml', time: '2.0', status: 'FAILED', failureMessage: 'boom' },
      ],
    },
    'flows/b.yml',
  );
  assert.equal(result.status, 'failed');
  assert.equal(result.error_message, 'boom');
  assert.equal(result.error_stack, 'boom');
});

test('aggregateSpec: empty testcases -> spec-level skipped, no test_cases', () => {
  const result = aggregateSpec({ testcases: [] }, 'flows/a.yml');
  assert.equal(result.status, 'skipped');
  assert.deepEqual(result.test_cases, []);
});

test('aggregateSpec: missing time falls back to 0', () => {
  const result = aggregateSpec(
    { testcases: [{ id: 'a', name: 'a', status: 'SUCCESS' }] },
    'flows/a.yml',
  );
  assert.equal(result.test_cases[0].duration_ms, 0);
});

test('normalizeSpecPath is a passthrough (merged testsuite name is already the spec_path)', () => {
  assert.equal(normalizeSpecPath('detox/maestro/flows/timezone/clock_display.yml'), 'detox/maestro/flows/timezone/clock_display.yml');
});

test('collectSpecFiles returns every testsuite name', () => {
  const files = collectSpecFiles({ testsuites: [{ name: 'flows/a.yml' }, { name: 'flows/b.yml' }] });
  assert.deepEqual(files, ['flows/a.yml', 'flows/b.yml']);
});

test('collectSpecFiles handles empty/malformed input safely', () => {
  assert.deepEqual(collectSpecFiles({}), []);
  assert.deepEqual(collectSpecFiles(null), []);
});

// ─── Real fixture round-trip (against .local/mattermost-mobile-ci/) ────────

const REAL_FIXTURES = [
  path.resolve(__dirname, '..', '..', '.local', 'mattermost-mobile-ci', 'maestro-ios-results-30513211589', 'maestro-report.xml'),
  path.resolve(__dirname, '..', '..', '.local', 'mattermost-mobile-ci', 'maestro-android-results-30513211589', 'maestro-report.xml'),
];

for (const fixture of REAL_FIXTURES) {
  test(`real-fixture round-trip: parsed testcase count matches <testcase occurrences (${path.basename(path.dirname(fixture))})`, { skip: !fs.existsSync(fixture) }, () => {
    const xml = fs.readFileSync(fixture, 'utf8');
    const parsed = parseMaestroReport(xml);

    const rawTestcaseCount = (xml.match(/<testcase\b/g) || []).length;
    let parsedTestcaseCount = 0;
    for (const rawName of collectSpecFiles(parsed)) {
      const entry = parsed.testsuites.find((ts) => ts.name === rawName);
      const result = aggregateSpec(entry, normalizeSpecPath(rawName));
      parsedTestcaseCount += result.test_cases.length;
      // Every real fixture is SUCCESS-only today; assert the mapping holds
      // rather than hardcoding a pass count that'd go stale if the fixture changes.
      for (const tc of result.test_cases) {
        assert.ok(['passed', 'failed', 'skipped'].includes(tc.status));
      }
    }
    assert.equal(parsedTestcaseCount, rawTestcaseCount, 'every <testcase> in the file was parsed exactly once');
  });
}

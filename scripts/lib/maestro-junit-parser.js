// Maestro merged JUnit XML (`maestro-report.xml`, produced by
// mattermost-mobile's mergeMaestroJunitReports()) -> orchestration
// SpecResult parser. Ported from dispatch-run's maestro.ts
// (maestroStatus()/aggregateMaestroReport()), kept in sync by hand, not a
// shared import — that action's tsup build bundles src/index.ts.
//
// Hand-rolled regex-based extraction, not a full XML parser: this script
// has no package.json/dependencies (unlike the GH Action, which uses
// fast-xml-parser), and only ever reads our own already-merged,
// already-trusted build artifact — a narrower, well-known shape (one
// <testsuite> per flow, regrouped by file path; see
// mergeMaestroJunitReports() in mattermost-mobile's detox/utils/).
//
// Each merged testsuite's `name` attribute IS the flow's repo-relative
// path (e.g. "detox/maestro/flows/timezone/clock_display.yml") — that's
// the regroup key mergeMaestroJunitReports() uses, so it doubles as the
// spec_path convention here.

'use strict';

const RANKS = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

function maestroStatus(raw) {
  switch (String(raw || '').toUpperCase()) {
    case 'SUCCESS':
    case 'PASSED':
      return 'passed';
    case 'FAILED':
    case 'ERROR':
      return 'failed';
    case 'SKIPPED':
    case 'WARNING':
      return 'skipped';
    case 'CANCELED':
    case 'STOPPED':
    case 'PENDING':
    case 'PREPARING':
    case 'INSTALLING':
    case 'RUNNING':
    default:
      return 'interrupted';
  }
}

function unescapeXmlEntities(s) {
  return s
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, '&');
}

function parseAttrs(attrString) {
  const out = {};
  const re = /([\w:-]+)\s*=\s*"([^"]*)"/g;
  let m;
  while ((m = re.exec(attrString))) {
    out[m[1]] = unescapeXmlEntities(m[2]);
  }
  return out;
}

// Extracts the message attribute (or inner text) of the first
// <failure>/<error> child within a testcase's inner XML, if any.
function extractFailure(innerXml) {
  const m = /<(failure|error)\b([^>]*?)(?:\/>|>([\s\S]*?)<\/\1>)/.exec(innerXml);
  if (!m) return null;
  const attrs = parseAttrs(m[2] || '');
  const text = m[3] ? unescapeXmlEntities(m[3].trim()) : '';
  return attrs.message || text || null;
}

function parseTestcases(suiteInnerXml) {
  const out = [];
  // Matches both self-closing <testcase .../> and paired <testcase ...>...</testcase>.
  const re = /<testcase\b([^>]*?)(?:\/>|>([\s\S]*?)<\/testcase>)/g;
  let m;
  while ((m = re.exec(suiteInnerXml))) {
    const attrs = parseAttrs(m[1]);
    out.push({
      id: attrs.id,
      name: attrs.name,
      classname: attrs.classname,
      file: attrs.file,
      time: attrs.time,
      status: attrs.status,
      failureMessage: extractFailure(m[2] || ''),
    });
  }
  return out;
}

// parseMaestroReport parses a merged maestro-report.xml into
// { testsuites: [{ name, time, testcases: [...] }] }.
function parseMaestroReport(xml) {
  const testsuites = [];
  const re = /<testsuite\b([^>]*?)(?:\/>|>([\s\S]*?)<\/testsuite>)/g;
  let m;
  while ((m = re.exec(xml))) {
    const attrs = parseAttrs(m[1]);
    testsuites.push({
      name: attrs.name,
      time: attrs.time,
      testcases: parseTestcases(m[2] || ''),
    });
  }
  return { testsuites };
}

// collectSpecFiles returns every testsuite's `name` (the flow's
// repo-relative path) in a parsed Maestro report.
function collectSpecFiles(parsed) {
  return ((parsed && parsed.testsuites) || []).map((ts) => ts.name).filter(Boolean);
}

function parseDurationMs(raw) {
  const seconds = Number.parseFloat(raw);
  return Number.isFinite(seconds) ? Math.round(seconds * 1000) : 0;
}

// aggregateSpec returns the /complete SpecResult shape for one parsed
// testsuite entry ({name, testcases: [{id, name, classname, file, time,
// status, failureMessage}]}).
function aggregateSpec(testsuiteEntry, specPath) {
  const cases = [];
  let totalMs = 0;
  let worst = 'skipped';
  let ordinal = 0;

  for (const tc of (testsuiteEntry && testsuiteEntry.testcases) || []) {
    const status = maestroStatus(tc.status);
    const durationMs = parseDurationMs(tc.time);
    const out = {
      title: tc.name || tc.id || '',
      full_title: tc.classname || tc.name || tc.id || '',
      status,
      retry_count: 0,
      duration_ms: durationMs,
      ordinal: ordinal++,
    };
    if (tc.failureMessage) {
      out.error_message = tc.failureMessage;
      out.error_stack = tc.failureMessage;
    }
    cases.push(out);
    totalMs += durationMs;
    if (RANKS[status] > RANKS[worst]) worst = status;
  }

  if (cases.length === 0) {
    return { spec_path: specPath, status: 'skipped', actual_duration_ms: 0, test_cases: [] };
  }
  const out = { spec_path: specPath, status: worst, actual_duration_ms: totalMs, test_cases: cases };
  const firstFail = cases.find((c) => c.status === 'failed');
  if (firstFail && firstFail.error_message) out.error_message = firstFail.error_message;
  if (firstFail && firstFail.error_stack) out.error_stack = firstFail.error_stack;
  return out;
}

// normalizeSpecPath is a passthrough — the merged report's testsuite
// `name` already IS the repo-relative spec_path (see module doc comment),
// unlike Detox's Jest testResults[].name which needs the /e2e/test/
// marker stripped. Kept for API symmetry with detox-jest-results-parser.js.
function normalizeSpecPath(rawName) {
  return rawName;
}

module.exports = {
  parseMaestroReport,
  aggregateSpec,
  collectSpecFiles,
  normalizeSpecPath,
  maestroStatus,
};

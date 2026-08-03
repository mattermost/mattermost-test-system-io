// Detox/Jest native `--json --outputFile` → orchestration SpecResult parser.
// Ported from dispatch-run's detox.ts (detoxStatus()/aggregateDetoxFile()),
// kept in sync by hand, not a shared import — that action's tsup build
// bundles src/index.ts.
//
// Walks one testResults[] file entry's assertionResults[]. Spec status is
// the worst per-test outcome, ranked:
//
//   skipped < passed < flaky < interrupted < timedOut < failed
//
// Detox/Jest's native vocabulary has no "flaky"/"timedOut"/"interrupted" —
// those only ever appear here as the worst-of default (skipped), since
// detoxStatus() only maps to passed/failed/skipped.

'use strict';

const RANKS = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

function detoxStatus(s) {
  switch (s) {
    case 'passed':
      return 'passed';
    case 'failed':
      return 'failed';
    case 'pending':
    case 'skipped':
    case 'todo':
      return 'skipped';
    default:
      return 'skipped';
  }
}

// aggregateSpec returns the /complete SpecResult shape for one Jest
// testResults[] file entry ({name, assertionResults: [{ancestorTitles,
// fullName, status, duration, failureMessages}]}).
function aggregateSpec(fileEntry, specPath) {
  const cases = [];
  let totalMs = 0;
  let worst = 'skipped';
  let ordinal = 0;

  for (const t of (fileEntry && fileEntry.assertionResults) || []) {
    const status = detoxStatus(t.status);
    const durationMs = typeof t.duration === 'number' ? t.duration : 0;
    const tc = {
      title: t.title || '',
      full_title: t.fullName || t.title || '',
      status,
      retry_count: 0,
      duration_ms: durationMs,
      ordinal: ordinal++,
    };
    if (t.failureMessages && t.failureMessages.length > 0) {
      tc.error_message = t.failureMessages.join('\n');
      tc.error_stack = tc.error_message;
    }
    cases.push(tc);
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

// normalizeSpecPath strips a Jest testResults[].name absolute path down to
// the repo-relative convention discoverDetoxSpecs() produces
// (path.relative(detoxDir, full)) — e.g.
//   /home/runner/work/mattermost-mobile/mattermost-mobile/detox/e2e/test/products/x.e2e.ts
//   -> products/x.e2e.ts
// Falls back to the bare basename if the e2e/test/ marker isn't found
// (defensive — every real CI path has it).
function normalizeSpecPath(rawName) {
  const marker = '/e2e/test/';
  const idx = rawName.lastIndexOf(marker);
  if (idx === -1) {
    const parts = rawName.split('/').filter(Boolean);
    return parts[parts.length - 1] || rawName;
  }
  return rawName.slice(idx + marker.length);
}

// collectSpecFiles returns every testResults[].name in a Jest run.
function collectSpecFiles(jestJson) {
  return ((jestJson && jestJson.testResults) || []).map((f) => f.name).filter(Boolean);
}

module.exports = { aggregateSpec, collectSpecFiles, normalizeSpecPath, detoxStatus };

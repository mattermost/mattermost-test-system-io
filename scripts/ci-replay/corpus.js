// Builds a replay dataset for one named CI group from real historical
// artifacts under CI_RUNS_ROOT (default .local/mattermost-ci for the
// cypress-*/playwright-* groups, .local/mattermost-mobile-ci for the
// detox-* groups — see README.md).
//
// Each debug-dir (`<group>-debug-N/` for cypress/playwright, `{ios,android}-
// results-<id>-N/` for detox) is one historical worker's session. Since
// replay-time dispatch order won't match history's, this module pools every
// recorded (spec_path -> outcome) sample across all debug-dirs in the group
// into one lookup, and dispatches the union of every spec_path seen — so
// every leased spec is guaranteed at least one recorded sample. Discards
// real inter-spec timing/ordering correlation (accepted limitation).

'use strict';

const fs = require('fs');
const path = require('path');
const { parseMochawesomeJson } = require('../lib/cypress-mochawesome-parser');
const {
  aggregateSpec: aggregatePlaywrightSpec,
  collectSpecFiles,
} = require('../lib/playwright-json-reporter-parser');
const {
  aggregateSpec: aggregateDetoxSpec,
  collectSpecFiles: collectDetoxSpecFiles,
  normalizeSpecPath: normalizeDetoxSpecPath,
} = require('../lib/detox-jest-results-parser');
const {
  parseMaestroReport,
  aggregateSpec: aggregateMaestroSpec,
  collectSpecFiles: collectMaestroSpecFiles,
  normalizeSpecPath: normalizeMaestroSpecPath,
} = require('../lib/maestro-junit-parser');

// Directory-name prefixes as they exist on disk (note the inconsistent
// double-dash on non-fips groups vs single-dash on fips). Detox/Maestro
// groups are keyed by the mattermost-mobile artifact-name prefix instead —
// that corpus has no fips split.
const GROUP_PREFIXES = {
  'cypress-full': 'cypress-full--debug-',
  'cypress-full-fips': 'cypress-full-fips-debug-',
  'playwright-full': 'playwright-full--debug-',
  'playwright-full-fips': 'playwright-full-fips-debug-',
  'detox-ios': 'ios-results-',
  'detox-android': 'android-results-',
  'detox-ipad': 'ipad-results-',
  'maestro-ios': 'maestro-ios-results-',
  'maestro-android': 'maestro-android-results-',
};

function frameworkForGroup(group) {
  if (group.startsWith('maestro')) return 'maestro';
  if (group.startsWith('detox')) return 'detox';
  return group.startsWith('playwright') ? 'playwright' : 'cypress';
}

// Override with CI_RUNS_ROOT if `gh run download` was pointed somewhere
// else. Otherwise defaults by framework: the Detox/Maestro corpus comes
// from a different repo (mattermost-mobile) than Cypress/Playwright's
// (mattermost), so it lives under its own default directory.
function resolveRoot(group) {
  if (process.env.CI_RUNS_ROOT) return path.resolve(process.cwd(), process.env.CI_RUNS_ROOT);
  const framework = frameworkForGroup(group);
  const dirName = framework === 'detox' || framework === 'maestro' ? 'mattermost-mobile-ci' : 'mattermost-ci';
  return path.resolve(__dirname, '..', '..', '.local', dirName);
}

function listDebugDirs(group, root) {
  const prefix = GROUP_PREFIXES[group];
  if (!prefix) {
    throw new Error(
      `unknown group ${JSON.stringify(group)}; expected one of ${Object.keys(GROUP_PREFIXES).join(', ')}`,
    );
  }
  if (!fs.existsSync(root)) {
    throw new Error(`corpus root not found: ${root}`);
  }
  return listSubdirs(root)
    .filter((d) => d.startsWith(prefix))
    .sort()
    .map((d) => path.join(root, d));
}

// iter-0, iter-1, ..., iter-10 — numeric sort so iter-10 doesn't land
// between iter-1 and iter-2.
function listIterDirsSorted(runDir) {
  if (!fs.existsSync(runDir) || !fs.statSync(runDir).isDirectory()) return [];
  return fs
    .readdirSync(runDir)
    .filter((d) => /^iter-\d+$/.test(d))
    .sort((a, b) => Number(a.slice(5)) - Number(b.slice(5)))
    .map((d) => path.join(runDir, d));
}

// listImageFiles recursively collects absolute paths of .png/.jpg/.jpeg
// under dir. Used per-spec for Cypress screenshotFiles, scoped to
// iterDir/output/<spec_basename>/ so specs never share screenshots.
function listImageFiles(dir) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { recursive: true, withFileTypes: true });
  } catch {
    return [];
  }
  const out = [];
  for (const ent of entries) {
    if (!ent.isFile()) continue;
    const ext = path.extname(ent.name).toLowerCase();
    if (ext !== '.png' && ext !== '.jpg' && ext !== '.jpeg') continue;
    const entDir = ent.parentPath || ent.path || dir;
    out.push(path.join(entDir, ent.name));
  }
  return out;
}

// listSubdirs returns only the directory entries of dir (stray files like
// macOS .DS_Store are real and present in this corpus).
function listSubdirs(dir) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return [];
  }
  return entries.filter((e) => e.isDirectory()).map((e) => e.name);
}

function normalizeCypressSpecPath(rawFile) {
  return rawFile.split(path.sep).join('/');
}

// Reconstructs the same spec_path convention production uses (playwright
// config's testDir is conventionally "specs/").
function normalizePlaywrightSpecPath(rawFile) {
  return `specs/${rawFile.split(path.sep).join('/')}`;
}

// One iteration's archived Mochawesome JSON is a single file named after
// the spec (reporter-config.json's reportFilename: 'json/tests/[name]').
// Returns [] entries as {specPath, sample}.
function loadCypressIter(iterDir) {
  const out = [];
  let entries;
  try {
    entries = fs.readdirSync(iterDir);
  } catch {
    return out;
  }
  for (const f of entries) {
    if (!f.endsWith('.json')) continue;
    let parsed;
    try {
      parsed = JSON.parse(fs.readFileSync(path.join(iterDir, f), 'utf8'));
    } catch {
      continue;
    }
    const top = (parsed.results || [])[0];
    // top.file only — top.fullFile is absolute and normalizeCypressSpecPath
    // just swaps separators, so falling back to it would produce a
    // spec_path that doesn't match production's repo-relative convention.
    const rawFile = top && top.file;
    if (!rawFile) continue;
    const specPath = normalizeCypressSpecPath(rawFile);
    const { aggregateStatus, testCases } = parseMochawesomeJson(parsed);
    const durationMs = testCases.reduce((acc, c) => acc + (c.duration_ms || 0), 0);
    // sourcePath/iterDir/screenshotFiles: only used by UPLOAD_SHARDS.
    // iterDir is the directory whose `output` subdir holds this
    // invocation's screenshots — for Cypress, the raw iter dir itself.
    out.push({
      specPath,
      sample: {
        status: aggregateStatus,
        actual_duration_ms: durationMs,
        test_cases: testCases,
        sourcePath: path.join(iterDir, f),
        iterDir,
        screenshotFiles: listImageFiles(path.join(iterDir, 'output', path.basename(specPath))),
      },
    });
  }
  return out;
}

// One iteration's archived Playwright reporter JSON lives at
// results/reporter/results.json (see main.ts / playwright.ts's runUnit).
function loadPlaywrightIter(iterDir) {
  const jsonPath = path.join(iterDir, 'results', 'reporter', 'results.json');
  if (!fs.existsSync(jsonPath)) return [];
  let parsed;
  try {
    parsed = JSON.parse(fs.readFileSync(jsonPath, 'utf8'));
  } catch {
    return [];
  }
  const fallbackMs =
    parsed.stats && typeof parsed.stats.duration === 'number' ? parsed.stats.duration : 0;
  // Playwright nests JSON+screenshots under iter-N/results/, so iterDir
  // points there (unlike Cypress's raw iter dir) to keep `iterDir/output`
  // uniform. No per-sample screenshotFiles — shard upload walks the whole
  // directory once per invocation instead.
  const resultsIterDir = path.join(iterDir, 'results');
  const out = [];
  for (const rawFile of collectSpecFiles(parsed)) {
    const specPath = normalizePlaywrightSpecPath(rawFile);
    const result = aggregatePlaywrightSpec(parsed, rawFile, fallbackMs);
    out.push({
      specPath,
      sample: {
        status: result.status,
        actual_duration_ms: result.actual_duration_ms,
        test_cases: result.test_cases,
        sourcePath: jsonPath,
        iterDir: resultsIterDir,
      },
    });
  }
  return out;
}

// One shard's archived Jest JSON is a single `jest-results.json` directly in
// the shard dir (unlike Cypress/Playwright, there's no worker-artifacts/
// iter-N nesting here — mattermost-mobile's e2e-{ios,android}-template.yml
// downloads one flat per-matrix-worker artifact, not a per-checkout-unit
// archive, since Detox isn't yet orchestrated via begin/checkout/complete).
// A shard's jest-results.json batches every spec that matrix worker ran, so
// (unlike Cypress/Playwright) one shard yields many samples, not one.
function loadDetoxShard(shardDir) {
  const jsonPath = path.join(shardDir, 'jest-results.json');
  if (!fs.existsSync(jsonPath)) return [];
  let parsed;
  try {
    parsed = JSON.parse(fs.readFileSync(jsonPath, 'utf8'));
  } catch {
    return [];
  }
  const out = [];
  for (const rawFile of collectDetoxSpecFiles(parsed)) {
    const specPath = normalizeDetoxSpecPath(rawFile);
    const fileEntry = parsed.testResults.find((f) => f.name === rawFile);
    const result = aggregateDetoxSpec(fileEntry, specPath);
    // No nested results/output dir and no screenshots in this corpus —
    // the whole shard dir stands in for iterDir/sourcePath uniformity with
    // the other two frameworks' Sample shape.
    out.push({
      specPath,
      sample: {
        status: result.status,
        actual_duration_ms: result.actual_duration_ms,
        test_cases: result.test_cases,
        sourcePath: jsonPath,
        iterDir: shardDir,
        screenshotFiles: [],
      },
    });
  }
  return out;
}

// One run's downloaded artifact is `maestro-{ios,android}-results-<runid>/
// maestro-report.xml` — a merged JUnit file with one <testsuite> per flow
// (see maestro-junit-parser.js's doc comment), analogous to Detox's single
// jest-results.json batching every spec a shard ran. No nested worker-
// artifacts/iter-N here either, and no per-flow screenshots in this corpus.
function loadMaestroShard(shardDir) {
  const xmlPath = path.join(shardDir, 'maestro-report.xml');
  if (!fs.existsSync(xmlPath)) return [];
  let parsed;
  try {
    parsed = parseMaestroReport(fs.readFileSync(xmlPath, 'utf8'));
  } catch {
    return [];
  }
  const out = [];
  for (const rawName of collectMaestroSpecFiles(parsed)) {
    const specPath = normalizeMaestroSpecPath(rawName);
    const testsuiteEntry = parsed.testsuites.find((ts) => ts.name === rawName);
    const result = aggregateMaestroSpec(testsuiteEntry, specPath);
    out.push({
      specPath,
      sample: {
        status: result.status,
        actual_duration_ms: result.actual_duration_ms,
        test_cases: result.test_cases,
        sourcePath: xmlPath,
        iterDir: shardDir,
        screenshotFiles: [],
      },
    });
  }
  return out;
}

// loadCorpus builds the replay dataset for one named group.
//
// Returns { framework, specPaths (sorted union, feeds /begin), samplesBySpec
// (Map<specPath, Sample[]>), workerCount (debug-dirs found), singleSampleSpecPaths
// (specs seen in only one debug-dir) }.
function loadCorpus(group) {
  const framework = frameworkForGroup(group);
  const root = resolveRoot(group);
  const debugDirs = listDebugDirs(group, root);
  if (debugDirs.length === 0) {
    throw new Error(`no debug-dirs found for group ${group} under ${root}`);
  }

  const samplesBySpec = new Map();
  const debugDirsSeenBySpec = new Map();

  const record = (specPath, sample, debugDir) => {
    let arr = samplesBySpec.get(specPath);
    if (!arr) {
      arr = [];
      samplesBySpec.set(specPath, arr);
    }
    arr.push(sample);

    let seenIn = debugDirsSeenBySpec.get(specPath);
    if (!seenIn) {
      seenIn = new Set();
      debugDirsSeenBySpec.set(specPath, seenIn);
    }
    seenIn.add(debugDir);
  };

  if (framework === 'detox' || framework === 'maestro') {
    const loadShard = framework === 'detox' ? loadDetoxShard : loadMaestroShard;
    for (const debugDir of debugDirs) {
      for (const { specPath, sample } of loadShard(debugDir)) {
        record(specPath, sample, debugDir);
      }
    }
  } else {
    for (const debugDir of debugDirs) {
      const artifactsRoot = path.join(debugDir, 'worker-artifacts');
      for (const ghRunId of listSubdirs(artifactsRoot)) {
        const runDir = path.join(artifactsRoot, ghRunId);
        for (const iterDir of listIterDirsSorted(runDir)) {
          const entries = framework === 'cypress' ? loadCypressIter(iterDir) : loadPlaywrightIter(iterDir);
          for (const { specPath, sample } of entries) {
            record(specPath, sample, debugDir);
          }
        }
      }
    }
  }

  const specPaths = [...samplesBySpec.keys()].sort();
  const singleSampleSpecPaths = specPaths.filter((sp) => debugDirsSeenBySpec.get(sp).size === 1);

  return { framework, specPaths, samplesBySpec, workerCount: debugDirs.length, singleSampleSpecPaths };
}

// percentileDurationMs returns the p-th percentile (0-100) of
// actual_duration_ms across every sample in the corpus.
function percentileDurationMs(samplesBySpec, p) {
  const durations = [];
  for (const samples of samplesBySpec.values()) {
    for (const s of samples) durations.push(s.actual_duration_ms || 0);
  }
  if (durations.length === 0) return 0;
  durations.sort((a, b) => a - b);
  const idx = Math.min(durations.length - 1, Math.floor((p / 100) * durations.length));
  return durations[idx];
}

module.exports = { loadCorpus, percentileDurationMs, GROUP_PREFIXES };

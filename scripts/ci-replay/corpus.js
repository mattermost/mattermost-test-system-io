// Builds a replay dataset for one named CI group from real historical
// artifacts under CI_RUNS_ROOT (default .local/mattermost-ci — see README.md).
//
// Each `<group>-debug-N/` directory is one historical worker's session.
// Since replay-time dispatch order won't match history's, this module pools
// every recorded (spec_path -> outcome) sample across all debug-dirs in the
// group into one lookup, and dispatches the union of every spec_path seen —
// so every leased spec is guaranteed at least one recorded sample. Discards
// real inter-spec timing/ordering correlation (accepted limitation).

'use strict';

const fs = require('fs');
const path = require('path');
const { parseMochawesomeJson } = require('../lib/cypress-mochawesome-parser');
const {
  aggregateSpec: aggregatePlaywrightSpec,
  collectSpecFiles,
} = require('../lib/playwright-json-reporter-parser');

// Override with CI_RUNS_ROOT if `gh run download` was pointed somewhere else.
const CI_RUNS_ROOT = process.env.CI_RUNS_ROOT
  ? path.resolve(process.cwd(), process.env.CI_RUNS_ROOT)
  : path.resolve(__dirname, '..', '..', '.local', 'mattermost-ci');

// Directory-name prefixes as they exist on disk (note the inconsistent
// double-dash on non-fips groups vs single-dash on fips).
const GROUP_PREFIXES = {
  'cypress-full': 'cypress-full--debug-',
  'cypress-full-fips': 'cypress-full-fips-debug-',
  'playwright-full': 'playwright-full--debug-',
  'playwright-full-fips': 'playwright-full-fips-debug-',
};

function frameworkForGroup(group) {
  return group.startsWith('playwright') ? 'playwright' : 'cypress';
}

function listDebugDirs(group) {
  const prefix = GROUP_PREFIXES[group];
  if (!prefix) {
    throw new Error(
      `unknown group ${JSON.stringify(group)}; expected one of ${Object.keys(GROUP_PREFIXES).join(', ')}`,
    );
  }
  if (!fs.existsSync(CI_RUNS_ROOT)) {
    throw new Error(`corpus root not found: ${CI_RUNS_ROOT}`);
  }
  return listSubdirs(CI_RUNS_ROOT)
    .filter((d) => d.startsWith(prefix))
    .sort()
    .map((d) => path.join(CI_RUNS_ROOT, d));
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
    const rawFile = top && (top.file || top.fullFile);
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

// loadCorpus builds the replay dataset for one named group.
//
// Returns { framework, specPaths (sorted union, feeds /begin), samplesBySpec
// (Map<specPath, Sample[]>), workerCount (debug-dirs found), singleSampleSpecPaths
// (specs seen in only one debug-dir) }.
function loadCorpus(group) {
  const framework = frameworkForGroup(group);
  const debugDirs = listDebugDirs(group);
  if (debugDirs.length === 0) {
    throw new Error(`no debug-dirs found for group ${group} under ${CI_RUNS_ROOT}`);
  }

  const samplesBySpec = new Map();
  const debugDirsSeenBySpec = new Map();

  for (const debugDir of debugDirs) {
    const artifactsRoot = path.join(debugDir, 'worker-artifacts');
    for (const ghRunId of listSubdirs(artifactsRoot)) {
      const runDir = path.join(artifactsRoot, ghRunId);
      for (const iterDir of listIterDirsSorted(runDir)) {
        const entries = framework === 'cypress' ? loadCypressIter(iterDir) : loadPlaywrightIter(iterDir);
        for (const { specPath, sample } of entries) {
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

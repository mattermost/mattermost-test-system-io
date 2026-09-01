// W10 — mechanical stabilization-PR bans.
//
// A CI check on AGENT-AUTHORED stabilization PRs that rejects diffs which add
// any of six banned patterns. The bans stay enforcing only on agent-authored
// stabilization PRs — never on normal product PRs — and the same checker
// doubles as a read-only audit of a whole branch (report-only mode, W16
// item 9).
//
// Known limit (state in the PR description): these are additive-diff rules. A
// semantic weakening — rewriting an assertion weaker, loosening a selector,
// hiding a wait inside a helper — can pass. That residue is the human
// reviewer's job and is why review stays mandatory.
//
// The human-override label (the one thing that may waive a ban) is enforced
// workflow-side: the workflow must check the label was applied by a non-agent
// actor. This module only reports.
//
// Runs under node's stdlib only — no deps. Test:
//   node --test scripts/lib/stabilization-ban-checker.test.js

'use strict';

/** Spec roots the bans apply inside (W0 #21 — the real E2E paths). */
const DEFAULT_ROOTS = ['e2e-tests/'];

/** Config files where retry/timeout raises are banned too. */
const CONFIG_RE = /(?:playwright|cypress|detox)\.config\.[cm]?[jt]s$/;

// --- rule matchers: each sees one file's added+removed lines ---

const RULES = [
  {
    rule: 'ban-bare-wait',
    message: 'bare sleep / fixed wait added — poll for the condition instead',
    match(added) {
      return added.some((l) =>
        /waitForTimeout\s*\(/.test(l.text) ||
        /cy\.wait\s*\(\s*\d/.test(l.text) ||
        /new Promise\s*\([^)]*=>\s*setTimeout/.test(l.text) ||
        /\bsleep\s*\(\s*\d/.test(l.text),
      );
    },
  },
  {
    rule: 'ban-retry-wrapper',
    message: 'retry added around previously un-retried behavior — fix the cause, not the symptom',
    match(added, file) {
      const inConfig = CONFIG_RE.test(file.path);
      return added.some((l) =>
        /retries\s*:\s*\d/.test(l.text) ||
        /retries\s*=\s*\d/.test(l.text) ||
        /jest\.retryTimes\s*\(/.test(l.text) ||
        /test\.describe\.configure\s*\(/.test(l.text) ||
        /\.retry\(\s*\d/.test(l.text) ||
        (inConfig && /retry/i.test(l.text)),
      );
    },
  },
  {
    rule: 'ban-loosened-assertion',
    message: 'assertion loosened — a weaker matcher replaced a stronger one, or a soft assertion was added',
    match(added, file) {
      // expect.soft is always a ban: it never fails the run.
      if (added.some((l) => /expect\.soft\s*\(/.test(l.text))) return true;
      // A weak matcher added in a file that also REMOVED an assertion line is
      // the loosening shape (strict matcher gone, weak one arrived).
      const weakAdded = added.some((l) =>
        /\.toBeTruthy\s*\(|\.toBeDefined\s*\(|\.toBeNull\s*\(|\.toBeGreaterThan\s*\(\s*-?\s*1\s*\)/.test(l.text),
      );
      const removedAssertion = file.removed.some((l) => /\bexpect\s*\(|\bassert\b|\.should\(/.test(l.text));
      return weakAdded && removedAssertion;
    },
  },
  {
    rule: 'ban-deleted-assertion',
    message: 'assertion deleted without replacement — coverage was removed, not fixed',
    match(added, file) {
      const removedAsserts = file.removed.filter((l) => /\bexpect\s*\(|\bassert\s*\(|\.should\(/.test(l.text)).length;
      const addedAsserts = added.filter((l) => /\bexpect\s*\(|\bassert\s*\(|\.should\(/.test(l.text)).length;
      return removedAsserts > addedAsserts;
    },
  },
  {
    rule: 'ban-skip-tag',
    message: 'skip/ignore tag added — the test is being silenced, not stabilized',
    match(added) {
      return added.some((l) =>
        /\b(?:test|it|describe)\.(?:skip|fixme)\b/.test(l.text) ||
        /\bxit\s*\(|\bxdescribe\s*\(/.test(l.text) ||
        /@Ignore\b/.test(l.text) ||
        /test\.skip\s*\(/.test(l.text),
      );
    },
  },
  {
    rule: 'ban-raised-timeout',
    message: 'timeout raised — waiting longer is masking, not fixing',
    match(added, file) {
      const inConfig = CONFIG_RE.test(file.path);
      return added.some(
        (l) =>
          // Test/suite/config-level raises only. An ASSERTION-level timeout
          // (`expect(locator).toBeVisible({ timeout: 10_000 })`) is the
          // recommended stabilization fix, not a ban — the polling assertion
          // replaces a fixed sleep.
          /setTestTimeout\s*\(/.test(l.text) ||
          /test\.setTimeout\s*\(/.test(l.text) ||
          /setDefaultTimeout\s*\(/.test(l.text) ||
          /defaultCommandTimeout\s*[:=]/.test(l.text) ||
          /jest\.setTimeout\s*\(/.test(l.text) ||
          /actionTimeout\s*[:=]/.test(l.text) ||
          /navigationTimeout\s*[:=]/.test(l.text) ||
          (inConfig && /timeout\s*[:=]\s*\d/.test(l.text)),
      );
    },
  },
];

// --- diff parsing ---

/** Parse a unified diff into per-file added/removed line lists. */
function parseUnifiedDiff(diffText) {
  const files = [];
  let current = null;
  let addedNo = 0;
  let removedNo = 0;
  for (const raw of String(diffText || '').split('\n')) {
    if (raw.startsWith('+++ b/')) {
      current = { path: raw.slice(6), added: [], removed: [] };
      files.push(current);
      continue;
    }
    if (!current) continue;
    if (raw.startsWith('@@')) {
      const m = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
      removedNo = m ? Number(m[1]) : 0;
      addedNo = m ? Number(m[2]) : 0;
      continue;
    }
    if (raw.startsWith('+') && !raw.startsWith('+++')) {
      current.added.push({ no: addedNo++, text: raw.slice(1) });
    } else if (raw.startsWith('-') && !raw.startsWith('---')) {
      current.removed.push({ no: removedNo++, text: raw.slice(1) });
    }
  }
  return files;
}

/**
 * Check a unified diff. Options:
 *   roots       — editable roots the bans apply inside (default e2e-tests/)
 *   reportOnly  — violations are reported, never failing
 *   configFiles — also apply config-shaped rules to *.config.* files
 *                 (default true; config edits outside roots are still checked
 *                 because a raised timeout in a config is global)
 */
function checkStabilizationDiff(diffText, opts = {}) {
  const roots = opts.roots || DEFAULT_ROOTS;
  const reportOnly = Boolean(opts.reportOnly);
  const checkConfigs = opts.configFiles !== false;

  const violations = [];
  let checkedFiles = 0;
  let skippedFiles = 0;

  for (const file of parseUnifiedDiff(diffText)) {
    const inRoots = roots.some((root) => file.path.startsWith(root));
    const isConfig = CONFIG_RE.test(file.path);
    if (!inRoots && !(isConfig && checkConfigs)) {
      skippedFiles++;
      continue; // the bans never apply to product code — that is W10's own rule
    }
    if (file.added.length === 0) {
      continue; // pure deletion of a flaky spec is a legitimate stabilization
    }
    checkedFiles++;
    for (const r of RULES) {
      if (r.match(file.added, file)) {
        violations.push({
          rule: r.rule,
          file: file.path,
          message: r.message,
        });
      }
    }
  }

  return {
    passed: reportOnly || violations.length === 0,
    reportOnly,
    checkedFiles,
    skippedFiles,
    violations,
  };
}

module.exports = { parseUnifiedDiff, checkStabilizationDiff, DEFAULT_ROOTS, CONFIG_RE };

// --- CLI: node scripts/lib/stabilization-ban-checker.js <diff-file|-> [--report-only] ---

if (require.main === module) {
  const args = process.argv.slice(2);
  const reportOnly = args.includes('--report-only');
  const target = args.find((a) => !a.startsWith('--')) || '-';
  const input = target === '-' ? require('fs').readFileSync(0, 'utf8') : require('fs').readFileSync(target, 'utf8');
  const result = checkStabilizationDiff(input, { reportOnly });
  if (result.violations.length === 0) {
    console.log(`stabilization-ban-checker: clean (${result.checkedFiles} file(s) checked)`);
  } else {
    console.log(`stabilization-ban-checker: ${result.violations.length} violation(s)`);
    for (const v of result.violations) {
      console.log(`  ${v.file}: [${v.rule}] ${v.message}`);
    }
    if (reportOnly) console.log('report-only mode: not failing');
  }
  process.exit(result.passed ? 0 : 1);
}
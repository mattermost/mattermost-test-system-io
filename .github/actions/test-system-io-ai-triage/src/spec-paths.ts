/**
 * Re-rooting a report's spec path to a repo-relative one.
 *
 * TSIO ingests Playwright/Cypress JSON with paths relative to the framework's
 * spec dir (playwright's testDir is `e2e-tests/playwright/specs`), so an
 * evidence file like
 * "functional/channels/team_settings/team_settings_policy_editor.spec.ts"
 * does not exist at that path in the repository. The agent's get_test_source
 * tool has to re-root it before it can read the failing test — without this
 * the tool silently returns nothing and the model adjudicates blind.
 *
 * This lived in fixer.ts, which was removed with the agent fix loop. The fixer
 * needed it for a different reason (deciding what it was allowed to edit); the
 * triage path needs it to read source, which is the half that actually runs.
 */

/** Framework spec roots, repo-relative. */
export const SPEC_ROOTS = ["e2e-tests/playwright/specs/", "e2e-tests/cypress/tests/integration/"];

/**
 * Candidate repo-relative paths for a reported spec file, best-effort and
 * deterministic (no filesystem access). Callers try them in order.
 *
 * Non-spec paths return nothing on purpose: a product source file or a stray
 * data file must never be re-rooted into `e2e-tests/`.
 */
export function repoRelSpecCandidates(file: string): string[] {
  const norm = file.replace(/^\.\//, "").replace(/^\/+/, "");
  if (norm.startsWith("e2e-tests/")) return [norm];
  if (!/\.(spec|test)\.(ts|tsx|js|mjs)$/.test(norm) && !/_spec\.(js|ts)$/.test(norm)) {
    return [];
  }
  return SPEC_ROOTS.map((root) => root + norm);
}

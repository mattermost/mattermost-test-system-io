// Round-7 live demo driver. NOT part of the action; deleted after the demo.
// Feeds the REAL evidence packs from the running server into the REAL decide().
import { decide } from "./policy.ts";
import type { EvidencePack, ClaudeVerdict } from "./types.ts";
import { readFileSync } from "node:fs";

const SP = process.env.SP!;

// Changed files per PR, as GitHub would report them.
const CHANGED: Record<string, string[]> = {
  A: ["webapp/channels/src/components/search/search_bar.tsx"],
  B: ["webapp/channels/src/components/drafts/drafts.tsx"],
  // The real ABAC cherry-pick: 30 files, all under .github/**.
  C: [".github/workflows/e2e-tests.yml", ".github/actions/e2e-setup/action.yml"],
  D: ["webapp/channels/src/components/common/typo.tsx"],
  E: ["webapp/channels/src/components/emoji/emoji_picker_tweak.tsx"],
  F: ["webapp/channels/src/components/profile_popover/profile_popover.tsx"],
  G: ["webapp/channels/src/components/unrelated/thing.tsx"],
};

// My verdicts as the model (Opus 5), from the evidence packs only, applying
// the rule table in agent.ts. `undefined` = the deterministic layer set
// needs_ai=false, so no model call is made at all.
const MODEL: Record<string, ClaudeVerdict | undefined> = {
  A: {
    verdict: "FLAKY_TEST",
    confidence: 0.9,
    reason:
      "Bare `locator.click` timeout with no wrong product state. Baseline is 40% failing with 15 flips, and the rate did NOT shift here (1-of-3, p=0.784) — this much failure is exactly what this test's own history predicts. The PR touches search components; the stack names only the spec. Rule 5: UI timing race.",
    gist: "Channel switcher click timed out — same timing race this test shows on master, not your change.",
    citations: ["history", "pr_diff", "rate_shift", "error_message"],
    chronic: true,
  },
  B: {
    verdict: "PR_REGRESSION",
    confidence: 0.95,
    reason:
      'Not a timeout: `Expected "hello" Received ""` is a WRONG PRODUCT STATE — the draft did not persist. Rule 2. Baseline is spotless (0/20), the rate shifted hard (3-of-3, p=0.000), and the stack names drafts.tsx which this PR edits.',
    gist: "Draft came back empty after reload — drafts.tsx no longer persists text.",
    citations: ["error_message", "history", "pr_diff", "rate_shift"],
  },
  C: {
    verdict: "PR_REGRESSION",
    confidence: 0.88,
    reason:
      'Not a timeout: the policy row is absent from the list after search (Expected true, Received false) — a wrong product state, Rule 2. The test is 40% flaky on master, but the rate SHIFTED here (3-of-3, p=0.064), which historical flakiness does not explain. The diff is .github/** only, which is how the test server boots (testcontainers baseURL) — a permissions view failing right after a boot-config change is causal, not coincidence.',
    gist: "ABAC policy row missing after search — the CI boot-config change broke the permissions view.",
    citations: ["error_message", "history", "pr_diff", "rate_shift"],
  },
  D: undefined, // deterministic MAIN_REGRESSION, needs_ai=false
  E: undefined, // status=flaky -> deterministic FLAKY_TEST conf 1.0, needs_ai=false
  // G is the quarantined test: the deterministic layer suggests FLAKY_TEST and
  // the quarantine decides the check, so no model verdict is needed.
  G: undefined,
  F: {
    verdict: "FLAKY_TEST",
    confidence: 0.9,
    reason:
      "`expect(reply).toBeVisible` timeout, no wrong product state. Baseline is 2/20 with 4 flips and the rate did not shift here (1-of-3, p=0.271). The PR touches the profile popover, unrelated to threads. Rule 5: UI timing race.",
    gist: "Thread reply took too long to appear — this test's usual timing race, not your change.",
    citations: ["history", "pr_diff", "rate_shift", "error_message"],
  },
};

const EXPECT: Record<string, string> = {
  A: "success (flaky, not yours)",
  B: "failure (your PR broke it)",
  C: "failure (flaky test that really broke — round 6 waived this)",
  D: "success on the bystander PR (pre-existing on master)",
  E: "success (measured flake: failed then recovered on retry, 5% on master)",
  F: "success (10% flake, rate did not shift)",
  G: "success (quarantined: 15% on master, hits 6 PRs)",
};

for (const tag of ["A", "B", "C", "D", "E", "F", "G"]) {
  const pack: EvidencePack = JSON.parse(readFileSync(`${SP}/ev_${tag}.json`, "utf8"));
  const c = pack.clusters[0]!;
  const d = decide({
    failure: c.representative,
    runType: "PR",
    branch: pack.group.branch,
    changedFiles: CHANGED[tag]!,
    ai: MODEL[tag],
    phase: 1, // PR gate
  });
  console.log(`\n===== Scenario ${tag} — PR #${pack.group.gh_pr_number} — ${c.representative.external_test_id}`);
  console.log(`  expected      : ${EXPECT[tag]}`);
  console.log(`  model verdict : ${MODEL[tag] ? MODEL[tag]!.verdict + " @ " + MODEL[tag]!.confidence : "(no model call — deterministic)"}`);
  console.log(`  FINAL verdict : ${d.verdict}  source=${d.source}  kind=${d.kind}`);
  console.log(`  WAIVED        : ${d.waived}`);
  console.log(`  CHECK STATE   : ${d.check_state.toUpperCase()}`);
  console.log(`  reason        : ${d.reason}`);
  console.log(`  gist          : ${d.gist ?? "-"}`);
  console.log(`  citations     : ${d.citations.join(", ")}`);
  const am = c.representative.amnesty;
  console.log(`  amnesty       : granted=${am?.granted} (${am?.reason})`);
}

// Goal 3 — the same master failure seen from a MAIN run: never waived.
const packD: EvidencePack = JSON.parse(readFileSync(`${SP}/ev_D.json`, "utf8"));
const mainRun = decide({
  failure: packD.clusters[0]!.representative,
  runType: "MAIN",
  branch: "main",
  changedFiles: [],
  phase: 2, // master gating enabled
});
console.log(`\n===== Goal 3 — the SAME failure on a MAIN run (phase 2)`);
console.log(`  FINAL verdict : ${mainRun.verdict}`);
console.log(`  WAIVED        : ${mainRun.waived}`);
console.log(`  CHECK STATE   : ${mainRun.check_state.toUpperCase()}`);
console.log(`  reason        : ${mainRun.reason}`);
const h = packD.clusters[0]!.representative.history!;
console.log(`  attribution   : last_pass=${h.last_pass_commit}  failing_since=${h.failing_since_commit}`);

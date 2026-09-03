import type { ClaudeVerdict, Decision, EvidenceFailure, Suggestion } from "./types.ts";
import { kindOf } from "./blame.ts";

export const WAIVE_CONFIDENCE = 0.85;

/**
 * R7-E — the minimum baseline history a FLAKY_* waiver needs.
 *
 * The round-6 bar reads: "INCONCLUSIVE on <3-run tests | 100% | Never guess
 * without history." That bar existed only in a document. Nothing in canWaive
 * checked history depth, so a brand-new test with runs=0 and a confident model
 * flake verdict was waivable — the deterministic layer correctly returns
 * INCONCLUSIVE for runs=0, the model overrides it to FLAKY_* at 0.87, and every
 * remaining gate passes (amnesty grants at rate 0, the rate-shift test needs
 * >= 5 baseline runs so it cannot fire, and diff overlap is false when the PR
 * is elsewhere).
 *
 * Observed live on mattermost#38154: three clusters, all runs=0, all
 * FLAKY_* at 0.87-0.88. Nothing was waived only because the run was in
 * shadow mode. In gate mode all three would have greened the check on no
 * history at all — which is what this floor now refuses on its own.
 */
export const MIN_HISTORY_RUNS_FOR_WAIVER = 3;

/**
 * B2/B3: nothing greens unless the ledger recorded it. Gate mode refuses the
 * flip on any ledger failure; only shadow mode may observe without a ledger
 * row (and it flips nothing anyway).
 */
export function mayFlipChecks(
  mode: string,
  ledgerOK: boolean,
): { allowed: boolean; reason?: string } {
  if (ledgerOK) return { allowed: true };
  if (mode !== "gate")
    return {
      allowed: true,
      reason: "shadow mode observes without flipping — ledger skip tolerated",
    };
  return {
    allowed: false,
    reason: "ledger write failed — refusing to flip: a waiver without a ledger row is silent",
  };
}

/**
 * The full waiver decision: policy (canWaive) AND the run's mode.
 *
 * Gating is owned by the caller's workflow, not by server state: the action
 * only ever waives in gate mode, so a workflow not wired to gate cannot green
 * anything, whatever the classifier or the model concluded. RELEASE runs may
 * be in gate mode too, but neverAutoWaive means nothing is waivable there —
 * release trains stay fail-closed by policy, not by mode.
 */
export function canWaiveInMode(args: Parameters<typeof canWaive>[0] & { mode: string }): {
  waived: boolean;
  reason: string;
} {
  if ((args.mode || "").toLowerCase() !== "gate") {
    return {
      waived: false,
      reason: `shadow mode observes only — ${args.runType || "PR"} run flips nothing`,
    };
  }
  return canWaive(args);
}

const FLAKY = new Set(["FLAKY_TEST", "FLAKY_INFRA", "FLAKY_SERVER"]);
const NEVER_WAIVE = new Set(["PR_REGRESSION", "INCONCLUSIVE", "TEST_DEBT", "BUILD_OR_ENV_ERROR"]);

/**
 * Error text showing the product DELIBERATELY refusing an input (business-rule
 * rejection). Not infrastructure flake — the server answered correctly; the
 * test (or the environment) created input the product must refuse. Human
 * review required, regardless of model confidence.
 *
 * Patterns are deliberately narrow: they must describe the product saying
 * "no" to the action, not transport failures or filesystem EACCES noise.
 */
const PRODUCT_REJECTION: RegExp[] = [
  /would remove your access/i,
  /you cannot (?:save|delete|remove|deactivate|archive|update|change)\b/i,
  /you (?:do not|don't|dont) have permission/i,
  /not permitted to/i,
  /insufficient permission/i,
  /action is not allowed/i,
  /permission_required/i,
];

export function isProductRejection(error?: string, stack?: string): boolean {
  const text = `${error ?? ""}\n${stack ?? ""}`;
  return PRODUCT_REJECTION.some((re) => re.test(text));
}

/**
 * RELEASE / release-* never auto-waive (CMT / release trains stay fail-closed).
 * MAIN may waive confirmed flakes so required e2e-test/* checks on main go
 * green — otherwise Create Release Branches cannot push release-* from a
 * flaky main commit (no PR labels apply to that path).
 */
export function neverAutoWaive(runType: string, branch: string): boolean {
  const t = (runType || "").toUpperCase();
  if (t === "RELEASE") return true;
  const b = (branch || "").toLowerCase();
  return b.startsWith("release-") || b.startsWith("release/");
}

/** @deprecated use neverAutoWaive — kept for call sites during rename */
export function isProtectedRun(runType: string, branch: string): boolean {
  return neverAutoWaive(runType, branch);
}

/** Paths that every Detox run touches; editing them must not block flake waivers. */
export function isSharedHarness(path: string): boolean {
  return isMetaHarness(path) || /\.(test|spec)\.(ts|tsx|js|jsx)$/.test(normalizePath(path));
}

/**
 * Pure CI/harness paths that genuinely cannot affect a product failure.
 * Deliberately EXCLUDES e2e spec files (*.spec.ts, *.e2e.ts): a PR that edits
 * the failing spec is editing the failure's own area, never "CI-only".
 */
export function isMetaHarness(path: string): boolean {
  const p = normalizePath(path);
  return (
    p.startsWith(".github/") ||
    p.startsWith("detox/e2e/support/") ||
    p.startsWith("detox/utils/") ||
    p === "detox/create_android_emulator.sh" ||
    p.endsWith(".md")
  );
}

/** True when the PR only touched CI/harness/docs — not product or the failing spec. */
export function isCIOnlyDiff(changedFiles: string[]): boolean {
  const files = changedFiles.map(normalizePath).filter(Boolean);
  if (files.length === 0) return false;
  return files.every(isMetaHarness);
}

/** True when the PR changed the failing spec itself — the failure's own area. */
function touchesFailingSpec(changedFiles: string[], specFile?: string): boolean {
  if (!specFile) return false;
  const spec = normalizePath(specFile);
  return changedFiles.map(normalizePath).some((f) => pathsMatch(spec, f));
}

function normalizePath(p: string): string {
  return p.replace(/^\.\//, "").replace(/\\/g, "/");
}

/** True when the PR changed the failing spec itself (not a vague substring). */
function pathsMatch(spec: string, changed: string): boolean {
  if (spec === changed) return true;
  if (spec.endsWith("/" + changed) || changed.endsWith("/" + spec)) return true;
  const specBase = spec.split("/").pop() || "";
  const changedBase = changed.split("/").pop() || "";
  return Boolean(specBase) && specBase === changedBase && specBase.includes(".");
}

/** Stack frames mention the changed file as a path, not as an accidental substring. */
function stackMentions(stack: string, changed: string): boolean {
  if (changed.length < 8) return false;
  if (stack.includes(changed)) return true;
  const base = changed.split("/").pop() || "";
  if (base.length < 8 || !base.includes(".")) return false;
  return new RegExp(`(?:^|[\\s(/])${escapeRegExp(base)}(?::\\d|\\)|$)`, "m").test(stack);
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function diffOverlaps(changedFiles: string[], specFile?: string, stack?: string): boolean {
  const files = changedFiles.map(normalizePath);
  // The failing spec itself is always overlapping — even though isSharedHarness
  // treats unit tests and specs as harness, a PR editing the failing spec is
  // editing the failure's own area.
  if (touchesFailingSpec(files, specFile)) return true;
  const product = files.filter((f) => !isSharedHarness(f));
  if (!stack) return false;
  return product.some((f) => stackMentions(stack, f));
}

export function hasAdjudicationEvidence(failure: EvidenceFailure): boolean {
  if ((failure.screenshots || []).length > 0) return true;
  if ((failure.error_message || "").trim().length > 0) return true;
  if ((failure.error_stack || "").trim().length > 0) return true;
  return false;
}

const bystanderPreexisting = (args: {
  runType: string;
  verdict: string;
  citations: string[];
}): boolean =>
  args.verdict === "MAIN_REGRESSION" &&
  args.citations.includes("failing_on_baseline") &&
  (args.runType || "").toUpperCase() !== "MAIN";

/**
 * R7-C — the chronic-flake bystander carve-out.
 *
 * THE FAULT THIS FIXES. Two rules were exactly complementary, and together they
 * made the product's primary promise unreachable:
 *
 *   classify.go pre-tags FLAKY_TEST only when FailureRate >= 0.10
 *   amnesty denies a waiver     whenever   FailureRate >= 0.10  (inclusive)
 *
 * So the history-based flake verdict could NEVER be waived. Any test flakier
 * than 10% on master turned every PR that touched it red — including PRs whose
 * authors had nothing to do with it. Measured live on seeded data: a 40% flake
 * and a 10% flake both came back FAILURE with reason "amnesty denied", while
 * the model's verdict was correct in both cases. No amount of model quality
 * could have fixed that.
 *
 * THE PRINCIPLE, already written into the W4 carve-out below: amnesty's pain
 * must land on master and on the test's owner, never on a bystander PR author.
 * W4 applied it only to MAIN_REGRESSION. This extends the same reasoning to
 * FLAKY_* on PR runs, which is where the promise "if it's flaky and not your
 * change, your check goes green" actually lives.
 *
 * WHY THIS IS SAFE NOW AND WAS NOT BEFORE. The original reason to exclude
 * FLAKY_* was that a chronic flake could not be distinguished from a chronic
 * flake that this time broke for real. The R7-B rate-shift gate above now
 * decides exactly that, and it runs FIRST — so anything reaching here has a
 * failure count its own baseline explains. The redundant check on
 * rateShiftedAtCommit is kept so this stays correct if the blocks are ever
 * reordered.
 *
 * WHAT IT DELIBERATELY DOES NOT DO. MAIN runs still require amnesty, so a
 * chronic flake still goes red on master, still gets an owner, and still gets
 * promoted up the stabilization ranking. The forcing function moves to master,
 * which is where the fix is owned — it is not removed.
 */
const chronicFlakeBystander = (args: {
  runType: string;
  verdict: string;
  rateShiftedAtCommit?: boolean;
}): boolean =>
  FLAKY.has(args.verdict) &&
  (args.runType || "").toUpperCase() !== "MAIN" &&
  args.rateShiftedAtCommit !== true;

export function canWaive(args: {
  runType: string;
  branch: string;
  verdict: string;
  confidence: number;
  citations: string[];
  amnestyGranted?: boolean;
  diffOverlapsFailure: boolean;
  productRejection?: boolean;
  /**
   * R7-B — this test's failure rate shifted materially at this commit
   * (binomial tail test vs its own baseline rate; see rateshift.go). Optional
   * so existing call sites keep compiling, and absent is read as "no signal",
   * which never refuses a waiver on its own.
   */
  rateShiftedAtCommit?: boolean;
  /**
   * R7-L3 — an ACTIVE quarantine for this test (owned, not expired). The
   * server computes `active`; pass that through verbatim and never re-derive
   * the expiry rule here.
   */
  quarantined?: { owner: string; expiresAt: string; daysRemaining: number };
  /**
   * R7-E — baseline runs for this test. Undefined means the history lookup
   * itself failed, which is handled by the classifier's own fail-closed path
   * rather than here; 0 is a real answer meaning "brand-new test".
   */
  historyRuns?: number;
}): { waived: boolean; reason: string } {
  if (neverAutoWaive(args.runType, args.branch)) {
    return { waived: false, reason: "release runs never auto-waive" };
  }
  // W6: on a MAIN run the baseline IS this run — "pre-existing on the
  // baseline" is self-referential. Waiving it would green a red master, which
  // is exactly what the stabilization half exists to prevent. Master red stays
  // red until the test is fixed.
  if ((args.runType || "").toUpperCase() === "MAIN" && args.verdict === "MAIN_REGRESSION") {
    return {
      waived: false,
      reason: "MAIN runs never waive MAIN_REGRESSION — the baseline is this run",
    };
  }
  // R7-L3: an active quarantine is a HUMAN PRE-AUTHORIZATION, so it does not
  // need the model to be confident — it works even on INCONCLUSIVE, which is
  // most of its value: an unreliable test should stop gating PRs whether or
  // not a model can explain today's failure. It therefore sits above
  // NEVER_WAIVE, the confidence floor and the citation rule.
  //
  // Four things it must never hide, checked before it can apply:
  //
  //   PR_REGRESSION      "your change broke this" is the message the whole
  //                      system exists to deliver; a quarantine on the test
  //                      must not suppress it.
  //   productRejection   the server deliberately refused the action — it
  //                      answered correctly, so this is not the test's fault.
  //   diffOverlapsFailure this PR touches the failing area, so attribution is
  //                      ambiguous and a pre-authorization cannot resolve it.
  //   rateShiftedAtCommit the failure count is not explained by the test's own
  //                      flakiness — the very thing the quarantine asserts.
  //
  // MAIN is excluded by construction: quarantine hides a test from PR gating,
  // never from master. Master keeps running it, keeps counting it in
  // raw_failures, and keeps it in the stabilization ranking.
  if (args.quarantined && (args.runType || "").toUpperCase() !== "MAIN") {
    const blocked =
      args.verdict === "PR_REGRESSION" ||
      args.productRejection === true ||
      args.diffOverlapsFailure ||
      args.rateShiftedAtCommit === true;
    if (!blocked) {
      return {
        waived: true,
        reason:
          `quarantined test (owner ${args.quarantined.owner}, ` +
          `${args.quarantined.daysRemaining}d left, expires ${args.quarantined.expiresAt})`,
      };
    }
  }
  if (NEVER_WAIVE.has(args.verdict)) {
    return { waived: false, reason: `${args.verdict} is not waivable` };
  }
  if (FLAKY.has(args.verdict) && args.productRejection) {
    return {
      waived: false,
      reason:
        "error shows the product deliberately refusing the action — not flake, needs human review",
    };
  }
  if (args.diffOverlapsFailure && FLAKY.has(args.verdict)) {
    return { waived: false, reason: "PR diff touches the failing area — attribution is ambiguous" };
  }
  // R7-B: the rate-shift gate — the most expensive error class must not rest
  // on model judgment.
  //
  // The deterministic classifier can only reach PR_REGRESSION when a test has
  // never failed on the baseline (classify.go requires Failed == 0), so a
  // historically flaky test that this time broke for real is indistinguishable
  // from one that merely flaked again: both land on FLAKY_TEST, which is
  // waivable. The rate-shift test separates them from the test's own data — if
  // this commit's failure count is too unlikely under its baseline rate, then
  // "it flaked again" does not explain what happened.
  //
  // This is a gate, not a hint. It fires whatever the model said and whatever
  // confidence it claimed, because the model is measurably overconfident (a
  // stated 0.9 was ~60% correct in the round-6 backtest) and because the
  // policy gate, not the model, owns every green. It sits above the confidence
  // floor deliberately: a higher stated confidence must not buy past it.
  if (args.rateShiftedAtCommit === true && FLAKY.has(args.verdict)) {
    return {
      waived: false,
      reason:
        "failure rate shifted materially at this commit — historical flakiness does not explain it",
    };
  }
  // R7-E: insufficient history — do not guess without a baseline.
  //
  // The exception is deliberate and is the whole reason this is not a blanket
  // refusal: in-run recovery (status=flaky / retry_count>0) is flakiness
  // MEASURED at this commit, not inferred from history. The test failed, then
  // passed, with no code change in between. That evidence does not need a
  // baseline, so a new test that demonstrably flaked in-run stays waivable.
  //
  // Without the exception every newly-added test would go permanently red on
  // every PR that merely runs it, which is a worse failure than the one being
  // prevented.
  //
  // Validated against the three real clusters on mattermost#38154: it refuses
  // the ABAC file-permissions cluster (screenshot + diff reasoning but NO
  // measured recovery — an empty channel where ABAC-permitted files should
  // appear is also what a real regression looks like) and permits the two that
  // cite this_run_recovered.
  if (
    FLAKY.has(args.verdict) &&
    args.historyRuns !== undefined &&
    args.historyRuns < MIN_HISTORY_RUNS_FOR_WAIVER &&
    !args.citations.includes("this_run_recovered")
  ) {
    return {
      waived: false,
      reason:
        `only ${args.historyRuns} baseline run(s) — need ${MIN_HISTORY_RUNS_FOR_WAIVER} ` +
        `or in-run recovery before calling this a flake`,
    };
  }
  // W4 bystander carve-out: amnesty's pain must land on master, not on
  // bystander PR authors. A PR that hits a failure already failing on the
  // baseline stays waivable even after the test's amnesty has expired — the
  // escalation (hard red on master, promotion up the stabilization ranking)
  // happens on the master side, where the fix is owned. FLAKY_* verdicts on
  // any run, and everything on a MAIN run, still require amnesty.
  if (
    args.amnestyGranted === false &&
    !bystanderPreexisting(args) &&
    !chronicFlakeBystander(args)
  ) {
    return { waived: false, reason: "amnesty denied" };
  }
  if (args.confidence < WAIVE_CONFIDENCE) {
    return { waived: false, reason: `confidence ${args.confidence} < ${WAIVE_CONFIDENCE}` };
  }
  const measured = args.citations.includes("this_run_recovered");
  if (!measured && args.citations.length < 2) {
    return { waived: false, reason: "need two independent citations (or in-run recovery)" };
  }
  if (FLAKY.has(args.verdict)) {
    return { waived: true, reason: args.verdict };
  }
  if (bystanderPreexisting(args)) {
    return { waived: true, reason: "pre-existing on the baseline branch" };
  }
  return { waived: false, reason: `${args.verdict} is not waivable` };
}

export function decide(args: {
  failure: EvidenceFailure;
  runType: string;
  branch: string;
  changedFiles: string[];
  ai?: ClaudeVerdict;
  /** The run's mode ("gate" | "shadow") — gates the waiver decision itself
   * (B5 fix): the decision, the ledger row and the outputs must all agree
   * with it, not just a local variable read later. REQUIRED — a missing mode
   * must be a compile error, never a silent default. */
  mode: string;
}): Decision {
  const suggested: Suggestion = args.failure.suggested;
  const overlaps = diffOverlaps(args.changedFiles, args.failure.file, args.failure.error_stack);
  // Rejection is visible in text evidence OR in the screenshot the model viewed —
  // the MM-67594_13 case: the banner text appears only in the screenshot.
  const rejection =
    isProductRejection(args.failure.error_message, args.failure.error_stack) ||
    args.ai?.product_refusal === true;
  const merged = mergeModel(suggested, args.ai, overlaps, args.failure, args.changedFiles);
  // R7-B — read the server-computed shift verbatim. The action must not
  // recompute or soften it: the threshold lives in one place (rateshift.go) so
  // the ledger row and the gate can never disagree about what was judged.
  const rateShifted = args.failure.rate_shift?.shifted === true;
  // R7-L3 — trust the server's `active`; the action must not re-derive expiry.
  const q = args.failure.quarantine;
  const quarantined =
    q?.active === true
      ? { owner: q.owner, expiresAt: q.expires_at, daysRemaining: q.days_remaining }
      : undefined;
  // R7-E — history depth. A failed lookup leaves this undefined on purpose:
  // the classifier already fails closed to INCONCLUSIVE on a history error, so
  // treating "unknown" as "insufficient" here would double-count it.
  const historyRuns = args.failure.history_error ? undefined : args.failure.history?.runs;
  const waiver = canWaiveInMode({
    runType: args.runType,
    branch: args.branch,
    verdict: merged.verdict,
    confidence: merged.confidence,
    citations: merged.citations,
    amnestyGranted: args.failure.amnesty?.granted,
    diffOverlapsFailure: overlaps,
    productRejection: rejection,
    rateShiftedAtCommit: rateShifted,
    quarantined,
    historyRuns,
    mode: args.mode,
  });
  const d: Decision = {
    ...merged,
    waived: waiver.waived,
    check_state: waiver.waived ? "success" : "failure",
    // R7-L3: a waiver's reason is kept whenever it carries provenance the
    // verdict does not already state. For a plain flake waive the reason IS
    // the verdict name ("FLAKY_TEST"), which would only be noise — but a
    // quarantine names its owner and deadline, and that must reach the PR
    // comment and the ledger row. A waiver nobody can attribute is exactly
    // the silence this system exists to remove.
    reason: waiver.waived
      ? waiver.reason === merged.verdict
        ? merged.reason
        : `${merged.reason} (${waiver.reason})`
      : `${merged.reason} (${waiver.reason})`,
    kind: kindOf(merged.verdict),
    member_count: 1,
    chronic: args.ai?.chronic === true,
    refusal: rejection,
    // A waiver granted at the minimum confidence is a coin flip — surface it
    // for a human eyeball even when it goes green.
    borderline: waiver.waived && merged.confidence < BORDERLINE_CONFIDENCE,
  };
  return d;
}

/** Waivers at or below this confidence are flagged as borderline in summaries. */
export const BORDERLINE_CONFIDENCE = 0.9;

function mergeModel(
  suggested: Suggestion,
  ai: ClaudeVerdict | undefined,
  overlaps: boolean,
  failure: EvidenceFailure,
  changedFiles: string[],
): {
  verdict: string;
  confidence: number;
  reason: string;
  gist?: string;
  citations: string[];
  source: Decision["source"];
} {
  let merged: {
    verdict: string;
    confidence: number;
    reason: string;
    gist?: string;
    citations: string[];
    source: Decision["source"];
  };

  if (!ai) {
    merged = {
      verdict: suggested.verdict,
      confidence: suggested.confidence,
      reason: suggested.reason,
      citations: suggested.citations,
      source: "history",
    };
  } else if (overlaps && FLAKY.has(ai.verdict)) {
    merged = {
      verdict: suggested.verdict === "INCONCLUSIVE" ? "PR_REGRESSION" : suggested.verdict,
      confidence: Math.min(suggested.confidence, 0.6),
      reason: `${ai.reason} — ignored: PR diff overlaps the failing area`,
      citations: suggested.citations,
      source: "policy",
    };
  } else {
    merged = {
      verdict: ai.verdict,
      confidence: ai.confidence,
      reason: ai.reason,
      gist: ai.gist,
      citations: unique([...suggested.citations, ...ai.citations]),
      source: "model",
    };
  }

  return enforceDecisiveVerdict(merged, failure, changedFiles, overlaps);
}

/**
 * With screenshots / error / stack in hand, refuse to leave the run as
 * INCONCLUSIVE. Also refuse PR_REGRESSION / TEST_DEBT when the PR only
 * touched CI/harness — that was poisoning #9996 dogfood.
 */
/**
 * R7-D — the confidence the policy layer asserts when it overrides a verdict,
 * plus the citation that says it did.
 *
 * THE PROBLEM. Four override paths below turn a non-waivable verdict into a
 * waivable FLAKY_* and raise the confidence to `WAIVE_CONFIDENCE` — which is
 * exactly the threshold `canWaive` then checks it against. So on those paths
 * the 0.85 floor is unfalsifiable: the policy manufactures the number that
 * clears its own gate. An INCONCLUSIVE at confidence 0 becomes a waived flake
 * at 0.85.
 *
 * WHY THE BUMP STAYS. Removing it would leave the override pointless — the
 * verdict would change but could never be waived, so every ambiguous run stays
 * red and the "with evidence in hand, do not leave this INCONCLUSIVE" rule
 * stops meaning anything. Whether that trade is right is a design decision for
 * the team, not something to change unilaterally.
 *
 * WHAT IS FIXED. The provenance. Until now a 0.85 measured by the model and a
 * 0.85 fabricated here were indistinguishable in the ledger, in the blind
 * audit, and in the calibration metrics — which are precisely the mechanisms
 * meant to catch a bad waiver. Every manufactured confidence now carries
 * `policy_asserted_confidence`, so it can be filtered, audited and excluded
 * from calibration. The citation is added ONLY when the floor was actually
 * raised; a model that was already confident enough gets no marker.
 */
function assertedConfidence(
  modelConfidence: number,
  cites: string[],
): { confidence: number; citations: string[] } {
  if (modelConfidence >= WAIVE_CONFIDENCE) {
    return { confidence: modelConfidence, citations: cites };
  }
  return {
    confidence: WAIVE_CONFIDENCE,
    citations: [...cites, "policy_asserted_confidence"],
  };
}

export function enforceDecisiveVerdict(
  merged: {
    verdict: string;
    confidence: number;
    reason: string;
    gist?: string;
    citations: string[];
    source: Decision["source"];
  },
  failure: EvidenceFailure,
  changedFiles: string[],
  overlaps: boolean,
): {
  verdict: string;
  confidence: number;
  reason: string;
  gist?: string;
  citations: string[];
  source: Decision["source"];
} {
  const evidence = hasAdjudicationEvidence(failure);
  const ciOnly = isCIOnlyDiff(changedFiles);
  const specTouched = touchesFailingSpec(changedFiles, failure.file);
  const cites = [...merged.citations];

  if (
    evidence &&
    (failure.screenshots || []).length > 0 &&
    !cites.some((c) => /screenshot/i.test(c))
  ) {
    cites.push("screenshot");
  }
  if (evidence && (failure.error_message || "").trim() && !cites.some((c) => /error/i.test(c))) {
    cites.push("error_message");
  }

  // CI-only PR cannot be a product PR_REGRESSION / TEST_DEBT for a UI failure.
  // But a PR that edits the failing spec is NOT ci-only for that failure —
  // the model's bug verdict stands.
  if (
    !overlaps &&
    !specTouched &&
    ciOnly &&
    (merged.verdict === "PR_REGRESSION" || merged.verdict === "TEST_DEBT")
  ) {
    const asserted = assertedConfidence(merged.confidence, [...cites, "ci_only_diff"]);
    return {
      verdict: "FLAKY_INFRA",
      confidence: asserted.confidence,
      reason: `${merged.reason} — overridden: PR only touches CI/harness, not product code under test`,
      gist: merged.gist,
      citations: unique(asserted.citations),
      source: "policy",
    };
  }

  // PR_REGRESSION means "this PR caused it" — impossible without product/spec overlap.
  if (!overlaps && !specTouched && merged.verdict === "PR_REGRESSION" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    const asserted = assertedConfidence(merged.confidence, [...cites, "no_product_overlap"]);
    return {
      verdict: flakeKind,
      confidence: asserted.confidence,
      reason: `${merged.reason} — overridden: PR does not touch this failure's product/spec area`,
      gist: merged.gist,
      citations: unique(asserted.citations),
      source: "policy",
    };
  }

  // Mis-labeled TEST_DEBT on infra/server timeouts when this PR did not touch the failure.
  if (!overlaps && !specTouched && merged.verdict === "TEST_DEBT" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    if (flakeKind === "FLAKY_INFRA" || flakeKind === "FLAKY_SERVER") {
      const asserted = assertedConfidence(merged.confidence, [...cites, "no_product_overlap"]);
      return {
        verdict: flakeKind,
        confidence: asserted.confidence,
        reason: `${merged.reason} — overridden: infra/server signal with no product overlap`,
        gist: merged.gist,
        citations: unique(asserted.citations),
        source: "policy",
      };
    }
  }

  if (merged.verdict === "INCONCLUSIVE" && evidence) {
    const flakeKind = inferFlakeKind(failure.error_message || "", failure.error_stack || "");
    const asserted = assertedConfidence(merged.confidence, [...cites, "error_or_screenshot"]);
    return {
      verdict: flakeKind,
      confidence: asserted.confidence,
      reason: `${merged.reason} — overridden: evidence present (error/screenshots/stack); INCONCLUSIVE forbidden`,
      gist: merged.gist,
      citations: unique(asserted.citations),
      source: "policy",
    };
  }

  return { ...merged, citations: unique(cites) };
}

function inferFlakeKind(error: string, stack: string): string {
  const text = `${error}\n${stack}`.toLowerCase();
  if (
    /enotfound|econnrefused|etimedout|dns|socket hang up|network request failed|server.*unreachable/.test(
      text,
    )
  ) {
    return "FLAKY_SERVER";
  }
  if (
    /emulator|simulator|metro|adb|device offline|bootstrap|connecttoserver|loginavailable|waitfor.*timeout/.test(
      text,
    )
  ) {
    return "FLAKY_INFRA";
  }
  return "FLAKY_TEST";
}

function unique(xs: string[]): string[] {
  return [...new Set(xs.filter(Boolean))];
}

export function rollup(decisions: Decision[]): {
  waived: boolean;
  verdict: string;
  state: "success" | "failure";
  description: string;
} {
  if (decisions.length === 0) {
    return { waived: true, verdict: "", state: "success", description: "no failures" };
  }
  const unwaived = decisions.filter((d) => !d.waived);
  if (unwaived.length === 0) {
    return {
      waived: true,
      verdict: decisions[0]!.verdict,
      state: "success",
      description: `${decisions.reduce((n, d) => n + d.member_count, 0)} failure(s) in ${decisions.length} cluster(s) classified as flaky/pre-existing`,
    };
  }
  const worst = unwaived[0]!;
  return {
    waived: false,
    verdict: worst.verdict,
    state: "failure",
    description: `${unwaived.length}/${decisions.length} cluster(s) unwaived (${worst.verdict})`,
  };
}

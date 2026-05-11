/**
 * test-system-io-summary: read /api/v1/orchestration/status and write a
 * Markdown job summary to $GITHUB_STEP_SUMMARY linking to the per-group
 * dashboard page. Per-shard report uploads already happened inside each
 * worker; the report_group auto-finalizes once total_reports_expected
 * uploads have landed (server-side count-based predicate).
 *
 * Framework-agnostic — the framework input is purely a UI label for the
 * summary header.
 *
 * Exits non-zero when any unit ended in completed_fail or the run did not
 * reach `completed` (unless fail-on-test-failures=false).
 */

import * as fs from "node:fs";
import * as core from "@actions/core";
import { deletePRCommentByMarker, postOrUpdatePRComment } from "./pr-comment";

interface CompositeIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  name: string;
  branch?: string;
  gh_pr_number?: number | string;
}

interface OrchestrationStatus {
  status?: string;
  total_units?: number;
  counts?: {
    completed_pass?: number;
    completed_fail?: number;
    completed_skipped?: number;
    pending?: number;
    leased?: number;
  };
  // Per-test-case rollup, present once any attempt has reported
  // test_cases. Counts use the same any-passed-AND-any-failed → flaky
  // rule the dashboard's listing rows use, so this matches what the UI
  // shows for a finished run.
  tests?: {
    passed?: number;
    failed?: number;
    flaky?: number;
    skipped?: number;
    total?: number;
  };
  // Wall-clock split between the first-pass dispatch and any retest
  // dispatches. Both ms fields are optional: first_pass_ms is absent
  // until any first attempt reports; retest_ms is absent unless a unit
  // was re-leased after a fail. retest_unit_count is the distinct count
  // of dispatch units that had at least one retest. begin_at /
  // first_test_at are the absolute timestamps used to derive the setup
  // segment (begin → first checkout); first_test_at is absent until any
  // attempt is dispatched.
  durations?: {
    first_pass_ms?: number;
    retest_ms?: number;
    retest_unit_count?: number;
    begin_at?: string;
    first_test_at?: string;
  };
  // Per-unit detail returned alongside the run snapshot. Used here to
  // surface the failed-spec list in the PR comment.
  units?: Array<{
    spec_path?: string;
    state?: string;
    dispatch_seq?: number;
  }>;
}

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const compositeIdentityRaw = core.getInput("composite-identity", { required: true });
  const framework = core.getInput("framework", { required: true });
  const failOnTestFailures = core.getInput("fail-on-test-failures") !== "false";

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  normalizeCompositeIdentity(compositeIdentity);

  const bearer = await core.getIDToken(audience);
  // Mark the JWT for the runner's output filter so subsequent `core.info`,
  // error messages, or stack traces involving it print as `***`.
  core.setSecret(bearer);

  const params = new URLSearchParams({
    repository: compositeIdentity.repository,
    commit_sha: compositeIdentity.commit_sha,
    gh_run_id: compositeIdentity.gh_run_id,
    name: compositeIdentity.name,
    gh_run_attempt: compositeIdentity.gh_run_attempt,
  });
  const statusRes = await fetch(`${baseURL}/api/v1/orchestration/status?${params.toString()}`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  let status: OrchestrationStatus | null = null;
  try {
    status = (await statusRes.json()) as OrchestrationStatus;
  } catch {
    status = null;
  }
  // Whitelist only the fields we render — guards against accidental leakage
  // if the API ever evolves to include sensitive fields (signed URLs, debug
  // tokens, etc.) since this runs in consumer CI logs which are public.
  if (status) {
    const counts = status.counts || {};
    core.info(
      `orchestration status: status=${status.status ?? "unknown"} total=${status.total_units ?? "?"} ` +
        `pass=${counts.completed_pass ?? 0} fail=${counts.completed_fail ?? 0} ` +
        `skip=${counts.completed_skipped ?? 0} pending=${counts.pending ?? 0} leased=${counts.leased ?? 0}`,
    );
  }

  // Dashboard URLs use only the trailing segment of the repository slug
  // ("owner/repo" → "repo") to match the convention surfaced by the
  // /reports/consolidated and /reports/grouped endpoints. Mirroring the same
  // path shape used elsewhere in the UI keeps deep links consistent and
  // browsable.
  const repoSlug = compositeIdentity.repository || "";
  const repoTrailing = repoSlug.split("/").pop() || repoSlug;
  const repo = encodeURIComponent(repoTrailing);
  const branch = encodeURIComponent(compositeIdentity.branch || "main");
  const shortSha = (compositeIdentity.commit_sha || "").slice(0, 7);
  const name = encodeURIComponent(compositeIdentity.name);
  const reportURL = `${baseURL}/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(compositeIdentity.gh_run_id)}`;

  const summaryPath = process.env.GITHUB_STEP_SUMMARY;
  if (summaryPath) {
    const counts = status?.counts || {};
    const total = status?.total_units ?? "?";
    const lines = [
      `## E2E Test Results — ${framework} (Test System IO orchestrated)`,
      "",
      `**Run status:** \`${status?.status ?? "unknown"}\``,
      "",
      "| metric | value |",
      "|---|---|",
      `| total units | ${total} |`,
      `| pass | ${counts.completed_pass ?? 0} |`,
      `| fail | ${counts.completed_fail ?? 0} |`,
      `| skipped | ${counts.completed_skipped ?? 0} |`,
      `| pending | ${counts.pending ?? 0} |`,
      `| leased | ${counts.leased ?? 0} |`,
      "",
      `[Open Report Group](${reportURL})`,
      "",
    ];
    fs.appendFileSync(summaryPath, lines.join("\n"));
  }

  // Mirror calculate-cypress-results' commit_status_message format so v2
  // commit statuses read the same as v1's: `100% passed (1345), 446 specs`
  // when all green, `96.8% passed (457/472), 117 specs, 15 failed`
  // otherwise. The (passed/total) part uses test-case counts when the
  // per-test rollup is available — that's the headline number a reader
  // expects on a commit status — and the trailing "N specs" suffix uses
  // the dispatch-unit count (one unit == one spec) so the message answers
  // both "how many tests" and "how many specs ran" at a glance.
  const unitPass = status?.counts?.completed_pass ?? 0;
  const unitFail = status?.counts?.completed_fail ?? 0;
  const unitSkip = status?.counts?.completed_skipped ?? 0;
  const totalSpecs = unitPass + unitFail + unitSkip;

  const t = status?.tests;
  const haveTestRollup = !!t && (t.total ?? 0) > 0;
  // Flaky tests counted alongside passed for the message — same convention
  // calculate-cypress-results uses (a flaky-but-eventually-passed test is
  // not a "failed" test from the commit-status perspective).
  const passed = haveTestRollup ? (t!.passed ?? 0) + (t!.flaky ?? 0) : unitPass;
  const failed = haveTestRollup ? (t!.failed ?? 0) : unitFail;
  const skipped = haveTestRollup ? (t!.skipped ?? 0) : unitSkip;
  const flaky = haveTestRollup ? (t!.flaky ?? 0) : 0;
  const rateDenom = passed + failed;
  const rate = rateDenom > 0 ? (passed * 100) / rateDenom : 0;
  const rateStr = rate === 100 ? "100%" : `${rate.toFixed(1)}%`;
  const specSuffix = totalSpecs > 0 ? `, ${totalSpecs} specs` : "";
  const commitStatusMessage =
    rate === 100
      ? `${rateStr} passed (${passed})${specSuffix}`
      : `${rateStr} passed (${passed}/${rateDenom})${specSuffix}, ${failed} failed`;

  // First-pass / retest wall-clock split. Rendered in the commit-status
  // description as `first-pass + retest` (e.g. `15m 23s + 2m 5s retest`)
  // so a reader can tell at a glance how much of the elapsed time was
  // spent re-running flakes/failures.
  const firstPassMs = status?.durations?.first_pass_ms ?? null;
  const retestMs = status?.durations?.retest_ms ?? null;
  const retestUnitCount = status?.durations?.retest_unit_count ?? 0;
  const durationDisplay = formatDurationDisplay(firstPassMs, retestMs);
  // Setup segment (begin → first checkout) reuses the same derivation the
  // dashboard's report-summary header shows: first_test_at - begin_at.
  // Only rendered in the PR comment; webhook + commit-status outputs keep
  // the v1-compatible `first-pass + retest` shape.
  const setupMs = computeSetupMs(status?.durations?.begin_at, status?.durations?.first_test_at);

  // Inputs that drive the commit-status description and webhook payload.
  // All optional; missing segments degrade gracefully.
  const imageTag = core.getInput("image-tag");
  const imageAliases = core.getInput("image-aliases");
  const serverImage = core.getInput("server-image") || imageTag;
  const reportType = (core.getInput("report-type") || "PR").toUpperCase();
  const inputPRNumber = core.getInput("pr-number");
  const inputRefBranch = core.getInput("ref-branch");
  const webhookUsername = core.getInput("webhook-username") || "E2E Test";
  const webhookIconURL =
    core.getInput("webhook-icon-url") ||
    "https://mattermost.com/wp-content/uploads/2022/02/icon_WS.png";

  // Effective branch / PR number: explicit input wins, fall back to the
  // composite-identity values so the webhook source line still renders
  // correctly when callers only set composite-identity.
  const effectiveBranch = inputRefBranch || compositeIdentity.branch || "";
  const effectivePRNumber =
    inputPRNumber ||
    (compositeIdentity.gh_pr_number != null ? String(compositeIdentity.gh_pr_number) : "");

  // Final commit-status description: message + duration + image_tag.
  // Mirrors v1's e2e-tests-cypress-template.yml update-success-status's
  // shape verbatim, so consumers can drop the v2 output straight into
  // mattermost/actions/delivery/update-commit-status' `description:`.
  const aliasesSuffix = imageAliases ? ` (${imageAliases})` : "";
  const imageTagSegment = imageTag ? `, image_tag:${imageTag}${aliasesSuffix}` : "";
  const durationSegment = durationDisplay ? `, ${durationDisplay}` : "";
  const commitStatusDescription = `${commitStatusMessage}${durationSegment}${imageTagSegment}`;

  // Webhook payload: mirrors v1's ci/publish-report attachment shape so
  // existing receivers render it identically.
  const webhookColor = colorForRate(rate);
  const retestDisplay = retestUnitCount > 0 ? `:repeat: re-run ${retestUnitCount} spec(s)` : "";
  const webhookPayload = renderWebhookPayload({
    username: webhookUsername,
    iconURL: webhookIconURL,
    color: webhookColor,
    contextName: core.getInput("context-name"),
    framework,
    reportType,
    repository: compositeIdentity.repository,
    commitSHA: compositeIdentity.commit_sha,
    refBranch: effectiveBranch,
    prNumber: effectivePRNumber,
    serverImage,
    commitStatusMessage,
    retestDisplay,
    setupMs,
    firstPassMs,
    retestMs,
    reportURL,
  });

  core.setOutput("passed", passed);
  core.setOutput("failed", failed);
  core.setOutput("flaky", flaky);
  core.setOutput("skipped", skipped);
  core.setOutput("total_specs", totalSpecs);
  core.setOutput("pass_rate", rateStr);
  core.setOutput("first_pass_duration_ms", firstPassMs ?? "");
  core.setOutput("retest_duration_ms", retestMs ?? "");
  core.setOutput("retest_unit_count", retestUnitCount);
  core.setOutput("duration_display", durationDisplay);
  core.setOutput("webhook_color", webhookColor);
  core.setOutput("commit_status_message", commitStatusMessage);
  core.setOutput("commit_status_description", commitStatusDescription);
  core.setOutput("webhook_payload", webhookPayload);

  // PR comment — best-effort, opt-in. Skips silently for non-PR runs.
  if (core.getInput("post-pr-comment") === "true") {
    await postSummaryComment({
      compositeIdentity,
      contextName: core.getInput("context-name"),
      serverImage,
      runStatus: status?.status ?? "unknown",
      commitStatusMessage,
      setupMs,
      firstPassMs,
      retestMs,
      reportURL,
      units: status?.units ?? [],
      failedUnitCount: failed,
    });
  }

  if (status?.status !== "completed") {
    const msg = `run did not complete cleanly: ${status?.status}`;
    if (failOnTestFailures) throw new Error(msg);
    core.warning(msg);
  }
  if (failed > 0) {
    const msg = `${failed} unit(s) failed`;
    if (failOnTestFailures) throw new Error(msg);
    core.warning(msg);
  }
}

function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

// formatDuration renders a millisecond count as "Xm Ys". Sub-minute
// values still render as "0m Ns" for visual consistency with the v1
// commit-status format (cf. e2e-tests-cypress-template.yml's
// ci/compute-duration step).
function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}m ${s}s`;
}

// formatDurationDisplay renders the first-pass / retest wall-clock split.
// `15m 23s` when no retests ran; `15m 23s + 2m 5s retest` when both are
// present (the trailing "retest" label disambiguates the two segments at
// a glance in the commit-status description). Returns an empty string
// when no first attempt has reported yet — the v2 template's commit-
// status description folds that into a graceful empty-duration display.
function formatDurationDisplay(firstPassMs: number | null, retestMs: number | null): string {
  if (firstPassMs == null) return "";
  const first = formatDuration(firstPassMs);
  if (retestMs == null || retestMs <= 0) return first;
  return `${first} + ${formatDuration(retestMs)} retest`;
}

// computeSetupMs derives the begin → first-checkout segment from the
// absolute timestamps the /status endpoint surfaces. Returns null when
// either timestamp is missing or unparseable so the caller can suppress
// the setup chip.
function computeSetupMs(beginAt?: string, firstTestAt?: string): number | null {
  if (!beginAt || !firstTestAt) return null;
  const begin = Date.parse(beginAt);
  const first = Date.parse(firstTestAt);
  if (!Number.isFinite(begin) || !Number.isFinite(first)) return null;
  return Math.max(0, first - begin);
}

// formatCommentDuration renders the duration chip shown in the PR
// comment stats line: `(5m setup) + 14m 1s + 2m 15s (retest)`. Segments
// are dropped when the underlying ms is null/zero, so a fresh run with
// no first attempt yet returns an empty string and the caller omits
// the `, duration: ...` suffix entirely.
function formatCommentDuration(
  setupMs: number | null,
  firstPassMs: number | null,
  retestMs: number | null,
): string {
  const parts: string[] = [];
  if (setupMs != null && setupMs > 0) parts.push(`(${formatDuration(setupMs)} setup)`);
  if (firstPassMs != null) parts.push(formatDuration(firstPassMs));
  if (retestMs != null && retestMs > 0) parts.push(`${formatDuration(retestMs)} (retest)`);
  return parts.join(" + ");
}

// Webhook attachment color bands. Mirrors getColor() in
// mattermost/.github/actions/calculate-cypress-results/src/merge.ts so v2
// posts the same hue v1 receivers already render against.
function colorForRate(rate: number): string {
  if (rate === 100) return "#43A047";
  if (rate >= 99) return "#FFEB3B";
  if (rate >= 98) return "#FF9800";
  return "#F44336";
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// escapeForCodeSpan strips characters that would break out of a Markdown
// inline code span. Spec paths come from filesystem discovery on the
// consumer's repo; defensive sanitization avoids visual breakage (and
// minor format-injection vectors) on rendered comment bodies.
function escapeForCodeSpan(s: string | undefined): string {
  if (!s) return "<unknown>";
  return s.replace(/[`\r\n]/g, "");
}

interface WebhookFields {
  username: string;
  iconURL: string;
  color: string;
  contextName: string;
  framework: string;
  reportType: string;
  repository: string;
  commitSHA: string;
  refBranch: string;
  prNumber: string;
  serverImage: string;
  commitStatusMessage: string;
  retestDisplay: string;
  setupMs: number | null;
  firstPassMs: number | null;
  retestMs: number | null;
  reportURL: string;
}

interface SummaryCommentArgs {
  compositeIdentity: CompositeIdentity;
  contextName: string;
  serverImage: string;
  runStatus: string;
  commitStatusMessage: string;
  setupMs: number | null;
  firstPassMs: number | null;
  retestMs: number | null;
  reportURL: string;
  units: NonNullable<OrchestrationStatus["units"]>;
  failedUnitCount: number;
}

const FAILED_SPECS_PREVIEW = 5;

// postSummaryComment finalizes the PR comment keyed by the test-system-io
// marker:
//
//   * Clean pass (run reached `completed` AND no failed units) — DELETE
//     the related comment so the PR conversation stays uncluttered.
//     The "started" comment from dispatch-begin is removed once it's no
//     longer load-bearing.
//   * Anything else (failed units, timed_out, incomplete) — UPDATE the
//     comment with the summary so a reviewer sees the failed-spec list
//     and where to look. Heading link reads `[completed]` for terminal
//     runs and `[ended]` for non-completed states.
async function postSummaryComment(a: SummaryCommentArgs): Promise<void> {
  const c = a.compositeIdentity;
  if (c.gh_pr_number == null || c.gh_pr_number === "") return;
  const prNumber = Number.parseInt(String(c.gh_pr_number), 10);
  if (!Number.isFinite(prNumber)) return;

  if (!a.contextName) {
    core.warning("post-pr-comment is true but context-name is empty; skipping PR comment.");
    return;
  }

  const token = core.getInput("github-token");
  const [owner, repo] = (c.repository || "").split("/");
  if (!owner || !repo) return;

  const shortSha = (c.commit_sha || "").slice(0, 7);
  const marker = `<!-- test-system-io:${a.contextName}@${shortSha} -->`;

  const cleanPass = a.runStatus === "completed" && a.failedUnitCount === 0;
  if (cleanPass) {
    await deletePRCommentByMarker({
      token,
      owner,
      repo,
      prNumber,
      marker,
      // Match the lookup window the post path uses.
      singlePage: true,
    });
    return;
  }

  const statusLabel =
    a.runStatus === "completed" ? (a.failedUnitCount > 0 ? "failed" : "completed") : "ended";
  const imageLabel = a.serverImage || shortSha;
  const heading = formatCommentHeading(
    a.contextName,
    imageLabel,
    statusLabel,
    c.repository || "",
    c.gh_run_id || "",
    a.reportURL,
  );

  const durationSegment = formatCommentDuration(a.setupMs, a.firstPassMs, a.retestMs);
  const durationSuffix = durationSegment ? `, duration: ${durationSegment}` : "";
  const statsLine =
    a.runStatus === "completed"
      ? `${a.commitStatusMessage}${durationSuffix}`
      : `Run did not finish cleanly: ${a.runStatus}. ${a.commitStatusMessage}${durationSuffix}`;
  const lines: string[] = [heading, "", statsLine];

  const failed = a.units.filter((u) => u.state === "completed_fail");
  if (failed.length > 0) {
    failed.sort((u, v) => (u.dispatch_seq ?? 0) - (v.dispatch_seq ?? 0));
    const preview = failed.slice(0, FAILED_SPECS_PREVIEW);
    const remaining = failed.length - preview.length;
    lines.push("", "Failed specs:");
    for (const u of preview) {
      lines.push(`- \`${escapeForCodeSpan(u.spec_path)}\``);
    }
    if (remaining > 0) {
      lines.push(`- _…and ${remaining} more — see the [full report](${a.reportURL})._`);
    }
  }

  lines.push("", marker, "");

  await postOrUpdatePRComment({
    token,
    owner,
    repo,
    prNumber,
    marker,
    body: lines.join("\n"),
    // Cap the lookup at the first page (100 comments). On a busy PR
    // the begin comment may be past page 1; in that rare case the
    // summary is posted as a new comment instead of paginating.
    singlePage: true,
  });
}

function formatCommentHeading(
  contextName: string,
  imageLabel: string,
  statusLabel: string,
  repository: string,
  ghRunId: string,
  reportURL: string,
): string {
  const pipelineURL = buildPipelineURL(repository, ghRunId);
  return `[${contextName}](${pipelineURL}) for \`${imageLabel}\` [${statusLabel}](${reportURL})`;
}

function buildPipelineURL(repository: string, ghRunId: string): string {
  const serverURL = process.env.GITHUB_SERVER_URL || "https://github.com";
  return `${serverURL}/${repository}/actions/runs/${ghRunId}`;
}

// LONG_RUN_THRESHOLD_MS — total wall-clock at which the webhook prefixes
// the duration line with :warning: so a reader can spot a slow run
// without scanning the numbers.
const LONG_RUN_THRESHOLD_MS = 15 * 60 * 1000;

// renderWebhookPayload builds the Mattermost-style webhook JSON body.
// Same attachment shape v1 produced (so existing receivers render it);
// title is the caller-supplied context-name and the duration line uses
// the `(setup) + first + (retest)` shape that matches the PR comment.
function renderWebhookPayload(f: WebhookFields): string {
  // Title: prefer the caller-owned context-name (matches the PR comment
  // heading and the GitHub commit-status context). Falls back to
  // `<Framework> Tests` only when context-name was not provided, so the
  // attachment still has something readable in that path.
  const titleSubject = f.contextName || `${capitalize(f.framework)} Tests`;
  const title = `**Results - ${titleSubject}**`;

  // Source line: PR runs link the PR; everything else (MASTER, RELEASE,
  // RELEASE_CUT) links the commit + branch. RELEASE_CUT uses the
  // github_round icon to signal a tagged cut, MASTER/RELEASE use the
  // git_merge icon for an integration build.
  const commitShort = f.commitSHA ? f.commitSHA.slice(0, 7) : "";
  const commitURL = `https://github.com/${f.repository}/commit/${f.commitSHA}`;
  let sourceLine: string;
  if (f.reportType === "RELEASE_CUT") {
    sourceLine = `:github_round: [${commitShort}](${commitURL}) on \`${f.refBranch}\``;
  } else if (f.reportType === "MASTER" || f.reportType === "RELEASE") {
    sourceLine = `:git_merge: [${commitShort}](${commitURL}) on \`${f.refBranch}\``;
  } else {
    const repoTrailing = f.repository.split("/").pop() || f.repository;
    sourceLine = `:open-pull-request: [${repoTrailing}-pr-${f.prNumber}](https://github.com/${f.repository}/pull/${f.prNumber})`;
  }

  const retestPart = f.retestDisplay ? ` | ${f.retestDisplay}` : "";
  const dockerLine = f.serverImage ? `\n:docker: \`${f.serverImage}\`` : "";
  const durationSegments = formatCommentDuration(f.setupMs, f.firstPassMs, f.retestMs);
  const totalMs = (f.setupMs ?? 0) + (f.firstPassMs ?? 0) + (f.retestMs ?? 0);
  const longRunBadge = totalMs > LONG_RUN_THRESHOLD_MS ? ":warning: " : "";
  const durationLine = durationSegments ? `\n${longRunBadge}${durationSegments}` : "";
  const text = `${title}\n\n${sourceLine}${dockerLine}\n${f.commitStatusMessage}${retestPart} | [full report](${f.reportURL})${durationLine}`;

  return JSON.stringify({
    username: f.username,
    icon_url: f.iconURL,
    attachments: [{ color: f.color, text }],
  });
}

// normalizeCompositeIdentity coerces gh_pr_number to a number when it
// arrived as a string. Shell-built composite-identity payloads (jq
// `--arg pr "${PR_NUMBER}"`) emit it as a string, but the server's
// identityFields.GHPRNumber is *int and json.Decode rejects the string
// form. Normalizing once here keeps every downstream payload accepted.
function normalizeCompositeIdentity(c: CompositeIdentity): void {
  if (typeof c.gh_pr_number === "string") {
    const n = Number.parseInt(c.gh_pr_number, 10);
    if (Number.isFinite(n)) {
      c.gh_pr_number = n;
    } else {
      delete c.gh_pr_number;
    }
  }
}

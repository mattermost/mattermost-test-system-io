/**
 * Round-3 major 3 gate: drive run() in SHADOW mode with a PR_REGRESSION and
 * assert a PR comment IS posted and ZERO commit statuses are written. The
 * predicate under test is the comment gate in main.ts — shadow must comment
 * (observational) but never write a check row. Only the network side effects
 * (upsertTriageComment, setCommitStatus) are stubbed; the predicate and the
 * real decide/rollup/formatTriageComment path all execute.
 */
import { test, mock } from "node:test";
import * as assert from "node:assert/strict";

const commentCalls: Array<{ prNumber: number; body: string }> = [];
const statusCalls: Array<{ context: string; state: string }> = [];

const inputs: Record<string, string> = {
  "use-staging": "false",
  "oidc-audience": "mattermost-test-system-io",
  "composite-identity": JSON.stringify({
    repository: "mattermost/mattermost",
    commit_sha: "abc123",
    gh_run_id: "run-1",
    name: "e2e",
    gh_pr_number: 123,
  }),
  "group-id": "",
  "baseline-branch": "main",
  "run-type": "PR",
  mode: "shadow",
  "commit-status-context": "e2e-test/ai-triage",
  "original-commit-status-contexts": "",
  "github-token": "gh-token",
  "pr-token": "gh-token",
  "anthropic-api-key": "",
  "claude-model": "claude-sonnet-4-6",
  "fix-clusters": "",
};

mock.module("@actions/core", {
  exports: {
    getInput: (name: string) => inputs[name] ?? "",
    getIDToken: async () => "bearer",
    setOutput: () => {},
    setFailed: () => {},
    info: () => {},
    warning: () => {},
    notice: () => {},
    setSecret: () => {},
  },
});

mock.module("./commit-status.ts", {
  exports: {
    setCommitStatus: async (args: { context: string; state: string }) => {
      statusCalls.push({ context: args.context, state: args.state });
    },
    listLatestCommitStatuses: async () => [],
  },
});

mock.module("./triage-comment.ts", {
  exports: {
    formatTriageComment: () => "observational comment body",
    upsertTriageComment: async (args: { prNumber: number; body: string }) => {
      commentCalls.push({ prNumber: args.prNumber, body: args.body });
      return "https://github.com/x/comment";
    },
    VERDICT_COMMENT_MARKER: "<!-- tsio:ai-triage-verdict -->",
  },
});

// A PR_REGRESSION cluster with no screenshots/error (so enforceDecisiveVerdict
// does not override it) and needs_ai=false (so no Anthropic call).
const evidencePack = {
  group: {
    id: "g1",
    repository: "mattermost/mattermost",
    branch: "feat/x",
    commit_sha: "abc123",
    gh_run_id: "run-1",
    gh_run_attempt: "1",
    name: "e2e",
    status: "completed",
    framework: "playwright",
  },
  failure_count: 1,
  cluster_count: 1,
  truncated: false,
  lookups: 1,
  max_lookups: 8,
  clusters: [
    {
      signature: "sig1",
      label: "Save fails",
      member_count: 1,
      representative: {
        full_title: "Save fails",
        title: "Save fails",
        file: "e2e-tests/a.spec.ts",
        status: "failed",
        retry_count: 0,
        duration_ms: 100,
        screenshots: [],
        suggested: {
          verdict: "PR_REGRESSION",
          confidence: 0.9,
          needs_ai: false,
          reason: "save failed after PR changes",
          citations: ["changed_files"],
        },
        amnesty: { granted: true, reason: "under limit" },
      },
      suggested: {
        verdict: "PR_REGRESSION",
        confidence: 0.9,
        needs_ai: false,
        reason: "save failed after PR changes",
        citations: ["changed_files"],
      },
    },
  ],
};

globalThis.fetch = (async (input: RequestInfo | URL) => {
  const url = String(input);
  if (url.includes("/api/v1/triage/evidence")) {
    return new Response(JSON.stringify(evidencePack), { status: 200 });
  }
  if (url.includes("/api/v1/triage/verdicts")) {
    return new Response("{}", { status: 200 });
  }
  if (url.includes("api.github.com/repos/") && url.includes("/pulls/")) {
    return new Response(JSON.stringify({ user: { login: "octocat" } }), { status: 200 });
  }
  return new Response("{}", { status: 404 });
}) as typeof fetch;

const { run } = await import("./main.ts");

test("shadow mode posts the PR comment and writes zero commit statuses", async () => {
  await run();
  assert.equal(commentCalls.length, 1, "shadow must post the PR comment");
  assert.equal(commentCalls[0]!.prNumber, 123);
  assert.equal(statusCalls.length, 0, "shadow must write no commit statuses");
});

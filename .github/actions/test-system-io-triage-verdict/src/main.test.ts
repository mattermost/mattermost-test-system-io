import { beforeEach, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({
  inputs: {} as Record<string, string>,
  output: vi.fn(),
  failed: vi.fn(),
  token: vi.fn(),
  github: vi.fn(),
  current: vi.fn(),
  statuses: vi.fn(),
  labels: vi.fn(),
  paginate: vi.fn(),
  fetch: vi.fn(),
}));
vi.mock("@actions/core", () => ({
  getInput: (key: string) => mocks.inputs[key] || "",
  getIDToken: mocks.token,
  setSecret: vi.fn(),
  setOutput: mocks.output,
  setFailed: mocks.failed,
  warning: vi.fn(),
}));
vi.mock("@actions/github", () => ({
  context: { repo: { owner: "o", repo: "r" }, actor: "human" },
  getOctokit: mocks.github,
}));
import { run } from "./main";
const verdict = {
  id: "v1",
  mode: "shadow",
  verdict: "REGRESSION",
  confidence: 1,
  counts: { blocking: 1, exonerated: 0 },
  findings: [],
  markdown: { status_description: "blocked", pr_comment: "comment", check_summary: "check" },
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.inputs = {
    "composite-identity": JSON.stringify({
      repository: "o/r",
      commit_sha: "abc",
      gh_run_id: "12",
      gh_run_attempt: "1",
      name: "test",
    }),
    context: "e2e/test",
    "github-token": "GITHUB_WRITE_TOKEN",
    "post-check-run": "false",
    "post-pr-comment": "false",
  };
  mocks.token.mockResolvedValue("OIDC_ONLY");
  mocks.current.mockResolvedValue({ data: { run_attempt: 1 } });
  mocks.paginate.mockResolvedValue([]);
  mocks.github.mockReturnValue({
    rest: {
      actions: { getWorkflowRun: mocks.current },
      issues: { listLabelsOnIssue: mocks.labels },
      checks: { listForRef: vi.fn() },
      repos: { listCommitStatusesForRef: vi.fn(), createCommitStatus: mocks.statuses },
    },
    paginate: mocks.paginate,
  });
  vi.stubGlobal("fetch", mocks.fetch);
  mocks.fetch.mockResolvedValue(new Response(JSON.stringify(verdict), { status: 200 }));
});
it("off returns outputs without GitHub access", async () => {
  mocks.fetch.mockResolvedValue(
    new Response(JSON.stringify({ ...verdict, mode: "off" }), { status: 200 }),
  );
  await run();
  expect(mocks.github).not.toHaveBeenCalled();
  expect(mocks.output).toHaveBeenCalledWith("mode", "off");
});
it("shadow never posts required status or fails for a blocking verdict", async () => {
  await run();
  expect(mocks.statuses).not.toHaveBeenCalled();
  expect(mocks.failed).not.toHaveBeenCalled();
  const options = mocks.fetch.mock.calls[0][1];
  expect(options.headers.Authorization).toBe("Bearer OIDC_ONLY");
  expect(options.body).not.toContain("GITHUB_WRITE_TOKEN");
  expect(JSON.parse(options.body).wait_for_completion_ms).toBeLessThanOrEqual(60000);
});
it("enforce fails on incomplete and posts failure", async () => {
  mocks.fetch.mockResolvedValue(
    new Response(JSON.stringify({ ...verdict, mode: "enforce", verdict: "INCOMPLETE" }), {
      status: 409,
    }),
  );
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state: "failure" }));
  expect(mocks.failed).toHaveBeenCalled();
});
it("an older attempt never posts", async () => {
  mocks.current.mockResolvedValue({ data: { run_attempt: 2 } });
  mocks.fetch.mockResolvedValue(
    new Response(JSON.stringify({ ...verdict, mode: "enforce" }), { status: 200 }),
  );
  await run();
  expect(mocks.statuses).not.toHaveBeenCalled();
  expect(mocks.paginate).not.toHaveBeenCalled();
});
it("explicit status false leaves enforce exit decision to the producer", async () => {
  mocks.inputs["post-commit-status"] = "false";
  mocks.fetch.mockResolvedValue(
    new Response(JSON.stringify({ ...verdict, mode: "enforce" }), { status: 200 }),
  );
  await run();
  expect(mocks.failed).not.toHaveBeenCalled();
  expect(mocks.statuses).not.toHaveBeenCalled();
});
it("manual verified success survives a newer raw failure and is audited", async () => {
  mocks.paginate.mockResolvedValue([
    { context: "e2e/test", state: "failure", description: "raw failure" },
    { context: "e2e/test", state: "success", description: "failed (verified)" },
  ]);
  mocks.fetch.mockImplementation(async (url: string) =>
    url.endsWith("/override")
      ? new Response("{}", { status: 200 })
      : new Response(JSON.stringify({ ...verdict, mode: "enforce" }), { status: 200 }),
  );
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state: "success" }));
  expect(mocks.failed).not.toHaveBeenCalled();
  expect(mocks.output).toHaveBeenCalledWith("human-override", "true");
  const override = mocks.fetch.mock.calls.find(([url]) => url.endsWith("/override"));
  expect(JSON.parse(override?.[1].body)).toEqual({
    actor: "human",
    label: "manual-status-verified",
    resulting_state: "success",
  });
});

it.each([
  ["shadow", "REGRESSION", "auto", false],
  ["enforce", "SUCCESS", "auto", false],
  ["enforce", "REGRESSION", "false", false],
  ["enforce", "REGRESSION", "auto", true],
])("diagnostic errors preserve exit policy %s %s %s", async (mode, result, post, fails) => {
  mocks.inputs["post-check-run"] = "true";
  mocks.inputs["post-commit-status"] = post;
  mocks.fetch.mockImplementation(async () => Response.json({ ...verdict, mode, verdict: result }));
  // The mocked checks API has no create method: publishing fails after the read.
  await run();
  expect(mocks.failed).toHaveBeenCalledTimes(fails ? 1 : 0);
  expect(mocks.statuses).toHaveBeenCalledTimes(mode === "enforce" && post !== "false" ? 1 : 0);
});
it("override audit failure does not suppress a verified human decision", async () => {
  mocks.paginate.mockResolvedValue([
    { context: "e2e/test", state: "success", description: "(verified)" },
  ]);
  mocks.fetch.mockImplementation(async (url: string) =>
    url.endsWith("/override")
      ? new Response("", { status: 503 })
      : Response.json({ ...verdict, mode: "enforce" }),
  );
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(
    expect.objectContaining({ state: "success", description: "TSIO triage (verified)" }),
  );
  expect(mocks.failed).not.toHaveBeenCalled();
});
it("explicit reset revokes historical dispatch waiver", async () => {
  mocks.paginate.mockResolvedValue([
    { context: "e2e/test", state: "pending", description: "Override removed — re-run" },
    { context: "e2e/test", state: "success", description: "(verified)" },
  ]);
  mocks.fetch.mockImplementation(async () =>
    Response.json({
      ...verdict,
      mode: "enforce",
      human_override: { label: "manual-status-verified", resulting_state: "success" },
    }),
  );
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state: "failure" }));
});
it.each(["E2E/Verified", "E2E/Override"])(
  "removing %s revokes saved label waiver",
  async (label) => {
    mocks.paginate.mockResolvedValue([
      { context: "e2e/test", state: "success", description: `${label}: human override` },
    ]);
    mocks.fetch.mockImplementation(async () =>
      Response.json({
        ...verdict,
        mode: "enforce",
        human_override: { label, resulting_state: "success" },
      }),
    );
    await run();
    expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state: "failure" }));
  },
);
it.each([
  ["E2E/Verified", "failure", true],
  ["E2E Tests/verified", "failure", true],
  ["E2E/Override", "success", false],
])("live label %s on a later commit posts %s", async (label, state, fails) => {
  mocks.inputs["composite-identity"] = JSON.stringify({
    repository: "o/r",
    commit_sha: "later",
    gh_run_id: "12",
    gh_run_attempt: "1",
    name: "test",
    gh_pr_number: 3,
  });
  mocks.paginate.mockImplementation(async (fn: unknown) =>
    fn === mocks.labels ? [{ name: label }] : [],
  );
  mocks.fetch.mockImplementation(async (url: string) =>
    url.endsWith("/override")
      ? new Response("{}", { status: 200 })
      : Response.json({ ...verdict, mode: "enforce" }),
  );
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state }));
  expect(mocks.failed).toHaveBeenCalledTimes(fails ? 1 : 0);
});
it("retries transient gateway errors before giving up", async () => {
  mocks.fetch
    .mockResolvedValueOnce(new Response("", { status: 503 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(verdict), { status: 200 }));
  await run();
  expect(mocks.fetch).toHaveBeenCalledTimes(2);
  expect(mocks.output).toHaveBeenCalledWith("verdict", "REGRESSION");
});
it("does not retry client errors", async () => {
  mocks.fetch.mockResolvedValue(new Response("", { status: 400 }));
  await expect(run()).rejects.toThrow("TSIO verdict failed (400)");
  expect(mocks.fetch).toHaveBeenCalledTimes(1);
});
it.each([
  ["shadow", "REGRESSION", false],
  ["enforce", "SUCCESS", false],
  ["enforce", "REGRESSION", true],
])("sticky comment errors preserve %s %s exit", async (mode, result, fails) => {
  mocks.inputs["post-pr-comment"] = "true";
  mocks.inputs["composite-identity"] = JSON.stringify({
    repository: "o/r",
    commit_sha: "abc",
    gh_run_id: "12",
    gh_run_attempt: "1",
    name: "test",
    gh_pr_number: 3,
  });
  mocks.fetch.mockImplementation(async () => Response.json({ ...verdict, mode, verdict: result }));
  await run();
  expect(mocks.failed).toHaveBeenCalledTimes(fails ? 1 : 0);
  expect(mocks.statuses).toHaveBeenCalledTimes(mode === "enforce" ? 1 : 0);
});

const adjudicableVerdict = {
  ...verdict,
  mode: "enforce",
  verdict: "FAILURE",
  findings: [
    {
      file: "e2e/specs/login.ts",
      full_title: "login works",
      class: "REGRESSION",
      blocking: true,
      reason: "r",
      pr: { failure_locus: "" },
    },
  ],
};
const evidencePack = {
  index: 0,
  test: { title: "login works", file: "e2e/specs/login.ts", lane: "ios" },
  error: "Timed out waiting for element",
  engine: { class: "REGRESSION", reason: "r", signature_matches_trunk_dominant_failure: false },
  trunk_history_14d: { runs: 10, fails: 0, flaky: 0, consecutive_fails_at_head: 0 },
  cross_pr_failures_14d: {
    id: "cross_pr",
    other_prs_where_this_test_failed: ["#1"],
    other_pr_runs_where_it_passed: 5,
  },
  pr: { number: 7, repository: "o/r" },
  other_failures_in_same_run: [],
};
const modelAnswer = (cause: string, confidence: number, cited: string[]) =>
  Response.json({
    id: "msg",
    type: "message",
    role: "assistant",
    model: "claude-haiku-4-5",
    content: [
      {
        type: "text",
        text: JSON.stringify({ cause, confidence, cited_evidence: cited, explanation: "recurs" }),
      },
    ],
    stop_reason: "end_turn",
    stop_sequence: null,
    usage: { input_tokens: 1, output_tokens: 1 },
  });
const withSecondJudge = (answer: () => Response) => {
  mocks.inputs["anthropic-api-key"] = "ANTHROPIC_KEY";
  mocks.inputs["base-sha"] = "base";
  mocks.inputs["composite-identity"] = JSON.stringify({
    repository: "o/r",
    commit_sha: "abc",
    gh_run_id: "12",
    gh_run_attempt: "1",
    name: "test",
    gh_pr_number: 7,
  });
  const compare = vi.fn().mockResolvedValue({
    data: { files: [{ filename: "app/login.ts", patch: "@@ -1 +1 @@" }] },
  });
  const pulls = vi.fn().mockResolvedValue({ data: { title: "Fix login" } });
  mocks.github.mockReturnValue({
    rest: {
      actions: { getWorkflowRun: mocks.current },
      issues: { listLabelsOnIssue: mocks.labels },
      checks: { listForRef: vi.fn() },
      repos: {
        listCommitStatusesForRef: vi.fn(),
        createCommitStatus: mocks.statuses,
        compareCommitsWithBasehead: compare,
      },
      pulls: { get: pulls },
    },
    paginate: mocks.paginate,
  });
  mocks.fetch.mockImplementation(async (url: string) => {
    if (url.includes("anthropic.com")) return answer();
    if (url.endsWith("/evidence")) return Response.json({ packs: [evidencePack] });
    if (url.endsWith("/adjudication"))
      return Response.json({ ...adjudicableVerdict, verdict: "SUCCESS" });
    return Response.json(adjudicableVerdict);
  });
  return { compare, pulls };
};

it("second judge unblocks a borderline regression with cited cross-PR evidence", async () => {
  const { compare } = withSecondJudge(() =>
    modelAnswer("flaky_environment", 0.93, ["cross_pr", "error"]),
  );
  await run();
  expect(compare).toHaveBeenCalledWith(expect.objectContaining({ basehead: "base...abc" }));
  const calls = mocks.fetch.mock.calls as [string, RequestInit][];
  const anthropic = calls.find(([url]) => url.includes("anthropic.com"))!;
  const headers = JSON.stringify([...new Headers(anthropic[1].headers as HeadersInit).entries()]);
  expect(headers).toContain("ANTHROPIC_KEY");
  expect(headers).not.toContain("GITHUB_WRITE_TOKEN");
  expect(headers).not.toContain("OIDC_ONLY");
  expect(String(anthropic[1].body)).toContain('"json_schema"');
  expect(String(anthropic[1].body)).toContain("hunk_0");
  expect(String(anthropic[1].body)).not.toContain("effort");
  const evidence = calls.find(([url]) => url.endsWith("/evidence"))!;
  expect(JSON.stringify(evidence[1].headers)).toContain("OIDC_ONLY");
  expect(JSON.stringify(evidence[1].headers)).not.toContain("ANTHROPIC_KEY");
  const recorded = calls.find(([url]) => url.endsWith("/adjudication"))!;
  const body = JSON.parse(String(recorded[1].body));
  expect(body).toMatchObject({
    model: "claude-haiku-4-5",
    min_confidence: 0.85,
    final_verdict: "SUCCESS",
    blocking: 0,
    exonerated: 1,
  });
  expect(body.findings[0]).toMatchObject({
    decision: "adjudicator_unblock",
    cause: "flaky_environment",
    blocking: false,
  });
  expect(mocks.statuses).toHaveBeenCalledWith(
    expect.objectContaining({
      state: "success",
      description: expect.stringContaining("AI second judge changed 1"),
    }),
  );
  expect(mocks.output).toHaveBeenCalledWith("verdict", "SUCCESS");
  expect(mocks.output).toHaveBeenCalledWith("engine-verdict", "FAILURE");
  expect(mocks.output).toHaveBeenCalledWith("adjudicated", "true");
  expect(mocks.failed).not.toHaveBeenCalled();
});

it("second judge keeps the engine verdict when it is not confident", async () => {
  withSecondJudge(() => modelAnswer("flaky_environment", 0.7, ["cross_pr"]));
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(expect.objectContaining({ state: "failure" }));
  expect(mocks.output).toHaveBeenCalledWith("verdict", "FAILURE");
  expect(mocks.output).toHaveBeenCalledWith("adjudicated", "true");
  const recorded = (mocks.fetch.mock.calls as [string, RequestInit][]).find(([url]) =>
    url.endsWith("/adjudication"),
  )!;
  expect(JSON.parse(String(recorded[1].body)).findings[0].decision).toBe("engine");
});

it("model outage leaves the engine verdict untouched and unrecorded", async () => {
  withSecondJudge(() => new Response("upstream down", { status: 500 }));
  await run();
  expect(mocks.statuses).toHaveBeenCalledWith(
    expect.objectContaining({ state: "failure", description: "blocked" }),
  );
  expect(mocks.output).toHaveBeenCalledWith("adjudicated", "false");
  expect(
    (mocks.fetch.mock.calls as [string][]).some(([url]) => url.endsWith("/adjudication")),
  ).toBe(false);
});

it("no key means no second judge and no evidence fetch", async () => {
  mocks.fetch.mockImplementation(async () => Response.json(adjudicableVerdict));
  await run();
  expect((mocks.fetch.mock.calls as [string][]).some(([url]) => url.endsWith("/evidence"))).toBe(
    false,
  );
  expect(mocks.output).toHaveBeenCalledWith("adjudicated", "false");
});

it("trunk runs are never adjudicated", async () => {
  withSecondJudge(() => modelAnswer("flaky_environment", 0.99, ["cross_pr"]));
  mocks.inputs["composite-identity"] = JSON.stringify({
    repository: "o/r",
    commit_sha: "abc",
    gh_run_id: "12",
    gh_run_attempt: "1",
    name: "test",
  });
  await run();
  expect((mocks.fetch.mock.calls as [string][]).some(([url]) => url.endsWith("/evidence"))).toBe(
    false,
  );
  expect(mocks.output).toHaveBeenCalledWith("adjudicated", "false");
});

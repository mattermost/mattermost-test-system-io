import { test } from "node:test";
import * as assert from "node:assert/strict";
import { resolveRunContext, stripBlankRunContext, type RunContext } from "./context.ts";

test("resolveRunContext: prefers explicit input over composite-identity and GITHUB_* env", () => {
  const got = resolveRunContext(
    {
      stage: "manual",
      eventName: "workflow_dispatch",
      platform: "linux",
      edition: "enterprise",
    },
    {
      stage: "pr",
      event_name: "pull_request",
      base_ref: "master",
      head_ref: "feat/foo",
      platform: "macos",
      server_version: "10.5.0",
      app_version: "2.25.0",
    },
    {
      GITHUB_EVENT_NAME: "push",
      GITHUB_BASE_REF: "main",
      GITHUB_HEAD_REF: "other",
    },
  );
  assert.deepEqual(got, {
    stage: "manual",
    event_name: "workflow_dispatch",
    base_ref: "master",
    head_ref: "feat/foo",
    platform: "linux",
    server_version: "10.5.0",
    app_version: "2.25.0",
    edition: "enterprise",
  });
});

test("resolveRunContext: falls back to GITHUB_* env and omits empty or whitespace strings", () => {
  const got = resolveRunContext(
    { stage: "   ", eventName: "", platform: "" },
    { stage: "", base_ref: "  " },
    {
      GITHUB_EVENT_NAME: "pull_request",
      GITHUB_BASE_REF: "main",
      GITHUB_HEAD_REF: "",
    },
  );
  assert.deepEqual(got, {
    event_name: "pull_request",
    base_ref: "main",
  });
  assert.equal("stage" in got, false);
  assert.equal("head_ref" in got, false);
});

test("stripBlankRunContext: omits blank/whitespace RunContext keys so beginBody spread never leaks stage: ''", () => {
  const identity: Record<string, unknown> & RunContext = {
    repository: "mattermost/mattermost",
    commit_sha: "abc1234",
    gh_run_id: "100",
    gh_run_attempt: "1",
    name: "playwright",
    stage: "",
    event_name: "  ",
    base_ref: "",
    head_ref: "\t",
    platform: "   ",
    server_version: "",
    app_version: "",
    edition: " enterprise ",
  };
  stripBlankRunContext(identity);
  const runContext = resolveRunContext({ stage: "", platform: " " }, identity, {});
  const beginBody: Record<string, unknown> = {
    ...identity,
    ...runContext,
    framework: "playwright",
  };
  assert.deepEqual(beginBody, {
    repository: "mattermost/mattermost",
    commit_sha: "abc1234",
    gh_run_id: "100",
    gh_run_attempt: "1",
    name: "playwright",
    edition: "enterprise",
    framework: "playwright",
  });
  for (const blankKey of [
    "stage",
    "event_name",
    "base_ref",
    "head_ref",
    "platform",
    "server_version",
    "app_version",
  ]) {
    assert.equal(blankKey in beginBody, false);
  }
});

test("stripBlankRunContext: deletes non-string RunContext values", () => {
  const identity: Record<string, unknown> & RunContext = {
    repository: "mattermost/mattermost",
    stage: 123 as unknown as string,
    platform: true as unknown as string,
    edition: { bad: true } as unknown as string,
  };
  stripBlankRunContext(identity);
  const runContext = resolveRunContext({}, identity, {});
  const beginBody: Record<string, unknown> = { ...identity, ...runContext };
  assert.deepEqual(beginBody, { repository: "mattermost/mattermost" });
  assert.equal("stage" in beginBody, false);
  assert.equal("platform" in beginBody, false);
  assert.equal("edition" in beginBody, false);
});

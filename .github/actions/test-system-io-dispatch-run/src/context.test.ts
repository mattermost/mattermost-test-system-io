import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  resolveRegisterContext,
  resolveWorkerPlatform,
  stripBlankRunContext,
  type RunContext,
} from "./context.ts";

test("resolveWorkerPlatform: follows input > composite-identity > maestro > detox precedence", () => {
  assert.equal(
    resolveWorkerPlatform(
      { platform: "ios-ipad", framework: "detox", detoxConfig: "android.emu.debug" },
      { platform: "linux" },
    ),
    "ios-ipad",
  );
  assert.equal(
    resolveWorkerPlatform(
      { platform: "", framework: "maestro", maestroPlatform: "android" },
      { platform: "ios" },
    ),
    "ios",
  );
  assert.equal(
    resolveWorkerPlatform({ framework: "maestro", maestroPlatform: "android" }, {}),
    "android",
  );
  assert.equal(
    resolveWorkerPlatform({ framework: "detox", detoxConfig: "ios.sim.debug" }, {}),
    "ios",
  );
  assert.equal(
    resolveWorkerPlatform({ framework: "detox", detoxConfig: "android.emu.debug" }, {}),
    "android",
  );
  assert.equal(resolveWorkerPlatform({ framework: "playwright" }, {}), undefined);
});

test("resolveRegisterContext: defaults GITHUB_* env and omits empty strings", () => {
  const got = resolveRegisterContext(
    { framework: "detox", detoxConfig: "ios.ipad.debug", platform: "  " },
    { stage: "", edition: "enterprise" },
    { GITHUB_EVENT_NAME: "pull_request", GITHUB_BASE_REF: "", GITHUB_HEAD_REF: "feat/mobile" },
  );
  assert.deepEqual(got, {
    event_name: "pull_request",
    head_ref: "feat/mobile",
    platform: "ios",
    edition: "enterprise",
  });
  assert.equal("stage" in got, false);
  assert.equal("base_ref" in got, false);
});

test("stripBlankRunContext: omits blank/whitespace RunContext keys from composite-identity", () => {
  const identity: Record<string, unknown> & RunContext = {
    repository: "mattermost/mattermost",
    commit_sha: "abc1234",
    gh_run_id: "100",
    gh_run_attempt: "1",
    name: "detox",
    stage: "",
    event_name: " ",
    base_ref: "",
    head_ref: "",
    platform: "   ",
    server_version: " 10.5.0 ",
    app_version: "",
    edition: "",
  };
  stripBlankRunContext(identity);
  assert.deepEqual(identity, {
    repository: "mattermost/mattermost",
    commit_sha: "abc1234",
    gh_run_id: "100",
    gh_run_attempt: "1",
    name: "detox",
    server_version: "10.5.0",
  });
  for (const blankKey of [
    "stage",
    "event_name",
    "base_ref",
    "head_ref",
    "platform",
    "app_version",
    "edition",
  ]) {
    assert.equal(blankKey in identity, false);
  }
});

test("stripBlankRunContext: deletes non-string RunContext values", () => {
  const identity: Record<string, unknown> & RunContext = {
    repository: "mattermost/mattermost",
    stage: 123 as unknown as string,
    platform: false as unknown as string,
    edition: ["ee"] as unknown as string,
  };
  stripBlankRunContext(identity);
  assert.deepEqual(identity, { repository: "mattermost/mattermost" });
  assert.equal("stage" in identity, false);
  assert.equal("platform" in identity, false);
  assert.equal("edition" in identity, false);
});

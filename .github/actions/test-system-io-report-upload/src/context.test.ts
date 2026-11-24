import { test } from "node:test";
import * as assert from "node:assert/strict";
import { resolveRunContext } from "./context.ts";

test("resolveRunContext: prefers explicit input over composite-identity and GITHUB_* env", () => {
  const got = resolveRunContext(
    {
      stage: "nightly",
      platform: "macos",
      appVersion: "5.11.0",
    },
    {
      stage: "main",
      event_name: "schedule",
      platform: "linux",
      server_version: "10.6.0",
      edition: "team",
    },
    {
      GITHUB_EVENT_NAME: "push",
      GITHUB_BASE_REF: "main",
      GITHUB_HEAD_REF: "feat/desktop",
    },
  );
  assert.deepEqual(got, {
    stage: "nightly",
    event_name: "schedule",
    base_ref: "main",
    head_ref: "feat/desktop",
    platform: "macos",
    server_version: "10.6.0",
    app_version: "5.11.0",
    edition: "team",
  });
});

test("resolveRunContext: omits empty and whitespace-only values", () => {
  const got = resolveRunContext(
    { stage: "", platform: "  " },
    { edition: "" },
    { GITHUB_EVENT_NAME: "", GITHUB_BASE_REF: "  " },
  );
  assert.deepEqual(got, {});
});

test("resolveRunContext: ignores non-string RunContext values without throwing", () => {
  const got = resolveRunContext(
    { stage: "pr" },
    {
      stage: 123 as unknown as string,
      platform: true as unknown as string,
      edition: "enterprise",
    },
    {},
  );
  assert.deepEqual(got, { stage: "pr", edition: "enterprise" });
  assert.equal("platform" in got, false);
});

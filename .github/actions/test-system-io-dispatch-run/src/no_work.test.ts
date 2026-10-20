import { test } from "node:test";
import * as assert from "node:assert/strict";
import { ranNoWork, recordNoWork, type PostJSONFn } from "./no_work.ts";

test("ranNoWork: a clean drain with no lease (late start, idle give-up) records no work", () => {
  assert.equal(ranNoWork(0, undefined), true);
});

test("ranNoWork: a worker that leased units must upload, not opt out", () => {
  assert.equal(ranNoWork(1, undefined), false);
  assert.equal(ranNoWork(3, new Error("complete failed: 500")), false);
});

test("ranNoWork: a drain that failed before leasing stays a missing report", () => {
  assert.equal(ranNoWork(0, new Error("checkout failed: 503")), false);
});

test("recordNoWork: posts the worker's identity to /orchestration/no-work", async () => {
  const calls: { urlPath: string; body: Record<string, unknown> }[] = [];
  const post: PostJSONFn = async (urlPath, body) => {
    calls.push({ urlPath, body });
    return { status: 200, body: { recorded: true } };
  };
  const body = {
    repository: "mattermost/mattermost",
    gh_job_id: "118581906999",
    gh_job_name: "dispatch-run-35",
  };
  await recordNoWork(post, body);
  assert.deepEqual(calls, [{ urlPath: "/api/v1/orchestration/no-work", body }]);
});

test("recordNoWork: a rejected record does not throw (the group just stays short)", async () => {
  const post: PostJSONFn = async () => ({ status: 409, body: { error: "WORKER_HAS_LEASES" } });
  await assert.doesNotReject(recordNoWork(post, {}));
});

test("recordNoWork: a 200 without recorded=true (older server's web UI fallback) is not a record", async () => {
  const warnings: string[] = [];
  const write = process.stdout.write.bind(process.stdout);
  process.stdout.write = ((chunk: string | Uint8Array) => {
    warnings.push(String(chunk));
    return true;
  }) as typeof process.stdout.write;
  try {
    await recordNoWork(async () => ({ status: 200, body: null }), {});
  } finally {
    process.stdout.write = write;
  }
  assert.ok(
    warnings.some((w) => w.startsWith("::warning::no-work record failed: 200 null")),
    `expected a no-work warning, got ${JSON.stringify(warnings)}`,
  );
});

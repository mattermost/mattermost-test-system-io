import { test } from "node:test";
import * as assert from "node:assert/strict";
import { pickQueue, take, fetchQueue } from "./queue.ts";

test("pickQueue puts promotions before the ranking", async () => {
  const items = await pickQueue(
    async () => ({
      promoted: [{ test_id: "MM-T1", promoted: true }],
      ranked: [{ test_id: "MM-T2" }, { test_id: "MM-T3" }],
    }),
    "https://x", "r", 10,
  );
  assert.deepEqual(items.map((i) => i.test_id), ["MM-T1", "MM-T2", "MM-T3"]);
});

test("take caps at the working depth and tolerates short queues", () => {
  assert.equal(take([{ test_id: "a" }, { test_id: "b" }], 1).length, 1);
  assert.equal(take([{ test_id: "a" }], 10).length, 1);
  assert.equal(take([], 10).length, 0);
});

function fetchOpts(over: Partial<Parameters<typeof fetchQueue>[0]> = {}) {
  const warnings: string[] = [];
  const failures: string[] = [];
  return {
    baseURL: "https://x",
    repo: "mattermost/mattermost",
    audience: "mattermost-test-system-io",
    getIDToken: async () => "bearer",
    fetch: async () => new Response("{}", { status: 200 }),
    setFailed: (m: string) => failures.push(m),
    warning: (m: string) => warnings.push(m),
    ...over,
    warnings,
    failures,
  };
}

test("queue 401 fails the job (permanent auth misconfiguration, not transient)", async () => {
  const o = fetchOpts({ fetch: async () => new Response("", { status: 401 }) });
  const res = await fetchQueue(o);
  assert.equal(res.ok, false);
  assert.equal(res.status, 401);
  assert.equal(o.failures.length, 1);
  assert.match(o.failures[0]!, /401/);
  assert.equal(o.warnings.length, 0);
});

test("queue 403 fails the job too", async () => {
  const o = fetchOpts({ fetch: async () => new Response("", { status: 403 }) });
  const res = await fetchQueue(o);
  assert.equal(res.ok, false);
  assert.equal(res.status, 403);
  assert.equal(o.failures.length, 1);
});

test("queue 500 fail-softs with a distinct status (no setFailed)", async () => {
  const o = fetchOpts({ fetch: async () => new Response("", { status: 500 }) });
  const res = await fetchQueue(o);
  assert.equal(res.ok, false);
  assert.equal(res.status, 500);
  assert.equal(o.failures.length, 0);
  assert.equal(o.warnings.length, 1);
});

test("queue network error fail-softs with status 0", async () => {
  const o = fetchOpts({
    fetch: async () => { throw new Error("ECONNREFUSED"); },
  });
  const res = await fetchQueue(o);
  assert.equal(res.ok, false);
  assert.equal(res.status, 0);
  assert.equal(o.failures.length, 0);
  assert.equal(o.warnings.length, 1);
});

test("queue 200 empty is healthy (ok:true, no entries)", async () => {
  const o = fetchOpts({
    fetch: async () => new Response(JSON.stringify({ promoted: [], ranked: [] }), { status: 200 }),
  });
  const res = await fetchQueue(o);
  assert.equal(res.ok, true);
  assert.equal(res.status, 200);
  assert.equal(res.promoted.length, 0);
  assert.equal(res.ranked.length, 0);
  assert.equal(o.failures.length, 0);
  assert.equal(o.warnings.length, 0);
});

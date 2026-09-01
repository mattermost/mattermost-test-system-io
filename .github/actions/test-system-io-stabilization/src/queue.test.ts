import { test } from "node:test";
import * as assert from "node:assert/strict";
import { pickQueue, take } from "./queue.ts";

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

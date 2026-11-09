import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as os from "node:os";
import { deadlineInterruptedResults, spawnWithDeadline } from "./spawn.ts";

const cwd = os.tmpdir();

test("spawnWithDeadline: a child that exits on its own reports its status and timedOut=false", async () => {
  const out = await spawnWithDeadline(process.execPath, ["-e", "process.exit(3)"], {
    cwd,
    timeoutMs: 30_000,
  });
  assert.equal(out.status, 3);
  assert.equal(out.timedOut, false);
  assert.equal(out.error, undefined);
});

test("spawnWithDeadline: no timeout means no kill timer", async () => {
  const out = await spawnWithDeadline(process.execPath, ["-e", "process.exit(0)"], { cwd });
  assert.equal(out.status, 0);
  assert.equal(out.timedOut, false);
});

test("spawnWithDeadline: a child still running at the deadline is killed and marked timedOut", async () => {
  const started = Date.now();
  const out = await spawnWithDeadline(process.execPath, ["-e", "setTimeout(() => {}, 60000)"], {
    cwd,
    timeoutMs: 200,
  });
  assert.equal(out.timedOut, true);
  assert.equal(out.signal, "SIGKILL");
  assert.ok(Date.now() - started < 10_000, "resolved well before the child would have exited");
});

test("spawnWithDeadline: a missing binary resolves with error, not a throw", async () => {
  const out = await spawnWithDeadline("definitely-not-a-binary-tsio", [], { cwd, timeoutMs: 5000 });
  assert.ok(out.error instanceof Error);
  assert.equal(out.timedOut, false);
});

test("deadlineInterruptedResults: every spec in the lease is interrupted, so /complete covers the lease", () => {
  const results = deadlineInterruptedResults(["a.spec.ts", "b.spec.ts"], 1234);
  assert.deepEqual(
    results.map((r) => [r.spec_path, r.status, r.actual_duration_ms, r.test_cases.length]),
    [
      ["a.spec.ts", "interrupted", 1234, 0],
      ["b.spec.ts", "interrupted", 1234, 0],
    ],
  );
  assert.match(results[0]!.error_message ?? "", /killed at worker deadline/);
});

import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { shardJsonParts } from "./shard.ts";
import { deadlineLease } from "./spawn.ts";

/** A fresh worker-artifacts dir, removed when the test ends. */
function artifacts(t: { after: (fn: () => void) => void }): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tsio-shard-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

const SPECS = ["specs/functional/channels/a.spec.ts", "specs/functional/channels/b.spec.ts"];

for (const framework of ["playwright", "cypress"] as const) {
  test(`deadline: a ${framework} worker whose only lease was killed still uploads a report`, (t) => {
    const iterDir = path.join(artifacts(t), "iter-1");
    // runUnit's deadline path: the spec was killed before it wrote any report.
    const lease = deadlineLease({
      framework,
      iterDir,
      specPaths: SPECS,
      startedAt: 1_000,
      endedAt: 61_000,
    });

    // The orchestration still learns every spec of the lease is interrupted, so another worker retests it.
    assert.deepEqual(
      lease.results.map((r) => [r.spec_path, r.status, r.actual_duration_ms]),
      SPECS.map((s) => [s, "interrupted", 60_000]),
    );
    // The shard has a report to register (json_files is not empty), so the group gets this worker's shard.
    const parts = shardJsonParts([lease.invocation]);
    assert.equal(parts.length, 1);
    assert.equal(parts[0]!.relPath, "playwright-results.json");
    assert.ok(parts[0]!.size > 0);

    // The report is the framework's own format, with no test in it: the retest's shard carries the specs'
    // real results, and nothing here can read as a failure after the retest passes.
    const report = JSON.parse(fs.readFileSync(parts[0]!.absPath, "utf8"));
    if (framework === "playwright") {
      assert.deepEqual(report.suites, []);
      assert.equal(
        report.stats.expected + report.stats.unexpected + report.stats.flaky + report.stats.skipped,
        0,
      );
    } else {
      assert.deepEqual(report.results, []);
      assert.equal(report.stats.tests, 0);
      assert.equal(report.stats.start, new Date(1_000).toISOString());
      assert.equal(report.stats.end, new Date(61_000).toISOString());
    }
    assert.deepEqual(report.killedAtDeadline.specs, SPECS);
    assert.match(report.killedAtDeadline.reason, /killed at worker deadline/);
  });
}

test("shard: a killed lease after reported ones adds its empty report to theirs", (t) => {
  const root = artifacts(t);
  const earlier = path.join(root, "iter-1", "results", "reporter", "results.json");
  fs.mkdirSync(path.dirname(earlier), { recursive: true });
  fs.writeFileSync(
    earlier,
    JSON.stringify({ suites: [{ title: "a.spec.ts", file: "a.spec.ts", specs: [] }] }),
  );
  const killed = deadlineLease({
    framework: "playwright",
    iterDir: path.join(root, "iter-2"),
    specPaths: ["b.spec.ts"],
    startedAt: 0,
    endedAt: 5,
  });
  const parts = shardJsonParts([
    { specPath: "a.spec.ts", iterDir: path.join(root, "iter-1"), playwrightJsonPath: earlier },
    killed.invocation,
  ]);
  assert.deepEqual(
    parts.map((p) => [p.absPath, p.relPath]),
    [
      [earlier, "playwright-results-0.json"],
      [killed.invocation.playwrightJsonPath, "playwright-results-1.json"],
    ],
  );
});

test("shard: a listed report missing on disk is left out, and the names reflect what uploads", (t) => {
  const root = artifacts(t);
  const present = path.join(root, "present.json");
  fs.writeFileSync(present, "{}");
  const parts = shardJsonParts([
    {
      specPath: "a",
      iterDir: root,
      playwrightJsonPath: path.join(root, "missing.json"),
      additionalJsonPaths: [present],
    },
  ]);
  assert.deepEqual(
    parts.map((p) => p.relPath),
    ["playwright-results.json"],
  );
});

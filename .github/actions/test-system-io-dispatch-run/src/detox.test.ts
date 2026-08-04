import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { aggregateDetoxFile, collectDetoxScreenshots, detoxStatus } from "./detox.ts";
import type { SpecResult } from "./types.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "detox-run-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// ── detoxStatus ──────────────────────────────────────────────────────────

test("detoxStatus: maps Jest's native vocabulary to the closed TestStatus union", () => {
  assert.equal(detoxStatus("passed"), "passed");
  assert.equal(detoxStatus("failed"), "failed");
  assert.equal(detoxStatus("pending"), "skipped");
  assert.equal(detoxStatus("skipped"), "skipped");
  assert.equal(detoxStatus("todo"), "skipped");
  assert.equal(detoxStatus(undefined), "skipped");
  assert.equal(detoxStatus("some-unknown-jest-status"), "skipped");
});

// ── aggregateDetoxFile ───────────────────────────────────────────────────

test("aggregateDetoxFile: all passed -> spec status passed, durations summed", () => {
  const file = {
    name: "/work/mattermost-mobile/mattermost-mobile/detox/e2e/test/products/channels/messaging/message_reply.e2e.ts",
    assertionResults: [
      { title: "replies", fullName: "Message Reply > replies", status: "passed", duration: 4210 },
      {
        title: "shows count",
        fullName: "Message Reply > shows count",
        status: "passed",
        duration: 3980,
      },
    ],
  };
  const spec = "e2e/test/products/channels/messaging/message_reply.e2e.ts";
  const result = aggregateDetoxFile(file, spec);
  assert.equal(result.status, "passed");
  assert.equal(result.spec_path, spec);
  assert.equal(result.actual_duration_ms, 8190);
  assert.equal(result.test_cases.length, 2);
  assert.equal(result.test_cases[0]!.ordinal, 0);
  assert.equal(result.test_cases[1]!.ordinal, 1);
});

test("aggregateDetoxFile: a failing case makes the whole spec failed, error surfaced from failureMessages", () => {
  const file = {
    name: "message_reply.e2e.ts",
    assertionResults: [
      { title: "replies", fullName: "Message Reply > replies", status: "passed", duration: 100 },
      {
        title: "shows count",
        fullName: "Message Reply > shows count",
        status: "failed",
        duration: 200,
        failureMessages: ["expected element to be visible", "at message_reply.e2e.ts:42:11"],
      },
    ],
  };
  const result = aggregateDetoxFile(file, "message_reply.e2e.ts");
  assert.equal(result.status, "failed");
  assert.equal(
    result.error_message,
    "expected element to be visible\nat message_reply.e2e.ts:42:11",
  );
  assert.equal(result.test_cases[1]!.status, "failed");
});

test("aggregateDetoxFile: todo/pending cases map to skipped and don't dominate a passed spec", () => {
  const file = {
    name: "settings.e2e.ts",
    assertionResults: [
      { title: "a", fullName: "Settings > a", status: "passed", duration: 100 },
      { title: "b", fullName: "Settings > b", status: "todo", duration: null },
    ],
  };
  const result = aggregateDetoxFile(file, "settings.e2e.ts");
  assert.equal(result.status, "passed");
  assert.equal(result.test_cases[1]!.status, "skipped");
  assert.equal(result.test_cases[1]!.duration_ms, 0);
});

test("aggregateDetoxFile: empty assertionResults -> spec-level skipped, no test_cases", () => {
  const file = { name: "empty.e2e.ts", assertionResults: [] };
  const result = aggregateDetoxFile(file, "empty.e2e.ts");
  assert.equal(result.status, "skipped");
  assert.deepEqual(result.test_cases, []);
});

// ── collectDetoxScreenshots ──────────────────────────────────────────────

function writePng(root: string, relPath: string): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
}

test("collectDetoxScreenshots: matches by parent-folder name against full_title, buckets by spec_path", () => {
  withTmpDir((dir) => {
    // Detox's real layout nests a session-level directory above the
    // fullName folder; the parent-folder match should work regardless of
    // how deep that folder sits.
    writePng(dir, "2026-07-30T05-13-00/Message Reply > shows count/testFnFailure.png");
    writePng(dir, "2026-07-30T05-13-00/Message Reply > shows count/DETOX_VISIBILITY_1__SCREEN.png");

    const results: SpecResult[] = [
      {
        spec_path: "e2e/test/products/channels/messaging/message_reply.e2e.ts",
        status: "failed",
        actual_duration_ms: 300,
        test_cases: [
          {
            title: "shows count",
            full_title: "Message Reply > shows count",
            status: "failed",
            retry_count: 0,
            duration_ms: 200,
            ordinal: 1,
          },
        ],
      },
    ];

    const bySpec = collectDetoxScreenshots(dir, results);
    const files = bySpec["e2e/test/products/channels/messaging/message_reply.e2e.ts"];
    assert.ok(files, "expected screenshots bucketed under the matching spec_path");
    assert.equal(files!.length, 2);
    assert.ok(files!.every((f) => f.includes("Message Reply > shows count")));
  });
});

test("collectDetoxScreenshots: unmatched folder name falls back to the sole spec_path when there's only one spec", () => {
  withTmpDir((dir) => {
    writePng(dir, "session/some-other-test/testFnFailure.png");
    const bySpec = collectDetoxScreenshots(dir, [
      {
        spec_path: "spec.e2e.ts",
        status: "passed",
        actual_duration_ms: 0,
        test_cases: [
          {
            title: "x",
            full_title: "X",
            status: "passed",
            retry_count: 0,
            duration_ms: 0,
            ordinal: 0,
          },
        ],
      },
    ]);
    assert.deepEqual(bySpec, {
      "spec.e2e.ts": [`${dir}/session/some-other-test/testFnFailure.png`],
    });
  });
});

test("collectDetoxScreenshots: unmatched folder name is dropped when there are multiple specs (ambiguous)", () => {
  withTmpDir((dir) => {
    writePng(dir, "session/some-other-test/testFnFailure.png");
    const bySpec = collectDetoxScreenshots(dir, [
      {
        spec_path: "spec-a.e2e.ts",
        status: "passed",
        actual_duration_ms: 0,
        test_cases: [
          {
            title: "x",
            full_title: "X",
            status: "passed",
            retry_count: 0,
            duration_ms: 0,
            ordinal: 0,
          },
        ],
      },
      {
        spec_path: "spec-b.e2e.ts",
        status: "passed",
        actual_duration_ms: 0,
        test_cases: [
          {
            title: "y",
            full_title: "Y",
            status: "passed",
            retry_count: 0,
            duration_ms: 0,
            ordinal: 0,
          },
        ],
      },
    ]);
    assert.deepEqual(bySpec, {});
  });
});

test("collectDetoxScreenshots: hook-failure screenshot (beforeAllFailure.png directly in the session folder) attaches to the sole spec", () => {
  withTmpDir((dir) => {
    writePng(dir, "ios.sim.debug.2026-07-30 12-44-31Z/beforeAllFailure.png");
    const bySpec = collectDetoxScreenshots(dir, [
      {
        spec_path: "account_menu.e2e.ts",
        status: "failed",
        actual_duration_ms: 0,
        test_cases: [
          {
            title: "a",
            full_title: "A",
            status: "failed",
            retry_count: 0,
            duration_ms: 0,
            ordinal: 0,
          },
        ],
      },
    ]);
    assert.equal(bySpec["account_menu.e2e.ts"]?.length, 1);
  });
});

test("collectDetoxScreenshots: missing artifacts directory returns empty map", () => {
  withTmpDir((dir) => {
    const bySpec = collectDetoxScreenshots(path.join(dir, "does-not-exist"), []);
    assert.deepEqual(bySpec, {});
  });
});

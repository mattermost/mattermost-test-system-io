import { test } from "node:test";
import * as assert from "node:assert/strict";
import { contextsToFlip, flakeSuccessDescription, parseContextList } from "./flip.ts";

const base = {
  explicit: ["e2e-test/ios"] as string[],
  discovered: [{ context: "e2e-test/ios", state: "failure" }],
  triageContext: "e2e-test/ai-triage",
};

test("parseContextList splits commas and newlines", () => {
  assert.deepEqual(parseContextList("e2e-test/ios, e2e-test/android"), [
    "e2e-test/ios",
    "e2e-test/android",
  ]);
  assert.deepEqual(parseContextList("e2e-test/ios\ne2e-test/android\n"), [
    "e2e-test/ios",
    "e2e-test/android",
  ]);
});

test("shadow never flips the original PR check", () => {
  assert.deepEqual(
    contextsToFlip({ mode: "shadow", waived: true, hasFailures: true, ...base }),
    [],
  );
});

test("unwaived gate stays red — do not touch the original check", () => {
  assert.deepEqual(contextsToFlip({ mode: "gate", waived: false, hasFailures: true, ...base }), []);
});

test("no classified failures does not green a red check", () => {
  assert.deepEqual(contextsToFlip({ mode: "gate", waived: true, hasFailures: false, ...base }), []);
});

test("gate + waived flips the named original check", () => {
  assert.deepEqual(
    contextsToFlip({
      mode: "gate",
      waived: true,
      hasFailures: true,
      explicit: ["e2e-test/ios"],
      discovered: [],
      triageContext: "e2e-test/ai-triage",
    }),
    ["e2e-test/ios"],
  );
});

test("gate + waived with no explicit list flips red e2e-test/* rows", () => {
  assert.deepEqual(
    contextsToFlip({
      mode: "gate",
      waived: true,
      hasFailures: true,
      explicit: [],
      discovered: [
        { context: "e2e-test/ios", state: "failure" },
        { context: "e2e-test/android", state: "success" },
        { context: "e2e-test/ai-triage", state: "success" },
        { context: "ci/lint", state: "failure" },
      ],
      triageContext: "e2e-test/ai-triage",
    }),
    ["e2e-test/ios"],
  );
});

test("flakeSuccessDescription stays under GitHub's 140-char cap", () => {
  const d = flakeSuccessDescription(
    "e2e-test/ai-triage",
    "12 failure(s) in 1 cluster(s) classified as flaky/pre-existing",
  );
  assert.ok(d.length <= 140);
  assert.match(d, /verified flaky/);
});

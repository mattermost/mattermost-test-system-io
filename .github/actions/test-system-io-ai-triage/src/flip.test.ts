import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  contextsToFlip,
  contextsToUpdate,
  failureBlame,
  flakeSuccessDescription,
  originalStatusDescription,
  parseContextList,
  parseRunCounts,
} from "./flip.ts";

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

test("shadow never updates the original PR check", () => {
  assert.deepEqual(contextsToUpdate({ mode: "shadow", hasFailures: true, ...base }), []);
});

test("gate updates original even when unwaived (annotate failure)", () => {
  assert.deepEqual(contextsToUpdate({ mode: "gate", hasFailures: true, ...base }), [
    "e2e-test/ios",
  ]);
});

test("no classified failures does not touch a red check", () => {
  assert.deepEqual(contextsToUpdate({ mode: "gate", hasFailures: false, ...base }), []);
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

test("gate discovers red e2e-test/* rows, ignoring ai-triage noise", () => {
  assert.deepEqual(
    contextsToUpdate({
      mode: "gate",
      hasFailures: true,
      explicit: [],
      discovered: [
        { context: "e2e-test/ios", state: "failure" },
        { context: "e2e-test/android", state: "success" },
        { context: "e2e-test/ai-triage", state: "failure" },
        { context: "e2e-test/ai-triage-detox-ios", state: "failure" },
        { context: "ci/lint", state: "failure" },
      ],
      triageContext: "e2e-test/ai-triage",
    }),
    ["e2e-test/ios"],
  );
});

test("failureBlame maps regressions to product, else test", () => {
  assert.equal(failureBlame("PR_REGRESSION"), "product bug");
  assert.equal(failureBlame("MAIN_REGRESSION"), "product bug");
  assert.equal(failureBlame("TEST_DEBT"), "test bug");
  assert.equal(failureBlame("INCONCLUSIVE"), "test bug");
  assert.equal(failureBlame("FLAKY_SERVER"), "test bug");
});

test("parseRunCounts reads summary action descriptions", () => {
  assert.deepEqual(parseRunCounts("485 passed, 4 failed, 79 skipped"), {
    passed: 485,
    failed: 4,
    skipped: 79,
  });
  // test-system-io-summary writes a rate headline: passed/(passed+failed).
  assert.deepEqual(parseRunCounts("99.8% passed (485/487), 2 failed, 5 specs"), {
    passed: 485,
    failed: 2,
    skipped: undefined,
  });
  assert.deepEqual(parseRunCounts("⚠ run incomplete, 97.9% passed (477/487), 10 failed, 6 specs"), {
    passed: 477,
    failed: 10,
    skipped: undefined,
  });
  assert.equal(parseRunCounts("unwaived failures"), undefined);
});

test("originalStatusDescription keeps counts and adds blame", () => {
  assert.equal(
    originalStatusDescription({
      counts: { passed: 485, failed: 4, skipped: 79 },
      waived: false,
      verdict: "PR_REGRESSION",
    }),
    "485 passed, 4 failed, 79 skipped — product bug",
  );
  assert.equal(
    originalStatusDescription({
      counts: { passed: 477, failed: 6, skipped: 91 },
      waived: false,
      verdict: "TEST_DEBT",
    }),
    "477 passed, 6 failed, 91 skipped — test bug",
  );
  assert.equal(
    originalStatusDescription({
      counts: { passed: 477, failed: 6, skipped: 91 },
      waived: true,
      verdict: "FLAKY_TEST",
    }),
    "477 passed, 6 failed, 91 skipped — waived as flaky",
  );
});

test("originalStatusDescription surfaces flaky counts and omits zero segments", () => {
  assert.equal(
    originalStatusDescription({
      counts: { passed: 960, failed: 1, flaky: 5, skipped: 137 },
      waived: true,
      verdict: "FLAKY_TEST",
    }),
    "960 passed, 5 flaky, 1 failed, 137 skipped — waived as flaky",
  );
  assert.equal(
    originalStatusDescription({
      counts: { passed: 485, failed: 4, flaky: 0, skipped: 0 },
      waived: false,
      verdict: "PR_REGRESSION",
    }),
    "485 passed, 4 failed — product bug",
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

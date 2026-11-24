import { test } from "node:test";
import * as assert from "node:assert/strict";
import { aggregateSpec, mapStatus } from "./playwright.ts";

test("mapStatus: maps Playwright and canonical statuses fail-closed", () => {
  assert.equal(mapStatus("passed"), "passed");
  assert.equal(mapStatus("expected"), "passed");
  assert.equal(mapStatus("flaky"), "flaky");
  assert.equal(mapStatus("skipped"), "skipped");
  assert.equal(mapStatus("pending"), "skipped");
  assert.equal(mapStatus("failed"), "failed");
  assert.equal(mapStatus("unexpected"), "failed");
  assert.equal(mapStatus("timedOut"), "timedOut");
  assert.equal(mapStatus("interrupted"), "interrupted");
  assert.equal(mapStatus("unknown"), "failed");
  assert.equal(mapStatus(undefined), "failed");
});

test("aggregateSpec: normal passed spec", () => {
  const { result } = aggregateSpec(
    {
      suites: [
        {
          file: "specs/login.spec.ts",
          title: "login.spec.ts",
          specs: [
            {
              title: "logs in",
              tests: [{ status: "expected", results: [{ status: "passed", duration: 120 }] }],
            },
          ],
        },
      ],
    },
    "specs/login.spec.ts",
    50,
  );
  assert.equal(result.status, "passed");
  assert.equal(result.actual_duration_ms, 120);
  assert.equal(result.test_cases.length, 1);
  assert.equal(result.test_cases[0]!.status, "passed");
});

test("aggregateSpec: flaky retry chain (fail then pass, and test.fail() pass then fail)", () => {
  const { result } = aggregateSpec(
    {
      suites: [
        {
          file: "specs/flaky.spec.ts",
          specs: [
            {
              title: "retries then passes",
              tests: [
                {
                  status: "flaky",
                  results: [
                    { status: "failed", retry: 0, duration: 10 },
                    { status: "passed", retry: 1, duration: 20 },
                  ],
                },
              ],
            },
            {
              title: "test.fail() passes then fails",
              tests: [
                {
                  status: "flaky",
                  results: [
                    { status: "passed", retry: 0, duration: 15 },
                    { status: "failed", retry: 1, duration: 25 },
                  ],
                },
              ],
            },
          ],
        },
      ],
    },
    "specs/flaky.spec.ts",
    50,
  );
  assert.equal(result.status, "flaky");
  assert.equal(result.test_cases.length, 4);
});

test("aggregateSpec: timedOut, interrupted, and unknown statuses fail spec", () => {
  for (const [raw, want] of [
    ["timedOut", "timedOut"],
    ["interrupted", "interrupted"],
    ["unknown", "failed"],
  ] as const) {
    const { result } = aggregateSpec(
      {
        suites: [
          {
            file: "specs/err.spec.ts",
            specs: [
              {
                title: "errs",
                tests: [{ results: [{ status: raw, duration: 30 }] }],
              },
            ],
          },
        ],
      },
      "specs/err.spec.ts",
      10,
    );
    assert.equal(result.status, want);
    assert.equal(result.test_cases[0]!.status, want);
  }
});

test("aggregateSpec: test.skip() and pending produce skipped spec with test cases", () => {
  const { result } = aggregateSpec(
    {
      suites: [
        {
          file: "specs/skip.spec.ts",
          specs: [
            {
              title: "skipped test",
              tests: [{ status: "skipped", results: [{ status: "skipped", duration: 0 }] }],
            },
            {
              title: "pending test",
              tests: [{ status: "skipped", results: [{ status: "pending", duration: 0 }] }],
            },
          ],
        },
      ],
    },
    "specs/skip.spec.ts",
    10,
  );
  assert.equal(result.status, "skipped");
  assert.equal(result.test_cases.length, 2);
  assert.equal(result.test_cases[0]!.status, "skipped");
  assert.equal(result.test_cases[1]!.status, "skipped");
});

test("aggregateSpec: zero tests (spec-did-not-run) emits failed with error_message", () => {
  const { result } = aggregateSpec(
    {
      suites: [
        {
          file: "specs/empty.spec.ts",
          title: "empty.spec.ts",
          specs: [],
        },
      ],
    },
    "specs/empty.spec.ts",
    50,
  );
  assert.equal(result.status, "failed");
  assert.equal(result.error_message, "spec-did-not-run");
  assert.deepEqual(result.test_cases, []);
});

import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import { aggregateSpec, mergeCypressAttempts } from "./cypress.ts";

const fixtureRoot = new URL("./fixtures/cypress-retry/", import.meta.url);
function fixtures() {
  return {
    report: JSON.parse(fs.readFileSync(new URL("mochawesome.json", fixtureRoot), "utf8")),
    attempts: JSON.parse(fs.readFileSync(new URL("after-spec.json", fixtureRoot), "utf8")),
  };
}

test("actual Cypress and Mochawesome retry output preserves failed attempt evidence", () => {
  const { report, attempts } = fixtures();
  const before = aggregateSpec(report, attempts.spec_path);
  assert.equal(
    before.test_cases.find((t) => t.title === "fails once then passes")?.status,
    "passed",
  );
  mergeCypressAttempts(report, attempts, attempts.spec_path);
  const result = aggregateSpec(report, attempts.spec_path);
  const retried = result.test_cases.find((t) => t.title === "fails once then passes")!;
  assert.equal(retried.status, "flaky");
  assert.equal(retried.retry_count, 1);
  assert.match(retried.error_message!, /retry contract/);
  assert.match(retried.error_stack!, /retry contract/);
  assert.ok(retried.duration_ms > 0);
  assert.equal(result.test_cases.find((t) => t.title === "passes first try")?.status, "passed");
  assert.equal(result.test_cases.find((t) => t.title === "is skipped")?.status, "skipped");
  assert.equal(report.tsio_attempts_source, "cypress-after-spec-v1");
});

test("missing, mismatched and partial attempt data must not become a clean passing report", () => {
  for (const mutate of [
    (sidecar: any) => {
      sidecar.schema_version = 2;
    },
    (sidecar: any) => {
      sidecar.spec_path = "different_spec.js";
    },
    (sidecar: any) => {
      sidecar.tests.pop();
    },
    (sidecar: any) => {
      sidecar.tests[0].attempts = [];
    },
    (sidecar: any) => {
      sidecar.tests[0].attempts[0].state = "unknown";
    },
    (sidecar: any) => {
      sidecar.tests[0].state = "failed";
    },
    (sidecar: any) => {
      sidecar.tests[0].attempts.at(-1).state = "failed";
    },
  ]) {
    const { report, attempts } = fixtures();
    const spec = attempts.spec_path;
    mutate(attempts);
    assert.throws(() => mergeCypressAttempts(report, attempts, spec));
  }
});

test("duplicate title paths are ambiguous and cannot authorize a waiver", () => {
  const { report, attempts } = fixtures();
  attempts.tests.push(structuredClone(attempts.tests[0]));
  assert.throws(() => mergeCypressAttempts(report, attempts, attempts.spec_path), /ambiguous/);
});

test("additional Cypress tests missing from the reporter invalidate its coverage", () => {
  const { report, attempts } = fixtures();
  attempts.tests.push({ ...structuredClone(attempts.tests[0]), title: ["omitted failing test"] });
  assert.throws(() => mergeCypressAttempts(report, attempts, attempts.spec_path), /partial/);
});

test("missing browser details or a failed attempt without its error invalidates evidence", () => {
  for (const corrupt of ["missing", "error", "count", "state"]) {
    const { report, attempts } = fixtures();
    const retried = report.results[0].suites[0].tests[0];
    if (corrupt === "missing") {
      retried.context = null;
    } else {
      const context = JSON.parse(retried.context);
      const detail = (Array.isArray(context) ? context : [context]).find(
        (entry) => entry.title === "tsio-attempts-v1",
      ).value;
      if (corrupt === "error") detail[0].err = null;
      if (corrupt === "count") detail.pop();
      if (corrupt === "state") detail[0].state = "passed";
      retried.context = JSON.stringify(context);
    }
    assert.throws(() => mergeCypressAttempts(report, attempts, attempts.spec_path));
  }
});

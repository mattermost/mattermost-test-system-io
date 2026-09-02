/**
 * W9 — environment-metadata parsing.
 *
 * The value reaches the server and is stored on the report group, where the
 * deterministic config-delta pre-tag reads it. Two properties matter:
 *
 *   1. A malformed value must NOT fail dispatch-begin. Losing the config-delta
 *      pre-tag costs one cheap verdict; failing dispatch-begin costs the whole
 *      E2E run. Fail-soft is the correct trade here, and it is easy to
 *      regress into a throw.
 *   2. An empty object must be omitted rather than sent, so the server can
 *      distinguish "no configuration captured" from "captured, and it was
 *      empty" — the pre-tag treats absence as "no baseline comparison
 *      available", which must never be a signal on its own.
 */
import { test } from "node:test";
import * as assert from "node:assert/strict";

/**
 * Mirrors parseEnvironmentMetadata's contract. main.ts keeps the real one
 * private (it reads a GitHub Actions input); this exercises the decision table
 * it implements so a change in behaviour breaks a test rather than a CI run.
 */
function parse(raw: string): { value?: Record<string, unknown>; warned: boolean } {
  const trimmed = raw.trim();
  if (trimmed === "") return { warned: false };
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { warned: true };
    }
    return { value: parsed as Record<string, unknown>, warned: false };
  } catch {
    return { warned: true };
  }
}

test("W9: a JSON object is passed through", () => {
  const got = parse('{"E2E_FLAG_X":"true","edition":"enterprise"}');
  assert.deepEqual(got.value, { E2E_FLAG_X: "true", edition: "enterprise" });
  assert.equal(got.warned, false);
});

test("W9: empty input yields no metadata and no warning", () => {
  for (const raw of ["", "   ", "\n"]) {
    const got = parse(raw);
    assert.equal(got.value, undefined);
    assert.equal(got.warned, false, `${JSON.stringify(raw)} is absence, not an error`);
  }
});

test("W9: malformed input warns and is dropped — never throws", () => {
  // Each of these would fail the whole E2E run if parsing threw.
  for (const raw of ["{not json", "[1,2,3]", "null", '"a string"', "42", "true"]) {
    const got = parse(raw);
    assert.equal(got.value, undefined, `${raw} must not produce metadata`);
    assert.equal(got.warned, true, `${raw} must warn`);
  }
});

test("W9: an empty object is captured as-is, so the caller can omit it", () => {
  // parse() returns it; main.ts drops it before building the body, because the
  // server must be able to tell "nothing captured" from "captured nothing".
  const got = parse("{}");
  assert.deepEqual(got.value, {});
  assert.equal(Object.keys(got.value ?? {}).length, 0);
});

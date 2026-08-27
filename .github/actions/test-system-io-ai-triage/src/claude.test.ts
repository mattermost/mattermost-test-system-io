import { test } from "node:test";
import * as assert from "node:assert/strict";
import { parseVerdict } from "./claude.ts";

test("parseVerdict reads a bare JSON object", () => {
  const v = parseVerdict(
    `{"verdict":"FLAKY_TEST","confidence":0.91,"reason":"spinner","citations":["screenshot","error_message"]}`,
  );
  assert.equal(v.verdict, "FLAKY_TEST");
  assert.equal(v.confidence, 0.91);
  assert.deepEqual(v.citations, ["screenshot", "error_message"]);
});

test("parseVerdict extracts JSON from surrounding text", () => {
  const v = parseVerdict(
    `Sure.\n{"verdict":"PR_REGRESSION","confidence":0.8,"reason":"button missing","citations":["screenshot"]}\n`,
  );
  assert.equal(v.verdict, "PR_REGRESSION");
});

test("parseVerdict extracts JSON from a fenced code block", () => {
  const v = parseVerdict(
    'Here you go:\n```json\n{"verdict":"FLAKY_TEST","confidence":0.9,"reason":"timeout","citations":["screenshot","history"]}\n```\n',
  );
  assert.equal(v.verdict, "FLAKY_TEST");
  assert.equal(v.confidence, 0.9);
});

test("parseVerdict tolerates trailing commas", () => {
  const v = parseVerdict(
    `{"verdict":"FLAKY_SERVER","confidence":0.9,"reason":"server blip","citations":["screenshot","history"],}`,
  );
  assert.equal(v.verdict, "FLAKY_SERVER");
});

test("parseVerdict reads the chronic flag", () => {
  const v = parseVerdict(
    `{"verdict":"FLAKY_TEST","confidence":0.9,"reason":"chronic flake (5/20)","citations":["history","this_run_recovered"],"chronic":true}`,
  );
  assert.equal(v.chronic, true);
  const plain = parseVerdict(
    `{"verdict":"FLAKY_TEST","confidence":0.9,"reason":"one-off","citations":["screenshot"]}`,
  );
  assert.equal(plain.chronic, false);
});

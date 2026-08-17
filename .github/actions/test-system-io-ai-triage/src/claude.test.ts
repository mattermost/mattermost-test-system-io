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

test("parseVerdict clamps unknown verdicts to INCONCLUSIVE", () => {
  const v = parseVerdict(`{"verdict":"WHO_KNOWS","confidence":2,"reason":"x","citations":[]}`);
  assert.equal(v.verdict, "INCONCLUSIVE");
  assert.equal(v.confidence, 1);
});

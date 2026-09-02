/**
 * Regression for the mattermost#38154 crash.
 *
 * Pointing the triage job at a TSIO without the /api/v1/triage endpoints gave
 * `SyntaxError: Unexpected token '<'` and a crashed job, because those paths
 * fall through to the SPA and return HTTP 200 with HTML — so every res.ok
 * check passes and only JSON.parse notices.
 */
import { test } from "node:test";
import * as assert from "node:assert/strict";
import { parseJSON } from "./retry-fetch.ts";

const res = (body: string, init: ResponseInit = {}) => new Response(body, { status: 200, ...init });

test("parseJSON: an HTML page names the real cause, not a SyntaxError", async () => {
  const html =
    "<!doctype html><html><head><title>Test System IO</title></head><body></body></html>";
  await assert.rejects(
    () => parseJSON(res(html, { headers: { "content-type": "text/html" } }), "triage/evidence"),
    (err: Error) => {
      assert.match(err.message, /triage\/evidence did not return JSON/);
      assert.match(err.message, /does not have the \/api\/v1\/triage endpoints/);
      assert.match(err.message, /use-staging/, "must point at the input that fixes it");
      assert.doesNotMatch(
        err.message,
        /Unexpected token/,
        "the raw SyntaxError is not the message",
      );
      return true;
    },
  );
});

test("parseJSON: valid JSON is accepted whatever the content-type claims", async () => {
  // Proxies and hand-rolled test doubles both serve JSON as text/plain. A guard
  // that rejected those would break working callers to catch a broken one.
  for (const ct of ["application/json", "text/plain;charset=UTF-8", ""]) {
    const got = await parseJSON<{ ok: boolean }>(
      res('{"ok":true}', ct ? { headers: { "content-type": ct } } : {}),
      "triage/phase",
    );
    assert.deepEqual(got, { ok: true }, `content-type ${JSON.stringify(ct)} must be accepted`);
  }
});

test("parseJSON: non-JSON, non-HTML says so plainly", async () => {
  await assert.rejects(
    () => parseJSON(res("upstream connect error"), "triage/evidence"),
    (err: Error) => {
      assert.match(err.message, /neither JSON nor HTML/);
      return true;
    },
  );
});

test("parseJSON: the body excerpt is bounded and single-line", async () => {
  // It lands in a CI annotation; a megabyte of minified HTML there is useless.
  const huge = "<!doctype html>\n" + "<div>x</div>\n".repeat(5000);
  await assert.rejects(
    () => parseJSON(res(huge), "triage/evidence"),
    (err: Error) => {
      assert.ok(err.message.length < 600, `message was ${err.message.length} chars`);
      assert.doesNotMatch(err.message.replace(/^.*Body starts: /s, ""), /\n/);
      return true;
    },
  );
});

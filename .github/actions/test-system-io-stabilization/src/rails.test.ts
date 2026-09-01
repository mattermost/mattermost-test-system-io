import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { guardEditable, clampConcurrency, attemptsExhausted } from "./rails.ts";

function workspace(): string {
  const ws = fs.mkdtempSync(path.join(os.tmpdir(), "rails-"));
  fs.mkdirSync(path.join(ws, "e2e-tests"), { recursive: true });
  return ws;
}

test("guardEditable accepts e2e-tests paths only", () => {
  const ws = workspace();
  assert.equal(
    guardEditable(ws, "e2e-tests/playwright/specs/a.spec.ts"),
    path.join(ws, "e2e-tests/playwright/specs/a.spec.ts"),
  );
  fs.rmSync(ws, { recursive: true, force: true });
});

test("guardEditable rejects product code, config, and escapes", () => {
  assert.throws(() => guardEditable("/tmp/ws", "webapp/src/x.tsx"));
  assert.throws(() => guardEditable("/tmp/ws", "playwright.config.ts"));
  assert.throws(() => guardEditable("/tmp/ws", "../outside.ts"));
  assert.throws(() => guardEditable("/tmp/ws", "e2e-tests/../webapp/x.ts"));
});

test("clampConcurrency caps at the hard limit 5", () => {
  assert.equal(clampConcurrency(99), 5);
  assert.equal(clampConcurrency(2), 2);
  assert.equal(clampConcurrency(0), 1);
  assert.equal(clampConcurrency(-3), 1);
});

test("attemptsExhausted is inclusive", () => {
  assert.equal(attemptsExhausted(2, 3), false);
  assert.equal(attemptsExhausted(3, 3), true);
});

test("M13: a symlink under e2e-tests/ pointing outside is rejected", () => {
  const ws = workspace();
  fs.mkdirSync(path.join(ws, "secret"), { recursive: true });
  fs.writeFileSync(path.join(ws, "secret", "escape.ts"), "product code");
  // The attack: e2e-tests/link is a directory symlink to ../secret.
  fs.symlinkSync(path.join(ws, "secret"), path.join(ws, "e2e-tests", "link"), "dir");
  assert.throws(() => guardEditable(ws, "e2e-tests/link/escape.ts"), /outside e2e-tests|escapes workspace/);
  // A plain in-root path still resolves fine.
  assert.ok(guardEditable(ws, "e2e-tests/plain.spec.ts").length > 0);
  fs.rmSync(ws, { recursive: true, force: true });
});

test("R2-3: a DANGLING symlink under e2e-tests is rejected, not skipped", () => {
  const ws = workspace();
  // Dangling link pointing OUTSIDE the workspace entirely.
  fs.symlinkSync("/tmp/sym/outside/pwn.ts", path.join(ws, "e2e-tests", "dangling.ts"));
  assert.throws(() => guardEditable(ws, "e2e-tests/dangling.ts"), /symlink|escapes/i);
  // Dangling link pointing at a product file INSIDE the workspace.
  fs.symlinkSync(path.join(ws, "newprod.ts"), path.join(ws, "e2e-tests", "dangling2.ts"));
  assert.throws(() => guardEditable(ws, "e2e-tests/dangling2.ts"), /symlink|outside e2e-tests/i);
  // And a NON-dangling file link is still rejected.
  fs.writeFileSync(path.join(ws, "victim.ts"), "product");
  fs.symlinkSync(path.join(ws, "victim.ts"), path.join(ws, "e2e-tests", "link.ts"));
  assert.throws(() => guardEditable(ws, "e2e-tests/link.ts"), /symlink/i);
  fs.rmSync(ws, { recursive: true, force: true });
});

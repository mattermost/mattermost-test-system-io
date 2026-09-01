import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { execFileSync } from "node:child_process";
import { checkOwnDiff } from "./self_check.ts";

function gitWorkspace(): string {
  const ws = fs.mkdtempSync(path.join(os.tmpdir(), "selfcheck-"));
  execFileSync("git", ["-C", ws, "init", "-q"]);
  execFileSync("git", ["-C", ws, "config", "user.email", "t@t"]);
  execFileSync("git", ["-C", ws, "config", "user.name", "t"]);
  fs.mkdirSync(path.join(ws, "e2e-tests"), { recursive: true });
  fs.writeFileSync(path.join(ws, "e2e-tests/a.spec.ts"), "await page.goto('/');\n");
  execFileSync("git", ["-C", ws, "add", "-A"]);
  execFileSync("git", ["-C", ws, "commit", "-qm", "init"]);
  return ws;
}

test("clean diff passes self-check", () => {
  const ws = gitWorkspace();
  assert.deepEqual(checkOwnDiff(ws), { passed: true, violations: [] });
  fs.rmSync(ws, { recursive: true, force: true });
});

test("a banned edit is rejected by the loop's own enforcement", () => {
  const ws = gitWorkspace();
  fs.writeFileSync(
    path.join(ws, "e2e-tests/a.spec.ts"),
    "await page.waitForTimeout(3000);\n",
  );
  const result = checkOwnDiff(ws);
  assert.equal(result.passed, false);
  assert.ok(result.violations.some((v) => v.rule === "ban-bare-wait"));
  fs.rmSync(ws, { recursive: true, force: true });
});

test("empty workspace diff passes (nothing attempted)", () => {
  const ws = fs.mkdtempSync(path.join(os.tmpdir(), "selfcheck-empty-"));
  execFileSync("git", ["-C", ws, "init", "-q"]);
  assert.deepEqual(checkOwnDiff(ws), { passed: true, violations: [] });
  fs.rmSync(ws, { recursive: true, force: true });
});

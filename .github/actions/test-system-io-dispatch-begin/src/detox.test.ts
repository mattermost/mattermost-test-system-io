import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { discoverDetoxSpecs } from "./detox.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "detox-discovery-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function writeSpec(root: string, relPath: string): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, "describe('x', () => { it('y', async () => {}); });");
}

test("discoverDetoxSpecs: walks nested dirs, sorted, forward-slash paths", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/channels/browse_channels.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/account/settings.e2e.ts");
    const specs = discoverDetoxSpecs(dir, { searchPath: "e2e/test", excludeDir: "ipad" });
    assert.deepEqual(specs, [
      "e2e/test/products/channels/account/settings.e2e.ts",
      "e2e/test/products/channels/channels/browse_channels.e2e.ts",
      "e2e/test/products/channels/messaging/message_post.e2e.ts",
    ]);
  });
});

test("discoverDetoxSpecs: excludes named directory by default (ipad)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/ipad/ipad_only.e2e.ts");
    const specs = discoverDetoxSpecs(dir, { searchPath: "e2e/test", excludeDir: "ipad" });
    assert.deepEqual(specs, ["e2e/test/products/channels/messaging/message_post.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: empty excludeDir disables exclusion (iPad-only run)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/ipad/ipad_only.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test/products/channels/ipad",
      excludeDir: "",
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/ipad/ipad_only.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: ignores non-.e2e.ts files (support/helper modules)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    fs.mkdirSync(path.join(dir, "e2e/support"), { recursive: true });
    fs.writeFileSync(path.join(dir, "e2e/support/server_api.ts"), "export const Setup = {};");
    const specs = discoverDetoxSpecs(dir, { searchPath: "e2e/test", excludeDir: "ipad" });
    assert.deepEqual(specs, ["e2e/test/products/channels/messaging/message_post.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: searchPath pointing at a single file returns just that file", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/smoke_test/server_login.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/smoke_test/account.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test/products/channels/smoke_test/server_login.e2e.ts",
      excludeDir: "ipad",
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/smoke_test/server_login.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: searchPath file not matching *.e2e.ts throws", () => {
  withTmpDir((dir) => {
    fs.mkdirSync(path.join(dir, "e2e/test"), { recursive: true });
    fs.writeFileSync(path.join(dir, "e2e/test/not-a-spec.ts"), "export {};");
    assert.throws(
      () => discoverDetoxSpecs(dir, { searchPath: "e2e/test/not-a-spec.ts", excludeDir: "ipad" }),
      /doesn't match \*\.e2e\.ts/,
    );
  });
});

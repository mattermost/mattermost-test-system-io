import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  discoverDetoxSpecs,
  parseDetoxSpecTags,
  passesDetoxTagFilters,
} from "./detox.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "detox-discovery-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function writeSpec(root: string, relPath: string, body?: string): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(
    abs,
    body ?? "describe('x', () => { it('y', async () => {}); });",
  );
}

test("discoverDetoxSpecs: walks nested dirs, sorted, forward-slash paths", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/channels/browse_channels.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/account/settings.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      excludeDir: "ipad",
      includeTags: [],
      excludeTags: [],
    });
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
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      excludeDir: "ipad",
      includeTags: [],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/messaging/message_post.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: empty excludeDir disables exclusion (iPad-only run)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/ipad/ipad_only.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test/products/channels/ipad",
      excludeDir: "",
      includeTags: [],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/ipad/ipad_only.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: ignores non-.e2e.ts files (support/helper modules)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    fs.mkdirSync(path.join(dir, "e2e/test/support"), { recursive: true });
    fs.writeFileSync(path.join(dir, "e2e/test/support/server_api.ts"), "export const Setup = {};");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      excludeDir: "ipad",
      includeTags: [],
      excludeTags: [],
    });
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
      includeTags: [],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/smoke_test/server_login.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: searchPath file not matching *.e2e.ts throws", () => {
  withTmpDir((dir) => {
    fs.mkdirSync(path.join(dir, "e2e/test"), { recursive: true });
    fs.writeFileSync(path.join(dir, "e2e/test/readme.md"), "x");
    assert.throws(
      () =>
        discoverDetoxSpecs(dir, {
          searchPath: "e2e/test/readme.md",
          excludeDir: "ipad",
          includeTags: [],
          excludeTags: [],
        }),
      /doesn't match/,
    );
  });
});

test("parseDetoxSpecTags: reads // Tags: @tokens from preamble only", () => {
  const tags = parseDetoxSpecTags(
    [
      "// Copyright",
      "// Tags: @ios_complementary @smoke",
      "",
      "import {describe} from 'detox';",
      "// Tags: @ignored_after_import",
      "describe('x', () => {});",
    ].join("\n"),
  );
  assert.deepEqual(tags, ["@ios_complementary", "@smoke"]);
});

test("passesDetoxTagFilters: include/exclude semantics", () => {
  assert.equal(
    passesDetoxTagFilters(["@ios_complementary"], { includeTags: ["@ios_complementary"], excludeTags: [] }),
    true,
  );
  assert.equal(
    passesDetoxTagFilters(["@smoke"], { includeTags: ["@ios_complementary"], excludeTags: [] }),
    false,
  );
  assert.equal(
    passesDetoxTagFilters(["@ios_complementary", "@flaky"], {
      includeTags: ["@ios_complementary"],
      excludeTags: ["@flaky"],
    }),
    false,
  );
  assert.equal(
    passesDetoxTagFilters([], { includeTags: [], excludeTags: [] }),
    true,
  );
});

test("discoverDetoxSpecs: includeTags keeps only matching specs with real paths", () => {
  withTmpDir((dir) => {
    writeSpec(
      dir,
      "e2e/test/products/channels/smoke_test/channels.e2e.ts",
      "// Tags: @ios_complementary\ndescribe('x', () => {});",
    );
    writeSpec(
      dir,
      "e2e/test/products/channels/messaging/message_post.e2e.ts",
      "// Tags: @other\ndescribe('x', () => {});",
    );
    writeSpec(dir, "e2e/test/products/channels/account/settings.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      excludeDir: "ipad",
      includeTags: ["@ios_complementary"],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/smoke_test/channels.e2e.ts"]);
  });
});

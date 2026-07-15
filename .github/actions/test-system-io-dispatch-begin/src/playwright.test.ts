import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { discoverPlaywrightSpecs, readPlaywrightSpecConfig } from "./playwright.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pw-discover-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function writeFile(root: string, relPath: string, body: string): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, body);
}

// ── readPlaywrightSpecConfig ────────────────────────────────────────────

test("readPlaywrightSpecConfig: reads testDir, falls back to default testMatch", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `
import {defineConfig} from '@playwright/test';
export default defineConfig({
  testDir: 'specs',
  projects: [
    {name: 'setup', testMatch: /test_setup\\.ts/},
    {name: 'chrome', use: {}, dependencies: ['setup']},
  ],
});
`,
    );
    const cfg = readPlaywrightSpecConfig(dir);
    assert.equal(cfg.testDir, path.resolve(dir, "specs"));
    assert.deepEqual(cfg.testMatch, ["**/*.{spec,test}.{ts,tsx,js,jsx,mjs,cjs,mts,cts}"]);
  });
});

test("readPlaywrightSpecConfig: explicit testMatch (single string) wins over default", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `
export default {
  testDir: 'tests',
  testMatch: '**/*.spec.ts',
};
`,
    );
    const cfg = readPlaywrightSpecConfig(dir);
    assert.equal(cfg.testDir, path.resolve(dir, "tests"));
    assert.deepEqual(cfg.testMatch, ["**/*.spec.ts"]);
  });
});

test("readPlaywrightSpecConfig: array testMatch", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `
export default {
  testDir: 'specs',
  testMatch: ['**/*.spec.ts', '**/*.e2e.ts'],
};
`,
    );
    const cfg = readPlaywrightSpecConfig(dir);
    assert.deepEqual(cfg.testMatch, ["**/*.spec.ts", "**/*.e2e.ts"]);
  });
});

test("readPlaywrightSpecConfig: no config file falls back to defaults", () => {
  withTmpDir((dir) => {
    const cfg = readPlaywrightSpecConfig(dir);
    assert.equal(cfg.testDir, path.resolve(dir, "."));
    assert.deepEqual(cfg.testMatch, ["**/*.{spec,test}.{ts,tsx,js,jsx,mjs,cjs,mts,cts}"]);
  });
});

test("readPlaywrightSpecConfig: per-project testMatch ignored at top level", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `
export default {
  testDir: 'specs',
  projects: [
    {name: 'a', testMatch: '**/*.alpha.spec.ts'},
    {name: 'b', testMatch: '**/*.beta.spec.ts'},
  ],
};
`,
    );
    const cfg = readPlaywrightSpecConfig(dir);
    // Top-level testMatch is unset, so we use the default — NOT the
    // per-project values, which only gate `--project=` invocations.
    assert.deepEqual(cfg.testMatch, ["**/*.{spec,test}.{ts,tsx,js,jsx,mjs,cjs,mts,cts}"]);
  });
});

// ── discoverPlaywrightSpecs ─────────────────────────────────────────────

test("discoverPlaywrightSpecs: globs config-declared testMatch", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `export default { testDir: 'specs', testMatch: '**/*.spec.ts' };`,
    );
    writeFile(dir, "specs/login.spec.ts", "// pw test");
    writeFile(dir, "specs/sub/notifications.spec.ts", "// pw test");
    writeFile(dir, "specs/utils/helper.ts", "// not a spec");
    writeFile(dir, "other/random.spec.ts", "// outside testDir");

    const out = discoverPlaywrightSpecs(dir);
    assert.deepEqual(out, ["specs/login.spec.ts", "specs/sub/notifications.spec.ts"]);
  });
});

test("discoverPlaywrightSpecs: applies excludePaths post-filter", () => {
  withTmpDir((dir) => {
    fs.writeFileSync(
      path.join(dir, "playwright.config.ts"),
      `export default { testDir: 'specs', testMatch: '**/*.spec.ts' };`,
    );
    writeFile(dir, "specs/test_setup.ts.spec.ts", "// would also match");
    writeFile(dir, "specs/login.spec.ts", "");
    writeFile(dir, "specs/test_setup.ts", "");
    writeFile(dir, "specs/visual/regression.spec.ts", "");

    const out = discoverPlaywrightSpecs(dir, ["test_setup.ts", "specs/visual/"]);
    // login passes; test_setup.ts is excluded; specs/visual/* prefix-excluded.
    assert.deepEqual(out, ["specs/login.spec.ts", "specs/test_setup.ts.spec.ts"]);
  });
});

test("discoverPlaywrightSpecs: default testMatch picks both .spec and .test files", () => {
  withTmpDir((dir) => {
    // No config file → defaults to testDir='.', testMatch the Playwright glob.
    writeFile(dir, "alpha.spec.ts", "");
    writeFile(dir, "beta.test.ts", "");
    writeFile(dir, "ignored.txt", "");

    const out = discoverPlaywrightSpecs(dir);
    assert.deepEqual([...out].sort(), ["alpha.spec.ts", "beta.test.ts"]);
  });
});

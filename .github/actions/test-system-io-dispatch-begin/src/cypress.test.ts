import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  discoverCypressSpecs,
  parseCypressMetadata,
  parseTagList,
  partitionBySort,
  passesFilters,
  readCypressSpecConfig,
  type CypressFilters,
  type SpecMetadata,
} from "./cypress.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "cypress-filters-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function writeSpec(root: string, relPath: string, body: string): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, body);
}

/** Drops a minimal cypress.config.ts that points specPattern at the
 *  mattermost convention so per-test fixture writes find a home. */
function writeMattermostCypressConfig(cypressDir: string): void {
  fs.writeFileSync(
    path.join(cypressDir, "cypress.config.ts"),
    `export default { e2e: { specPattern: 'tests/integration/**/*_spec.{js,ts}' } };`,
  );
}

const noFilters: CypressFilters = {
  stage: [],
  includeGroup: [],
  excludeGroup: [],
  sortFirst: [],
  sortLast: [],
};

// ── parseCypressMetadata ────────────────────────────────────────────────

test("parseCypressMetadata: stage and group on adjacent lines", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// Stage: @prod\n// Group: @channels @bot_accounts\n\ndescribe(...)");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: ["@prod"], groups: ["@channels", "@bot_accounts"] });
  });
});

test("parseCypressMetadata: blank lines between header comments", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// Stage: @prod\n\n// Group: @channels\n\ndescribe(...)");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: ["@prod"], groups: ["@channels"] });
  });
});

test("parseCypressMetadata: multi-tag stage and group lines", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// Stage: @prod @smoke\n// Group: @channels @flaky @e2e\n");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, {
      stages: ["@prod", "@smoke"],
      groups: ["@channels", "@flaky", "@e2e"],
    });
  });
});

test("parseCypressMetadata: keyword case-insensitive", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// stage: @prod\n// GROUP: @channels\n");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: ["@prod"], groups: ["@channels"] });
  });
});

test("parseCypressMetadata: stops at first non-comment line", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(
      f,
      "// Stage: @prod\nimport foo from 'bar';\n// Group: @ignored\ndescribe(...)",
    );
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: ["@prod"], groups: [] });
  });
});

test("parseCypressMetadata: missing tags returns empty arrays", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// just a comment\ndescribe(...)");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: [], groups: [] });
  });
});

test("parseCypressMetadata: malformed group line keeps only @-tokens", () => {
  withTmpDir((dir) => {
    const f = path.join(dir, "spec.ts");
    fs.writeFileSync(f, "// Group: @channels stray-token @bot_accounts\n");
    const meta = parseCypressMetadata(f);
    assert.deepEqual(meta, { stages: [], groups: ["@channels", "@bot_accounts"] });
  });
});

// ── passesFilters ───────────────────────────────────────────────────────

test("passesFilters: empty filters admit everything", () => {
  const meta: SpecMetadata = { stages: [], groups: [] };
  assert.equal(passesFilters(meta, noFilters), true);
});

test("passesFilters: stage filter requires overlap", () => {
  const meta: SpecMetadata = { stages: ["@smoke"], groups: [] };
  assert.equal(passesFilters(meta, { ...noFilters, stage: ["@prod"] }), false);
  assert.equal(passesFilters(meta, { ...noFilters, stage: ["@prod", "@smoke"] }), true);
});

test("passesFilters: spec without stage drops when stage filter active", () => {
  const meta: SpecMetadata = { stages: [], groups: ["@channels"] };
  assert.equal(passesFilters(meta, { ...noFilters, stage: ["@prod"] }), false);
});

test("passesFilters: includeGroup requires overlap", () => {
  const meta: SpecMetadata = { stages: ["@prod"], groups: ["@channels"] };
  assert.equal(passesFilters(meta, { ...noFilters, includeGroup: ["@bot_accounts"] }), false);
  assert.equal(passesFilters(meta, { ...noFilters, includeGroup: ["@channels", "@e2e"] }), true);
});

test("passesFilters: excludeGroup drops on any overlap", () => {
  const meta: SpecMetadata = { stages: ["@prod"], groups: ["@channels", "@flaky"] };
  assert.equal(passesFilters(meta, { ...noFilters, excludeGroup: ["@flaky"] }), false);
  assert.equal(passesFilters(meta, { ...noFilters, excludeGroup: ["@deprecated"] }), true);
});

test("passesFilters: include + exclude evaluated in pipeline order", () => {
  const meta: SpecMetadata = { stages: ["@prod"], groups: ["@channels", "@flaky"] };
  // Included by @channels, excluded by @flaky → drop.
  assert.equal(
    passesFilters(meta, { ...noFilters, includeGroup: ["@channels"], excludeGroup: ["@flaky"] }),
    false,
  );
});

// ── partitionBySort ─────────────────────────────────────────────────────

test("partitionBySort: sortFirst entries lead, sortLast entries trail", () => {
  const specs = [
    { path: "a.ts", meta: { stages: [], groups: ["@channels"] } },
    { path: "b.ts", meta: { stages: [], groups: ["@known_issue"] } },
    { path: "c.ts", meta: { stages: [], groups: ["@flaky"] } },
    { path: "d.ts", meta: { stages: [], groups: ["@channels"] } },
  ];
  const ordered = partitionBySort(specs, {
    ...noFilters,
    sortFirst: ["@flaky"],
    sortLast: ["@known_issue"],
  });
  assert.deepEqual(
    ordered.map((s) => s.path),
    ["c.ts", "a.ts", "d.ts", "b.ts"],
  );
});

test("partitionBySort: sortFirst wins when a spec matches both lists", () => {
  const specs = [{ path: "a.ts", meta: { stages: [], groups: ["@flaky", "@known_issue"] } }];
  const ordered = partitionBySort(specs, {
    ...noFilters,
    sortFirst: ["@flaky"],
    sortLast: ["@known_issue"],
  });
  assert.equal(ordered.length, 1);
  // Spec is in the sort-first partition (which renders ahead of last).
  assert.equal(ordered[0]?.path, "a.ts");
});

// ── parseTagList ────────────────────────────────────────────────────────

test("parseTagList: splits commas, trims, drops empties", () => {
  assert.deepEqual(parseTagList(""), []);
  assert.deepEqual(parseTagList("  "), []);
  assert.deepEqual(parseTagList("@prod"), ["@prod"]);
  assert.deepEqual(parseTagList("@prod, @smoke , ,@plus,"), ["@prod", "@smoke", "@plus"]);
});

// ── discoverCypressSpecs (end-to-end with a tmpdir layout) ──────────────

test("discoverCypressSpecs: applies filter pipeline + dedup", () => {
  withTmpDir((cypressDir) => {
    writeMattermostCypressConfig(cypressDir);
    const integ = path.join(cypressDir, "tests", "integration");
    writeSpec(integ, "channels/a_spec.ts", "// Stage: @prod\n// Group: @channels\n");
    writeSpec(integ, "channels/b_spec.ts", "// Stage: @prod\n// Group: @channels @flaky\n");
    writeSpec(integ, "bots/c_spec.ts", "// Stage: @smoke\n// Group: @bot_accounts\n");
    writeSpec(integ, "broken/d_spec.ts", "// Stage: @prod\n// Group: @known_issue\n");
    writeSpec(integ, "tools/e_spec.js", "// Stage: @prod\n// Group: @flaky\n");

    const out = discoverCypressSpecs(cypressDir, {
      stage: ["@prod"],
      includeGroup: [],
      excludeGroup: [],
      sortFirst: ["@flaky"],
      sortLast: ["@known_issue"],
    });

    // c_spec.ts dropped (stage @smoke ∉ @prod). Order within each
    // partition is glob-order-dependent and intentionally not sorted —
    // assert membership of each partition slice.
    assert.equal(out.length, 4);
    assert.deepEqual(out.slice(0, 2).sort(), [
      "tests/integration/channels/b_spec.ts",
      "tests/integration/tools/e_spec.js",
    ]);
    assert.equal(out[2], "tests/integration/channels/a_spec.ts"); // neither
    assert.equal(out[3], "tests/integration/broken/d_spec.ts"); // @known_issue → last
  });
});

test("discoverCypressSpecs: includeGroup narrows the set", () => {
  withTmpDir((cypressDir) => {
    writeMattermostCypressConfig(cypressDir);
    const integ = path.join(cypressDir, "tests", "integration");
    writeSpec(integ, "a_spec.ts", "// Stage: @prod\n// Group: @channels\n");
    writeSpec(integ, "b_spec.ts", "// Stage: @prod\n// Group: @bot_accounts\n");
    writeSpec(integ, "c_spec.ts", "// Stage: @prod\n// Group: @system_console\n");

    const out = discoverCypressSpecs(cypressDir, {
      stage: ["@prod"],
      includeGroup: ["@bot_accounts", "@system_console"],
      excludeGroup: [],
      sortFirst: [],
      sortLast: [],
    });
    assert.deepEqual(out, ["tests/integration/b_spec.ts", "tests/integration/c_spec.ts"]);
  });
});

test("discoverCypressSpecs: empty filters return everything matching specPattern", () => {
  withTmpDir((cypressDir) => {
    writeMattermostCypressConfig(cypressDir);
    const integ = path.join(cypressDir, "tests", "integration");
    writeSpec(integ, "a_spec.ts", "");
    writeSpec(integ, "nested/b_spec.js", "");
    writeSpec(integ, "ignored.ts", ""); // not a *_spec.ts — skipped

    const out = discoverCypressSpecs(cypressDir, noFilters);
    // Order is filesystem-dependent for the no-filter case, so compare as a set.
    assert.deepEqual([...out].sort(), [
      "tests/integration/a_spec.ts",
      "tests/integration/nested/b_spec.js",
    ]);
  });
});

test("discoverCypressSpecs: missing integration dir returns []", () => {
  withTmpDir((cypressDir) => {
    const out = discoverCypressSpecs(cypressDir, noFilters);
    assert.deepEqual(out, []);
  });
});

// ── readCypressSpecConfig ───────────────────────────────────────────────

test("readCypressSpecConfig: reads specPattern + excludeSpecPattern from e2e block", () => {
  withTmpDir((cypressDir) => {
    fs.writeFileSync(
      path.join(cypressDir, "cypress.config.ts"),
      `
import {defineConfig} from 'cypress';
export default defineConfig({
  e2e: {
    specPattern: 'tests/integration/**/*_spec.{js,ts}',
    excludeSpecPattern: '**/node_modules/**/*',
    setupNodeEvents() {},
  },
});
`,
    );
    const cfg = readCypressSpecConfig(cypressDir);
    assert.deepEqual(cfg.include, ["tests/integration/**/*_spec.{js,ts}"]);
    assert.deepEqual(cfg.exclude, ["**/node_modules/**/*"]);
  });
});

test("readCypressSpecConfig: array form for specPattern", () => {
  withTmpDir((cypressDir) => {
    fs.writeFileSync(
      path.join(cypressDir, "cypress.config.ts"),
      `
export default {
  e2e: {
    specPattern: ['tests/a/**/*_spec.ts', 'tests/b/**/*_spec.ts'],
  },
};
`,
    );
    const cfg = readCypressSpecConfig(cypressDir);
    assert.deepEqual(cfg.include, ["tests/a/**/*_spec.ts", "tests/b/**/*_spec.ts"]);
    assert.deepEqual(cfg.exclude, []);
  });
});

test("readCypressSpecConfig: missing config falls back to default specPattern", () => {
  withTmpDir((cypressDir) => {
    const cfg = readCypressSpecConfig(cypressDir);
    assert.deepEqual(cfg.include, ["**/*.cy.{js,jsx,ts,tsx}"]);
    assert.deepEqual(cfg.exclude, []);
  });
});

test("readCypressSpecConfig: ignores specPattern outside the e2e block", () => {
  withTmpDir((cypressDir) => {
    // A specPattern in `component:` shouldn't be picked up for e2e.
    fs.writeFileSync(
      path.join(cypressDir, "cypress.config.ts"),
      `
export default {
  component: {
    specPattern: 'src/**/*.cy.tsx',
  },
};
`,
    );
    const cfg = readCypressSpecConfig(cypressDir);
    assert.deepEqual(cfg.include, ["**/*.cy.{js,jsx,ts,tsx}"]); // default
  });
});

test("discoverCypressSpecs: respects excludeSpecPattern from config", () => {
  withTmpDir((cypressDir) => {
    fs.writeFileSync(
      path.join(cypressDir, "cypress.config.ts"),
      `
export default {
  e2e: {
    specPattern: 'tests/integration/**/*_spec.{js,ts}',
    excludeSpecPattern: '**/node_modules/**/*',
  },
};
`,
    );
    const integ = path.join(cypressDir, "tests", "integration");
    writeSpec(integ, "a_spec.ts", "// Stage: @prod\n// Group: @x\n");
    // Should be excluded by excludeSpecPattern even though it matches specPattern.
    writeSpec(
      path.join(integ, "node_modules", "vendor"),
      "b_spec.ts",
      "// Stage: @prod\n// Group: @x\n",
    );

    const out = discoverCypressSpecs(cypressDir, { ...noFilters, stage: ["@prod"] });
    assert.deepEqual(out, ["tests/integration/a_spec.ts"]);
  });
});

import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  discoverDetoxSpecs,
  filterDetoxSpecsByTags,
  listDetoxSpecs,
  listedSpecPaths,
  parseDetoxSpecList,
  parseDetoxSpecTags,
  passesDetoxTagFilters,
  resolveDetoxSpecList,
  sortDetoxSpecsLast,
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
  fs.writeFileSync(abs, body ?? "describe('x', () => { it('y', async () => {}); });");
}

test("discoverDetoxSpecs: walks nested dirs, sorted, forward-slash paths", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/channels/browse_channels.e2e.ts");
    writeSpec(dir, "e2e/test/products/channels/account/settings.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
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

test("discoverDetoxSpecs: excludeTags @ipad_only drops iPad specs anywhere", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(
      dir,
      "e2e/test/products/channels/tablet/ipad_sidebar.e2e.ts",
      "// Tags: @ipad_only\ndescribe('x', () => {});",
    );
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      includeTags: [],
      excludeTags: ["@ipad_only"],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/messaging/message_post.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: includeTags @ipad_only keeps iPad specs anywhere", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    writeSpec(
      dir,
      "e2e/test/products/channels/tablet/ipad_sidebar.e2e.ts",
      "// Tags: @ipad_only\ndescribe('x', () => {});",
    );
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      includeTags: ["@ipad_only"],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/tablet/ipad_sidebar.e2e.ts"]);
  });
});

test("discoverDetoxSpecs: ignores non-.e2e.ts files (support/helper modules)", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/products/channels/messaging/message_post.e2e.ts");
    fs.mkdirSync(path.join(dir, "e2e/test/support"), { recursive: true });
    fs.writeFileSync(path.join(dir, "e2e/test/support/server_api.ts"), "export const Setup = {};");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
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
      "// Tags: @ios_pr @smoke",
      "",
      "import {describe} from 'detox';",
      "// Tags: @ignored_after_import",
      "describe('x', () => {});",
    ].join("\n"),
  );
  assert.deepEqual(tags, ["@ios_pr", "@smoke"]);
});

test("parseDetoxSpecTags: ignores Tags after a type declaration (not just import/describe)", () => {
  const tags = parseDetoxSpecTags(
    [
      "// Copyright",
      "",
      "type ChannelFixture = { id: string };",
      "// Tags: @ios_pr",
      "describe('x', () => {});",
    ].join("\n"),
  );
  assert.deepEqual(tags, []);
});

test("passesDetoxTagFilters: include/exclude semantics", () => {
  assert.equal(
    passesDetoxTagFilters(["@ios_pr"], { includeTags: ["@ios_pr"], excludeTags: [] }),
    true,
  );
  assert.equal(
    passesDetoxTagFilters(["@smoke"], { includeTags: ["@ios_pr"], excludeTags: [] }),
    false,
  );
  assert.equal(
    passesDetoxTagFilters(["@ios_pr", "@flaky"], {
      includeTags: ["@ios_pr"],
      excludeTags: ["@flaky"],
    }),
    false,
  );
  assert.equal(passesDetoxTagFilters([], { includeTags: [], excludeTags: [] }), true);
});

test("discoverDetoxSpecs: includeTags keeps only matching specs with real paths", () => {
  withTmpDir((dir) => {
    writeSpec(
      dir,
      "e2e/test/products/channels/smoke_test/channels.e2e.ts",
      "// Tags: @ios_pr\ndescribe('x', () => {});",
    );
    writeSpec(
      dir,
      "e2e/test/products/channels/messaging/message_post.e2e.ts",
      "// Tags: @other\ndescribe('x', () => {});",
    );
    writeSpec(dir, "e2e/test/products/channels/account/settings.e2e.ts");
    const specs = discoverDetoxSpecs(dir, {
      searchPath: "e2e/test",
      includeTags: ["@ios_pr"],
      excludeTags: [],
    });
    assert.deepEqual(specs, ["e2e/test/products/channels/smoke_test/channels.e2e.ts"]);
  });
});

test("resolveDetoxSpecList: relative and in-dir absolute entries become sorted, unique detox-relative paths", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/b.e2e.ts");
    writeSpec(dir, "e2e/test/nested/a.ios.e2e.ts");
    const specs = resolveDetoxSpecList(dir, [
      "e2e/test/b.e2e.ts",
      path.join(dir, "e2e/test/nested/a.ios.e2e.ts"),
      "./e2e/test/b.e2e.ts",
      "",
    ]);
    assert.deepEqual(specs, ["e2e/test/b.e2e.ts", "e2e/test/nested/a.ios.e2e.ts"]);
  });
});

test("resolveDetoxSpecList: a symlinked detoxDir still yields paths relative to it", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "real/e2e/test/a.e2e.ts");
    fs.symlinkSync(path.join(dir, "real"), path.join(dir, "link"));
    const listed = path.join(fs.realpathSync(dir), "real/e2e/test/a.e2e.ts");
    assert.deepEqual(resolveDetoxSpecList(path.join(dir, "link"), [listed]), ["e2e/test/a.e2e.ts"]);
  });
});

test("resolveDetoxSpecList: missing, outside, and non-spec entries throw", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "detox/e2e/test/a.e2e.ts");
    writeSpec(dir, "other/b.e2e.ts");
    fs.writeFileSync(path.join(dir, "detox/e2e/test/helper.ts"), "");
    const detoxDir = path.join(dir, "detox");
    assert.throws(
      () => resolveDetoxSpecList(detoxDir, ["e2e/test/missing.e2e.ts"]),
      /does not exist/,
    );
    assert.throws(() => resolveDetoxSpecList(detoxDir, ["../other/b.e2e.ts"]), /outside/);
    assert.throws(
      () => resolveDetoxSpecList(detoxDir, [path.join(dir, "other/b.e2e.ts")]),
      /outside/,
    );
    assert.throws(
      () => resolveDetoxSpecList(detoxDir, ["e2e/test/helper.ts"]),
      /not a \*\.e2e\.ts/,
    );
    assert.throws(() => resolveDetoxSpecList(detoxDir, ["e2e/test"]), /not a \*\.e2e\.ts/);
    writeSpec(detoxDir, "e2e/test/a $(id).e2e.ts");
    assert.throws(
      () => resolveDetoxSpecList(detoxDir, ["e2e/test/a $(id).e2e.ts"]),
      /characters outside/,
    );
  });
});

test("resolveDetoxSpecList: every bad entry is reported, not just the first", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "detox/e2e/test/a.e2e.ts");
    writeSpec(dir, "x.e2e.ts");
    const detoxDir = path.join(dir, "detox");
    assert.throws(
      () =>
        resolveDetoxSpecList(detoxDir, [
          "e2e/test/missing.e2e.ts",
          "e2e/test/a.e2e.ts",
          "../x.e2e.ts",
        ]),
      (err: Error) => {
        assert.equal(
          err.message,
          `2 invalid detox spec(s) under ${detoxDir}:\n` +
            `  "e2e/test/missing.e2e.ts" does not exist\n` +
            `  "../x.e2e.ts" is outside detox-dir`,
        );
        return true;
      },
    );
  });
});

test("resolveDetoxSpecList: lists the first 10 bad entries, then how many more", () => {
  withTmpDir((dir) => {
    const missing = Array.from({ length: 13 }, (_, i) => `e2e/test/missing_${i}.e2e.ts`);
    assert.throws(
      () => resolveDetoxSpecList(dir, missing),
      (err: Error) => {
        const lines = err.message.split("\n");
        assert.equal(lines[0], `13 invalid detox spec(s) under ${dir}:`);
        assert.equal(lines.length, 12);
        assert.equal(lines[10], `  "e2e/test/missing_9.e2e.ts" does not exist`);
        assert.equal(lines[11], "  ...and 3 more");
        return true;
      },
    );
  });
});

test("parseDetoxSpecList: splits on any whitespace", () => {
  assert.deepEqual(parseDetoxSpecList("  a.e2e.ts\nb.e2e.ts  c.e2e.ts\n"), [
    "a.e2e.ts",
    "b.e2e.ts",
    "c.e2e.ts",
  ]);
  assert.deepEqual(parseDetoxSpecList(""), []);
});

test("listedSpecPaths: keeps absolute *.e2e.ts lines only", () => {
  const out = [
    "/w/detox/e2e/test/a.e2e.ts",
    "",
    "  /w/detox/e2e/test/b.ipad.e2e.ts  ",
    "e2e/rel.e2e.ts",
    "/w/x.ts",
    "log line",
  ];
  assert.deepEqual(listedSpecPaths(out.join("\n")), [
    "/w/detox/e2e/test/a.e2e.ts",
    "/w/detox/e2e/test/b.ipad.e2e.ts",
  ]);
});

function withFakeNpx(dir: string, script: string, fn: () => void): void {
  const bin = path.join(dir, "bin");
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, "npx"), `#!/bin/sh\n${script}\n`, { mode: 0o755 });
  const prev = process.env.PATH;
  process.env.PATH = `${bin}${path.delimiter}${prev ?? ""}`;
  try {
    fn();
  } finally {
    process.env.PATH = prev;
  }
}

test("listDetoxSpecs: runs detox --listTests in detoxDir with the config and returns detox-relative paths", () => {
  withTmpDir((dir) => {
    const detoxDir = path.join(dir, "detox");
    writeSpec(detoxDir, "e2e/test/a.e2e.ts");
    writeSpec(detoxDir, "e2e/test/b.ipad.e2e.ts");
    const script = [
      'echo "$*" > "$PWD/args.txt"',
      'echo "detox log" >&2',
      'echo "$PWD/e2e/test/b.ipad.e2e.ts"',
      'echo "$PWD/e2e/test/a.e2e.ts"',
    ].join("\n");
    withFakeNpx(dir, script, () => {
      assert.deepEqual(listDetoxSpecs(detoxDir, "ios.ipad.debug"), [
        "e2e/test/a.e2e.ts",
        "e2e/test/b.ipad.e2e.ts",
      ]);
    });
    assert.equal(
      fs.readFileSync(path.join(detoxDir, "args.txt"), "utf8").trim(),
      "detox test -c ios.ipad.debug --listTests",
    );
  });
});

test("listDetoxSpecs: a failing detox exit throws with stderr", () => {
  withTmpDir((dir) => {
    fs.mkdirSync(path.join(dir, "detox"));
    withFakeNpx(dir, 'echo "no such configuration" >&2; exit 2', () => {
      assert.throws(
        () => listDetoxSpecs(path.join(dir, "detox"), "nope"),
        /exited 2[\s\S]*no such configuration/,
      );
    });
  });
});

test("listDetoxSpecs: refuses a config that is not a plain name", () => {
  for (const bad of ["", "-c", "--listTests", "ios sim", "a;b", "../x"]) {
    assert.throws(() => listDetoxSpecs(".", bad), /not a valid configuration name/);
  }
});

test("filterDetoxSpecsByTags and sortDetoxSpecsLast: tags read from each listed file", () => {
  withTmpDir((dir) => {
    writeSpec(dir, "e2e/test/a.e2e.ts");
    writeSpec(
      dir,
      "e2e/test/m1.e2e.ts",
      "// Copyright\n// Tags: @server_mutation\nimport x from 'y';",
    );
    writeSpec(dir, "e2e/test/b.e2e.ts", "// Tags: @flaky\n");
    writeSpec(dir, "e2e/test/m2.e2e.ts", "// Tags: @smoke @server_mutation\n");
    const a = "e2e/test/a.e2e.ts";
    const b = "e2e/test/b.e2e.ts";
    const m1 = "e2e/test/m1.e2e.ts";
    const m2 = "e2e/test/m2.e2e.ts";

    assert.deepEqual(
      filterDetoxSpecsByTags(dir, [a, b, m1, m2], { includeTags: [], excludeTags: ["@flaky"] }),
      [a, m1, m2],
    );
    assert.deepEqual(sortDetoxSpecsLast(dir, [m1, a, b, m2], ["@server_mutation"]), [a, b, m1, m2]);
    assert.deepEqual(sortDetoxSpecsLast(dir, [m1, a], []), [m1, a]);
  });
});

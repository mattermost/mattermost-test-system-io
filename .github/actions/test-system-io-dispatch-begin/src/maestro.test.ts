import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { discoverMaestroSpecs, parseMaestroFlowTags } from "./maestro.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-discovery-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function writeFlow(
  root: string,
  relPath: string,
  body = "appId: ${MAESTRO_APP_ID}\n---\n- launchApp\n",
): void {
  const abs = path.join(root, relPath);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, body);
}

test("discoverMaestroSpecs: walks nested dirs, sorted, forward-slash paths", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/timezone/clock_display.yml");
    writeFlow(dir, "flows/channels/browse_channels.yml");
    writeFlow(dir, "flows/account/settings.yml");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows",
      excludeDir: "multi_device",
      excludeTags: [],
    });
    assert.deepEqual(specs, [
      "flows/account/settings.yml",
      "flows/channels/browse_channels.yml",
      "flows/timezone/clock_display.yml",
    ]);
  });
});

test("discoverMaestroSpecs: excludes named directory by default (multi_device)", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/calls/join_call.yml");
    writeFlow(dir, "flows/multi_device/two_device_call.yml");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows",
      excludeDir: "multi_device",
      excludeTags: [],
    });
    assert.deepEqual(specs, ["flows/calls/join_call.yml"]);
  });
});

test("discoverMaestroSpecs: empty excludeDir disables exclusion", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/multi_device/two_device_call.yml");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows/multi_device",
      excludeDir: "",
      excludeTags: [],
    });
    assert.deepEqual(specs, ["flows/multi_device/two_device_call.yml"]);
  });
});

test("discoverMaestroSpecs: ignores non-.yml/.yaml files (fixtures/scripts)", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/timezone/clock_display.yml");
    fs.mkdirSync(path.join(dir, "flows/timezone"), { recursive: true });
    fs.writeFileSync(path.join(dir, "flows/timezone/helper.ts"), "export const Setup = {};");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows",
      excludeDir: "multi_device",
      excludeTags: [],
    });
    assert.deepEqual(specs, ["flows/timezone/clock_display.yml"]);
  });
});

test("discoverMaestroSpecs: ignores helper flows (_-prefixed) and picker flows", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/account/settings.yml");
    writeFlow(dir, "flows/account/_connect_check.yml");
    writeFlow(dir, "flows/account/server_picker.yml");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows",
      excludeDir: "multi_device",
      excludeTags: [],
    });
    assert.deepEqual(specs, ["flows/account/settings.yml"]);
  });
});

test("discoverMaestroSpecs: excludeTags drops flows sharing a tag", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/calls/join_call.yml", "tags:\n  - @known_issue\nappId: x\n");
    writeFlow(dir, "flows/calls/mute_call.yml", "tags:\n  - MM-T100\nappId: x\n");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows",
      excludeDir: "multi_device",
      excludeTags: ["@known_issue"],
    });
    assert.deepEqual(specs, ["flows/calls/mute_call.yml"]);
  });
});

test("discoverMaestroSpecs: searchPath pointing at a single file returns just that file", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/account/login.yml");
    writeFlow(dir, "flows/account/settings.yml");
    const specs = discoverMaestroSpecs(dir, {
      searchPath: "flows/account/login.yml",
      excludeDir: "multi_device",
      excludeTags: [],
    });
    assert.deepEqual(specs, ["flows/account/login.yml"]);
  });
});

test("discoverMaestroSpecs: direct helper/picker paths are excluded", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/account/_connect_check.yml");
    writeFlow(dir, "flows/account/server_picker.yml");
    assert.deepEqual(
      discoverMaestroSpecs(dir, {
        searchPath: "flows/account/_connect_check.yml",
        excludeDir: "multi_device",
        excludeTags: [],
      }),
      [],
    );
    assert.deepEqual(
      discoverMaestroSpecs(dir, {
        searchPath: "flows/account/server_picker.yml",
        excludeDir: "multi_device",
        excludeTags: [],
      }),
      [],
    );
  });
});

test("discoverMaestroSpecs: direct tagged path honors excludeTags", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/calls/join_call.yml", "tags:\n  - @known_issue\nappId: x\n");
    assert.deepEqual(
      discoverMaestroSpecs(dir, {
        searchPath: "flows/calls/join_call.yml",
        excludeDir: "multi_device",
        excludeTags: ["@known_issue"],
      }),
      [],
    );
  });
});

test("discoverMaestroSpecs: excluded directory as search root returns empty", () => {
  withTmpDir((dir) => {
    writeFlow(dir, "flows/multi_device/two_device_call.yml");
    assert.deepEqual(
      discoverMaestroSpecs(dir, {
        searchPath: "flows/multi_device",
        excludeDir: "multi_device",
        excludeTags: [],
      }),
      [],
    );
  });
});

test("discoverMaestroSpecs: searchPath file not matching *.yml/*.yaml throws", () => {
  withTmpDir((dir) => {
    fs.mkdirSync(path.join(dir, "flows"), { recursive: true });
    fs.writeFileSync(path.join(dir, "flows/not-a-flow.ts"), "export {};");
    assert.throws(
      () =>
        discoverMaestroSpecs(dir, {
          searchPath: "flows/not-a-flow.ts",
          excludeDir: "multi_device",
          excludeTags: [],
        }),
      /doesn't match \*\.yml\/\*\.yaml/,
    );
  });
});

test("parseMaestroFlowTags: block-list form", () => {
  const tags = parseMaestroFlowTags("tags:\n  - MM-T1325\n  - @known_issue\nappId: x\n");
  assert.deepEqual(tags, ["MM-T1325", "@known_issue"]);
});

test("parseMaestroFlowTags: inline flow form", () => {
  const tags = parseMaestroFlowTags('tags: [MM-T1325, "@known_issue"]\nappId: x\n');
  assert.deepEqual(tags, ["MM-T1325", "@known_issue"]);
});

test("parseMaestroFlowTags: absent tags returns empty", () => {
  assert.deepEqual(parseMaestroFlowTags("appId: x\n---\n- launchApp\n"), []);
});

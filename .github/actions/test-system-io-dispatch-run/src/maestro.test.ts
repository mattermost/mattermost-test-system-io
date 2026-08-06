import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  aggregateMaestroReport,
  collectMaestroScreenshots,
  maestroStatus,
  parseMaestroEnv,
  parseMaestroScenarioScript,
} from "./maestro.ts";

function withTmpDir(fn: (dir: string) => void): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-run-"));
  try {
    fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test("maestroStatus: maps Maestro's JUnit status vocabulary to the closed TestStatus union", () => {
  assert.equal(maestroStatus("SUCCESS"), "passed");
  assert.equal(maestroStatus("PASSED"), "passed");
  assert.equal(maestroStatus("FAILED"), "failed");
  assert.equal(maestroStatus("ERROR"), "failed");
  assert.equal(maestroStatus("SKIPPED"), "skipped");
  assert.equal(maestroStatus("WARNING"), "skipped");
  assert.equal(maestroStatus("CANCELED"), "interrupted");
  assert.equal(maestroStatus("STOPPED"), "interrupted");
  assert.equal(maestroStatus("RUNNING"), "interrupted");
  assert.equal(maestroStatus("PENDING"), "interrupted");
  assert.equal(maestroStatus(undefined), "interrupted");
  assert.equal(maestroStatus("something-unknown"), "interrupted");
});

test("aggregateMaestroReport: a passing flow -> spec status passed, duration from time (seconds -> ms)", () => {
  const xml = `<testsuites>
  <testsuite name="Test Suite" device="detox_pixel_8_api_35" tests="1" failures="0" time="118.0">
    <testcase id="clock_display" name="clock_display" classname="clock_display" file="detox/maestro/flows/timezone/clock_display.yml" time="118.0" status="SUCCESS">
      <properties>
        <property name="tags" value="MM-T1325"/>
      </properties>
    </testcase>
  </testsuite>
</testsuites>`;
  const result = aggregateMaestroReport(xml, "detox/maestro/flows/timezone/clock_display.yml");
  assert.equal(result.status, "passed");
  assert.equal(result.actual_duration_ms, 118000);
  assert.equal(result.test_cases.length, 1);
  assert.equal(result.test_cases[0]!.title, "clock_display");
  assert.equal(result.test_cases[0]!.status, "passed");
  assert.equal(result.error_message, undefined);
});

test("aggregateMaestroReport: a failing flow surfaces the <failure> message and marks the spec failed", () => {
  const xml = `<testsuites>
  <testsuite name="Test Suite" device="ios-simulator" tests="1" failures="1" time="42.5">
    <testcase id="join_call" name="join_call" classname="join_call" file="detox/maestro/flows/calls/join_call.yml" time="42.5" status="FAILED">
      <failure message="element not found: Join Call button">assertVisible failed at step 4</failure>
    </testcase>
  </testsuite>
</testsuites>`;
  const result = aggregateMaestroReport(xml, "detox/maestro/flows/calls/join_call.yml");
  assert.equal(result.status, "failed");
  assert.equal(result.actual_duration_ms, 42500);
  assert.equal(result.error_message, "element not found: Join Call button");
  assert.equal(result.test_cases[0]!.status, "failed");
  assert.equal(result.test_cases[0]!.error_message, "element not found: Join Call button");
});

test("aggregateMaestroReport: ERROR status maps to failed even without a <failure> element", () => {
  const xml = `<testsuites>
  <testsuite name="Test Suite" tests="1" failures="0" errors="1" time="5.0">
    <testcase id="flaky_flow" name="flaky_flow" classname="flaky_flow" time="5.0" status="ERROR"/>
  </testsuite>
</testsuites>`;
  const result = aggregateMaestroReport(xml, "detox/maestro/flows/misc/flaky_flow.yml");
  assert.equal(result.status, "failed");
  assert.equal(result.test_cases[0]!.status, "failed");
});

test("aggregateMaestroReport: empty testsuites -> spec-level skipped, no test_cases", () => {
  const xml = `<testsuites></testsuites>`;
  const result = aggregateMaestroReport(xml, "detox/maestro/flows/timezone/clock_display.yml");
  assert.equal(result.status, "skipped");
  assert.equal(result.actual_duration_ms, 0);
  assert.deepEqual(result.test_cases, []);
});

test("aggregateMaestroReport: missing time attribute falls back to 0", () => {
  const xml = `<testsuites>
  <testsuite name="Test Suite" tests="1" failures="0">
    <testcase id="no_time" name="no_time" classname="no_time" status="SUCCESS"/>
  </testsuite>
</testsuites>`;
  const result = aggregateMaestroReport(xml, "detox/maestro/flows/misc/no_time.yml");
  assert.equal(result.test_cases[0]!.duration_ms, 0);
});

test("collectMaestroScreenshots: every image under the invocation's artifacts dir attaches to the single spec_path", () => {
  withTmpDir((dir) => {
    fs.mkdirSync(path.join(dir, "screenshots"), { recursive: true });
    fs.writeFileSync(path.join(dir, "screenshots", "join_call-1.png"), "");
    fs.writeFileSync(path.join(dir, "join_call-2.jpg"), "");
    fs.writeFileSync(path.join(dir, "notes.txt"), "");
    const bySpec = collectMaestroScreenshots(dir, "detox/maestro/flows/calls/join_call.yml");
    assert.deepEqual(Object.keys(bySpec), ["detox/maestro/flows/calls/join_call.yml"]);
    assert.equal(bySpec["detox/maestro/flows/calls/join_call.yml"]!.length, 2);
  });
});

test("collectMaestroScreenshots: missing artifacts directory returns empty map", () => {
  withTmpDir((dir) => {
    const bySpec = collectMaestroScreenshots(
      path.join(dir, "does-not-exist"),
      "detox/maestro/flows/calls/join_call.yml",
    );
    assert.deepEqual(bySpec, {});
  });
});

test("parseMaestroEnv: parses one KEY=VALUE pair per line", () => {
  const env = parseMaestroEnv("SITE_1_URL=https://example.com\nTEST_USER_EMAIL=a@b.com\n");
  assert.deepEqual(env, { SITE_1_URL: "https://example.com", TEST_USER_EMAIL: "a@b.com" });
});

test("parseMaestroEnv: ignores blank lines and lines without =", () => {
  const env = parseMaestroEnv("SITE_1_URL=https://example.com\n\nnotakeyvalue\n  \n");
  assert.deepEqual(env, { SITE_1_URL: "https://example.com" });
});

test("parseMaestroEnv: only the first = splits key from value", () => {
  const env = parseMaestroEnv("ADMIN_TOKEN=abc=def==");
  assert.deepEqual(env, { ADMIN_TOKEN: "abc=def==" });
});

test("parseMaestroEnv: empty input returns empty object", () => {
  assert.deepEqual(parseMaestroEnv(""), {});
});

test("parseMaestroScenarioScript: reads script field", () => {
  assert.equal(
    parseMaestroScenarioScript(
      "name: Multi-device message sync\nscript: detox/maestro/scripts/run_two_device.sh\ntags:\n  - multi_device\n",
    ),
    "detox/maestro/scripts/run_two_device.sh",
  );
});

test("parseMaestroScenarioScript: strips quotes", () => {
  assert.equal(parseMaestroScenarioScript('script: "scripts/a.sh"\n'), "scripts/a.sh");
  assert.equal(parseMaestroScenarioScript("script: 'scripts/b.sh'\n"), "scripts/b.sh");
});

test("parseMaestroScenarioScript: missing script throws", () => {
  assert.throws(
    () => parseMaestroScenarioScript("name: no script\n"),
    /missing required "script:"/,
  );
});

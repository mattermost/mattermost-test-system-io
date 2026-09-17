import { describe, expect, it } from "vitest";
import { contextFor, laneFor, parseArgs, q } from "./lib";

describe("backtest lib", () => {
  it("dollar-quotes without altering the value, even when it contains the tag", () => {
    expect(q("plain")).toBe("$q$plain$q$");
    expect(q("costs $5")).toBe("$q$costs $5$q$");
    const tricky = "has $q$ inside";
    const quoted = q(tricky);
    expect(quoted.startsWith("$q")).toBe(true);
    expect(quoted.slice(quoted.indexOf("$", 1) + 1, quoted.lastIndexOf("$q"))).toBe(tricky);
  });
  it("maps producer group names to contexts and lanes", () => {
    expect(contextFor("mattermost/mattermost-mobile", "mobile-pr-detox-ios")).toBe(
      "e2e-test/detox-ios",
    );
    expect(contextFor("mattermost/mattermost", "playwright-full-enterprise")).toBe(
      "e2e-test/playwright-full/enterprise",
    );
    expect(laneFor("mobile-pr-detox-ios")).toBe("ios");
    expect(laneFor("playwright-full-enterprise-master")).toBe("enterprise");
    expect(laneFor("mobile-main-maestro-android-e2e")).toBe("android");
  });
  it("parses flags and positionals", () => {
    expect(parseArgs(["a", "--x", "1", "b", "--flag"])).toEqual({
      opts: { x: "1", flag: "true" },
      positional: ["a", "b"],
    });
  });
});

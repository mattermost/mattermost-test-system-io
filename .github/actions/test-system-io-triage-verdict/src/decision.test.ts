import { describe, expect, it, vi } from "vitest";
import {
  annotation,
  conclusion,
  manualStatus,
  overrideDescription,
  shouldPostStatus,
  statusState,
  upsertComment,
  type Comment,
} from "./decision";

describe("verdict publishing", () => {
  it.each([
    ["SUCCESS", "success", "success"],
    ["NEUTRAL", "neutral", "success"],
    ["REGRESSION", "failure", "failure"],
    ["INCOMPLETE", "action_required", "failure"],
    ["ACTION_REQUIRED", "action_required", "failure"],
    ["INFRA", "failure", "failure"],
    ["INSUFFICIENT_DATA", "failure", "failure"],
    ["UNKNOWN", "failure", "failure"],
  ])("maps %s", (verdict, check, status) => {
    expect(conclusion(verdict)).toBe(check);
    expect(statusState(verdict)).toBe(status);
  });
  it("never publishes required statuses in off/shadow", () => {
    for (const mode of ["off", "shadow"] as const)
      for (const input of ["true", "false", "auto"])
        expect(shouldPostStatus(mode, input)).toBe(false);
    expect(shouldPostStatus("enforce", "auto")).toBe(true);
    expect(shouldPostStatus("enforce", "false")).toBe(false);
  });
  it("annotates infra as warning and blocking regression as failure", () => {
    const finding = {
      file: "spec.ts",
      full_title: "test",
      class: "INFRA",
      blocking: true,
      reason: "infrastructure",
      pr: { failure_locus: "spec.ts:42:3" },
    };
    expect(annotation(finding)).toMatchObject({ annotation_level: "warning", start_line: 42 });
    expect(annotation({ ...finding, class: "REGRESSION" }).annotation_level).toBe("failure");
    expect(annotation({ ...finding, class: "FLAKY", blocking: false }).annotation_level).toBe(
      "notice",
    );
  });
  it("recognizes manual workflow override but not engine success", () => {
    expect(manualStatus({ state: "success", description: "2 failed (verified), 1 passed" })).toBe(
      true,
    );
    expect(manualStatus({ state: "success", description: "TSIO SUCCESS" })).toBe(false);
    expect(manualStatus({ state: "failure", description: "(verified)" })).toBe(false);
  });
});

describe("sticky comments", () => {
  function writer(comments: Comment[] = []) {
    return {
      list: vi.fn(async () => comments),
      create: vi.fn(async () => undefined),
      update: vi.fn(async () => undefined),
    };
  }
  it("creates with the exact context marker", async () => {
    const w = writer();
    await upsertComment(w, "e2e/test", "body", "99", 1);
    expect(w.create).toHaveBeenCalledWith(
      "<!-- tsio-triage:e2e/test -->\n<!-- tsio-attempt:99:1 -->\nbody",
    );
  });
  it("updates an existing bot comment", async () => {
    const w = writer([{ id: 7, body: "<!-- tsio-triage:x -->\nold", user: { type: "Bot" } }]);
    await upsertComment(w, "x", "new", "99", 2);
    expect(w.update).toHaveBeenCalledWith(7, expect.stringContaining("new"));
    expect(w.create).not.toHaveBeenCalled();
  });
  it("ignores user comments and other contexts", async () => {
    const w = writer([
      { id: 1, body: "<!-- tsio-triage:x -->", user: { type: "User" } },
      { id: 2, body: "<!-- tsio-triage:y -->", user: { type: "Bot" } },
    ]);
    await upsertComment(w, "x", "new", "99", 1);
    expect(w.create).toHaveBeenCalled();
    expect(w.update).not.toHaveBeenCalled();
  });
  it.each([
    ["99", 3],
    ["100", 1],
  ])("never replaces newer run %s attempt %i", async (run, attempt) => {
    const w = writer([
      {
        id: 1,
        body: `<!-- tsio-triage:x -->\n<!-- tsio-attempt:${run}:${attempt} -->\nnewer`,
        user: { type: "Bot" },
      },
    ]);
    await upsertComment(w, "x", "old", "99", 2);
    expect(w.update).not.toHaveBeenCalled();
    expect(w.create).not.toHaveBeenCalled();
  });
  it("does not rewrite identical content", async () => {
    const w = writer([
      {
        id: 1,
        body: "<!-- tsio-triage:x -->\n<!-- tsio-attempt:99:2 -->\nsame",
        user: { type: "Bot" },
      },
    ]);
    await upsertComment(w, "x", "same", "99", 2);
    expect(w.update).not.toHaveBeenCalled();
  });
});

it("keeps native markers needed by mobile label removal", () => {
  expect(overrideDescription("E2E/Verified", "alice")).toContain(" — verified flaky by alice");
  expect(overrideDescription("E2E/Override", "alice")).toContain("e2e overridden");
  expect(
    manualStatus({ state: "success", description: overrideDescription("E2E/Verified", "alice") }),
  ).toBe(false);
});

it("resolves evidence links against the selected deployment", () => {
  expect(
    annotation(
      {
        file: "a.ts",
        full_title: "test",
        class: "REGRESSION",
        blocking: true,
        reason: "bad",
        pr: { failure_locus: "a.ts:2" },
        links: { tsio_case_url: "/reports/case", tsio_history_url: "/triage/tests/1" },
      },
      "https://staging-test-io.test.mattermost.com",
    ).message,
  ).toBe(
    "bad\nhttps://staging-test-io.test.mattermost.com/reports/case\nhttps://staging-test-io.test.mattermost.com/triage/tests/1",
  );
});

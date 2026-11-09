import { test } from "node:test";
import * as assert from "node:assert/strict";
import {
  durationLaneNames,
  durationOf,
  fetchDurationHistory,
  orderLongestFirst,
  type FetchDurationHistoryOptions,
} from "./durations.ts";

const BASE = "https://test-io.example";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function baseOpts(fetchImpl: typeof fetch): FetchDurationHistoryOptions {
  return {
    baseURL: BASE,
    repository: "mattermost/mattermost",
    name: "e2e-playwright-chrome",
    framework: "playwright",
    branch: "master",
    timeoutMs: 5000,
    fetchImpl,
  };
}

test("durationOf: exact path wins, otherwise the longest suffix match on a path boundary", () => {
  const d = new Map([
    ["specs/channels/a.spec.ts", 100],
    ["channels/a.spec.ts", 999],
    ["x/b.spec.ts", 50],
  ]);
  assert.equal(durationOf("specs/channels/a.spec.ts", d), 100);
  // Suite path has extra leading dirs, spec path is relative to the playwright dir.
  assert.equal(durationOf("tests/x/b.spec.ts", d), 50);
  // Spec has more leading dirs than the suite file path.
  assert.equal(durationOf("e2e/specs/x/b.spec.ts", d), 50);
  // A different file with the same basename but another directory is not a match.
  assert.equal(durationOf("specs/other/b.spec.ts", d), undefined);
  // A partial basename never matches: boundary required.
  assert.equal(durationOf("specs/zb.spec.ts", d), undefined);
});

test("orderLongestFirst: longest known first, unknown after alphabetically, ties by name", () => {
  const d = new Map([
    ["b.spec.ts", 14 * 60_000],
    ["c.spec.ts", 2 * 60_000],
    ["d.spec.ts", 2 * 60_000],
  ]);
  const ordered = orderLongestFirst(
    ["z.spec.ts", "d.spec.ts", "a.spec.ts", "c.spec.ts", "b.spec.ts"],
    (s) => s,
    d,
  );
  assert.deepEqual(ordered, ["b.spec.ts", "c.spec.ts", "d.spec.ts", "a.spec.ts", "z.spec.ts"]);
});

test("orderLongestFirst: deterministic for a given snapshot, whatever the input order", () => {
  const d = new Map([
    ["a.spec.ts", 5],
    ["m.spec.ts", 9],
  ]);
  const one = orderLongestFirst(["q.spec.ts", "m.spec.ts", "a.spec.ts", "b.spec.ts"], (s) => s, d);
  const two = orderLongestFirst(["b.spec.ts", "a.spec.ts", "m.spec.ts", "q.spec.ts"], (s) => s, d);
  assert.deepEqual(one, two);
  assert.deepEqual(one, ["m.spec.ts", "a.spec.ts", "b.spec.ts", "q.spec.ts"]);
});

test("orderLongestFirst: no history keeps the input order untouched (fallback)", () => {
  const input = ["z.spec.ts", "a.spec.ts"];
  assert.deepEqual(
    orderLongestFirst(input, (s) => s, new Map()),
    ["z.spec.ts", "a.spec.ts"],
  );
});

test("fetchDurationHistory: takes the newest completed base-branch group of the same lane and framework", async () => {
  const urls: string[] = [];
  const fetchImpl = (async (input: string | URL | Request) => {
    const url = String(input);
    urls.push(url);
    // No base-branch lane of its own: the lane's own name holds its base-branch runs.
    if (url.includes("name=e2e-playwright-chrome-master"))
      return jsonResponse(200, { reports: [] });
    if (url.includes("/api/v1/reports?")) {
      return jsonResponse(200, {
        reports: [
          {
            id: "g-pr",
            branch: "feature/x",
            framework: "playwright",
            status: "completed",
            commit: "c3",
            gh_run_id: "3",
          },
          {
            id: "g-run",
            branch: "master",
            framework: "playwright",
            status: "incomplete",
            commit: "c2",
            gh_run_id: "2",
          },
          {
            id: "g-cy",
            branch: "master",
            framework: "cypress",
            status: "completed",
            commit: "c1",
            gh_run_id: "1",
          },
          {
            id: "g-ok",
            branch: "master",
            framework: "playwright",
            status: "completed",
            commit: "c0",
            gh_run_id: "0",
          },
        ],
      });
    }
    return jsonResponse(200, {
      suites: [
        { file_path: "specs/a.spec.ts", duration_ms: 1000, parent_suite_id: null },
        { file_path: "specs/a.spec.ts", duration_ms: 1500, parent_suite_id: "p" },
        { file_path: "specs/b.spec.ts", duration_ms: 0 },
        { file_path: null, duration_ms: 77 },
      ],
    });
  }) as typeof fetch;

  const h = await fetchDurationHistory(baseOpts(fetchImpl));
  assert.equal(h.runId, "0");
  assert.equal(h.commit, "c0");
  // Rows of one file add up: a describe row holds its own cases, not its parent's.
  assert.deepEqual([...h.durations], [["specs/a.spec.ts", 2500]]);
  assert.equal(h.name, "e2e-playwright-chrome");
  assert.equal(urls.length, 3);
  const baseURL = new URL(urls[0]!);
  assert.equal(baseURL.searchParams.get("name"), "e2e-playwright-chrome-master");
  assert.equal(baseURL.searchParams.get("limit"), "20");
  const listURL = new URL(urls[1]!);
  assert.equal(listURL.searchParams.get("repository"), "mattermost/mattermost");
  assert.equal(listURL.searchParams.get("name"), "e2e-playwright-chrome");
  assert.equal(listURL.searchParams.get("limit"), "200");
  assert.equal(new URL(urls[2]!).pathname, "/api/v1/reports/g-ok/suites");
});

test("durationLaneNames: a base-branch run is stored under the lane name with the branch added", () => {
  const cases: [string, string, string[]][] = [
    [
      "playwright-full-enterprise",
      "master",
      ["playwright-full-enterprise-master", "playwright-full-enterprise"],
    ],
    [
      "playwright-full-enterprise-subpath",
      "master",
      [
        "playwright-full-enterprise-master-subpath",
        "playwright-full-enterprise-master",
        "playwright-full-enterprise-subpath",
      ],
    ],
    ["cypress-full-enterprise-master", "master", ["cypress-full-enterprise-master"]],
    [
      "playwright-full-enterprise-master-subpath",
      "master",
      ["playwright-full-enterprise-master-subpath", "playwright-full-enterprise-master"],
    ],
    [
      "playwright-full-fips-upgrade-from-release-11.10",
      "master",
      [
        "playwright-full-fips-upgrade-from-release-11.10-master",
        "playwright-full-fips-upgrade-from-release-11.10",
      ],
    ],
    [
      "e2e-playwright-chrome",
      "release/11.0",
      ["e2e-playwright-chrome-release-11.0", "e2e-playwright-chrome"],
    ],
  ];
  for (const [name, branch, want] of cases)
    assert.deepEqual(durationLaneNames(name, branch), want, name);
});

// The shape test-system-io serves (checked against the production API, Nov 2026): a PR lane's own name lists only PR
// runs (branch pr-<n>; /reports ignores a branch filter), master's runs of the lane are under "<name>-master", and a
// group that finished is "completed" ("complete" is a per-shard report status, never a group's).
function realShapeFetch(byName: Record<string, object[]>, urls: string[]): typeof fetch {
  return (async (input: string | URL | Request) => {
    const url = new URL(String(input));
    urls.push(String(input));
    if (url.pathname === "/api/v1/reports") {
      return jsonResponse(200, { reports: byName[url.searchParams.get("name") ?? ""] ?? [] });
    }
    return jsonResponse(200, {
      suites: [
        { file_path: "specs/functional/channels/a.spec.ts", duration_ms: 90000 },
        { file_path: "specs/functional/channels/b.spec.ts", duration_ms: 30000 },
      ],
    });
  }) as typeof fetch;
}

const row = (id: string, branch: string, status: string, framework = "playwright") => ({
  id,
  name: "",
  branch,
  framework,
  status,
  commit: `sha-${id}`,
  gh_run_id: `run-${id}`,
});

test("fetchDurationHistory: a PR lane reads master's runs of the lane from <name>-master", async () => {
  const urls: string[] = [];
  const fetchImpl = realShapeFetch(
    {
      "playwright-full-enterprise": [
        row("pr1", "pr-39803", "completed"),
        row("pr2", "pr-38885", "completed"),
      ],
      "playwright-full-enterprise-master": [
        row("m-run", "master", "in_progress"),
        row("m-inc", "master", "incomplete"),
        row("m-shard", "master", "complete"),
        row("m-ok", "master", "completed"),
      ],
    },
    urls,
  );
  const h = await fetchDurationHistory({
    ...baseOpts(fetchImpl),
    name: "playwright-full-enterprise",
  });
  assert.equal(h.name, "playwright-full-enterprise-master");
  assert.equal(h.runId, "run-m-ok");
  assert.equal(h.durations.get("specs/functional/channels/a.spec.ts"), 90000);
  assert.equal(urls.length, 2);
  assert.equal(new URL(urls[1]!).pathname, "/api/v1/reports/m-ok/suites");
});

test("fetchDurationHistory: a subpath lane falls back to master's non-subpath lane", async () => {
  const urls: string[] = [];
  const fetchImpl = realShapeFetch(
    {
      "playwright-full-enterprise-master-subpath": [row("ms-inc", "master", "incomplete")],
      "playwright-full-enterprise-master": [row("m-ok", "master", "completed")],
    },
    urls,
  );
  const h = await fetchDurationHistory({
    ...baseOpts(fetchImpl),
    name: "playwright-full-enterprise-subpath",
  });
  assert.equal(h.name, "playwright-full-enterprise-master");
  assert.deepEqual(
    urls.slice(0, 2).map((u) => new URL(u).searchParams.get("name")),
    ["playwright-full-enterprise-master-subpath", "playwright-full-enterprise-master"],
  );
});

test("fetchDurationHistory: only PR runs under every name throws", async () => {
  const fetchImpl = realShapeFetch(
    { "cypress-full-enterprise": [row("pr1", "pr-1", "completed", "cypress")] },
    [],
  );
  await assert.rejects(
    fetchDurationHistory({
      ...baseOpts(fetchImpl),
      name: "cypress-full-enterprise",
      framework: "cypress",
    }),
    /no completed cypress run on master under "cypress-full-enterprise-master", "cypress-full-enterprise"/,
  );
});

test("fetchDurationHistory: no completed base-branch group throws, so the caller can fall back", async () => {
  const fetchImpl = (async () =>
    jsonResponse(200, {
      reports: [
        {
          id: "g",
          branch: "feature/x",
          framework: "playwright",
          status: "completed",
          commit: "c",
          gh_run_id: "1",
        },
      ],
    })) as typeof fetch;
  await assert.rejects(
    fetchDurationHistory(baseOpts(fetchImpl)),
    /no completed playwright run on master under "e2e-playwright-chrome-master", "e2e-playwright-chrome"/,
  );
});

test("fetchDurationHistory: a group whose suites carry no durations throws", async () => {
  const fetchImpl = (async (input: string | URL | Request) =>
    String(input).includes("/suites")
      ? jsonResponse(200, { suites: [] })
      : jsonResponse(200, {
          reports: [
            {
              id: "g",
              branch: "master",
              framework: "playwright",
              status: "completed",
              commit: "c",
              gh_run_id: "9",
            },
          ],
        })) as typeof fetch;
  await assert.rejects(fetchDurationHistory(baseOpts(fetchImpl)), /no per-file durations/);
});

test("fetchDurationHistory: a non-200 or a network failure throws", async () => {
  const notOK = (async () => jsonResponse(503, {})) as typeof fetch;
  await assert.rejects(fetchDurationHistory(baseOpts(notOK)), /HTTP 503/);
  const down = (async () => {
    throw new TypeError("fetch failed");
  }) as typeof fetch;
  await assert.rejects(fetchDurationHistory(baseOpts(down)), /fetch failed/);
});

test("fetchDurationHistory: a hung read is bounded by timeoutMs", async () => {
  const hang = ((_: string | URL | Request, init?: RequestInit) =>
    new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new Error("aborted")));
    })) as typeof fetch;
  const started = Date.now();
  await assert.rejects(fetchDurationHistory({ ...baseOpts(hang), timeoutMs: 50 }), /aborted/);
  assert.ok(Date.now() - started < 5000);
});

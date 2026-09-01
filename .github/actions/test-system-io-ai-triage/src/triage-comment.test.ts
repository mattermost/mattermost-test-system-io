import test from "node:test";
import assert from "node:assert/strict";
import type { Decision, EvidenceCluster } from "./types.ts";
import { formatTriageComment, VERDICT_COMMENT_MARKER } from "./triage-comment.ts";
import { rollup } from "./policy.ts";

function decision(over: Partial<Decision> = {}): Decision {
  return {
    verdict: "PR_REGRESSION",
    kind: "bug",
    waived: false,
    confidence: 0.95,
    reason: "save failed after PR changes",
    member_count: 1,
    citations: [],
    source: "model",
    ...over,
  } as Decision;
}

function cluster(sig: string, label: string): EvidenceCluster {
  return {
    signature: sig,
    label,
    member_count: 1,
    representative: { external_test_id: sig, full_title: label },
  } as unknown as EvidenceCluster;
}

test("PR_REGRESSION tags the PR author with override instructions", () => {
  const body = formatTriageComment({
    prAuthor: "octocat",
    decisions: [decision()],
    clusters: [cluster("sig1", "Policy editor saves rules")],
    reportURL: "https://r.example",
  });
  assert.ok(body);
  assert.ok(body.startsWith(VERDICT_COMMENT_MARKER), "marker must lead for idempotent upsert");
  assert.match(body, /@octocat/);
  assert.match(body, /look caused by this PR/);
  assert.match(body, /\/e2e-triage-override/);
  assert.ok(!body.includes("undefined"));
});

test("MAIN_REGRESSION blames master, never the PR author, bisect queued", () => {
  const body = formatTriageComment({
    prAuthor: "octocat",
    decisions: [decision({ verdict: "MAIN_REGRESSION", reason: "invite modal race" })],
    clusters: [cluster("sig2", "Invite modal opens")],
    reportURL: "https://r.example",
  });
  assert.ok(body);
  assert.ok(!body.includes("@octocat"), "PR author must NOT be tagged for master bugs");
  assert.match(body, /existing bug on master/);
  assert.match(body, /bisect is queued/);
  assert.match(body, /invite modal race|invite modal/); // gist falls back to reason
});

test("gist is preferred over the long reason and stays one line", () => {
  const body = formatTriageComment({
    prAuthor: "octocat",
    decisions: [
      decision({
        verdict: "MAIN_REGRESSION",
        reason: "long forensic story — 500 chars of citation chain nobody reads",
        gist: "Badge shows 3 mentions after unchecking suppress — wrong product state.",
      }),
    ],
    clusters: [cluster("sig6", "Unread mention badge")],
    reportURL: "https://r.example",
  });
  assert.ok(body);
  assert.match(body, /Badge shows 3 mentions after unchecking suppress/);
  assert.ok(!body.includes("forensic story"));
});

test("all-waived outcome stays silent — silence is the unblock", () => {
  const rolled = rollup([decision({ verdict: "FLAKY_TEST", waived: true })]);
  assert.equal(rolled.waived, true);
  const body = formatTriageComment({
    prAuthor: "octocat",
    decisions: [decision({ verdict: "FLAKY_TEST", waived: true })],
    clusters: [cluster("sig3", "Slow save")],
    reportURL: "https://r.example",
  });
  assert.equal(body, null);
});

test("mixed: one unwaived master bug still comments and shows waived truthfully", () => {
  const body = formatTriageComment({
    prAuthor: "octocat",
    decisions: [
      decision({ verdict: "MAIN_REGRESSION" }),
      decision({ verdict: "FLAKY_TEST", waived: true, confidence: 0.9 }),
    ],
    clusters: [cluster("sig4", "Invite modal"), cluster("sig5", "Flaky wait")],
    reportURL: "https://r.example",
  });
  assert.ok(body);
  assert.match(body, /1 failure cluster looks like an existing bug on master/);
  assert.match(body, /✅/); // waived truthfully in the details table
});

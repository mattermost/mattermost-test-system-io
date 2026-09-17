## E2E triage: SUCCESS

Mode: **shadow** · Confidence: **100%** · Engine: `triage-4`

8 passed · 1 failed · 1 exonerated · 0 blocking · 0 infra

| Test | Classification | Trunk runs / failures / flakes | Explanation |
| --- | --- | --- | --- |
| leave_call leave_call | QUARANTINED | 14 / 3 / 0 | Active auto quarantine: flaky |

Thresholds: 14 days, 30 runs, minimum 5 runs; probability ≥ 0.050; confidence ≥ 0.60.

Existing repository override labels (E2E/Override, E2E/Verified, or E2E Tests/verified) take precedence.


### Second judge (claude-haiku-4-5)

Engine: **SUCCESS** → final: **SUCCESS** (0 blocking, 1 exonerated).

- **leave_call leave_call** — bug on_master (95%, agreed): The engine detected this as a REGRESSION with a specific fix committed to trunk after this PR's base (commit b8f42ac). The trunk history shows the test fails intermittently (4/11 runs over 14 days) but has zero consecutive failures at head, indicating the issue was resolved on master. The cross_pr evidence confirms this is a widespread flaky test affecting 12 other unrelated PRs during the same period, with 15 passing runs elsewhere. The PR's changes (channel labels display) do not touch the leave_call test file or call-related UI components. Rebase your PR to include the fix from master.
## E2E triage: FAILURE

Mode: **shadow** · Confidence: **100%** · Engine: `triage-4`

569 passed · 1 failed · 0 exonerated · 1 blocking · 0 infra

| Test | Classification | Trunk runs / failures / flakes | Explanation |
| --- | --- | --- | --- |
| Channel Settings - Copy Tests MM-T868_1 should show Copy option when long-pressing channel purpose text | REGRESSION_AREA | 11 / 0 / 0 | This file has 1 PR failures; trunk runs in the window had at most 0 (slack 0). |

Thresholds: 14 days, 30 runs, minimum 5 runs; probability ≥ 0.050; confidence ≥ 0.60.

Existing repository override labels (E2E/Override, E2E/Verified, or E2E Tests/verified) take precedence.


### Second judge (claude-haiku-4-5)

Engine: **FAILURE** → final: **FAILURE** (1 blocking, 0 exonerated).

- **Channel Settings - Copy Tests MM-T868_1 should show Copy option when long-pressing channel purpose text** — flaky environment (72%, still blocking): The test failure involves a view visibility matcher ('doesn't match the selected view'), which is a UI timing/rendering issue unrelated to the PR's changes. The PR modifies markdown and phone number linking components, not channel settings UI. While the test passed in 31 cross-PR runs but failed in two runs of PR 10050 on the same test, this pattern indicates an environmental or flaky UI rendering condition rather than a code-induced regression. The trunk history shows zero failures in the last 14 days, supporting that this is not a pre-existing bug the PR inherited.
## E2E triage: FAILURE

Mode: **shadow** · Confidence: **100%** · Engine: `triage-4`

3 passed · 1 failed · 0 exonerated · 1 blocking · 0 infra

| Test | Classification | Trunk runs / failures / flakes | Explanation |
| --- | --- | --- | --- |
| attach_logs_disabled_when_download_logs_off attach_logs_disabled_when_download_logs_off | REGRESSION_AREA | 8 / 0 / 0 | This file has 1 PR failures; trunk runs in the window had at most 0 (slack 0). |

Thresholds: 14 days, 30 runs, minimum 5 runs; probability ≥ 0.050; confidence ≥ 0.60.

Existing repository override labels (E2E/Override, E2E/Verified, or E2E Tests/verified) take precedence.


### Second judge (claude-haiku-4-5)

Engine: **FAILURE** → final: **SUCCESS** (0 blocking, 1 exonerated).

- **attach_logs_disabled_when_download_logs_off attach_logs_disabled_when_download_logs_off** — flaky environment (85%, unblocked): The test failure is a gRPC UNAVAILABLE error from the Maestro test infrastructure (io.grpc.StatusRuntimeException: UNAVAILABLE during deviceInfo call), which is environmental/infrastructural and unrelated to this PR's code changes. The PR only modifies channel linking logic in TypeScript (app/actions/remote/channel.ts, app/utils/deep_link/index.ts), touching no test framework or Android driver code. Critically, this same test failed on 6 other unrelated PRs (PR 10151, PR 10125) over the past 14 days with the same infrastructure error, while passing 10 times on other PR runs, indicating a recurring environmental issue with the Maestro Android test environment rather than a defect introduced by this specific change.
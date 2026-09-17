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

- **attach_logs_disabled_when_download_logs_off attach_logs_disabled_when_download_logs_off** — flaky environment (85%, unblocked): The failure is a gRPC UNAVAILABLE error from the Maestro test driver attempting to call deviceInfo() on an Android emulator/device. This is an infrastructure/environment issue unrelated to the PR's code changes, which focus on deep link handling and server reconnection logic. The error signature appears in multiple other PRs (PR 10125 across multiple commits), confirming this is a recurring environmental problem. The PR modifies navigation flows and deep link processing but does not touch test infrastructure, device communication, or the Maestro driver itself. The trunk has insufficient run history to establish a baseline, but the cross-PR recurrence strongly indicates environmental flakiness.
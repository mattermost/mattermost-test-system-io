## E2E triage: FAILURE

Mode: **shadow** · Confidence: **100%** · Engine: `triage-4`

10 passed · 1 failed · 0 exonerated · 1 blocking · 0 infra

| Test | Classification | Trunk runs / failures / flakes | Explanation |
| --- | --- | --- | --- |
| attach_logs_toggle_on_surfaces_option attach_logs_toggle_on_surfaces_option | REGRESSION_AREA | 14 / 0 / 0 | This file has 1 PR failures; trunk runs in the window had at most 0 (slack 0). |

Thresholds: 14 days, 30 runs, minimum 5 runs; probability ≥ 0.050; confidence ≥ 0.60.

Existing repository override labels (E2E/Override, E2E/Verified, or E2E Tests/verified) take precedence.


### Second judge (claude-haiku-4-5)

Engine: **FAILURE** → final: **SUCCESS** (0 blocking, 1 exonerated).

- **attach_logs_toggle_on_surfaces_option attach_logs_toggle_on_surfaces_option** — flaky environment (92%, unblocked): The test failure is a gRPC UNAVAILABLE error (io.grpc.StatusRuntimeException) occurring during device communication in the Maestro test framework, not related to the PR's code changes. This error has occurred on 12 other PRs in the past 14 days (PR 10125, 10116, 10050) where the same test sometimes passed and sometimes failed, indicating an environmental/infrastructure issue. The PR changes only touch websocket event handling and channel access logic—files unrelated to the Maestro test infrastructure or Android device communication. The engine confirms this is a flaky test (FLAKY_CONFIRMED class) with low trunk instability (15.38%), and the error signature doesn't match the trunk's dominant failure pattern. This is a device/gRPC connectivity issue unrelated to the PR's changes.
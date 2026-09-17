# Triage backtest: mattermost/mattermost-mobile since 2026-08-18T00:00:00Z

Runs: 509 PR runs, 49 PRs. Verdicts computed with `as_of` = run end + 5 min.

## Ground truth vs verdict

| Ground truth | Verdict | Runs |
|---|---|---|
| FIXED_BY_AUTHOR | ACTION_REQUIRED | 1 |
| FIXED_BY_AUTHOR | FAILURE | 9 |
| FIXED_BY_AUTHOR | SUCCESS | 1 |
| GREEN | SUCCESS | 21 |
| OVERRIDDEN | ACTION_REQUIRED | 3 |
| OVERRIDDEN | FAILURE | 7 |
| OVERRIDDEN | SUCCESS | 16 |
| RERUN_PASSED | FAILURE | 1 |
| UNRESOLVED | ACTION_REQUIRED | 53 |
| UNRESOLVED | FAILURE | 275 |
| UNRESOLVED | SUCCESS | 72 |
| WAIVED | ACTION_REQUIRED | 14 |
| WAIVED | FAILURE | 17 |
| WAIVED | SUCCESS | 19 |

Agreement on scoreable failing runs (WAIVED/RERUN_PASSED should unblock, FIXED_BY_AUTHOR should block): 29/62


False exonerations (author fixed it, engine would have unblocked): 1

## Failing runs

| Repo | PR | Lane | SHA | Run at | Failed | Ground truth | Verdict | Classes | Would unblock |
|---|---|---|---|---|---|---|---|---|---|
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-ios-e2e | cee5c4a | 2026-09-02T12:23 | 1 | FIXED_BY_AUTHOR | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10149](https://github.com/mattermost/mattermost-mobile/pull/10149) | mobile-pr-detox-ios | 027b993 | 2026-09-11T15:06 | 3 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-maestro-ios-e2e | fd2aa49 | 2026-09-16T01:10 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-ios | fd2aa49 | 2026-09-16T01:16 | 3 | FIXED_BY_AUTHOR | FAILURE | REGRESSION_CLUSTER×3 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-android | fd2aa49 | 2026-09-16T01:23 | 4 | FIXED_BY_AUTHOR | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×3 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-ipad | afd5d73 | 2026-09-16T07:10 | 5 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×5 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-maestro-ios-e2e | afd5d73 | 2026-09-16T07:40 | 2 | FIXED_BY_AUTHOR | FAILURE | FLAKY_CROSS_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-android | afd5d73 | 2026-09-16T07:45 | 29 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×4, REGRESSION×1, REGRESSION_CLUSTER×24 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-ios | afd5d73 | 2026-09-16T07:48 | 28 | FIXED_BY_AUTHOR | FAILURE | REGRESSION×7, REGRESSION_CLUSTER×21 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-android | 1e1ba92 | 2026-09-16T13:33 | 1 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10161](https://github.com/mattermost/mattermost-mobile/pull/10161) | mobile-pr-detox-ios | 1e1ba92 | 2026-09-16T13:37 | 2 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [9919](https://github.com/mattermost/mattermost-mobile/pull/9919) | mobile-pr-maestro-ios-e2e | 59b484f | 2026-08-18T04:07 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [9919](https://github.com/mattermost/mattermost-mobile/pull/9919) | mobile-pr-detox-ios | 59b484f | 2026-08-18T04:18 | 1 | OVERRIDDEN | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [9830](https://github.com/mattermost/mattermost-mobile/pull/9830) | mobile-pr-maestro-ios-e2e | 0dce0d6 | 2026-08-18T14:56 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [9830](https://github.com/mattermost/mattermost-mobile/pull/9830) | mobile-pr-detox-ios | c39b637 | 2026-08-18T17:56 | 7 | OVERRIDDEN | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×6 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-android-e2e | 9ffad17 | 2026-08-21T17:39 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10091](https://github.com/mattermost/mattermost-mobile/pull/10091) | mobile-pr-detox-android | 594399c | 2026-08-21T20:28 | 3 | OVERRIDDEN | SUCCESS | FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10091](https://github.com/mattermost/mattermost-mobile/pull/10091) | mobile-pr-detox-ios | 594399c | 2026-08-21T20:44 | 2 | OVERRIDDEN | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | 97d234d | 2026-08-25T01:37 | 1 | OVERRIDDEN | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | caaf61d | 2026-08-26T01:36 | 2 | OVERRIDDEN | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | caaf61d | 2026-08-26T01:46 | 6 | OVERRIDDEN | FAILURE | FLAKY_CROSS_PR×3, REGRESSION×3 | no |
| mattermost-mobile | [10112](https://github.com/mattermost/mattermost-mobile/pull/10112) | mobile-pr-maestro-android-e2e | da096ab | 2026-09-01T18:45 | 1 | OVERRIDDEN | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10112](https://github.com/mattermost/mattermost-mobile/pull/10112) | mobile-pr-detox-ios | 4283a53 | 2026-09-01T21:07 | 2 | OVERRIDDEN | FAILURE | FLAKY_CONFIRMED×1, INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10112](https://github.com/mattermost/mattermost-mobile/pull/10112) | mobile-pr-maestro-ios-e2e | 7a81ec4 | 2026-09-03T16:34 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10120](https://github.com/mattermost/mattermost-mobile/pull/10120) | mobile-pr-maestro-android-e2e | a5ea008 | 2026-09-04T17:57 | 1 | OVERRIDDEN | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10120](https://github.com/mattermost/mattermost-mobile/pull/10120) | mobile-pr-maestro-ios-e2e | a5ea008 | 2026-09-04T18:46 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10120](https://github.com/mattermost/mattermost-mobile/pull/10120) | mobile-pr-detox-ios | a5ea008 | 2026-09-04T19:43 | 3 | OVERRIDDEN | SUCCESS | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10119](https://github.com/mattermost/mattermost-mobile/pull/10119) | mobile-pr-detox-android | b59e25f | 2026-09-08T16:43 | 1 | OVERRIDDEN | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10027](https://github.com/mattermost/mattermost-mobile/pull/10027) | mobile-pr-detox-ios | d6eedb2 | 2026-09-08T20:10 | 3 | OVERRIDDEN | FAILURE | BROKEN_ON_TRUNK×2, REGRESSION×1 | no |
| mattermost-mobile | [10119](https://github.com/mattermost/mattermost-mobile/pull/10119) | mobile-pr-detox-ios | 09cdf4b | 2026-09-08T22:41 | 4 | OVERRIDDEN | FAILURE | FLAKY_CONFIRMED×3, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ipad | 3a7807c | 2026-09-09T00:24 | 1 | OVERRIDDEN | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10127](https://github.com/mattermost/mattermost-mobile/pull/10127) | mobile-pr-maestro-android-e2e | a508815 | 2026-09-09T10:42 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10127](https://github.com/mattermost/mattermost-mobile/pull/10127) | mobile-pr-detox-ios | a508815 | 2026-09-09T12:19 | 4 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×2, FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | a5c43b8 | 2026-09-10T06:43 | 1 | OVERRIDDEN | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 7b3c626 | 2026-09-10T12:38 | 1 | OVERRIDDEN | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 0eff784 | 2026-09-10T14:58 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 62d6b0c | 2026-09-10T18:53 | 1 | OVERRIDDEN | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10149](https://github.com/mattermost/mattermost-mobile/pull/10149) | mobile-pr-detox-ios | 679fd1b | 2026-09-15T18:18 | 1 | RERUN_PASSED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-android | 7f95067 | 2026-08-18T00:58 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 7f95067 | 2026-08-18T01:09 | 4 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-detox-ios | ffff65e | 2026-08-18T02:20 | 2 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 79023fb | 2026-08-18T03:52 | 9 | UNRESOLVED | FAILURE | OWNED_BY_PR×9 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 79023fb | 2026-08-18T03:57 | 10 | UNRESOLVED | FAILURE | OWNED_BY_PR×10 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-android | 13af470 | 2026-08-18T04:04 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 13af470 | 2026-08-18T04:06 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | e866553 | 2026-08-18T05:31 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | fcc9187 | 2026-08-18T10:15 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 7f75e5c | 2026-08-18T11:57 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 7f75e5c | 2026-08-18T12:04 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 7f75e5c | 2026-08-18T12:36 | 8 | UNRESOLVED | FAILURE | OWNED_BY_PR×8 | no |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-maestro-ios-e2e | 2469ef5 | 2026-08-18T13:19 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10054](https://github.com/mattermost/mattermost-mobile/pull/10054) | maestro-android | f4519fc | 2026-08-18T13:26 | 10 | UNRESOLVED | FAILURE | NEW_TEST×2, OWNED_BY_PR×8 | no |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-detox-ios | 2469ef5 | 2026-08-18T13:33 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-detox-android | 2469ef5 | 2026-08-18T13:37 | 2 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10054](https://github.com/mattermost/mattermost-mobile/pull/10054) | maestro-android | 547e5e6 | 2026-08-18T14:38 | 5 | UNRESOLVED | FAILURE | NEW_TEST×1, OWNED_BY_PR×4 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 5865fcd | 2026-08-18T15:00 | 9 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×5, OWNED_BY_PR×4 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 706e84e | 2026-08-18T15:16 | 3 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-maestro-ios-e2e | 0b563de | 2026-08-18T15:27 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-detox-android | 0b563de | 2026-08-18T15:44 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-detox-ios | 0b563de | 2026-08-18T15:53 | 6 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×6 | yes |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-maestro-android-e2e | b21b1aa | 2026-08-18T16:39 | 3 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-detox-ios | cfa2441 | 2026-08-18T17:05 | 7 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×2, REGRESSION×4 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 5865fcd | 2026-08-18T17:13 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10047](https://github.com/mattermost/mattermost-mobile/pull/10047) | mobile-pr-detox-ios | b21b1aa | 2026-08-18T17:41 | 3 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 99f63a4 | 2026-08-18T19:50 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 99f63a4 | 2026-08-18T20:12 | 202 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×5, OWNED_BY_PR×123, REGRESSION×35, REGRESSION_CLUSTER×38 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-android | 99f63a4 | 2026-08-18T20:42 | 51 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×2, OWNED_BY_PR×12, REGRESSION×4, REGRESSION_CLUSTER×33 | no |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-detox-ios | 27fced3 | 2026-08-18T22:01 | 7 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×3, REGRESSION×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 6a269a9 | 2026-08-18T22:12 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 6a269a9 | 2026-08-18T23:03 | 11 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×10 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 6a269a9 | 2026-08-18T23:09 | 23 | UNRESOLVED | FAILURE | OWNED_BY_PR×18, REGRESSION×5 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-ios-e2e | 1ed059f | 2026-08-19T02:05 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 1ed059f | 2026-08-19T02:29 | 11 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×1, OWNED_BY_PR×9 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-android | 1ed059f | 2026-08-19T02:48 | 11 | UNRESOLVED | FAILURE | OWNED_BY_PR×9, REGRESSION×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | 82717fb | 2026-08-19T03:59 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 0eaaa5d | 2026-08-19T06:24 | 25 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×23, REGRESSION×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | dd385d5 | 2026-08-19T06:40 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 66147f8 | 2026-08-19T07:16 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | 66147f8 | 2026-08-19T07:24 | 7 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×3, OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 66147f8 | 2026-08-19T07:33 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ipad | 66147f8 | 2026-08-19T07:37 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 978a4f8 | 2026-08-19T08:14 | 2 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | ab86a52 | 2026-08-19T08:27 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-android-e2e | 978a4f8 | 2026-08-19T08:28 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 058376b | 2026-08-19T09:29 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-ios-e2e | 058376b | 2026-08-19T09:32 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 058376b | 2026-08-19T10:05 | 17 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×14, REGRESSION×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 8081cc9 | 2026-08-19T10:22 | 22 | UNRESOLVED | FAILURE | OWNED_BY_PR×19, REGRESSION×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 8081cc9 | 2026-08-19T10:42 | 14 | UNRESOLVED | FAILURE | OWNED_BY_PR×14 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-android | 978a4f8 | 2026-08-19T11:02 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10041](https://github.com/mattermost/mattermost-mobile/pull/10041) | mobile-pr-detox-ios | 0fa1785 | 2026-08-19T13:05 | 16 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×3, REGRESSION_CLUSTER×11 | no |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-maestro-android-e2e | 89e173b | 2026-08-19T13:53 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10028](https://github.com/mattermost/mattermost-mobile/pull/10028) | mobile-pr-detox-ios | 4e8de63 | 2026-08-19T14:01 | 9 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×3, REGRESSION×1, REGRESSION_CLUSTER×5 | no |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-detox-ios | 5ba7c05 | 2026-08-19T16:09 | 4 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 2c2bbd2 | 2026-08-20T09:02 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-android-e2e | 00ee540 | 2026-08-20T12:02 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | 7e13980 | 2026-08-20T13:29 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 7e13980 | 2026-08-20T13:54 | 3 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×3 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | 7a6c8d3 | 2026-08-20T15:56 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | fd45a15 | 2026-08-20T17:35 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | fd45a15 | 2026-08-20T17:39 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | de98cf0 | 2026-08-20T18:07 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-android-e2e | 947fae2 | 2026-08-20T18:56 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10041](https://github.com/mattermost/mattermost-mobile/pull/10041) | mobile-pr-detox-ios | 1f46395 | 2026-08-20T19:19 | 3 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-ios-e2e | 947fae2 | 2026-08-20T19:38 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 135f38d | 2026-08-20T19:46 | 9 | UNRESOLVED | FAILURE | OWNED_BY_PR×5, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 947fae2 | 2026-08-20T20:53 | 30 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×6, REGRESSION_CLUSTER×24 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 135f38d | 2026-08-20T21:16 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-maestro-android-e2e | 947fae2 | 2026-08-20T23:05 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 947fae2 | 2026-08-20T23:16 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | a2b911e | 2026-08-21T00:08 | 2 | UNRESOLVED | FAILURE | INFRA×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | ffe2d39 | 2026-08-21T00:15 | 30 | UNRESOLVED | FAILURE | OWNED_BY_PR×29, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | ffe2d39 | 2026-08-21T00:21 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 61a4668 | 2026-08-21T10:49 | 4 | UNRESOLVED | FAILURE | INFRA×1, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 7cceda5 | 2026-08-21T11:11 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 9fdf58a | 2026-08-21T13:08 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 65cea21 | 2026-08-21T17:34 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-ios-e2e | 65cea21 | 2026-08-21T17:35 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-ios-e2e | 574c7af | 2026-08-21T18:18 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | 9ffad17 | 2026-08-21T18:55 | 15 | UNRESOLVED | FAILURE | OWNED_BY_PR×2, REGRESSION×2, REGRESSION_CLUSTER×11 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 574c7af | 2026-08-21T19:00 | 52 | UNRESOLVED | FAILURE | OWNED_BY_PR×52 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 309c706 | 2026-08-21T21:41 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | cdc11fa | 2026-08-21T22:01 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 7a46643 | 2026-08-22T00:55 | 15 | UNRESOLVED | FAILURE | OWNED_BY_PR×15 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | ebb61a7 | 2026-08-22T02:57 | 21 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×17, REGRESSION×3 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | a980ec9 | 2026-08-22T05:31 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 04278cd | 2026-08-22T06:56 | 9 | UNRESOLVED | FAILURE | REGRESSION×1, REGRESSION_CLUSTER×8 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | 04278cd | 2026-08-22T07:15 | 167 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×3, FLAKY_CROSS_PR×5, REGRESSION×57, REGRESSION_CLUSTER×102 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 28263fd | 2026-08-22T07:51 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 4f512d5 | 2026-08-22T07:53 | 14 | UNRESOLVED | FAILURE | OWNED_BY_PR×14 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 4f512d5 | 2026-08-22T08:06 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 04278cd | 2026-08-22T08:53 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 6a3697f | 2026-08-22T11:26 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 6a3697f | 2026-08-22T11:50 | 13 | UNRESOLVED | FAILURE | INFRA×2, OWNED_BY_PR×11 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-android | 40d390e | 2026-08-24T13:29 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 40d390e | 2026-08-24T13:47 | 7 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×1, REGRESSION×4, REGRESSION_AREA×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-android-e2e | 4989cff | 2026-08-24T16:14 | 9 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×8, REGRESSION×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-ios-e2e | 4989cff | 2026-08-24T17:20 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 4989cff | 2026-08-24T17:47 | 8 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×6 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ipad | 7dac573 | 2026-08-24T21:21 | 5 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×5 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-android | 7dac573 | 2026-08-24T22:28 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 7dac573 | 2026-08-24T23:18 | 188 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×6, FLAKY_CROSS_PR×6, REGRESSION×95, REGRESSION_AREA×2, REGRESSION_CLUSTER×78 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 2f12ad0 | 2026-08-25T03:32 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | f181296 | 2026-08-25T03:43 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×11, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | f181296 | 2026-08-25T04:00 | 15 | UNRESOLVED | FAILURE | OWNED_BY_PR×15 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | 483625c | 2026-08-25T06:13 | 5 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×5 | yes |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | e278441 | 2026-08-25T08:40 | 11 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×8, REGRESSION×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 45de7f3 | 2026-08-25T08:44 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-ios | e278441 | 2026-08-25T08:47 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 45de7f3 | 2026-08-25T08:53 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 45de7f3 | 2026-08-25T09:34 | 14 | UNRESOLVED | FAILURE | OWNED_BY_PR×13, REGRESSION×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-ios-e2e | 2e6e405 | 2026-08-25T14:26 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 2e6e405 | 2026-08-25T14:53 | 8 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×6, REGRESSION×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-android | 2e6e405 | 2026-08-25T14:59 | 34 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×6, REGRESSION×28 | no |
| mattermost-mobile | [10081](https://github.com/mattermost/mattermost-mobile/pull/10081) | mobile-pr-detox-android | 4a16531 | 2026-08-25T15:26 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10035](https://github.com/mattermost/mattermost-mobile/pull/10035) | mobile-pr-detox-ios | dd1efe1 | 2026-08-25T15:43 | 2 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-maestro-android-e2e | 8a7508b | 2026-08-25T17:35 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-maestro-ios-e2e | 4dc998c | 2026-08-25T18:58 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 15d4437 | 2026-08-25T19:02 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 25c50a1 | 2026-08-25T20:12 | 9 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×1, REGRESSION×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 15d4437 | 2026-08-25T20:18 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [9959](https://github.com/mattermost/mattermost-mobile/pull/9959) | mobile-pr-detox-ios | 13ef2cc | 2026-08-25T21:04 | 2 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-ios-e2e | 8f89593 | 2026-08-25T22:20 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | 8f89593 | 2026-08-25T22:40 | 233 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×5, FLAKY_CROSS_PR×35, REGRESSION×44, REGRESSION_AREA×1, REGRESSION_CLUSTER×148 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-maestro-ios-e2e | 31b03ff | 2026-08-25T23:52 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-android | 31b03ff | 2026-08-26T00:11 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 31b03ff | 2026-08-26T00:17 | 13 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×6, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 6b4bae1 | 2026-08-26T08:43 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 6b4bae1 | 2026-08-26T08:46 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 6b4bae1 | 2026-08-26T09:32 | 10 | UNRESOLVED | FAILURE | OWNED_BY_PR×10 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | 15b60d1 | 2026-08-26T12:59 | 6 | UNRESOLVED | FAILURE | OWNED_BY_PR×6 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | fecd872 | 2026-08-26T15:26 | 2 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | a4e8b59 | 2026-08-26T16:51 | 7 | UNRESOLVED | FAILURE | REGRESSION×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | e609530 | 2026-08-26T21:12 | 5 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×5 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | fed173c | 2026-08-26T23:34 | 20 | UNRESOLVED | FAILURE | OWNED_BY_PR×20 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | fed173c | 2026-08-27T00:32 | 17 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, INFRA×1, OWNED_BY_PR×15 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 9781143 | 2026-08-27T04:35 | 7 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | a756944 | 2026-08-27T04:49 | 11 | UNRESOLVED | FAILURE | OWNED_BY_PR×10, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | a756944 | 2026-08-27T06:08 | 15 | UNRESOLVED | FAILURE | OWNED_BY_PR×15 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-ios | ebb55cd | 2026-08-27T12:20 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 9fed41b | 2026-08-27T13:21 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 9fed41b | 2026-08-27T13:38 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 9fed41b | 2026-08-27T14:20 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10100](https://github.com/mattermost/mattermost-mobile/pull/10100) | mobile-pr-detox-android | 636eadb | 2026-08-27T14:51 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10100](https://github.com/mattermost/mattermost-mobile/pull/10100) | mobile-pr-detox-ios | 636eadb | 2026-08-27T14:57 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-android | 64a8923 | 2026-08-27T18:11 | 3 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 64a8923 | 2026-08-27T18:29 | 19 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×9, REGRESSION×4, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-android | a639e87 | 2026-08-27T20:36 | 4 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×4 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | a639e87 | 2026-08-27T20:37 | 12 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×4, FLAKY_CROSS_PR×1, REGRESSION×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | b1fb107 | 2026-08-28T11:37 | 8 | UNRESOLVED | FAILURE | OWNED_BY_PR×8 | no |
| mattermost-mobile | [10100](https://github.com/mattermost/mattermost-mobile/pull/10100) | mobile-pr-detox-ios | 0e6d6d6 | 2026-08-28T12:07 | 9 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×3, INSUFFICIENT_DATA×2, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10096](https://github.com/mattermost/mattermost-mobile/pull/10096) | mobile-pr-detox-ios | 89ec69d | 2026-08-28T13:01 | 3 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 9334aad | 2026-08-28T13:52 | 8 | UNRESOLVED | FAILURE | OWNED_BY_PR×8 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 9334aad | 2026-08-28T14:22 | 11 | UNRESOLVED | FAILURE | OWNED_BY_PR×11 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | c2bda4f | 2026-08-28T21:37 | 9 | UNRESOLVED | FAILURE | OWNED_BY_PR×8, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | c2bda4f | 2026-08-28T22:00 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 8dc1999 | 2026-08-28T23:12 | 6 | UNRESOLVED | FAILURE | OWNED_BY_PR×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 8dc1999 | 2026-08-28T23:36 | 13 | UNRESOLVED | FAILURE | OWNED_BY_PR×13 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 43d05b4 | 2026-08-29T07:21 | 6 | UNRESOLVED | FAILURE | OWNED_BY_PR×6 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 43d05b4 | 2026-08-29T08:04 | 21 | UNRESOLVED | FAILURE | OWNED_BY_PR×19, REGRESSION×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 0a47cb3 | 2026-08-29T09:25 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 0a47cb3 | 2026-08-29T09:26 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ipad | 0a47cb3 | 2026-08-29T09:26 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 0a47cb3 | 2026-08-29T09:28 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 63ba8ce | 2026-08-29T11:03 | 7 | UNRESOLVED | FAILURE | OWNED_BY_PR×7 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 63ba8ce | 2026-08-29T11:05 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 63ba8ce | 2026-08-29T11:49 | 14 | UNRESOLVED | FAILURE | OWNED_BY_PR×14 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 937b1f0 | 2026-08-29T13:44 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 937b1f0 | 2026-08-29T14:33 | 13 | UNRESOLVED | FAILURE | OWNED_BY_PR×13 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 7864682 | 2026-08-29T15:46 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 7864682 | 2026-08-29T16:04 | 17 | UNRESOLVED | FAILURE | OWNED_BY_PR×17 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 80bef0e | 2026-08-29T16:51 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | 80bef0e | 2026-08-29T16:51 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ipad | 80bef0e | 2026-08-29T16:51 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 80bef0e | 2026-08-29T16:51 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | e7ea447 | 2026-08-30T08:56 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | e7ea447 | 2026-08-30T09:05 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 127dfb5 | 2026-08-30T09:56 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 127dfb5 | 2026-08-30T11:08 | 11 | UNRESOLVED | FAILURE | OWNED_BY_PR×11 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 9295a79 | 2026-08-30T18:02 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 9295a79 | 2026-08-30T18:46 | 14 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×13 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 03da433 | 2026-08-30T23:54 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 03da433 | 2026-08-31T00:31 | 17 | UNRESOLVED | FAILURE | OWNED_BY_PR×17 | no |
| mattermost-mobile | [10105](https://github.com/mattermost/mattermost-mobile/pull/10105) | mobile-pr-detox-ios | 59e6a3c | 2026-08-31T00:56 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | c276349 | 2026-08-31T05:12 | 8 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×4, OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | c276349 | 2026-08-31T05:15 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 40b8e5e | 2026-08-31T07:02 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 40b8e5e | 2026-08-31T07:15 | 22 | UNRESOLVED | FAILURE | OWNED_BY_PR×22 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 40b8e5e | 2026-08-31T07:29 | 98 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×5, INSUFFICIENT_DATA×2, OWNED_BY_PR×89, REGRESSION×1 | no |
| mattermost-mobile | [10096](https://github.com/mattermost/mattermost-mobile/pull/10096) | mobile-pr-maestro-ios-e2e | d584997 | 2026-08-31T11:37 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 59176a3 | 2026-08-31T18:47 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 59176a3 | 2026-08-31T18:49 | 7 | UNRESOLVED | FAILURE | OWNED_BY_PR×7 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 0af8631 | 2026-08-31T23:31 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 0af8631 | 2026-08-31T23:50 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | caace97 | 2026-09-01T09:49 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×2, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | caace97 | 2026-09-01T09:54 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | caace97 | 2026-09-01T10:05 | 8 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×6, REGRESSION_AREA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | 2cba073 | 2026-09-01T11:48 | 6 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION×5 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-android-e2e | 4049852 | 2026-09-01T12:54 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 5fc64cf | 2026-09-01T14:20 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [9958](https://github.com/mattermost/mattermost-mobile/pull/9958) | mobile-pr-detox-ios | 4d7c6c6 | 2026-09-01T15:47 | 2 | UNRESOLVED | FAILURE | INFRA×1, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ipad | c78b50f | 2026-09-01T18:17 | 10 | UNRESOLVED | FAILURE | OWNED_BY_PR×10 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | c78b50f | 2026-09-01T18:27 | 7 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×5, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | c78b50f | 2026-09-01T18:43 | 253 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, INFRA×1, OWNED_BY_PR×238, REGRESSION×6, REGRESSION_CLUSTER×7 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | c78b50f | 2026-09-01T18:43 | 4 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, REGRESSION_CLUSTER×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | c78b50f | 2026-09-01T18:43 | 352 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, INFRA×1, OWNED_BY_PR×336, REGRESSION×8, REGRESSION_AREA×2, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | c78b50f | 2026-09-01T20:01 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 06bbd9c | 2026-09-02T01:23 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 06bbd9c | 2026-09-02T01:43 | 4 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | dbc0464 | 2026-09-02T10:15 | 9 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×9 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | dbc0464 | 2026-09-02T11:17 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×3, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | dbc0464 | 2026-09-02T11:39 | 470 | UNRESOLVED | FAILURE | INFRA×1, INSUFFICIENT_DATA×1, OWNED_BY_PR×207, REGRESSION×2, REGRESSION_CLUSTER×259 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 235558c | 2026-09-02T14:45 | 13 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×3, OWNED_BY_PR×9, REGRESSION×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 235558c | 2026-09-02T14:55 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | e54b67a | 2026-09-02T20:49 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | 8ede270 | 2026-09-02T21:08 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | e6eed6c | 2026-09-02T21:37 | 9 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×9 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | d6ae278 | 2026-09-03T00:03 | 9 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×9 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-ios-e2e | d6ae278 | 2026-09-03T00:41 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-detox-ios | d6ae278 | 2026-09-03T01:10 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-detox-android | d6ae278 | 2026-09-03T01:31 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 04b1028 | 2026-09-03T06:52 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-android | b89ed6b | 2026-09-03T18:14 | 2 | UNRESOLVED | FAILURE | REGRESSION×2 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | b89ed6b | 2026-09-03T19:14 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×2, REGRESSION×2 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | 0646611 | 2026-09-03T20:10 | 3 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×3 | yes |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | 92918de | 2026-09-03T20:54 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-detox-ios | 92918de | 2026-09-03T22:20 | 3 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×2, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 27fced5 | 2026-09-04T11:25 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-android-e2e | b5a03bc | 2026-09-04T11:31 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-android-e2e | 2477545 | 2026-09-04T12:24 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 16a1ba0 | 2026-09-04T12:26 | 10 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×9, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 16a1ba0 | 2026-09-04T12:43 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ipad | 16a1ba0 | 2026-09-04T12:43 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 16a1ba0 | 2026-09-04T12:44 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 4ec67f6 | 2026-09-04T13:20 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 4ec67f6 | 2026-09-04T13:21 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 2477545 | 2026-09-04T13:45 | 4 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×2, FLAKY_CROSS_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | a4f4c85 | 2026-09-04T14:12 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | a4f4c85 | 2026-09-04T14:19 | 10 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×9, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | a4f4c85 | 2026-09-04T14:41 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×2, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | a4f4c85 | 2026-09-04T15:28 | 8 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×5, INFRA×1, REGRESSION×2 | no |
| mattermost-mobile | [10120](https://github.com/mattermost/mattermost-mobile/pull/10120) | mobile-pr-maestro-android-e2e | 4a7ae34 | 2026-09-04T16:51 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-android-e2e | 132f46e | 2026-09-04T17:58 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 57debea | 2026-09-04T18:42 | 9 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×7, OWNED_BY_PR×2 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 132f46e | 2026-09-04T18:43 | 4 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 57debea | 2026-09-04T18:51 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 57debea | 2026-09-04T19:14 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | d148602 | 2026-09-04T20:17 | 10 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×8, OWNED_BY_PR×2 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-android-e2e | d0c2a39 | 2026-09-04T20:18 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 77220c5 | 2026-09-04T20:58 | 10 | UNRESOLVED | FAILURE | OWNED_BY_PR×2, REGRESSION_CLUSTER×8 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-android | d0c2a39 | 2026-09-04T21:08 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 77220c5 | 2026-09-04T21:35 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ipad | 77220c5 | 2026-09-04T21:38 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 77220c5 | 2026-09-04T21:50 | 20 | UNRESOLVED | ACTION_REQUIRED | INFRA×20 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 2c7dab6 | 2026-09-04T22:52 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 2c7dab6 | 2026-09-04T22:59 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 2c7dab6 | 2026-09-04T23:32 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 2c7dab6 | 2026-09-05T00:24 | 31 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×3, OWNED_BY_PR×20, REGRESSION×6 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 1004316 | 2026-09-05T01:40 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-android-e2e | d0c2a39 | 2026-09-05T01:53 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 1004316 | 2026-09-05T01:53 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 1004316 | 2026-09-05T02:22 | 50 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, INSUFFICIENT_DATA×4, OWNED_BY_PR×38, REGRESSION×7 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | d0c2a39 | 2026-09-05T02:37 | 7 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, REGRESSION×1, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 20b3cbb | 2026-09-05T04:52 | 43 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×10, INSUFFICIENT_DATA×4, OWNED_BY_PR×23, REGRESSION×6 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 46a5d40 | 2026-09-05T05:10 | 4 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 20b3cbb | 2026-09-05T05:14 | 39 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×4, OWNED_BY_PR×3, REGRESSION×8, REGRESSION_CLUSTER×24 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 6a0b20b | 2026-09-05T07:19 | 44 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×4, INSUFFICIENT_DATA×4, OWNED_BY_PR×22, REGRESSION×14 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 6a0b20b | 2026-09-05T07:42 | 40 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION×6, REGRESSION_CLUSTER×33 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | e0ad986 | 2026-09-06T06:09 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 5c3eb3b | 2026-09-06T07:22 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-ios-e2e | 409742f | 2026-09-06T08:19 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-detox-ios | 409742f | 2026-09-06T08:43 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | e6dc8f8 | 2026-09-06T09:37 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | f44d1d8 | 2026-09-06T12:19 | 10 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×7, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10050](https://github.com/mattermost/mattermost-mobile/pull/10050) | mobile-pr-maestro-android-e2e | db4f42f | 2026-09-06T12:42 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | f44d1d8 | 2026-09-06T13:04 | 5 | UNRESOLVED | FAILURE | OWNED_BY_PR×5 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | f44d1d8 | 2026-09-06T14:13 | 469 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×27, INSUFFICIENT_DATA×6, OWNED_BY_PR×52, REGRESSION×384 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | cb36a8e | 2026-09-07T06:06 | 6 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×3, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | cb36a8e | 2026-09-07T06:41 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | f5905f5 | 2026-09-07T08:00 | 6 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×2, REGRESSION×3 | no |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-detox-ios | ecf1f3c | 2026-09-07T08:56 | 9 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×1, REGRESSION×6 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 9381c5c | 2026-09-07T09:30 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 65e986c | 2026-09-07T10:21 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 65e986c | 2026-09-07T10:43 | 8 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×8 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | 5cb1c6c | 2026-09-07T12:17 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 5cb1c6c | 2026-09-07T13:52 | 25 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×7, OWNED_BY_PR×9, REGRESSION×7 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 5cb1c6c | 2026-09-07T14:03 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-android | 8dd0346 | 2026-09-07T15:08 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | 765d07f | 2026-09-07T15:17 | 1 | UNRESOLVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-ios-e2e | 8dd0346 | 2026-09-07T15:20 | 2 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 8dd0346 | 2026-09-07T15:54 | 365 | UNRESOLVED | FAILURE | OWNED_BY_PR×361, REGRESSION×4 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 765d07f | 2026-09-07T17:07 | 25 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×1, INFRA×1, OWNED_BY_PR×9, REGRESSION×1, REGRESSION_CLUSTER×11 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-android | 8dd0346 | 2026-09-07T17:58 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 8ddfd50 | 2026-09-07T18:07 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 8dd0346 | 2026-09-07T18:17 | 2 | UNRESOLVED | FAILURE | INFRA×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 8ddfd50 | 2026-09-07T18:42 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-ios-e2e | 2adf926 | 2026-09-07T19:55 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 2adf926 | 2026-09-07T20:35 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 98bdca0 | 2026-09-07T21:50 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 5b1c02e | 2026-09-07T23:56 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 54e8caf | 2026-09-08T01:03 | 36 | UNRESOLVED | FAILURE | OWNED_BY_PR×29, REGRESSION×7 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 54e8caf | 2026-09-08T01:20 | 28 | UNRESOLVED | FAILURE | OWNED_BY_PR×25, REGRESSION×3 | no |
| mattermost-mobile | [10119](https://github.com/mattermost/mattermost-mobile/pull/10119) | mobile-pr-detox-ios | 7102f01 | 2026-09-08T03:34 | 2 | UNRESOLVED | ACTION_REQUIRED | BROKEN_ON_TRUNK×2 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 3873cde | 2026-09-08T05:10 | 4 | UNRESOLVED | FAILURE | OWNED_BY_PR×4 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | dc5ad14 | 2026-09-08T05:44 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10119](https://github.com/mattermost/mattermost-mobile/pull/10119) | mobile-pr-detox-ios | 9ff14df | 2026-09-08T06:09 | 5 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, REGRESSION_CLUSTER×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | dc5ad14 | 2026-09-08T06:19 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-maestro-ios-e2e | 71f9ab7 | 2026-09-08T07:24 | 1 | UNRESOLVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 71f9ab7 | 2026-09-08T07:56 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-maestro-android-e2e | e7163c1 | 2026-09-08T13:50 | 1 | UNRESOLVED | ACTION_REQUIRED | BROKEN_ON_TRUNK×1 | no |
| mattermost-mobile | [10122](https://github.com/mattermost/mattermost-mobile/pull/10122) | mobile-pr-detox-ios | 80dadd3 | 2026-09-08T13:55 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10116](https://github.com/mattermost/mattermost-mobile/pull/10116) | mobile-pr-detox-ios | e7163c1 | 2026-09-08T15:33 | 2 | UNRESOLVED | ACTION_REQUIRED | BROKEN_ON_TRUNK×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 2e6aed2 | 2026-09-08T16:24 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | aeaff26 | 2026-09-08T17:23 | 1 | UNRESOLVED | ACTION_REQUIRED | BROKEN_ON_TRUNK×1 | no |
| mattermost-mobile | [10119](https://github.com/mattermost/mattermost-mobile/pull/10119) | mobile-pr-detox-ios | b59e25f | 2026-09-08T17:35 | 12 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×6, REGRESSION×3 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | aeaff26 | 2026-09-08T19:04 | 28 | UNRESOLVED | FAILURE | BROKEN_ON_TRUNK×2, FLAKY_CROSS_PR×2, OWNED_BY_PR×9, REGRESSION_CLUSTER×15 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 9a51326 | 2026-09-08T23:26 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 3a7807c | 2026-09-09T00:08 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 3a7807c | 2026-09-09T00:11 | 5 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 3a7807c | 2026-09-09T00:22 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 3a7807c | 2026-09-09T00:37 | 28 | UNRESOLVED | ACTION_REQUIRED | INFRA×28 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 4ac6d33 | 2026-09-09T03:24 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 4ac6d33 | 2026-09-09T03:38 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 4ac6d33 | 2026-09-09T03:45 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 4ac6d33 | 2026-09-09T03:58 | 10 | UNRESOLVED | FAILURE | OWNED_BY_PR×3, REGRESSION×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | c6689b7 | 2026-09-09T05:16 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | c6689b7 | 2026-09-09T05:30 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | c6689b7 | 2026-09-09T05:41 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | dd255b9 | 2026-09-09T06:46 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 2a03125 | 2026-09-09T07:48 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 2a03125 | 2026-09-09T07:51 | 34 | UNRESOLVED | FAILURE | OWNED_BY_PR×25, REGRESSION×9 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 2a03125 | 2026-09-09T08:09 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 2a03125 | 2026-09-09T08:25 | 44 | UNRESOLVED | FAILURE | OWNED_BY_PR×25, REGRESSION×7, REGRESSION_CLUSTER×12 | no |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-detox-ios | 20d83ae | 2026-09-09T11:45 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 44d0029 | 2026-09-09T12:06 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 44d0029 | 2026-09-09T12:22 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 44d0029 | 2026-09-09T13:01 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 44d0029 | 2026-09-09T13:20 | 7 | UNRESOLVED | FAILURE | OWNED_BY_PR×4, REGRESSION×3 | no |
| mattermost-mobile | [10140](https://github.com/mattermost/mattermost-mobile/pull/10140) | mobile-pr-detox-ios | 06d6e02 | 2026-09-09T14:05 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 1b9386c | 2026-09-09T14:15 | 2 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 1b9386c | 2026-09-09T14:29 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 1b9386c | 2026-09-09T14:39 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 1b9386c | 2026-09-09T14:42 | 26 | UNRESOLVED | FAILURE | OWNED_BY_PR×26 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | b10a083 | 2026-09-09T17:18 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | b10a083 | 2026-09-09T18:19 | 19 | UNRESOLVED | FAILURE | OWNED_BY_PR×9, REGRESSION×2, REGRESSION_CLUSTER×8 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 241a0ee | 2026-09-09T18:39 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 241a0ee | 2026-09-09T18:44 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-ios-e2e | 241a0ee | 2026-09-09T18:56 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 241a0ee | 2026-09-09T19:12 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [9898](https://github.com/mattermost/mattermost-mobile/pull/9898) | mobile-pr-detox-android | f3592ea | 2026-09-10T00:37 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [9898](https://github.com/mattermost/mattermost-mobile/pull/9898) | mobile-pr-detox-ios | f3592ea | 2026-09-10T00:53 | 4 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 6a13513 | 2026-09-10T05:46 | 3 | UNRESOLVED | FAILURE | OWNED_BY_PR×3 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | a5c43b8 | 2026-09-10T06:26 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | a5c43b8 | 2026-09-10T06:29 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | a5c43b8 | 2026-09-10T07:02 | 2 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-maestro-android-e2e | 4545c89 | 2026-09-10T08:37 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-android | 4545c89 | 2026-09-10T08:55 | 25 | UNRESOLVED | FAILURE | OWNED_BY_PR×25 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 4545c89 | 2026-09-10T08:59 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost-mobile | [10142](https://github.com/mattermost/mattermost-mobile/pull/10142) | mobile-pr-maestro-android-e2e | 4487760 | 2026-09-10T12:23 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 7b3c626 | 2026-09-10T13:28 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10142](https://github.com/mattermost/mattermost-mobile/pull/10142) | mobile-pr-detox-ios | 4487760 | 2026-09-10T14:05 | 3 | UNRESOLVED | FAILURE | INFRA×1, INSUFFICIENT_DATA×1, REGRESSION×1 | no |
| mattermost-mobile | [10125](https://github.com/mattermost/mattermost-mobile/pull/10125) | mobile-pr-detox-ios | 0eff784 | 2026-09-10T15:37 | 9 | UNRESOLVED | FAILURE | REGRESSION_CLUSTER×9 | no |
| mattermost-mobile | [10143](https://github.com/mattermost/mattermost-mobile/pull/10143) | mobile-pr-detox-android | 7d731b1 | 2026-09-10T19:46 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10143](https://github.com/mattermost/mattermost-mobile/pull/10143) | mobile-pr-detox-ios | 7d731b1 | 2026-09-10T20:20 | 6 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, REGRESSION×1, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | ff00b42 | 2026-09-11T13:16 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | 8d250bd | 2026-09-11T14:12 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10149](https://github.com/mattermost/mattermost-mobile/pull/10149) | mobile-pr-maestro-ios-e2e | 027b993 | 2026-09-11T14:43 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10151](https://github.com/mattermost/mattermost-mobile/pull/10151) | mobile-pr-maestro-android-e2e | 9836abe | 2026-09-11T16:04 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost-mobile | [10151](https://github.com/mattermost/mattermost-mobile/pull/10151) | mobile-pr-detox-android | 7380638 | 2026-09-11T17:21 | 10 | UNRESOLVED | FAILURE | REGRESSION×10 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 57a15d3 | 2026-09-11T18:04 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 57a15d3 | 2026-09-12T11:09 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | 9f378b3 | 2026-09-12T14:15 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 9f378b3 | 2026-09-12T14:31 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 9f378b3 | 2026-09-12T14:40 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10153](https://github.com/mattermost/mattermost-mobile/pull/10153) | mobile-pr-detox-ios | 2faf9c0 | 2026-09-13T19:25 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10154](https://github.com/mattermost/mattermost-mobile/pull/10154) | mobile-pr-maestro-android-e2e | c03adbf | 2026-09-14T13:43 | 1 | UNRESOLVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10155](https://github.com/mattermost/mattermost-mobile/pull/10155) | mobile-pr-detox-android | 83ac3c5 | 2026-09-14T14:10 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10154](https://github.com/mattermost/mattermost-mobile/pull/10154) | mobile-pr-maestro-android-e2e | e52b564 | 2026-09-14T14:12 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10155](https://github.com/mattermost/mattermost-mobile/pull/10155) | mobile-pr-detox-ios | 83ac3c5 | 2026-09-14T14:13 | 2 | UNRESOLVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 670bfb1 | 2026-09-14T14:40 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 670bfb1 | 2026-09-14T15:15 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10154](https://github.com/mattermost/mattermost-mobile/pull/10154) | mobile-pr-maestro-ios-e2e | e52b564 | 2026-09-14T15:41 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10154](https://github.com/mattermost/mattermost-mobile/pull/10154) | mobile-pr-detox-ios | e52b564 | 2026-09-14T15:53 | 8 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, OWNED_BY_PR×5, REGRESSION×2 | no |
| mattermost-mobile | [10139](https://github.com/mattermost/mattermost-mobile/pull/10139) | mobile-pr-maestro-android-e2e | 0e89cda | 2026-09-14T21:26 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10139](https://github.com/mattermost/mattermost-mobile/pull/10139) | mobile-pr-detox-ios | 0e89cda | 2026-09-14T23:03 | 2 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×2 | yes |
| mattermost-mobile | [10139](https://github.com/mattermost/mattermost-mobile/pull/10139) | mobile-pr-maestro-android-e2e | 7727354 | 2026-09-15T10:35 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10139](https://github.com/mattermost/mattermost-mobile/pull/10139) | mobile-pr-detox-ios | 7727354 | 2026-09-15T11:42 | 6 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×4, FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 2ed06ec | 2026-09-15T12:50 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 2ed06ec | 2026-09-15T14:08 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10149](https://github.com/mattermost/mattermost-mobile/pull/10149) | mobile-pr-maestro-android-e2e | 3307f5a | 2026-09-15T15:44 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | f1d77f9 | 2026-09-15T18:23 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | f1d77f9 | 2026-09-15T19:10 | 12 | UNRESOLVED | FAILURE | OWNED_BY_PR×12 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-maestro-android-e2e | 73648ee | 2026-09-16T11:04 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-android | 73648ee | 2026-09-16T11:39 | 4 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×3 | no |
| mattermost-mobile | [10113](https://github.com/mattermost/mattermost-mobile/pull/10113) | mobile-pr-detox-ios | 73648ee | 2026-09-16T11:46 | 4 | UNRESOLVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION_CLUSTER×3 | no |
| mattermost-mobile | [10140](https://github.com/mattermost/mattermost-mobile/pull/10140) | mobile-pr-detox-android | 13b5821 | 2026-09-16T13:52 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10140](https://github.com/mattermost/mattermost-mobile/pull/10140) | mobile-pr-maestro-ios-e2e | 13b5821 | 2026-09-16T14:01 | 1 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10140](https://github.com/mattermost/mattermost-mobile/pull/10140) | mobile-pr-detox-ios | 13b5821 | 2026-09-16T14:16 | 4 | UNRESOLVED | SUCCESS | FLAKY_CONFIRMED×3, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ipad | 13af470 | 2026-08-18T04:04 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-maestro-ios-e2e | 27fced3 | 2026-08-18T21:33 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-detox-android | 27fced3 | 2026-08-18T22:32 | 20 | WAIVED | FAILURE | FLAKY_CONFIRMED×2, FLAKY_CROSS_PR×1, REGRESSION×13, REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10080](https://github.com/mattermost/mattermost-mobile/pull/10080) | mobile-pr-detox-android | 038982c | 2026-08-20T11:27 | 2 | WAIVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10080](https://github.com/mattermost/mattermost-mobile/pull/10080) | mobile-pr-maestro-ios-e2e | 038982c | 2026-08-20T11:33 | 1 | WAIVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10080](https://github.com/mattermost/mattermost-mobile/pull/10080) | mobile-pr-detox-ios | 038982c | 2026-08-20T11:58 | 9 | WAIVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×8 | yes |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-ios | 94c1492 | 2026-08-20T12:48 | 4 | WAIVED | SUCCESS | BROKEN_ON_TRUNK×1, FLAKY_CROSS_PR×3 | yes |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-android | 94c1492 | 2026-08-20T13:03 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-android | 1fd839f | 2026-08-20T19:15 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-maestro-android-e2e | 1fd839f | 2026-08-20T19:25 | 7 | WAIVED | SUCCESS | FLAKY_CROSS_PR×7 | yes |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-maestro-ios-e2e | 1fd839f | 2026-08-20T19:40 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-ipad | 1fd839f | 2026-08-20T19:46 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-ios | 1fd839f | 2026-08-20T19:52 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-android-e2e | 574c7af | 2026-08-21T18:32 | 2 | WAIVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10056](https://github.com/mattermost/mattermost-mobile/pull/10056) | mobile-pr-detox-ios | 85bdbf0 | 2026-08-24T18:16 | 1 | WAIVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ipad | 4dc998c | 2026-08-25T18:44 | 5 | WAIVED | SUCCESS | FLAKY_CONFIRMED×5 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ipad | 8f89593 | 2026-08-25T22:05 | 5 | WAIVED | FAILURE | REGRESSION_CLUSTER×5 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-android | 8f89593 | 2026-08-25T22:20 | 21 | WAIVED | FAILURE | FLAKY_CROSS_PR×2, REGRESSION×3, REGRESSION_CLUSTER×16 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-maestro-ios-e2e | 7bd512d | 2026-08-26T15:31 | 1 | WAIVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-maestro-android-e2e | a4e8b59 | 2026-08-26T15:37 | 1 | WAIVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-android | 7bd512d | 2026-08-26T15:43 | 2 | WAIVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-maestro-ios-e2e | a4e8b59 | 2026-08-26T16:21 | 1 | WAIVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-maestro-ios-e2e | 0332b4e | 2026-08-26T19:17 | 2 | WAIVED | FAILURE | OWNED_BY_PR×2 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-android | 0332b4e | 2026-08-26T19:39 | 168 | WAIVED | FAILURE | FLAKY_CROSS_PR×1, INFRA×1, OWNED_BY_PR×163, REGRESSION×3 | no |
| mattermost-mobile | [10023](https://github.com/mattermost/mattermost-mobile/pull/10023) | mobile-pr-detox-ios | fac0968 | 2026-08-26T22:53 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [9928](https://github.com/mattermost/mattermost-mobile/pull/9928) | mobile-pr-detox-ios | a3c9d59 | 2026-08-27T13:41 | 2 | WAIVED | SUCCESS | FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10101](https://github.com/mattermost/mattermost-mobile/pull/10101) | mobile-pr-detox-ios | b51fe29 | 2026-08-27T14:36 | 6 | WAIVED | FAILURE | FLAKY_CONFIRMED×2, REGRESSION×4 | no |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-ios | 3e67acd | 2026-08-27T23:28 | 10 | WAIVED | SUCCESS | FLAKY_CONFIRMED×3, FLAKY_CROSS_PR×7 | yes |
| mattermost-mobile | [10093](https://github.com/mattermost/mattermost-mobile/pull/10093) | mobile-pr-detox-android | 3e67acd | 2026-08-27T23:54 | 8 | WAIVED | SUCCESS | FLAKY_CONFIRMED×8 | yes |
| mattermost-mobile | [10102](https://github.com/mattermost/mattermost-mobile/pull/10102) | mobile-pr-detox-ios | e43f0e1 | 2026-08-28T14:30 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10105](https://github.com/mattermost/mattermost-mobile/pull/10105) | mobile-pr-maestro-ios-e2e | d6a8bc4 | 2026-08-31T01:50 | 1 | WAIVED | ACTION_REQUIRED | INFRA×1 | no |
| mattermost-mobile | [10105](https://github.com/mattermost/mattermost-mobile/pull/10105) | mobile-pr-detox-ios | 62c3ae5 | 2026-08-31T02:58 | 1 | WAIVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-android-e2e | f0ed204 | 2026-09-01T13:48 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-maestro-ios-e2e | f0ed204 | 2026-09-01T14:42 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10017](https://github.com/mattermost/mattermost-mobile/pull/10017) | mobile-pr-detox-ios | f0ed204 | 2026-09-01T15:04 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10102](https://github.com/mattermost/mattermost-mobile/pull/10102) | mobile-pr-maestro-ios-e2e | 7378f65 | 2026-09-01T17:27 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10110](https://github.com/mattermost/mattermost-mobile/pull/10110) | mobile-pr-detox-ios | 71b8a38 | 2026-09-01T19:12 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CONFIRMED×1 | no |
| mattermost-mobile | [10102](https://github.com/mattermost/mattermost-mobile/pull/10102) | mobile-pr-detox-ios | 4a4c082 | 2026-09-01T20:20 | 5 | WAIVED | FAILURE | FLAKY_CONFIRMED×1, REGRESSION×4 | no |
| mattermost-mobile | [10117](https://github.com/mattermost/mattermost-mobile/pull/10117) | mobile-pr-maestro-ios-e2e | 2bd028d | 2026-09-03T09:50 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10117](https://github.com/mattermost/mattermost-mobile/pull/10117) | mobile-pr-detox-ios | 2bd028d | 2026-09-03T10:26 | 1 | WAIVED | ACTION_REQUIRED | FLAKY_CROSS_PR×1 | no |
| mattermost-mobile | [10111](https://github.com/mattermost/mattermost-mobile/pull/10111) | mobile-pr-detox-ios | d053d64 | 2026-09-04T18:07 | 4 | WAIVED | SUCCESS | BROKEN_ON_TRUNK×2, FLAKY_CONFIRMED×1, FLAKY_CROSS_PR×1 | yes |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-maestro-android-e2e | ecf1f3c | 2026-09-07T07:24 | 1 | WAIVED | SUCCESS | BROKEN_ON_TRUNK×1 | yes |
| mattermost-mobile | [10142](https://github.com/mattermost/mattermost-mobile/pull/10142) | mobile-pr-detox-android | 4487760 | 2026-09-10T13:35 | 4 | WAIVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×1, REGRESSION×1 | no |
| mattermost-mobile | [10153](https://github.com/mattermost/mattermost-mobile/pull/10153) | mobile-pr-detox-ios | 158e70a | 2026-09-13T23:22 | 3 | WAIVED | FAILURE | INFRA×1, REGRESSION×2 | no |
| mattermost-mobile | [10074](https://github.com/mattermost/mattermost-mobile/pull/10074) | mobile-pr-detox-ios | 69010c7 | 2026-09-14T10:12 | 9 | WAIVED | FAILURE | FLAKY_CONFIRMED×2, INFRA×1, REGRESSION_CLUSTER×6 | no |
| mattermost-mobile | [10142](https://github.com/mattermost/mattermost-mobile/pull/10142) | mobile-pr-maestro-android-e2e | d2712c7 | 2026-09-14T10:24 | 1 | WAIVED | SUCCESS | FLAKY_CONFIRMED×1 | yes |
| mattermost-mobile | [10142](https://github.com/mattermost/mattermost-mobile/pull/10142) | mobile-pr-detox-ios | d2712c7 | 2026-09-14T11:26 | 4 | WAIVED | ACTION_REQUIRED | FLAKY_CONFIRMED×2, FLAKY_CROSS_PR×1, INFRA×1 | no |
| mattermost-mobile | [10158](https://github.com/mattermost/mattermost-mobile/pull/10158) | mobile-pr-maestro-android-e2e | a0052a7 | 2026-09-14T21:05 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |
| mattermost-mobile | [10158](https://github.com/mattermost/mattermost-mobile/pull/10158) | mobile-pr-detox-ipad | a28287a | 2026-09-14T22:39 | 4 | WAIVED | FAILURE | REGRESSION_CLUSTER×4 | no |
| mattermost-mobile | [10158](https://github.com/mattermost/mattermost-mobile/pull/10158) | mobile-pr-maestro-ios-e2e | a28287a | 2026-09-14T22:48 | 1 | WAIVED | FAILURE | REGRESSION×1 | no |

Green runs: 21; engine disagreed on 0: 

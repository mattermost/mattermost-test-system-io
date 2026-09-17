# Triage backtest: mattermost/mattermost since 2026-08-18T00:00:00Z

Runs: 200 PR runs, 75 PRs. Verdicts computed with `as_of` = run end + 5 min.

## Ground truth vs verdict

| Ground truth | Verdict | Runs |
|---|---|---|
| FIXED_BY_AUTHOR | FAILURE | 13 |
| FIXED_BY_AUTHOR | SUCCESS | 7 |
| GREEN | SUCCESS | 78 |
| RERUN_PASSED | FAILURE | 4 |
| RERUN_PASSED | SUCCESS | 1 |
| UNRESOLVED | FAILURE | 78 |
| UNRESOLVED | SUCCESS | 19 |

Agreement on scoreable failing runs (WAIVED/RERUN_PASSED should unblock, FIXED_BY_AUTHOR should block): 14/25


False exonerations (author fixed it, engine would have unblocked): 7

## Failing runs

| Repo | PR | Lane | SHA | Run at | Failed | Ground truth | Verdict | Classes | Would unblock |
|---|---|---|---|---|---|---|---|---|---|
| mattermost | [38418](https://github.com/mattermost/mattermost/pull/38418) | playwright-full-enterprise | f2230b8 | 2026-09-09T09:22 | 1 | FIXED_BY_AUTHOR | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [37851](https://github.com/mattermost/mattermost/pull/37851) | playwright-full-enterprise-upgrade-from-release-11.7-esr | e8caf07 | 2026-09-10T18:19 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [37851](https://github.com/mattermost/mattermost/pull/37851) | playwright-full-enterprise-upgrade-from-release-11.9 | 970911b | 2026-09-11T18:46 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38509](https://github.com/mattermost/mattermost/pull/38509) | playwright-full-enterprise | 94492f0 | 2026-09-13T06:34 | 1 | FIXED_BY_AUTHOR | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise-upgrade-from-release-11.9 | 7c9a03a | 2026-09-14T00:11 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise-upgrade-from-release-11.9 | b969561 | 2026-09-14T01:57 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38516](https://github.com/mattermost/mattermost/pull/38516) | playwright-full-enterprise | 2859a44 | 2026-09-14T02:37 | 1 | FIXED_BY_AUTHOR | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38472](https://github.com/mattermost/mattermost/pull/38472) | playwright-full-enterprise | c1a7ce8 | 2026-09-14T14:27 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [37851](https://github.com/mattermost/mattermost/pull/37851) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 7ed7b21 | 2026-09-14T16:52 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [37851](https://github.com/mattermost/mattermost/pull/37851) | playwright-full-enterprise-upgrade-from-release-11.10 | 7ed7b21 | 2026-09-14T16:52 | 1 | FIXED_BY_AUTHOR | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38535](https://github.com/mattermost/mattermost/pull/38535) | playwright-full-enterprise | fa0c8f5 | 2026-09-14T17:48 | 1 | FIXED_BY_AUTHOR | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38535](https://github.com/mattermost/mattermost/pull/38535) | playwright-full-enterprise | 4efca41 | 2026-09-14T18:50 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38541](https://github.com/mattermost/mattermost/pull/38541) | playwright-full-enterprise | b85b968 | 2026-09-15T04:41 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38541](https://github.com/mattermost/mattermost/pull/38541) | playwright-full-enterprise | 005c810 | 2026-09-15T08:43 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38472](https://github.com/mattermost/mattermost/pull/38472) | playwright-full-enterprise | a560e7e | 2026-09-15T11:17 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38224](https://github.com/mattermost/mattermost/pull/38224) | playwright-full-enterprise | 02bb666 | 2026-09-15T19:05 | 1 | FIXED_BY_AUTHOR | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38557](https://github.com/mattermost/mattermost/pull/38557) | playwright-full-enterprise | 30627f9 | 2026-09-15T20:12 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38559](https://github.com/mattermost/mattermost/pull/38559) | playwright-full-enterprise | 3e870ee | 2026-09-15T21:12 | 1 | FIXED_BY_AUTHOR | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38561](https://github.com/mattermost/mattermost/pull/38561) | playwright-full-enterprise | 79639a0 | 2026-09-16T07:20 | 4 | FIXED_BY_AUTHOR | FAILURE | FLAKY_SUSPICIOUS×1, OWNED_BY_PR×3 | no |
| mattermost | [38561](https://github.com/mattermost/mattermost/pull/38561) | playwright-full-enterprise | bd19aa8 | 2026-09-16T08:41 | 4 | FIXED_BY_AUTHOR | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×3 | no |
| mattermost | [38439](https://github.com/mattermost/mattermost/pull/38439) | playwright-full-enterprise | 790c19f | 2026-09-10T07:14 | 1 | RERUN_PASSED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38460](https://github.com/mattermost/mattermost/pull/38460) | playwright-full-enterprise | 8ad5356 | 2026-09-10T21:12 | 1 | RERUN_PASSED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38451](https://github.com/mattermost/mattermost/pull/38451) | playwright-full-enterprise | b827957 | 2026-09-11T13:51 | 1 | RERUN_PASSED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38509](https://github.com/mattermost/mattermost/pull/38509) | playwright-full-enterprise | 6a2cac4 | 2026-09-15T04:32 | 1 | RERUN_PASSED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38550](https://github.com/mattermost/mattermost/pull/38550) | playwright-full-enterprise | 58a90a4 | 2026-09-15T10:14 | 1 | RERUN_PASSED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38074](https://github.com/mattermost/mattermost/pull/38074) | playwright-full-enterprise | 13d6fed | 2026-09-09T02:26 | 19 | UNRESOLVED | FAILURE | OWNED_BY_PR×15, REGRESSION×4 | no |
| mattermost | [38016](https://github.com/mattermost/mattermost/pull/38016) | playwright-full-enterprise | af9ab78 | 2026-09-09T02:26 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38415](https://github.com/mattermost/mattermost/pull/38415) | playwright-full-enterprise | 1e5a730 | 2026-09-09T03:25 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [37455](https://github.com/mattermost/mattermost/pull/37455) | playwright-full-enterprise | ffba00e | 2026-09-09T03:39 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [37455](https://github.com/mattermost/mattermost/pull/37455) | playwright-full-enterprise | 1127115 | 2026-09-09T04:35 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [37455](https://github.com/mattermost/mattermost/pull/37455) | playwright-full-enterprise | 34b7166 | 2026-09-09T05:06 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [37455](https://github.com/mattermost/mattermost/pull/37455) | playwright-full-enterprise | 34b7166 | 2026-09-09T05:17 | 3 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, OWNED_BY_PR×2 | no |
| mattermost | [37455](https://github.com/mattermost/mattermost/pull/37455) | playwright-full-enterprise | d8c8366 | 2026-09-09T06:23 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [37604](https://github.com/mattermost/mattermost/pull/37604) | playwright-full-enterprise | 33d19c4 | 2026-09-09T11:31 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38425](https://github.com/mattermost/mattermost/pull/38425) | playwright-full-enterprise | dcd024a | 2026-09-09T12:05 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [37604](https://github.com/mattermost/mattermost/pull/37604) | playwright-full-enterprise | 15c4c2d | 2026-09-09T12:09 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38311](https://github.com/mattermost/mattermost/pull/38311) | playwright-full-enterprise | 564757d | 2026-09-09T21:36 | 25 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×1, FLAKY_SUSPICIOUS×1, OWNED_BY_PR×12, REGRESSION×11 | no |
| mattermost | [38409](https://github.com/mattermost/mattermost/pull/38409) | playwright-full-enterprise | 333d41e | 2026-09-09T23:23 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38434](https://github.com/mattermost/mattermost/pull/38434) | playwright-full-enterprise | 2aefef3 | 2026-09-10T02:55 | 6 | UNRESOLVED | FAILURE | NEW_TEST×1, REGRESSION×5 | no |
| mattermost | [38430](https://github.com/mattermost/mattermost/pull/38430) | playwright-full-enterprise | 2fd8760 | 2026-09-10T02:55 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38432](https://github.com/mattermost/mattermost/pull/38432) | playwright-full-enterprise | 1c6afb2 | 2026-09-10T02:56 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [38431](https://github.com/mattermost/mattermost/pull/38431) | playwright-full-enterprise | a3ba481 | 2026-09-10T02:57 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38433](https://github.com/mattermost/mattermost/pull/38433) | playwright-full-enterprise-upgrade-from-release-11.9 | 012b63a | 2026-09-10T02:57 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38431](https://github.com/mattermost/mattermost/pull/38431) | playwright-full-enterprise-upgrade-from-release-11.8 | a3ba481 | 2026-09-10T02:57 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38431](https://github.com/mattermost/mattermost/pull/38431) | playwright-full-enterprise-upgrade-from-release-11.9 | a3ba481 | 2026-09-10T02:57 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×1 | no |
| mattermost | [38431](https://github.com/mattermost/mattermost/pull/38431) | playwright-full-enterprise-upgrade-from-release-11.7-esr | a3ba481 | 2026-09-10T02:58 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38432](https://github.com/mattermost/mattermost/pull/38432) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 4beed3f | 2026-09-10T03:25 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38431](https://github.com/mattermost/mattermost/pull/38431) | playwright-full-enterprise | afc7d58 | 2026-09-10T03:50 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38435](https://github.com/mattermost/mattermost/pull/38435) | playwright-full-enterprise | 87ef767 | 2026-09-10T05:26 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38435](https://github.com/mattermost/mattermost/pull/38435) | playwright-full-enterprise | 87ef767 | 2026-09-10T05:39 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [38444](https://github.com/mattermost/mattermost/pull/38444) | playwright-full-enterprise | 629ed69 | 2026-09-10T11:14 | 3 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, REGRESSION×1 | no |
| mattermost | [38311](https://github.com/mattermost/mattermost/pull/38311) | playwright-full-enterprise | 1018b1b | 2026-09-10T12:58 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise | d710b74 | 2026-09-10T16:04 | 2 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×2 | yes |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise-upgrade-from-release-11.7-esr | d710b74 | 2026-09-10T16:04 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38409](https://github.com/mattermost/mattermost/pull/38409) | playwright-full-enterprise | 858447f | 2026-09-10T17:57 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38459](https://github.com/mattermost/mattermost/pull/38459) | playwright-full-enterprise | 5f47bb4 | 2026-09-10T21:11 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38464](https://github.com/mattermost/mattermost/pull/38464) | playwright-full-enterprise | a97708f | 2026-09-11T06:56 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38464](https://github.com/mattermost/mattermost/pull/38464) | playwright-full-enterprise | ec43ff9 | 2026-09-11T07:27 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38444](https://github.com/mattermost/mattermost/pull/38444) | playwright-full-enterprise | 7b4ea03 | 2026-09-11T07:31 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38475](https://github.com/mattermost/mattermost/pull/38475) | playwright-full-enterprise | 470ac2e | 2026-09-11T10:42 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38475](https://github.com/mattermost/mattermost/pull/38475) | playwright-full-fips | 4685493 | 2026-09-11T15:30 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [26966](https://github.com/mattermost/mattermost/pull/26966) | playwright-full-enterprise | dc94876 | 2026-09-11T15:58 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [37599](https://github.com/mattermost/mattermost/pull/37599) | playwright-full-enterprise | bbf7788 | 2026-09-11T16:04 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [26966](https://github.com/mattermost/mattermost/pull/26966) | playwright-full-enterprise | 4fcd892 | 2026-09-11T16:31 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38505](https://github.com/mattermost/mattermost/pull/38505) | playwright-full-enterprise | bd66a65 | 2026-09-13T06:13 | 9 | UNRESOLVED | FAILURE | REGRESSION×1, REGRESSION_CLUSTER×8 | no |
| mattermost | [38505](https://github.com/mattermost/mattermost/pull/38505) | playwright-full-enterprise | 81731ee | 2026-09-13T07:22 | 15 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION×2, REGRESSION_CLUSTER×12 | no |
| mattermost | [38505](https://github.com/mattermost/mattermost/pull/38505) | playwright-full-enterprise | 875ecac | 2026-09-13T08:21 | 9 | UNRESOLVED | FAILURE | OWNED_BY_PR×1, REGRESSION_CLUSTER×8 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 7c9a03a | 2026-09-14T00:11 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise | b969561 | 2026-09-14T01:57 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | c7a0476 | 2026-09-14T02:34 | 9 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×4, OWNED_BY_PR×1, REGRESSION×2 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | bccbb09 | 2026-09-14T02:40 | 10 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | 2964e3a | 2026-09-14T03:29 | 16 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×4, REGRESSION×10 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | 065afad | 2026-09-14T03:29 | 13 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, FLAKY_SUSPICIOUS×1, INSUFFICIENT_DATA×4, OWNED_BY_PR×1, REGRESSION×5 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | e16dad2 | 2026-09-14T04:14 | 10 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, FLAKY_SUSPICIOUS×1, INSUFFICIENT_DATA×4, REGRESSION×3 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | ab67c9d | 2026-09-14T04:15 | 8 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×4, REGRESSION×2 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | 06f889e | 2026-09-14T05:26 | 11 | UNRESOLVED | FAILURE | FLAKY_CROSS_PR×2, INSUFFICIENT_DATA×4, REGRESSION×5 | no |
| mattermost | [38516](https://github.com/mattermost/mattermost/pull/38516) | playwright-full-enterprise | 045fbbc | 2026-09-14T06:38 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | 68695bb | 2026-09-14T06:40 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | 15d7643 | 2026-09-14T06:40 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38529](https://github.com/mattermost/mattermost/pull/38529) | playwright-full-enterprise | 087dfc7 | 2026-09-14T10:24 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [35569](https://github.com/mattermost/mattermost/pull/35569) | playwright-full-enterprise | 01e8aa5 | 2026-09-14T11:37 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38485](https://github.com/mattermost/mattermost/pull/38485) | playwright-full-enterprise | 0cec63d | 2026-09-14T13:06 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [37314](https://github.com/mattermost/mattermost/pull/37314) | playwright-full-enterprise-upgrade-from-release-11.7-esr | d14c64c | 2026-09-14T13:59 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38475](https://github.com/mattermost/mattermost/pull/38475) | playwright-full-fips | 7fb10ff | 2026-09-14T15:51 | 1 | UNRESOLVED | FAILURE | REGRESSION×1 | no |
| mattermost | [38481](https://github.com/mattermost/mattermost/pull/38481) | playwright-full-enterprise | 9273e63 | 2026-09-14T17:34 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38473](https://github.com/mattermost/mattermost/pull/38473) | playwright-full-enterprise | 7e1d3b4 | 2026-09-14T17:35 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | df3d5f9 | 2026-09-14T22:27 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.7-esr | df3d5f9 | 2026-09-14T22:27 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.10 | df3d5f9 | 2026-09-14T22:27 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.9 | df3d5f9 | 2026-09-14T22:27 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | ff882c2 | 2026-09-14T23:47 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.7-esr | ff882c2 | 2026-09-14T23:48 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.9 | ff882c2 | 2026-09-14T23:48 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.10 | ff882c2 | 2026-09-14T23:48 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | c141b12 | 2026-09-15T00:53 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.10 | c141b12 | 2026-09-15T00:53 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.9 | c141b12 | 2026-09-15T00:53 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | 96d3f9d | 2026-09-15T01:48 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.9 | 96d3f9d | 2026-09-15T01:48 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 96d3f9d | 2026-09-15T01:48 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | 8f8c8ad | 2026-09-15T02:39 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.10 | 8f8c8ad | 2026-09-15T02:40 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.9 | 8f8c8ad | 2026-09-15T02:40 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 8f8c8ad | 2026-09-15T02:40 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1, OWNED_BY_PR×1 | no |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise | 6a97567 | 2026-09-15T04:30 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38537](https://github.com/mattermost/mattermost/pull/38537) | playwright-full-enterprise-upgrade-from-release-11.7-esr | 6a97567 | 2026-09-15T04:30 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | 92b9600 | 2026-09-15T04:47 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | 8bff4c1 | 2026-09-15T04:47 | 14 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×10 | no |
| mattermost | [38518](https://github.com/mattermost/mattermost/pull/38518) | playwright-full-enterprise | 03d4e64 | 2026-09-15T05:43 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38516](https://github.com/mattermost/mattermost/pull/38516) | playwright-full-enterprise | e4388c8 | 2026-09-15T05:51 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | fa28977 | 2026-09-15T05:56 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | 55fcb9c | 2026-09-15T05:58 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38526](https://github.com/mattermost/mattermost/pull/38526) | playwright-full-enterprise | bc7b68d | 2026-09-15T10:40 | 1 | UNRESOLVED | FAILURE | OWNED_BY_PR×1 | no |
| mattermost | [38129](https://github.com/mattermost/mattermost/pull/38129) | playwright-full-enterprise-upgrade-from-release-11.7-esr | b917cfb | 2026-09-15T14:27 | 2 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×2 | no |
| mattermost | [38555](https://github.com/mattermost/mattermost/pull/38555) | playwright-full-enterprise | 0d65e03 | 2026-09-15T17:29 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |
| mattermost | [38558](https://github.com/mattermost/mattermost/pull/38558) | playwright-full-enterprise | 1436e61 | 2026-09-15T20:23 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38519](https://github.com/mattermost/mattermost/pull/38519) | playwright-full-enterprise | 85a93b6 | 2026-09-16T05:37 | 8 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×4 | no |
| mattermost | [38520](https://github.com/mattermost/mattermost/pull/38520) | playwright-full-enterprise | c6fee2a | 2026-09-16T05:37 | 10 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×4, REGRESSION×6 | no |
| mattermost | [38535](https://github.com/mattermost/mattermost/pull/38535) | playwright-full-enterprise | 26028e7 | 2026-09-16T10:33 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38119](https://github.com/mattermost/mattermost/pull/38119) | playwright-full-enterprise | 74b917d | 2026-09-16T14:25 | 1 | UNRESOLVED | FAILURE | FLAKY_SUSPICIOUS×1 | no |
| mattermost | [38129](https://github.com/mattermost/mattermost/pull/38129) | playwright-full-enterprise-upgrade-from-release-11.7-esr | acfedfd | 2026-09-16T14:28 | 1 | UNRESOLVED | FAILURE | INSUFFICIENT_DATA×1 | no |
| mattermost | [37851](https://github.com/mattermost/mattermost/pull/37851) | playwright-full-enterprise | 4ad18fe | 2026-09-16T17:35 | 1 | UNRESOLVED | SUCCESS | FLAKY_CROSS_PR×1 | yes |

Green runs: 78; engine disagreed on 0: 

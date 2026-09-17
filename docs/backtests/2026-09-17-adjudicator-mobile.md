# Adjudicator evaluation — mattermost-mobile, 343 labeled PR runs (2026-09-17)

Same 640 evidence packs (1,297 borderline findings) sent to two models via the
repository's CI key (`triage-adjudicate-eval` workflow, run ids 35210335759 and
35213057228). Decision matrix as in `docs/triage.md` (unblock ≥ 0.85 with a
cross-PR or diff-hunk citation; veto ≥ 0.9 with a hunk citation).

| Ground truth (refined) | runs | engine alone green | + Sonnet 5 green | + Haiku 4.5 green |
|---|---|---|---|---|
| LIKELY_REGRESSION (failing test unique to the PR, later fixed) | 143 | 3 | 3 | 3 |
| RECURRING_ELSEWHERE (same tests failed on other PRs, later passed) | 157 | 49 | 69 | 67 |
| WAIVED by a human (`E2E/Verified`) | 32 | 12 | 18 | 19 |
| RERUN_PASSED (same commit passed on rerun) | 11 | 2 | 2 | 2 |

Model behaviour on the 628–642 adjudicated findings:

| | Sonnet 5 (`effort: medium`) | Haiku 4.5 |
|---|---|---|
| answered | 625/640 (15 hit the 2,000-token cap) | 639/640 |
| causes | flaky 524, caused_by_pr 83, bug_on_master 21 | flaky 559, caused_by_pr 52, bug_on_master 22, test_bug 9 |
| mean confidence (flaky / pr) | 0.82 / 0.74 | 0.84 / 0.80 |
| cites cross-PR or a hunk | 97% | 100% |
| agreement with each other | 549/627 causes (88%); Haiku says "flaky" where Sonnet says "caused by PR" 48 times, the reverse 18 |
| tokens (in / out) | 3.33M / 291k | 3.19M / 132k |
| cost at list price | ≈ $9.6 | ≈ $3.9 |

Threshold sweep (Sonnet, runs unblocked; citation required):

| unblock ≥ | LIKELY_REGRESSION | RECURRING_ELSEWHERE | WAIVED |
|---|---|---|---|
| 0.90 | 3 | 61 | 17 |
| 0.85 | 3 | 69 | 18 |
| 0.75 | 5 | 74 | 19 |
| 0.60 | 11 | 89 | 23 |

What still blocks recurring-elsewhere runs after adjudication (Sonnet): 50 runs
are `OWNED_BY_PR` on the failing spec itself (policy: the engine does not consult
the model when the PR edited the test); 20 are `REGRESSION` where the model said
flaky with confidence below 0.85; 5 where it agreed with the engine.

Conclusion: with the evidence-citation gate, Haiku 4.5 reaches the same
outcome as Sonnet 5 at 40% of the cost and with fewer truncated answers; the
gate, not the model, is what keeps false unblocks at zero. Haiku is the default
for the production adjudicator; Sonnet stays available per context.

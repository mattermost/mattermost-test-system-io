# Demo pack: automated E2E triage on real Mattermost history

Everything below is real: production `mattermost-mobile` runs from Aug 18 to
Sep 16, 2026 (imported with `tsioctl db import-remote`), the engine's verdicts on
them, and the second judge's actual answers (Claude Haiku 4.5, asked once from
CI and cached). Nothing is synthesized and no answer is re-asked.

The question the system answers on every red PR run is the one developers ask
first: **is this failure mine?** The deterministic engine answers from history
(trunk, other PRs, ownership); the second judge answers from the evidence pack
(the full error, the diff hunks, the recurrence list) and must cite what it used.
A borderline finding is unblocked only with confidence ≥ 0.85 **and** a
citation that a reviewer can check (recurrence on other PRs or a diff hunk).

## Running the demo

```sh
# Postgres with the imported history is the dev compose database on :6432.
cd apps/server && go run ./cmd/tsio           # serves the UI and API on :8080
```

| Page | URL |
| --- | --- |
| Test health (mobile, flaky) | http://localhost:8080/triage/health?repository=mattermost%2Fmattermost-mobile&classification=flaky |
| Verdict: PR 10158 unblocked | http://localhost:8080/triage/verdicts/01a0afe8-6b0a-76c1-b230-1ec797741d1b |
| Verdict: PR 10151 unblocked | http://localhost:8080/triage/verdicts/01a0afe8-6ccc-7c1d-bd9e-8d54912e02a4 |
| Verdict: PR 10142 (maestro) unblocked, human had waived | http://localhost:8080/triage/verdicts/01a0afe8-6f2b-7c02-8a1a-7003b9b07a56 |
| Verdict: PR 10113 quarantined flake, agreed | http://localhost:8080/triage/verdicts/01a0afe8-6dee-751b-9174-dbfffa534b1b |
| Verdict: PR 10142 (detox) stays red, 2 of 4 unblocked | http://localhost:8080/triage/verdicts/01a0afe8-783d-76c7-8419-ef99b70329c7 |
| Verdict: PR 10143 stays red, judge not confident enough | http://localhost:8080/triage/verdicts/01a0afe8-7c70-709e-828d-46f054c0496e |

The verdict ids above exist in the local database only (created 2026-09-17).
The PR comment the action would have posted for each is in `comments/`.

## The cases

### 1. PR 10158, maestro android: unblocked (engine said red)

*[MM-66560] Fix channel links ending in channel IDs*, 4 changed files.

- Failing test: `attach_logs_disabled_when_download_logs_off`. Error:
  `io.grpc.StatusRuntimeException: UNAVAILABLE` from the Maestro driver.
- Engine: `REGRESSION_AREA`, red. The file failed on this PR and never on trunk
  in the window (8 trunk runs, 0 failures), so by statistics alone it looks new.
- Second judge: **flaky environment, 85%, unblocked**, citing the error, the
  cross-PR list and the changed files. The same test failed on 6 other PRs in
  the previous 14 days (10149, 10151, 10125 several times) and passed 14 times
  elsewhere; the diff touches `app/actions/remote/channel.ts` and deep-link
  utilities, nothing near the Android driver.
- What history shows: the author's next run 40 minutes later, on a different
  commit that did not touch the test, passed. The PR merged.

### 2. PR 10151, maestro android: unblocked (engine said red)

*[MM-56533] Fix logged-out server deep links*, 8 changed files. Same test, same
gRPC `UNAVAILABLE` error, three days earlier.

- Engine: `REGRESSION_AREA`, red.
- Second judge: **flaky environment, 85%, unblocked**, citing error, cross-PR
  recurrence (PR 10125 across several commits) and the changed files (launch,
  native-intent, server screen; no driver code).
- What history shows: two later runs of the same PR passed with no change to
  the test. Cases 1 and 2 are the same infrastructure flake blocking two
  unrelated authors in one week.

### 3. PR 10142, maestro android: unblocked, and a human had waived it

*Handle the ABAC access_channel permission*, 21 changed files.

- Failing test: `attach_logs_toggle_on_surfaces_option`, gRPC `UNAVAILABLE`.
- Engine: `REGRESSION_AREA`, red (14 trunk runs, 0 failures).
- Second judge: **flaky environment, 92%, unblocked**. Same test failed on PR
  10125 eight times in the window and passed 38 times elsewhere; the diff is
  websocket event handling.
- What history shows: a maintainer applied `E2E/Verified` the next morning. The
  system reached the same conclusion, with the evidence written down, at the
  moment the run finished.

### 4. PR 10113, maestro android: auto-quarantined flake, judge agrees

*Mm 70299 channel labels display phase 1*, 48 changed files.

- Failing test: `leave_call`, `Assertion is false: id: tab_bar.home.tab is visible`.
- Engine: `QUARANTINED`, green. The health worker had auto-quarantined this test
  as flaky (3 failures in 14 trunk runs, no streak at head); it does not gate.
- Second judge: **bug on trunk, 95%, agreed**: fails on 12 other PRs in the
  window, passes on trunk head, the PR does not touch calls; "rebase to include
  the fix from master".
- What history shows: the same test flipped red and green across nine later
  runs of this PR while the author kept working. Without triage this author was
  blocked repeatedly by a test the whole repo was flaking on.

### 5. PR 10142, detox android: stays red, two of four findings unblocked

Same ABAC PR, the Detox lane, 4 failures in `browse_channels`, all Jest
360-second timeouts in setup hooks.

- Engine: one `REGRESSION_AREA`, three `REGRESSION_CLUSTER` (a shared failure
  signature never seen on trunk), red.
- Second judge: `MM-T4729_7` and `MM-T864_1` **unblocked at 95%** (same timeout
  on 6 other PRs, 34 passes elsewhere). `MM-T4729_5` and `MM-T4729_6` stayed
  **blocking at 72% and 75%**: little or no recurrence on other PRs, so the
  citation gate is not met and the engine's decision stands.
- What history shows: the author's next commit passed without touching the
  tests, so this was very likely flake too. The system chose red because it
  could not point at checkable evidence, which is the intended failure mode:
  it never unblocks on a hunch.

### 6. PR 10143, detox android: stays red, judge not confident

*MM-70688 Autolink phone numbers in posts as tel: links*, 11 changed files.

- Failing test: `Channel Settings - Copy Tests MM-T868_1`, a Detox visibility
  matcher mismatch.
- Engine: `REGRESSION_AREA`, red. No other PR failed this test in the window.
- Second judge: flaky environment at **72%**, below the 0.85 gate and with no
  cross-PR recurrence to cite, so **still blocking**.
- What history shows: the next run passed. A false block, resolved by the usual
  rerun. This is the cost side of the precision setting, and it is deliberate.

## What the developer sees

For each case, `comments/<case>.md` is the sticky PR comment the action posts:
the engine table (test, classification, trunk stats, reason), then a "Second
judge" section with the cause, the confidence, whether it changed the outcome,
and a one-paragraph explanation naming the evidence. The required status carries
the final verdict, so an unblocked run goes green without anyone applying a
label.

## Why the unblocks are trustworthy

Every unblock above rests on evidence a reviewer can open:

- the list of other PRs, with SHAs and dates, where the same test failed on the
  same days;
- the trunk pass count in the window;
- the PR's changed files, showing the failing area was not touched;
- and, after the fact, what happened next on the PR.

Across the whole labeled mobile set (343 runs), the engine plus the second judge
unblocked **0 of 143** runs that were later fixed by their authors and whose
failing tests were unique to the PR, while unblocking **67 of 157** runs whose
failures were recurring across other PRs and **19 of 32** runs a human had
waived by hand. Full tables: `docs/backtests/`.

## Caveats for the demo

- The local verdicts were computed on 2026-09-17 against the imported history,
  so their 14-day windows are anchored to that date, not to each run's own
  time. The backtest uses `as_of` replay for the honest historical view; the
  classifications above match it in outcome, with small differences in class
  names (for example a test that is quarantined now was `REGRESSION` then).
- Mode shows `shadow` because the local policy table has no repository row;
  in production the producer contexts run in `enforce`.
- Nothing here posted to GitHub. The end-to-end GitHub side (status, check run,
  comment) is exercised by pointing a producer branch at staging.

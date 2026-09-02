# Final capability report — does it do the four jobs?

**Date:** 2026-09-02
**Branch:** `feat/flakiness-management`
**Data:** production TSIO, `mattermost/mattermost` + Playwright, 2026-07-20 → 2026-09-02.
**Sample:** the same 41 labelled cases from round 4 (22 real bug, 18 flake, 1 unknowable).

---

## 1. The four answers

| # | Question | Answer | One sentence |
|---|---|---|---|
| 1 | Can a developer tell their PR introduced a bug that broke E2E? | **PARTIAL** | Keeps red 13/21 PR bugs, but waives 4 — including both cases of the hard sub-case (a real regression in a historically-flaky test). |
| 2 | Can a developer tell the failure is a flaky test, not their change? | **PARTIAL** | Waives 10/18 flakes, but keeps 3 PR flakes red (38% of PR flakes) — better than round 4's 50%, still above the 20% bar. |
| 3 | Can a developer tell the failure came from master and is a real bug? | **UNMEASURED** | The sample has only 1 master/release real bug; the "pre-existing vs. noise" split cannot be measured on n=1. |
| 4 | Can it watch master and open a fix PR? | **PARTIAL** | Flaky/test-bug loop is designed to open a stabilization PR but was not run end-to-end; product bugs are correctly routed (never fixed), with a CODEOWNERS wiring gap. |

---

## 2. A/B/C comparison

| Config | What runs | False greens | False reds | Model calls / 100 failures | Cost / 1,000 failures |
|---|---|---|---|---|---|
| **A — baseline** (`Suggest` only) | deterministic classifier | 2 | 9 | 0 | $0 |
| **B — as shipped** | `Suggest` → AI when `NeedsAI` → `canWaive` | 4 | 8 (3 on PR, 5 on release) | 88 | ~$44 |
| **C — inference always asks** | B + `NeedsAI:true` on inferred-waive branches | 4 | 9 | 100 | ~$50 (materially more on a red master) |

**False greens and false reds are reported separately, never averaged.**

- **False greens** (waived a real bug): A=2, B=4, C=4. **All three miss the bar of 0.**
- **False reds** (kept red pure noise): A=9, B=8, C=9. On PR branches only, B=3/8 flakes = 38% — still above 20%.

**C buys nothing.** It costs more (100 vs 88 calls, and on a red master every `MAIN_REGRESSION`/config-delta cluster now pays a model call) and does not move the false-green count. Do not adopt it.

**Cost note.** Model calls are per *cluster*, not per failure — 300 identical failures are one cluster, one call. The 88/100 figures assume the sample's ~1:1 cluster-to-failure ratio; production cost per 1,000 failures is lower wherever failures cluster.

---

## 3. The hard sub-case, on its own

**A real regression in a test that is historically flaky** (the ABAC shape): `MM-T5824` and `MM-T5820` on `pr-37732` (the testcontainers cherry-pick to release-11.8).

| | Count |
|---|---|
| Cases in sample | 2 |
| Pipeline kept red | 0 |
| Pipeline waived (false green) | **2** |

Both were waived. This is the one the design is weakest on, and it fails 2/2. The deterministic layer cannot distinguish "flaked again" from "broke for real" — `PR_REGRESSION` requires `s.Failed == 0` (`classify.go:147`), a condition a historically-flaky test can never meet, so the flaky-history branch (`Flips ≥ 3`) fires first and the AI is handed a `FLAKY_TEST` hint.

---

## 4. Every false green, in full

There are **four** in config B. Two are the ABAC shape; two are the AI overriding a correct `PR_REGRESSION` hint.

### False green 1 & 2 — `MM-T5824`, `MM-T5820` on `pr-37732` (the ABAC shape)

- **Evidence the pipeline saw:** `status=failed`, `runs=20`, `failed=8`, `flips=4`, `failure_rate=0.40`, `failing_since_commit=false`, `distinct_prs=0`. Error: `policy "Complex Policy …" should appear after search` (15s predicate timeout). PR diff: **30 files, all `.github/`** (CI-only).
- **What it concluded:** `Suggest` → `FLAKY_TEST` (0.8, `NeedsAI`). AI (simulated) → `FLAKY_TEST` (0.9). `canWaive` → waived.
- **What was true:** the test passed on release-11.8 for days before the cherry-pick, failed on the cherry-pick's early commits across three runs, then passed on its later commits (which contain `fix(e2e): sync Playwright navigations to live testcontainers baseURL`) and on release-11.8 after merge. A real regression, fixed by a later commit.
- **Why the evidence was insufficient:** the two gates the review named do **not** fire here. The 0.85 floor is passed because the *AI's* confidence (0.9), not the deterministic 0.8, is what `canWaive` sees — and `FLAKY_TEST` is `NeedsAI:true`, so the AI always runs. The diff-overlap block does not fire because the PR diff is CI-only (all `.github/`), so `diffOverlaps` is false. The amnesty gate does not fire because the test's failure rate on the *baseline* (release-11.8) is 0% — its 8 historical failures live on other branches. **The review's correction is right that round 4 ran only a fragment, but wrong that these two gates catch this case.**

### False green 3 — `MM-T1276` on `pr-37758`

- **Evidence:** `expect(locator).not.toBeVisible()` failed — `Expected: not visible, Received: visible` (a Settings dialog that should have closed stayed open). `Suggest` → `PR_REGRESSION` (0.7, `NeedsAI`).
- **What it concluded:** AI (simulated) → `FLAKY_TEST` (0.9) — my simulation misread a "wrong product state" as a timing race. `canWaive` → waived.
- **What was true:** a real bug (the dialog did not close). The deterministic hint was correct; the AI overrode it.
- **Why the evidence was insufficient:** this is a **simulation artifact, not a shipped defect.** The real AI's rule 2 ("wrong product state → bug") would classify `Received: visible` as a bug, not a flake. I flag it as a false green in the untuned numbers, but the real model would likely catch it.

### False green 4 — `MM-T5766` on `pr-38094`

- **Evidence:** `The maximum number of property fields for this object type has been reached` (a `ClientError` thrown by the server). `Suggest` → `PR_REGRESSION` (0.7, `NeedsAI`).
- **What it concluded:** AI (simulated) → `FLAKY_TEST` (0.9). `canWaive` → waived.
- **What was true:** a real bug (the server rejected the request). Same simulation artifact as false green 3 — the real AI's rule 2 would call this a bug.

**Net:** the two ABAC cases are genuine shipped false greens. The two `MM-T1276`/`MM-T5766` cases are artifacts of my no-screenshot AI simulation and would likely be caught by the real model. Either way, the bar of 0 is not met.

---

## 5. The three Phase 0 numbers (carried forward)

| Number | Value |
|---|---|
| baseline raw master pass-rate | **88.40%** |
| new-flaky arrival rate | **1.5 / day** |
| single-commit attribution rate | **16.0%** → M4 ships ledger-only |

---

## 6. Recommendation

**Do not start Phase 0.**

The full pipeline (config B) does not clear the false-green bar: the ABAC shape — a real regression in a historically-flaky test — is waived 2/2, and the two gates the review named (0.85 floor, diff-overlap) do not catch it. Config C buys nothing and costs more; do not adopt it.

**The single change that would:** add a **single-commit isolation signal** to `Suggest` — "this test passed on the immediately-prior commit on *this* branch and now fails" — and let it take precedence over the flaky-history branch. That signal already exists in the data (the 16% attribution rate is exactly it) and is the one thing that distinguishes "flaked again" from "broke for real". It is a classifier change, not a new feature, and it is the only change that moves the false-green count toward zero.

### Honest limitations

- **The AI layer is simulated, not run.** I have no model and no screenshots, so configs B and C approximate the AI with the prompt's deterministic rules. The two `MM-T1276`/`MM-T5766` false greens are likely simulation artifacts; the two ABAC false greens are not.
- **Q3 is unmeasured** (n=1 master/release real bug).
- **Q4's loop was not run end-to-end** (needs a real workspace + push); the answer is from the design and code, not a live PR.
- **Amnesty waiver-count is unknown** (needs the `triage_verdicts` ledger); I used the failure-rate limit only.

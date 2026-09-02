# Final capability report — does it do the four jobs?

**Date:** 2026-09-02
**Branch:** `feat/flakiness-management`
**Data:** production TSIO, `mattermost/mattermost` + Playwright, 2026-07-20 → 2026-09-02.
**Sample:** the same 41 labelled cases (22 real bug, 18 flake, 1 unknowable).
**Model:** `gemma4:31b-cloud` (the local 31B), `temperature=0`, `seed=42`, 3 runs per case per arm.

---

## 1. The four answers

| # | Question | Answer | One sentence |
|---|---|---|---|
| 1 | Can a developer tell their PR introduced a bug? | **NO** | Arm 2 waives 9/22 real PR bugs (41%) — including both ABAC cases — so a developer cannot rely on it to catch their own regression. |
| 2 | Can a developer tell it is a flaky test, not their change? | **PARTIAL** | Arm 2 waives 13/18 flakes but keeps 5 red (28%) — better than round 4's 50%, still above the 20% bar. |
| 3 | Can a developer tell it came from master and is a real bug? | **UNMEASURED** | n=1 master/release real bug; no rate can be reported from one case. |
| 4 | Can it watch master and open a fix PR? | **UNMEASURED** | The loop was not run end-to-end (needs a real workspace + push). Product bugs are routed, never fixed — by design, not a failure; CODEOWNERS still has no `e2e-tests/**` entry. |

---

## 2. Arm 1 vs Arm 2 (the value of the fix, isolated)

| Measure | Bar | Arm 1 (4 tools) | Arm 2 (+ diff + source) |
|---|---|---|---|
| False greens (waived a real bug) | 0 | 11/22 = **50%** | 9/22 = **41%** |
| False reds (kept noise red) | ≤ 20% | 6/18 = **33%** | 5/18 = **28%** |
| Verdict stability (3 identical runs) | ≥ 95% | 35/41 = **85%** | 36/41 = **88%** |
| Calibration at 0.85+ | ≥ 85% correct | 18/34 = **53%** | 21/35 = **60%** |
| Tool-call compliance (FLAKY_* called get_pr_diff) | 100% | — | **19/19 = 100%** |

**The tools help, but they do not clear the bars.** Arm 2 is better than Arm 1 on every axis (false greens 50%→41%, false reds 33%→28%, stability 85%→88%, calibration 53%→60%), and the model *does* comply — it called `get_pr_diff` before every single FLAKY_* verdict. But the false-green bar is 0 and Arm 2 is at 41%.

---

## 3. The hard sub-case, on its own

**A real regression in a historically-flaky test** (the ABAC shape): `MM-T5824` and `MM-T5820` on `pr-37732`.

| | Arm 1 | Arm 2 |
|---|---|---|
| Kept red | 0 | 0 |
| Waived (false green) | **2** | **2** |

**Both arms waive both cases.** The review's hypothesis — that giving the model the diff would let it see the testcontainers change and catch this — is **not supported by the measurement**. The model read the diff (it called `get_pr_diff`), saw the changed-file paths including `e2e-tests/playwright/lib/src/containers/…`, and still returned `FLAKY_TEST` at 0.9. Its stated reason: *"the history shows a high failure rate (40%) with multiple flips."*

The model's decision is dominated by the **history**, not the diff. A 40% historical failure rate is read as "this test flakes", and the diff does not override that. The evidence that would have distinguished "flaked again" from "broke for real" is the **single-commit isolation signal** — *this test passed on the immediately-prior commit on this branch and now fails* — and that signal is not in the prompt, the tools, or the model's rules.

---

## 4. Every false green, in full (Arm 2)

All nine are timeout/visibility errors the model classified as a UI timing race (rule 5 → `FLAKY_TEST`). The ground truth is "same commit failed ≥2×, never passed later" — a real bug.

| Test | PR | Error (first line) | Verdict | Conf |
|---|---|---|---|---|
| MM-T5779 | pr-38074 | `locator.click: Timeout 30000ms exceeded` | FLAKY_TEST | 0.90 |
| MM-T5768 | pr-38074 | `locator.click: Timeout 30000ms exceeded` | FLAKY_TEST | 0.90 |
| MM-T5769 | pr-38074 | `locator.click: Timeout 30000ms exceeded` | FLAKY_TEST | 0.90 |
| MM-T5777 | pr-38074 | `locator.click: Timeout 30000ms exceeded` | FLAKY_TEST | 0.90 |
| MM-T5776 | pr-38074 | `toBeFocused() failed — element(s) not found` | FLAKY_TEST | 0.87 |
| MM-T5796 | pr-38188 | `toBeFocused() failed — Expected: focused, Received: inactive` | FLAKY_TEST | 0.90 |
| MM-T5650 | pr-38188 | `toBeFocused() failed — Expected: focused, Received: inactive` | FLAKY_TEST | 0.80 |
| MM-T5824 | pr-37732 | `policy "…" should appear after search — Expected: true, Received: false` | FLAKY_TEST | 0.90 |
| MM-T5820 | pr-37732 | `policy "…" should appear after search — Expected: true, Received: false` | FLAKY_TEST | 0.90 |

The pattern is uniform: a timeout or a `focused/inactive` assertion is read as "element rendered but too slow" (rule 5). The model cannot tell a timing race from a wrong product state **without the screenshot** — and this backtest had no screenshots, so the vision path was never exercised. That is a real gap in this measurement, not a proof the design is broken.

---

## 5. Model control (Task 3)

**UNMEASURED.** The production model is Anthropic (the workflow forwards `ANTHROPIC_API_KEY`); no Anthropic key is available in this environment, so arm 2 could not be run on the production model. The numbers above are the local 31B only.

This matters: a 31B model is materially weaker than a frontier model on exactly the task that failed here — reading a timeout error and deciding whether it is a race or a break. **A bad local result means "this model cannot do it", not "the design cannot do it."** The production numbers are the real ones, and they are not in this report.

---

## 6. Calibration (Task 4)

The model is **overconfident**. It says 0.90 on almost everything, but at the 0.85–0.95 bucket it is correct only **53% (arm 1) / 60% (arm 2)** of the time. There are no verdicts below 0.7 or above 0.95 — the confidence distribution is a spike at 0.90.

This is the finding worth more than any accuracy number: **the 0.85 floor is decorative.** A model that says 0.9 on everything makes the floor protect nothing. If 0.9-confidence verdicts are right 60% of the time, the floor needs to move — or the model's confidence needs recalibration — before any waiver is safe.

---

## 7. Recommendation

**Do not start Phase 0.**

The tools are a real improvement and should ship (Arm 2 beats Arm 1 on every axis, and tool-call compliance is 100%), but they do not clear the false-green bar: 9/22 real bugs are still waived, including both ABAC cases. Two things stand between this and a start:

1. **The single-commit isolation signal.** The model waives on "high historical failure rate" and has no signal for "passed on the immediately-prior commit on this branch, now fails". That signal is the one thing that distinguishes "flaked again" from "broke for real", and it is absent from the prompt, the tools, and the rules. It is the same finding as round 5, now confirmed against a real model.

2. **Calibration.** The model's 0.9 is not 90%. Until the 0.85+ bucket is actually ≥85% correct, the floor protects nothing.

**The single change that would let me start:** add the single-commit isolation signal to the prompt and the classifier, and treat "passed on the prior commit, now fails" as a bug unless the screenshot shows a correct product state. That is a classifier/prompt change, not a new feature, and it is the only change that moves the false-green count toward zero.

### Honest limitations

- **The AI layer ran for the first time, but on the local 31B only.** The production (frontier) model is UNMEASURED — no Anthropic key. Treat these as lower-bound numbers.
- **No screenshots.** The vision path was never exercised, and the false greens are precisely the cases where a screenshot is the deciding evidence (timing race vs. wrong product state).
- **Q3 is unmeasured** (n=1 master/release real bug).
- **Q4's loop was not run end-to-end.**
- **Point-in-time diff is approximate.** `get_pr_diff` fetches the current PR diff; the cherry-pick has grown since the failure, so the ABAC diff the model saw is larger than the point-in-time one. The changed-file paths (the part that matters) are preserved.

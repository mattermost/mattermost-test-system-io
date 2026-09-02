**E2E flakiness management — proposal up for review** 📋

Full proposal: [`2026-08-31-e2e-flakiness-management-strategy.md`](docs/superpowers/specs/2026-08-31-e2e-flakiness-management-strategy.md) · status and numbers: [`PROJECT-STATUS.md`](docs/superpowers/specs/PROJECT-STATUS.md)

**The short version.** PR triage (tsio#101, mattermost#38154, toolkit#3 — in review) answers *"is this failure mine or noise?"*. This adds the master side: rolling alerting to replace the 9am spot check (M2), a review-gated stabilization queue that turns detected flakiness into an actual fix (M3), author attribution for master-only regressions (M4), env/flag evidence on failures (M5), and a weekly blind audit of the AI's waivers (M6).

**Three things keep us out of the old bucket-list trap:**
1. We publish the **raw** master pass-rate — waiving can never improve the number we're judged by, and never edits test history.
2. A chronically waived test burns its waiver budget, goes red on master, and gets an owner. Bystander PRs still get waived, so the pain lands on the owner, not on whoever's PR hit it.
3. The AI's waiver authority is re-earned against a blind human sample and drops a phase automatically when accuracy slips.

**Where it actually stands.** The mechanism is done and tested: nothing greens without a ledger row, the blind audit is blind, release trains never auto-waive, and master red stays red. The **AI's judgment is not yet measured** — the production model has never run (no API key in the dev environment) and our test sample had no screenshots, which is the deciding evidence for the hard case. So I've moved the expensive decision out of the model's hands: a test whose failure rate shifts at a commit can no longer be waived as a flake, whatever the model says. That's a policy gate, not a prompt.

⚠️ **What it costs you if you agree:** a named test-infra rotation — **45 min/week** for the first ~6 weeks (blind audit only), rising to **up to 3 hrs/week** from Phase 3, when agent-authored stabilization PRs start needing review. That's the real ask. Once shadow mode starts it's a **minimum 8-week clock** (4 weeks shadow, then 2 per promotion).

**The ask: start Phase 0 (shadow — nothing flips, everything observes).** Three of the four Phase 0 numbers are already measured (raw master pass-rate 88.40%, new-flaky arrival 1.5/day, attribution 16.0% — so M4 ships ledger-only, no pings). The one blocker is a decision to turn on shadow-phase evidence capture: **screenshot upload on failing specs, and the production API key wired to the triage job.** Without both, Phase 0 gives us another 4 weeks we can't measure.

👉 **Read the *Decisions* section and object here by Fri Sep 4.** Silence on a line = we proceed with the default. D3 (the rotation) and D1 (master auto-waive) are the two most worth your attention.

Your asks from the meeting are all in there and attributed. cc @saturnino @eva @nuno

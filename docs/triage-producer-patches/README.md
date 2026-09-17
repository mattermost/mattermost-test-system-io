# Producer PR patches

These diffs were built from the local producer checkouts without changing them.
Apply from the corresponding repository root:

```sh
git apply --check /path/to/mattermost-mobile.patch
git apply /path/to/mattermost-mobile.patch
# or mattermost.patch in the server/webapp repository
```

The TSIO API, metadata forwarding actions, and the new triage action must land
in a TSIO release before either producer patch ships. The patches reference the
TSIO actions as `@TSIO_TRIAGE_RELEASE_SHA`, a deliberate placeholder (never a
floating `@main`) that fails at job start if left in place. Substitute the
release SHA before opening the producer PRs:

```sh
SHA=<tsio release sha>
git apply /path/to/mattermost-mobile.patch
# perl -pi behaves the same on GNU/Linux and macOS (BSD sed would need -i '').
grep -rl TSIO_TRIAGE_RELEASE_SHA .github/workflows | xargs perl -pi -e "s{\@TSIO_TRIAGE_RELEASE_SHA # replace with the TSIO release SHA that ships the triage action \(see docs/triage-producer-patches/README.md\)}{\@$SHA # tsio triage}"
```

Mobile: adds merge-base and PR changed-file capture; preserves one report per
platform; forwards branch kind/base/lane; requests triage after upload. In
shadow/off mode `tsio-report-status.js` follows the pre-triage path byte for
byte (same `E2E/Override` flag from `e2e-override-label`, same description
format, no extra GitHub reads before the status write). Only in enforce mode
does it re-read the workflow attempt and the live `E2E/Override` label before
posting the triage verdict as the required status. `E2E/Verified` is never read
live: it is a per-SHA waiver that persists on the PR and would otherwise waive
later commits. Unknown/missing mode fails closed rather than bypassing
enforcement.

Playwright: adds metadata and changed paths; dispatch-begin forwards them; runs
triage after summary; disables the summary's unguarded status posting and uses a
small GitHub-only finalizer for shadow/off. The assertion has an explicit enforce
branch: a raw successful summary cannot bypass a blocking verdict. The manual
`(verified)` status marker on the head SHA remains authoritative; the
`E2E Tests/verified` label is not read live for the same per-SHA reason. The optional
`QUARANTINE_REGEX` applies only with `E2E_TRIAGE_SKIP_QUARANTINED=true`; no shared
repository variables are changed.

`ANTHROPIC_API_KEY` (already present in both repositories) is declared as an
optional `workflow_call` secret and passed only along the PR chains, so the
triage action can run the AI second judge on PR failures. Without it the
deterministic engine verdict stands; the action never adjudicates trunk runs.

Workflow-call chains receive exactly the permissions the triage action needs:
`checks: write` (check run), `pull-requests: write` (sticky PR comment; this is
the GITHUB_TOKEN scope for issue comments on PRs) and `actions: read` (latest
run attempt). `issues` stays `read`. Test jobs retain their existing execution
behavior. Residual risk, unchanged from today: the mobile report/finalize jobs
already run PR-authored `detox/utils/*.js` with `statuses: write`; the added
checks/comment scopes widen what a malicious PR could post from CI but do not
add repository-content or secret access.

Validation:

```sh
# mobile
node detox/utils/tsio-report-status.js --self-test
node --test detox/utils/tsio-triage-status.test.js
# mattermost
bash .github/scripts/tsio-assert-results.test.sh
# both: actionlint -shellcheck= -pyflakes= <changed workflows>
```

The assertion regression cases cover raw-green + enforced-block, neutral,
manual override, shadow raw failure, unavailable policy, and a failed triage
step. Producer job execution against real GitHub/TSIO requires deployment and
workflow dispatch; local checks do not claim that external validation.

The existing channel webhook payloads include a TSIO triage attachment with verdict, exonerated/blocking counts, confidence, and verdict URL. No destination changes. Mobile human-override descriptions retain native reset markers; label presence is rechecked before required status writes. The config resolver refreshes base metadata even for already-expanded PR/main platform configurations.

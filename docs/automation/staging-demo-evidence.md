# Staging demo evidence, 7 September 2026

This records observations from the live demo. It is a maintainer-side assessment,
not a Cursor review or proof that the PR cannot cause a regression.

## Deployed and tested revisions

- TSIO PR [114](https://github.com/mattermost/mattermost-test-system-io/pull/114):
  `d5b2bde060dca0e93091ebc6bd2870dd3f0a26d3` deployed successfully in
  [run 34167892341](https://github.com/mattermost/mattermost-test-system-io/actions/runs/34167892341).
  `reset_database=false`; six existing MM-T188 history observations survived.
- The final counter correction, `6c385b88b82f68be5ebcb7e6fdd96ce405d0bcb3`,
  passed all repository CI jobs and deployed in
  [run 34170478185](https://github.com/mattermost/mattermost-test-system-io/actions/runs/34170478185),
  again preserving the database. The live API reports that revision.
- Mattermost PR [38356](https://github.com/mattermost/mattermost/pull/38356):
  `5ed5f7d29ae67d6ecf7864022e37c66792280a45` tested in
  [run 34168873085, attempt 1](https://github.com/mattermost/mattermost/actions/runs/34168873085).
- Both frameworks recorded the same immutable image:
  `mattermostdevelopment/mattermost-enterprise-edition@sha256:81815fb2cde724cdc412bfd8ab688d7d884ff3cc7afb4ff995e55b6a7608c384`.
  [Enterprise build 34167989452](https://github.com/mattermost/enterprise/actions/runs/34167989452)
  published the image for that Mattermost head.

## Coverage and observed failures

| Suite | Worker reports | Dispatch units | Final outcome |
| --- | --- | --- | --- |
| Cypress enterprise | 40 expected, registered and complete | 447: 444 passed, 2 failed, 1 skipped | Failed after both failed specs were retested |
| Playwright enterprise | 20 expected, registered and complete | 291: 271 passed, 20 skipped | Passed |

Both evidence packs report `complete=true` and `truncated=false`. Both
orchestration runs completed with zero pending, leased, abandoned or
retest-eligible units.

Playwright's test status is green, but worker 19's debug-artifact upload timed
out after dispatch completed. That failed job remains in the raw workflow;
the scoped Cypress waiver does not change it.

- [Cypress evidence](https://staging-test-io.test.mattermost.com/api/v1/tests/evidence?repository=mattermost%2Fmattermost&commit_sha=5ed5f7d29ae67d6ecf7864022e37c66792280a45&gh_run_id=34168873085&gh_run_attempt=1&name=cypress-full-enterprise)
  contains all three identities with a failed attempt. Two remain failed;
  MM-T1 passed on retry. Three evidence identities therefore do not mean three
  final failures.
- [Playwright evidence](https://staging-test-io.test.mattermost.com/api/v1/tests/evidence?repository=mattermost%2Fmattermost&commit_sha=5ed5f7d29ae67d6ecf7864022e37c66792280a45&gh_run_id=34168873085&gh_run_attempt=1&name=playwright-full-enterprise)
  contains zero failure identities.
- MM-T188 and `tests/integration/channels/markdown/markdown_image_spec.js :: Markdown with in-line images 1`
  each retain four failed attempts: two internal attempts in each of two spec
  executions. History reports `run_failed=true` for each.
- MM-T1 history retains one failed attempt and successful attempts, with
  `outcome=flaky` and `run_failed=false`. Multiple source tests reuse this external
  ID, so the key combines observations. Its three stored attempt rows are not
  three independent trials of the image-preview test; use the full title and
  spec path in orchestration evidence to distinguish them.

The two persistent failures show the broken-image fallback where the tests
expect the proxied `https://docs.mattermost.com/_images/icon-76x76.png` image.
A request from the review machine at 23:26 UTC returned HTTP 404 for that URL.
This supports an unavailable external test dependency; it does not by itself
prove what every CI worker received.

The exact enterprise-master baseline has only two complete observations per
affected key in the preceding 30-day window: one pass and one failure. The
failed baseline is commit `0019ecc531dfef227d5f7523d41f8443d47227e1`,
[run 34075130200](https://github.com/mattermost/mattermost/actions/runs/34075130200),
group `01a0799e-7be7-73ce-a29a-c9910d8d6b8d`, around 21 hours before this PR run.
Its two assertions match the fresh failures after removing the historical
`AssertionError: ` prefix. Their raw cluster signatures differ because that
prefix changed with the reporter; a hash-only match would miss this evidence.

The relevant product code and test assertions are unchanged by this PR. The PR
does change the CI harness and reporting, so this fact alone cannot clear it.
No controlled PR-versus-merge-base reproduction was performed. MM-T1's retry
survival is observed, but its cause is unresolved. Its two stored master
observations passed, subject to the reused-ID ambiguity above.

## Demo acceptance

**GO for the demo of Cursor diagnosis followed by deliberate maintainer
verification.** The backend, real E2E execution and retry preservation are
demonstrated. The native-flaky counter correction passed regression tests and
live verification: Cypress now shows 1,347 passed, 2 failed, 1 flaky and 20 skipped
tests, total 1,370. All 40 Cypress reports and three evidence identities survived
the final deployment.

The fresh [Cursor review 5135830599](https://github.com/mattermost/mattermost/pull/38356#pullrequestreview-5135830599)
used staging and inspected the exact head/run/attempt. It covered both persistent
failures and the retry survivor, disclosed incomplete baseline configuration,
and avoided confidence percentages and causal clearance. Its attempt wording
describes two spec executions per persistent PR failure; stored history retains
four internal attempts. The maintainer record additionally discloses MM-T1's
reused-ID ambiguity.

The scoped [verification workflow 34170697788](https://github.com/mattermost/mattermost/actions/runs/34170697788)
succeeded using its repository `GITHUB_TOKEN`. The
[approval and copied diagnosis](https://github.com/mattermost/mattermost/pull/38356#issuecomment-5576794663)
were stored at 23:38:20 UTC, before Cypress success status `53701176449` at
23:38:23 UTC. The
[verified receipt](https://github.com/mattermost/mattermost/pull/38356#issuecomment-5576795350)
followed at 23:38:25 UTC. Both E2E commit statuses are green on the unchanged
assessed head. Raw failed CI jobs and TSIO observations remain visible.

Use the [replacement prompt](cursor-e2e-diagnosis.md); no Cursor label token is
needed. The current run demonstrated the diagnosis-only prompt and structured
binding. The latest prompt also adds the reused-ID guard discovered during this
verification. The demo does not establish autonomous classifier accuracy or
authorize automatic future waivers.

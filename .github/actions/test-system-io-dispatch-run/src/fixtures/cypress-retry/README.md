# Captured Cypress retry contract

Captured 2026-09-07 from a real headless Electron 138 run of Cypress 15.18.0,
with cypress-multi-reporters 2.0.5, Mochawesome 7.1.4 and
mocha-junit-reporter 2.2.1. Cypress embeds Mocha 7.2.0 (also recorded in the
report metadata). The test genuinely failed once and passed on its retry;
there was one real failure screenshot, one clean pass, and one pending test.

`mochawesome.json` is the unmodified reporter output, including context added
by Mattermost's browser `test:after:run` capture hook. Mochawesome itself does
not emit an `attempts` field. `after-spec.json` is the unmodified sidecar from
Mattermost's Node `after:spec` hook: Cypress 15 supplies attempt states only.
The dispatcher validates those states against the captured browser errors,
durations and screenshot context. Its archived, enriched output is also saved
as `apps/server/internal/ingest/testdata/cypress_retry_enriched.json` for Go.

To regenerate, install the exact package versions above in a temporary Cypress
project, copy Mattermost's `reporter-config.json`, and use the included
`retry_spec.js` as `tests/integration/retry_spec.js`. Set `retries.runMode: 1`,
`video: false`, and `screenshotsFolder: tests/screenshots` in Cypress config.
Register Mattermost's `tests/plugins/tsio_attempts.js` on `after:spec`.
In the browser support file, call Mattermost's
`tests/support/tsio_attempts.js` from `test:after:run`, after the existing
Mochawesome failure-screenshot context is added. For this isolated spec that
context points to the actual generated
`screenshots/retry_spec.js/Cypress retry contract -- fails once then passes (failed).png`.

Invoke the action's `runUnit` with that project directory and
`['tests/integration/retry_spec.js']`. Copy the raw reporter JSON and the
invocation's `attempts/*.json` sidecar to this directory; copy the archived
`retry_spec.json` to the Go fixture path above. Do not construct attempt
arrays manually. Run the action's Cypress test and `go test ./internal/ingest`.

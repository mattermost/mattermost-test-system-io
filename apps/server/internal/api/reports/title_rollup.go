package reports

import (
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

// retestLabelRE matches shard labels that are retest workers (Playwright
// "retest-…", Cypress "run-failed-tests"). Same idea as retestNamePattern.
var retestLabelRE = regexp.MustCompile(`(?i)retest|run[-_ ]?failed`)

// titleCase is one attempt of a title across shards for consolidated rollup.
type titleCase struct {
	ReportID   uuid.UUID
	ShardLabel string
	Status     string
}

func isRetestShardLabel(shardLabel string) bool {
	return retestLabelRE.MatchString(strings.TrimSpace(shardLabel))
}

func statusIsPass(s string) bool {
	return teststatus.IsPass(s)
}

func statusIsFail(s string) bool {
	return teststatus.IsFail(s)
}

// rollupAttemptStatuses collapses attempts that share a report (in-shard retries).
func rollupAttemptStatuses(statuses []string) string {
	return teststatus.TestVerdict(statuses, false)
}

// specAttempt is one stored attempt of a spec in a suite, in ordinal order.
type specAttempt struct {
	Status string
	Retry  int
}

// suiteSpecOK is whether a spec's attempts in one suite ended well enough to
// show as passed, flaky or skipped: its final attempt passed or was skipped,
// or the retries leading to it roll up to flaky. A Playwright test.fail() test
// that passed and then failed on retry is flaky there, as countStatuses counts
// it, though its final attempt failed. Only the final attempt's own retry chain
// (retry counts rising up to it) counts: another Playwright project, or a
// retest upload, starts again at retry 0, and its pass is not this failure's
// retry.
func suiteSpecOK(attempts []specAttempt) bool {
	if len(attempts) == 0 {
		return false
	}
	final := attempts[len(attempts)-1]
	if teststatus.IsPass(final.Status) || teststatus.IsSkip(final.Status) {
		return true
	}
	// Interrupted / timedOut / unknown retries are not treated as test.fail() flaky.
	if final.Status != teststatus.Failed {
		return false
	}
	start := len(attempts) - 1
	for start > 0 && attempts[start-1].Retry < attempts[start].Retry {
		start--
	}
	chain := make([]string, 0, len(attempts)-start)
	for _, a := range attempts[start:] {
		chain = append(chain, a.Status)
	}
	return rollupAttemptStatuses(chain) == teststatus.Flaky
}

// rollupTitleStatus is the consolidated verdict for one unique test title.
//
// Rules:
//  1. Collapse attempts inside each report (retries → flaky).
//  2. Among primary (non-retest) shards: pass on one platform + fail on
//     another is failed — not flaky (ios vs android, linux vs macos).
//  3. A primary failure recovered by a retest shard is flaky (web
//     Playwright/Cypress retest survivors, including Cypress's generic
//     "run-failed-tests" job name).
func rollupTitleStatus(cases []titleCase) string {
	if len(cases) == 0 {
		return teststatus.Passed
	}

	byReport := map[uuid.UUID][]string{}
	labelByReport := map[uuid.UUID]string{}
	order := make([]uuid.UUID, 0)
	for _, c := range cases {
		if _, ok := byReport[c.ReportID]; !ok {
			order = append(order, c.ReportID)
			labelByReport[c.ReportID] = c.ShardLabel
		}
		byReport[c.ReportID] = append(byReport[c.ReportID], c.Status)
	}

	var primaryFail, primaryPass, primaryFlaky, primarySkip bool
	var retestPass, retestFail bool
	primaryCount := 0

	for _, id := range order {
		out := rollupAttemptStatuses(byReport[id])
		if isRetestShardLabel(labelByReport[id]) {
			switch {
			case statusIsPass(out):
				retestPass = true
			case statusIsFail(out):
				retestFail = true
			}
			continue
		}
		primaryCount++
		switch out {
		case teststatus.Flaky:
			primaryFlaky = true
		case teststatus.Passed:
			primaryPass = true
		case teststatus.Skipped:
			primarySkip = true
		default:
			primaryFail = true
		}
	}

	// Peer-platform divergence: one primary shard failed while another
	// primary passed/flaky (ios vs android). Retest recovery only applies
	// when there is no surviving peer-platform success alongside a failure.
	if primaryFail {
		if primaryCount > 1 && (primaryPass || primaryFlaky) {
			return teststatus.Failed
		}
		if retestPass {
			return teststatus.Flaky
		}
		return teststatus.Failed
	}
	if retestFail && !retestPass {
		return teststatus.Failed
	}
	if primaryFlaky {
		return teststatus.Flaky
	}
	if primaryPass || retestPass {
		return teststatus.Passed
	}
	if primarySkip {
		return teststatus.Skipped
	}
	return teststatus.Passed
}

package reports

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
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
	return s == statusPassed || s == statusFlaky
}

func statusIsFail(s string) bool {
	return s == statusFailed || s == statusTimedOut
}

// rollupAttemptStatuses collapses attempts that share a report (in-shard retries).
func rollupAttemptStatuses(statuses []string) string {
	var hasPass, hasFail, hasSkip bool
	for _, s := range statuses {
		switch {
		case statusIsPass(s):
			hasPass = true
		case statusIsFail(s):
			hasFail = true
		case s == statusSkipped:
			hasSkip = true
		}
	}
	if hasPass && hasFail {
		return statusFlaky
	}
	if hasFail {
		return statusFailed
	}
	if hasPass {
		for _, s := range statuses {
			if s == statusFlaky {
				return statusFlaky
			}
		}
		return statusPassed
	}
	if hasSkip {
		return statusSkipped
	}
	return statusPassed
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
		return statusPassed
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
		case statusFailed, statusTimedOut:
			primaryFail = true
		case statusFlaky:
			primaryFlaky = true
		case statusPassed:
			primaryPass = true
		case statusSkipped:
			primarySkip = true
		}
	}

	// Peer-platform divergence: one primary shard failed while another
	// primary passed/flaky (ios vs android). Retest recovery only applies
	// when there is no surviving peer-platform success alongside a failure.
	if primaryFail {
		if primaryCount > 1 && (primaryPass || primaryFlaky) {
			return statusFailed
		}
		if retestPass {
			return statusFlaky
		}
		return statusFailed
	}
	if retestFail && !retestPass {
		return statusFailed
	}
	if primaryFlaky {
		return statusFlaky
	}
	if primaryPass || retestPass {
		return statusPassed
	}
	if primarySkip {
		return statusSkipped
	}
	return statusPassed
}

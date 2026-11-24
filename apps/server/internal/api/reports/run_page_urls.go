package reports

import (
	"net/url"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

func runPageHref(urlPath, ghRunID, ghRunAttempt string) string {
	if urlPath == "" {
		return "/"
	}
	if ghRunID == "" || containsGHRunID(urlPath) {
		return urlPath
	}
	if ghRunAttempt == "" {
		ghRunAttempt = "1"
	}
	sep := "?"
	if containsQuery(urlPath) {
		sep = "&"
	}
	return urlPath + sep + "gh_run_id=" + url.QueryEscape(ghRunID) + "&gh_run_attempt=" + url.QueryEscape(ghRunAttempt)
}

func containsGHRunID(path string) bool {
	return containsQueryKey(path, "gh_run_id")
}

func containsQuery(path string) bool {
	return containsByte(path, '?')
}

func containsQueryKey(path, key string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	return u.Query().Get(key) != ""
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

func passRatePercent(s *testStats, rule teststatus.Rule) int {
	if teststatus.NormalizeRule(rule) == teststatus.RuleShared {
		if s == nil {
			return -1
		}
		return teststatus.PassRatePercent(s.Passed, s.Failed, s.Flaky)
	}
	if s == nil || s.Total <= 0 {
		return -1
	}
	return ((s.Passed + s.Flaky) * 100) / s.Total
}

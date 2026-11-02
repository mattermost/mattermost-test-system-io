package reports

import "net/url"

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

func passRatePercent(s *testStats) int {
	if s == nil || s.Total <= 0 {
		return -1
	}
	passed := s.Passed + s.Flaky
	return (passed * 100) / s.Total
}

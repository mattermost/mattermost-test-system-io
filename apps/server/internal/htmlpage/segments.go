package htmlpage

import "regexp"

var commitSHAParamRE = regexp.MustCompile(`(?i)^[0-9a-f]{7,40}$`)

// Repo slug segment vs commit SHA or reserved path.
func IsRepoPageSegment(segment string) bool {
	if segment == "" {
		return false
	}
	switch segment {
	case "r", "g", "c", "fragment":
		return false
	}
	return !commitSHAParamRE.MatchString(segment)
}

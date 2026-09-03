package triage

// Small formatting helpers shared by the reason strings.
//
// Reasons are read by a developer on a pull request, in a CI log, and in a
// ledger row months later, so they are assembled here rather than with fmt
// verbs scattered through the decision — the decision reads better as prose
// when the formatting is out of the way.

import (
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

// pct renders a rate as a whole percentage: 0.4 -> "40%".
func pct(rate float64) string {
	return strconv.FormatFloat(rate*100, 'f', 0, 64) + "%"
}

// f3 renders a probability to three decimals, trailing zeros trimmed, so
// 0.064 stays "0.064" and 0.100 becomes "0.1".
func f3(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// short abbreviates a commit SHA for prose. Shorter than git's default because
// these appear mid-sentence, and the full value is in the JSON alongside.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

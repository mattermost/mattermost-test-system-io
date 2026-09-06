package testreport

import "regexp"

// externalTestIDRE matches the Mattermost case-ID convention: "MM-T" followed by
// digits, optionally suffixed with "_N" for a sub-case ("MM-T4783_1").
//
// Anchored on a word boundary at the front so "XMM-T123" does not match, and
// greedy on the digits so "MM-T12345" is not truncated to "MM-T1234".
var externalTestIDRE = regexp.MustCompile(`\bMM-T[0-9]+(?:_[0-9]+)?`)

// ExternalTestID extracts the stable case ID from a test title, preferring the
// ancestor-prefixed fullTitle (the ID is frequently on the parent describe block,
// not the leaf test). Returns nil when the title carries no ID — history is simply
// unavailable for such tests rather than being keyed on something unstable.
//
// Keyed on the ID rather than the title on purpose: titles get reworded, and a
// title-keyed history series silently breaks at the rename.
func ExternalTestID(title, fullTitle string) *string {
	for _, candidate := range []string{fullTitle, title} {
		if m := externalTestIDRE.FindString(candidate); m != "" {
			id := m
			return &id
		}
	}
	return nil
}

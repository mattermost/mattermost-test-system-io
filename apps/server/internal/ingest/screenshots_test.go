package ingest

import (
	"slices"
	"testing"
)

func TestCandidateTestNames(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "playwright exact",
			input:    "Suite > Sub > Test",
			contains: []string{"Suite > Sub > Test"},
		},
		{
			name:     "cypress mochawesome single describe",
			input:    "flaky_navigation_screenshot_spec.ts/flaky navigation screenshot -- passes only on odd draws after visiting the navigation page",
			contains: []string{"flaky navigation screenshot passes only on odd draws after visiting the navigation page"},
		},
		{
			name:     "cypress mochawesome nested describe",
			input:    "spec.ts/outer -- inner -- does the thing",
			contains: []string{"outer inner does the thing"},
		},
		{
			name:     "cypress folder slash form",
			input:    "Suite/Test",
			contains: []string{"Suite > Test"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := candidateTestNames(tc.input)
			for _, want := range tc.contains {
				if !slices.Contains(got, want) {
					t.Errorf("missing candidate %q in %v", want, got)
				}
			}
		})
	}
}

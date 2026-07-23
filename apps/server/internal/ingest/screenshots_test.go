package ingest

import (
	"slices"
	"testing"
)

func TestDeriveTestNameFromPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "cypress failed png",
			in:   "flaky_navigation_screenshot_spec.ts/flaky navigation screenshot -- passes only on odd draws after visiting the navigation page (failed).png",
			want: "flaky_navigation_screenshot_spec.ts/flaky navigation screenshot -- passes only on odd draws after visiting the navigation page",
		},
		{
			name: "playwright suite leaf with retry index",
			in:   "Suite > Sub/Test-1.png",
			want: "Suite > Sub/Test",
		},
		{
			name: "detox testFnFailure uses parent fullName folder",
			in:   "ios.sim.debug.2026-07-22 23-45-52Z/Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel/testFnFailure.png",
			want: "Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel",
		},
		{
			name: "detox testStart",
			in:   "android.emu.debug.2026-07-22 16-57-59Z/Some Suite Some Test/testStart.png",
			want: "Some Suite Some Test",
		},
		{
			name: "detox visibility dump in same folder",
			in:   "ios.sim.debug.2026-07-22 23-45-52Z/Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel/DETOX_VISIBILITY_RCTViewComponentView__0x127fc8010__SCREEN.png",
			want: "Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel",
		},
		{
			name: "detox session-level beforeAllFailure stays session name",
			in:   "ios.sim.debug.2026-07-22 23-45-52Z/beforeAllFailure.png",
			want: "ios.sim.debug.2026-07-22 23-45-52Z",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveTestNameFromPath(tc.in)
			if got != tc.want {
				t.Fatalf("DeriveTestNameFromPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFullTitleMatchesCandidate(t *testing.T) {
	cases := []struct {
		full string
		cand string
		want bool
	}{
		{"Suite > Test", "Suite > Test", true},
		{"Suite > Test [chromium]", "Suite > Test", true},
		{"detox/maestro/flows/account/attach_logs.yml > attach_logs", "attach_logs", true},
		{"attach_logs", "attach_logs", true},
		{"detox/maestro/flows/account/attach_logs.yml > attach_logs", "mute_unmute", false},
		{"Suite > Test", "", false},
	}
	for _, tc := range cases {
		got := fullTitleMatchesCandidate(tc.full, tc.cand)
		if got != tc.want {
			t.Fatalf("fullTitleMatchesCandidate(%q, %q) = %v, want %v", tc.full, tc.cand, got, tc.want)
		}
	}
}

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
		{
			name: "detox fullName from DeriveTestNameFromPath",
			input: DeriveTestNameFromPath(
				"ios.sim.debug.2026-07-22 23-45-52Z/Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel/testFnFailure.png",
			),
			contains: []string{"Search - Search Messages MM-T5294_3 - should be able to search messages in a specific channel"},
		},
		{
			name:     "maestro per-flow screenshot dir",
			input:    "attach_logs/report-problem-dismissed",
			contains: []string{"attach_logs", "report-problem-dismissed", "attach_logs > report-problem-dismissed"},
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

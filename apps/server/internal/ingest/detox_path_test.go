package ingest

import "testing"

func TestRelativeDetoxPath_KeepsRelativeLayouts(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"detox/e2e/test/foo.e2e.ts", "detox/e2e/test/foo.e2e.ts"},
		{"e2e/detox/test/foo.e2e.ts", "e2e/detox/test/foo.e2e.ts"},
		{"e2e/maestro/flows/calls/mute.yml", "e2e/maestro/flows/calls/mute.yml"},
		{"./e2e/detox/test/foo.e2e.ts", "e2e/detox/test/foo.e2e.ts"},
		{`e2e\detox\test\foo.e2e.ts`, "e2e/detox/test/foo.e2e.ts"},
		{`./e2e\detox\test\foo.e2e.ts`, "e2e/detox/test/foo.e2e.ts"},
		{"", ""},
	}
	for _, tc := range cases {
		got := relativeDetoxPath(tc.in)
		if got != tc.want {
			t.Errorf("relativeDetoxPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRelativeDetoxPath_StripsGitHubActionsWorkspace(t *testing.T) {
	in := "/home/runner/work/mattermost-mobile/mattermost-mobile/e2e/detox/test/foo.e2e.ts"
	want := "e2e/detox/test/foo.e2e.ts"
	if got := relativeDetoxPath(in); got != want {
		t.Errorf("relativeDetoxPath(%q) = %q, want %q", in, got, want)
	}

	legacy := "/home/runner/work/mattermost-mobile/mattermost-mobile/detox/e2e/test/foo.e2e.ts"
	wantLegacy := "detox/e2e/test/foo.e2e.ts"
	if got := relativeDetoxPath(legacy); got != wantLegacy {
		t.Errorf("relativeDetoxPath(%q) = %q, want %q", legacy, got, wantLegacy)
	}

	// Unrelated /work/<a>/<b>/... must not be truncated when a != b.
	unrelated := "/var/work/owner/other-repo/e2e/detox/test/foo.e2e.ts"
	if got := relativeDetoxPath(unrelated); got != unrelated {
		t.Errorf("relativeDetoxPath(%q) = %q, want unchanged", unrelated, got)
	}

	// Windows absolute paths must still strip the GHA workspace on Linux hosts.
	win := `C:\Users\runner\work\mattermost-mobile\mattermost-mobile\e2e\detox\test\foo.e2e.ts`
	if got := relativeDetoxPath(win); got != want {
		t.Errorf("relativeDetoxPath(%q) = %q, want %q", win, got, want)
	}

	unc := `\\runner\work\mattermost-mobile\mattermost-mobile\e2e\detox\test\foo.e2e.ts`
	if got := relativeDetoxPath(unc); got != want {
		t.Errorf("relativeDetoxPath(%q) = %q, want %q", unc, got, want)
	}
}

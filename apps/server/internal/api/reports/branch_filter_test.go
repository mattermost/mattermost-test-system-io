package reports

import (
	"strings"
	"testing"
)

func TestBranchFilterAppendSQL(t *testing.T) {
	var b strings.Builder
	args := []any{}
	BranchFilter("").appendSQL(&b, &args)
	if b.Len() != 0 {
		t.Fatal("empty filter should not append SQL")
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterMainMaster).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "branch = $1") || !strings.Contains(b.String(), "branch = $6") {
		t.Fatalf("sql = %q", b.String())
	}
	if len(args) != 6 {
		t.Fatalf("args = %v", args)
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterRelease).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "release-") {
		t.Fatalf("sql = %q", b.String())
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterPR).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "gh_pr_number") {
		t.Fatalf("sql = %q", b.String())
	}
}

func TestResolveBranchFilter(t *testing.T) {
	opts := []BranchFilterOption{{Value: branchFilterMainMaster, Label: "master"}}
	if got := ResolveBranchFilter("", opts); got != branchFilterMainMaster {
		t.Fatalf("got %q", got)
	}
	if got := ResolveBranchFilter("kind:pr", opts); got != "kind:pr" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveBranchFilter("", nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
func TestClassifyBranchFilterRow(t *testing.T) {
	pr := 99
	kind, sawMain, ok := classifyBranchFilterRow("main", &pr)
	if !ok || kind != branchKindPR || sawMain {
		t.Fatalf("kind=%d sawMain=%v ok=%v", kind, sawMain, ok)
	}
	kind, sawMain, ok = classifyBranchFilterRow("refs/heads/master", nil)
	if !ok || kind != branchKindMainMaster || sawMain {
		t.Fatalf("kind=%d sawMain=%v", kind, sawMain)
	}
	kind, _, ok = classifyBranchFilterRow("release-10.5", nil)
	if !ok || kind != branchKindRelease {
		t.Fatalf("kind=%d", kind)
	}
	if _, _, ok = classifyBranchFilterRow("release", nil); ok {
		t.Fatal("bare release branch should be excluded")
	}
	kind, _, ok = classifyBranchFilterRow("pr-12", nil)
	if !ok || kind != branchKindPR {
		t.Fatalf("kind=%d", kind)
	}
	if _, _, ok = classifyBranchFilterRow("pr-+123", nil); ok {
		t.Fatal("pr-+123 with leading plus must not match PR branch")
	}
	kind, sawMain, ok = classifyBranchFilterRow("main", nil)
	if !ok || kind != branchKindMainMaster || !sawMain {
		t.Fatalf("kind=%d sawMain=%v", kind, sawMain)
	}
}

func TestParsePRBranchStrictDigits(t *testing.T) {
	valid := map[string]int{
		"pr-1":          1,
		"PR-3891":       3891,
		"pr-0042":       42,
		"pr-2147483647": 2147483647,
	}
	for in, want := range valid {
		got, ok := parsePRBranch(in)
		if !ok || got != want {
			t.Fatalf("parsePRBranch(%q) = (%d, %v), want (%d, true)", in, got, ok, want)
		}
	}
	for _, invalid := range []string{
		"", "pr-", "pr-0", "pr-000", "pr-+123", "pr--123", "pr-12a", "pr- 12", "pr-2147483648", "main",
	} {
		if got, ok := parsePRBranch(invalid); ok {
			t.Fatalf("parsePRBranch(%q) = (%d, true), want false", invalid, got)
		}
	}
}

func TestIsValidRunStage(t *testing.T) {
	for _, valid := range []string{RunStagePR, RunStageMain, RunStageRelease, RunStageNightly, RunStageManual, RunStageOther} {
		if !IsValidRunStage(valid) {
			t.Fatalf("expected stage %q to be valid", valid)
		}
	}
	for _, invalid := range []string{"", "PR", "nightly ", "unknown"} {
		if IsValidRunStage(invalid) {
			t.Fatalf("expected stage %q to be invalid", invalid)
		}
	}
}

func TestResolveRunStage(t *testing.T) {
	pr := 42
	zero := 0
	cases := []struct {
		name     string
		explicit string
		event    string
		branch   string
		prNum    *int
		want     string
	}{
		{"explicit overrides event and branch", "manual", "pull_request", "main", &pr, RunStageManual},
		{"schedule event yields nightly even on main", "", "schedule", "main", nil, RunStageNightly},
		{"workflow_dispatch yields manual even on release", "", "workflow_dispatch", "release-10.5", nil, RunStageManual},
		{"pull_request yields pr on feature branch", "", "pull_request", "feat/run-ctx", nil, RunStagePR},
		{"pull_request_target yields pr", "", "pull_request_target", "feat/run-ctx", nil, RunStagePR},
		{"merge_group yields pr", "", "merge_group", "gh-readonly-queue/main/pr-1", nil, RunStagePR},
		{"pr number fallback yields pr", "", "push", "feat/x", &pr, RunStagePR},
		{"pr-N branch yields pr", "", "", "pr-12", nil, RunStagePR},
		{"pr-0 branch falls back to other", "", "", "pr-0", &zero, RunStageOther},
		{"refs/heads/master yields main", "", "push", "refs/heads/master", nil, RunStageMain},
		{"release-10.5 yields release", "", "push", "release-10.5", nil, RunStageRelease},
		{"bare release yields other", "", "", "release", nil, RunStageOther},
		{"empty yields other", "", "", "", nil, RunStageOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRunStage(tc.explicit, tc.event, tc.branch, tc.prNum); got != tc.want {
				t.Fatalf("ResolveRunStage(%q, %q, %q, %v) = %q, want %q",
					tc.explicit, tc.event, tc.branch, tc.prNum, got, tc.want)
			}
		})
	}
}

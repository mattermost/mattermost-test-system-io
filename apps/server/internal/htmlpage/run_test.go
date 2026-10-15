package htmlpage

import (
	"strings"
	"testing"
)

func TestRenderRunDispatch(t *testing.T) {
	html, err := RenderRun(RunPage{
		Title: "cypress-full · Test System IO",
		Site:  SiteInfo{ServerVersion: "0.15.0", Environment: "test"},
		Breadcrumbs: []Crumb{
			{Label: "Reports", Href: "/reports"},
			{Label: "mattermost", Href: "/reports/mattermost"},
			{Label: "master", Href: "/reports/mattermost/master"},
			{Label: "1ac532c", Href: "/reports/mattermost/master/1ac532c"},
			{Label: "cypress-full"},
		},
		Identity: RunIdentity{
			Repository:   "mattermost/mattermost",
			CommitSHA:    "1ac532c0123456789abcdef0123456789abcdef0",
			ShortSHA:     "1ac532c",
			GHRunID:      "gh_run_1789010226023",
			Name:         "cypress-full",
			GHRunAttempt: "1",
			Branch:       "master",
			Framework:    "Cypress",
		},
		Mode:            "dispatch",
		HasTestStatus:   true,
		TestStatus:      "failed",
		TestStatusClass: "test-status-fail",
		ProgressStatus:  "completed",
		ProgressClass:   "progress-done",
		ProgressLabel:   "Completed",
		PassRate:        "90",
		PassRateClass:   "rate-fail",
		HasTests:        true,
		TestTotal:       10,
		TestPassed:      9,
		TestFailed:      1,
		DurationText:    "12m",
		TotalSpecs:      3,
		SpecsDone:       3,
		UnitTotalAll:    3,
		UnitPass:        2,
		UnitFail:        1,
		UnitPage:        1,
		UnitPages:       1,
		UnitShowStart:   1,
		UnitShowEnd:     3,
		Units: []RunUnitRow{
			{
				RowNum: 1, SpecPath: "e2e/specs/login.ts", Status: "passed", StatusIcon: "pass",
				StatusClass: "unit-pass", DurationText: "12s", DurationClass: "duration-ok",
				TestTotal: 5, TestPassed: 5,
				WorkerSlots: []WorkerSlot{{Label: "1", Class: "slot-pass", Title: "job-0"}},
			},
			{
				RowNum: 2, SpecPath: "e2e/specs/fail.ts", Status: "failed", StatusIcon: "fail",
				StatusClass: "unit-fail", DurationText: "3s", DurationClass: "duration-ok",
				TestTotal: 2, TestFailed: 1, TestPassed: 1,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`cypress-full`,
		`gh_run_1789010226023`,
		`run-dispatch`,
		`e2e/specs/login.ts`,
		`unit-fail`,
		`3 specs`,
		`10 tests`,
		`90%`,
		`test-status-fail`,
		`github.com/mattermost/mattermost`,
		`git-chip-run`,
		`run-spec-header`,
		`run-filter-btn`,
		`data-orch-gh-run-id="gh_run_1789010226023"`,
		`breadcrumb`,
		`/reports/mattermost/master`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestRenderRunEmpty(t *testing.T) {
	html, err := RenderRun(RunPage{
		Title:       "missing · Test System IO",
		Site:        SiteInfo{},
		Mode:        "empty",
		ReportsHint: "No orchestration run found for this identity.",
		Breadcrumbs: []Crumb{{Label: "Reports", Href: "/reports"}, {Label: "x"}},
		Identity:    RunIdentity{Name: "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, "Nothing to combine yet") {
		t.Fatal("missing empty title")
	}
	if !strings.Contains(s, "No orchestration run found") {
		t.Fatal("missing hint")
	}
}

func TestDecodeBranchPathSegment(t *testing.T) {
	if got := decodeBranchPathSegment("feat~tsio"); got != "feat/tsio" {
		t.Fatalf("got %q", got)
	}
}

func TestWorkerSlotLabel(t *testing.T) {
	names := []string{"orch-worker-1", "orch-worker-2"}
	if got := workerSlotLabel("orch-worker-2", 99, names); got != 2 {
		t.Fatalf("shared suffix: got %d", got)
	}
	if got := workerSlotLabel("e2e-on-macos-26", 42, []string{"e2e-on-windows-2022"}); got != 42 {
		t.Fatalf("unrelated suffix: got %d", got)
	}
}

func TestPassRateDisplayRun(t *testing.T) {
	if got := passRateDisplay(9, 1, 0); got != "90" {
		t.Fatalf("got %q", got)
	}
	if got := passRateDisplay(10, 0, 0); got != "100" {
		t.Fatalf("got %q", got)
	}
}

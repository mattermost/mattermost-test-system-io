package triageassessment

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAssessTestEvidence(t *testing.T) {
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	current := observation{identity: identity{key: "chrome :: MM-T1", file: "spec.ts", title: "suite MM-T1 test", project: "chrome", framework: "playwright"}, failed: true}
	baseline := func(n int, age time.Duration) []observation {
		rows := []observation{}
		for i := range n {
			rows = append(rows, observation{identity: current.identity, groupID: strconv.Itoa(i), commit: strconv.Itoa(i), createdAt: at.Add(-age - time.Duration(i)*time.Hour), passed: true})
		}
		return rows
	}
	tests := []struct {
		name            string
		mutate          func(*observation, *[]observation, *[]observation)
		outcome, reason string
	}{
		{name: "clean master sample is only PR suspect", outcome: PRSuspect, reason: "failure_not_observed_in_sampled_master_baseline"},
		{name: "master failure is observation", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].failed = true }, outcome: ObservedOnMaster, reason: "master_failure_does_not_establish_pr_innocence"},
		{name: "thin even with failure", mutate: func(_ *observation, _ *[]observation, h *[]observation) { *h = (*h)[:1]; (*h)[0].failed = true }, outcome: Unknown, reason: "thin_master_baseline"},
		{name: "retries do not create independent commits", mutate: func(_ *observation, _ *[]observation, h *[]observation) {
			for i := range *h {
				(*h)[i].commit = "same"
			}
		}, outcome: Unknown, reason: "thin_master_baseline"},
		{name: "stale before observation", mutate: func(_ *observation, _ *[]observation, h *[]observation) {
			*h = baseline(3, 49*time.Hour)
			(*h)[0].failed = true
		}, outcome: Unknown, reason: "stale_master_baseline"},
		{name: "window excludes old samples", mutate: func(_ *observation, _ *[]observation, h *[]observation) { *h = baseline(3, 15*24*time.Hour) }, outcome: Unknown, reason: "thin_master_baseline"},
		{name: "future master excluded", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].createdAt = at.Add(time.Hour) }, outcome: Unknown, reason: "thin_master_baseline"},
		{name: "skips do not count coverage", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].passed = false }, outcome: Unknown, reason: "thin_master_baseline"},
		{name: "current file collision", mutate: func(c *observation, a *[]observation, _ *[]observation) {
			alias := *c
			alias.file = "other.ts"
			*a = append(*a, alias)
		}, outcome: Unknown, reason: "ambiguous_test_identity"},
		{name: "historical file moved", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].file = "old.ts" }, outcome: Unknown, reason: "ambiguous_or_renamed_master_identity"},
		{name: "historical title renamed", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].title = "old title" }, outcome: Unknown, reason: "ambiguous_or_renamed_master_identity"},
		{name: "historical project aliased", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].project = "firefox" }, outcome: Unknown, reason: "ambiguous_or_renamed_master_identity"},
		{name: "historical framework aliased", mutate: func(_ *observation, _ *[]observation, h *[]observation) { (*h)[0].framework = "cypress" }, outcome: Unknown, reason: "ambiguous_or_renamed_master_identity"},
		{name: "missing project cannot infer", mutate: func(c *observation, _ *[]observation, _ *[]observation) { c.project = "" }, outcome: Unknown, reason: "test_identity_missing"},
		{name: "missing file cannot infer", mutate: func(c *observation, _ *[]observation, _ *[]observation) { c.file = "" }, outcome: Unknown, reason: "test_identity_missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, aliases, history := current, []observation{current}, baseline(3, time.Hour)
			if tc.mutate != nil {
				tc.mutate(&c, &aliases, &history)
			}
			got := assessTest(c, aliases, history, at, 48*time.Hour)
			if got.Outcome != tc.outcome || !slices.Contains(got.Reasons, tc.reason) {
				t.Fatalf("assessment: %+v", got)
			}
		})
	}
}

func TestAggregatePrecedenceIndependentOfOrder(t *testing.T) {
	for _, tc := range []struct {
		outcomes []string
		want     string
	}{
		{nil, NoFailure}, {[]string{ObservedOnMaster}, ObservedOnMaster},
		{[]string{ObservedOnMaster, PRSuspect}, PRSuspect}, {[]string{PRSuspect, ObservedOnMaster}, PRSuspect},
		{[]string{PRSuspect, Unknown, ObservedOnMaster}, Unknown}, {[]string{Unknown, PRSuspect}, Unknown},
	} {
		var tests []Test
		for _, outcome := range tc.outcomes {
			tests = append(tests, Test{Outcome: outcome})
		}
		if got := aggregate(tests); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.outcomes, got, tc.want)
		}
	}
}

func TestBaselineFailureCountersDistinguishRetrySurvivors(t *testing.T) {
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	id := identity{key: "MM-T1", file: "spec.cy.ts", title: "MM-T1 test", framework: "cypress"}
	current := observation{identity: id, failed: true}
	rows := []observation{
		{identity: id, groupID: "failed", commit: "1", createdAt: at.Add(-time.Hour), failed: true},
		{identity: id, groupID: "flaky", commit: "2", createdAt: at.Add(-2 * time.Hour), failed: true, passed: true},
		{identity: id, groupID: "passed", commit: "3", createdAt: at.Add(-3 * time.Hour), passed: true},
	}
	got := assessTest(current, []observation{current}, rows, at, 48*time.Hour)
	if got.Outcome != ObservedOnMaster || got.BaselineRuns != 3 || got.BaselineFailures != 2 || got.BaselineFailedRuns != 1 || got.BaselineFlakyRuns != 1 {
		t.Fatalf("baseline counters: %+v", got)
	}
	if got := assessTest(current, []observation{current}, rows, at, 30*time.Minute); got.Outcome != Unknown {
		t.Fatalf("configured cadence was ignored: %+v", got)
	}
}

func TestRecordRejectsCallerAuthorityBeforeDatabase(t *testing.T) {
	valid := `{"repository":"mattermost/mattermost","commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","gh_run_id":"123","gh_run_attempt":"1","name":"cypress"}`
	for _, tc := range []struct {
		name, body, actor, scope string
		want                     int
	}{
		{"unauthenticated", valid, "", "", http.StatusForbidden},
		{"cross repository", valid, "workflow", "evil/fork", http.StatusForbidden},
		{"caller chosen tests", strings.TrimSuffix(valid, "}") + `,"stable_keys":["MM-T1"]}`, "workflow", "", http.StatusBadRequest},
		{"caller chosen author", strings.TrimSuffix(valid, "}") + `,"author":"admin"}`, "workflow", "", http.StatusBadRequest},
		{"two objects", valid + valid, "workflow", "", http.StatusBadRequest},
		{"missing selector", `{}`, "workflow", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.Header.Set("X-TSIO-Triage-Actor", tc.actor)
			req.Header.Set("X-TSIO-Triage-Repository", tc.scope)
			res := httptest.NewRecorder()
			(&Handlers{}).Record(res, req)
			if res.Code != tc.want {
				t.Fatalf("status %d body %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestPreviewRejectsCallerKeysAndAmbiguousSelectors(t *testing.T) {
	for _, query := range []string{"repository=x/y&stable_key=MM-T1", "gh_run_id=1&gh_run_id=2", "repository=mattermost"} {
		res := httptest.NewRecorder()
		(&Handlers{}).Attribution(res, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", query, res.Code)
		}
	}
}

func TestBaselineNameMapping(t *testing.T) {
	for _, framework := range []string{"cypress", "playwright"} {
		for _, edition := range []string{"enterprise", "fips"} {
			name := framework + "-full-" + edition
			if got := baselineName("mattermost/mattermost", name, framework); got != name+"-master" {
				t.Fatalf("mapping %s: %s", name, got)
			}
			if got := baselineName("other/mattermost", name, framework); got != name {
				t.Fatalf("mapped unrelated repository: %s", got)
			}
			if got := baselineName("mattermost/mattermost", name, "unknown"); got != name {
				t.Fatalf("mapped wrong framework: %s", got)
			}
		}
	}
	if got := baselineName("mattermost/mattermost", "cypress-full-cloud", "cypress"); got != "cypress-full-cloud" {
		t.Fatalf("inferred unknown suite: %s", got)
	}
}

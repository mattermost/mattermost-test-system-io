package triageassessment

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRunEvidenceQuery(t *testing.T) {
	valid := url.Values{"repository": {"mattermost/mattermost"}, "commit_sha": {strings.Repeat("a", 40)}, "gh_run_id": {"123"}, "gh_run_attempt": {"1"}, "name": {"cypress-full-enterprise"}}
	if _, err := runEvidenceSelector(httptest.NewRequest(http.MethodGet, "/?"+valid.Encode(), nil)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"repository", "commit_sha", "gh_run_id", "gh_run_attempt", "name"} {
		t.Run("missing "+key, func(t *testing.T) {
			q := cloneEvidenceQuery(valid)
			q.Del(key)
			assertEvidenceBadQuery(t, q)
		})
		t.Run("repeated "+key, func(t *testing.T) {
			q := cloneEvidenceQuery(valid)
			q.Add(key, q.Get(key))
			assertEvidenceBadQuery(t, q)
		})
	}
	for key, value := range map[string]string{"limit": "1", "commit_sha": "main", "gh_run_id": "0", "gh_run_attempt": "-1", "repository": "owner/repo/extra", "name": " "} {
		t.Run("invalid "+key, func(t *testing.T) {
			q := cloneEvidenceQuery(valid)
			q.Set(key, value)
			assertEvidenceBadQuery(t, q)
		})
	}
	if _, err := runEvidenceSelector(httptest.NewRequest(http.MethodGet, "/?"+valid.Encode()+"&malformed=%XX", nil)); err == nil {
		t.Fatal("accepted malformed query")
	}
}

func cloneEvidenceQuery(q url.Values) url.Values {
	result := url.Values{}
	for key, value := range q {
		result[key] = append([]string{}, value...)
	}
	return result
}

func assertEvidenceBadQuery(t *testing.T, q url.Values) {
	t.Helper()
	r := httptest.NewRecorder()
	(&Handlers{}).RunEvidence(r, httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
	if r.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", r.Code, r.Body)
	}
}

func TestRunEvidenceWorkflow(t *testing.T) {
	for _, framework := range []string{"cypress", "playwright"} {
		for _, edition := range []string{"enterprise", "fips"} {
			g := RunEvidenceGroup{Selector: Selector{Repository: "mattermost/mattermost", CommitSHA: strings.Repeat("a", 40), Name: framework + "-full-" + edition}, Framework: framework, Branch: "feature"}
			if ref := runEvidenceWorkflow(g, nil); !strings.Contains(ref, "/e2e-tests-ci.yml@refs/heads/master") {
				t.Fatal(ref)
			}
			g.Name += "-master"
			if runEvidenceWorkflow(g, nil) != "" {
				t.Fatal("baseline accepted non-master branch")
			}
			g.Branch = "master"
			if ref := runEvidenceWorkflow(g, nil); !strings.Contains(ref, "/e2e-tests-on-merge.yml@refs/heads/master") {
				t.Fatal(ref)
			}
			pr := 1
			if runEvidenceWorkflow(g, &pr) != "" {
				t.Fatal("baseline accepted PR identity")
			}
			g.Repository = "other/mattermost"
			if runEvidenceWorkflow(g, nil) != "" {
				t.Fatal("accepted unrelated repository")
			}
		}
	}
}

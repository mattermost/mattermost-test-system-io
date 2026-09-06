package testhistory

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestEvidence_missingIdentityReturns400(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Get("/api/v1/tests/evidence", h.Evidence)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/evidence", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestEvidence_badGroupIDReturns400(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Get("/api/v1/tests/evidence", h.Evidence)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/evidence?group_id=not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestParseShots_buildsFileURLs(t *testing.T) {
	got := parseShots([]byte(`[{"s3_key":"reports/a/shot.png","screenshot_type":"failure"}]`))
	if len(got) != 1 || got[0].URL != "/files/reports/a/shot.png" || got[0].S3Key != "reports/a/shot.png" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeFailure_prefersHardFailAndScreenshots(t *testing.T) {
	flaky := evidenceFailure{Status: "flaky", Screenshots: []evidenceShot{{S3Key: "a.png"}}}
	failed := evidenceFailure{Status: "failed"}
	got := mergeFailure(flaky, failed)
	if got.Status != "failed" {
		t.Fatalf("status = %s", got.Status)
	}
	if len(got.Screenshots) != 1 || got.Screenshots[0].S3Key != "a.png" {
		t.Fatalf("screenshots = %+v", got.Screenshots)
	}
}

// Every member carries the key /tests/history is looked up by, not only the
// representative — a consumer walking the members must never reconstruct it.
func TestClusterFailures_membersCarryStableKey(t *testing.T) {
	msg := "element not visible"
	failures := []evidenceFailure{
		{StableKey: "MM-T1", FullTitle: "MM-T1 a", Status: "failed", ErrorMessage: &msg},
		{StableKey: "sidebar collapses", FullTitle: "sidebar collapses", Status: "failed", ErrorMessage: &msg},
	}
	clusters, _ := clusterFailures(failures)
	if len(clusters) != 1 || len(clusters[0].Members) != 2 {
		t.Fatalf("clusters = %+v", clusters)
	}
	for _, m := range clusters[0].Members {
		if m.StableKey == "" {
			t.Fatalf("member %q has no stable_key", m.FullTitle)
		}
	}
}

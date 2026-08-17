package triage

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestEvidence_missingIdentityReturns400(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Get("/api/v1/triage/evidence", h.Evidence)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/triage/evidence", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestEvidence_badGroupIDReturns400(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Get("/api/v1/triage/evidence", h.Evidence)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/triage/evidence?group_id=not-a-uuid", nil)
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

package kpi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

func TestSummaryValidationErrors(t *testing.T) {
	// Non-nil dummy pool pointer is never dereferenced when validation fails first.
	h := &Handlers{Store: &kpi.Store{Pool: new(pgxpool.Pool)}}

	badURLs := []string{
		"/api/v1/kpi/summary?window=99d",
		"/api/v1/kpi/summary?stage=invalid",
		"/api/v1/kpi/summary?sort=unknown",
		"/api/v1/kpi/summary?window=%ZZ",
		"/api/v1/kpi/summary?lane=%00",
		"/api/v1/kpi/summary?app=%FF",
		"/api/v1/kpi/summary?repo=org/repo%00",
		"/api/v1/kpi/summary?platform=ios%FF",
	}
	for _, u := range badURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		h.Summary(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", u, rec.Code, rec.Body.String())
		}
		var errBody api.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("%s: unmarshal error body: %v", u, err)
		}
		if errBody.Code != "BAD_REQUEST" {
			t.Fatalf("%s: error code = %q, want BAD_REQUEST", u, errBody.Code)
		}
	}
}

func TestFlakyValidationErrors(t *testing.T) {
	h := &Handlers{Store: &kpi.Store{Pool: new(pgxpool.Pool)}}

	badURLs := []string{
		"/api/v1/kpi/flaky?window=1d",
		"/api/v1/kpi/flaky?stage=bogus",
		"/api/v1/kpi/flaky?sort=lane_asc",
		"/api/v1/kpi/flaky?page=-1",
		"/api/v1/kpi/flaky?limit=0",
		"/api/v1/kpi/flaky?window=%ZZ",
		"/api/v1/kpi/flaky?lane=%00",
		"/api/v1/kpi/flaky?app=%FF",
		"/api/v1/kpi/flaky?repo=org/repo%00",
		"/api/v1/kpi/flaky?platform=ios%FF",
	}
	for _, u := range badURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		h.Flaky(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", u, rec.Code, rec.Body.String())
		}
		var errBody api.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("%s: unmarshal error body: %v", u, err)
		}
		if errBody.Code != "BAD_REQUEST" {
			t.Fatalf("%s: error code = %q, want BAD_REQUEST", u, errBody.Code)
		}
	}
}

func TestNilStoreReturnsInternalError(t *testing.T) {
	h := &Handlers{}
	for _, fn := range []http.HandlerFunc{h.Summary, h.Flaky} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/kpi/summary", nil)
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	}
}

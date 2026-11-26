package kpi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

const errCodeBadRequest = "BAD_REQUEST"

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
		if errBody.Code != errCodeBadRequest {
			t.Fatalf("%s: error code = %q, want %s", u, errBody.Code, errCodeBadRequest)
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
		if errBody.Code != errCodeBadRequest {
			t.Fatalf("%s: error code = %q, want %s", u, errBody.Code, errCodeBadRequest)
		}
	}
}

func TestPRsRedValidationErrors(t *testing.T) {
	h := &Handlers{Store: &kpi.Store{Pool: new(pgxpool.Pool)}}

	badURLs := []string{
		"/api/v1/kpi/prs/red?window=5d",
		"/api/v1/kpi/prs/red?stage=bogus",
		"/api/v1/kpi/prs/red?sort=duration_desc",
		"/api/v1/kpi/prs/red?page=0",
		"/api/v1/kpi/prs/red?limit=200",
		"/api/v1/kpi/prs/red?window=%ZZ",
	}
	for _, u := range badURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		h.PRsRed(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", u, rec.Code, rec.Body.String())
		}
		var errBody api.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("%s: unmarshal error body: %v", u, err)
		}
		if errBody.Code != errCodeBadRequest {
			t.Fatalf("%s: error code = %q, want %s", u, errBody.Code, errCodeBadRequest)
		}
	}
}

func TestFailuresValidationErrors(t *testing.T) {
	h := &Handlers{Store: &kpi.Store{Pool: new(pgxpool.Pool)}}

	badURLs := []string{
		"/api/v1/kpi/failures?window=3d",
		"/api/v1/kpi/failures?stage=invalid",
		"/api/v1/kpi/failures?sort=flaky_commits_desc",
		"/api/v1/kpi/failures?page=0",
		"/api/v1/kpi/failures?limit=101",
		"/api/v1/kpi/failures?window=%ZZ",
	}
	for _, u := range badURLs {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		h.Failures(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", u, rec.Code, rec.Body.String())
		}
		var errBody api.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("%s: unmarshal error body: %v", u, err)
		}
		if errBody.Code != errCodeBadRequest {
			t.Fatalf("%s: error code = %q, want %s", u, errBody.Code, errCodeBadRequest)
		}
	}
}

func TestFailureDetailValidationErrors(t *testing.T) {
	h := &Handlers{Store: &kpi.Store{Pool: new(pgxpool.Pool)}}

	withID := func(id, target string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}

	reqBadID := withID("not-a-uuid", "/api/v1/kpi/failures/not-a-uuid")
	recBadID := httptest.NewRecorder()
	h.FailureDetail(recBadID, reqBadID)
	if recBadID.Code != http.StatusBadRequest {
		t.Fatalf("invalid UUID status = %d, want 400 (body=%s)", recBadID.Code, recBadID.Body.String())
	}

	validID := uuid.NewString()
	for _, target := range []string{
		"/api/v1/kpi/failures/" + validID + "?window=99d",
		"/api/v1/kpi/failures/" + validID + "?window=%ZZ",
	} {
		reqBadWin := withID(validID, target)
		recBadWin := httptest.NewRecorder()
		h.FailureDetail(recBadWin, reqBadWin)
		if recBadWin.Code != http.StatusBadRequest {
			t.Fatalf("%s: invalid window status = %d, want 400 (body=%s)", target, recBadWin.Code, recBadWin.Body.String())
		}
	}
}

func TestNilStoreReturnsInternalError(t *testing.T) {
	h := &Handlers{}
	for _, fn := range []http.HandlerFunc{h.Summary, h.Flaky, h.PRsRed, h.Failures, h.FailureDetail} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/kpi/summary", nil)
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	}
}

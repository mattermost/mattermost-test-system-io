package artifacts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func newRouter() chi.Router {
	h := &Handlers{Pool: nil, Store: nil, PresignTTL: 5 * time.Minute}
	r := chi.NewRouter()
	r.Get("/api/v1/artifacts/{id}", h.Get)
	return r
}

func TestGet_badUUID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	newRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("content-type = %q, want application/json[; charset=…]", got)
	}
}

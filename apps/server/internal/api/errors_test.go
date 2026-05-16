package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

func TestWriteError_maps(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"bad request", ErrBadRequest, http.StatusBadRequest, "BAD_REQUEST"},
		{"query too short", ErrQueryTooShort, http.StatusBadRequest, "QUERY_TOO_SHORT"},
		{"unauthorized", ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"session expired", ErrSessionExpired, http.StatusUnauthorized, "SESSION_EXPIRED"},
		{"forbidden", ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{"not found", ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"conflict", ErrConflict, http.StatusConflict, "CONFLICT"},
		{"too large", ErrTooLarge, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE"},
		{"storage not found", storage.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"wrapped bad request", fmt.Errorf("context: %w", ErrBadRequest), http.StatusBadRequest, "BAD_REQUEST"},
		{"unknown error", errors.New("boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/thing", nil)

			WriteError(w, r, c.err)

			if got := w.Code; got != c.wantStatus {
				t.Errorf("status = %d, want %d", got, c.wantStatus)
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			var body Error
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode json: %v", err)
			}
			if body.Code != c.wantCode {
				t.Errorf("error = %q, want %q", body.Code, c.wantCode)
			}
			if body.Message == "" {
				t.Errorf("message empty for %s", c.name)
			}
		})
	}
}

func TestWriteError_internalHidesDetail(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/oops", nil)

	WriteError(w, r, errors.New("secret internal detail: database password leaked"))

	var body Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Message == "secret internal detail: database password leaked" {
		t.Fatalf("internal detail leaked: %q", body.Message)
	}
	if body.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q, want INTERNAL_ERROR", body.Code)
	}
}

func TestWriteErrorCode(t *testing.T) {
	w := httptest.NewRecorder()
	WriteErrorCode(w, http.StatusBadRequest, "VALIDATION_FAILED", "field 'name' is required")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var body Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "VALIDATION_FAILED" || body.Message != "field 'name' is required" {
		t.Errorf("body = %+v", body)
	}
}

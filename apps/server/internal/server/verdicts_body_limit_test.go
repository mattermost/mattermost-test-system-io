package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	verdictsapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/verdicts"
)

func TestVerdictsPostBodyLimitBeforeAuth(t *testing.T) {
	r := Build(Deps{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		OpenAPISpecPath: specPath,
	})

	t.Run("unauthenticated oversized POST /api/v1/verdicts returns 413", func(t *testing.T) {
		payload := bytes.Repeat([]byte(" "), verdictsapi.MaxCreateBodyBytes+1)
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/verdicts", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
		}
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("decode error response: %v (body=%s)", err, rec.Body.String())
		}
		if errResp.Error != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("error code = %q, want PAYLOAD_TOO_LARGE", errResp.Error)
		}
	})

	t.Run("non-verdict POST route reads full body past MaxCreateBodyBytes in Build", func(t *testing.T) {
		// OpenAPIValidator reads the entire request body for /api/v1/reports/history;
		// if verdictsBodyLimitMiddleware wrapped all routes, this would fail with 413.
		payload := append([]byte(`{"repository":"o/r","files":["a.spec.ts"],"branch":"`), bytes.Repeat([]byte("a"), verdictsapi.MaxCreateBodyBytes)...)
		payload = append(payload, []byte(`"}`)...)
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/reports/history", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("non-verdict POST returned 413 PAYLOAD_TOO_LARGE: %s", rec.Body.String())
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (after History 256KiB decoder cap); body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("streamed upload routes read entire body past MaxCreateBodyBytes", func(t *testing.T) {
		wantLen := int64(verdictsapi.MaxCreateBodyBytes + 1024)
		h := verdictsBodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if n != wantLen {
				http.Error(w, "short read", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))

		for _, path := range []string{
			"/api/v1/reports/upload/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/json",
			"/api/v1/reports/upload/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/screenshots",
			"/api/v1/orchestration/screenshots",
		} {
			payload := bytes.Repeat([]byte("a"), int(wantLen))
			req := httptest.NewRequest(http.MethodPost, "http://localhost:8080"+path, bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
			}
		}

		// Control: POST /api/v1/verdicts through the same middleware hits MaxBytesError.
		payload := bytes.Repeat([]byte("a"), int(wantLen))
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/verdicts", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("/api/v1/verdicts control: status = %d, want 413; body=%s", rec.Code, rec.Body.String())
		}
	})
}

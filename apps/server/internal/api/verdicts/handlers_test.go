package verdicts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/verdicts"
)

func TestCreate_UnauthorizedAndValidation(t *testing.T) {
	h := &Handlers{
		Store:     &verdicts.Store{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthzMode: authapi.AuthzEnforce,
	}

	t.Run("missing subject returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/verdicts", strings.NewReader(`{"repository":"mattermost/mattermost","verdicts":[{"label":"flaky"}]}`))
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("repo access enforce rejects mismatched OIDC repo", func(t *testing.T) {
		sub := authapi.Subject{
			Kind:       "oidc",
			Role:       policy.RoleUploader,
			Repository: "mattermost/repo-a",
		}
		if err := authapi.CheckRepoAccess(authapi.AuthzEnforce, h.Logger, sub, "mattermost/repo-b"); err == nil {
			t.Fatal("expected CheckRepoAccess to reject mismatched repo in enforce mode")
		}
		if err := authapi.CheckRepoAccess(authapi.AuthzEnforce, h.Logger, sub, "Mattermost/Repo-A.git"); err != nil {
			t.Fatalf("expected normalized repo match to pass, got %v", err)
		}
		if err := authapi.CheckRepoAccess(authapi.AuthzLogOnly, h.Logger, sub, "mattermost/repo-b"); err != nil {
			t.Fatalf("expected log-only mode to allow mismatched repo, got %v", err)
		}
	})

	t.Run("toBatchInput rejects invalid or blank UUIDs", func(t *testing.T) {
		for _, bad := range []string{"not-a-uuid", "", "   "} {
			v := bad
			if _, err := toBatchInput(createBatchBody{
				Repository:    "mattermost/mattermost",
				ReportGroupID: &v,
				Verdicts:      []createItemBody{{Label: "flaky"}},
			}); err == nil {
				t.Fatalf("expected error for invalid report_group_id UUID %q", v)
			}
			if _, err := toBatchInput(createBatchBody{
				Repository: "mattermost/mattermost",
				Verdicts:   []createItemBody{{TestID: &v, Label: "flaky"}},
			}); err == nil {
				t.Fatalf("expected error for invalid test_id UUID %q", v)
			}
			if _, err := toBatchInput(createBatchBody{
				Repository: "mattermost/mattermost",
				Verdicts:   []createItemBody{{SignatureID: &v, Label: "flaky"}},
			}); err == nil {
				t.Fatalf("expected error for invalid signature_id UUID %q", v)
			}
			if _, err := toBatchInput(createBatchBody{
				Repository: "mattermost/mattermost",
				Verdicts:   []createItemBody{{ReusedFromID: &v, Label: "flaky"}},
			}); err == nil {
				t.Fatalf("expected error for invalid reused_from_id UUID %q", v)
			}
		}
	})
}

func TestBySignature_Validation(t *testing.T) {
	h := &Handlers{
		Store:  &verdicts.Store{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	call := func(target, fp string) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("fingerprint", fp)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.BySignature(rec, req)
		return rec.Code
	}

	if code := call("/api/v1/verdicts/by-signature/", "  "); code != http.StatusBadRequest {
		t.Fatalf("empty fingerprint status = %d, want 400", code)
	}
	for _, q := range []string{"abc", "0", "-1", "366", "", "%20%20"} {
		if code := call("/api/v1/verdicts/by-signature/deadbeef?window_days="+q, "deadbeef"); code != http.StatusBadRequest {
			t.Fatalf("window_days=%s status = %d, want 400", q, code)
		}
	}
	for _, repo := range []string{"", "%20%20", ".git", ".GIT"} {
		if code := call("/api/v1/verdicts/by-signature/deadbeef?repository="+repo, "deadbeef"); code != http.StatusBadRequest {
			t.Fatalf("repository=%s status = %d, want 400", repo, code)
		}
	}
}

func TestCreate_TrailingBytesMaxBytesReturns413(t *testing.T) {
	h := &Handlers{
		Store:     &verdicts.Store{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthzMode: authapi.AuthzEnforce,
	}

	prefix := `{"repository":"mattermost/mattermost","verdicts":[{"label":"flaky"}]}`
	padLen := MaxCreateBodyBytes - len(prefix) + 64
	payload := append([]byte(prefix), bytes.Repeat([]byte(" "), padLen)...)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/verdicts", bytes.NewReader(payload))
	req = req.WithContext(authapi.WithSubject(req.Context(), authapi.Subject{
		Kind: "apikey",
		Role: policy.RoleUploader,
	}))
	rec := httptest.NewRecorder()
	h.Create(rec, req)

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
}

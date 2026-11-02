package authapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

func roleProtected(allowed ...policy.Role) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return RequireRole(allowed...)(inner)
}

func requestAs(role policy.Role) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	return req.WithContext(context.WithValue(req.Context(), subjectKey, Subject{Kind: "apikey", Role: role}))
}

func TestRequireRole_allowedRolePasses(t *testing.T) {
	for _, role := range WriteRoles {
		rec := httptest.NewRecorder()
		roleProtected(WriteRoles...).ServeHTTP(rec, requestAs(role))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", role, rec.Code)
		}
	}
}

func TestRequireRole_viewerCannotWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	roleProtected(WriteRoles...).ServeHTTP(rec, requestAs(policy.RoleViewer))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireRole_uploaderCannotRead(t *testing.T) {
	rec := httptest.NewRecorder()
	roleProtected(ReadRoles...).ServeHTTP(rec, requestAs(policy.RoleUploader))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireRole_withoutSubjectIs401(t *testing.T) {
	// RequireRole runs after RequireAuth; without a subject it must not let the request through.
	rec := httptest.NewRecorder()
	roleProtected(WriteRoles...).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/protected", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestParseKeyRole(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want policy.Role
	}{
		{"uploader", policy.RoleUploader},
		{"viewer", policy.RoleViewer},
		{"Editor", policy.RoleEditor},
		{" admin ", policy.RoleAdmin}, // surrounding spaces from a CLI flag are trimmed
	} {
		if got, ok := ParseKeyRole(tc.in); !ok || got != tc.want {
			t.Errorf("ParseKeyRole(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
	for _, in := range []string{"", "denied", "contributor", "root"} {
		if _, ok := ParseKeyRole(in); ok {
			t.Errorf("ParseKeyRole(%q) accepted; want rejected", in)
		}
	}
}

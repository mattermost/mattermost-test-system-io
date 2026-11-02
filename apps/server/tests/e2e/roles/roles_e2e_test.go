//go:build e2e
// +build e2e

// Package rolese2e checks who may write: uploader, editor and admin subjects can upload and delete; a
// viewer — API key, OIDC policy or browser session — authenticates but gets 403 on every write, and can
// still use the authenticated reads.

package rolese2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func do(t *testing.T, method, url, header, value string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(header, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestAPIKeyRolesOnWrites(t *testing.T) {
	env := testenv.Start(t)
	env.DefaultReportGroup(t)

	for _, tc := range []struct {
		role policy.Role
		want int
	}{
		{policy.RoleUploader, http.StatusOK},
		{policy.RoleEditor, http.StatusOK},
		{policy.RoleAdmin, http.StatusOK},
		{policy.RoleViewer, http.StatusForbidden},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			key := env.IssueAPIKeyWithRole(t, "key-"+string(tc.role), tc.role)
			if got := env.RegisterStatelessUploadWith(t, "X-API-Key", key, nil); got.StatusCode != tc.want {
				t.Errorf("register: status = %d, want %d (body %s)", got.StatusCode, tc.want, got.Body)
			}
			deleteWant := http.StatusNoContent
			if tc.want == http.StatusForbidden {
				deleteWant = http.StatusForbidden
			}
			if got := do(t, http.MethodDelete, env.ServerURL+"/api/v1/reports/"+uuid.NewString(), "X-API-Key", key); got != deleteWant {
				t.Errorf("delete: status = %d, want %d", got, deleteWant)
			}
		})
	}
}

func TestViewerKeyKeepsAuthenticatedReads(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKeyWithRole(t, "alpha-ai", policy.RoleViewer)
	// An unknown artifact: authenticated and allowed, so not found rather than 401 or 403.
	if got := do(t, http.MethodGet, env.ServerURL+"/api/v1/artifacts/"+uuid.NewString(), "X-API-Key", key); got != http.StatusNotFound {
		t.Errorf("artifact read: status = %d, want 404", got)
	}
}

func TestOIDCViewerPolicyCannotWrite(t *testing.T) {
	env := testenv.Start(t)
	env.DefaultReportGroup(t)
	env.InsertPolicy(t, "view-only", 1, "viewer", map[string]string{"repository_owner": "mattermost"})

	tok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/mm-e2e:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      "mattermost/mm-e2e",
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})
	if got := env.RegisterStatelessUpload(t, "Bearer "+tok, nil); got.StatusCode != http.StatusForbidden {
		t.Errorf("register as an OIDC viewer: status = %d, want 403 (body %s)", got.StatusCode, got.Body)
	}
}

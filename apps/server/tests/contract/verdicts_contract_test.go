//go:build e2e
// +build e2e

package contract

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestVerdictsContract(t *testing.T) {
	env := testenv.Start(t, testenv.WithAuthzMode(authapi.AuthzEnforce), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "uploader", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": "mattermost",
		"workflow":         "ci",
	})
	env.InsertPolicy(t, "viewer", 2, string(policy.RoleViewer), map[string]string{
		"repository_owner": "mattermost",
		"workflow":         "viewer",
	})

	doc, opts := loadSpec(t)
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	c := &specClient{env: env, doc: doc, router: router, opts: opts}

	apiKey := env.IssueAPIKey(t, "contract-verdicts")
	hdrs := map[string]string{"X-API-Key": apiKey}

	fp := strings.Repeat("e", 64)
	fpOld := strings.Repeat("d", 64)
	var sigCanon uuid.UUID
	if err := env.Pool.QueryRow(t.Context(), `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'contract failure', now())
		RETURNING id
	`, fp, errorsig.AlgoVersion).Scan(&sigCanon); err != nil {
		t.Fatalf("seed signature: %v", err)
	}
	if _, err := env.Pool.Exec(t.Context(), `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at, merged_into_id)
		VALUES ($1, 1, 'old contract failure', now(), $2)
	`, fpOld, sigCanon); err != nil {
		t.Fatalf("seed merged signature: %v", err)
	}

	// Missing verdict -> 404 conforming to OpenAPI
	exchange(t, c, http.MethodGet, "/api/v1/verdicts/by-signature/"+fp, "", nil, nil, http.StatusNotFound, true)

	// Invalid window_days -> 400 conforming to OpenAPI (validateReq=false since request intentionally violates schema)
	exchange(t, c, http.MethodGet, "/api/v1/verdicts/by-signature/"+fp+"?window_days=999", "", nil, nil, http.StatusBadRequest, false)

	// Create batch -> 200 conforming to OpenAPI (never includes error_context)
	postBody := jsonBody(t, map[string]any{
		"repository": "mattermost/mattermost",
		"verdicts": []any{
			map[string]any{
				"fingerprint": fp,
				"label":       "flaky",
				"confidence":  0.95,
				"error_context": map[string]any{
					"token": "ghp_1234567890abcdef1234567890abcdef",
				},
			},
		},
	})

	// Unauthenticated -> 401
	exchange(t, c, http.MethodPost, "/api/v1/verdicts", "application/json", postBody, nil, http.StatusUnauthorized, true)

	// Viewer OIDC -> 403 conforming to OpenAPI
	viewerTok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/mattermost:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      "mattermost/mattermost",
		RepositoryOwner: "mattermost",
		Workflow:        "viewer",
		Ref:             "refs/heads/main",
	})
	exchange(t, c, http.MethodPost, "/api/v1/verdicts", "application/json", postBody, map[string]string{"Authorization": "Bearer " + viewerTok}, http.StatusForbidden, true)

	// Wrong-repo uploader OIDC -> 403 conforming to OpenAPI
	wrongRepoTok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/other:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      "mattermost/other",
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})
	exchange(t, c, http.MethodPost, "/api/v1/verdicts", "application/json", postBody, map[string]string{"Authorization": "Bearer " + wrongRepoTok}, http.StatusForbidden, true)

	// Authorized API key -> 200 conforming to OpenAPI
	exchange(t, c, http.MethodPost, "/api/v1/verdicts", "application/json", postBody, hdrs, http.StatusOK, true)

	// Lookup canonical & merged_into_id ancestor -> 200 conforming to OpenAPI
	exchange(t, c, http.MethodGet, "/api/v1/verdicts/by-signature/"+fp+"?window_days=90", "", nil, nil, http.StatusOK, true)
	exchange(t, c, http.MethodGet, "/api/v1/verdicts/by-signature/"+fpOld+"?window_days=90", "", nil, nil, http.StatusOK, true)
}

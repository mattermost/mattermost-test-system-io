//go:build e2e
// +build e2e

package contract

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestVerdictsContract(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	doc, opts := loadSpec(t)
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	c := &specClient{env: env, doc: doc, router: router, opts: opts}

	apiKey := env.IssueAPIKey(t, "contract-verdicts")
	hdrs := map[string]string{"X-API-Key": apiKey}

	fp := strings.Repeat("e", 64)
	if _, err := env.Pool.Exec(t.Context(), `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'contract failure', now())
	`, fp, errorsig.AlgoVersion); err != nil {
		t.Fatalf("seed signature: %v", err)
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
	exchange(t, c, http.MethodPost, "/api/v1/verdicts", "application/json", postBody, hdrs, http.StatusOK, true)

	// Lookup -> 200 conforming to OpenAPI
	exchange(t, c, http.MethodGet, "/api/v1/verdicts/by-signature/"+fp+"?window_days=90", "", nil, nil, http.StatusOK, true)
}

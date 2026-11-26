//go:build e2e
// +build e2e

package verdictse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	defaultOwner = "mattermost"
	repoA        = "mattermost/repo-a"
	repoB        = "mattermost/repo-b"
	wfUploader   = "ci"
	wfViewer     = "viewer-wf"
	commitSHA    = "deadbeefcafebabe1234567890abcdefdeadbeef"
)

func startEnforced(t *testing.T) *testenv.Env {
	t.Helper()
	env := testenv.Start(t, testenv.WithAuthzMode(authapi.AuthzEnforce), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "uploader", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultOwner,
		"workflow":         wfUploader,
	})
	env.InsertPolicy(t, "viewer", 2, string(policy.RoleViewer), map[string]string{
		"repository_owner": defaultOwner,
		"workflow":         wfViewer,
	})
	return env
}

func oidcToken(t *testing.T, env *testenv.Env, repo, workflow string) string {
	t.Helper()
	return env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:" + repo + ":ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      repo,
		RepositoryOwner: defaultOwner,
		Workflow:        workflow,
		Ref:             "refs/heads/main",
	})
}

func doJSON(t *testing.T, method, url, bearer, apiKey string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func seedGroup(t *testing.T, env *testenv.Env, repo, runID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright', 'default', $1, 'main', $2, $3, '1')
		RETURNING id
	`, repo, commitSHA, runID).Scan(&id)
	if err != nil {
		t.Fatalf("seed report_group: %v", err)
	}
	return id
}

func seedTest(t *testing.T, env *testenv.Env, repo, key string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO tests (repository, framework, test_key, file, full_title)
		VALUES ($1, 'playwright', $2, 'e2e/login.spec.ts', 'Login test')
		RETURNING id
	`, repo, key).Scan(&id)
	if err != nil {
		t.Fatalf("seed test: %v", err)
	}
	return id
}

func seedSignature(t *testing.T, env *testenv.Env, fp string, algo int16, mergedInto *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at, merged_into_id)
		VALUES ($1, $2, 'norm msg', now(), $3)
		RETURNING id
	`, fp, algo, mergedInto).Scan(&id)
	if err != nil {
		t.Fatalf("seed signature: %v", err)
	}
	return id
}

func TestMigration000035_UpDownAndIndexes(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	upBytes, err := migrations.FS.ReadFile("000035_verdicts.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	downBytes, err := migrations.FS.ReadFile("000035_verdicts.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if strings.Contains(strings.ToUpper(string(upBytes)), "CONCURRENTLY") {
		t.Fatalf("000035 up migration must not use CONCURRENTLY")
	}

	var uqName, mergedIdx *string
	if err := env.Pool.QueryRow(ctx, `
		SELECT
			(SELECT indexname FROM pg_indexes WHERE tablename = 'verdicts' AND indexname = 'verdicts_current_uq'),
			(SELECT indexname FROM pg_indexes WHERE tablename = 'failure_signatures' AND indexname = 'failure_signatures_merged_into_idx')
	`).Scan(&uqName, &mergedIdx); err != nil {
		t.Fatalf("check indexes: %v", err)
	}
	if uqName == nil || mergedIdx == nil {
		t.Fatalf("missing expected indexes: verdicts_current_uq=%v failure_signatures_merged_into_idx=%v", uqName, mergedIdx)
	}

	if _, err := env.Pool.Exec(ctx, string(downBytes)); err != nil {
		t.Fatalf("exec down migration: %v", err)
	}
	var verdictsReg *string
	if err := env.Pool.QueryRow(ctx, `SELECT to_regclass('public.verdicts')`).Scan(&verdictsReg); err != nil {
		t.Fatalf("check dropped table: %v", err)
	}
	if verdictsReg != nil {
		t.Fatalf("expected verdicts table dropped, got %v", *verdictsReg)
	}

	if _, err := env.Pool.Exec(ctx, string(upBytes)); err != nil {
		t.Fatalf("re-exec up migration: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT to_regclass('public.verdicts')`).Scan(&verdictsReg); err != nil {
		t.Fatalf("check recreated table: %v", err)
	}
	if verdictsReg == nil {
		t.Fatal("expected verdicts table recreated")
	}
}

func TestVerdicts_AuthzAndRepoMatch(t *testing.T) {
	env := startEnforced(t)
	rgA := seedGroup(t, env, repoA, "run-authz-a")
	rgB := seedGroup(t, env, repoB, "run-authz-b")
	testB := seedTest(t, env, repoB, "h:test-b")
	sigID := seedSignature(t, env, strings.Repeat("a", 64), errorsig.AlgoVersion, nil)

	uploaderA := oidcToken(t, env, repoA, wfUploader)
	viewerA := oidcToken(t, env, repoA, wfViewer)
	apiKey := env.IssueAPIKey(t, "ci-verdicts")

	payload := map[string]any{
		"repository":      repoA,
		"report_group_id": rgA.String(),
		"verdicts": []any{
			map[string]any{"signature_id": sigID.String(), "label": "flaky"},
		},
	}

	t.Run("anonymous gets 401", func(t *testing.T) {
		code, _ := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", "", "", payload)
		if code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", code)
		}
	})

	t.Run("viewer OIDC gets 403", func(t *testing.T) {
		code, _ := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", viewerA, "", payload)
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
	})

	t.Run("wrong repo OIDC gets 403", func(t *testing.T) {
		wrongBody := map[string]any{
			"repository":      repoB,
			"report_group_id": rgB.String(),
			"verdicts":        []any{map[string]any{"signature_id": sigID.String(), "label": "flaky"}},
		}
		code, _ := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", uploaderA, "", wrongBody)
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
	})

	t.Run("cross-repo report_group_id and test_id get 403", func(t *testing.T) {
		crossRG := map[string]any{
			"repository":      repoA,
			"report_group_id": rgB.String(),
			"verdicts":        []any{map[string]any{"signature_id": sigID.String(), "label": "flaky"}},
		}
		code, _ := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", uploaderA, "", crossRG)
		if code != http.StatusForbidden {
			t.Fatalf("cross-repo report_group_id status = %d, want 403", code)
		}

		crossTest := map[string]any{
			"repository":      repoA,
			"report_group_id": rgA.String(),
			"verdicts":        []any{map[string]any{"test_id": testB.String(), "signature_id": sigID.String(), "label": "flaky"}},
		}
		code, _ = doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", uploaderA, "", crossTest)
		if code != http.StatusForbidden {
			t.Fatalf("cross-repo test_id status = %d, want 403", code)
		}
	})

	t.Run("normalized OIDC repo and API key succeed", func(t *testing.T) {
		normBody := map[string]any{
			"repository":      "Mattermost/Repo-A.git",
			"report_group_id": rgA.String(),
			"verdicts":        []any{map[string]any{"signature_id": sigID.String(), "label": "caused by PR"}},
		}
		code, body := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", uploaderA, "", normBody)
		if code != http.StatusOK {
			t.Fatalf("normalized repo status = %d, want 200 (body=%s)", code, body)
		}

		code, body = doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", "", apiKey, payload)
		if code != http.StatusOK {
			t.Fatalf("api key status = %d, want 200 (body=%s)", code, body)
		}
	})
}

func TestVerdicts_IdempotencySupersedeAndConcurrency(t *testing.T) {
	env := startEnforced(t)
	ctx := t.Context()
	rg := seedGroup(t, env, repoA, "run-idem")
	testID := seedTest(t, env, repoA, "h:idem-test")
	fp := strings.Repeat("b", 64)
	sigID := seedSignature(t, env, fp, errorsig.AlgoVersion, nil)
	tok := oidcToken(t, env, repoA, wfUploader)

	reqBody := map[string]any{
		"repository":      repoA,
		"report_group_id": rg.String(),
		"verdicts": []any{
			map[string]any{
				"test_id":     testID.String(),
				"fingerprint": fp,
				"label":       "broken on main",
				"decided_by":  "AI (claude-sonnet)",
				"rule":        "main-match",
				"confidence":  0.88,
				"summary":     "Also failed on main",
				"model":       "claude-sonnet-4-6",
				"triage_url":  "https://github.com/mattermost/repo-a/actions/runs/1",
			},
		},
	}

	type createResp struct {
		Verdicts []struct {
			ID           uuid.UUID  `json:"id"`
			SignatureID  *uuid.UUID `json:"signature_id"`
			Label        string     `json:"label"`
			Created      bool       `json:"created"`
			SupersededID *uuid.UUID `json:"superseded_id"`
		} `json:"verdicts"`
	}

	// 1. Initial insert
	code, raw1 := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", reqBody)
	if code != http.StatusOK {
		t.Fatalf("initial post = %d: %s", code, raw1)
	}
	var resp1 createResp
	if err := json.Unmarshal(raw1, &resp1); err != nil {
		t.Fatalf("decode resp1: %v", err)
	}
	if len(resp1.Verdicts) != 1 || !resp1.Verdicts[0].Created || resp1.Verdicts[0].SupersededID != nil {
		t.Fatalf("unexpected initial verdict response: %s", raw1)
	}
	if resp1.Verdicts[0].SignatureID == nil || *resp1.Verdicts[0].SignatureID != sigID {
		t.Fatalf("signature_id = %v, want %s", resp1.Verdicts[0].SignatureID, sigID)
	}
	if resp1.Verdicts[0].Label != "broken on main" {
		t.Fatalf("label = %q, want verbatim 'broken on main'", resp1.Verdicts[0].Label)
	}

	// 2. Idempotent replay
	code, raw2 := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", reqBody)
	if code != http.StatusOK {
		t.Fatalf("replay post = %d: %s", code, raw2)
	}
	var resp2 createResp
	if err := json.Unmarshal(raw2, &resp2); err != nil {
		t.Fatalf("decode resp2: %v", err)
	}
	if resp2.Verdicts[0].ID != resp1.Verdicts[0].ID || resp2.Verdicts[0].Created || resp2.Verdicts[0].SupersededID != nil {
		t.Fatalf("expected idempotent replay of %s with created=false, got %s", resp1.Verdicts[0].ID, raw2)
	}

	// 3. Supersede with changed label
	reqBody["verdicts"].([]any)[0].(map[string]any)["label"] = "caused by PR"
	code, raw3 := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", reqBody)
	if code != http.StatusOK {
		t.Fatalf("supersede post = %d: %s", code, raw3)
	}
	var resp3 createResp
	if err := json.Unmarshal(raw3, &resp3); err != nil {
		t.Fatalf("decode resp3: %v", err)
	}
	if !resp3.Verdicts[0].Created || resp3.Verdicts[0].SupersededID == nil || *resp3.Verdicts[0].SupersededID != resp1.Verdicts[0].ID {
		t.Fatalf("expected supersede of %s, got %s", resp1.Verdicts[0].ID, raw3)
	}

	var activeCount int
	if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM verdicts WHERE report_group_id = $1 AND superseded_at IS NULL`, rg).Scan(&activeCount); err != nil {
		t.Fatalf("count active verdicts: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("activeCount = %d, want 1", activeCount)
	}

	// 4. Validation errors return 400
	badCases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "unknown fingerprint",
			body: map[string]any{
				"repository":      repoA,
				"report_group_id": rg.String(),
				"verdicts":        []any{map[string]any{"fingerprint": "unknown-fp", "label": "flaky"}},
			},
		},
		{
			name: "unknown reused_from_id",
			body: map[string]any{
				"repository": repoA,
				"verdicts":   []any{map[string]any{"label": "flaky", "reused_from_id": uuid.NewString()}},
			},
		},
		{
			name: "intra-batch duplicate",
			body: map[string]any{
				"repository":      repoA,
				"report_group_id": rg.String(),
				"verdicts": []any{
					map[string]any{"test_id": testID.String(), "signature_id": sigID.String(), "label": "flaky"},
					map[string]any{"test_id": testID.String(), "fingerprint": fp, "label": "app bug"},
				},
			},
		},
	}
	for _, tc := range badCases {
		code, body := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", tc.body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", tc.name, code, body)
		}
	}

	// 5. Concurrent upserts with -race
	rgConc := seedGroup(t, env, repoA, "run-conc")
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			lbl := "flaky"
			if idx%2 == 1 {
				lbl = "test bug"
			}
			b := map[string]any{
				"repository":      repoA,
				"report_group_id": rgConc.String(),
				"verdicts": []any{
					map[string]any{"test_id": testID.String(), "signature_id": sigID.String(), "label": lbl},
				},
			}
			c, raw := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", b)
			if c != http.StatusOK {
				errs <- fmt.Errorf("worker %d status=%d body=%s", idx, c, raw)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var concActive int
	if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM verdicts WHERE report_group_id = $1 AND superseded_at IS NULL`, rgConc).Scan(&concActive); err != nil {
		t.Fatalf("count concurrent active: %v", err)
	}
	if concActive != 1 {
		t.Fatalf("concurrent activeCount = %d, want 1", concActive)
	}
}

func TestVerdicts_ReportGroupNullAndOnDeleteSetNull(t *testing.T) {
	env := startEnforced(t)
	ctx := t.Context()
	rg1 := seedGroup(t, env, repoA, "run-del-1")
	rg2 := seedGroup(t, env, repoA, "run-del-2")
	testID := seedTest(t, env, repoA, "h:del-test")
	sigID := seedSignature(t, env, strings.Repeat("c", 64), errorsig.AlgoVersion, nil)
	tok := oidcToken(t, env, repoA, wfUploader)

	for _, rg := range []uuid.UUID{rg1, rg2} {
		code, body := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", map[string]any{
			"repository":      repoA,
			"report_group_id": rg.String(),
			"verdicts": []any{
				map[string]any{"test_id": testID.String(), "signature_id": sigID.String(), "label": "flaky"},
			},
		})
		if code != http.StatusOK {
			t.Fatalf("seed verdict for %s failed: %d %s", rg, code, body)
		}
	}

	// Deleting both report_groups sets report_group_id = NULL on both verdicts without 23505.
	if _, err := env.Pool.Exec(ctx, `DELETE FROM report_groups WHERE id IN ($1, $2)`, rg1, rg2); err != nil {
		t.Fatalf("delete report_groups: %v", err)
	}
	var nullCount int
	if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM verdicts WHERE test_id = $1 AND report_group_id IS NULL AND superseded_at IS NULL`, testID).Scan(&nullCount); err != nil {
		t.Fatalf("count null report_group_id verdicts: %v", err)
	}
	if nullCount != 2 {
		t.Fatalf("nullCount = %d, want 2", nullCount)
	}

	// Standalone lane-level verdict with report_group_id: null, test_id: null, signature_id: null
	code, body := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", map[string]any{
		"repository": repoA,
		"verdicts": []any{
			map[string]any{"label": "all specs ran", "summary": "Lane healthy"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("standalone lane verdict failed: %d %s", code, body)
	}
}

func TestVerdicts_ErrorContextRedactionAndNonLeakage(t *testing.T) {
	env := startEnforced(t)
	ctx := t.Context()
	rg := seedGroup(t, env, repoA, "run-redact")
	fp := strings.Repeat("d", 64)
	sigID := seedSignature(t, env, fp, errorsig.AlgoVersion, nil)
	tok := oidcToken(t, env, repoA, wfUploader)

	secrets := []string{
		"ghp_1234567890abcdef1234567890abcdef",
		"s3cr3tpass",
		"X-Amz-Signature=deadbeef",
		"dev@mattermost.com",
		"top-secret-header",
		"raw-openai-secret-e2e",
		"secret-in-auth-obj-e2e",
		"secret-in-pw-arr-e2e",
	}

	code, postRaw := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", map[string]any{
		"repository":      repoA,
		"report_group_id": rg.String(),
		"verdicts": []any{
			map[string]any{
				"signature_id": sigID.String(),
				"label":        "machine failure",
				"error_context": map[string]any{
					"authorization":  "Bearer top-secret-header",
					"email":          "dev@mattermost.com",
					"openai_api_key": "raw-openai-secret-e2e",
					"auth":           map[string]any{"raw": "secret-in-auth-obj-e2e", "expires_in": 3600},
					"password":       []string{"secret-in-pw-arr-e2e"},
					"url":            "https://ci:s3cr3tpass@minio.local/bucket/obj?X-Amz-Signature=deadbeef",
					"snippet":        "token ghp_1234567890abcdef1234567890abcdef failed",
					"input_tokens":   512,
				},
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("post verdict status = %d: %s", code, postRaw)
	}

	var storedCtx string
	if err := env.Pool.QueryRow(ctx, `SELECT error_context::text FROM verdicts WHERE report_group_id = $1`, rg).Scan(&storedCtx); err != nil {
		t.Fatalf("query stored error_context: %v", err)
	}
	for _, s := range secrets {
		if strings.Contains(storedCtx, s) {
			t.Errorf("stored error_context leaked %q: %s", s, storedCtx)
		}
	}
	if !strings.Contains(storedCtx, `"input_tokens": 512`) && !strings.Contains(storedCtx, `"input_tokens":512`) {
		t.Errorf("stored error_context dropped safe numeric field: %s", storedCtx)
	}

	code, getRaw := doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fp, "", "", nil)
	if code != http.StatusOK {
		t.Fatalf("get by signature status = %d: %s", code, getRaw)
	}
	for _, respBody := range []string{string(postRaw), string(getRaw)} {
		if strings.Contains(respBody, "error_context") {
			t.Fatalf("HTTP response leaked error_context key: %s", respBody)
		}
		for _, s := range secrets {
			if strings.Contains(respBody, s) {
				t.Fatalf("HTTP response leaked secret %q: %s", s, respBody)
			}
		}
	}
}

func TestVerdicts_LookupMergedLineageWindowAndReuse(t *testing.T) {
	env := startEnforced(t)
	ctx := t.Context()
	tok := oidcToken(t, env, repoA, wfUploader)

	// Build lineage: s1 -> s2 -> s3 (canonical), plus sibling s4 -> s3.
	fp1 := strings.Repeat("1", 64)
	fp2 := strings.Repeat("2", 64)
	fp3 := strings.Repeat("3", 64)
	fp4 := strings.Repeat("4", 64)
	s3 := seedSignature(t, env, fp3, errorsig.AlgoVersion, nil)
	s2 := seedSignature(t, env, fp2, 1, &s3)
	s1 := seedSignature(t, env, fp1, 1, &s2)
	s4 := seedSignature(t, env, fp4, 1, &s3)
	_, _ = s1, s4

	// Case A: verdict recorded on canonical s3, queried via old fp1 and sibling fp4.
	rg1 := seedGroup(t, env, repoA, "run-lineage-1")
	code, postRaw := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", map[string]any{
		"repository":      repoA,
		"report_group_id": rg1.String(),
		"verdicts": []any{
			map[string]any{"signature_id": s3.String(), "label": "app bug", "summary": "Known bug"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("post canonical verdict: %d %s", code, postRaw)
	}
	var postResp struct {
		Verdicts []struct {
			ID uuid.UUID `json:"id"`
		} `json:"verdicts"`
	}
	if err := json.Unmarshal(postRaw, &postResp); err != nil {
		t.Fatalf("decode postResp: %v", err)
	}
	firstVerdictID := postResp.Verdicts[0].ID

	for _, lookupFP := range []string{fp1, fp2, fp3, fp4} {
		c, raw := doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+lookupFP, "", "", nil)
		if c != http.StatusOK {
			t.Fatalf("lookup %s status = %d: %s", lookupFP, c, raw)
		}
		var v struct {
			ID          uuid.UUID `json:"id"`
			Fingerprint string    `json:"fingerprint"`
			Label       string    `json:"label"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("decode lookup %s: %v", lookupFP, err)
		}
		if v.ID != firstVerdictID || v.Fingerprint != fp3 || v.Label != "app bug" {
			t.Fatalf("lookup %s got %+v, want id=%s fp=%s", lookupFP, v, firstVerdictID, fp3)
		}
	}

	// Case B: reused_from_id round-trip on old s1, queried from canonical fp3.
	rg2 := seedGroup(t, env, repoA, "run-lineage-2")
	reusedStr := firstVerdictID.String()
	code, reuseRaw := doJSON(t, http.MethodPost, env.ServerURL+"/api/v1/verdicts", tok, "", map[string]any{
		"repository":      repoA,
		"report_group_id": rg2.String(),
		"verdicts": []any{
			map[string]any{
				"fingerprint":    fp1,
				"label":          "app bug",
				"reused_from_id": reusedStr,
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("post reused verdict: %d %s", code, reuseRaw)
	}
	var reusePost struct {
		Verdicts []struct {
			ID           uuid.UUID  `json:"id"`
			ReusedFromID *uuid.UUID `json:"reused_from_id"`
		} `json:"verdicts"`
	}
	if err := json.Unmarshal(reuseRaw, &reusePost); err != nil {
		t.Fatalf("decode reusePost: %v", err)
	}
	reusedVerdictID := reusePost.Verdicts[0].ID
	if reusePost.Verdicts[0].ReusedFromID == nil || *reusePost.Verdicts[0].ReusedFromID != firstVerdictID {
		t.Fatalf("reused_from_id = %v, want %s", reusePost.Verdicts[0].ReusedFromID, firstVerdictID)
	}

	// Canonical lookup returns latest (the reused verdict on s1)
	code, latestRaw := doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fp3, "", "", nil)
	if code != http.StatusOK {
		t.Fatalf("lookup fp3 after reuse: %d %s", code, latestRaw)
	}
	var gotLatest struct {
		ID           uuid.UUID  `json:"id"`
		ReusedFromID *uuid.UUID `json:"reused_from_id"`
	}
	if err := json.Unmarshal(latestRaw, &gotLatest); err != nil {
		t.Fatalf("decode latestRaw: %v", err)
	}
	if gotLatest.ID != reusedVerdictID || gotLatest.ReusedFromID == nil || *gotLatest.ReusedFromID != firstVerdictID {
		t.Fatalf("gotLatest = %+v, want id=%s reused_from_id=%s", gotLatest, reusedVerdictID, firstVerdictID)
	}

	// Repository query filter excludes other repo
	code, _ = doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fp3+"?repository="+repoB, "", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("wrong-repo lookup status = %d, want 404", code)
	}

	// Window expiry: age both verdicts to 91 days ago -> default 90d returns 404, window_days=100 returns 200.
	if _, err := env.Pool.Exec(ctx, `UPDATE verdicts SET created_at = now() - interval '91 days' WHERE id IN ($1, $2)`, firstVerdictID, reusedVerdictID); err != nil {
		t.Fatalf("age verdicts: %v", err)
	}
	code, _ = doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fp3, "", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("expired 90d lookup status = %d, want 404", code)
	}
	code, _ = doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fp3+"?window_days=100", "", "", nil)
	if code != http.StatusOK {
		t.Fatalf("window_days=100 lookup status = %d, want 200", code)
	}

	// Cycle safety: introduce cycle between cycA <-> cycB and verify lookup terminates cleanly.
	fpCycA := strings.Repeat("f", 64)
	fpCycB := strings.Repeat("9", 64)
	cycA := seedSignature(t, env, fpCycA, errorsig.AlgoVersion, nil)
	cycB := seedSignature(t, env, fpCycB, errorsig.AlgoVersion, &cycA)
	if _, err := env.Pool.Exec(ctx, `UPDATE failure_signatures SET merged_into_id = $1 WHERE id = $2`, cycB, cycA); err != nil {
		t.Fatalf("create signature cycle: %v", err)
	}
	code, _ = doJSON(t, http.MethodGet, env.ServerURL+"/api/v1/verdicts/by-signature/"+fpCycA, "", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("cycle lookup status = %d, want 404", code)
	}
}

func TestReportsCases_ExposesTestAndSignatureIdentity(t *testing.T) {
	env := startEnforced(t)
	env.InsertPolicy(t, "allow-all-ci", 3, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultOwner,
	})
	tok := oidcToken(t, env, repoA, wfUploader)

	pwPayload := `{
	  "suites": [{
	    "title": "login.spec.ts",
	    "file": "e2e/login.spec.ts",
	    "specs": [
	      {
	        "title": "MM-T101 failing test",
	        "tests": [{
	          "projectName": "chromium",
	          "status": "unexpected",
	          "results": [{
	            "status": "failed",
	            "duration": 100,
	            "retry": 0,
	            "errors": [{
	              "message": "TimeoutError: waiting for button 12345 exceeded 30000ms",
	              "stack": "TimeoutError: waiting for button 12345 exceeded 30000ms\n    at e2e/login.spec.ts:14:5"
	            }]
	          }]
	        }]
	      },
	      {
	        "title": "MM-T102 passing test",
	        "tests": [{
	          "projectName": "chromium",
	          "status": "expected",
	          "results": [{"status": "passed", "duration": 25, "retry": 0}]
	        }]
	      }
	    ]
	  }]
	}`

	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository": repoA,
		"framework":  "playwright",
		"gh_run_id":  "run-cases-identity",
		"json_files": []any{map[string]any{"path": "results.json", "size": len(pwPayload)}},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d: %s", reg.StatusCode, reg.Body)
	}
	var regBody struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &regBody); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}
	upBuf := &bytes.Buffer{}
	mw := multipart.NewWriter(upBuf)
	part, err := mw.CreateFormFile("files", "results.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(pwPayload)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	upURL := fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/json", env.ServerURL, regBody.ReportID, regBody.UploadID)
	req, err := http.NewRequest(http.MethodPost, upURL, upBuf)
	if err != nil {
		t.Fatalf("new upload req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload json: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload json status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		err := env.Pool.QueryRow(t.Context(), `SELECT status FROM reports WHERE id = $1`, regBody.UploadID).Scan(&status)
		if err == nil && status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	casesPath := fmt.Sprintf("/api/v1/reports/%s/cases", regBody.ReportID)
	cResp, err := http.Get(env.ServerURL + casesPath)
	if err != nil {
		t.Fatalf("GET cases: %v", err)
	}
	defer func() { _ = cResp.Body.Close() }()
	casesRaw, _ := io.ReadAll(cResp.Body)
	if cResp.StatusCode != http.StatusOK {
		t.Fatalf("GET cases status = %d: %s", cResp.StatusCode, casesRaw)
	}

	// Validate response against openapi.yaml
	validateCasesAgainstOpenAPI(t, casesPath, cResp.StatusCode, cResp.Header, casesRaw)

	var items []struct {
		Title                string     `json:"title"`
		Status               string     `json:"status"`
		TestID               *uuid.UUID `json:"test_id"`
		SignatureID          *uuid.UUID `json:"signature_id"`
		SignatureFingerprint *string    `json:"signature_fingerprint"`
	}
	if err := json.Unmarshal(casesRaw, &items); err != nil {
		t.Fatalf("unmarshal cases: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(cases) = %d, want 2: %s", len(items), casesRaw)
	}

	wantSig := errorsig.Compute(errorsig.Input{
		ErrorMessage: "TimeoutError: waiting for button 12345 exceeded 30000ms",
		ErrorStack:   "TimeoutError: waiting for button 12345 exceeded 30000ms\n    at e2e/login.spec.ts:14:5",
		Framework:    "playwright",
		Status:       "failed",
	})

	failItem, passItem := items[0], items[1]
	if failItem.Status != "failed" || failItem.TestID == nil || failItem.SignatureID == nil || failItem.SignatureFingerprint == nil {
		t.Fatalf("failed case missing identity fields: %+v", failItem)
	}
	if *failItem.SignatureFingerprint != wantSig.Fingerprint {
		t.Fatalf("signature_fingerprint = %q, want %q", *failItem.SignatureFingerprint, wantSig.Fingerprint)
	}
	if passItem.Status != "passed" || passItem.TestID == nil || passItem.SignatureID != nil || passItem.SignatureFingerprint != nil {
		t.Fatalf("passed case unexpected identity fields: %+v", passItem)
	}
}

func validateCasesAgainstOpenAPI(t *testing.T, path string, status int, hdr http.Header, body []byte) {
	t.Helper()
	loader := &openapi3.Loader{Context: t.Context()}
	doc, err := loader.LoadFromFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("validate openapi.yaml: %v", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, doc.Servers[0].URL+path, nil)
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find route: %v", err)
	}
	opts := &openapi3filter.Options{
		AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil },
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: params,
			Route:      route,
			Options:    opts,
		},
		Status:  status,
		Header:  hdr,
		Options: opts,
	}
	respIn.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Fatalf("cases response violates openapi.yaml: %v", err)
	}
}

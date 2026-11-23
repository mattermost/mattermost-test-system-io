//go:build e2e
// +build e2e

// Package authze2e exercises route-role and token-repository authorization
// (TSIO_AUTHZ_MODE off | log-only | enforce) against a real Postgres, the mock
// OIDC provider, API keys and human sessions.
package authze2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	owner      = "mattermost"
	repoA      = "mattermost/repo-a"
	repoB      = "mattermost/repo-b"
	wfUploader = "ci"
	wfAdmin    = "admin-wf"
	commit     = "deadbeefcafebabe1234567890abcdefdeadbeef"
)

// creds is how a request authenticates: exactly one field is set.
type creds struct{ bearer, apiKey, cookie string }

func start(t *testing.T, mode authapi.AuthzMode) *testenv.Env {
	t.Helper()
	env := testenv.Start(t, testenv.WithAuthzMode(mode))
	// Disjoint matchers, so policy ordering never matters.
	env.InsertPolicy(t, "uploader", 1, "uploader", map[string]string{"repository_owner": owner, "workflow": wfUploader})
	env.InsertPolicy(t, "admin", 1, "admin", map[string]string{"repository_owner": owner, "workflow": wfAdmin})
	return env
}

func oidcTok(t *testing.T, env *testenv.Env, repo, workflow string) creds {
	t.Helper()
	return creds{bearer: env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:" + repo + ":ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      repo,
		RepositoryOwner: owner,
		Workflow:        workflow,
		Ref:             "refs/heads/main",
	})}
}

// sessionFor inserts a user with the given role and returns a live session cookie.
func sessionFor(t *testing.T, env *testenv.Env, role string) creds {
	t.Helper()
	var uid uuid.UUID
	err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO users (github_id, github_login, role) VALUES ($1, $2, $3) RETURNING id`,
		time.Now().UnixNano(), "user-"+role+"-"+uuid.NewString()[:8], role).Scan(&uid)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// Insert directly: session.Manager.Issue binds its ip arg as text against
	// an inet column and fails on a real Postgres.
	tok := session.TokenPrefix + uuid.NewString()
	sum := sha256.Sum256([]byte(tok))
	_, err = env.Pool.Exec(context.Background(),
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		hex.EncodeToString(sum[:]), uid)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return creds{cookie: tok}
}

func do(t *testing.T, env *testenv.Env, c creds, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, env.ServerURL+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: c.cookie})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func expect(t *testing.T, what string, got int, body []byte, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status = %d, want %d, body=%s", what, got, want, body)
	}
}

func groupBody(repo string) map[string]any {
	return map[string]any{
		"repository": repo, "commit": commit, "gh_run_id": "run-1", "gh_run_attempt": "1",
		"framework": "playwright", "name": "default", "branch": "main",
		"total_reports_expected": 1,
	}
}

func registerBody(repo string) map[string]any {
	b := groupBody(repo)
	delete(b, "total_reports_expected")
	b["gh_job_id"] = "job-1"
	b["gh_job_name"] = "default"
	b["json_files"] = []any{}
	b["screenshots"] = []any{}
	return b
}

func orchBody(repo string) map[string]any {
	return map[string]any{
		"repository": repo, "commit_sha": commit, "gh_run_id": "run-1", "gh_run_attempt": "1",
		"name": "default", "branch": "main", "framework": "playwright",
		"total_reports_expected": 1,
		"dispatch_units":         []any{map[string]any{"spec_path": "a.spec.ts"}},
	}
}

func groupCount(t *testing.T, env *testenv.Env, repo string) int {
	t.Helper()
	var n int
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM report_groups WHERE repository = $1`, repo).Scan(&n); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	return n
}

func insertGroup(t *testing.T, env *testenv.Env, repo string) string {
	t.Helper()
	var id string
	err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright','default',$1,'main',$2,'seed-run','1') RETURNING id`, repo, commit).Scan(&id)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	return id
}

func groupExists(t *testing.T, env *testenv.Env, id string) bool {
	t.Helper()
	var n int
	if err := env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM report_groups WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("group exists: %v", err)
	}
	return n == 1
}

func TestEnforce(t *testing.T) {
	env := start(t, authapi.AuthzEnforce)

	t.Run("oidc own repo succeeds on begin/register/orchestration begin", func(t *testing.T) {
		tok := oidcTok(t, env, repoA, wfUploader)
		for _, c := range []struct {
			path string
			body map[string]any
			want int
		}{
			{"/api/v1/reports/begin", groupBody(repoA), http.StatusOK},
			{"/api/v1/reports/register", registerBody(repoA), http.StatusOK},
			{"/api/v1/orchestration/begin", orchBody(repoA), http.StatusCreated},
		} {
			code, body := do(t, env, tok, http.MethodPost, c.path, c.body)
			expect(t, c.path, code, body, c.want)
		}
	})

	t.Run("oidc repo match is case-insensitive in both directions", func(t *testing.T) {
		upper := oidcTok(t, env, "Mattermost/Repo-Case", wfUploader)
		b := groupBody("mattermost/repo-case")
		code, body := do(t, env, upper, http.MethodPost, "/api/v1/reports/begin", b)
		expect(t, "upper token, lower body", code, body, http.StatusOK)

		lower := oidcTok(t, env, "mattermost/repo-case2", wfUploader)
		b = groupBody("MATTERMOST/REPO-CASE2")
		code, body = do(t, env, lower, http.MethodPost, "/api/v1/reports/begin", b)
		expect(t, "lower token, upper body", code, body, http.StatusOK)
	})

	t.Run("oidc wrong repo gets 403 and writes nothing", func(t *testing.T) {
		tok := oidcTok(t, env, repoA, wfUploader)
		for _, c := range []struct {
			path string
			body map[string]any
		}{
			{"/api/v1/reports/begin", groupBody(repoB)},
			{"/api/v1/reports/register", registerBody(repoB)},
			{"/api/v1/orchestration/begin", orchBody(repoB)},
		} {
			code, body := do(t, env, tok, http.MethodPost, c.path, c.body)
			expect(t, c.path, code, body, http.StatusForbidden)
		}
		if n := groupCount(t, env, repoB); n != 0 {
			t.Fatalf("wrong-repo requests created %d report_groups rows for %s", n, repoB)
		}
	})

	t.Run("repo-b token cannot upload into or drive repo-a work", func(t *testing.T) {
		// Same OIDC subject, different repository claim: owner checks pass,
		// only the repository binding can stop it.
		sub := "repo:shared:ref:refs/heads/main"
		mint := func(repo string) creds {
			return creds{bearer: env.Mock.IssueToken(t, oidcmock.Claims{
				Subject: sub, Audience: "tsio", Repository: repo,
				RepositoryOwner: owner, Workflow: wfUploader, Ref: "refs/heads/main",
			})}
		}
		a, b := mint("mattermost/repo-x"), mint("mattermost/repo-y")

		code, body := do(t, env, a, http.MethodPost, "/api/v1/reports/register", registerBody("mattermost/repo-x"))
		expect(t, "register x", code, body, http.StatusOK)
		var reg struct {
			ReportID string `json:"report_id"`
			UploadID string `json:"upload_id"`
		}
		if err := json.Unmarshal(body, &reg); err != nil {
			t.Fatalf("decode register: %v", err)
		}
		for _, leaf := range []string{"json", "screenshots"} {
			code, body = do(t, env, b, http.MethodPost, "/api/v1/reports/upload/"+reg.ReportID+"/"+reg.UploadID+"/"+leaf, nil)
			expect(t, "upload "+leaf+" as repo-y", code, body, http.StatusForbidden)
		}

		code, body = do(t, env, a, http.MethodPost, "/api/v1/orchestration/begin", orchBody("mattermost/repo-x"))
		expect(t, "orch begin x", code, body, http.StatusCreated)
		worker := map[string]any{"gh_job_name": "w", "gh_job_id": "j1", "batch_size": 1}
		for _, path := range []string{"checkout", "complete", "no-work"} {
			req := orchBody("mattermost/repo-x")
			delete(req, "dispatch_units")
			for k, v := range worker {
				req[k] = v
			}
			req["results"] = []any{map[string]any{"spec_path": "a.spec.ts", "status": "passed"}}
			code, body = do(t, env, b, http.MethodPost, "/api/v1/orchestration/"+path, req)
			expect(t, "orchestration "+path+" as repo-y", code, body, http.StatusForbidden)
		}
	})

	t.Run("admin oidc bypasses repo binding", func(t *testing.T) {
		tok := oidcTok(t, env, repoA, wfAdmin)
		code, body := do(t, env, tok, http.MethodPost, "/api/v1/reports/begin", groupBody("mattermost/repo-admin-target"))
		expect(t, "admin oidc cross-repo begin", code, body, http.StatusOK)
	})

	t.Run("uploader oidc and api key cannot delete reports", func(t *testing.T) {
		id := insertGroup(t, env, "mattermost/repo-del")
		for name, c := range map[string]creds{
			"oidc":   oidcTok(t, env, "mattermost/repo-del", wfUploader),
			"apikey": {apiKey: env.IssueAPIKey(t, "uploader-key")},
		} {
			code, body := do(t, env, c, http.MethodDelete, "/api/v1/reports/"+id, nil)
			expect(t, name+" delete", code, body, http.StatusForbidden)
		}
		if !groupExists(t, env, id) {
			t.Fatal("forbidden delete still removed the report group")
		}
	})

	t.Run("api key may begin and register for any repo", func(t *testing.T) {
		key := creds{apiKey: env.IssueAPIKey(t, "any-repo-key")}
		code, body := do(t, env, key, http.MethodPost, "/api/v1/reports/begin", groupBody("mattermost/repo-apikey"))
		expect(t, "apikey begin", code, body, http.StatusOK)
	})

	t.Run("admin oidc can delete", func(t *testing.T) {
		id := insertGroup(t, env, "mattermost/repo-del-admin")
		code, body := do(t, env, oidcTok(t, env, "mattermost/repo-del-admin", wfAdmin), http.MethodDelete, "/api/v1/reports/"+id, nil)
		expect(t, "admin oidc delete", code, body, http.StatusNoContent)
		if groupExists(t, env, id) {
			t.Fatal("admin delete left the group behind")
		}
	})

	t.Run("human admin session deletes, editor does not", func(t *testing.T) {
		id := insertGroup(t, env, "mattermost/repo-del-human")
		code, body := do(t, env, sessionFor(t, env, "editor"), http.MethodDelete, "/api/v1/reports/"+id, nil)
		expect(t, "editor delete", code, body, http.StatusForbidden)
		code, body = do(t, env, sessionFor(t, env, "admin"), http.MethodDelete, "/api/v1/reports/"+id, nil)
		expect(t, "admin delete", code, body, http.StatusNoContent)
		if groupExists(t, env, id) {
			t.Fatal("admin session delete left the group behind")
		}
	})

	t.Run("human viewer reads artifacts but cannot write", func(t *testing.T) {
		viewer := sessionFor(t, env, "viewer")
		code, body := do(t, env, viewer, http.MethodGet, "/api/v1/artifacts/"+uuid.NewString(), nil)
		if code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Fatalf("viewer GET artifact: status = %d, want a non-auth error, body=%s", code, body)
		}
		code, body = do(t, env, viewer, http.MethodPost, "/api/v1/reports/begin", groupBody("mattermost/repo-viewer"))
		expect(t, "viewer begin", code, body, http.StatusForbidden)
		id := insertGroup(t, env, "mattermost/repo-viewer-del")
		code, body = do(t, env, viewer, http.MethodDelete, "/api/v1/reports/"+id, nil)
		expect(t, "viewer delete", code, body, http.StatusForbidden)
		if n := groupCount(t, env, "mattermost/repo-viewer"); n != 0 {
			t.Fatalf("forbidden viewer begin created %d groups", n)
		}
	})

	t.Run("unauthenticated is 401 not 403", func(t *testing.T) {
		code, body := do(t, env, creds{}, http.MethodDelete, "/api/v1/reports/"+uuid.NewString(), nil)
		expect(t, "anonymous delete", code, body, http.StatusUnauthorized)
	})
}

// Below enforce, failed checks are logged but never rejected.
func TestLogOnlyAndOffAllowEverything(t *testing.T) {
	for _, mode := range []authapi.AuthzMode{authapi.AuthzLogOnly, authapi.AuthzOff, ""} {
		t.Run("mode="+string(mode), func(t *testing.T) {
			env := start(t, mode)

			code, body := do(t, env, oidcTok(t, env, repoA, wfUploader), http.MethodPost, "/api/v1/reports/begin", groupBody(repoB))
			expect(t, "wrong-repo begin", code, body, http.StatusOK)
			code, body = do(t, env, oidcTok(t, env, repoA, wfUploader), http.MethodPost, "/api/v1/orchestration/begin", orchBody(repoB))
			expect(t, "wrong-repo orchestration begin", code, body, http.StatusCreated)

			id := insertGroup(t, env, "mattermost/repo-del")
			code, body = do(t, env, creds{apiKey: env.IssueAPIKey(t, "uploader-key")}, http.MethodDelete, "/api/v1/reports/"+id, nil)
			expect(t, "uploader api key delete", code, body, http.StatusNoContent)
			if groupExists(t, env, id) {
				t.Fatal("delete returned 204 but row remains")
			}

			viewer := sessionFor(t, env, "viewer")
			code, body = do(t, env, viewer, http.MethodPost, "/api/v1/reports/begin", groupBody("mattermost/repo-viewer"))
			expect(t, "viewer begin", code, body, http.StatusOK)
			id = insertGroup(t, env, "mattermost/repo-viewer-del")
			code, body = do(t, env, viewer, http.MethodDelete, "/api/v1/reports/"+id, nil)
			expect(t, "viewer delete", code, body, http.StatusNoContent)

			code, body = do(t, env, creds{}, http.MethodDelete, "/api/v1/reports/"+uuid.NewString(), nil)
			expect(t, "anonymous still 401", code, body, http.StatusUnauthorized)
		})
	}
}

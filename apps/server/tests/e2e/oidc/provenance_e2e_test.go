//go:build e2e

package oidce2e

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestVerifiedUploadReceiptsAndOwnership(t *testing.T) {
	env := testenv.Start(t)
	env.InsertPolicy(t, "allow-receipts", 1, "uploader", map[string]string{"repository_owner": claimOwner})
	claims := baseClaims("repo:mattermost/mm-e2e:ref:refs/heads/main")
	claims.Extra = map[string]any{"run_id": "8001", "run_attempt": "1", "sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "workflow_ref": "mattermost/mm-e2e/.github/workflows/ci.yml@refs/heads/main"}
	token := env.Mock.IssueToken(t, claims)
	body := map[string]any{"repository": claimRepo, "commit": claims.Extra["sha"], "gh_run_id": "8001", "gh_run_attempt": "1", "framework": "playwright", "name": "receipt-suite", "branch": "main", "total_reports_expected": 1}
	begin, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := do(t, env, http.MethodPost, "/api/v1/reports/begin", bytes.NewReader(begin), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
	})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("begin: %d %s", r.StatusCode, readAll(r.Body))
	}
	_ = r.Body.Close()
	body["gh_job_id"] = "worker-1"
	body["environment_metadata"] = map[string]any{"server_image_digest": "example/server@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	registered := env.RegisterStatelessUpload(t, "Bearer "+token, body)
	if registered.StatusCode != http.StatusOK {
		t.Fatalf("register: %d %s", registered.StatusCode, registered.Body)
	}
	var ids struct {
		Group  string `json:"report_id"`
		Report string `json:"upload_id"`
	}
	if err = json.Unmarshal(registered.Body, &ids); err != nil {
		t.Fatal(err)
	}
	var repo, run, receiptRepo, receiptRun, principal string
	if err = env.Pool.QueryRow(t.Context(), `SELECT c.repository,c.raw_claims->>'run_id',r.registration_receipt->>'repository',r.registration_receipt->>'gh_run_id',r.upload_principal FROM reports r JOIN oidc_claims c ON c.report_id=r.id WHERE r.id=$1`, ids.Report).Scan(&repo, &run, &receiptRepo, &receiptRun, &principal); err != nil {
		t.Fatal(err)
	}
	if repo != claimRepo || run != "8001" || receiptRepo != claimRepo || receiptRun != run || principal == "" {
		t.Fatalf("unbound receipt: %q %q %q %q %q", repo, run, receiptRepo, receiptRun, principal)
	}
	var expected int
	if err = env.Pool.QueryRow(t.Context(), `SELECT (receipt->>'total_reports_expected')::int FROM report_group_begin_receipts WHERE report_group_id=$1 AND verified_claims->>'run_id'='8001'`, ids.Group).Scan(&expected); err != nil || expected != 1 {
		t.Fatalf("trusted count=%d err=%v", expected, err)
	}
	// A fresh JWT for the same job identity may resume; another run sharing
	// the same repo/ref subject cannot overwrite the report or its object keys.
	refreshed := env.Mock.IssueToken(t, claims)
	if got := env.RegisterStatelessUpload(t, "Bearer "+refreshed, body); got.StatusCode != http.StatusOK {
		t.Fatalf("refresh rejected: %d %s", got.StatusCode, got.Body)
	}
	claims.Extra["run_id"] = "8002"
	other := env.Mock.IssueToken(t, claims)
	if got := env.RegisterStatelessUpload(t, "Bearer "+other, body); got.StatusCode != http.StatusForbidden {
		t.Fatalf("hijack registration=%d %s", got.StatusCode, got.Body)
	}
	for _, kind := range []string{"json", "screenshots"} {
		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		part, e := mw.CreateFormFile("files", "report.json")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.WriteString(part, `{"suites":[]}`); e != nil {
			t.Fatal(e)
		}
		if e = mw.Close(); e != nil {
			t.Fatal(e)
		}
		resp := do(t, env, http.MethodPost, "/api/v1/reports/upload/"+ids.Group+"/"+ids.Report+"/"+kind, buf, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+other)
			r.Header.Set("Content-Type", mw.FormDataContentType())
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("hijack %s=%d %s", kind, resp.StatusCode, readAll(resp.Body))
		}
		_ = resp.Body.Close()
	}
	var files int
	if err = env.Pool.QueryRow(t.Context(), `SELECT count(*) FROM report_json_files WHERE report_id=$1`, ids.Report).Scan(&files); err != nil || files != 0 {
		t.Fatalf("unauthorized files=%d err=%v", files, err)
	}
}

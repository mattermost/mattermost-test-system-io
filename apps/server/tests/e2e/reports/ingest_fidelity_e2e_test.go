//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const fidelityPlaywrightJSON = `{
  "suites": [
    {
      "title": "login.spec.ts",
      "file": "login.spec.ts",
      "tags": ["@auth"],
      "specs": [
        {
          "title": "logs in with SSO",
          "tags": ["@smoke", "@auth"],
          "tests": [
            {
              "projectName": "chromium",
              "tags": ["@p0"],
              "annotations": [{"type": "issue", "description": "SEC-11510"}],
              "status": "unexpected",
              "results": [
                {
                  "status": "failed",
                  "duration": 150,
                  "retry": 0,
                  "startTime": "2026-01-01T00:00:00.000Z",
                  "annotations": [{"type": "slow", "description": "waited"}],
                  "errors": [{"message": "Expected 200", "stack": "Error: Expected 200\n    at login.spec.ts:14:5"}]
                }
              ]
            }
          ]
        }
      ]
    },
    {
      "title": "home.spec.ts",
      "file": "home.spec.ts",
      "specs": [
        {
          "title": "renders login page",
          "tests": [
            {
              "status": "expected",
              "results": [
                {"status": "passed", "duration": 25, "retry": 0, "startTime": "2026-01-01T00:00:01.000Z"}
              ]
            }
          ]
        }
      ]
    }
  ]
}`

const fidelityCypressJSON = `{
  "stats": {"start": "2026-01-01T00:00:00.000Z", "end": "2026-01-01T00:00:05.000Z"},
  "results": [
    {
      "title": "",
      "file": "cypress/e2e/channel.cy.ts",
      "fullFile": "cypress/e2e/channel.cy.ts",
      "suites": [
        {
          "title": "Channel",
          "tests": [
            {
              "title": "recovers on retry",
              "fullTitle": "Channel recovers on retry",
              "duration": 120,
              "state": "passed",
              "pass": true,
              "attempts": [
                {"err": {"message": "first hiccup", "estack": "Error: first hiccup\n    at channel.cy.ts:8:3"}},
                {}
              ]
            },
            {
              "title": "fails persistently",
              "fullTitle": "Channel fails persistently",
              "duration": 200,
              "state": "failed",
              "fail": true,
              "attempts": [{}, {}],
              "err": {"message": "Timed out", "estack": "AssertionError: Timed out\n    at channel.cy.ts:20:5"}
            }
          ]
        }
      ]
    }
  ]
}`

func TestIngestFidelity_PlaywrightAndCypress(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-reports-e2e", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultRepoOwner,
	})
	tok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         defaultOIDCSubject,
		Audience:        "tsio",
		Repository:      defaultRepository,
		RepositoryOwner: defaultRepoOwner,
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})

	upload := func(framework, runID, payload string) shard {
		reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
			"repository": defaultRepository,
			"framework":  framework,
			"gh_run_id":  runID,
			"json_files": []any{map[string]any{"path": "results.json", "size": len(payload)}},
		})
		if reg.StatusCode != http.StatusOK {
			t.Fatalf("register %s status = %d, want 200 (body=%s)", framework, reg.StatusCode, reg.Body)
		}
		var regBody struct {
			ReportID string `json:"report_id"`
			UploadID string `json:"upload_id"`
		}
		if err := json.Unmarshal(reg.Body, &regBody); err != nil {
			t.Fatalf("unmarshal register: %v", err)
		}
		s := shard{env: env, token: tok, groupID: regBody.ReportID, uploadID: regBody.UploadID}
		if code := s.post(t, "json", "results.json", payload); code != http.StatusOK {
			t.Fatalf("upload %s status = %d, want 200", framework, code)
		}
		s.waitComplete(t)
		return s
	}

	pw := upload("playwright", "run-fidelity-pw", fidelityPlaywrightJSON)
	cy := upload("cypress", "run-fidelity-cy", fidelityCypressJSON)

	// Verify DB persistence for Playwright cases.
	var pwSuiteID uuid.UUID
	if err := env.Pool.QueryRow(context.Background(), `
		SELECT s.id FROM suites s WHERE s.report_id = $1 AND s.title = 'login.spec.ts'
	`, pw.uploadID).Scan(&pwSuiteID); err != nil {
		t.Fatalf("query pw suite: %v", err)
	}

	type dbCase struct {
		title       string
		status      string
		retryCount  int
		errorStack  *string
		project     *string
		tags        []string
		annotations []map[string]any
	}
	loadDBCases := func(reportID string) map[string]dbCase {
		rows, err := env.Pool.Query(context.Background(), `
			SELECT tc.title, tc.status, tc.retry_count, tc.error_stack, tc.project, tc.tags, tc.annotations
			FROM test_cases tc
			JOIN suites s ON s.id = tc.suite_id
			WHERE s.report_id = $1
		`, reportID)
		if err != nil {
			t.Fatalf("query test_cases: %v", err)
		}
		defer rows.Close()
		out := map[string]dbCase{}
		for rows.Next() {
			var c dbCase
			var tagsRaw, annRaw []byte
			if err := rows.Scan(&c.title, &c.status, &c.retryCount, &c.errorStack, &c.project, &tagsRaw, &annRaw); err != nil {
				t.Fatalf("scan test_case: %v", err)
			}
			if err := json.Unmarshal(tagsRaw, &c.tags); err != nil {
				t.Fatalf("unmarshal tags %q: %v", tagsRaw, err)
			}
			if err := json.Unmarshal(annRaw, &c.annotations); err != nil {
				t.Fatalf("unmarshal annotations %q: %v", annRaw, err)
			}
			out[c.title] = c
		}
		return out
	}

	pwCases := loadDBCases(pw.uploadID)
	sso := pwCases["logs in with SSO"]
	if sso.project == nil || *sso.project != "chromium" {
		t.Fatalf("pw project = %v, want chromium", sso.project)
	}
	if !reflect.DeepEqual(sso.tags, []string{"@auth", "@smoke", "@p0"}) {
		t.Fatalf("pw tags = %v, want [@auth @smoke @p0]", sso.tags)
	}
	if len(sso.annotations) != 2 {
		t.Fatalf("pw annotations len = %d, want 2: %+v", len(sso.annotations), sso.annotations)
	}
	if sso.errorStack == nil || !strings.Contains(*sso.errorStack, "login.spec.ts:14:5") {
		t.Fatalf("pw error_stack = %v, want stack trace", sso.errorStack)
	}

	clean := pwCases["renders login page"]
	if clean.project != nil || len(clean.tags) != 0 || len(clean.annotations) != 0 {
		t.Fatalf("clean pw case unexpected metadata: %+v", clean)
	}

	// Verify DB persistence for Cypress cases.
	cyCases := loadDBCases(cy.uploadID)
	flaky := cyCases["recovers on retry"]
	if flaky.status != "flaky" || flaky.retryCount != 1 {
		t.Fatalf("cy flaky status/retry = %q/%d, want flaky/1", flaky.status, flaky.retryCount)
	}
	if flaky.errorStack == nil || !strings.Contains(*flaky.errorStack, "channel.cy.ts:8:3") {
		t.Fatalf("cy flaky error_stack = %v, want attempt stack", flaky.errorStack)
	}
	failed := cyCases["fails persistently"]
	if failed.status != "failed" || failed.retryCount != 1 {
		t.Fatalf("cy failed status/retry = %q/%d, want failed/1", failed.status, failed.retryCount)
	}
	if failed.errorStack == nil || !strings.Contains(*failed.errorStack, "channel.cy.ts:20:5") {
		t.Fatalf("cy failed error_stack = %v, want estack", failed.errorStack)
	}

	// Validate HTTP responses + OpenAPI compliance for /cases and /suites/{suiteID}/specs.
	doc, opts := loadOpenAPISpec(t)

	getAndValidate := func(path string) []byte {
		t.Helper()
		resp, err := http.Get(env.ServerURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, resp.StatusCode, body)
		}
		validateOpenAPIResponse(t, doc, opts, http.MethodGet, path, resp.StatusCode, resp.Header, body)
		return body
	}

	pwCasesBody := getAndValidate(fmt.Sprintf("/api/v1/reports/%s/cases", pw.groupID))
	var apiCases []struct {
		Title       string           `json:"title"`
		Project     *string          `json:"project"`
		Tags        []string         `json:"tags"`
		Annotations []map[string]any `json:"annotations"`
		ErrorStack  *string          `json:"error_stack"`
	}
	if err := json.Unmarshal(pwCasesBody, &apiCases); err != nil {
		t.Fatalf("decode /cases: %v", err)
	}
	if len(apiCases) != 2 {
		t.Fatalf("len(/cases) = %d, want 2", len(apiCases))
	}
	if apiCases[0].Project == nil || *apiCases[0].Project != "chromium" {
		t.Errorf("/cases[0].project = %v, want chromium", apiCases[0].Project)
	}
	if !reflect.DeepEqual(apiCases[0].Tags, []string{"@auth", "@smoke", "@p0"}) {
		t.Errorf("/cases[0].tags = %v, want [@auth @smoke @p0]", apiCases[0].Tags)
	}
	if apiCases[1].Project != nil || apiCases[1].Tags == nil || len(apiCases[1].Tags) != 0 {
		t.Errorf("/cases[1] project=%v tags=%v, want nil and []", apiCases[1].Project, apiCases[1].Tags)
	}

	specsBody := getAndValidate(fmt.Sprintf("/api/v1/reports/%s/suites/%s/specs", pw.groupID, pwSuiteID))
	var specsResp struct {
		Specs []struct {
			Title   string `json:"title"`
			Results []struct {
				ProjectName string `json:"project_name"`
				ErrorsJSON  string `json:"errors_json"`
			} `json:"results"`
		} `json:"specs"`
	}
	if err := json.Unmarshal(specsBody, &specsResp); err != nil {
		t.Fatalf("decode /specs: %v", err)
	}
	if len(specsResp.Specs) != 1 || len(specsResp.Specs[0].Results) != 1 {
		t.Fatalf("unexpected specs shape: %s", specsBody)
	}
	r0 := specsResp.Specs[0].Results[0]
	if r0.ProjectName != "chromium" {
		t.Errorf("specs[0].results[0].project_name = %q, want chromium", r0.ProjectName)
	}
	if !strings.Contains(r0.ErrorsJSON, "Expected 200") || !strings.Contains(r0.ErrorsJSON, "login.spec.ts:14:5") {
		t.Errorf("specs[0].results[0].errors_json = %q, want message and estack", r0.ErrorsJSON)
	}

	// Also validate /cases OpenAPI contract for the Cypress report.
	cyCasesBody := getAndValidate(fmt.Sprintf("/api/v1/reports/%s/cases", cy.groupID))
	var cyAPICases []struct {
		Title      string `json:"title"`
		Status     string `json:"status"`
		RetryCount int    `json:"retry_count"`
	}
	if err := json.Unmarshal(cyCasesBody, &cyAPICases); err != nil {
		t.Fatalf("decode cy /cases: %v", err)
	}
	if len(cyAPICases) != 2 || cyAPICases[0].Status != "flaky" || cyAPICases[0].RetryCount != 1 {
		t.Errorf("unexpected cy /cases payload: %s", cyCasesBody)
	}
}

func loadOpenAPISpec(t *testing.T) (*openapi3.T, *openapi3filter.Options) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	// this file → apps/server/tests/e2e/reports/ → up 3 = apps/server
	serverDir := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename))))
	spec := filepath.Join(serverDir, "api", "openapi.yaml")
	loader := &openapi3.Loader{Context: t.Context()}
	doc, err := loader.LoadFromFile(spec)
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("validate openapi: %v", err)
	}
	opts := &openapi3filter.Options{
		AuthenticationFunc: func(_ context.Context, _ *openapi3filter.AuthenticationInput) error { return nil },
	}
	return doc, opts
}

func validateOpenAPIResponse(
	t *testing.T, doc *openapi3.T, opts *openapi3filter.Options,
	method, path string, status int, header http.Header, body []byte,
) {
	t.Helper()
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	var serverURL string
	if len(doc.Servers) > 0 {
		serverURL = doc.Servers[0].URL
	}
	req, _ := http.NewRequest(method, serverURL+path, nil)
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find route %s %s: %v", method, path, err)
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: params,
			Route:      route,
			Options:    opts,
		},
		Status:  status,
		Header:  header,
		Options: opts,
	}
	respIn.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Errorf("openapi response validation failed for %s %s: %v\nbody=%s", method, path, err, string(body))
	}
}

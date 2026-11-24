//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestMigration000033_UpDownAndNoTestCasesIndex(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	// Verify no indexes were created on test_cases(test_id) or test_cases(signature_id)
	// in migration 000033 (deferred to SEC-11506 CONCURRENTLY rollout).
	rows, err := env.Pool.Query(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE tablename = 'test_cases'`)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			t.Fatalf("scan pg_indexes: %v", err)
		}
		if strings.Contains(def, "test_id") || strings.Contains(def, "signature_id") {
			rows.Close()
			t.Fatalf("unexpected index on test_cases in 000033: %s (%s)", name, def)
		}
	}
	rows.Close()

	downSQL, err := migrations.FS.ReadFile("000033_tests_and_failure_signatures.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	upSQL, err := migrations.FS.ReadFile("000033_tests_and_failure_signatures.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	if _, err := env.Pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("exec down migration: %v", err)
	}

	var testsReg, sigsReg *string
	if err := env.Pool.QueryRow(ctx, `SELECT to_regclass('public.tests'), to_regclass('public.failure_signatures')`).Scan(&testsReg, &sigsReg); err != nil {
		t.Fatalf("check dropped tables: %v", err)
	}
	if testsReg != nil || sigsReg != nil {
		t.Fatalf("expected tables dropped after down migration, got tests=%v failure_signatures=%v", testsReg, sigsReg)
	}

	if _, err := env.Pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-exec up migration: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT to_regclass('public.tests'), to_regclass('public.failure_signatures')`).Scan(&testsReg, &sigsReg); err != nil {
		t.Fatalf("check recreated tables: %v", err)
	}
	if testsReg == nil || sigsReg == nil {
		t.Fatalf("expected tables recreated after up migration, got tests=%v failure_signatures=%v", testsReg, sigsReg)
	}
}

func uploadReportShard(t *testing.T, env *testenv.Env, tok, framework, runID, filename, payload string) shard {
	t.Helper()
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository": defaultRepository,
		"framework":  framework,
		"gh_run_id":  runID,
		"json_files": []any{map[string]any{"path": filename, "size": len(payload)}},
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
	if code := s.post(t, "json", filename, payload); code != http.StatusOK {
		t.Fatalf("upload %s status = %d, want 200", framework, code)
	}
	s.waitComplete(t)
	return s
}

func TestIngestTestIDsAndFailureSignatures_AllFrameworks(t *testing.T) {
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

	upload := func(framework, runID, filename, payload string) shard {
		return uploadReportShard(t, env, tok, framework, runID, filename, payload)
	}

	pwPayload := `{
	  "suites": [{
	    "title": "login.spec.ts",
	    "file": "e2e/login.spec.ts",
	    "specs": [
	      {
	        "title": "MM-T101 logs in",
	        "tests": [
	          {
	            "projectName": "chromium",
	            "status": "unexpected",
	            "results": [{
	              "status": "failed",
	              "duration": 100,
	              "retry": 0,
	              "errors": [{
	                "message": "TimeoutError: waiting for locator('[data-testid=\"send\"]').click() exceeded 30000ms",
	                "stack": "TimeoutError: waiting for locator('[data-testid=\"send\"]').click() exceeded 30000ms\n    at /home/runner/work/mattermost/mattermost/e2e/login.spec.ts:14:5"
	              }]
	            }]
	          },
	          {
	            "projectName": "firefox",
	            "status": "expected",
	            "results": [{"status": "passed", "duration": 80, "retry": 0}]
	          }
	        ]
	      },
	      {
	        "title": "renders sidebar",
	        "tests": [{
	          "projectName": "chromium",
	          "status": "unexpected",
	          "results": [{
	            "status": "failed",
	            "duration": 90,
	            "retry": 0,
	            "errors": [{
	              "message": "TimeoutError: waiting for locator('[data-testid=\"send\"]').click() exceeded 15000ms",
	              "stack": "TimeoutError: waiting for locator('[data-testid=\"send\"]').click() exceeded 15000ms\n    at /home/runner/work/mattermost/mattermost/e2e/home.spec.ts:99:7"
	            }]
	          }]
	        }]
	      },
	      {
	        "title": "skipped case",
	        "tests": [{
	          "status": "skipped",
	          "results": [{"status": "skipped", "duration": 0, "retry": 0}]
	        }]
	      },
	      {
	        "title": "timed out case",
	        "tests": [{
	          "status": "unexpected",
	          "results": [{"status": "timedOut", "duration": 5000, "retry": 0}]
	        }]
	      },
	      {
	        "title": "interrupted case",
	        "tests": [{
	          "status": "unexpected",
	          "results": [{"status": "interrupted", "duration": 50, "retry": 0}]
	        }]
	      },
	      {
	        "title": "did not run case",
	        "tests": [{
	          "status": "unexpected",
	          "results": [{"status": "failed", "duration": 0, "retry": 0, "errors": [{"message": "spec-did-not-run"}]}]
	        }]
	      },
	      {
	        "title": "no error case",
	        "tests": [{
	          "status": "unexpected",
	          "results": [{"status": "failed", "duration": 10, "retry": 0}]
	        }]
	      }
	    ]
	  }]
	}`

	cyPayload := `{
	  "stats": {"start": "2026-01-01T00:00:00.000Z", "end": "2026-01-01T00:00:05.000Z"},
	  "results": [{
	    "title": "",
	    "file": "cypress/e2e/channel.cy.ts",
	    "fullFile": "cypress/e2e/channel.cy.ts",
	    "suites": [{
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
	          "err": {"message": "AssertionError: Timed out retrying", "estack": "AssertionError: Timed out retrying\n    at cypress/e2e/channel.cy.ts:20:5"}
	        }
	      ]
	    }]
	  }]
	}`

	detoxPayload := `{
	  "testResults": [{
	    "testFilePath": "/home/runner/work/mattermost-mobile/mattermost-mobile/e2e/detox/test/search.e2e.ts",
	    "perfStats": {"start": 1767225600000},
	    "testResults": [{
	      "ancestorTitles": ["Search"],
	      "title": "MM-T5294_3 - should search messages",
	      "fullName": "Search MM-T5294_3 - should search messages",
	      "status": "passed",
	      "duration": 250,
	      "retryReasons": ["Error: element not visible\n    at e2e/detox/test/search.e2e.ts:44:9"]
	    }]
	  }]
	}`

	maestroPayload := `<testsuites>
	  <testsuite name="Maestro Flow" tests="2" failures="1" timestamp="2026-01-01T00:00:00Z">
	    <testcase name="flow fails silently" file="e2e/maestro/flows/login.yml" time="1.2">
	      <failure message=""/>
	    </testcase>
	    <testcase name="flow passes" file="e2e/maestro/flows/login.yml" time="0.5"/>
	  </testsuite>
	</testsuites>`

	pw := upload("playwright", "run-id-pw", "results.json", pwPayload)
	cy := upload("cypress", "run-id-cy", "results.json", cyPayload)
	dt := upload("detox", "run-id-detox", "results.json", detoxPayload)
	ma := upload("maestro", "run-id-maestro", "results.xml", maestroPayload)

	ctx := context.Background()

	type caseRow struct {
		status       string
		project      *string
		testID       *uuid.UUID
		testKey      *string
		zephyrKey    *string
		testFile     *string
		sigID        *uuid.UUID
		algoVersion  *int16
		errorClass   *string
		normMsg      *string
		topFrame     *string
		firstGroupID *uuid.UUID
	}
	loadCases := func(reportID string) []caseRow {
		rows, err := env.Pool.Query(ctx, `
			SELECT tc.status, tc.project, tc.test_id, t.test_key, t.zephyr_key, t.file,
			       tc.signature_id, fs.algo_version, fs.error_class, fs.normalized_message,
			       fs.top_frame, fs.first_seen_report_group_id
			FROM test_cases tc
			JOIN suites s ON s.id = tc.suite_id
			LEFT JOIN tests t ON t.id = tc.test_id
			LEFT JOIN failure_signatures fs ON fs.id = tc.signature_id
			WHERE s.report_id = $1
			ORDER BY tc.ordinal
		`, reportID)
		if err != nil {
			t.Fatalf("query cases for %s: %v", reportID, err)
		}
		defer rows.Close()
		var out []caseRow
		for rows.Next() {
			var r caseRow
			if err := rows.Scan(
				&r.status, &r.project, &r.testID, &r.testKey, &r.zephyrKey, &r.testFile,
				&r.sigID, &r.algoVersion, &r.errorClass, &r.normMsg, &r.topFrame, &r.firstGroupID,
			); err != nil {
				t.Fatalf("scan case: %v", err)
			}
			out = append(out, r)
		}
		return out
	}

	pwCases := loadCases(pw.uploadID)
	if len(pwCases) != 8 {
		t.Fatalf("pw len = %d, want 8", len(pwCases))
	}
	// [0] MM-T101 chromium failed, [1] MM-T101 firefox passed -> same test_id, [1] has NULL signature_id
	if pwCases[0].testID == nil || pwCases[1].testID == nil || *pwCases[0].testID != *pwCases[1].testID {
		t.Fatalf("MM-T101 projects did not share test_id: %v vs %v", pwCases[0].testID, pwCases[1].testID)
	}
	if *pwCases[0].testKey != "zephyr:MM-T101" || *pwCases[0].zephyrKey != "MM-T101" {
		t.Fatalf("MM-T101 test_key/zephyr_key = %q/%q", *pwCases[0].testKey, *pwCases[0].zephyrKey)
	}
	if pwCases[0].sigID == nil || *pwCases[0].algoVersion != errorsig.AlgoVersion {
		t.Fatalf("pw[0] sigID=%v algo=%v", pwCases[0].sigID, pwCases[0].algoVersion)
	}
	if pwCases[0].errorClass == nil || *pwCases[0].errorClass != "TimeoutError" {
		t.Fatalf("pw[0] error_class = %v, want TimeoutError", pwCases[0].errorClass)
	}
	if pwCases[0].topFrame == nil || *pwCases[0].topFrame != "<repo>/e2e/login.spec.ts:14:5" {
		t.Fatalf("pw[0] top_frame = %v, want <repo>/e2e/login.spec.ts:14:5", pwCases[0].topFrame)
	}
	if pwCases[0].firstGroupID == nil || pwCases[0].firstGroupID.String() != pw.groupID {
		t.Fatalf("pw[0] first_seen_report_group_id = %v, want %s", pwCases[0].firstGroupID, pw.groupID)
	}
	if pwCases[1].sigID != nil {
		t.Fatalf("passed case should have NULL signature_id, got %v", pwCases[1].sigID)
	}

	// [2] renders sidebar failed with same normalized error at different top_frame -> shares signature_id!
	if pwCases[2].sigID == nil || *pwCases[2].sigID != *pwCases[0].sigID {
		t.Fatalf("different top_frame should share algo_version=1 signature_id: %v vs %v", pwCases[2].sigID, pwCases[0].sigID)
	}
	if !strings.HasPrefix(*pwCases[2].testKey, "h:") {
		t.Fatalf("non-zephyr test_key = %q, want h:...", *pwCases[2].testKey)
	}

	// [3] skipped -> NULL signature_id
	if pwCases[3].testID == nil || pwCases[3].sigID != nil {
		t.Fatalf("skipped case testID=%v sigID=%v", pwCases[3].testID, pwCases[3].sigID)
	}
	// [4] timedOut -> timeout fallback, [5] interrupted -> interrupted fallback,
	// [6] spec-did-not-run -> spec-did-not-run fallback, [7] empty failed -> no-error:unknown:playwright
	wantFallbacks := map[int]string{
		4: "timeout",
		5: "interrupted",
		6: "spec-did-not-run",
		7: "no-error:unknown:playwright",
	}
	for idx, wantNorm := range wantFallbacks {
		if pwCases[idx].sigID == nil || pwCases[idx].normMsg == nil || *pwCases[idx].normMsg != wantNorm {
			t.Fatalf("pwCases[%d] normMsg = %v, want %q", idx, pwCases[idx].normMsg, wantNorm)
		}
	}

	// Cypress: [0] flaky (retried-and-passed) has signature_id IS NULL; [1] failed has signature_id populated.
	cyCases := loadCases(cy.uploadID)
	if len(cyCases) != 2 {
		t.Fatalf("cy len = %d, want 2", len(cyCases))
	}
	if cyCases[0].status != ingest.StatusFlaky || cyCases[0].testID == nil || cyCases[0].sigID != nil {
		t.Fatalf("cy flaky case status=%q testID=%v sigID=%v, want %s/non-nil/nil",
			cyCases[0].status, cyCases[0].testID, cyCases[0].sigID, ingest.StatusFlaky)
	}
	if cyCases[1].sigID == nil || cyCases[1].errorClass == nil || *cyCases[1].errorClass != "AssertionError" {
		t.Fatalf("cy failed case sigID=%v errorClass=%v", cyCases[1].sigID, cyCases[1].errorClass)
	}

	// Detox: [0] failed retry + [1] passed final share zephyr:MM-T5294 and stripped repo-relative file.
	dtCases := loadCases(dt.uploadID)
	if len(dtCases) != 2 {
		t.Fatalf("detox len = %d, want 2", len(dtCases))
	}
	if *dtCases[0].testID != *dtCases[1].testID || *dtCases[0].testKey != "zephyr:MM-T5294" || *dtCases[0].zephyrKey != "MM-T5294" {
		t.Fatalf("detox test identity mismatch: key=%q zephyr=%q", *dtCases[0].testKey, *dtCases[0].zephyrKey)
	}
	if *dtCases[0].testFile != "e2e/detox/test/search.e2e.ts" {
		t.Fatalf("detox tests.file = %q, want e2e/detox/test/search.e2e.ts", *dtCases[0].testFile)
	}
	if dtCases[0].sigID == nil || dtCases[0].topFrame == nil || *dtCases[0].topFrame != "e2e/detox/test/search.e2e.ts:44:9" {
		t.Fatalf("detox failed retry sigID=%v topFrame=%v", dtCases[0].sigID, dtCases[0].topFrame)
	}
	if dtCases[1].sigID != nil {
		t.Fatalf("detox final passed attempt should have NULL signature_id")
	}

	// Maestro: [0] failed without error text -> no-error:unknown:maestro; [1] passed -> NULL signature_id.
	maCases := loadCases(ma.uploadID)
	if len(maCases) != 2 {
		t.Fatalf("maestro len = %d, want 2", len(maCases))
	}
	if maCases[0].sigID == nil || *maCases[0].normMsg != "no-error:unknown:maestro" {
		t.Fatalf("maestro fallback = %v, want no-error:unknown:maestro", maCases[0].normMsg)
	}
	if maCases[1].sigID != nil {
		t.Fatalf("maestro passed case should have NULL signature_id")
	}
}

func TestConsolidate_ConcurrentSharedTestsAndSignatures(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	var groupID uuid.UUID
	err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright', 'pw-concurrent', 'mattermost/concurrent-test', 'main', 'abc1234', 'run-conc-1', '1')
		RETURNING id
	`).Scan(&groupID)
	if err != nil {
		t.Fatalf("insert report_group: %v", err)
	}

	const workers = 8
	reportIDs := make([]uuid.UUID, workers)
	for i := range workers {
		err := env.Pool.QueryRow(ctx, `
			INSERT INTO reports (report_group_id, name, gh_job_id)
			VALUES ($1, 'pw-concurrent', $2)
			RETURNING id
		`, groupID, fmt.Sprintf("job-%d", i)).Scan(&reportIDs[i])
		if err != nil {
			t.Fatalf("insert report %d: %v", i, err)
		}
	}

	file := "e2e/shared.spec.ts"
	errA := "Error: ECONNREFUSED 10.0.0.1:8065"
	errB := "TimeoutError: selector [data-testid=\"btn\"] timed out"
	baseCases := []ingest.ExtractedCase{
		{Title: "MM-T9001 shared zephyr", FullTitle: "Shared > MM-T9001 shared zephyr", Status: ingest.StatusFailed, ErrorMessage: &errA, Sequence: 0},
		{Title: "shared non-zephyr", FullTitle: "Shared > shared non-zephyr", Status: ingest.StatusFailed, ErrorMessage: &errB, Sequence: 1},
		{Title: "shared pass", FullTitle: "Shared > shared pass", Status: ingest.StatusPassed, Sequence: 2},
	}

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Reverse case order on odd shards to prove sorted bulk upserts avoid deadlocks.
			cases := append([]ingest.ExtractedCase(nil), baseCases...)
			if idx%2 == 1 {
				cases[0], cases[2] = cases[2], cases[0]
			}
			now := time.Now().UTC()
			<-start
			_, err := ingest.Consolidate(ctx, env.Pool, reportIDs[idx], []ingest.ExtractedSuite{{
				Title:     "Shared",
				FilePath:  &file,
				StartTime: &now,
				Cases:     cases,
			}}, &now, &now)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Consolidate failed: %v", err)
		}
	}

	var testCount, sigCount, linkedTests, linkedSigs int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tests WHERE repository = 'mattermost/concurrent-test'`).Scan(&testCount); err != nil {
		t.Fatalf("count tests: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM failure_signatures`).Scan(&sigCount); err != nil {
		t.Fatalf("count failure_signatures: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		SELECT COUNT(tc.test_id), COUNT(tc.signature_id)
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		WHERE r.report_group_id = $1
	`, groupID).Scan(&linkedTests, &linkedSigs); err != nil {
		t.Fatalf("count linked cases: %v", err)
	}
	if testCount != 3 || sigCount != 2 {
		t.Fatalf("dedup counts: tests=%d (want 3), signatures=%d (want 2)", testCount, sigCount)
	}
	if linkedTests != workers*3 || linkedSigs != workers*2 {
		t.Fatalf("linked counts: test_id=%d (want %d), signature_id=%d (want %d)",
			linkedTests, workers*3, linkedSigs, workers*2)
	}
}

func TestIngestTestIDsAndFailureSignatures_SanitizesNULAndInvalidUTF8(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-nul-e2e", 1, string(policy.RoleUploader), map[string]string{
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

	// 1. Playwright JSON containing \u0000 in suite, spec.title, spec.tags, test.annotations, attachments, and errors.
	pwNulPayload := `{
	  "suites": [{
	    "title": "nul\u0000suite",
	    "file": "e2e/nul\u0000spec.ts",
	    "specs": [{
	      "title": "handles \u0000 nul byte in title",
	      "tags": ["@tag\u0000bad"],
	      "tests": [{
	        "projectName": "chrom\u0000ium",
	        "status": "unexpected",
	        "annotations": [{"type": "issue", "description": "bad\u0000desc", "escaped": "keep\\u0000literal"}],
	        "results": [{
	          "status": "failed",
	          "duration": 42,
	          "retry": 0,
	          "attachments": [{"contentType": "image/pn\u0000g", "path": "shot\u0000.png"}],
	          "errors": [{
	            "message": "Error: bad\u0000msg",
	            "stack": "Error: bad\u0000msg\n    at e2e/nul\u0000spec.ts:7:3"
	          }]
	        }]
	      }]
	    }]
	  }]
	}`
	pw := uploadReportShard(t, env, tok, "playwright", "run-id-pw-nul", "results.json", pwNulPayload)

	ctx := t.Context()
	var title, fullTitle, errMsg, testFile, testTitle, normMsg, tagsJSON, annsJSON, attsJSON string
	err := env.Pool.QueryRow(ctx, `
		SELECT tc.title, tc.full_title, tc.error_message, t.file, t.full_title, fs.normalized_message,
		       tc.tags::text, tc.annotations::text, tc.attachments::text
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN tests t ON t.id = tc.test_id
		JOIN failure_signatures fs ON fs.id = tc.signature_id
		WHERE s.report_id = $1
	`, pw.uploadID).Scan(&title, &fullTitle, &errMsg, &testFile, &testTitle, &normMsg, &tagsJSON, &annsJSON, &attsJSON)
	if err != nil {
		t.Fatalf("query sanitized playwright case: %v", err)
	}
	if title != "handles  nul byte in title" || testFile != "e2e/nulspec.ts" || errMsg != "Error: badmsg" || normMsg != "Error: badmsg" {
		t.Fatalf("unexpected sanitized values: title=%q fullTitle=%q file=%q errMsg=%q normMsg=%q testTitle=%q",
			title, fullTitle, testFile, errMsg, normMsg, testTitle)
	}
	if !strings.Contains(tagsJSON, "@tagbad") || !strings.Contains(annsJSON, "baddesc") || !strings.Contains(annsJSON, `keep\\u0000literal`) || !strings.Contains(attsJSON, "shot.png") {
		t.Fatalf("unexpected sanitized jsonb values: tags=%s anns=%s atts=%s", tagsJSON, annsJSON, attsJSON)
	}

	// 2. Direct Consolidate with raw \x00 and invalid UTF-8 (\xff\xfe) across text fields.
	var directReportID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO reports (report_group_id, name, gh_job_id)
		VALUES ($1, 'pw-nul-direct', 'job-nul-direct')
		RETURNING id
	`, pw.groupID).Scan(&directReportID); err != nil {
		t.Fatalf("insert direct report: %v", err)
	}
	badFile := "e2e/raw\x00\xff\xfefile.spec.ts"
	badMsg := "Error: raw\x00\xff\xfefailure"
	badStack := "Error: raw\x00\xff\xfefailure\n    at e2e/raw\x00\xff\xfefile.spec.ts:12:4"
	badProj := "proj\x00\xff\xfe"
	now := time.Now().UTC()
	if _, err := ingest.Consolidate(ctx, env.Pool, directReportID, []ingest.ExtractedSuite{{
		Title:     "suite\x00\xff\xfe",
		FilePath:  &badFile,
		StartTime: &now,
		Cases: []ingest.ExtractedCase{{
			Title:        "case\x00\xff\xfe",
			FullTitle:    "suite\x00\xff\xfe > case\x00\xff\xfe",
			Status:       ingest.StatusFailed,
			Project:      &badProj,
			ErrorMessage: &badMsg,
			ErrorStack:   &badStack,
			Sequence:     0,
		}},
	}}, &now, &now); err != nil {
		t.Fatalf("Consolidate with raw NUL/invalid UTF-8 failed: %v", err)
	}

	var rawCaseTitle, rawTestFile, rawNormMsg string
	if err := env.Pool.QueryRow(ctx, `
		SELECT tc.title, t.file, fs.normalized_message
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN tests t ON t.id = tc.test_id
		JOIN failure_signatures fs ON fs.id = tc.signature_id
		WHERE s.report_id = $1
	`, directReportID).Scan(&rawCaseTitle, &rawTestFile, &rawNormMsg); err != nil {
		t.Fatalf("query direct sanitized case: %v", err)
	}
	for _, s := range []string{rawCaseTitle, rawTestFile, rawNormMsg} {
		if strings.Contains(s, "\x00") || !utf8.ValidString(s) {
			t.Fatalf("unsanitized persisted string: %q", s)
		}
	}

	// 3. Direct ResolveCaseIdentities with dirty Framework and empty error text (fallback path).
	tx, err := env.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids, err := ingest.ResolveCaseIdentities(ctx, tx, ingest.IdentityMeta{
		ReportGroupID: uuid.MustParse(pw.groupID),
		Repository:    defaultRepository,
		Framework:     "play\x00\xff\xfewright",
	}, []ingest.CaseIdentityInput{{
		File:      "e2e/fw-fallback.spec.ts",
		Title:     "empty err fallback",
		FullTitle: "Suite > empty err fallback",
		Status:    ingest.StatusFailed,
	}})
	if err != nil {
		t.Fatalf("ResolveCaseIdentities with dirty framework failed: %v", err)
	}
	if len(ids) != 1 || ids[0].SignatureID == nil {
		t.Fatalf("unexpected ResolveCaseIdentities result: %+v", ids)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	var gotFW, gotNorm string
	if err := env.Pool.QueryRow(ctx, `SELECT framework FROM tests WHERE id = $1`, ids[0].TestID).Scan(&gotFW); err != nil {
		t.Fatalf("query sanitized test framework: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT normalized_message FROM failure_signatures WHERE id = $1`, *ids[0].SignatureID).Scan(&gotNorm); err != nil {
		t.Fatalf("query sanitized signature fallback: %v", err)
	}
	if gotFW != "playwright" || gotNorm != "no-error:unknown:playwright" {
		t.Fatalf("framework/fallback mismatch: got (%q, %q), want (playwright, no-error:unknown:playwright)", gotFW, gotNorm)
	}
}

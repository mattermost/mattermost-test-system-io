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

	down37SQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.down.sql")
	if err != nil {
		t.Fatalf("read 000037 down migration: %v", err)
	}
	up37SQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.up.sql")
	if err != nil {
		t.Fatalf("read 000037 up migration: %v", err)
	}
	down36SQL, err := migrations.FS.ReadFile("000036_kpi_rollups.down.sql")
	if err != nil {
		t.Fatalf("read 000036 down migration: %v", err)
	}
	up36SQL, err := migrations.FS.ReadFile("000036_kpi_rollups.up.sql")
	if err != nil {
		t.Fatalf("read 000036 up migration: %v", err)
	}
	down35SQL, err := migrations.FS.ReadFile("000035_verdicts.down.sql")
	if err != nil {
		t.Fatalf("read 000035 down migration: %v", err)
	}
	up35SQL, err := migrations.FS.ReadFile("000035_verdicts.up.sql")
	if err != nil {
		t.Fatalf("read 000035 up migration: %v", err)
	}
	down34SQL, err := migrations.FS.ReadFile("000034_validate_test_cases_fks.down.sql")
	if err != nil {
		t.Fatalf("read 000034 down migration: %v", err)
	}
	up34SQL, err := migrations.FS.ReadFile("000034_validate_test_cases_fks.up.sql")
	if err != nil {
		t.Fatalf("read 000034 up migration: %v", err)
	}
	downSQL, err := migrations.FS.ReadFile("000033_tests_and_failure_signatures.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	upSQL, err := migrations.FS.ReadFile("000033_tests_and_failure_signatures.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}

	if _, err := env.Pool.Exec(ctx, string(down37SQL)); err != nil {
		t.Fatalf("exec 000037 down migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(down36SQL)); err != nil {
		t.Fatalf("exec 000036 down migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(down35SQL)); err != nil {
		t.Fatalf("exec 000035 down migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(down34SQL)); err != nil {
		t.Fatalf("exec 000034 down migration: %v", err)
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
	if _, err := env.Pool.Exec(ctx, string(up34SQL)); err != nil {
		t.Fatalf("re-exec 000034 up migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(up35SQL)); err != nil {
		t.Fatalf("re-exec 000035 up migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(up36SQL)); err != nil {
		t.Fatalf("re-exec 000036 up migration: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(up37SQL)); err != nil {
		t.Fatalf("re-exec 000037 up migration: %v", err)
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

	// [2] renders sidebar failed with same normalized error in a different spec file -> distinct signature_id.
	if pwCases[2].sigID == nil || *pwCases[2].sigID == *pwCases[0].sigID {
		t.Fatalf("different spec file should not merge signature_id: %v vs %v", pwCases[2].sigID, pwCases[0].sigID)
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

	// Detox: [0] failed retry + [1] passed final share zephyr:MM-T5294_3 (with base zephyr_key MM-T5294) and stripped repo-relative file.
	dtCases := loadCases(dt.uploadID)
	if len(dtCases) != 2 {
		t.Fatalf("detox len = %d, want 2", len(dtCases))
	}
	if *dtCases[0].testID != *dtCases[1].testID || *dtCases[0].testKey != "zephyr:MM-T5294_3" || *dtCases[0].zephyrKey != "MM-T5294" {
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

	// Verify exact per-case test_id, test_key, signature_id, and fingerprint mappings across every worker.
	rows, err := env.Pool.Query(ctx, `
		SELECT r.gh_job_id, tc.full_title, tc.test_id, t.test_key, tc.signature_id, fs.fingerprint
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		LEFT JOIN tests t ON t.id = tc.test_id
		LEFT JOIN failure_signatures fs ON fs.id = tc.signature_id
		WHERE r.report_group_id = $1
	`, groupID)
	if err != nil {
		t.Fatalf("query concurrent case identities: %v", err)
	}
	defer rows.Close()

	type caseIDs struct {
		testID      *uuid.UUID
		testKey     *string
		sigID       *uuid.UUID
		fingerprint *string
	}
	byWorker := make(map[string]map[string]caseIDs, workers)
	for rows.Next() {
		var jobID, fullTitle string
		var testID, sigID *uuid.UUID
		var testKey, fp *string
		if err := rows.Scan(&jobID, &fullTitle, &testID, &testKey, &sigID, &fp); err != nil {
			t.Fatalf("scan concurrent case identity: %v", err)
		}
		if byWorker[jobID] == nil {
			byWorker[jobID] = make(map[string]caseIDs, 3)
		}
		if _, dup := byWorker[jobID][fullTitle]; dup {
			t.Fatalf("duplicate case (%s, %q) returned for worker", jobID, fullTitle)
		}
		byWorker[jobID][fullTitle] = caseIDs{testID: testID, testKey: testKey, sigID: sigID, fingerprint: fp}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate concurrent case identities: %v", err)
	}
	if len(byWorker) != workers {
		t.Fatalf("worker count = %d, want %d", len(byWorker), workers)
	}

	wantKeyByTitle := make(map[string]string, len(baseCases))
	wantFPByTitle := make(map[string]string, 2)
	for _, bc := range baseCases {
		k, _ := ingest.DeriveTestKey("mattermost/concurrent-test", file, bc.FullTitle, bc.Title, bc.Tags)
		wantKeyByTitle[bc.FullTitle] = k
		if bc.Status == ingest.StatusFailed {
			sig := errorsig.Compute(errorsig.Input{
				Framework:    "playwright",
				Status:       bc.Status,
				ErrorMessage: *bc.ErrorMessage,
			})
			wantFPByTitle[bc.FullTitle] = sig.Fingerprint
		}
	}

	wantTestIDs := make(map[string]uuid.UUID, 3)
	wantSigIDs := make(map[string]uuid.UUID, 2)
	for i := range workers {
		jobID := fmt.Sprintf("job-%d", i)
		w := byWorker[jobID]
		if len(w) != 3 {
			t.Fatalf("worker %s case count = %d, want 3", jobID, len(w))
		}
		for _, bc := range baseCases {
			got, ok := w[bc.FullTitle]
			if !ok || got.testID == nil || got.testKey == nil {
				t.Fatalf("worker %s missing case %q or nil test_id/test_key", jobID, bc.FullTitle)
			}
			if *got.testKey != wantKeyByTitle[bc.FullTitle] {
				t.Fatalf("worker %s case %q test_key = %q, want %q", jobID, bc.FullTitle, *got.testKey, wantKeyByTitle[bc.FullTitle])
			}
			if prev, seen := wantTestIDs[bc.FullTitle]; !seen {
				wantTestIDs[bc.FullTitle] = *got.testID
			} else if prev != *got.testID {
				t.Fatalf("worker %s case %q test_id = %s, want %s", jobID, bc.FullTitle, *got.testID, prev)
			}
			if bc.Status == ingest.StatusFailed {
				if got.sigID == nil || got.fingerprint == nil {
					t.Fatalf("worker %s failing case %q has nil signature_id/fingerprint", jobID, bc.FullTitle)
				}
				if *got.fingerprint != wantFPByTitle[bc.FullTitle] {
					t.Fatalf("worker %s failing case %q fingerprint = %q, want %q", jobID, bc.FullTitle, *got.fingerprint, wantFPByTitle[bc.FullTitle])
				}
				if prev, seen := wantSigIDs[bc.FullTitle]; !seen {
					wantSigIDs[bc.FullTitle] = *got.sigID
				} else if prev != *got.sigID {
					t.Fatalf("worker %s failing case %q signature_id = %s, want %s", jobID, bc.FullTitle, *got.sigID, prev)
				}
			} else if got.sigID != nil || got.fingerprint != nil {
				t.Fatalf("worker %s passing case %q got unexpected signature_id=%v fingerprint=%v", jobID, bc.FullTitle, got.sigID, got.fingerprint)
			}
		}
	}
	distinctTests := make(map[uuid.UUID]struct{}, len(wantTestIDs))
	for _, id := range wantTestIDs {
		distinctTests[id] = struct{}{}
	}
	if len(distinctTests) != 3 {
		t.Fatalf("distinct test_ids across base cases = %d, want 3 (%v)", len(distinctTests), wantTestIDs)
	}
	if wantSigIDs[baseCases[0].FullTitle] == wantSigIDs[baseCases[1].FullTitle] {
		t.Fatalf("distinct failure fingerprints shared signature_id %s", wantSigIDs[baseCases[0].FullTitle])
	}
}

func TestConsolidate_ConcurrentSignatureAttributionNoDeadlock(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	var groupID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright', 'pw-attr-conc', 'mattermost/attr-conc', 'main', 'abc1234', 'run-attr-conc', '1')
		RETURNING id
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert report_group: %v", err)
	}
	const workers = 4
	reportIDs := make([]uuid.UUID, workers)
	for i := range workers {
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO reports (report_group_id, name, gh_job_id)
			VALUES ($1, 'pw-attr-conc', $2)
			RETURNING id
		`, groupID, fmt.Sprintf("job-attr-conc-%d", i)).Scan(&reportIDs[i]); err != nil {
			t.Fatalf("insert report %d: %v", i, err)
		}
	}

	// Stall after each case insert so every shard holds the FK KEY SHARE lock on
	// both new signatures before any shard reaches AttributeFailureSignaturesTx.
	if _, err := env.Pool.Exec(ctx, `
		CREATE FUNCTION stall_case_insert() RETURNS trigger AS $$
		BEGIN
			PERFORM pg_sleep(0.3);
			RETURN NULL;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER trg_stall_case_insert
		AFTER INSERT ON test_cases
		FOR EACH STATEMENT EXECUTE FUNCTION stall_case_insert();
	`); err != nil {
		t.Fatalf("install stall trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.Pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS trg_stall_case_insert ON test_cases;
			DROP FUNCTION IF EXISTS stall_case_insert();
		`)
	})

	file := "e2e/attr_conc.spec.ts"
	errA := "Error: attribution concurrency sentinel A"
	errB := "Error: attribution concurrency sentinel B"
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cases := []ingest.ExtractedCase{
				{Title: "a", FullTitle: "AttrConc > a", Status: ingest.StatusFailed, ErrorMessage: &errA, Sequence: 0},
				{Title: "b", FullTitle: "AttrConc > b", Status: ingest.StatusFailed, ErrorMessage: &errB, Sequence: 1},
			}
			if idx%2 == 1 {
				cases[0], cases[1] = cases[1], cases[0]
			}
			now := time.Now().UTC()
			<-start
			_, err := ingest.Consolidate(ctx, env.Pool, reportIDs[idx], []ingest.ExtractedSuite{{
				Title: "AttrConc", FilePath: &file, StartTime: &now, Cases: cases,
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

	var sigs, attributed int
	if err := env.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE first_seen_report_group_id = $1)
		FROM failure_signatures
	`, groupID).Scan(&sigs, &attributed); err != nil {
		t.Fatalf("query signature attribution: %v", err)
	}
	if sigs != 2 || attributed != 2 {
		t.Fatalf("signatures=%d attributed to group=%d, want 2 and 2", sigs, attributed)
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
	        "annotations": [{"type": "issue", "id": 9007199254740993, "description": "bad\u0000desc", "escaped": "keep\\u0000literal"}],
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
	if !strings.Contains(tagsJSON, "@tagbad") || !strings.Contains(annsJSON, "baddesc") || !strings.Contains(annsJSON, `keep\\u0000literal`) || !strings.Contains(annsJSON, "9007199254740993") || !strings.Contains(attsJSON, "shot.png") {
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

	// 3. Direct ResolveCaseIdentities with dirty Framework, empty error text, and NUL in status.
	tx, err := env.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids, err := ingest.ResolveCaseIdentities(ctx, tx, ingest.IdentityMeta{
		ReportGroupID: uuid.MustParse(pw.groupID),
		Repository:    defaultRepository,
		Framework:     "play\x00\xff\xfewright",
	}, []ingest.CaseIdentityInput{
		{
			File:      "e2e/fw-fallback.spec.ts",
			Title:     "empty err fallback",
			FullTitle: "Suite > empty err fallback",
			Status:    ingest.StatusFailed,
		},
		{
			File:         "e2e/fw-fallback.spec.ts",
			Title:        "nul passed status",
			FullTitle:    "Suite > nul passed status",
			Status:       "pas\x00sed",
			ErrorMessage: "should-not-create-sig",
		},
	})
	if err != nil {
		t.Fatalf("ResolveCaseIdentities with dirty framework failed: %v", err)
	}
	if len(ids) != 2 || ids[0].SignatureID == nil || ids[1].SignatureID != nil {
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

	// Out-of-order ingestion updates failure_signatures.first_seen_at and first_seen_report_group_id to the earlier occurrence.
	newerTime := now.Add(-2 * time.Hour).UTC().Truncate(time.Microsecond)
	olderTime := now.Add(-48 * time.Hour).UTC().Truncate(time.Microsecond)
	var oldGroupID, laterGroupID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright', 'ooo-old', 'completed', $1, 'main', 'sha-ooo-old', 'run-ooo-old', '1')
		RETURNING id
	`, defaultRepository).Scan(&oldGroupID); err != nil {
		t.Fatalf("insert older group: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright', 'ooo-new', 'completed', $1, 'main', 'sha-ooo-new', 'run-ooo-new', '1')
		RETURNING id
	`, defaultRepository).Scan(&laterGroupID); err != nil {
		t.Fatalf("insert newer group: %v", err)
	}
	oooCase := ingest.CaseIdentityInput{
		File:         "e2e/ooo_first_seen.spec.ts",
		Title:        "ooo test",
		FullTitle:    "Suite > ooo test",
		Status:       ingest.StatusFailed,
		ErrorMessage: "Error: ooo deterministic signature mismatch 9911",
	}
	txNew, err := env.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin ooo new tx: %v", err)
	}
	oooNewIDs, err := ingest.ResolveCaseIdentities(ctx, txNew, ingest.IdentityMeta{
		ReportGroupID: laterGroupID,
		Repository:    defaultRepository,
		Framework:     "playwright",
		SeenAt:        newerTime,
	}, []ingest.CaseIdentityInput{oooCase})
	if err != nil || len(oooNewIDs) != 1 || oooNewIDs[0].SignatureID == nil {
		_ = txNew.Rollback(ctx)
		t.Fatalf("ooo newer ResolveCaseIdentities failed: err=%v ids=%+v", err, oooNewIDs)
	}
	if err := txNew.Commit(ctx); err != nil {
		t.Fatalf("commit ooo new tx: %v", err)
	}
	txOld, err := env.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin ooo old tx: %v", err)
	}
	oooOldIDs, err := ingest.ResolveCaseIdentities(ctx, txOld, ingest.IdentityMeta{
		ReportGroupID: oldGroupID,
		Repository:    defaultRepository,
		Framework:     "playwright",
		SeenAt:        olderTime,
	}, []ingest.CaseIdentityInput{oooCase})
	if err != nil || len(oooOldIDs) != 1 || oooOldIDs[0].SignatureID == nil {
		_ = txOld.Rollback(ctx)
		t.Fatalf("ooo older ResolveCaseIdentities failed: err=%v ids=%+v", err, oooOldIDs)
	}
	if err := txOld.Commit(ctx); err != nil {
		t.Fatalf("commit ooo old tx: %v", err)
	}
	var gotFirstSeen time.Time
	var gotFirstGroup uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		SELECT first_seen_at, first_seen_report_group_id
		  FROM failure_signatures
		 WHERE id = $1
	`, *oooOldIDs[0].SignatureID).Scan(&gotFirstSeen, &gotFirstGroup); err != nil {
		t.Fatalf("query ooo failure_signatures: %v", err)
	}
	if !gotFirstSeen.UTC().Equal(olderTime) || gotFirstGroup != oldGroupID {
		t.Fatalf("out-of-order signature first_seen = (%s, %s), want (%s, %s)",
			gotFirstSeen.UTC(), gotFirstGroup, olderTime, oldGroupID)
	}

	// Epoch 1970-01-01 StartTime falls back to defaultSeen, and uuid.Nil groupID on an earlier
	// ingest must preserve an existing non-NULL first_seen_report_group_id.
	epochTime := time.Unix(0, 0).UTC()
	earlierNoGroup := olderTime.Add(-24 * time.Hour).UTC().Truncate(time.Microsecond)
	txGuard, err := env.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin ooo guard tx: %v", err)
	}
	guardCaseEpoch := oooCase
	guardCaseEpoch.StartTime = &epochTime
	if _, err := ingest.ResolveCaseIdentities(ctx, txGuard, ingest.IdentityMeta{
		ReportGroupID: uuid.Nil,
		Repository:    defaultRepository,
		Framework:     "playwright",
		SeenAt:        earlierNoGroup,
	}, []ingest.CaseIdentityInput{guardCaseEpoch}); err != nil {
		_ = txGuard.Rollback(ctx)
		t.Fatalf("guard ResolveCaseIdentities failed: %v", err)
	}
	if err := txGuard.Commit(ctx); err != nil {
		t.Fatalf("commit ooo guard tx: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		SELECT first_seen_at, first_seen_report_group_id
		  FROM failure_signatures
		 WHERE id = $1
	`, *oooOldIDs[0].SignatureID).Scan(&gotFirstSeen, &gotFirstGroup); err != nil {
		t.Fatalf("query guarded failure_signatures: %v", err)
	}
	if !gotFirstSeen.UTC().Equal(earlierNoGroup) || gotFirstGroup != oldGroupID {
		t.Fatalf("guarded signature first_seen = (%s, %s), want (%s, %s)",
			gotFirstSeen.UTC(), gotFirstGroup, earlierNoGroup, oldGroupID)
	}

	// 4. Sanitized attachment lookup links staged screenshot, countStatuses matches stored cases,
	// and annotations preserve large integers + clean keys on collision.
	var regReportID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO reports (report_group_id, name, gh_job_id)
		VALUES ($1, 'pw-nul-regression', 'job-nul-regression')
		RETURNING id
	`, pw.groupID).Scan(&regReportID); err != nil {
		t.Fatalf("insert regression report: %v", err)
	}
	const wantShotKey = "reports/reg/shot.png"
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO report_screenshots (report_id, filename, s3_key, size_bytes, test_name)
		VALUES ($1, 'shot.png', $2, 128, 'Suite > signin')
	`, regReportID, wantShotKey); err != nil {
		t.Fatalf("insert staged screenshot: %v", err)
	}
	totals, err := ingest.Consolidate(ctx, env.Pool, regReportID, []ingest.ExtractedSuite{{
		Title:     "Suite",
		StartTime: &now,
		Cases: []ingest.ExtractedCase{
			{
				Title:       "sign\x00in",
				FullTitle:   "Suite > sign\x00in",
				Status:      ingest.StatusPassed,
				Annotations: []json.RawMessage{json.RawMessage(`{"id":9007199254740993,"issue":"keep","issue\u0000":"drop"}`)},
				Attachments: []ingest.ExtractedAttachment{{Path: "dir/shot\x00.png"}},
				Sequence:    0,
			},
			{
				Title:     "signin",
				FullTitle: "Suite > signin",
				Status:    ingest.StatusPassed,
				Sequence:  1,
			},
		},
	}}, &now, &now)
	if err != nil {
		t.Fatalf("Consolidate regression case failed: %v", err)
	}
	if totals.Cases != 1 || totals.Passed != 1 {
		t.Fatalf("Consolidate totals = %+v, want Cases=1 Passed=1", totals)
	}
	var regCaseID uuid.UUID
	var regAnnsJSON, regAttsJSON string
	var linkedCaseID *uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		SELECT tc.id, tc.annotations::text, tc.attachments::text
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		WHERE s.report_id = $1 AND tc.ordinal = 0
	`, regReportID).Scan(&regCaseID, &regAnnsJSON, &regAttsJSON); err != nil {
		t.Fatalf("query regression test_case: %v", err)
	}
	if !strings.Contains(regAnnsJSON, "9007199254740993") || !strings.Contains(regAnnsJSON, `"issue": "keep"`) && !strings.Contains(regAnnsJSON, `"issue":"keep"`) {
		t.Fatalf("annotations lost precision or clean key: %s", regAnnsJSON)
	}
	if strings.Contains(regAnnsJSON, `"drop"`) {
		t.Fatalf("dirty annotation key overwrote clean key: %s", regAnnsJSON)
	}
	if !strings.Contains(regAttsJSON, wantShotKey) || !strings.Contains(regAttsJSON, `"missing": false`) && !strings.Contains(regAttsJSON, `"missing":false`) {
		t.Fatalf("sanitized attachment path did not match staged screenshot: %s", regAttsJSON)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT case_id FROM report_screenshots WHERE report_id = $1`, regReportID).Scan(&linkedCaseID); err != nil {
		t.Fatalf("query linked screenshot: %v", err)
	}
	if linkedCaseID == nil || *linkedCaseID != regCaseID {
		t.Fatalf("report_screenshots.case_id = %v, want %s", linkedCaseID, regCaseID)
	}

	// 5. Status with NUL bytes is sanitized before signature classification:
	// "pas\x00sed" persists status='passed' with signature_id IS NULL (no bogus signature row),
	// and "timed\x00Out" yields the "timeout" fallback signature.
	var statusReportID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO reports (report_group_id, name, gh_job_id)
		VALUES ($1, 'pw-nul-status', 'job-nul-status')
		RETURNING id
	`, pw.groupID).Scan(&statusReportID); err != nil {
		t.Fatalf("insert status report: %v", err)
	}
	var sigCountBefore int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM failure_signatures`).Scan(&sigCountBefore); err != nil {
		t.Fatalf("count signatures before: %v", err)
	}
	if _, err := ingest.Consolidate(ctx, env.Pool, statusReportID, []ingest.ExtractedSuite{{
		Title:     "StatusSuite",
		StartTime: &now,
		Cases: []ingest.ExtractedCase{
			{Title: "nul passed", FullTitle: "StatusSuite > nul passed", Status: "pas\x00sed", Sequence: 0},
			{Title: "nul timedOut", FullTitle: "StatusSuite > nul timedOut", Status: "timed\x00Out", Sequence: 1},
		},
	}}, &now, &now); err != nil {
		t.Fatalf("Consolidate NUL status cases failed: %v", err)
	}
	var sigCountAfter, bogusPassSigs int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM failure_signatures`).Scan(&sigCountAfter); err != nil {
		t.Fatalf("count signatures after: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM failure_signatures WHERE normalized_message = 'should-not-create-sig'`).Scan(&bogusPassSigs); err != nil {
		t.Fatalf("query bogus pass signature: %v", err)
	}
	if sigCountAfter != sigCountBefore+1 || bogusPassSigs != 0 {
		t.Fatalf("signature count delta = %d (want 1), bogusPassSigs = %d (want 0)", sigCountAfter-sigCountBefore, bogusPassSigs)
	}
	rows, err := env.Pool.Query(ctx, `
		SELECT tc.status, tc.signature_id, fs.normalized_message
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		LEFT JOIN failure_signatures fs ON fs.id = tc.signature_id
		WHERE s.report_id = $1
		ORDER BY tc.ordinal
	`, statusReportID)
	if err != nil {
		t.Fatalf("query NUL status cases: %v", err)
	}
	defer rows.Close()
	type statusRow struct {
		status  string
		sigID   *uuid.UUID
		normMsg *string
	}
	var gotStatusRows []statusRow
	for rows.Next() {
		var r statusRow
		if err := rows.Scan(&r.status, &r.sigID, &r.normMsg); err != nil {
			t.Fatalf("scan NUL status case: %v", err)
		}
		gotStatusRows = append(gotStatusRows, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate NUL status cases: %v", err)
	}
	if len(gotStatusRows) != 2 {
		t.Fatalf("NUL status rows len = %d, want 2", len(gotStatusRows))
	}
	if gotStatusRows[0].status != ingest.StatusPassed || gotStatusRows[0].sigID != nil {
		t.Fatalf("pas\\x00sed row = %+v, want status=passed sigID=nil", gotStatusRows[0])
	}
	if gotStatusRows[1].status != "timedOut" || gotStatusRows[1].sigID == nil || gotStatusRows[1].normMsg == nil || *gotStatusRows[1].normMsg != "timeout" {
		t.Fatalf("timed\\x00Out row = %+v, want status=timedOut normMsg=timeout", gotStatusRows[1])
	}
}

func TestIngestTestIDs_AnnotationNumericRangeAndDedup(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-numeric-e2e", 1, string(policy.RoleUploader), map[string]string{
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
	payload := `{"suites":[{"title":"num.spec.ts","file":"e2e/num.spec.ts","specs":[{"title":"numbers","tests":[{
		"status":"expected",
		"annotations":[{"type":"big","n":1e200000},{"type":"id","id":1},{"type":"id","id":1.0},
			{"type":"exact","id":9007199254740993},{"type":"exact","id":9007199254740992}],
		"results":[{"status":"passed","retry":0}]}]}]}]}`
	pw := uploadReportShard(t, env, tok, "playwright", "run-id-pw-numeric", "results.json", payload)

	var annsJSON string
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT tc.annotations::text
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		WHERE s.report_id = $1
	`, pw.uploadID).Scan(&annsJSON); err != nil {
		t.Fatalf("query numeric annotations case: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(annsJSON))
	dec.UseNumber()
	var anns []map[string]any
	if err := dec.Decode(&anns); err != nil {
		t.Fatalf("decode annotations %s: %v", annsJSON, err)
	}
	var ids []string
	for _, a := range anns {
		ids = append(ids, fmt.Sprint(a["id"]))
	}
	if got := strings.Join(ids, ","); got != "1,9007199254740993,9007199254740992" {
		t.Fatalf("annotation ids = %s (anns=%s), want 1,9007199254740993,9007199254740992", got, annsJSON)
	}
}

func TestConsolidate_DeferredSignatureAttributionOnPhase2Failure(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	insertReport := func(runID, jobID string) (uuid.UUID, uuid.UUID) {
		t.Helper()
		var groupID, reportID uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
			VALUES ('playwright', 'pw-phase2-attr', 'mattermost/phase2-attr', 'main', 'deadbeef', $1, '1')
			RETURNING id
		`, runID).Scan(&groupID); err != nil {
			t.Fatalf("insert report_group %s: %v", runID, err)
		}
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO reports (report_group_id, name, gh_job_id)
			VALUES ($1, 'pw-phase2-attr', $2)
			RETURNING id
		`, groupID, jobID).Scan(&reportID); err != nil {
			t.Fatalf("insert report %s: %v", jobID, err)
		}
		return groupID, reportID
	}

	group1, report1 := insertReport("run-attr-1", "job-attr-1")
	group2, report2 := insertReport("run-attr-2", "job-attr-2")
	_, report3 := insertReport("run-attr-3", "job-attr-3")

	file := "e2e/phase2_fail.spec.ts"
	errMsg := "Error: phase2 attribution failure sentinel"
	now := time.Now().UTC()
	suites := []ingest.ExtractedSuite{{
		Title:     "Phase2Fail",
		FilePath:  &file,
		StartTime: &now,
		Cases: []ingest.ExtractedCase{{
			Title:        "fails in phase 2",
			FullTitle:    "Phase2Fail > fails in phase 2",
			Status:       ingest.StatusFailed,
			ErrorMessage: &errMsg,
			Sequence:     0,
		}},
	}}
	wantSig := errorsig.Compute(errorsig.Input{
		Framework:    "playwright",
		Status:       ingest.StatusFailed,
		ErrorMessage: errMsg,
	})

	// Force a Phase-2 failure after resolveCaseIdentitiesTx has committed.
	if _, err := env.Pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION fail_phase2_case_insert() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'simulated phase-2 insert failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE CONSTRAINT TRIGGER trg_fail_phase2_case_insert
		AFTER INSERT ON test_cases
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION fail_phase2_case_insert();
	`); err != nil {
		t.Fatalf("install phase-2 failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.Pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS trg_fail_phase2_case_insert ON test_cases;
			DROP FUNCTION IF EXISTS fail_phase2_case_insert();
		`)
	})

	// The deferred trigger fires at Commit, after case insert and attribution ran.
	if _, err := ingest.Consolidate(ctx, env.Pool, report1, suites, &now, &now); err == nil ||
		!strings.Contains(err.Error(), "simulated phase-2 insert failure") {
		t.Fatalf("Consolidate err = %v, want simulated phase-2 insert failure", err)
	}

	var firstGroupID *uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		SELECT first_seen_report_group_id
		FROM failure_signatures
		WHERE fingerprint = $1
	`, wantSig.Fingerprint).Scan(&firstGroupID); err != nil {
		t.Fatalf("query signature after Phase-2 failure: %v", err)
	}
	if firstGroupID != nil {
		t.Fatalf("first_seen_report_group_id after Phase-2 failure = %v (group1=%s), want nil", firstGroupID, group1)
	}

	var group1Cases int
	if err := env.Pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		WHERE r.report_group_id = $1
	`, group1).Scan(&group1Cases); err != nil {
		t.Fatalf("count group1 test_cases: %v", err)
	}
	if group1Cases != 0 {
		t.Fatalf("group1 test_cases count = %d, want 0", group1Cases)
	}

	// Remove the failure trigger and retry on report2 (in group2); attribution must stick to group2.
	if _, err := env.Pool.Exec(ctx, `DROP TRIGGER trg_fail_phase2_case_insert ON test_cases`); err != nil {
		t.Fatalf("drop phase-2 failure trigger: %v", err)
	}
	if _, err := ingest.Consolidate(ctx, env.Pool, report2, suites, &now, &now); err != nil {
		t.Fatalf("retry Consolidate on report2 failed: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		SELECT first_seen_report_group_id
		FROM failure_signatures
		WHERE fingerprint = $1
	`, wantSig.Fingerprint).Scan(&firstGroupID); err != nil {
		t.Fatalf("query signature after retry: %v", err)
	}
	if firstGroupID == nil || *firstGroupID != group2 {
		t.Fatalf("first_seen_report_group_id after retry = %v, want %s", firstGroupID, group2)
	}

	// A subsequent group hitting the same signature must not overwrite first_seen_report_group_id.
	if _, err := ingest.Consolidate(ctx, env.Pool, report3, suites, &now, &now); err != nil {
		t.Fatalf("Consolidate on report3 failed: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		SELECT first_seen_report_group_id
		FROM failure_signatures
		WHERE fingerprint = $1
	`, wantSig.Fingerprint).Scan(&firstGroupID); err != nil {
		t.Fatalf("query signature after report3: %v", err)
	}
	if firstGroupID == nil || *firstGroupID != group2 {
		t.Fatalf("first_seen_report_group_id after report3 = %v, want %s", firstGroupID, group2)
	}
}

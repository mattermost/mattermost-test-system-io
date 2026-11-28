//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/backfill"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const bfPlaywright = `{"suites": [{"title": "login.spec.ts", "file": "e2e/login.spec.ts", "specs": [
  {"title": "MM-T101 logs in", "tags": ["@smoke"], "tests": [{
    "projectName": "chromium", "status": "unexpected",
    "annotations": [{"type": "issue", "description": "MM-1"}],
    "results": [{"status": "failed", "duration": 100, "retry": 0, "errors": [{
      "message": "TimeoutError: waiting for locator exceeded 30000ms",
      "stack": "TimeoutError: waiting for locator exceeded 30000ms\n    at e2e/login.spec.ts:14:5"}]}]}]},
  {"title": "no error case", "tests": [{"projectName": "chromium", "status": "unexpected",
    "results": [{"status": "failed", "duration": 10, "retry": 0}]}]},
  {"title": "passes", "tests": [{"projectName": "chromium", "status": "expected",
    "results": [{"status": "passed", "duration": 5, "retry": 0}]}]}
]}]}`

const bfCypress = `{"results": [{"title": "", "file": "cypress/e2e/channel.cy.ts", "suites": [{"title": "Channel", "tests": [
  {"title": "fails persistently", "fullTitle": "Channel fails persistently", "duration": 200,
   "state": "failed", "fail": true,
   "err": {"message": "AssertionError: Timed out retrying", "estack": "AssertionError: Timed out retrying\n    at cypress/e2e/channel.cy.ts:20:5"}},
  {"title": "works", "fullTitle": "Channel works", "duration": 20, "state": "passed", "pass": true}
]}]}]}`

const bfMaestro = `<testsuites>
  <testsuite name="Maestro Flow" tests="2" failures="1" timestamp="2026-01-01T00:00:00Z">
    <testcase name="flow fails silently" file="e2e/maestro/flows/login.yml" time="1.2"><failure message=""/></testcase>
    <testcase name="flow passes" file="e2e/maestro/flows/login.yml" time="0.5"/>
  </testsuite>
</testsuites>`

const bfTwoProjects = `{"suites": [{"title": "multi.spec.ts", "file": "e2e/multi.spec.ts", "specs": [
  {"title": "renders", "tests": [
    {"projectName": "chromium", "status": "unexpected", "results": [{"status": "failed", "duration": 1, "retry": 0,
      "errors": [{"message": "chromium boom", "stack": "chromium boom\n    at e2e/multi.spec.ts:1:1"}]}]},
    {"projectName": "firefox", "status": "unexpected", "results": [{"status": "failed", "duration": 1, "retry": 0,
      "errors": [{"message": "firefox boom", "stack": "firefox boom\n    at e2e/multi.spec.ts:2:2"}]}]}
  ]}
]}]}`

const bfFirefoxOnly = `{"suites": [{"title": "multi.spec.ts", "file": "e2e/multi.spec.ts", "specs": [
  {"title": "renders", "tests": [
    {"projectName": "firefox", "status": "unexpected", "results": [{"status": "failed", "duration": 1, "retry": 0,
      "errors": [{"message": "firefox boom", "stack": "firefox boom\n    at e2e/multi.spec.ts:2:2"}]}]}
  ]}
]}]}`

type bfCase struct {
	id                       uuid.UUID
	testID, sigID, sigGroup  *uuid.UUID
	errorStack, project, msg *string
	tags, annotations        string
}

// errStore fails every Get like an unreachable S3 endpoint would.
type errStore struct{ storage.ObjectStore }

func (errStore) Get(context.Context, string) (io.ReadCloser, storage.ObjectMeta, error) {
	return nil, storage.ObjectMeta{}, errors.New("dial tcp: connection reset by peer")
}

// hookStore runs before() ahead of the Get for key, simulating another writer
// committing between the backfill's case snapshot and its UPDATE.
type hookStore struct {
	storage.ObjectStore
	key    string
	before func()
	gets   int
}

func (h *hookStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	h.gets++
	if key == h.key && h.before != nil {
		h.before()
	}
	return h.ObjectStore.Get(ctx, key)
}

// startBackfillEnv uploads one report per payload ([framework, run id, file,
// body]) through the real ingest path, then strips every SEC-11519 column so
// the reports look like they were ingested before it.
func startBackfillEnv(t *testing.T, uploads ...[4]string) (*testenv.Env, []shard) {
	t.Helper()
	env := testenv.Start(t, testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-reports-e2e", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultRepoOwner,
	})
	tok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject: defaultOIDCSubject, Audience: "tsio", Repository: defaultRepository,
		RepositoryOwner: defaultRepoOwner, Workflow: "ci", Ref: "refs/heads/main",
	})
	shards := make([]shard, len(uploads))
	for i, u := range uploads {
		shards[i] = uploadReportShard(t, env, tok, u[0], u[1], u[2], u[3])
	}
	// finalizeResults marks report_groups complete before calling
	// refreshGroupSummaryBestEffort -> kpi.RefreshGroupRollupsBestEffort; wait
	// for async rollup refresh to finish before stripping test_cases/signatures.
	// Note: Register already sets kpi_rollups_refreshed_at synchronously, so check
	// version and staleness against updated_at / last_upload_at rather than IS NULL alone.
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var unrefreshed int
		if err := env.Pool.QueryRow(waitCtx, `
			SELECT count(*)
			  FROM report_groups g
			 WHERE g.kpi_rollups_refreshed_at IS NULL
			    OR g.kpi_rollups_version < g.kpi_summary_version
			    OR g.kpi_rollups_refreshed_at < GREATEST(g.updated_at, g.last_upload_at)
			    OR EXISTS (
			          SELECT 1 FROM reports r
			           WHERE r.report_group_id = g.id
			             AND r.updated_at > g.kpi_rollups_refreshed_at)
		`).Scan(&unrefreshed); err != nil {
			t.Fatalf("wait for async rollup refresh: %v", err)
		}
		if unrefreshed == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	bfExec(t, env, `UPDATE test_cases SET test_id = NULL, signature_id = NULL, error_stack = NULL,
		project = NULL, tags = '[]', annotations = '[]'`)
	bfExec(t, env, `DELETE FROM failure_signatures`)
	bfExec(t, env, `DELETE FROM tests`)
	bfExec(t, env, `UPDATE report_groups SET stage = 'other', event_name = NULL, branch = 'feature-x', gh_pr_number = NULL`)
	return env, shards
}

func bfExec(t *testing.T, env *testenv.Env, sql string, args ...any) {
	t.Helper()
	if _, err := env.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func bfInt(t *testing.T, env *testenv.Env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

func bfStage(t *testing.T, env *testenv.Env, groupID string) string {
	t.Helper()
	var stage string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT stage FROM report_groups WHERE id = $1`, groupID).Scan(&stage); err != nil {
		t.Fatalf("read stage: %v", err)
	}
	return stage
}

func bfCaseIDs(t *testing.T, env *testenv.Env) []uuid.UUID {
	t.Helper()
	rows, err := env.Pool.Query(context.Background(), `SELECT id FROM test_cases ORDER BY id`)
	if err != nil {
		t.Fatalf("query case ids: %v", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan case id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func bfCases(t *testing.T, env *testenv.Env, reportID string) map[string]bfCase {
	t.Helper()
	rows, err := env.Pool.Query(context.Background(), `
		SELECT tc.title, tc.id, tc.test_id, tc.signature_id, fs.first_seen_report_group_id,
		       tc.error_stack, tc.project, fs.normalized_message, tc.tags::text, tc.annotations::text
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		LEFT JOIN failure_signatures fs ON fs.id = tc.signature_id
		WHERE s.report_id = $1`, reportID)
	if err != nil {
		t.Fatalf("query cases: %v", err)
	}
	defer rows.Close()
	out := map[string]bfCase{}
	for rows.Next() {
		var title string
		var c bfCase
		if err := rows.Scan(&title, &c.id, &c.testID, &c.sigID, &c.sigGroup,
			&c.errorStack, &c.project, &c.msg, &c.tags, &c.annotations); err != nil {
			t.Fatalf("scan case: %v", err)
		}
		out[title] = c
	}
	return out
}

func bfCheckpoint(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	return b
}

func TestBackfill_StagesCasesDryRunAndIdempotency(t *testing.T) {
	env, s := startBackfillEnv(t,
		[4]string{"playwright", "bf-pw", "results.json", bfPlaywright},
		[4]string{"cypress", "bf-cy", "results.json", bfCypress},
		[4]string{"maestro", "bf-ma", "results.xml", bfMaestro},
		[4]string{"playwright", "bf-missing", "results.json", bfPlaywright},
	)
	pw, cy, ma, missing := s[0], s[1], s[2], s[3]
	ctx := context.Background()

	bfExec(t, env, `UPDATE report_groups SET branch = 'main' WHERE id = $1`, pw.groupID)
	bfExec(t, env, `UPDATE report_groups SET event_name = 'pull_request' WHERE id = $1`, cy.groupID)
	bfExec(t, env, `UPDATE report_groups SET stage = 'nightly', branch = 'main' WHERE id = $1`, ma.groupID)

	var missingKey string
	if err := env.Pool.QueryRow(ctx, `SELECT object_key FROM report_json_files WHERE report_id = $1`,
		missing.uploadID).Scan(&missingKey); err != nil {
		t.Fatalf("read missing key: %v", err)
	}
	if err := env.Store.Delete(ctx, missingKey); err != nil {
		t.Fatalf("delete s3 object: %v", err)
	}

	pwFail := bfCases(t, env, pw.uploadID)["MM-T101 logs in"].id
	bfExec(t, env, `INSERT INTO report_screenshots (report_id, filename, s3_key, size_bytes, test_name, case_id)
		VALUES ($1, 'shot.png', 'bf/shot.png', 4, 'MM-T101 logs in', $2)`, pw.uploadID, pwFail)
	idsBefore := bfCaseIDs(t, env)
	cpFile := filepath.Join(t.TempDir(), "checkpoint.json")

	// Dry run plans every change but writes nothing, not even the checkpoint.
	dry, err := backfill.Run(ctx, env.Pool, env.Store, backfill.Options{DryRun: true, CheckpointFile: cpFile})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !dry.DryRun || dry.StageGroupsUpdated != 2 || dry.ReportsUpdated != 4 || dry.CasesUpdated != 10 ||
		dry.S3FilesParsed != 3 || dry.S3FilesMissing != 1 || dry.CasesEnrichedFromS3 == 0 {
		t.Fatalf("dry run summary = %+v", dry)
	}
	if n := bfInt(t, env, `SELECT count(*) FROM test_cases
		WHERE test_id IS NOT NULL OR signature_id IS NOT NULL OR error_stack IS NOT NULL OR project IS NOT NULL
		   OR tags <> '[]' OR annotations <> '[]'`); n != 0 {
		t.Fatalf("dry run wrote %d test_cases", n)
	}
	if n := bfInt(t, env, `SELECT (SELECT count(*) FROM tests) + (SELECT count(*) FROM failure_signatures)
		+ (SELECT count(*) FROM report_groups WHERE stage NOT IN ('other', 'nightly'))`); n != 0 {
		t.Fatalf("dry run wrote %d tests/signatures/stages", n)
	}
	if _, err := os.Stat(cpFile); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote checkpoint: %v", err)
	}

	sum, err := backfill.Run(ctx, env.Pool, env.Store, backfill.Options{CheckpointFile: cpFile})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if sum.StageGroupsScanned != 3 || sum.StageGroupsUpdated != 2 || sum.ReportsScanned != 4 ||
		sum.ReportsUpdated != 4 || sum.CasesUpdated != dry.CasesUpdated ||
		sum.CasesEnrichedFromS3 != dry.CasesEnrichedFromS3 || sum.S3FilesMissing != 1 {
		t.Fatalf("summary = %+v, dry run = %+v", sum, dry)
	}

	for group, want := range map[string]string{
		pw.groupID: "main", cy.groupID: "pr", ma.groupID: "nightly", missing.groupID: "other",
	} {
		if got := bfStage(t, env, group); got != want {
			t.Errorf("group %s stage = %q, want %q", group, got, want)
		}
	}
	if idsAfter := bfCaseIDs(t, env); !slices.Equal(idsBefore, idsAfter) {
		t.Fatalf("test_cases ids changed:\nbefore %v\nafter  %v", idsBefore, idsAfter)
	}
	if n := bfInt(t, env, `SELECT count(*) FROM report_screenshots WHERE case_id = $1`, pwFail); n != 1 {
		t.Fatalf("report_screenshots.case_id lost its link (%d rows)", n)
	}
	if n := bfInt(t, env, `SELECT count(*) FROM test_cases WHERE test_id IS NULL`); n != 0 {
		t.Fatalf("%d test_cases still lack test_id", n)
	}

	pwc := bfCases(t, env, pw.uploadID)
	fail := pwc["MM-T101 logs in"]
	if fail.errorStack == nil || !strings.Contains(*fail.errorStack, "login.spec.ts:14:5") ||
		fail.project == nil || *fail.project != "chromium" || fail.tags != `["@smoke"]` ||
		!strings.Contains(fail.annotations, "MM-1") {
		t.Fatalf("playwright enrichment = %+v", fail)
	}
	if fail.sigID == nil || fail.sigGroup == nil || fail.sigGroup.String() != pw.groupID {
		t.Fatalf("playwright signature/attribution = %v / %v, want group %s", fail.sigID, fail.sigGroup, pw.groupID)
	}
	if c := pwc["no error case"]; c.msg == nil || *c.msg != "no-error:unknown:playwright" {
		t.Fatalf("playwright empty-error fallback = %v", c.msg)
	}
	if c := pwc["passes"]; c.sigID != nil || c.project == nil || *c.project != "chromium" {
		t.Fatalf("playwright passing case sig=%v project=%v", c.sigID, c.project)
	}

	cyFail := bfCases(t, env, cy.uploadID)["fails persistently"]
	if cyFail.errorStack == nil || !strings.Contains(*cyFail.errorStack, "channel.cy.ts:20:5") || cyFail.sigID == nil {
		t.Fatalf("cypress enrichment = %+v", cyFail)
	}
	if c := bfCases(t, env, ma.uploadID)["flow fails silently"]; c.msg == nil || *c.msg != "no-error:unknown:maestro" {
		t.Fatalf("maestro empty-error fallback = %v", c.msg)
	}

	// Missing S3 object: identities still come from DB columns, S3-only fields stay empty.
	mf := bfCases(t, env, missing.uploadID)["MM-T101 logs in"]
	if mf.testID == nil || *mf.testID != *fail.testID || mf.sigID == nil || mf.errorStack != nil || mf.project != nil {
		t.Fatalf("missing-S3 fallback = %+v", mf)
	}

	counting := &hookStore{ObjectStore: env.Store}
	again, err := backfill.Run(ctx, env.Pool, counting, backfill.Options{})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again.ReportsScanned != 4 || again.ReportsSkipped != 4 || again.ReportsUpdated != 0 ||
		again.CasesUpdated != 0 || again.S3FilesParsed != 0 || again.StageGroupsUpdated != 0 {
		t.Fatalf("re-run not idempotent: %+v", again)
	}
	if counting.gets != 0 {
		t.Fatalf("re-run triggered %d Store.Get calls, want 0", counting.gets)
	}
}

func TestBackfill_CheckpointPauseResumeAndTransientS3Abort(t *testing.T) {
	env, _ := startBackfillEnv(t,
		[4]string{"playwright", "bf-a", "results.json", bfPlaywright},
		[4]string{"cypress", "bf-b", "results.json", bfCypress},
	)
	ctx := context.Background()
	var first, second string
	if err := env.Pool.QueryRow(ctx, `SELECT min(id::text), max(id::text) FROM reports`).Scan(&first, &second); err != nil {
		t.Fatalf("read report ids: %v", err)
	}
	unset := func(reportID string) int {
		return bfInt(t, env, `SELECT count(*) FROM test_cases tc JOIN suites s ON s.id = tc.suite_id
			WHERE s.report_id = $1 AND tc.test_id IS NULL`, reportID)
	}
	cpFile := filepath.Join(t.TempDir(), "checkpoint.json")
	sleeps := 0
	opts := backfill.Options{
		BatchSize: 1, SkipStage: true, CheckpointFile: cpFile, SleepInterval: time.Hour,
		Sleep: func(_ context.Context, d time.Duration) error {
			if d != time.Hour {
				t.Errorf("sleep %s, want 1h", d)
			}
			sleeps++
			return nil
		},
	}

	paused := opts
	paused.MaxBatches = 1
	sum, err := backfill.Run(ctx, env.Pool, env.Store, paused)
	if err != nil {
		t.Fatalf("paused run: %v", err)
	}
	if sum.BatchesRun != 1 || sum.ReportsScanned != 1 || sum.LastReportID.String() != first || sleeps != 0 {
		t.Fatalf("paused run = %+v, sleeps %d", sum, sleeps)
	}
	if unset(first) != 0 || unset(second) == 0 {
		t.Fatalf("paused run touched the wrong report: first unset=%d second unset=%d", unset(first), unset(second))
	}
	cp := bfCheckpoint(t, cpFile)
	if !strings.Contains(string(cp), `"last_report_id": "`+first+`"`) {
		t.Fatalf("checkpoint = %s, want last_report_id %s", cp, first)
	}

	if _, err := backfill.Run(ctx, env.Pool, errStore{env.Store}, opts); err == nil ||
		!strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("transient S3 error = %v, want abort", err)
	}
	if got := bfCheckpoint(t, cpFile); !bytes.Equal(got, cp) || unset(second) == 0 {
		t.Fatalf("transient abort advanced: checkpoint %s, second unset %d", got, unset(second))
	}

	sum, err = backfill.Run(ctx, env.Pool, env.Store, opts)
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if sum.BatchesRun != 1 || sum.ReportsScanned != 1 || sum.LastReportID.String() != second || sleeps != 1 {
		t.Fatalf("resumed run = %+v, sleeps %d", sum, sleeps)
	}
	if unset(second) != 0 || !strings.Contains(string(bfCheckpoint(t, cpFile)), second) {
		t.Fatalf("resume did not finish: second unset=%d", unset(second))
	}
}

func bfJSONKey(t *testing.T, env *testenv.Env, reportID string) string {
	t.Helper()
	var key string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT object_key FROM report_json_files WHERE report_id = $1`, reportID).Scan(&key); err != nil {
		t.Fatalf("read json key: %v", err)
	}
	return key
}

func TestBackfill_PartialS3FallbackAndConcurrentWritesWin(t *testing.T) {
	env, s := startBackfillEnv(t,
		[4]string{"playwright", "bf-multi", "results.json", bfTwoProjects},
		[4]string{"playwright", "bf-race", "results.json", bfPlaywright},
	)
	multi, race := s[0], s[1]
	ctx := context.Background()

	// The report keeps its firefox file but loses its chromium one.
	if err := env.Store.Put(ctx, bfJSONKey(t, env, multi.uploadID), strings.NewReader(bfFirefoxOnly),
		"application/json", int64(len(bfFirefoxOnly))); err != nil {
		t.Fatalf("rewrite firefox json: %v", err)
	}
	bfExec(t, env, `INSERT INTO report_json_files (report_id, object_key, size_bytes, sha256)
		VALUES ($1, 'bf/chromium-missing.json', 1, 'gone')`, multi.uploadID)

	raceCase := bfCases(t, env, race.uploadID)["MM-T101 logs in"].id
	var liveTestID, liveSigID uuid.UUID
	store := &hookStore{ObjectStore: env.Store, key: bfJSONKey(t, env, race.uploadID), before: func() {
		if err := env.Pool.QueryRow(ctx, `
			WITH t AS (
				INSERT INTO tests (repository, framework, test_key, file, full_title)
				VALUES ('mattermost/test', 'playwright', 'live-key', 'e2e/login.spec.ts', 'live')
				RETURNING id),
			s AS (
				INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
				VALUES ('live-sig-fp', 1, 'live concurrent error', now())
				RETURNING id)
			UPDATE test_cases SET test_id = (SELECT id FROM t), signature_id = (SELECT id FROM s),
				error_stack = 'live stack', project = 'live-project', tags = '["@live"]'
			WHERE id = $1
			RETURNING test_id, signature_id`, raceCase).Scan(&liveTestID, &liveSigID); err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}}

	sum, err := backfill.Run(ctx, env.Pool, store, backfill.Options{SkipStage: true})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if sum.S3FilesMissing != 1 || sum.ReportsUpdated != 2 {
		t.Fatalf("summary = %+v", sum)
	}

	// Whole report fell back to DB fields: firefox data never lands on the chromium row.
	if n := bfInt(t, env, `SELECT count(*) FROM test_cases tc JOIN suites s ON s.id = tc.suite_id
		WHERE s.report_id = $1 AND (tc.project IS NOT NULL OR tc.error_stack IS NOT NULL OR tc.test_id IS NULL)`,
		multi.uploadID); n != 0 {
		t.Fatalf("%d multi-project rows were cross-enriched or left without test_id", n)
	}

	// Values committed after the snapshot win; untouched blanks are still enriched.
	// Attribution must target the returned liveSigID rather than the unstored snapshot signature.
	c := bfCases(t, env, race.uploadID)["MM-T101 logs in"]
	if c.testID == nil || *c.testID != liveTestID || c.sigID == nil || *c.sigID != liveSigID ||
		c.sigGroup == nil || c.sigGroup.String() != race.groupID ||
		c.errorStack == nil || *c.errorStack != "live stack" ||
		c.project == nil || *c.project != "live-project" || c.tags != `["@live"]` ||
		!strings.Contains(c.annotations, "MM-1") {
		t.Fatalf("concurrent write not preserved: %+v (live test_id %s, sig_id %s)", c, liveTestID, liveSigID)
	}
	if n := bfInt(t, env, `SELECT count(*) FROM failure_signatures
		WHERE id <> $1 AND first_seen_report_group_id = $2 AND normalized_message LIKE 'TimeoutError:%'`,
		liveSigID, race.groupID); n != 0 {
		t.Fatalf("stale snapshot signature was falsely attributed (%d rows)", n)
	}
}

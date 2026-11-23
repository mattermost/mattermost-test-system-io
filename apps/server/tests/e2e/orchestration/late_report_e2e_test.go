//go:build e2e
// +build e2e

package orchestratione2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// TestLateReportAcceptedAfterReassign drives the late-report acceptance path:
// worker A's lease times out before reporting, worker B inherits the unit and
// reports it `passed`, then worker A finally reports for its (now-expired)
// lease. Per spec, A's report is accepted with `late_report=true`, the unit's
// state is preserved, superseded_unit_ids names the unit, and a separate
// attempts row records A's late result.
func TestLateReportAcceptedAfterReassign(t *testing.T) {
	env, tok := startEnv(t)

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{
			"tests/late.spec.ts",
			"tests/spare.spec.ts",
		}, map[string]any{
			"lease_timeout_ms": 1000, // 1s
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- Worker A leases the first unit. ---
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-late-A", 1))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	unitsA := bodyA["units"].([]any)
	if len(unitsA) != 1 {
		t.Fatalf("worker A got %d units, want 1", len(unitsA))
	}
	leasedUnitID := unitIDOf(t, unitsA[0].(map[string]any))

	// --- Wait until A's lease has timed out (rely on reaper or lazy on next checkout).
	time.Sleep(1500 * time.Millisecond)

	// --- Worker B picks up the reclaimed unit. ---
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-late-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	unitsB := bodyB["units"].([]any)
	if len(unitsB) != 1 {
		t.Fatalf("worker B got %d units, want 1", len(unitsB))
	}
	if unitIDOf(t, unitsB[0].(map[string]any)) != leasedUnitID {
		t.Fatalf("worker B unit_id = %s, want reclaimed %s",
			unitIDOf(t, unitsB[0].(map[string]any)), leasedUnitID)
	}

	// --- Worker B reports passed: unit transitions to completed_pass. ---
	completeB := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-late-B", passResults([]string{"tests/late.spec.ts"})))
	resB := expectStatus(t, completeB, http.StatusOK)
	if resB["late_report"].(bool) {
		t.Fatalf("worker B should be on-time, got late_report=true")
	}
	if len(resB["unit_states_changed"].([]any)) != 1 {
		t.Fatalf("worker B unit_states_changed = %d, want 1", len(resB["unit_states_changed"].([]any)))
	}
	if supB := stringSlice(t, resB["superseded_unit_ids"]); len(supB) != 0 {
		t.Fatalf("worker B superseded_unit_ids = %v, want empty", supB)
	}

	// --- Worker A finally reports the SAME passed result. Late, accepted, no state change. ---
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-late-A", passResults([]string{"tests/late.spec.ts"})))
	resA := expectStatus(t, completeA, http.StatusOK)
	if !resA["accepted"].(bool) {
		t.Fatalf("worker A late report rejected: accepted=false")
	}
	if !resA["late_report"].(bool) {
		t.Fatalf("worker A late_report = false, want true")
	}
	if len(resA["unit_states_changed"].([]any)) != 0 {
		t.Fatalf("worker A unit_states_changed = %d, want 0 (unit already finalized)",
			len(resA["unit_states_changed"].([]any)))
	}
	if supA := stringSlice(t, resA["superseded_unit_ids"]); len(supA) != 1 || supA[0] != leasedUnitID {
		t.Fatalf("worker A superseded_unit_ids = %v, want [%s]", supA, leasedUnitID)
	}

	// --- DB invariants: 2 attempts rows for tests/late.spec.ts with fresh & reclaim kinds ---
	ctx := context.Background()
	rows, err := env.Pool.Query(ctx, `
		SELECT a.expired, a.late_report, a.status, a.kind, l.kind
		  FROM attempts a
		  JOIN leases l ON l.id = a.lease_id
		  JOIN dispatch_units du ON du.id = a.dispatch_unit_id
		 WHERE du.id = $1 AND a.spec_path = 'tests/late.spec.ts'
		 ORDER BY a.created_at
	`, leasedUnitID)
	if err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	type attemptRow struct {
		expired   bool
		late      bool
		status    *string
		kind      string
		leaseKind string
	}
	var attempts []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.expired, &a.late, &a.status, &a.kind, &a.leaseKind); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		attempts = append(attempts, a)
	}
	rows.Close()
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts rows for the spec, got %d (%+v)", len(attempts), attempts)
	}

	// One attempt is the on-time reclaim pass (expired=false, late_report=false, passed, reclaim),
	// the other is the late fresh report (expired=true, late_report=true, passed, fresh).
	var sawOnTime, sawLate bool
	for _, a := range attempts {
		if a.status == nil || *a.status != "passed" {
			t.Fatalf("attempt status = %v, want passed", a.status)
		}
		if !a.expired && !a.late && a.kind == "reclaim" && a.leaseKind == "reclaim" {
			sawOnTime = true
		}
		if a.expired && a.late && a.kind == "fresh" && a.leaseKind == "fresh" {
			sawLate = true
		}
	}
	if !sawOnTime || !sawLate {
		t.Fatalf("missing attempt variants; got attempts=%+v", attempts)
	}

	// --- Unit row: lease_count==2, state=completed_pass. ---
	var (
		leaseCount int
		state      string
	)
	err = env.Pool.QueryRow(ctx, `SELECT lease_count, state FROM dispatch_units WHERE id = $1`,
		leasedUnitID).Scan(&leaseCount, &state)
	if err != nil {
		t.Fatalf("query dispatch_units: %v", err)
	}
	if leaseCount != 2 {
		t.Fatalf("lease_count = %d, want 2", leaseCount)
	}
	if state != "completed_pass" {
		t.Fatalf("state = %q, want completed_pass", state)
	}
}

// TestLateReportDoesNotStealActiveReclaimedLease verifies that when worker A's
// lease expires and worker B actively holds the reclaimed lease (state='leased'),
// worker A's late /complete returns 200 with late_report=true and
// superseded_unit_ids=[U] without stealing or mutating U, so worker B can still
// finalize U normally. Also validates /status and /complete payloads against openapi.yaml.
func TestLateReportDoesNotStealActiveReclaimedLease(t *testing.T) {
	env, tok := startEnv(t, testenv.WithReaperDisabled())
	doc, opts := loadOrchOpenAPISpec(t)

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{"tests/reclaimed.spec.ts"}, map[string]any{
			"lease_timeout_ms": 500,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A checks out unit U.
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-A", 1))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	unitID := unitIDOf(t, bodyA["units"].([]any)[0].(map[string]any))

	// Expire A's lease and let B reclaim U while B keeps it in state='leased'.
	time.Sleep(700 * time.Millisecond)
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-active-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	if len(bodyB["units"].([]any)) != 1 {
		t.Fatalf("worker B expected 1 reclaimed unit, got %v", bodyB["units"])
	}

	// /status while B holds active reclaimed lease validates against OpenAPI and exposes kind='reclaim'.
	statResp := getJSON(t, env, tok, statusURL(identity(nil)))
	statBody := expectStatusValidated(t, doc, opts, http.MethodGet, "/api/v1/orchestration/status", statResp, http.StatusOK)
	u0 := statBody["units"].([]any)[0].(map[string]any)
	curLease := u0["current_lease"].(map[string]any)
	if curLease["kind"] != "reclaim" {
		t.Fatalf("current_lease.kind = %v, want reclaim", curLease["kind"])
	}
	attempts := u0["attempts"].([]any)
	if len(attempts) != 2 || attempts[0].(map[string]any)["kind"] != "fresh" || attempts[1].(map[string]any)["kind"] != "reclaim" {
		t.Fatalf("unexpected attempts kinds: %+v", attempts)
	}

	// Look up leaseB ID for DB ownership assertion.
	var leaseAID, leaseBID uuid.UUID
	if err := env.Pool.QueryRow(context.Background(), `SELECT id FROM leases WHERE gh_job_id = 'job-active-A'`).Scan(&leaseAID); err != nil {
		t.Fatalf("query leaseA: %v", err)
	}
	if err := env.Pool.QueryRow(context.Background(), `SELECT id FROM leases WHERE gh_job_id = 'job-active-B'`).Scan(&leaseBID); err != nil {
		t.Fatalf("query leaseB: %v", err)
	}

	// Worker A reports FAILED while B still holds the unit. Must NOT steal or mutate U.
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-active-A", failResults([]string{"tests/reclaimed.spec.ts"})))
	resA := expectStatusValidated(t, doc, opts, http.MethodPost, "/api/v1/orchestration/complete", completeA, http.StatusOK)
	if !resA["accepted"].(bool) || !resA["late_report"].(bool) {
		t.Fatalf("expected accepted=true, late_report=true; got %+v", resA)
	}
	if len(resA["unit_states_changed"].([]any)) != 0 {
		t.Fatalf("worker A should not change unit state, got %v", resA["unit_states_changed"])
	}
	if supA := stringSlice(t, resA["superseded_unit_ids"]); len(supA) != 1 || supA[0] != unitID {
		t.Fatalf("worker A superseded_unit_ids = %v, want [%s]", supA, unitID)
	}

	// Confirm unit is still leased to leaseB, leased_count==1, and leaseA preserved release_reason='expired'.
	var (
		duState        string
		duCurrentLease *uuid.UUID
		leaseAReason   *string
		leasedCount    int
	)
	if err := env.Pool.QueryRow(context.Background(), `
		SELECT state, current_lease_id FROM dispatch_units WHERE id = $1
	`, unitID).Scan(&duState, &duCurrentLease); err != nil {
		t.Fatalf("query dispatch_unit: %v", err)
	}
	if duState != "leased" || duCurrentLease == nil || *duCurrentLease != leaseBID {
		t.Fatalf("unit stolen/corrupted: state=%q current_lease_id=%v want leaseB=%s", duState, duCurrentLease, leaseBID)
	}
	if err := env.Pool.QueryRow(context.Background(), `SELECT release_reason FROM leases WHERE id = $1`, leaseAID).Scan(&leaseAReason); err != nil {
		t.Fatalf("query leaseA release_reason: %v", err)
	}
	if leaseAReason == nil || *leaseAReason != "expired" {
		t.Fatalf("leaseA release_reason = %v, want expired", leaseAReason)
	}
	if err := env.Pool.QueryRow(context.Background(), `SELECT leased_count FROM orchestration_runs WHERE id = $1`,
		runUUID(t, env, identity(nil))).Scan(&leasedCount); err != nil {
		t.Fatalf("query leased_count: %v", err)
	}
	if leasedCount != 1 {
		t.Fatalf("leased_count = %d, want 1", leasedCount)
	}

	// Worker B now completes on-time as passed -> transitions unit to completed_pass.
	completeB := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-active-B", passResults([]string{"tests/reclaimed.spec.ts"})))
	resB := expectStatusValidated(t, doc, opts, http.MethodPost, "/api/v1/orchestration/complete", completeB, http.StatusOK)
	if resB["late_report"].(bool) {
		t.Fatalf("worker B should be on-time, got late_report=true")
	}
	if supB := stringSlice(t, resB["superseded_unit_ids"]); len(supB) != 0 {
		t.Fatalf("worker B superseded_unit_ids = %v, want []", supB)
	}
	changesB := resB["unit_states_changed"].([]any)
	if len(changesB) != 1 || changesB[0].(map[string]any)["new_state"] != "completed_pass" {
		t.Fatalf("worker B unit_states_changed = %v, want completed_pass", changesB)
	}
}

// TestLateReportSupersededWhileReclaimedLeaseActive_Race uses barrier-synchronized
// goroutines under -race to race late worker A against active worker B (and
// against concurrent checkout/lazy-expire). Fails without the leases-first FOR UPDATE
// lock and current_lease_id guard in finalizeUnitStateTx.
func TestLateReportSupersededWhileReclaimedLeaseActive_Race(t *testing.T) {
	t.Run("late_A_vs_ontime_B", func(t *testing.T) {
		env, tok := startEnv(t, testenv.WithReaperDisabled())

		const iterations = 4
		for i := range iterations {
			runID := fmt.Sprintf("race-late-vs-active-%d", i)
			id := identity(map[string]any{"gh_run_id": runID})
			beginBody := merge(id, map[string]any{
				"playwright_project":     defaultPlaywrightPrj,
				"total_reports_expected": 1,
				"lease_timeout_ms":       300,
				"idle_timeout_ms":        60_000,
				"dispatch_units":         []map[string]any{{"spec_path": "tests/race.spec.ts"}},
			})
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody), http.StatusCreated)

			jobA := fmt.Sprintf("job-race-A-%d", i)
			jobB := fmt.Sprintf("job-race-B-%d", i)

			checkA := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
				merge(id, map[string]any{"gh_job_name": "shard-A", "gh_job_id": jobA, "batch_size": 1})), http.StatusOK)
			unitID := unitIDOf(t, checkA["units"].([]any)[0].(map[string]any))

			time.Sleep(450 * time.Millisecond)

			checkB := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
				merge(id, map[string]any{"gh_job_name": "shard-B", "gh_job_id": jobB, "batch_size": 1})), http.StatusOK)
			if len(checkB["units"].([]any)) != 1 {
				t.Fatalf("iter %d: worker B got %v units, want 1", i, checkB["units"])
			}

			// Race Worker A (reporting failed) and Worker B (reporting passed) at a barrier.
			start := make(chan struct{})
			var wg sync.WaitGroup
			var respA, respB map[string]any
			var errA, errB error

			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				resp, body, err := doCompleteHTTP(env.ServerURL, tok, merge(id, map[string]any{
					"gh_job_name": "shard-A",
					"gh_job_id":   jobA,
					"results":     failResults([]string{"tests/race.spec.ts"}),
				}))
				if err != nil {
					errA = err
					return
				}
				if resp.StatusCode != http.StatusOK {
					errA = fmt.Errorf("A status=%d body=%s", resp.StatusCode, body)
					return
				}
				errA = decodeMapBytes(body, &respA)
			}()
			go func() {
				defer wg.Done()
				<-start
				resp, body, err := doCompleteHTTP(env.ServerURL, tok, merge(id, map[string]any{
					"gh_job_name": "shard-B",
					"gh_job_id":   jobB,
					"results":     passResults([]string{"tests/race.spec.ts"}),
				}))
				if err != nil {
					errB = err
					return
				}
				if resp.StatusCode != http.StatusOK {
					errB = fmt.Errorf("B status=%d body=%s", resp.StatusCode, body)
					return
				}
				errB = decodeMapBytes(body, &respB)
			}()
			close(start)
			wg.Wait()

			if errA != nil || errB != nil {
				t.Fatalf("iter %d: concurrent completes failed: errA=%v errB=%v", i, errA, errB)
			}
			if !respA["late_report"].(bool) {
				t.Fatalf("iter %d: A late_report=false, want true", i)
			}
			if len(respA["unit_states_changed"].([]any)) != 0 {
				t.Fatalf("iter %d: A stole unit transition: %+v", i, respA)
			}
			if supA := stringSlice(t, respA["superseded_unit_ids"]); len(supA) != 1 || supA[0] != unitID {
				t.Fatalf("iter %d: A superseded_unit_ids = %v, want [%s]", i, supA, unitID)
			}
			if respB["late_report"].(bool) {
				t.Fatalf("iter %d: B late_report=true, want false", i)
			}
			if len(respB["unit_states_changed"].([]any)) != 1 ||
				respB["unit_states_changed"].([]any)[0].(map[string]any)["new_state"] != "completed_pass" {
				t.Fatalf("iter %d: B unit_states_changed = %+v, want completed_pass", i, respB)
			}
			if supB := stringSlice(t, respB["superseded_unit_ids"]); len(supB) != 0 {
				t.Fatalf("iter %d: B superseded_unit_ids = %v, want []", i, supB)
			}

			var duState string
			if err := env.Pool.QueryRow(context.Background(), `SELECT state FROM dispatch_units WHERE id = $1`, unitID).Scan(&duState); err != nil {
				t.Fatalf("query du state: %v", err)
			}
			if duState != "completed_pass" {
				t.Fatalf("iter %d: final dispatch_units.state = %q, want completed_pass", i, duState)
			}
		}
	})

	t.Run("complete_vs_reclaim_checkout", func(t *testing.T) {
		env, tok := startEnv(t, testenv.WithReaperDisabled())

		const iterations = 4
		for i := range iterations {
			runID := fmt.Sprintf("race-complete-vs-reclaim-%d", i)
			id := identity(map[string]any{"gh_run_id": runID})
			beginBody := merge(id, map[string]any{
				"playwright_project":     defaultPlaywrightPrj,
				"total_reports_expected": 1,
				"lease_timeout_ms":       250,
				"idle_timeout_ms":        60_000,
				"dispatch_units":         []map[string]any{{"spec_path": "tests/reap-race.spec.ts"}},
			})
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody), http.StatusCreated)

			jobA := fmt.Sprintf("job-reap-A-%d", i)
			jobB := fmt.Sprintf("job-reap-B-%d", i)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
				merge(id, map[string]any{"gh_job_name": "shard-A", "gh_job_id": jobA, "batch_size": 1})), http.StatusOK)

			time.Sleep(350 * time.Millisecond)

			start := make(chan struct{})
			var wg sync.WaitGroup
			var completeBodyMap, checkoutBMap map[string]any
			var errComplete, errCheckout error

			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				resp, body, err := doCompleteHTTP(env.ServerURL, tok, merge(id, map[string]any{
					"gh_job_name": "shard-A",
					"gh_job_id":   jobA,
					"results":     passResults([]string{"tests/reap-race.spec.ts"}),
				}))
				if err != nil {
					errComplete = err
					return
				}
				if resp.StatusCode != http.StatusOK {
					errComplete = fmt.Errorf("A complete status=%d body=%s", resp.StatusCode, body)
					return
				}
				errComplete = decodeMapBytes(body, &completeBodyMap)
			}()
			go func() {
				defer wg.Done()
				<-start
				resp, body, err := doPostHTTP(env.ServerURL+"/api/v1/orchestration/checkout", tok, merge(id, map[string]any{
					"gh_job_name": "shard-B",
					"gh_job_id":   jobB,
					"batch_size":  1,
				}))
				if err != nil {
					errCheckout = err
					return
				}
				// 409 RUN_NOT_IN_PROGRESS is valid if A completed before B checked out.
				if resp.StatusCode == http.StatusConflict {
					return
				}
				if resp.StatusCode != http.StatusOK {
					errCheckout = fmt.Errorf("B checkout status=%d body=%s", resp.StatusCode, body)
					return
				}
				errCheckout = decodeMapBytes(body, &checkoutBMap)
			}()
			close(start)
			wg.Wait()

			if errComplete != nil || errCheckout != nil {
				t.Fatalf("iter %d: race failed: errComplete=%v errCheckout=%v", i, errComplete, errCheckout)
			}

			// If B acquired the reclaimed unit, A must have seen superseded_unit_ids and B must be able to finish it.
			if checkoutBMap != nil && len(checkoutBMap["units"].([]any)) == 1 {
				if len(completeBodyMap["unit_states_changed"].([]any)) != 0 {
					t.Fatalf("iter %d: both B reclaimed and A finalized unit", i)
				}
				if sup := stringSlice(t, completeBodyMap["superseded_unit_ids"]); len(sup) != 1 {
					t.Fatalf("iter %d: expected A superseded_unit_ids len 1, got %v", i, sup)
				}
				expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
					merge(id, map[string]any{
						"gh_job_name": "shard-B",
						"gh_job_id":   jobB,
						"results":     passResults([]string{"tests/reap-race.spec.ts"}),
					})), http.StatusOK)
			}

			// In all outcomes, run must end cleanly with completed_pass=1, leased=0, pending=0.
			stat := expectStatus(t, getJSON(t, env, tok, statusURL(id)), http.StatusOK)
			c := counts(t, stat)
			if c["completed_pass"] != 1 || c["leased"] != 0 || c["pending"] != 0 {
				t.Fatalf("iter %d: final counts=%s, want pass=1 leased=0 pending=0", i, fmtCounts(c))
			}
		}
	})
}

// TestMigration000030BackfillsLeaseAndAttemptKinds rolls back to 000029, inserts
// pre-existing rows covering fresh, reclaim, and retest histories (including a
// mixed-batch lease), runs migration 000030 up, and asserts backfilled kinds.
func TestMigration000030BackfillsLeaseAndAttemptKinds(t *testing.T) {
	env, _ := startEnv(t, testenv.WithReaperDisabled())
	ctx := context.Background()

	connStr := migrateURL(env.Pool.Config().ConnString())
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, connStr)
	if err != nil {
		t.Fatalf("new migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate down 1 step (000030 -> 000029): %v", err)
	}

	runID := uuid.Must(uuid.NewV7())
	unit0ID := uuid.Must(uuid.NewV7())
	unit1ID := uuid.Must(uuid.NewV7())
	lease1ID := uuid.Must(uuid.NewV7())
	lease2ID := uuid.Must(uuid.NewV7())
	lease3ID := uuid.Must(uuid.NewV7())

	t0 := time.Date(2026, time.May, 1, 10, 0, 0, 0, time.UTC)

	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO orchestration_runs (
			id, repository, commit_sha, gh_run_id, name, gh_run_attempt,
			framework, branch, lease_timeout_ms, idle_timeout_ms,
			retest_on_fail, retest_budget, status, terminal_at,
			pending_count, leased_count, completed_pass_count, total_units,
			dispatch_units_hash, started_at, last_activity_at, owner_oidc_subject
		) VALUES (
			$1, 'mattermost/backfill-test', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '999', 'pw', '1',
			'playwright', 'main', 60000, 600000,
			true, 1, 'completed', $2,
			0, 0, 2, 2,
			'\x01'::bytea, $2, $2, 'repo:mattermost/backfill-test:ref:refs/heads/main'
		)
	`, runID, t0); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO dispatch_units (id, run_id, dispatch_seq, spec_path, state, lease_count, fail_count, outcome_set_at)
		VALUES ($1, $3, 0, 'tests/u0.spec.ts', 'completed_pass', 3, 1, $4),
		       ($2, $3, 1, 'tests/u1.spec.ts', 'completed_pass', 1, 0, $4)
	`, unit0ID, unit1ID, runID, t0); err != nil {
		t.Fatalf("insert dispatch_units: %v", err)
	}

	// Lease 1: expired on unit0. Lease 2: mixed batch (reclaim on unit0 + fresh on unit1), completed with unit0 failed.
	// Lease 3: retest on unit0, completed pass.
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO leases (id, run_id, gh_job_name, gh_job_id, unit_ids, issued_at, deadline, released_at, release_reason)
		VALUES ($1, $4, 'w1', 'j1', ARRAY[$5::uuid], $7, $8, $8, 'expired'),
		       ($2, $4, 'w2', 'j2', ARRAY[$5::uuid, $6::uuid], $9, $10, $10, 'completed'),
		       ($3, $4, 'w3', 'j3', ARRAY[$5::uuid], $11, $12, $12, 'completed')
	`, lease1ID, lease2ID, lease3ID, runID, unit0ID, unit1ID,
		t0, t0.Add(time.Minute),
		t0.Add(2*time.Minute), t0.Add(3*time.Minute),
		t0.Add(4*time.Minute), t0.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert leases: %v", err)
	}

	att1ID := uuid.Must(uuid.NewV7())
	att2ID := uuid.Must(uuid.NewV7())
	att1bID := uuid.Must(uuid.NewV7())
	att3ID := uuid.Must(uuid.NewV7())
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO attempts (id, lease_id, dispatch_unit_id, run_id, spec_path, status, reported_at, expired, created_at)
		VALUES ($1, $5, $8, $10, 'tests/u0.spec.ts', NULL, NULL, TRUE, $11),
		       ($2, $6, $8, $10, 'tests/u0.spec.ts', 'failed', $12, FALSE, $12),
		       ($3, $6, $9, $10, 'tests/u1.spec.ts', 'passed', $12, FALSE, $12),
		       ($4, $7, $8, $10, 'tests/u0.spec.ts', 'passed', $13, FALSE, $13)
	`, att1ID, att2ID, att1bID, att3ID, lease1ID, lease2ID, lease3ID, unit0ID, unit1ID, runID,
		t0, t0.Add(2*time.Minute), t0.Add(4*time.Minute)); err != nil {
		t.Fatalf("insert attempts: %v", err)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up (apply 000030): %v", err)
	}

	wantAttempts := map[uuid.UUID]string{
		att1ID:  "fresh",
		att2ID:  "reclaim",
		att1bID: "fresh",
		att3ID:  "retest",
	}
	for id, want := range wantAttempts {
		var got string
		if err := env.Pool.QueryRow(ctx, `SELECT kind FROM attempts WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("query attempt %s kind: %v", id, err)
		}
		if got != want {
			t.Fatalf("attempt %s backfilled kind = %q, want %q", id, got, want)
		}
	}

	wantLeases := map[uuid.UUID]string{
		lease1ID: "fresh",
		lease2ID: "reclaim",
		lease3ID: "retest",
	}
	for id, want := range wantLeases {
		var got string
		if err := env.Pool.QueryRow(ctx, `SELECT kind FROM leases WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("query lease %s kind: %v", id, err)
		}
		if got != want {
			t.Fatalf("lease %s backfilled kind = %q, want %q", id, got, want)
		}
	}
}

func stringSlice(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T (%v)", v, v)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("expected string element, got %T (%v)", item, item)
		}
		out = append(out, s)
	}
	return out
}

func doCompleteHTTP(baseURL, token string, body any) (*http.Response, []byte, error) {
	return doPostHTTP(baseURL+"/api/v1/orchestration/complete", token, body)
}

func doPostHTTP(fullURL, token string, body any) (*http.Response, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, fullURL, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, respBytes, nil
}

func decodeMapBytes(b []byte, dst *map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(dst)
}

func migrateURL(pgURL string) string {
	if strings.HasPrefix(pgURL, "postgres://") {
		return "pgx5://" + strings.TrimPrefix(pgURL, "postgres://")
	}
	if strings.HasPrefix(pgURL, "postgresql://") {
		return "pgx5://" + strings.TrimPrefix(pgURL, "postgresql://")
	}
	return pgURL
}

func loadOrchOpenAPISpec(t *testing.T) (*openapi3.T, *openapi3filter.Options) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
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

func expectStatusValidated(
	t *testing.T, doc *openapi3.T, opts *openapi3filter.Options,
	method, specPath string, resp *http.Response, wantStatus int,
) map[string]any {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, wantStatus, string(body))
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	var serverURL string
	if len(doc.Servers) > 0 {
		serverURL = doc.Servers[0].URL
	}
	req, _ := http.NewRequest(method, serverURL+specPath, nil)
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find route %s %s: %v", method, specPath, err)
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: params,
			Route:      route,
			Options:    opts,
		},
		Status:  resp.StatusCode,
		Header:  resp.Header,
		Options: opts,
	}
	respIn.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Fatalf("openapi response validation failed for %s %s: %v\nbody=%s", method, specPath, err, string(body))
	}
	var out map[string]any
	if err := decodeMapBytes(body, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

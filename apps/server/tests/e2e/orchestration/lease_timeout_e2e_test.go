//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestLeaseTimeoutReclaim drives the lazy-on-checkout lease expiration path:
// worker A holds a lease past its deadline; worker B subsequently checks out;
// the inline expiration sweep in AtomicCheckout reclaims the unit and hands it
// back at its original dispatch_seq.
func TestLeaseTimeoutReclaim(t *testing.T) {
	env, tok := startEnv(t)

	// Short lease deadline so the test stays sub-5s. Note: the schema floors
	// the deadline at lease_timeout_ms, so even with the reaper untouched the
	// inline sweep on the next checkout will reclaim.
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{
			"tests/single-a.spec.ts",
			"tests/single-b.spec.ts",
		}, map[string]any{
			"lease_timeout_ms": 1000, // 1s
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- Worker A leases the first unit and never reports. ---
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-aaaaa", 1))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	unitsA := bodyA["units"].([]any)
	if len(unitsA) != 1 {
		t.Fatalf("worker A got %d units, want 1", len(unitsA))
	}
	originalUnitID := unitIDOf(t, unitsA[0].(map[string]any))
	originalSeq := dispatchSeqOf(t, unitsA[0].(map[string]any))

	// Wait past the 1s deadline. The inline sweep in checkout B will reclaim.
	time.Sleep(1500 * time.Millisecond)

	// --- Worker B checks out. The lazy-expire pass runs first; B inherits
	//     the original unit at the same dispatch_seq.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-bbbbb", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	unitsB := bodyB["units"].([]any)
	if len(unitsB) != 1 {
		t.Fatalf("worker B got %d units, want 1", len(unitsB))
	}
	reclaimed := unitsB[0].(map[string]any)
	if dispatchSeqOf(t, reclaimed) != originalSeq {
		t.Fatalf("reclaimed dispatch_seq = %d, want original %d", dispatchSeqOf(t, reclaimed), originalSeq)
	}
	if unitIDOf(t, reclaimed) != originalUnitID {
		t.Fatalf("reclaimed unit_id = %s, want original %s", unitIDOf(t, reclaimed), originalUnitID)
	}

	// --- DB invariants: A's lease released expired, B's lease active, unit
	//     lease_count==2, current_lease_id points at B's lease. ---
	ctx := context.Background()
	runID := runUUID(t, env, identity(nil))

	type leaseRow struct {
		id            uuid.UUID
		ghJobID       string
		releasedAt    *time.Time
		releaseReason *string
	}
	rows, err := env.Pool.Query(ctx, `
		SELECT id, gh_job_id, released_at, release_reason
		  FROM leases WHERE run_id = $1 ORDER BY issued_at
	`, runID)
	if err != nil {
		t.Fatalf("query leases: %v", err)
	}
	var leases []leaseRow
	for rows.Next() {
		var lr leaseRow
		if err := rows.Scan(&lr.id, &lr.ghJobID, &lr.releasedAt, &lr.releaseReason); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		leases = append(leases, lr)
	}
	rows.Close()
	if len(leases) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(leases))
	}

	var aLease, bLease leaseRow
	for _, lr := range leases {
		switch lr.ghJobID {
		case "job-aaaaa":
			aLease = lr
		case "job-bbbbb":
			bLease = lr
		}
	}
	if aLease.releasedAt == nil || aLease.releaseReason == nil || *aLease.releaseReason != "expired" {
		t.Fatalf("worker A lease should be released-expired, got reason=%v", aLease.releaseReason)
	}
	if bLease.releasedAt != nil {
		t.Fatalf("worker B lease should still be active, got released_at=%v", *bLease.releasedAt)
	}

	// Unit row: lease_count==2, current_lease_id==B's lease, state=='leased'.
	var (
		leaseCount     int
		currentLeaseID *uuid.UUID
		state          string
	)
	err = env.Pool.QueryRow(ctx, `
		SELECT lease_count, current_lease_id, state FROM dispatch_units WHERE id = $1
	`, originalUnitID).Scan(&leaseCount, &currentLeaseID, &state)
	if err != nil {
		t.Fatalf("query dispatch_units: %v", err)
	}
	if leaseCount != 2 {
		t.Fatalf("lease_count = %d, want 2", leaseCount)
	}
	if currentLeaseID == nil || *currentLeaseID != bLease.id {
		t.Fatalf("current_lease_id = %v, want %v", currentLeaseID, bLease.id)
	}
	if state != "leased" {
		t.Fatalf("unit state = %q, want leased", state)
	}
}

//go:build e2e
// +build e2e

// Package admincli exercises the tsioctl keys lifecycle against a real
// Postgres via testcontainers. It invokes the public apikey APIs —
// equivalent to the cobra subcommands — and asserts the DB side-effects.

package admincli

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestKeysLifecycle(t *testing.T) {
	env := testenv.Start(t)
	repo := &apikey.Repo{Pool: env.Pool}
	ctx := context.Background()

	// issue
	iss, err := apikey.Issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	row, err := repo.Insert(ctx, "ci-primary", iss)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if row.Status != apikey.StatusActive {
		t.Errorf("status = %q, want active", row.Status)
	}

	// list active
	active := apikey.StatusActive
	rows, err := repo.List(ctx, &active)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("list active count = %d, want 1", len(rows))
	}

	// rotate: mark rotating, issue new
	if err := repo.MarkRotating(ctx, row.ID); err != nil {
		t.Fatalf("mark rotating: %v", err)
	}
	iss2, err := apikey.Issue()
	if err != nil {
		t.Fatalf("issue2: %v", err)
	}
	if _, err := repo.Insert(ctx, "ci-primary-rotated", iss2); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	allRows, err := repo.List(ctx, nil)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(allRows) != 2 {
		t.Errorf("list all count = %d, want 2", len(allRows))
	}
	var oldRow apikey.Row
	for _, r := range allRows {
		if r.ID == row.ID {
			oldRow = r
			break
		}
	}
	if oldRow.Status != apikey.StatusRotating {
		t.Errorf("old key status = %q, want rotating", oldRow.Status)
	}

	// revoke old
	if err := repo.Revoke(ctx, row.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked := apikey.StatusRevoked
	rows, err = repo.List(ctx, &revoked)
	if err != nil {
		t.Fatalf("list revoked: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Errorf("list revoked = %+v, want [%s]", rows, row.ID)
	}
	if rows[0].RevokedAt == nil {
		t.Error("revoked_at should be set")
	}

	// plaintext of the revoked key must no longer verify on a lookup chain:
	// verify directly (the middleware itself additionally checks status).
	fetched, err := repo.ByPrefix(ctx, iss.Prefix)
	if err != nil {
		t.Fatalf("by prefix: %v", err)
	}
	if fetched.Status != apikey.StatusRevoked {
		t.Errorf("fetched status = %q, want revoked", fetched.Status)
	}
	if !apikey.Verify(iss.PlainText, fetched.KeyHash) {
		t.Error("hash should still match plaintext; revocation is status-based, not hash-invalidation")
	}

	// freshly-issued rotating key verifies normally.
	fetched2, err := repo.ByPrefix(ctx, iss2.Prefix)
	if err != nil {
		t.Fatalf("by prefix 2: %v", err)
	}
	if !apikey.Verify(iss2.PlainText, fetched2.KeyHash) {
		t.Error("new key hash failed to verify")
	}
}

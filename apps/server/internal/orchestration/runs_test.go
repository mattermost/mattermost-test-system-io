package orchestration

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// validIdentity returns a CompositeIdentity with all required fields set.
// Used to seed table-driven tests that mutate single fields to assert
// per-field validation errors.
func validIdentity() CompositeIdentity {
	return CompositeIdentity{
		Repository:   "mattermost/mattermost",
		CommitSHA:    "deadbeefcafebabe1234567890abcdefdeadbeef",
		GHRunID:      "1234567890",
		Name:         "playwright-shard-1",
		GHRunAttempt: "1",
		Framework:    FrameworkPlaywright,
	}
}

func TestHashUnits_DeterministicForSameInput(t *testing.T) {
	t.Parallel()

	ci := validIdentity()
	specPaths := []string{"tests/a.spec.ts", "tests/b.spec.ts", "tests/c.spec.ts"}

	h1 := ci.HashUnits(specPaths)
	h2 := ci.HashUnits(specPaths)

	if !bytes.Equal(h1, h2) {
		t.Fatalf("HashUnits not deterministic: %x vs %x", h1, h2)
	}
	if len(h1) != 32 {
		t.Fatalf("expected 32-byte sha256 digest, got %d", len(h1))
	}
}

// TestHashUnits_DifferentForReorderedUnits asserts that swapping the order
// of dispatch units produces a different hash. Units are FIFO-ordered
// (dispatch_seq is the unit's position in the input slice), so reordering
// changes how workers pick up work — it MUST be a different identity for
// idempotency.
func TestHashUnits_DifferentForReorderedUnits(t *testing.T) {
	t.Parallel()

	ci := validIdentity()

	a := []string{"a", "b"}
	b := []string{"b", "a"}

	if bytes.Equal(ci.HashUnits(a), ci.HashUnits(b)) {
		t.Fatalf("expected reordered units to hash differently (units are FIFO-ordered)")
	}
}

func TestHashUnits_DifferentForDifferentSpecPaths(t *testing.T) {
	t.Parallel()

	ci := validIdentity()

	a := []string{"a"}
	b := []string{"b"}

	if bytes.Equal(ci.HashUnits(a), ci.HashUnits(b)) {
		t.Fatalf("expected different spec paths to hash differently")
	}
}

func TestCompositeIdentity_Validate(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		if err := validIdentity().Validate(); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("framework must be playwright", func(t *testing.T) {
		t.Parallel()
		ci := validIdentity()
		ci.Framework = "jest"
		if err := ci.Validate(); err == nil {
			t.Fatal("expected error for non-playwright framework")
		}
	})

	t.Run("framework empty is rejected", func(t *testing.T) {
		t.Parallel()
		ci := validIdentity()
		ci.Framework = ""
		if err := ci.Validate(); err == nil {
			t.Fatal("expected error for empty framework")
		}
	})

	t.Run("required fields", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name   string
			mutate func(*CompositeIdentity)
		}{
			{"repository", func(c *CompositeIdentity) { c.Repository = "" }},
			{"commit_sha", func(c *CompositeIdentity) { c.CommitSHA = "" }},
			{"gh_run_id", func(c *CompositeIdentity) { c.GHRunID = "" }},
			{"name", func(c *CompositeIdentity) { c.Name = "" }},
			{"gh_run_attempt", func(c *CompositeIdentity) { c.GHRunAttempt = "" }},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				ci := validIdentity()
				tc.mutate(&ci)
				if err := ci.Validate(); err == nil {
					t.Fatalf("expected error when %s is empty", tc.name)
				}
			})
		}
	})
}

// Compile-time assertions that the public types referenced by the tests
// exist with the expected shapes; keeps these tests honest if a type is
// renamed or moved.
var (
	_ = ErrConflict
	_ = ErrPartialReport
	_ = ErrUnknownLease
	_ = ErrWorkerHasActiveLease
	_ = ErrRunNotInProgress
	_ = ErrNotFound
	_ uuid.UUID
)

// Sanity: errors.Is reflexivity for sentinels — protects against accidental
// non-pointer redeclaration.
func TestSentinelErrorsAreSelfIdentical(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrPartialReport, ErrPartialReport) {
		t.Fatal("ErrPartialReport not identical to itself")
	}
}

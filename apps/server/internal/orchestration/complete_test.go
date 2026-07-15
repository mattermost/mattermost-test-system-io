package orchestration

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnitOutcomeFromSpecStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{
			name:     "all passed",
			statuses: []string{AttemptStatusPassed, AttemptStatusPassed},
			want:     UnitStateCompletedPass,
		},
		{
			name:     "any failed",
			statuses: []string{AttemptStatusPassed, AttemptStatusFailed},
			want:     UnitStateCompletedFail,
		},
		{
			name:     "any timedOut",
			statuses: []string{AttemptStatusPassed, AttemptStatusTimedOut},
			want:     UnitStateCompletedFail,
		},
		{
			name:     "any interrupted",
			statuses: []string{AttemptStatusPassed, AttemptStatusInterrupted},
			want:     UnitStateCompletedFail,
		},
		{
			name:     "all skipped",
			statuses: []string{AttemptStatusSkipped, AttemptStatusSkipped},
			want:     UnitStateCompletedSkipped,
		},
		{
			name:     "mix of passed and flaky",
			statuses: []string{AttemptStatusPassed, AttemptStatusFlaky},
			want:     UnitStateCompletedPass,
		},
		{
			name:     "mix of skipped and passed",
			statuses: []string{AttemptStatusSkipped, AttemptStatusPassed},
			want:     UnitStateCompletedPass,
		},
		{
			name:     "single failed",
			statuses: []string{AttemptStatusFailed},
			want:     UnitStateCompletedFail,
		},
		{
			name:     "fail dominates flaky",
			statuses: []string{AttemptStatusFlaky, AttemptStatusFailed},
			want:     UnitStateCompletedFail,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapStatusesToUnitState(tc.statuses)
			if got != tc.want {
				t.Fatalf("mapStatusesToUnitState(%v) = %q, want %q", tc.statuses, got, tc.want)
			}
		})
	}
}

func TestValidateResultsCoverAllSpecs(t *testing.T) {
	t.Parallel()

	unitID := uuid.Must(uuid.NewV7())
	leaseSpecs := map[string]uuid.UUID{
		"tests/a.spec.ts": unitID,
		"tests/b.spec.ts": unitID,
	}

	t.Run("coverage match returns nil", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
			{SpecPath: "tests/b.spec.ts", Status: AttemptStatusFailed},
		}
		if err := validateResultsCoverLease(results, leaseSpecs); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("missing spec is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})

	t.Run("extra spec not in lease is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
			{SpecPath: "tests/b.spec.ts", Status: AttemptStatusPassed},
			{SpecPath: "tests/c.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})

	t.Run("duplicate spec_path is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})

	t.Run("empty spec_path is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "", Status: AttemptStatusPassed},
			{SpecPath: "tests/b.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})

	t.Run("invalid status is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: "bogus"},
			{SpecPath: "tests/b.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})

	t.Run("wrong-spec swap is ErrPartialReport", func(t *testing.T) {
		t.Parallel()
		// Same cardinality but one entry is not in the lease.
		results := []SpecResult{
			{SpecPath: "tests/a.spec.ts", Status: AttemptStatusPassed},
			{SpecPath: "tests/c.spec.ts", Status: AttemptStatusPassed},
		}
		err := validateResultsCoverLease(results, leaseSpecs)
		if !errors.Is(err, ErrPartialReport) {
			t.Fatalf("expected ErrPartialReport, got %v", err)
		}
	})
}

func TestLateReportFlag(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, time.April, 25, 12, 0, 0, 0, time.UTC)

	t.Run("reported before deadline is on-time", func(t *testing.T) {
		t.Parallel()
		now := deadline.Add(-1 * time.Second)
		if isLateReport(now, deadline) {
			t.Fatalf("expected on-time report")
		}
	})

	t.Run("reported at deadline is on-time", func(t *testing.T) {
		t.Parallel()
		// time.Time.After is strictly greater-than, so a report exactly at
		// the deadline is on-time.
		if isLateReport(deadline, deadline) {
			t.Fatalf("expected at-deadline report to be on-time")
		}
	})

	t.Run("reported after deadline is late", func(t *testing.T) {
		t.Parallel()
		now := deadline.Add(1 * time.Second)
		if !isLateReport(now, deadline) {
			t.Fatalf("expected late report")
		}
	})
}

func TestValidAttemptStatus(t *testing.T) {
	t.Parallel()

	good := []string{
		AttemptStatusPassed, AttemptStatusFailed, AttemptStatusSkipped,
		AttemptStatusFlaky, AttemptStatusTimedOut, AttemptStatusInterrupted,
	}
	for _, s := range good {
		if !validAttemptStatus(s) {
			t.Fatalf("validAttemptStatus(%q) = false, want true", s)
		}
	}

	bad := []string{"", "PASSED", "fail", "ok", "unknown"}
	for _, s := range bad {
		if validAttemptStatus(s) {
			t.Fatalf("validAttemptStatus(%q) = true, want false", s)
		}
	}
}

func TestUnitStateCounterColumn(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		UnitStateCompletedPass:    "completed_pass_count",
		UnitStateCompletedFail:    "completed_fail_count",
		UnitStateCompletedSkipped: "completed_skipped_count",
		UnitStateAbandoned:        "abandoned_count",
	}
	for state, want := range cases {
		got := unitStateCounterColumn(state)
		if got != want {
			t.Fatalf("unitStateCounterColumn(%q) = %q, want %q", state, got, want)
		}
	}

	// Non-terminal / unknown states return "" so callers must guard.
	for _, state := range []string{UnitStatePending, UnitStateLeased, "", "bogus"} {
		if got := unitStateCounterColumn(state); got != "" {
			t.Fatalf("unitStateCounterColumn(%q) = %q, want empty string", state, got)
		}
	}
}

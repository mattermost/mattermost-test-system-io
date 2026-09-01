package triage

import (
	"testing"
	"time"
)

// W15c gate: clock tests per category, the two carve-outs (no clock for
// advisory-period blame is action-side; corrected/promoted close here), and
// the 1x/2x breach flags.

func TestSlaClockCategories(t *testing.T) {
	cases := []struct {
		name       string
		verdict    string
		attributed bool
		age        time.Duration
		wantState  string
		wantDays   int
	}{
		{"MAIN_REGRESSION attributed within 2d", "MAIN_REGRESSION", true, 1 * 24 * time.Hour, "open", 2},
		{"MAIN_REGRESSION attributed past 1x", "MAIN_REGRESSION", true, 3 * 24 * time.Hour, "flag1", 2},
		{"MAIN_REGRESSION attributed past 2x", "MAIN_REGRESSION", true, 5 * 24 * time.Hour, "flag2", 2},
		{"MAIN_REGRESSION unattributed gets 5d", "MAIN_REGRESSION", false, 3 * 24 * time.Hour, "open", 5},
		{"MAIN_REGRESSION unattributed past 1x", "MAIN_REGRESSION", false, 6 * 24 * time.Hour, "flag1", 5},
		{"FLAKY_INFRA 2d", "FLAKY_INFRA", false, 3 * 24 * time.Hour, "flag1", 2},
		{"FLAKY_TEST expired 5d", "FLAKY_TEST", false, 6 * 24 * time.Hour, "flag1", 5},
		{"PR_REGRESSION no clock", "PR_REGRESSION", false, 30 * 24 * time.Hour, "none", 0},
		{"TEST_DEBT no clock", "TEST_DEBT", false, 30 * 24 * time.Hour, "none", 0},
		{"INCONCLUSIVE no clock", "INCONCLUSIVE", false, 30 * 24 * time.Hour, "none", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, limit := SlaClock(tc.verdict, tc.attributed, tc.age, false, false)
			if state != tc.wantState {
				t.Fatalf("state = %q, want %q", state, tc.wantState)
			}
			if days := int(limit.Hours() / 24); days != tc.wantDays {
				t.Fatalf("limit = %dd, want %dd", days, tc.wantDays)
			}
		})
	}
}

func TestSlaClockClosesOnAction(t *testing.T) {
	if state, _ := SlaClock("MAIN_REGRESSION", true, 9*24*time.Hour, true, false); state != "closed" {
		t.Fatalf("corrected verdict clock = %q, want closed", state)
	}
	if state, _ := SlaClock("MAIN_REGRESSION", true, 9*24*time.Hour, false, true); state != "closed" {
		t.Fatalf("queue-promoted verdict clock = %q, want closed", state)
	}
}

func TestSortSlaRowsWorstFirst(t *testing.T) {
	rows := []slaRow{
		{State: "open", AgeDays: 9},
		{State: "flag1", AgeDays: 4},
		{State: "flag2", AgeDays: 6},
		{State: "flag1", AgeDays: 7},
	}
	sortSlaRows(rows)
	if rows[0].State != "flag2" {
		t.Fatalf("first = %s, want flag2", rows[0].State)
	}
	if rows[1].AgeDays != 7 || rows[2].AgeDays != 4 {
		t.Fatalf("flag1 order wrong: %d then %d, want 7 then 4", rows[1].AgeDays, rows[2].AgeDays)
	}
	if rows[3].State != "open" {
		t.Fatalf("last = %s, want open", rows[3].State)
	}
}

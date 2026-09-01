package triage

import (
	"testing"
	"time"
)

func day(ts string) time.Time { t, _ := time.Parse("2006-01-02", ts); return t }

func rates(pairs ...DayRate) []DayRate { return pairs }

func pct(outcomes, failures int) float64 {
	return (float64(outcomes-failures) / float64(outcomes)) * 100
}

// W7 rule gates: each rule fires on its condition and only its condition.
func TestEvaluateMasterAlertsRules(t *testing.T) {
	base := rates(
		DayRate{Day: day("2026-08-25"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-26"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-27"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-28"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-29"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-30"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
		DayRate{Day: day("2026-08-31"), Outcomes: 100, Failures: 5, Rate: pct(100, 5)},
	)

	t.Run("steady state fires nothing", func(t *testing.T) {
		alerts := EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: base})
		if len(alerts) != 0 {
			t.Fatalf("steady state fired %d alerts: %+v", len(alerts), alerts)
		}
	})

	t.Run("24h crash fires drop_24h (>=10 points below 7d median)", func(t *testing.T) {
		crashed := append(append([]DayRate{}, base[:6]...),
			DayRate{Day: day("2026-08-31"), Outcomes: 100, Failures: 40, Rate: pct(100, 40)})
		alerts := EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: crashed})
		if !hasRule(alerts, AlertRuleDrop24h) {
			t.Fatalf("drop_24h did not fire: %+v", alerts)
		}
	})

	t.Run("9-point drop is below threshold (no fire)", func(t *testing.T) {
		crashed := append(append([]DayRate{}, base[:6]...),
			DayRate{Day: day("2026-08-31"), Outcomes: 100, Failures: 14, Rate: pct(100, 14)}) // 86 vs 95 = 9 points
		alerts := EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: crashed})
		if hasRule(alerts, AlertRuleDrop24h) {
			t.Fatal("drop_24h fired on a 9-point drop (threshold is 10)")
		}
	})

	t.Run("floor disabled when unset", func(t *testing.T) {
		terrible := append(append([]DayRate{}, base[:6]...),
			DayRate{Day: day("2026-08-31"), Outcomes: 100, Failures: 99, Rate: pct(100, 99)})
		alerts := EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: terrible, Floor: 0})
		if hasRule(alerts, AlertRuleFloor) {
			t.Fatal("floor rule fired with floor unset — must ship disabled")
		}
		alerts = EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: terrible, Floor: 80})
		if !hasRule(alerts, AlertRuleFloor) {
			t.Fatal("floor rule did not fire below a set floor")
		}
	})

	t.Run("new streak fires; old streak does not; short streak does not", func(t *testing.T) {
		in := MasterAlertInputs{Repo: "r", DayRates: base, Streaks: []StreakInput{
			{TestID: "MM-T1", Streak: 3, PrevFailed: false, TotalRuns: 10}, // new
			{TestID: "MM-T2", Streak: 5, PrevFailed: true, TotalRuns: 10},  // already failing before
			{TestID: "MM-T3", Streak: 2, PrevFailed: false, TotalRuns: 10}, // too short
		}}
		alerts := EvaluateMasterAlerts(in)
		subjects := alertSubjects(alerts, AlertRuleNewStreak)
		if len(subjects) != 1 || subjects[0] != "MM-T1" {
			t.Fatalf("streak rule subjects = %v, want [MM-T1]", subjects)
		}
	})

	t.Run("minRuns excludes UNKNOWN-history tests (default 3)", func(t *testing.T) {
		in := MasterAlertInputs{Repo: "r", DayRates: base, MinRuns: 3, Streaks: []StreakInput{
			{TestID: "MM-TNEW", Streak: 3, PrevFailed: false, TotalRuns: 3}, // 3 runs only — exactly at the bar
			{TestID: "MM-TNEW2", Streak: 3, PrevFailed: false, TotalRuns: 2},
		}}
		alerts := EvaluateMasterAlerts(in)
		subjects := alertSubjects(alerts, AlertRuleNewStreak)
		if len(subjects) != 1 || subjects[0] != "MM-TNEW" {
			t.Fatalf("minRuns filter wrong: %v", subjects)
		}
	})

	t.Run("cross-PR cluster fires at 3 PRs, not 2", func(t *testing.T) {
		alerts := EvaluateMasterAlerts(MasterAlertInputs{Repo: "r", DayRates: base, CrossPR: []CrossPRInput{
			{TestID: "MM-T9", DistinctPRs: 2},
			{TestID: "MM-T8", DistinctPRs: 3},
		}})
		subjects := alertSubjects(alerts, AlertRuleCrossPR)
		if len(subjects) != 1 || subjects[0] != "MM-T8" {
			t.Fatalf("cross-PR subjects = %v, want [MM-T8]", subjects)
		}
	})
}

// W8 dedup gates: 12 consecutive firings → one channel post per 24h, one
// issue opened once then updated in place.
func TestApplyAlertDedupW8Gate(t *testing.T) {
	now := time.Now()
	alert := Alert{Rule: AlertRuleNewStreak, Subject: "MM-T777", Severity: "warning"}

	records := map[string]FiringRecord{}
	totalPosts, issuesOpened, issuesUpdated := 0, 0, 0

	// 12 consecutive daily firings (the W8 gate scenario).
	for i := 0; i < 12; i++ {
		at := now.Add(time.Duration(i) * 24 * time.Hour)
		plan := ApplyAlertDedup([]Alert{alert}, records, at)
		totalPosts += len(plan.ToPost)
		issuesOpened += len(plan.ToOpenIssue)
		issuesUpdated += len(plan.ToUpdateIssue)
	}

	if totalPosts != 12 {
		t.Fatalf("channel posts = %d, want 12 (daily firings, each outside the 24h cooldown)", totalPosts)
	}
	if issuesOpened != 1 {
		t.Fatalf("issues opened = %d, want exactly 1 (opened once at 48h, never again)", issuesOpened)
	}
	if issuesUpdated != 9 {
		t.Fatalf("issues updated = %d, want 9 (open at 48h, then one in-place update per day)", issuesUpdated)
	}

	rec := records[firingKey(alert.Rule, alert.Subject)]
	if rec.FireCount != 12 {
		t.Fatalf("fire count = %d, want 12 (truth increments every firing)", rec.FireCount)
	}
	if rec.ChannelPosts != 12 {
		t.Fatalf("channel posts recorded = %d, want 12", rec.ChannelPosts)
	}
}

func TestApplyAlertDedupSuppressesWithin24h(t *testing.T) {
	now := time.Now()
	alert := Alert{Rule: AlertRuleCrossPR, Subject: "MM-T888"}

	records := map[string]FiringRecord{}
	first := ApplyAlertDedup([]Alert{alert}, records, now)
	if len(first.ToPost) != 1 || first.Suppressed != 0 {
		t.Fatalf("first firing: posts=%d suppressed=%d, want 1/0", len(first.ToPost), first.Suppressed)
	}
	second := ApplyAlertDedup([]Alert{alert}, records, now.Add(1*time.Hour))
	if len(second.ToPost) != 0 || second.Suppressed != 1 {
		t.Fatalf("1h later: posts=%d suppressed=%d, want 0/1 (24h cooldown)", len(second.ToPost), second.Suppressed)
	}
	rec := records[firingKey(alert.Rule, alert.Subject)]
	if rec.FireCount != 2 {
		t.Fatalf("fire count = %d, want 2 (recorded even when suppressed)", rec.FireCount)
	}
}

func TestApplyAlertDedumpIssueNotBefore48h(t *testing.T) {
	now := time.Now()
	alert := Alert{Rule: AlertRuleNewStreak, Subject: "MM-T999"}
	records := map[string]FiringRecord{}
	day1 := ApplyAlertDedup([]Alert{alert}, records, now)
	if len(day1.ToOpenIssue) != 0 {
		t.Fatal("issue opened on day 1 — persistence requires 2 days")
	}
	day2 := ApplyAlertDedup([]Alert{alert}, records, now.Add(24*time.Hour))
	if len(day2.ToOpenIssue) != 0 {
		t.Fatal("issue opened at 24h — persistence requires 2 days")
	}
	day3 := ApplyAlertDedup([]Alert{alert}, records, now.Add(49*time.Hour))
	if len(day3.ToOpenIssue) != 1 {
		t.Fatal("issue not opened at 49h — persistence met")
	}
}

func hasRule(alerts []Alert, rule string) bool {
	for _, a := range alerts {
		if a.Rule == rule {
			return true
		}
	}
	return false
}

func alertSubjects(alerts []Alert, rule string) []string {
	var out []string
	for _, a := range alerts {
		if a.Rule == rule {
			out = append(out, a.Subject)
		}
	}
	return out
}

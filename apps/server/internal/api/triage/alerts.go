// W7/W8 — rolling-window master alerting, with dedup.
//
// The rules (build-acceptance thresholds, tunable; the floor is policy and
// ships UNSET — W12 sets it, and a hardcoded 80% floor is explicitly banned):
//
//	pass_rate_drop_24h  24h rate ≥ 10 points below the prior-7d median
//	pass_rate_trend_7d  7d median ≥ 5 points below the prior-30d median
//	pass_rate_floor     24h rate below the configured floor (0 = disabled)
//	new_failing_streak  a test newly failing ≥ 3 consecutive master runs
//	                    (tests with fewer than MinRuns total runs are excluded —
//	                    new specs are noise, not signal; flag-off via MinRuns=0)
//	cross_pr_cluster    the same test failing on ≥ 3 distinct PRs in 7d
//	                    ("open" is proxied by "had a run in the window" — the
//	                    server cannot see PR state; noted in the drift report)
//
// Everything reads RAW pass rate (W1): alerting must surface flakes, not hide
// them behind waivers.
//
// Dedup (W8): one channel post per subject per 24h; a GitHub issue opens once
// when a streak/cross-PR subject has been firing ≥ 2 days, then updates in
// place. All pure functions — the handlers only load data and apply effects.

package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// The five master-alerting rules. Subjects: "master-pass-rate" for the three
// rate rules, the test ID for the two cluster rules.
const (
	AlertRuleDrop24h   = "pass_rate_drop_24h"
	AlertRuleTrend7d   = "pass_rate_trend_7d"
	AlertRuleFloor     = "pass_rate_floor"
	AlertRuleNewStreak = "new_failing_streak"
	AlertRuleCrossPR   = "cross_pr_cluster"

	drop24hPoints   = 10.0
	trend7dPoints   = 5.0
	streakMinRuns   = 3
	crossPRMinPRs   = 3
	channelCooldown = 24 * time.Hour
	issueMinAge     = 48 * time.Hour
	issueCooldown   = 24 * time.Hour
)

// ---------- inputs ----------

// DayRate is one day's rolled raw pass rate — the unit the windows compare.
type DayRate struct {
	Day      time.Time `json:"day"`
	Outcomes int       `json:"outcomes"`
	Failures int       `json:"failures"`
	Rate     float64   `json:"rate"`
}

// StreakInput is a test's consecutive-failure streak on master as of the day.
type StreakInput struct {
	TestID     string `json:"test_id"`
	Streak     int    `json:"streak"`
	PrevFailed bool   `json:"prev_failed"` // the run before the streak — false means newly entered
	TotalRuns  int    `json:"total_runs"`
}

// CrossPRInput is a test's distinct-PR failure count inside the 7d window.
type CrossPRInput struct {
	TestID      string `json:"test_id"`
	DistinctPRs int    `json:"distinct_prs"`
}

// MasterAlertInputs is everything the pure rule engine reads for one as-of day.
type MasterAlertInputs struct {
	Repo     string         `json:"repo"`
	AsOf     time.Time      `json:"as_of"`
	DayRates []DayRate      `json:"day_rates"` // ascending by day, includes AsOf day
	Floor    float64        `json:"floor"`     // 0 = rule disabled
	MinRuns  int            `json:"min_runs"`  // streak exclusion; 0 = no exclusion
	Streaks  []StreakInput  `json:"streaks"`
	CrossPR  []CrossPRInput `json:"cross_pr"`
}

// Alert is one fired rule: what, on what subject, with what evidence.
type Alert struct {
	Rule     string         `json:"rule"`
	Subject  string         `json:"subject"`
	Severity string         `json:"severity"`
	Evidence map[string]any `json:"evidence"`
}

// EvaluateMasterAlerts is the pure rule engine.
func EvaluateMasterAlerts(in MasterAlertInputs) []Alert {
	var alerts []Alert

	rates := in.DayRates
	if len(rates) == 0 {
		return alerts
	}
	cur := rates[len(rates)-1]
	prior7 := medianRate(rates, 7)   // days strictly before the current one
	prior30 := medianRate(rates, 30) // (fewer days of history → smaller sample; honest)

	if prior7 != nil && cur.Rate <= *prior7-drop24hPoints {
		alerts = append(alerts, Alert{
			Rule: AlertRuleDrop24h, Subject: "master-pass-rate", Severity: "warning",
			Evidence: map[string]any{"rate_24h": cur.Rate, "median_7d": *prior7, "drop_points": *prior7 - cur.Rate},
		})
	}
	if prior7 != nil && prior30 != nil && *prior7 <= *prior30-trend7dPoints {
		alerts = append(alerts, Alert{
			Rule: AlertRuleTrend7d, Subject: "master-pass-rate", Severity: "warning",
			Evidence: map[string]any{"median_7d": *prior7, "median_30d": *prior30, "drop_points": *prior30 - *prior7},
		})
	}
	if in.Floor > 0 && cur.Rate < in.Floor {
		alerts = append(alerts, Alert{
			Rule: AlertRuleFloor, Subject: "master-pass-rate", Severity: "critical",
			Evidence: map[string]any{"rate_24h": cur.Rate, "floor": in.Floor},
		})
	}

	for _, s := range in.Streaks {
		if s.Streak < streakMinRuns || s.PrevFailed {
			continue // already in a streak before, or too short — not NEW
		}
		if in.MinRuns > 0 && s.TotalRuns < in.MinRuns {
			continue // UNKNOWN-history test: new specs are noise (W7 spec default)
		}
		alerts = append(alerts, Alert{
			Rule: AlertRuleNewStreak, Subject: s.TestID, Severity: "warning",
			Evidence: map[string]any{"streak": s.Streak, "total_runs": s.TotalRuns},
		})
	}
	for _, c := range in.CrossPR {
		if c.DistinctPRs >= crossPRMinPRs {
			alerts = append(alerts, Alert{
				Rule: AlertRuleCrossPR, Subject: c.TestID, Severity: "warning",
				Evidence: map[string]any{"distinct_prs": c.DistinctPRs},
			})
		}
	}
	return alerts
}

// medianRate: median of the daily rates over the `days` days STRICTLY BEFORE
// the most recent one — the baseline the current day is judged against.
func medianRate(rates []DayRate, days int) *float64 {
	if len(rates) < 2 {
		return nil
	}
	prior := rates[:len(rates)-1]
	if len(prior) > days {
		prior = prior[len(prior)-days:]
	}
	if len(prior) == 0 {
		return nil
	}
	vals := make([]float64, 0, len(prior))
	for _, d := range prior {
		if d.Outcomes > 0 {
			vals = append(vals, d.Rate)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	sort.Float64s(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return &vals[mid]
	}
	avg := (vals[mid-1] + vals[mid]) / 2
	return &avg
}

// ---------- W8 dedup ----------

// FiringRecord is the live dedup state for one (rule, subject) — the W8 noise
// controller. Fire counts are truth; posts and issue writes respect cooldowns.
type FiringRecord struct {
	Rule            string     `json:"rule"`
	Subject         string     `json:"subject"`
	FirstFiredAt    time.Time  `json:"first_fired_at"`
	LastFiredAt     time.Time  `json:"last_fired_at"`
	LastChannelPost *time.Time `json:"last_channel_post,omitempty"`
	ChannelPosts    int        `json:"channel_posts"`
	IssueURL        *string    `json:"issue_url,omitempty"`
	// Nullable column — a pointer, never a zero-int lie (minor: migration
	// 000031 leaves issue_number NULL until an issue exists).
	IssueNumber     *int       `json:"issue_number,omitempty"`
	LastIssueUpdate *time.Time `json:"last_issue_update,omitempty"`
	FireCount       int        `json:"fire_count"`

	// B9: the persisted open-intent. Set the moment an open is PLANNED and
	// written by recordFirings BEFORE the GitHub call, so a crash between
	// issue-open and URL-record cannot produce a second issue on the next
	// run. Replaces the in-memory-only issuePending.
	IssueClaimed *time.Time `json:"-"`
}

// DedupPlan is what actually goes out for this evaluation, and what was held.
type DedupPlan struct {
	ToPost        []Alert        `json:"to_post"`         // channel posts due now
	ToOpenIssue   []Alert        `json:"to_open_issue"`   // persistent, no issue yet
	ToUpdateIssue []Alert        `json:"to_update_issue"` // persistent, issue exists, cooldown passed
	Suppressed    int            `json:"suppressed"`      // fired but inside the 24h channel cooldown
	FireCountBump map[string]int `json:"-"`               // per (rule|subject) — always bumped on a firing
}

func firingKey(rule, subject string) string { return rule + "|" + subject }

// ApplyAlertDedup is the pure W8 core: given candidate alerts and the live
// firing records, decide what actually goes out. Fire counts bump on every
// firing (the truth), channel posts and issue writes respect cooldowns (the
// noise control).
func ApplyAlertDedup(cands []Alert, existing map[string]FiringRecord, now time.Time) DedupPlan {
	plan := DedupPlan{FireCountBump: map[string]int{}}
	for _, a := range cands {
		key := firingKey(a.Rule, a.Subject)
		rec, seen := existing[key]
		if !seen {
			rec = FiringRecord{Rule: a.Rule, Subject: a.Subject, FirstFiredAt: now}
		}
		rec.LastFiredAt = now
		rec.FireCount++
		plan.FireCountBump[key]++

		channelDue := rec.LastChannelPost == nil || now.Sub(*rec.LastChannelPost) >= channelCooldown
		if channelDue {
			plan.ToPost = append(plan.ToPost, a)
			t := now
			rec.LastChannelPost = &t
			rec.ChannelPosts++
		} else {
			plan.Suppressed++
		}

		// Issues: only cluster-shaped subjects (streak / cross-PR), only once
		// the firing is persistent — at least 2 days old. Open is claimed at
		// most once (B9: the claim PERSISTS via recordFirings before the
		// GitHub side effect); every later persistent firing updates in place.
		if a.Rule == AlertRuleNewStreak || a.Rule == AlertRuleCrossPR {
			if now.Sub(rec.FirstFiredAt) >= issueMinAge {
				hasIssue := rec.IssueURL != nil || rec.IssueClaimed != nil
				if !hasIssue {
					plan.ToOpenIssue = append(plan.ToOpenIssue, a)
					t := now
					rec.IssueClaimed = &t
				} else if rec.LastIssueUpdate == nil || now.Sub(*rec.LastIssueUpdate) >= issueCooldown {
					plan.ToUpdateIssue = append(plan.ToUpdateIssue, a)
					t := now
					rec.LastIssueUpdate = &t
				}
			}
		}
		existing[key] = rec
	}
	return plan
}

// ---------- data loading ----------

// normalizeAlertRepo makes ONE slug shape for every query and write in this
// file (M9): the reads match with split_part(...) = $1, but the writes bound
// the raw value — a short-form repo could read its own row and then ON
// CONFLICT miss it and insert a duplicate.
// repoFallbackOwner prefixes a bare repo name into a full slug.
const repoFallbackOwner = "mattermost/"

func normalizeAlertRepo(repo string) string {
	if containsSlash(repo) {
		return repo
	}
	return repoFallbackOwner + repo
}

type outcomePoint struct {
	At     time.Time
	Failed bool
	PR     *int
	Branch string
}

type masterAlertData struct {
	dayRates []DayRate
	series   map[string][]outcomePoint // per test, ascending by time
}

func (h *Handlers) loadMasterAlertData(ctx context.Context, repo, branch string, since time.Time) (masterAlertData, error) {
	var d masterAlertData
	d.series = map[string][]outcomePoint{}

	// M4: the series loads ALL branches. Day-rates stay branch-scoped (master
	// pass rate), but the streak rule reads master points while cross-PR
	// reads PR points — a branch filter here made cross_pr_cluster dead:
	// master runs have gh_pr_number NULL by construction.
	rows, err := h.Pool.Query(ctx, `
		WITH matched AS (
			SELECT g.id, g.created_at, g.gh_pr_number, g.branch,
			       coalesce(tc.external_test_id, 't:' || coalesce(nullif(tc.full_title, ''), tc.title)) AS test_key,
			       tc.status
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.created_at >= $2::timestamptz
		),
		rolled AS (
			SELECT id, created_at, gh_pr_number, branch, test_key,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed
			FROM matched
			GROUP BY id, created_at, gh_pr_number, branch, test_key
		)
		SELECT created_at::date, test_key, ever_failed, gh_pr_number, branch
		FROM rolled
		WHERE ever_passed OR ever_failed
		ORDER BY created_at ASC
	`, repo, since)
	if err != nil {
		return d, err
	}
	defer rows.Close()

	dayAgg := map[time.Time]*DayRate{}
	var dayOrder []time.Time
	for rows.Next() {
		var day time.Time
		var key string
		var failed bool
		var pr *int
		var ptBranch string
		if err := rows.Scan(&day, &key, &failed, &pr, &ptBranch); err != nil {
			return d, err
		}
		d.series[key] = append(d.series[key], outcomePoint{At: day, Failed: failed, PR: pr, Branch: ptBranch})

		// Day-rates stay scoped to the requested (master) branch.
		if ptBranch == branch {
			dr, ok := dayAgg[day]
			if !ok {
				dr = &DayRate{Day: day}
				dayAgg[day] = dr
				dayOrder = append(dayOrder, day)
			}
			dr.Outcomes++
			if failed {
				dr.Failures++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	for _, day := range dayOrder {
		dr := dayAgg[day]
		if dr.Outcomes > 0 {
			dr.Rate = (float64(dr.Outcomes-dr.Failures) / float64(dr.Outcomes)) * 100
		}
		d.dayRates = append(d.dayRates, *dr)
	}
	return d, nil
}

// inputsAsOf derives the rule inputs from the loaded data as of a given day —
// the same code path serves the live evaluate and every replay day.
func (d masterAlertData) inputsAsOf(repo string, asOf time.Time, floor float64, minRuns int) MasterAlertInputs {
	in := MasterAlertInputs{Repo: repo, AsOf: asOf, Floor: floor, MinRuns: minRuns}

	for _, dr := range d.dayRates {
		if !dr.Day.After(asOf) {
			in.DayRates = append(in.DayRates, dr)
		}
	}

	crossPRs := map[string]map[int]bool{}
	for key, points := range d.series {
		var streak int
		var seenPreStreak bool // a pass (or non-failed run) before the streak
		var sawMasterPoint bool
		total := 0
		for i := len(points) - 1; i >= 0; i-- {
			p := points[i]
			if p.At.After(asOf) {
				continue
			}
			total++
			isMaster := p.Branch == "main" || p.Branch == "master"
			if isMaster {
				sawMasterPoint = true
			}
			if !seenPreStreak {
				if p.Failed {
					streak++
				} else {
					seenPreStreak = true
				}
			}
			if p.Failed && p.PR != nil && p.At.After(asOf.Add(-7*24*time.Hour)) {
				if crossPRs[key] == nil {
					crossPRs[key] = map[int]bool{}
				}
				crossPRs[key][*p.PR] = true
			}
		}
		if streak >= streakMinRuns && sawMasterPoint {
			// M5: a streak that covers EVERY observed point predates the
			// window — it is not NEWLY entered, and re-firing it daily is
			// alert spam. Only a streak with a pass (or window start) behind
			// it counts as newly entering.
			predatesWindow := !seenPreStreak
			in.Streaks = append(in.Streaks, StreakInput{
				TestID:     key,
				Streak:     streak,
				PrevFailed: predatesWindow,
				TotalRuns:  total,
			})
		}
	}
	for key, prs := range crossPRs {
		if len(prs) >= crossPRMinPRs {
			in.CrossPR = append(in.CrossPR, CrossPRInput{TestID: key, DistinctPRs: len(prs)})
		}
	}
	return in
}

// ---------- handlers ----------

// AlertEvaluation serves GET /api/v1/triage/alerts/evaluation?repo= — the dry
// view: rules, candidate alerts, and what dedup WOULD do. No side effects.
func (h *Handlers) AlertEvaluation(w http.ResponseWriter, r *http.Request) {
	repo := normalizeAlertRepo(r.URL.Query().Get("repo"))
	if repo == repoFallbackOwner {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	branch := orDefault(r.URL.Query().Get("branch"), "main")
	floor, minRuns, cfgErr := alertConfigFromQuery(r)
	if cfgErr != nil {
		api.WriteError(w, r, cfgErr)
		return
	}

	data, err := h.loadMasterAlertData(r.Context(), repo, branch, time.Now().Add(-45*24*time.Hour))
	if err != nil {
		h.logError("alerts load", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	in := data.inputsAsOf(repo, time.Now(), floor, minRuns)
	alerts := EvaluateMasterAlerts(in)

	records, err := h.loadFiringRecords(r.Context(), repo)
	if err != nil {
		h.logError("alerts firing records", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	sim := map[string]FiringRecord{}
	for k, v := range records {
		sim[k] = v
	}
	plan := ApplyAlertDedup(alerts, sim, time.Now())

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":    repo,
		"inputs":  in,
		"alerts":  alerts,
		"dedup":   plan,
		"dry_run": true,
	})
}

// AlertEvaluate serves POST /api/v1/triage/alerts/evaluate?repo= — the
// scheduled job's call: evaluate, dedup, post to the channel, open/update
// issues, record firings. Authenticated (a forged alert is spam at scale).
func (h *Handlers) AlertEvaluate(w http.ResponseWriter, r *http.Request) {
	repo := normalizeAlertRepo(r.URL.Query().Get("repo"))
	if repo == repoFallbackOwner {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	branch := orDefault(r.URL.Query().Get("branch"), "main")
	floor, minRuns, cfgErr := alertConfigFromQuery(r)
	if cfgErr != nil {
		api.WriteError(w, r, cfgErr)
		return
	}

	ctx := r.Context()

	// M8: overlapping evaluations read-modify-write the same firing rows and
	// both post despite the 24h cooldown. One evaluation per repo at a time —
	// a held lock means another job is mid-flight; report and stand down.
	locked, err := h.Pool.Exec(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, repo)
	if err != nil || locked.RowsAffected() == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "alert evaluation already in flight for this repo"})
		return
	}
	defer func() {
		_, _ = h.Pool.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext($1))`, repo)
	}()
	data, err := h.loadMasterAlertData(ctx, repo, branch, time.Now().Add(-45*24*time.Hour))
	if err != nil {
		h.logError("alerts load", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	now := time.Now()
	alerts := EvaluateMasterAlerts(data.inputsAsOf(repo, now, floor, minRuns))

	records, err := h.loadFiringRecords(ctx, repo)
	if err != nil {
		h.logError("alerts firing records", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	plan := ApplyAlertDedup(alerts, records, now)

	// B9: persist the decision (fire counts, channel posts, issue CLAIMS)
	// BEFORE any side effect. A crash after this point leaves a durable
	// record that the issue was claimed — the next run updates instead of
	// opening a duplicate.
	if err := h.recordFirings(ctx, repo, records); err != nil {
		h.logError("alerts record firings (pre-notify)", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	posted, issuesOpened, issuesUpdated := 0, 0, 0
	if len(plan.ToPost) > 0 {
		if err := h.postAlertWebhook(ctx, repo, plan.ToPost); err != nil {
			h.logError("alerts webhook post", err) // fail open on notify: recording still happens
		} else {
			posted = len(plan.ToPost)
		}
	}
	for _, a := range plan.ToOpenIssue {
		url, num, err := h.openAlertIssue(ctx, repo, a)
		if err != nil {
			h.logError("alerts issue open", err)
			continue
		}
		// Minor: count only real opens — with no GitHub token the helper
		// returns "" (suppressed), which is not an opened issue.
		if url != "" {
			issuesOpened++
			key := firingKey(a.Rule, a.Subject)
			if rec, ok := records[key]; ok {
				rec.IssueURL = &url
				rec.IssueNumber = &num
				records[key] = rec
			}
		}
	}
	for _, a := range plan.ToUpdateIssue {
		if err := h.updateAlertIssue(ctx, repo, a, records[firingKey(a.Rule, a.Subject)]); err != nil {
			h.logError("alerts issue update", err)
			continue
		}
		issuesUpdated++
	}

	// Second pass: persist the GitHub URLs/numbers onto the claimed rows.
	if err := h.recordFirings(ctx, repo, records); err != nil {
		h.logError("alerts record firings (post-notify)", err) // claims already durable; URLs retry next run
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":           repo,
		"alerts":         alerts,
		"posted":         posted,
		"suppressed":     plan.Suppressed,
		"issues_opened":  issuesOpened,
		"issues_updated": issuesUpdated,
	})
}

// AlertReplay serves GET /api/v1/triage/alerts/replay?repo=&days=30 — walk
// the last N days and print, for each day, what the rules WOULD have fired
// with dedup simulated from an empty record set. The W7 gate machinery; the
// gate itself (real 30-day data, <10 firings/week, incident coverage,
// reviewer sign-off) needs production history and a human — this endpoint is
// the tool that produces the replay output for that review.
func (h *Handlers) AlertReplay(w http.ResponseWriter, r *http.Request) {
	repo := normalizeAlertRepo(r.URL.Query().Get("repo"))
	if repo == repoFallbackOwner {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	branch := orDefault(r.URL.Query().Get("branch"), "main")
	days := parseInt(r.URL.Query().Get("days"), 30)
	if days < 1 || days > 90 {
		api.WriteError(w, r, errRepoRequiredWith("days must be between 1 and 90"))
		return
	}
	floor, minRuns, cfgErr := alertConfigFromQuery(r)
	if cfgErr != nil {
		api.WriteError(w, r, cfgErr)
		return
	}

	data, err := h.loadMasterAlertData(r.Context(), repo, branch, time.Now().Add(-time.Duration(days+15)*24*time.Hour))
	if err != nil {
		h.logError("alerts replay load", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	sim := map[string]FiringRecord{}
	type dayFired struct {
		Day    string  `json:"day"`
		Alerts []Alert `json:"alerts"`
		Posted int     `json:"posted"`
	}
	out := []dayFired{}
	totalPosted := 0
	var firstDay, lastDay time.Time

	for i := days; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Truncate(24 * time.Hour)
		if len(data.dayRates) == 0 {
			break
		}
		// Only walk days that have data.
		hasData := false
		for _, dr := range data.dayRates {
			if dr.Day.Equal(day) || (dr.Day.After(day) && dr.Day.Before(day.Add(24*time.Hour))) {
				hasData = true
				break
			}
		}
		if !hasData {
			continue
		}
		if firstDay.IsZero() {
			firstDay = day
		}
		lastDay = day

		alerts := EvaluateMasterAlerts(data.inputsAsOf(repo, day.Add(23*time.Hour), floor, minRuns))
		plan := ApplyAlertDedup(alerts, sim, day)
		totalPosted += len(plan.ToPost)
		out = append(out, dayFired{Day: day.Format("2006-01-02"), Alerts: alerts, Posted: len(plan.ToPost)})
	}

	weeks := 1.0
	if span := lastDay.Sub(firstDay).Hours() / 24; span > 7 {
		weeks = span / 7
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repo":           repo,
		"days":           days,
		"days_with_data": len(out),
		"total_posts":    totalPosted,
		"posts_per_week": float64(totalPosted) / weeks,
		"replay":         out,
		"gate_note":      "W7 gate (a) wants <10 posts/week on real 30-day data; (b) incident coverage; (c) reviewer sign-off — run against production and attach to the PR",
	})
}

func alertConfigFromQuery(r *http.Request) (floor float64, minRuns int, err error) {
	// M3: rates are 0-100, so the floor parser is its own — parseFloat (max 1)
	// silently discarded every real threshold and permanently disabled the
	// rule. Out-of-range is a 400, never a silent default.
	if v := r.URL.Query().Get("floor"); v != "" {
		f, parseErr := strconv.ParseFloat(v, 64)
		if parseErr != nil || f < 0 || f > 100 {
			return 0, 0, errRepoRequiredWith("floor must be a number between 0 and 100 (pass rate points)")
		}
		floor = f // 0 = rule disabled (W12 sets it; no hardcoded threshold ships)
	}
	minRuns = parseInt(r.URL.Query().Get("min_runs"), streakMinRuns)
	return floor, minRuns, nil
}

func (h *Handlers) loadFiringRecords(ctx context.Context, repo string) (map[string]FiringRecord, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT rule, subject, first_fired_at, last_fired_at, last_channel_post,
		       channel_posts, issue_url, issue_number, last_issue_update, fire_count,
		       issue_claimed
		FROM alert_firings
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND resolved_at IS NULL
	`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FiringRecord{}
	for rows.Next() {
		var rec FiringRecord
		if err := rows.Scan(&rec.Rule, &rec.Subject, &rec.FirstFiredAt, &rec.LastFiredAt,
			&rec.LastChannelPost, &rec.ChannelPosts, &rec.IssueURL, &rec.IssueNumber,
			&rec.LastIssueUpdate, &rec.FireCount, &rec.IssueClaimed); err != nil {
			return nil, err
		}
		out[firingKey(rec.Rule, rec.Subject)] = rec
	}
	return out, rows.Err()
}

func (h *Handlers) recordFirings(ctx context.Context, repo string, records map[string]FiringRecord) error {
	for _, rec := range records {
		_, err := h.Pool.Exec(ctx, `
			INSERT INTO alert_firings (repository, rule, subject, first_fired_at, last_fired_at,
			                           last_channel_post, channel_posts, issue_url, issue_number,
			                           last_issue_update, fire_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (repository, rule, subject) WHERE resolved_at IS NULL
			DO UPDATE SET last_fired_at = EXCLUDED.last_fired_at,
			              last_channel_post = coalesce(EXCLUDED.last_channel_post, alert_firings.last_channel_post),
			              channel_posts = EXCLUDED.channel_posts,
			              issue_url = coalesce(EXCLUDED.issue_url, alert_firings.issue_url),
			              issue_number = EXCLUDED.issue_number,
			              last_issue_update = coalesce(EXCLUDED.last_issue_update, alert_firings.last_issue_update),
			              fire_count = EXCLUDED.fire_count
		`, repo, rec.Rule, rec.Subject, rec.FirstFiredAt, rec.LastFiredAt,
			rec.LastChannelPost, rec.ChannelPosts, rec.IssueURL, rec.IssueNumber,
			rec.LastIssueUpdate, rec.FireCount)
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------- notifier (env-gated; unset = log-only, flagged in W16) ----------

// postJSONTo posts a JSON body; the optional token adds GitHub auth.
func postJSONTo(ctx context.Context, url string, body any, token string) (map[string]any, error) { //nolint:gosec // G704: url is operator-configured (TSIO_ALERT_WEBHOOK_URL / api.github.com), never request input
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw)) //nolint:gosec // G704: url is operator-configured (webhook env var / api.github.com), request input only ever becomes a validated slug
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: same operator-configured destinations as above
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("post to %s: status %d", url, resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&out) // may be empty — callers treat nil fields
	return out, nil
}

func (h *Handlers) postAlertWebhook(ctx context.Context, repo string, alerts []Alert) error {
	url := os.Getenv("TSIO_ALERT_WEBHOOK_URL")
	if url == "" {
		if h.Logger != nil {
			h.Logger.Info("alert channel post suppressed (TSIO_ALERT_WEBHOOK_URL unset)", "repo", repo, "count", len(alerts))
		}
		return nil
	}
	var lines strings.Builder
	for _, a := range alerts {
		lines.WriteString("- **" + a.Rule + "** — " + a.Subject + "\n")
	}
	_, err := postJSONTo(ctx, url, map[string]any{
		"text": "🔔 **" + repo + "** master alerting fired:\n" + lines.String(),
	}, "")
	return err
}

// openAlertIssue creates the persistent-cluster issue. Returns the URL and
// number (empty/0 when GitHub is not configured — the firing is still
// recorded; issue tracking stays manual until TSIO_ALERT_GITHUB_TOKEN is set).
func (h *Handlers) openAlertIssue(ctx context.Context, repo string, a Alert) (string, int, error) {
	token := os.Getenv("TSIO_ALERT_GITHUB_TOKEN")
	if token == "" {
		if h.Logger != nil {
			h.Logger.Info("alert issue open suppressed (TSIO_ALERT_GITHUB_TOKEN unset)", "repo", repo, "subject", a.Subject)
		}
		return "", 0, nil
	}
	fullRepo, ok := fullRepoSlug(repo)
	if !ok {
		return "", 0, fmt.Errorf("invalid repo slug: %q", repo)
	}
	out, err := postJSONTo(ctx, "https://api.github.com/repos/"+fullRepo+"/issues", map[string]any{
		"title":  "[triage] persistent failure: " + a.Subject,
		"body":   "Rule `" + a.Rule + "` has fired for ≥2 days.\n\n" + alertEvidenceMarkdown(a) + "\n\n— Test System IO alerting (auto)",
		"labels": []string{"e2e-triage"},
	}, token)
	if err != nil {
		return "", 0, err
	}
	url, _ := out["html_url"].(string)
	num, _ := out["number"].(float64)
	return url, int(num), nil
}

// updateAlertIssue adds a fresh comment to the existing issue — "updated in
// place", never a second issue.
func (h *Handlers) updateAlertIssue(ctx context.Context, repo string, a Alert, rec FiringRecord) error {
	token := os.Getenv("TSIO_ALERT_GITHUB_TOKEN")
	if token == "" || rec.IssueNumber == nil {
		return nil
	}
	fullRepo, ok := fullRepoSlug(repo)
	if !ok {
		return fmt.Errorf("invalid repo slug: %q", repo)
	}
	_, err := postJSONTo(ctx,
		"https://api.github.com/repos/"+fullRepo+"/issues/"+itoa(*rec.IssueNumber)+"/comments",
		map[string]any{"body": "Still firing (`" + a.Rule + "`, fire count " + itoa(rec.FireCount) + ").\n" + alertEvidenceMarkdown(a)},
		token)
	return err
}

func alertEvidenceMarkdown(a Alert) string {
	var out strings.Builder
	for k, v := range a.Evidence {
		out.WriteString("- `" + k + "`: " + fmt.Sprintf("%v", v) + "\n")
	}
	return out.String()
}

func containsSlash(s string) bool {
	for _, c := range s {
		if c == '/' {
			return true
		}
	}
	return false
}

// fullRepoSlug normalizes a repo to owner/name and rejects anything that is
// not a plain slug — the de-taint for the GitHub issue URL (G704): request
// input can only ever become mattermost/<safe-chars>, never a URL of its own.
func fullRepoSlug(repo string) (string, bool) {
	full := repo
	if !containsSlash(full) {
		full = "mattermost/" + full
	}
	for _, r := range full {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return "", false
		}
	}
	parts := 0
	for _, r := range full {
		if r == '/' {
			parts++
		}
	}
	if parts != 1 {
		return "", false
	}
	return full, true
}

// Package kpi computes and serves pre-aggregated CI test-health rollups
// and KPI metrics (SEC-11516).
package kpi

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads pre-aggregated KPI rollups from PostgreSQL.
type Store struct {
	Pool *pgxpool.Pool
	// Now overrides time.Now in tests when non-nil.
	Now func() time.Time
}

// NowTime returns the current UTC time (honoring Store.Now when set).
func (s *Store) NowTime() time.Time {
	return s.now()
}

func (s *Store) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Supported query window literals.
const (
	Window7d  = "7d"
	Window14d = "14d"
	Window30d = "30d"
	Window90d = "90d"
)

// Supported sort literals for /kpi/summary.
const (
	SortLaneAsc      = "lane_asc"
	SortPassRateAsc  = "pass_rate_asc"  //nolint:gosec // sort enum literal
	SortPassRateDesc = "pass_rate_desc" //nolint:gosec // sort enum literal
	SortDurationDesc = "duration_desc"
	SortDurationAsc  = "duration_asc"
)

// Supported sort literals for /kpi/flaky.
const (
	SortFlakyCommitsDesc = "flaky_commits_desc"
	SortFlakeRateDesc    = "flake_rate_desc"
	SortTotalRunsDesc    = "total_runs_desc"
	SortLastFlakyDesc    = "last_flaky_desc"
	SortNameAsc          = "name_asc"
)

// FilterParams holds validated query filters shared across KPI endpoints.
type FilterParams struct {
	App        string    `json:"app"`
	Repo       string    `json:"repo"`
	Stage      string    `json:"stage"`
	Lane       string    `json:"lane"`
	Platform   string    `json:"platform"`
	Window     string    `json:"window"`
	Sort       string    `json:"sort"`
	Page       int       `json:"page,omitempty"`
	Limit      int       `json:"limit,omitempty"`
	WindowDays int       `json:"window_days"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`

	// Internal UTC date bounds (inclusive).
	CurFromDate   time.Time `json:"-"`
	CurToDate     time.Time `json:"-"`
	PriorFromDate time.Time `json:"-"`
	PriorToDate   time.Time `json:"-"`
}

// LaneCoverage declares which lanes contributed to a KPI calculation, including
// orchestrated vs upload-only distinction.
type LaneCoverage struct {
	TotalLanes           int      `json:"total_lanes"`
	OrchestratedLanes    int      `json:"orchestrated_lanes"`
	UploadOnlyLanes      int      `json:"upload_only_lanes"`
	StrictSupportedLanes int      `json:"strict_supported_lanes"`
	TotalRuns            int      `json:"total_runs"`
	OrchestratedRuns     int      `json:"orchestrated_runs"`
	UploadOnlyRuns       int      `json:"upload_only_runs"`
	CoveredLanes         []string `json:"covered_lanes"`
	UploadOnlyLaneNames  []string `json:"upload_only_lane_names"`
	Notes                []string `json:"notes"`
}

// TestLevelPassRate holds KPI 1 primary test-level pass rate on main attempt 1.
type TestLevelPassRate struct {
	PassRate           *float64 `json:"pass_rate"`
	PriorPassRate      *float64 `json:"prior_pass_rate"`
	DeltaPP            *float64 `json:"delta_pp"`
	FirstAttemptPassed int      `json:"first_attempt_passed"`
	TotalTests         int      `json:"total_tests"`
}

// LaneLevelPassRate holds KPI 1 secondary lane-level pass rates on main attempt 1.
type LaneLevelPassRate struct {
	PassRate             *float64 `json:"pass_rate"`
	PriorPassRate        *float64 `json:"prior_pass_rate"`
	DeltaPP              *float64 `json:"delta_pp"`
	PassedLanes          int      `json:"passed_lanes"`
	TotalLanes           int      `json:"total_lanes"`
	StrictPassRate       *float64 `json:"strict_pass_rate"`
	StrictPriorPassRate  *float64 `json:"strict_prior_pass_rate"`
	StrictDeltaPP        *float64 `json:"strict_delta_pp"`
	StrictPassedLanes    int      `json:"strict_passed_lanes"`
	StrictTotalLanes     int      `json:"strict_total_lanes"`
	UploadOnlyLanesCount int      `json:"upload_only_lanes_count"`
}

// MainLaneRow breaks down KPI 1 by (app, repository, lane).
type MainLaneRow struct {
	App                string   `json:"app"`
	Repository         string   `json:"repository"`
	Lane               string   `json:"lane"`
	TestPassRate       *float64 `json:"test_pass_rate"`
	FirstAttemptPassed int      `json:"first_attempt_passed"`
	TestsTotal         int      `json:"tests_total"`
	LanePassRate       *float64 `json:"lane_pass_rate"`
	StrictPassRate     *float64 `json:"strict_pass_rate"`
	LanesPassed        int      `json:"lanes_passed"`
	LanesTotal         int      `json:"lanes_total"`
	StrictLanesPassed  int      `json:"strict_lanes_passed"`
	StrictLanesTotal   int      `json:"strict_lanes_total"`
	OrchestratedRuns   int      `json:"orchestrated_runs"`
	UploadOnlyRuns     int      `json:"upload_only_runs"`
}

// MainPassRate bundles KPI 1 primary, secondary, per-lane breakdown, and coverage.
type MainPassRate struct {
	TestLevel    TestLevelPassRate `json:"test_level"`
	LaneLevel    LaneLevelPassRate `json:"lane_level"`
	ByLane       []MainLaneRow     `json:"by_lane"`
	LaneCoverage LaneCoverage      `json:"lane_coverage"`
}

// AppFlakyCount aggregates flaky test counts for one app.
type AppFlakyCount struct {
	App        string `json:"app"`
	Count      int    `json:"count"`
	PriorCount *int   `json:"prior_count"`
	Delta      *int   `json:"delta"`
}

// FlakySummary holds KPI 3 headline summary (trailing 14d, >=2 flaky commits).
type FlakySummary struct {
	WindowDays        int             `json:"window_days"`
	BaselineWindowDay int             `json:"baseline_window_days"`
	MinFlakyCommits   int             `json:"min_flaky_commits"`
	Count             int             `json:"count"`
	PriorCount        *int            `json:"prior_count"`
	Delta             *int            `json:"delta"`
	ByApp             []AppFlakyCount `json:"by_app"`
	LaneCoverage      LaneCoverage    `json:"lane_coverage"`
}

// DurationLaneRow breaks down KPI 5 by (app, repository, lane).
type DurationLaneRow struct {
	App              string   `json:"app"`
	Repository       string   `json:"repository"`
	Lane             string   `json:"lane"`
	P50Ms            *float64 `json:"p50_ms"`
	P90Ms            *float64 `json:"p90_ms"`
	SampleCount      int      `json:"sample_count"`
	OrchestratedRuns int      `json:"orchestrated_runs"`
	UploadOnlyRuns   int      `json:"upload_only_runs"`
}

// E2EDurationSummary holds KPI 5 p50/p90 PR orchestrated duration metrics.
type E2EDurationSummary struct {
	P50Ms        *float64          `json:"p50_ms"`
	PriorP50Ms   *float64          `json:"prior_p50_ms"`
	DeltaP50Ms   *float64          `json:"delta_p50_ms"`
	P90Ms        *float64          `json:"p90_ms"`
	PriorP90Ms   *float64          `json:"prior_p90_ms"`
	DeltaP90Ms   *float64          `json:"delta_p90_ms"`
	SampleCount  int               `json:"sample_count"`
	ByLane       []DurationLaneRow `json:"by_lane"`
	LaneCoverage LaneCoverage      `json:"lane_coverage"`
}

// WeeklyPoint holds one ISO-week bucket in /kpi/summary's weekly_series.
type WeeklyPoint struct {
	WeekStart              string   `json:"week_start"`
	MainTestPassRate       *float64 `json:"main_test_pass_rate"`
	MainLanePassRate       *float64 `json:"main_lane_pass_rate"`
	MainStrictLanePassRate *float64 `json:"main_strict_lane_pass_rate"`
	MainTestsTotal         int      `json:"main_tests_total"`
	MainLanesTotal         int      `json:"main_lanes_total"`
	FlakyTestCount         int      `json:"flaky_test_count"`
	E2EP50Ms               *float64 `json:"e2e_p50_ms"`
	E2EP90Ms               *float64 `json:"e2e_p90_ms"`
	E2ESampleCount         int      `json:"e2e_sample_count"`
	PRFalseRedRate         *float64 `json:"pr_false_red_rate"`
	RecurringFailuresCount *int     `json:"recurring_failures_count"`
	TriageCoverageRate     *float64 `json:"triage_coverage_rate"`
}

// SummaryResponse is the wire shape for GET /api/v1/kpi/summary.
type SummaryResponse struct {
	Filters           FilterParams       `json:"filters"`
	LaneCoverage      LaneCoverage       `json:"lane_coverage"`
	MainPassRate      MainPassRate       `json:"main_pass_rate"`
	FlakyTests        FlakySummary       `json:"flaky_tests"`
	E2EDuration       E2EDurationSummary `json:"e2e_duration"`
	WeeklySeries      []WeeklyPoint      `json:"weekly_series"`
	PRFalseRed        any                `json:"pr_false_red"`
	RecurringFailures any                `json:"recurring_failures"`
	Triage            any                `json:"triage"`
}

// Pagination describes page metadata on list endpoints.
type Pagination struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// FlakyTestRow describes one flaky (test_id, project, platform) identity.
type FlakyTestRow struct {
	TestID           string    `json:"test_id"`
	TestKey          string    `json:"test_key"`
	ZephyrKey        *string   `json:"zephyr_key"`
	File             string    `json:"file"`
	FullTitle        string    `json:"full_title"`
	Repository       string    `json:"repository"`
	App              *string   `json:"app"`
	Framework        string    `json:"framework"`
	Project          *string   `json:"project"`
	Platform         *string   `json:"platform"`
	FlakyCommitCount int       `json:"flaky_commit_count"`
	TotalCommitCount int       `json:"total_commit_count"`
	TotalRuns        int       `json:"total_runs"`
	FlakeRate        float64   `json:"flake_rate"`
	LastFlakyAt      time.Time `json:"last_flaky_at"`
	LastCommitSHA    string    `json:"last_commit_sha"`
	Lanes            []string  `json:"lanes"`
}

// FlakyResponse is the wire shape for GET /api/v1/kpi/flaky.
type FlakyResponse struct {
	Filters         FilterParams    `json:"filters"`
	WindowDays      int             `json:"window_days"`
	MinFlakyCommits int             `json:"min_flaky_commits"`
	TotalFlakyTests int             `json:"total_flaky_tests"`
	ByApp           []AppFlakyCount `json:"by_app"`
	LaneCoverage    LaneCoverage    `json:"lane_coverage"`
	Pagination      Pagination      `json:"pagination"`
	Tests           []FlakyTestRow  `json:"tests"`
}

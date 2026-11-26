// Package kpi computes and serves pre-aggregated CI test-health rollups
// and KPI metrics (SEC-11516).
package kpi

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store reads pre-aggregated KPI rollups from PostgreSQL.
type Store struct {
	Pool *pgxpool.Pool
	// Now overrides time.Now in tests when non-nil.
	Now func() time.Time
	// E2ELanes optionally overrides kpi_e2e_lanes ("owner/repo:lane" or "lane" entries).
	E2ELanes []string
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

// Supported sort literals for /kpi/prs/red.
const (
	SortCreatedDesc = "created_desc"
	SortAgeDesc     = "age_desc"
	SortPRDesc      = "pr_desc"
	SortReasonAsc   = "reason_asc"
)

// Attribution reason literals for red PR E2E lanes (KPI 2).
const (
	ReasonWentGreenOnRerun = "went_green_on_rerun"
	ReasonFailingElsewhere = "failing_elsewhere"
	ReasonOther            = "other"
)

// Supported sort literals for /kpi/failures.
const (
	SortOccurrencesDesc = "occurrences_desc"
	SortCountDesc       = "count_desc"
	SortRateDesc        = "rate_desc"
	SortTrendDesc       = "trend_desc"
	SortLastSeenDesc    = "last_seen_desc"
)

// Computed recurring-failure states (never "Fixed").
const (
	FailureStateActive        = "Active"
	FailureStateQuiet         = "Quiet"
	FailureStateNotEnoughRuns = "Not enough runs"
	FailureStateResurfaced    = "Resurfaced"

	QuietRunThreshold = 500
	StateWindowDays   = 7
	TrendWindowDays   = 14
)

// Computed test lifecycle/health states for GET /api/v1/tests/{id}/state.
const (
	TestStateFlaky = "flaky"
	TestStateNew   = "new"
	TestStateKnown = "known"
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
	Filters           FilterParams              `json:"filters"`
	LaneCoverage      LaneCoverage              `json:"lane_coverage"`
	MainPassRate      MainPassRate              `json:"main_pass_rate"`
	FlakyTests        FlakySummary              `json:"flaky_tests"`
	E2EDuration       E2EDurationSummary        `json:"e2e_duration"`
	WeeklySeries      []WeeklyPoint             `json:"weekly_series"`
	PRFalseRed        *PRFalseRedSummary        `json:"pr_false_red"`
	RecurringFailures *RecurringFailuresSummary `json:"recurring_failures"`
	Triage            *TriageSummary            `json:"triage"`
}

// TriageLabelMix describes one verdict label's count and percentage share.
type TriageLabelMix struct {
	Label string  `json:"label"`
	Count int     `json:"count"`
	Share float64 `json:"share"`
}

// RunCompletionSummary summarizes run lifecycle completion vs incomplete and timed-out runs.
// TotalRuns == CompletedRuns + IncompleteRuns + TimedOutRuns + InProgressRuns (mutually exclusive:
// runs with orch_status='timed_out' are counted in TimedOutRuns and subtracted from their group_status
// bucket so timed-out runs are neither double-counted nor dropped).
// CompletionRate = CompletedRuns / (TotalRuns - InProgressRuns) * 100 (nil when denominator is 0).
type RunCompletionSummary struct {
	TotalRuns      int      `json:"total_runs"`
	CompletedRuns  int      `json:"completed_runs"`
	IncompleteRuns int      `json:"incomplete_runs"`
	InProgressRuns int      `json:"in_progress_runs"`
	TimedOutRuns   int      `json:"timed_out_runs"`
	CompletionRate *float64 `json:"completion_rate"`
}

// TriageSummary holds SEC-11520 triage coverage, verdict reuse, label mix, and run completion metrics.
type TriageSummary struct {
	RunCoverageRate        *float64             `json:"run_coverage_rate"`
	TestCoverageRate       *float64             `json:"test_coverage_rate"`
	FailedRunsCount        int                  `json:"failed_runs_count"`
	TriagedRunsCount       int                  `json:"triaged_runs_count"`
	FailedTestOccurrences  int                  `json:"failed_test_occurrences"`
	TriagedTestOccurrences int                  `json:"triaged_test_occurrences"`
	VerdictReuseRate       *float64             `json:"verdict_reuse_rate"`
	VerdictsTotal          int                  `json:"verdicts_total"`
	VerdictsReused         int                  `json:"verdicts_reused"`
	LabelMix               []TriageLabelMix     `json:"label_mix"`
	RunCompletion          RunCompletionSummary `json:"run_completion"`
}

// TestStateResponse is the wire shape for GET /api/v1/tests/{id}/state.
// error_context is deliberately excluded and never queried.
type TestStateResponse struct {
	TestID              string    `json:"test_id"`
	TestKey             string    `json:"test_key"`
	ZephyrKey           *string   `json:"zephyr_key"`
	File                string    `json:"file"`
	FullTitle           string    `json:"full_title"`
	Repository          string    `json:"repository"`
	App                 string    `json:"app"`
	Framework           string    `json:"framework"`
	State               string    `json:"state"`
	IsFlaky             bool      `json:"is_flaky"`
	FlakyCommitCount14d int       `json:"flaky_commit_count_14d"`
	TotalCommits        int       `json:"total_commits"`
	TotalRuns           int       `json:"total_runs"`
	FirstSeenAt         time.Time `json:"first_seen_at"`
	LastSeenAt          time.Time `json:"last_seen_at"`
	LatestVerdictLabel  *string   `json:"latest_verdict_label"`
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

// PRFalseRedReasonMix breaks down settled (>24h) red PR E2E lanes by primary attribution reason.
type PRFalseRedReasonMix struct {
	WentGreenOnRerun int `json:"went_green_on_rerun"`
	FailingElsewhere int `json:"failing_elsewhere"`
	Other            int `json:"other"`
	Total            int `json:"total"`
}

// PRFalseRedLaneRow breaks down KPI 2 by configured PR E2E lane.
type PRFalseRedLaneRow struct {
	App                  string   `json:"app"`
	Repository           string   `json:"repository"`
	Lane                 string   `json:"lane"`
	FalseRedRate         *float64 `json:"false_red_rate"`
	SettledRedLanes      int      `json:"settled_red_lanes"`
	SettledFalseRedLanes int      `json:"settled_false_red_lanes"`
	UnsettledRedLanes    int      `json:"unsettled_red_lanes"`
	WentGreenOnRerun     int      `json:"went_green_on_rerun"`
	FailingElsewhere     int      `json:"failing_elsewhere"`
	Other                int      `json:"other"`
	OrchestratedRuns     int      `json:"orchestrated_runs"`
	UploadOnlyRuns       int      `json:"upload_only_runs"`
}

// PRFalseRedSummary holds KPI 2 headline lower-bound ("at least") metrics for GET /api/v1/kpi/summary.
type PRFalseRedSummary struct {
	IsLowerBound         bool                `json:"is_lower_bound"`
	RateQualifier        string              `json:"rate_qualifier"`
	SettledAfterHours    int                 `json:"settled_after_hours"`
	FalseRedRate         *float64            `json:"false_red_rate"`
	PriorFalseRedRate    *float64            `json:"prior_false_red_rate"`
	DeltaPP              *float64            `json:"delta_pp"`
	SettledRedLanes      int                 `json:"settled_red_lanes"`
	SettledFalseRedLanes int                 `json:"settled_false_red_lanes"`
	UnsettledRedLanes    int                 `json:"unsettled_red_lanes"`
	ReasonMix            PRFalseRedReasonMix `json:"reason_mix"`
	ByLane               []PRFalseRedLaneRow `json:"by_lane"`
	LaneCoverage         LaneCoverage        `json:"lane_coverage"`
}

// ConfiguredE2ELane identifies one configured PR E2E lane.
type ConfiguredE2ELane struct {
	Repository string `json:"repository"`
	Lane       string `json:"lane"`
}

// RedPRLaneRow represents one red PR E2E lane execution returned by GET /api/v1/kpi/prs/red.
// Author names are intentionally omitted.
type RedPRLaneRow struct {
	ReportGroupID                 string    `json:"report_group_id"`
	Repository                    string    `json:"repository"`
	App                           string    `json:"app"`
	Lane                          string    `json:"lane"`
	Branch                        string    `json:"branch"`
	GHPRNumber                    int       `json:"gh_pr_number"`
	CommitSHA                     string    `json:"commit_sha"`
	GHRunID                       string    `json:"gh_run_id"`
	GHRunAttempt                  string    `json:"gh_run_attempt"`
	Platform                      *string   `json:"platform"`
	Framework                     string    `json:"framework"`
	HasOrchestration              bool      `json:"has_orchestration"`
	OrchStatus                    *string   `json:"orch_status"`
	CreatedAt                     time.Time `json:"created_at"`
	AgeSeconds                    int64     `json:"age_seconds"`
	Settled                       bool      `json:"settled"`
	StillRed                      bool      `json:"still_red"`
	WentGreenOnRerun              bool      `json:"went_green_on_rerun"`
	FailingOnMain                 bool      `json:"failing_on_main"`
	OtherPRsCount                 int       `json:"other_prs_count"`
	AllSignaturesFailingElsewhere bool      `json:"all_signatures_failing_elsewhere"`
	FailedSignatureCount          int       `json:"failed_signature_count"`
	TestsFailed                   int       `json:"tests_failed"`
	Reason                        string    `json:"reason"`
	RunURL                        string    `json:"run_url"`
}

// PRRedResponse is the wire shape for GET /api/v1/kpi/prs/red.
type PRRedResponse struct {
	Filters              FilterParams        `json:"filters"`
	WindowDays           int                 `json:"window_days"`
	SettledAfterHours    int                 `json:"settled_after_hours"`
	IsLowerBound         bool                `json:"is_lower_bound"`
	RateQualifier        string              `json:"rate_qualifier"`
	FalseRedRate         *float64            `json:"false_red_rate"`
	PriorFalseRedRate    *float64            `json:"prior_false_red_rate"`
	DeltaPP              *float64            `json:"delta_pp"`
	SettledRedLanes      int                 `json:"settled_red_lanes"`
	SettledFalseRedLanes int                 `json:"settled_false_red_lanes"`
	UnsettledRedLanes    int                 `json:"unsettled_red_lanes"`
	StillRedLanes        int                 `json:"still_red_lanes"`
	ReasonMix            PRFalseRedReasonMix `json:"reason_mix"`
	ConfiguredE2ELanes   []ConfiguredE2ELane `json:"configured_e2e_lanes"`
	LaneCoverage         LaneCoverage        `json:"lane_coverage"`
	Pagination           Pagination          `json:"pagination"`
	Lanes                []RedPRLaneRow      `json:"lanes"`
}

// RecurringFailuresSummary summarizes recurring failure counts by state.
type RecurringFailuresSummary struct {
	TotalSignatures    int `json:"total_signatures"`
	ActiveCount        int `json:"active_count"`
	ResurfacedCount    int `json:"resurfaced_count"`
	QuietCount         int `json:"quiet_count"`
	NotEnoughRunsCount int `json:"not_enough_runs_count"`
	TotalOccurrences   int `json:"total_occurrences"`
	TriagedCount       int `json:"triaged_count"`
}

// RecurringFailureRow represents one canonical failure signature in GET /api/v1/kpi/failures.
type RecurringFailureRow struct {
	SignatureID          string    `json:"signature_id"`
	Fingerprint          string    `json:"fingerprint"`
	AlgoVersion          int       `json:"algo_version"`
	ErrorClass           *string   `json:"error_class"`
	NormalizedMessage    string    `json:"normalized_message"`
	TopFrame             *string   `json:"top_frame"`
	MergedSignatureIDs   []string  `json:"merged_signature_ids"`
	State                string    `json:"state"`
	QuietDays            *int      `json:"quiet_days"`
	OccurrenceCount      int       `json:"occurrence_count"`
	RawAttemptCount      int       `json:"raw_attempt_count"`
	GroupCount           int       `json:"group_count"`
	AffectedTestCount    int       `json:"affected_test_count"`
	AffectedTestRuns     int       `json:"affected_test_runs"`
	RatePer1k            *float64  `json:"rate_per_1k"`
	Occurrences14d       int       `json:"occurrences_14d"`
	AffectedRuns14d      int       `json:"affected_runs_14d"`
	RatePer1k14d         *float64  `json:"rate_per_1k_14d"`
	PriorOccurrences14d  int       `json:"prior_occurrences_14d"`
	PriorAffectedRuns14d int       `json:"prior_affected_runs_14d"`
	PriorRatePer1k14d    *float64  `json:"prior_rate_per_1k_14d"`
	DeltaRatePer1k       *float64  `json:"delta_rate_per_1k"`
	Occurrences7d        int       `json:"occurrences_7d"`
	AffectedRuns7d       int       `json:"affected_runs_7d"`
	FirstSeenAt          time.Time `json:"first_seen_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	Repositories         []string  `json:"repositories"`
	Apps                 []string  `json:"apps"`
	Lanes                []string  `json:"lanes"`
	PRNumbers            []int     `json:"pr_numbers"`
	LatestVerdictLabel   *string   `json:"latest_verdict_label"`
}

// FailuresResponse is the wire shape for GET /api/v1/kpi/failures.
type FailuresResponse struct {
	Filters      FilterParams             `json:"filters"`
	WindowDays   int                      `json:"window_days"`
	Summary      RecurringFailuresSummary `json:"summary"`
	LaneCoverage LaneCoverage             `json:"lane_coverage"`
	Pagination   Pagination               `json:"pagination"`
	Failures     []RecurringFailureRow    `json:"failures"`
}

// FailureVerdict holds non-sensitive latest triage metadata for a failure signature.
// error_context is deliberately excluded and never queried.
type FailureVerdict struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	DecidedBy  *string   `json:"decided_by"`
	Rule       *string   `json:"rule"`
	Confidence *float64  `json:"confidence"`
	Summary    *string   `json:"summary"`
	Model      *string   `json:"model"`
	TriageURL  *string   `json:"triage_url"`
	CreatedAt  time.Time `json:"created_at"`
}

// FailureFirstSeen describes where a canonical failure signature was first observed.
type FailureFirstSeen struct {
	FirstSeenAt   time.Time `json:"first_seen_at"`
	ReportGroupID *string   `json:"report_group_id"`
	Repository    *string   `json:"repository"`
	Lane          *string   `json:"lane"`
	Branch        *string   `json:"branch"`
	CommitSHA     *string   `json:"commit_sha"`
	GHPRNumber    *int      `json:"gh_pr_number"`
}

// FailureDailyPoint is one UTC day in the failure's daily occurrence & rate series.
type FailureDailyPoint struct {
	Date             string   `json:"date"`
	OccurrenceCount  int      `json:"occurrence_count"`
	RawAttemptCount  int      `json:"raw_attempt_count"`
	GroupCount       int      `json:"group_count"`
	AffectedTestRuns int      `json:"affected_test_runs"`
	RatePer1k        *float64 `json:"rate_per_1k"`
}

// FailureAffectedTest summarizes one test affected by the failure signature.
type FailureAffectedTest struct {
	TestID     string    `json:"test_id"`
	TestKey    string    `json:"test_key"`
	ZephyrKey  *string   `json:"zephyr_key"`
	File       string    `json:"file"`
	FullTitle  string    `json:"full_title"`
	Repository string    `json:"repository"`
	Framework  string    `json:"framework"`
	GroupCount int       `json:"group_count"`
	RunCount   int       `json:"run_count"`
	FailCount  int       `json:"fail_count"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// FailureAffectedRepo summarizes one repository affected by the failure signature.
type FailureAffectedRepo struct {
	Repository      string   `json:"repository"`
	App             string   `json:"app"`
	OccurrenceCount int      `json:"occurrence_count"`
	GroupCount      int      `json:"group_count"`
	AffectedRuns    int      `json:"affected_runs"`
	RatePer1k       *float64 `json:"rate_per_1k"`
	Lanes           []string `json:"lanes"`
}

// FailureDetailResponse is the wire shape for GET /api/v1/kpi/failures/{id}.
type FailureDetailResponse struct {
	Filters              FilterParams          `json:"filters"`
	RequestedSignatureID string                `json:"requested_signature_id"`
	CanonicalSignatureID string                `json:"canonical_signature_id"`
	MergedIntoID         *string               `json:"merged_into_id"`
	MergedSignatureIDs   []string              `json:"merged_signature_ids"`
	Fingerprint          string                `json:"fingerprint"`
	AlgoVersion          int                   `json:"algo_version"`
	ErrorClass           *string               `json:"error_class"`
	NormalizedMessage    string                `json:"normalized_message"`
	TopFrame             *string               `json:"top_frame"`
	SampleMessage        *string               `json:"sample_message"`
	State                string                `json:"state"`
	QuietDays            *int                  `json:"quiet_days"`
	OccurrenceCount      int                   `json:"occurrence_count"`
	RawAttemptCount      int                   `json:"raw_attempt_count"`
	GroupCount           int                   `json:"group_count"`
	AffectedTestCount    int                   `json:"affected_test_count"`
	AffectedTestRuns     int                   `json:"affected_test_runs"`
	RatePer1k            *float64              `json:"rate_per_1k"`
	Occurrences14d       int                   `json:"occurrences_14d"`
	AffectedRuns14d      int                   `json:"affected_runs_14d"`
	RatePer1k14d         *float64              `json:"rate_per_1k_14d"`
	PriorOccurrences14d  int                   `json:"prior_occurrences_14d"`
	PriorAffectedRuns14d int                   `json:"prior_affected_runs_14d"`
	PriorRatePer1k14d    *float64              `json:"prior_rate_per_1k_14d"`
	DeltaRatePer1k       *float64              `json:"delta_rate_per_1k"`
	Occurrences7d        int                   `json:"occurrences_7d"`
	AffectedRuns7d       int                   `json:"affected_runs_7d"`
	LastSeenAt           *time.Time            `json:"last_seen_at"`
	FirstSeen            FailureFirstSeen      `json:"first_seen"`
	LatestVerdict        *FailureVerdict       `json:"latest_verdict"`
	DailySeries          []FailureDailyPoint   `json:"daily_series"`
	AffectedTests        []FailureAffectedTest `json:"affected_tests"`
	AffectedRepos        []FailureAffectedRepo `json:"affected_repos"`
	PRNumbers            []int                 `json:"pr_numbers"`
}

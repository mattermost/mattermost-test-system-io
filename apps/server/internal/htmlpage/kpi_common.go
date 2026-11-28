package htmlpage

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

const (
	kpiNotAvailable    = "N/A"
	kpiDeltaNeutral    = "kpi-delta-neutral"
	kpiDeltaGood       = "kpi-delta-good"
	kpiDeltaBad        = "kpi-delta-bad"
	kpiBadgeGood       = "kpi-badge kpi-state-quiet"
	kpiBadgeWarn       = "kpi-badge kpi-reason-elsewhere"
	kpiBadgeBad        = "kpi-badge kpi-state-active"
	kpiDefaultLimit    = 50
	kpiMinRunsForPer1k = 50
)

// KPIFilterOption represents one <option> in a KPI filter or sort dropdown.
type KPIFilterOption struct {
	Value    string
	Label    string
	Selected bool
}

// KPIFilterState holds normalized active query parameters for KPI SSR links.
type KPIFilterState struct {
	App         string
	Repo        string
	Stage       string
	Lane        string
	Platform    string
	Window      string
	Sort        string
	Page        int
	Limit       int
	DefaultSort string
	ActionPath  string
	ResetHref   string
}

// HasCustomLimit reports whether a non-default page limit is active.
func (s KPIFilterState) HasCustomLimit() bool {
	return s.Limit > 0 && s.Limit != kpiDefaultLimit
}

// KPIFilterBar holds all filter/sort dropdown options for KPI pages.
type KPIFilterBar struct {
	State            KPIFilterState
	Apps             []KPIFilterOption
	Repos            []KPIFilterOption
	Stages           []KPIFilterOption
	Lanes            []KPIFilterOption
	Platforms        []KPIFilterOption
	Windows          []KPIFilterOption
	Sorts            []KPIFilterOption
	HasSorts         bool
	HasActiveFilters bool
}

// KPIChartBar represents a single vertical bar in an inline SVG chart.
type KPIChartBar struct {
	Label      string
	ValueText  string
	Tooltip    string
	X          int
	Y          int
	Width      int
	Height     int
	ColorClass string
}

var kpiOverviewSortOptions = []KPIFilterOption{
	{Value: kpi.SortOccurrencesDesc, Label: "Most occurrences"},
	{Value: kpi.SortRateDesc, Label: "Highest failure rate"},
	{Value: kpi.SortTrendDesc, Label: "Fastest rising (14d)"},
	{Value: kpi.SortLastSeenDesc, Label: "Most recently seen"},
	{Value: kpi.SortNameAsc, Label: "Signature A–Z"},
}

var kpiWindowOptions = []KPIFilterOption{
	{Value: kpi.Window7d, Label: "Last 7 days"},
	{Value: kpi.Window14d, Label: "Last 14 days"},
	{Value: kpi.Window30d, Label: "Last 30 days"},
	{Value: kpi.Window90d, Label: "Last 90 days"},
}

func kpiFilterStateFromParams(f kpi.FilterParams, actionPath, defaultSort string) KPIFilterState {
	page := f.Page
	if page < 1 {
		page = 1
	}
	limit := f.Limit
	if limit <= 0 {
		limit = kpiDefaultLimit
	}
	window := f.Window
	if window == "" {
		window = kpi.Window30d
	}
	sortBy := f.Sort
	if sortBy == "" {
		sortBy = defaultSort
	}
	return KPIFilterState{
		App:         f.App,
		Repo:        f.Repo,
		Stage:       f.Stage,
		Lane:        f.Lane,
		Platform:    f.Platform,
		Window:      window,
		Sort:        sortBy,
		Page:        page,
		Limit:       limit,
		DefaultSort: defaultSort,
		ActionPath:  actionPath,
		ResetHref:   actionPath,
	}
}

func buildKPIFilterBar(
	f kpi.FilterParams,
	opts kpi.FilterOptions,
	actionPath, defaultSort string,
	sortOpts []KPIFilterOption,
) KPIFilterBar {
	state := kpiFilterStateFromParams(f, actionPath, defaultSort)

	windows := make([]KPIFilterOption, len(kpiWindowOptions))
	for i, w := range kpiWindowOptions {
		w.Selected = w.Value == state.Window
		windows[i] = w
	}

	sorts := make([]KPIFilterOption, len(sortOpts))
	for i, s := range sortOpts {
		s.Selected = s.Value == state.Sort
		sorts[i] = s
	}

	hasActive := state.App != "" || state.Repo != "" || state.Stage != "" ||
		state.Lane != "" || state.Platform != "" ||
		(state.Window != "" && state.Window != kpi.Window30d) ||
		(state.Sort != "" && defaultSort != "" && state.Sort != defaultSort) ||
		state.HasCustomLimit()

	return KPIFilterBar{
		State:            state,
		Apps:             buildStringFilterOptions(opts.Apps, state.App),
		Repos:            buildStringFilterOptions(opts.Repos, state.Repo),
		Stages:           buildStringFilterOptions(opts.Stages, state.Stage),
		Lanes:            buildStringFilterOptions(opts.Lanes, state.Lane),
		Platforms:        buildStringFilterOptions(opts.Platforms, state.Platform),
		Windows:          windows,
		Sorts:            sorts,
		HasSorts:         len(sorts) > 0,
		HasActiveFilters: hasActive,
	}
}

func buildStringFilterOptions(values []string, selected string) []KPIFilterOption {
	out := make([]KPIFilterOption, 0, len(values)+1)
	seenSelected := selected == ""
	for _, v := range values {
		if v == "" {
			continue
		}
		isSel := v == selected
		if isSel {
			seenSelected = true
		}
		out = append(out, KPIFilterOption{
			Value:    v,
			Label:    v,
			Selected: isSel,
		})
	}
	if !seenSelected {
		out = append(out, KPIFilterOption{
			Value:    selected,
			Label:    selected,
			Selected: true,
		})
	}
	return out
}

// kpiQueryString constructs a canonical query string (or "" when all defaults)
// so URL filters round-trip cleanly across pagination, sorting, and sub-tabs.
func kpiQueryString(f KPIFilterState, page int, sort string, includeSort bool) string {
	v := url.Values{}
	if f.App != "" {
		v.Set("app", f.App)
	}
	if f.Repo != "" {
		v.Set("repo", f.Repo)
	}
	if f.Stage != "" {
		v.Set("stage", f.Stage)
	}
	if f.Lane != "" {
		v.Set("lane", f.Lane)
	}
	if f.Platform != "" {
		v.Set("platform", f.Platform)
	}
	if f.Window != "" && f.Window != kpi.Window30d {
		v.Set("window", f.Window)
	}
	if includeSort && sort != "" && sort != f.DefaultSort {
		v.Set("sort", sort)
	}
	if f.HasCustomLimit() {
		v.Set("limit", strconv.Itoa(f.Limit))
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

func kpiHref(base string, f KPIFilterState, page int, sort string, includeSort bool) string {
	return base + kpiQueryString(f, page, sort, includeSort)
}

func kpiPageQuery(f KPIFilterState, page int) string {
	q := kpiQueryString(f, page, f.Sort, true)
	if q == "" {
		return "?"
	}
	return q
}

func formatLaneCoverageSummary(cov kpi.LaneCoverage) string {
	if cov.TotalLanes == 0 && cov.TotalRuns == 0 {
		return "No matching runs in this window"
	}
	return fmt.Sprintf(
		"%s across %s (%d orchestrated, %d upload-only)",
		pluralize(cov.TotalRuns, "run", "runs"),
		pluralize(cov.TotalLanes, "lane", "lanes"),
		cov.OrchestratedLanes,
		cov.UploadOnlyLanes,
	)
}

func pluralWord(n int, singular, plural string) string {
	if n == 1 || n == -1 {
		return singular
	}
	return plural
}

func pluralize(n int, singular, plural string) string {
	return fmt.Sprintf("%d %s", n, pluralWord(n, singular, plural))
}

func shortFingerprint(fp string) string {
	if len(fp) > 14 {
		return fp[:8] + "…" + fp[len(fp)-4:]
	}
	return fp
}

// formatFailureRate avoids extrapolating small samples (<50 runs) into misleading per-1k rates.
func formatFailureRate(ratePer1k *float64, occurrences, affectedRuns int) (text string, isLowSample bool) {
	if ratePer1k == nil || affectedRuns <= 0 {
		return kpiNotAvailable, false
	}
	if affectedRuns >= kpiMinRunsForPer1k {
		return formatRatePer1k(ratePer1k), false
	}
	pct := *ratePer1k / 10.0
	if math.Abs(pct-math.Round(pct)) < 0.05 {
		return fmt.Sprintf("%.0f%% (%d of %d)", pct, occurrences, affectedRuns), true
	}
	return fmt.Sprintf("%.1f%% (%d of %d)", pct, occurrences, affectedRuns), true
}

func formatFailureDelta14d(delta *float64, curOcc, curRuns, priorOcc, priorRuns int) (text, cssClass string) {
	if priorRuns == 0 && curOcc > 0 {
		return "New in 14d", kpiDeltaBad
	}
	if (curRuns >= kpiMinRunsForPer1k && priorRuns >= kpiMinRunsForPer1k) || (curRuns == 0 && priorRuns == 0) {
		return formatDeltaRatePer1k(delta), deltaClassLowerGood(delta)
	}
	if delta == nil {
		return kpiNotAvailable, kpiDeltaNeutral
	}
	diff := curOcc - priorOcc
	if diff == 0 {
		return "No change", kpiDeltaNeutral
	}
	absDiff := diff
	if absDiff < 0 {
		absDiff = -absDiff
	}
	return fmt.Sprintf("%s %s", formatSignedIntDelta(&diff), pluralWord(absDiff, "failure", "failures")), deltaIntLowerGoodClass(&diff)
}

func healthBadgeHigherGood(v *float64, goodMin, warnMin float64) (label, cssClass string) {
	if v == nil {
		return "", ""
	}
	switch {
	case *v >= goodMin:
		return "Healthy", kpiBadgeGood
	case *v >= warnMin:
		return "Watch", kpiBadgeWarn
	default:
		return "Action needed", kpiBadgeBad
	}
}

func healthBadgeLowerGood(v *float64, goodMax, warnMax float64) (label, cssClass string) {
	if v == nil {
		return "", ""
	}
	switch {
	case *v <= goodMax:
		return "Healthy", kpiBadgeGood
	case *v <= warnMax:
		return "Watch", kpiBadgeWarn
	default:
		return "Action needed", kpiBadgeBad
	}
}

func humanizeCoverageNotes(notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, humanizeCoverageNote(n))
	}
	return out
}

func humanizeCoverageNote(note string) string {
	switch {
	case strings.Contains(note, "total_duration_ms"):
		return "Upload-only jobs (such as mobile runs) don't report job-level retry or run-time telemetry, so they are excluded from zero-retry pass rate and median/p90 E2E duration."
	case strings.Contains(note, "test_stats_json"):
		return "Upload-only jobs determine pass/fail from uploaded test results because job-level retry history is unavailable."
	case strings.Contains(note, "lower bound ('at least')"):
		return "Lower bound ('at least'): excludes runs younger than 24h until settled and excludes failures resolved by pushing a new commit."
	case strings.Contains(note, "always evaluates stage='main'"):
		return "The stage filter does not apply to Main Health (always evaluates main-branch runs)."
	case strings.Contains(note, "always evaluates stage='pr'"):
		return "The stage filter does not apply to E2E PR run time (always evaluates PR runs)."
	case strings.Contains(note, "always evaluates PR lanes"):
		return "The stage filter does not apply to PR false-red rate (always evaluates pull-request runs)."
	case strings.Contains(note, "always evaluates trailing 14d"):
		return "The window filter does not apply to Flaky tests (always evaluates the trailing 14 days)."
	default:
		return note
	}
}

func formatPct(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	return fmt.Sprintf("%.1f%%", *v)
}

func formatDeltaPP(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	if *v > 0.05 {
		return fmt.Sprintf("+%.1f pp", *v)
	}
	if *v < -0.05 {
		return fmt.Sprintf("%.1f pp", *v)
	}
	return "0.0 pp"
}

func deltaClassHigherGood(v *float64) string {
	if v == nil {
		return kpiDeltaNeutral
	}
	if *v > 0.05 {
		return kpiDeltaGood
	}
	if *v < -0.05 {
		return kpiDeltaBad
	}
	return kpiDeltaNeutral
}

func deltaClassLowerGood(v *float64) string {
	if v == nil {
		return kpiDeltaNeutral
	}
	if *v < -0.05 {
		return kpiDeltaGood
	}
	if *v > 0.05 {
		return kpiDeltaBad
	}
	return kpiDeltaNeutral
}

func formatSignedIntDelta(v *int) string {
	if v == nil {
		return kpiNotAvailable
	}
	if *v > 0 {
		return fmt.Sprintf("+%d", *v)
	}
	return strconv.Itoa(*v)
}

func deltaIntLowerGoodClass(v *int) string {
	if v == nil || *v == 0 {
		return kpiDeltaNeutral
	}
	if *v < 0 {
		return kpiDeltaGood
	}
	return kpiDeltaBad
}

func formatRatePer1k(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	return fmt.Sprintf("%.1f / 1k", *v)
}

func formatDeltaRatePer1k(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	if *v > 0.05 {
		return fmt.Sprintf("+%.1f / 1k", *v)
	}
	if *v < -0.05 {
		return fmt.Sprintf("%.1f / 1k", *v)
	}
	return "0.0 / 1k"
}

func formatDurationMs(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	return formatSecondsDuration(int64(*v / 1000.0))
}

func formatDeltaDurationMs(v *float64) string {
	if v == nil {
		return kpiNotAvailable
	}
	sec := int64(*v / 1000.0)
	if sec == 0 {
		return "0s"
	}
	if sec > 0 {
		return "+" + formatSecondsDuration(sec)
	}
	return "-" + formatSecondsDuration(-sec)
}

func formatSecondsDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	mins := sec / 60
	remSec := sec % 60
	if mins < 60 {
		if remSec == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm %ds", mins, remSec)
	}
	hours := mins / 60
	remMin := mins % 60
	if hours < 48 {
		if remMin == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, remMin)
	}
	days := hours / 24
	remHours := hours % 24
	if remHours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, remHours)
}

// mapFailureStateBadge guarantees we NEVER render "Fixed" and always pair text + color class.
func mapFailureStateBadge(state string, quietDays *int) (label, cssClass string) {
	switch state {
	case kpi.FailureStateActive:
		return kpi.FailureStateActive, kpiBadgeBad
	case kpi.FailureStateResurfaced:
		return kpi.FailureStateResurfaced, "kpi-badge kpi-state-resurfaced"
	case kpi.FailureStateQuiet:
		if quietDays != nil && *quietDays >= 0 {
			return fmt.Sprintf("%s (%dd)", kpi.FailureStateQuiet, *quietDays), kpiBadgeGood
		}
		return kpi.FailureStateQuiet, kpiBadgeGood
	default:
		return kpi.FailureStateNotEnoughRuns, "kpi-badge kpi-state-not-enough-runs"
	}
}

func safeExternalURL(raw *string) string {
	if raw == nil {
		return ""
	}
	u := strings.TrimSpace(*raw)
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

func formatKPITimestamp(t time.Time, now time.Time) (iso, rel string) {
	if t.IsZero() {
		return "", kpiNotAvailable
	}
	u := t.UTC()
	iso = u.Format(time.RFC3339)
	diff := now.UTC().Sub(u)
	if diff < time.Minute {
		return iso, "just now"
	}
	if diff < time.Hour {
		return iso, fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return iso, fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	days := int(diff.Hours() / 24)
	if days < 30 {
		return iso, fmt.Sprintf("%dd ago", days)
	}
	return iso, u.Format("2006-01-02")
}

func shortSignatureLabel(errorClass *string, normalizedMsg string) string {
	if errorClass != nil && strings.TrimSpace(*errorClass) != "" {
		return strings.TrimSpace(*errorClass)
	}
	msg := strings.TrimSpace(normalizedMsg)
	if msg == "" {
		return "Failure signature"
	}
	runes := []rune(msg)
	if len(runes) > 64 {
		return string(runes[:61]) + "..."
	}
	return msg
}

type kpiPagination struct {
	Page           int
	TotalPages     int
	RowStart       int
	ShowEnd        int
	ShowPagination bool
	HasPrev        bool
	HasNext        bool
	PrevHref       string
	NextHref       string
}

func buildKPIPagination(state KPIFilterState, page, limit, total, respTotalPages int) kpiPagination {
	totalPages, rowStart, showEnd := paginateRange(page, limit, total)
	if respTotalPages > 0 {
		totalPages = respTotalPages
	}
	if page < 1 {
		page = 1
	}
	showPagination := total > 0 && (totalPages > 1 || page > totalPages)
	hasPrev := total > 0 && totalPages >= 1 && page > 1
	var prevHref string
	if hasPrev {
		prevHref = kpiPageQuery(state, min(page-1, totalPages))
	}
	hasNext := total > 0 && page < totalPages
	var nextHref string
	if hasNext {
		nextHref = kpiPageQuery(state, page+1)
	}
	return kpiPagination{
		Page:           page,
		TotalPages:     totalPages,
		RowStart:       rowStart,
		ShowEnd:        showEnd,
		ShowPagination: showPagination,
		HasPrev:        hasPrev,
		HasNext:        hasNext,
		PrevHref:       prevHref,
		NextHref:       nextHref,
	}
}

func paginateRange(page, limit, total int) (totalPages, rowStart, showEnd int) {
	if limit <= 0 {
		limit = kpiDefaultLimit
	}
	if page < 1 {
		page = 1
	}
	if total <= 0 {
		return 1, 0, 0
	}
	totalPages = (total + limit - 1) / limit
	if page-1 > math.MaxInt/limit {
		return totalPages, 0, 0
	}
	offset := (page - 1) * limit
	if offset >= total {
		return totalPages, 0, 0
	}
	rowStart = offset + 1
	showEnd = offset + limit
	if showEnd > total {
		showEnd = total
	}
	return totalPages, rowStart, showEnd
}

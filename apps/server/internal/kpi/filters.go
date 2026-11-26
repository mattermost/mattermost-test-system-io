package kpi

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// ErrValidation indicates an invalid KPI query parameter.
var ErrValidation = errors.New("invalid KPI query parameter")

// ErrNotFound indicates a requested KPI resource (such as a signature ID) does not exist.
var ErrNotFound = errors.New("kpi resource not found")

const (
	maxAppLen      = 64
	maxRepoLen     = 256
	maxLaneLen     = 256
	maxPlatformLen = 64
	defaultLimit   = 50
	maxLimit       = 100
	maxPage        = 1_000_000
)

// ParseSummaryParams parses and validates query parameters for GET /api/v1/kpi/summary.
func ParseSummaryParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window30d, SortLaneAsc, isValidSummarySort, false)
}

// ParseFlakyParams parses and validates query parameters for GET /api/v1/kpi/flaky.
func ParseFlakyParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window14d, SortFlakyCommitsDesc, isValidFlakySort, true)
}

// ParsePRsRedParams parses and validates query parameters for GET /api/v1/kpi/prs/red.
func ParsePRsRedParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window30d, SortCreatedDesc, isValidPRRedSort, true)
}

// ParseFailuresParams parses and validates query parameters for GET /api/v1/kpi/failures.
func ParseFailuresParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window30d, SortOccurrencesDesc, isValidFailuresSort, true)
}

// ParseFailureDetailParams parses and validates query parameters for GET /api/v1/kpi/failures/{id}.
func ParseFailureDetailParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window30d, SortOccurrencesDesc, isValidFailuresSort, true)
}

// ParseTestStateParams parses and validates query parameters for GET /api/v1/tests/{id}/state.
func ParseTestStateParams(q url.Values, now time.Time) (FilterParams, error) {
	return parseParams(q, now, Window14d, SortLaneAsc, isValidSummarySort, false)
}

func parseParams(
	q url.Values,
	now time.Time,
	defaultWindow, defaultSort string,
	validSort func(string) bool,
	withPagination bool,
) (FilterParams, error) {
	app := strings.TrimSpace(q.Get("app"))
	if err := validateTextFilter("app", app, maxAppLen); err != nil {
		return FilterParams{}, err
	}

	repo := authapi.NormalizeRepository(q.Get("repo"))
	if err := validateTextFilter("repo", repo, maxRepoLen); err != nil {
		return FilterParams{}, err
	}

	stage := strings.TrimSpace(q.Get("stage"))
	if stage != "" && !isValidStage(stage) {
		return FilterParams{}, fmt.Errorf("%w: stage must be one of pr, main, release, nightly, manual, other", ErrValidation)
	}

	lane := strings.TrimSpace(q.Get("lane"))
	if err := validateTextFilter("lane", lane, maxLaneLen); err != nil {
		return FilterParams{}, err
	}

	platform := strings.TrimSpace(q.Get("platform"))
	if err := validateTextFilter("platform", platform, maxPlatformLen); err != nil {
		return FilterParams{}, err
	}

	window := strings.TrimSpace(q.Get("window"))
	if window == "" {
		window = defaultWindow
	}
	days, ok := WindowDays(window)
	if !ok {
		return FilterParams{}, fmt.Errorf("%w: window must be one of 7d, 14d, 30d, 90d", ErrValidation)
	}

	sortBy := strings.TrimSpace(q.Get("sort"))
	if sortBy == "" {
		sortBy = defaultSort
	}
	if !validSort(sortBy) {
		return FilterParams{}, fmt.Errorf("%w: invalid sort %q", ErrValidation, sortBy)
	}

	var page, limit int
	if withPagination {
		page = 1
		if rawPage := strings.TrimSpace(q.Get("page")); rawPage != "" {
			n, err := strconv.Atoi(rawPage)
			if err != nil || n < 1 || n > maxPage || n-1 > math.MaxInt/maxLimit {
				return FilterParams{}, fmt.Errorf("%w: page must be an integer between 1 and %d", ErrValidation, maxPage)
			}
			page = n
		}

		limit = defaultLimit
		if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
			n, err := strconv.Atoi(rawLimit)
			if err != nil || n < 1 || n > maxLimit {
				return FilterParams{}, fmt.Errorf("%w: limit must be an integer between 1 and %d", ErrValidation, maxLimit)
			}
			limit = n
		}
		if err := validatePaginationParams(FilterParams{Page: page, Limit: limit}); err != nil {
			return FilterParams{}, err
		}
	}

	curFrom, curTo, priorFrom, priorTo := WindowBounds(now, days)
	return FilterParams{
		App:           app,
		Repo:          repo,
		Stage:         stage,
		Lane:          lane,
		Platform:      platform,
		Window:        window,
		Sort:          sortBy,
		Page:          page,
		Limit:         limit,
		WindowDays:    days,
		From:          curFrom,
		To:            now.UTC(),
		CurFromDate:   curFrom,
		CurToDate:     curTo,
		PriorFromDate: priorFrom,
		PriorToDate:   priorTo,
	}, nil
}

func validatePaginationParams(f FilterParams) error {
	if f.Page < 1 || f.Limit < 1 || f.Page-1 > math.MaxInt/f.Limit {
		return fmt.Errorf("%w: invalid pagination page=%d limit=%d", ErrValidation, f.Page, f.Limit)
	}
	return nil
}

// WindowDays returns the number of UTC days in a window literal.
func WindowDays(w string) (int, bool) {
	switch w {
	case Window7d:
		return 7, true
	case Window14d:
		return 14, true
	case Window30d:
		return 30, true
	case Window90d:
		return 90, true
	default:
		return 0, false
	}
}

// WindowBounds returns inclusive UTC date bounds (midnight UTC) for the
// current window of length days ending on today(now) and the immediately
// preceding non-overlapping window of equal length.
func WindowBounds(now time.Time, days int) (curFrom, curTo, priorFrom, priorTo time.Time) {
	u := now.UTC()
	today := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	curTo = today
	curFrom = today.AddDate(0, 0, -(days - 1))
	priorTo = curFrom.AddDate(0, 0, -1)
	priorFrom = curFrom.AddDate(0, 0, -days)
	return curFrom, curTo, priorFrom, priorTo
}

func validateTextFilter(name, val string, maxLen int) error {
	if !utf8.ValidString(val) || strings.ContainsRune(val, 0) {
		return fmt.Errorf("%w: %s contains invalid UTF-8 or NUL byte", ErrValidation, name)
	}
	if utf8.RuneCountInString(val) > maxLen {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrValidation, name, maxLen)
	}
	return nil
}

func isValidStage(s string) bool {
	switch s {
	case "pr", "main", "release", "nightly", "manual", "other":
		return true
	default:
		return false
	}
}

func isValidSummarySort(s string) bool {
	switch s {
	case SortLaneAsc, SortPassRateAsc, SortPassRateDesc, SortDurationDesc, SortDurationAsc:
		return true
	default:
		return false
	}
}

func isValidFlakySort(s string) bool {
	switch s {
	case SortFlakyCommitsDesc, SortFlakeRateDesc, SortTotalRunsDesc, SortLastFlakyDesc, SortNameAsc:
		return true
	default:
		return false
	}
}

func isValidPRRedSort(s string) bool {
	switch s {
	case SortCreatedDesc, SortAgeDesc, SortPRDesc, SortLaneAsc, SortReasonAsc:
		return true
	default:
		return false
	}
}

func isValidFailuresSort(s string) bool {
	switch s {
	case SortOccurrencesDesc, SortCountDesc, SortRateDesc, SortTrendDesc, SortLastSeenDesc, SortNameAsc:
		return true
	default:
		return false
	}
}

// sqlFilterBuilder helps construct parameterized WHERE clauses cleanly.
type sqlFilterBuilder struct {
	args []any
}

func (b *sqlFilterBuilder) add(val any) string {
	b.args = append(b.args, val)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *sqlFilterBuilder) appendCommonFilters(alias string, f FilterParams, includeStage bool) string {
	var sb strings.Builder
	if f.App != "" {
		ph := b.add(f.App)
		fmt.Fprintf(&sb, " AND %s.app = %s", alias, ph)
	}
	if f.Repo != "" {
		ph := b.add(f.Repo)
		if strings.Contains(f.Repo, "/") {
			fmt.Fprintf(&sb, " AND %s.repository = %s", alias, ph)
		} else {
			fmt.Fprintf(&sb, " AND (%s.repository = %s OR split_part(%s.repository, '/', 2) = %s)", alias, ph, alias, ph)
		}
	}
	if includeStage && f.Stage != "" {
		ph := b.add(f.Stage)
		fmt.Fprintf(&sb, " AND %s.stage = %s", alias, ph)
	}
	if f.Lane != "" {
		ph := b.add(f.Lane)
		fmt.Fprintf(&sb, " AND %s.lane = %s", alias, ph)
	}
	return sb.String()
}

func (b *sqlFilterBuilder) appendSignatureExecFilters(alias string, f FilterParams) string {
	var sb strings.Builder
	sb.WriteString(b.appendCommonFilters(alias, f, true))
	if f.Platform != "" {
		ph := b.add(f.Platform)
		fmt.Fprintf(&sb, " AND %s.platform = %s", alias, ph)
	}
	return sb.String()
}

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
	kpiNotAvailable       = "N/A"
	kpiDash               = "—"
	kpiDeltaNeutral       = "kpi-delta-neutral"
	kpiDeltaGood          = "kpi-delta-good"
	kpiDeltaBad           = "kpi-delta-bad"
	kpiBadgeGood          = "kpi-badge kpi-state-quiet"
	kpiBadgeWarn          = "kpi-badge kpi-reason-elsewhere"
	kpiBadgeBad           = "kpi-badge kpi-state-active"
	kpiBadgeNeutral       = "kpi-badge kpi-badge-neutral"
	kpiDefaultLimit       = 50
	kpiMinRunsForPer1k    = 50
	kpiMinSettledForDelta = 10
	repoMattermostWeb     = "mattermost/mattermost"
	repoMattermostMobile  = "mattermost/mattermost-mobile"
	repoMattermostDesktop = "mattermost/desktop"
)

// KPIFilterOption represents one <option> in a KPI filter or sort dropdown.
type KPIFilterOption struct {
	Value    string
	Label    string
	Selected bool
}

// KPIRepoTab represents one product/repository scope tab in the V2 KPI header.
type KPIRepoTab struct {
	Label  string
	Repo   string
	Href   string
	Active bool
}

// KPIWindowPill represents a quick time-window selector pill (7d, 14d, 30d, 90d).
type KPIWindowPill struct {
	Value  string
	Label  string
	Href   string
	Active bool
}

// KPIPlatformPill represents a platform selector pill (All, Android, iOS) on multi-platform repos.
type KPIPlatformPill struct {
	Value  string
	Label  string
	Href   string
	Active bool
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
	ShowAll     bool
	DefaultSort string
	ActionPath  string
	ResetHref   string
}

// HasCustomLimit reports whether a non-default page limit is active.
func (s KPIFilterState) HasCustomLimit() bool {
	return s.Limit > 0 && s.Limit != kpiDefaultLimit
}

// KPIFilterBar holds all filter/sort options and navigation pills for KPI pages.
type KPIFilterBar struct {
	State            KPIFilterState
	RepoTabs         []KPIRepoTab
	WindowPills      []KPIWindowPill
	PlatformPills    []KPIPlatformPill
	ShowPlatforms    bool
	ShowLaneSelect   bool
	IsSingleRepo     bool
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
	{Value: kpi.Window7d, Label: "7d"},
	{Value: kpi.Window14d, Label: "14d"},
	{Value: kpi.Window30d, Label: "30d"},
	{Value: kpi.Window90d, Label: "90d"},
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

func friendlyRepoName(repo string) string {
	switch repo {
	case repoMattermostWeb:
		return "Web & Server"
	case repoMattermostMobile:
		return "Mobile"
	case repoMattermostDesktop:
		return "Desktop"
	default:
		if idx := strings.LastIndex(repo, "/"); idx >= 0 && idx+1 < len(repo) {
			return repo[idx+1:]
		}
		return repo
	}
}

func repoMatchesApp(repo, app string) bool {
	if app == "" {
		return false
	}
	switch repo {
	case repoMattermostWeb:
		return app == "mattermost"
	case repoMattermostMobile:
		return app == "mobile"
	case repoMattermostDesktop:
		return app == "desktop"
	default:
		return strings.EqualFold(repo, app) || strings.HasSuffix(strings.ToLower(repo), "/"+strings.ToLower(app))
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
	windowPills := make([]KPIWindowPill, len(kpiWindowOptions))
	for i, w := range kpiWindowOptions {
		w.Selected = w.Value == state.Window
		windows[i] = w

		wState := state
		wState.Window = w.Value
		windowPills[i] = KPIWindowPill{
			Value:  w.Value,
			Label:  w.Label,
			Href:   kpiHref(actionPath, wState, 1, state.Sort, true),
			Active: w.Selected,
		}
	}

	sorts := make([]KPIFilterOption, len(sortOpts))
	for i, s := range sortOpts {
		s.Selected = s.Value == state.Sort
		sorts[i] = s
	}

	isSingleRepo := state.Repo != "" || state.App != ""

	repoList := make([]string, 0, len(opts.Repos)+1)
	seenRepo := map[string]bool{}
	for _, r := range opts.Repos {
		if r != "" && !seenRepo[r] {
			seenRepo[r] = true
			repoList = append(repoList, r)
		}
	}
	if state.Repo != "" && !seenRepo[state.Repo] {
		repoList = append(repoList, state.Repo)
	}

	allState := KPIFilterState{
		Window:      state.Window,
		Limit:       state.Limit,
		DefaultSort: state.DefaultSort,
		ActionPath:  actionPath,
		ResetHref:   actionPath,
	}
	repoTabs := []KPIRepoTab{{
		Label:  "All Repos",
		Href:   kpiHref(actionPath, allState, 1, "", false),
		Active: !isSingleRepo,
	}}
	for _, r := range repoList {
		rState := allState
		rState.Repo = r
		active := state.Repo == r || (state.Repo == "" && repoMatchesApp(r, state.App))
		repoTabs = append(repoTabs, KPIRepoTab{
			Label:  friendlyRepoName(r),
			Repo:   r,
			Href:   kpiHref(actionPath, rState, 1, "", false),
			Active: active,
		})
	}

	platformPills := make([]KPIPlatformPill, 0, len(opts.Platforms)+1)
	if isSingleRepo && (len(opts.Platforms) > 0 || state.Platform != "") {
		allPlatState := state
		allPlatState.Platform = ""
		platformPills = append(platformPills, KPIPlatformPill{
			Value:  "",
			Label:  "All",
			Href:   kpiHref(actionPath, allPlatState, 1, state.Sort, true),
			Active: state.Platform == "",
		})
		for _, p := range opts.Platforms {
			if p == "" {
				continue
			}
			pState := state
			pState.Platform = p
			platformPills = append(platformPills, KPIPlatformPill{
				Value:  p,
				Label:  formatPlatformLabel(p),
				Href:   kpiHref(actionPath, pState, 1, state.Sort, true),
				Active: state.Platform == p,
			})
		}
	}

	hasActive := state.App != "" || state.Repo != "" || state.Stage != "" ||
		state.Lane != "" || state.Platform != "" ||
		(state.Window != "" && state.Window != kpi.Window30d) ||
		(state.Sort != "" && defaultSort != "" && state.Sort != defaultSort) ||
		state.HasCustomLimit()

	return KPIFilterBar{
		State:            state,
		RepoTabs:         repoTabs,
		WindowPills:      windowPills,
		PlatformPills:    platformPills,
		ShowPlatforms:    len(platformPills) > 0,
		ShowLaneSelect:   isSingleRepo && (len(opts.Lanes) > 2 || state.Lane != ""),
		IsSingleRepo:     isSingleRepo,
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

func formatPlatformLabel(p string) string {
	switch strings.ToLower(p) {
	case "ios":
		return "iOS"
	case "android":
		return "Android"
	default:
		return p
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
	return kpiQueryStringWithShow(f, page, sort, includeSort, false)
}

func kpiQueryStringWithShow(f KPIFilterState, page int, sort string, includeSort, includeShow bool) string {
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
	if includeShow && f.ShowAll {
		v.Set("show", "all")
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

func kpiOverviewHref(f KPIFilterState, showAll bool) string {
	return "/kpi" + kpiQueryStringWithShow(f, 1, f.Sort, true, showAll)
}

func kpiPageQuery(f KPIFilterState, page int) string {
	q := kpiQueryStringWithShow(f, page, f.Sort, true, f.ShowAll)
	if q == "" {
		return "?"
	}
	return q
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

func formatPRFalseRedDelta(deltaPP *float64, curSettled, priorSettled int) (text, cssClass string) {
	if deltaPP == nil || curSettled < kpiMinSettledForDelta || priorSettled < kpiMinSettledForDelta {
		return "", ""
	}
	if *deltaPP > 0.05 {
		return fmt.Sprintf("Up %.1f pts", *deltaPP), kpiDeltaBad
	}
	if *deltaPP < -0.05 {
		return fmt.Sprintf("Down %.1f pts", -*deltaPP), kpiDeltaGood
	}
	return "No change", kpiDeltaNeutral
}

func humanizeVerdictLabel(raw string) string {
	switch raw {
	case "cleared":
		return "Cleared"
	case "kept_red":
		return "Kept red"
	default:
		return raw
	}
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
		return "Dormant", "kpi-badge kpi-state-not-enough-runs"
	}
}

func safeExternalURL(raw *string) string {
	if raw == nil {
		return ""
	}
	u := strings.TrimSpace(*raw)
	if u == "" || hasASCIIControl(u) {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	return u
}

func isSafeLocalReportHref(raw string) bool {
	if raw == "" || hasASCIIControl(raw) {
		return false
	}
	if raw != "/" && !strings.HasPrefix(raw, "/reports/") {
		return false
	}
	return !strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, "/\\")
}

func hasASCIIControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
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
	showPagination := total > 0
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

package htmlpage

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
)

// One home list row (live or static).
type HomeRunRow struct {
	GroupID        string
	RowNum         int
	Href           string
	Name           string
	RepositoryName string
	Branch         string
	ShortSHA       string
	GHRunID        string
	GHRunAttempt   string
	GHPRNumber     *int
	CreatedAt      string
	CreatedAtRel   string
	StatusIcon     string
	HasFailed      bool
	PassRate       string
	PassRateClass  string
	Passed         int
	Failed         int
	Flaky          int
	Skipped        int
	Total          int
	HasStats       bool
	SpecDone          int
	SpecTotal         int
	SpecPass          int
	SpecFail          int
	SpecSkipped       int
	SpecPending       int
	SpecLeased        int
	SpecUntested      int
	SpecUntestedClass string
	SpecRetest        int
	SpecClass         string
	DurationText   string
	DurationTitle  string
	IsLive         bool
	IsNew          bool
	Region         string
	FilterRepo     string
	FilterBranch   string
}

func mapLiveRun(r reports.HomeLiveRun) HomeRunRow {
	row := HomeRunRow{
		GroupID:        r.GroupID,
		Href:           r.Href,
		Name:           r.Name,
		RepositoryName: r.RepositoryName,
		Branch:         r.Branch,
		ShortSHA:       r.ShortSHA,
		GHRunID:        r.GHRunID,
		GHRunAttempt:   r.GHRunAttempt,
		GHPRNumber:     r.GHPRNumber,
		CreatedAt:      r.CreatedAt,
		CreatedAtRel:   relativeTimeISO(r.CreatedAt),
		StatusIcon:     "live",
		IsLive:         true,
		Region:         "home-live",
		FilterRepo:     strings.ToLower(r.RepositoryName),
		FilterBranch:   strings.ToLower(r.Branch),
	}
	if r.Orch != nil {
		fillSpecProgress(&row, r.Orch)
		fillStatsFromOrchView(&row, r.Orch)
		fillDurationFromOrch(&row, r.Orch, nil, nil)
	}
	return row
}

func mapStaticRun(r reports.HomeStaticRun) HomeRunRow {
	row := HomeRunRow{
		Href:           r.Href,
		Name:           r.Name,
		RepositoryName: r.RepositoryName,
		Branch:         r.Branch,
		ShortSHA:       r.ShortSHA,
		GHRunID:        r.GHRunID,
		GHRunAttempt:   r.GHRunAttempt,
		GHPRNumber:     r.GHPRNumber,
		CreatedAt:      r.CreatedAt,
		CreatedAtRel:   relativeTimeISO(r.CreatedAt),
		HasFailed:      r.HasFailed,
		IsLive:         false,
		Region:         "home-static",
		FilterRepo:     strings.ToLower(r.RepositoryName),
		FilterBranch:   strings.ToLower(r.Branch),
	}
	if r.Total > 0 {
		row.HasStats = true
		row.Passed = r.Passed
		row.Failed = r.Failed
		row.Flaky = r.Flaky
		row.Skipped = r.Skipped
		row.Total = r.Total
		if rate := passRateDisplay(r.Passed, r.Failed, r.Flaky); rate != "" {
			row.PassRate = rate
			row.PassRateClass = passRateColorClass(rate)
		}
	}
	row.StatusIcon = statusIconForRun(r.Status, r.Orch, r.Total, r.HasFailed, r.Flaky)
	row.IsNew = isRecentISO(r.CreatedAt, time.Hour)
	if r.Orch != nil {
		fillSpecProgress(&row, r.Orch)
		fillDurationFromOrch(&row, r.Orch, r.WallClockMs, r.RetestWallMs)
	} else {
		fillDurationFromWallClock(&row, r.WallClockMs, r.RetestWallMs)
	}
	return row
}

func fillSpecProgress(row *HomeRunRow, orch *reports.HomeOrchView) {
	if orch == nil || orch.TotalUnits <= 0 {
		return
	}
	c := orch.Counts
	row.SpecTotal = orch.TotalUnits
	row.SpecPass = c.CompletedPass
	row.SpecFail = c.CompletedFail
	row.SpecSkipped = c.CompletedSkipped
	row.SpecDone = c.CompletedPass + c.CompletedFail + c.CompletedSkipped + c.Abandoned
	switch {
	case row.SpecDone < orch.TotalUnits && orch.Status == "in_progress":
		row.SpecClass = "spec-blue"
	case row.SpecDone < orch.TotalUnits:
		row.SpecClass = "spec-orange"
	default:
		row.SpecClass = "spec-neutral"
	}
	row.SpecRetest = c.RetestEligible
	if orch.Status == "in_progress" {
		row.SpecPending = c.Pending
		row.SpecLeased = c.Leased
	} else if c.Abandoned > 0 {
		row.SpecUntested = c.Abandoned
		row.SpecUntestedClass = "text-fail"
	}
}

func fillStatsFromOrchView(row *HomeRunRow, orch *reports.HomeOrchView) {
	if orch == nil || orch.Tests == nil || orch.Tests.Total <= 0 {
		return
	}
	t := orch.Tests
	row.HasStats = true
	row.Passed = t.Passed
	row.Failed = t.Failed
	row.Flaky = t.Flaky
	row.Skipped = t.Skipped
	row.Total = t.Total
	row.HasFailed = t.Failed > 0
	if rate := passRateDisplay(t.Passed, t.Failed, t.Flaky); rate != "" {
		row.PassRate = rate
		row.PassRateClass = passRateColorClass(rate)
	}
}

func fillDurationFromOrch(row *HomeRunRow, orch *reports.HomeOrchView, wall, retest *int64) {
	if orch != nil && orch.Durations != nil {
		d := orch.Durations
		if !d.BeginAt.IsZero() && d.LastTestAt != nil {
			totalMs := d.LastTestAt.Sub(d.BeginAt).Milliseconds()
			if totalMs > 0 {
				row.DurationText = formatDuration(totalMs)
				row.DurationTitle = orchestrationDurationTitle(d)
				return
			}
		}
	}
	fillDurationFromWallClock(row, wall, retest)
}

func fillDurationFromWallClock(row *HomeRunRow, wall, retest *int64) {
	if wall != nil && *wall > 0 {
		row.DurationText = formatDuration(*wall)
		if retest != nil && *retest > 0 {
			row.DurationText += " + " + formatDuration(*retest)
		}
	}
}

func orchestrationDurationTitle(d *reports.HomeOrchDurationsView) string {
	if d == nil {
		return ""
	}
	var parts []string
	if d.FirstTestAt != nil && !d.BeginAt.IsZero() {
		setupMs := d.FirstTestAt.Sub(d.BeginAt).Milliseconds()
		if setupMs > 0 {
			parts = append(parts, formatDuration(setupMs)+" setup")
		}
	}
	if d.FirstPassMs != nil && *d.FirstPassMs > 0 {
		parts = append(parts, formatDuration(*d.FirstPassMs)+" first-pass")
	}
	if d.RetestMs != nil && *d.RetestMs > 0 {
		parts = append(parts, formatDuration(*d.RetestMs)+" retest")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " + ") + " (phases may overlap)"
}

func statusIconForRun(groupStatus string, orch *reports.HomeOrchView, total int, hasFailed bool, flaky int) string {
	_ = total
	_ = flaky
	// Icon from orchestration when present.
	if orch != nil {
		switch orch.Status {
		case "in_progress":
			return "live"
		case "timed_out":
			return "failed"
		}
		if hasFailed || orch.Counts.CompletedFail > 0 || orch.Counts.Abandoned > 0 {
			return "failed"
		}
		return "passed"
	}
	if groupStatus == "in_progress" {
		return "live"
	}
	if hasFailed || groupStatus == "incomplete" {
		return "failed"
	}
	return "passed"
}

func passRateDisplay(passed, failed, flaky int) string {
	p := passed + flaky
	total := p + failed
	if total == 0 {
		return ""
	}
	rate := float64(p*100) / float64(total)
	if rate == 100 {
		return "100"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", math.Floor(rate*10)/10), "0"), ".")
}

func passRateColorClass(rate string) string {
	if rate == "100" {
		return "rate-perfect"
	}
	return "rate-fail"
}

func formatDuration(ms int64) string {
	seconds := ms / 1000
	minutes := seconds / 60
	hours := minutes / 60
	if hours > 0 {
		rem := minutes % 60
		if rem > 0 {
			return fmt.Sprintf("%dh %dm", hours, rem)
		}
		return fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		rem := seconds % 60
		if rem > 0 {
			return fmt.Sprintf("%dm %ds", minutes, rem)
		}
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", seconds)
}

func relativeTimeISO(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	if days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	// Past 7d: calendar date.
	return t.Format("1/2/2006")
}

func isRecentISO(iso string, window time.Duration) bool {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return false
	}
	age := time.Since(t)
	return age >= 0 && age <= window
}

package reports

import (
	"net/url"
	"sort"
	"strings"
)

// runFamilies maps canonical run labels to legacy mobile report_group.name values.
var runFamilies = map[string][]string{
	"mobile-pr":   {"mobile-detox-pr", "mobile-maestro-pr"},
	"mobile-main": {"mobile-detox-main", "mobile-maestro-main", "mobile-master", "mobile-detox-master", "mobile-maestro-master"},
	"cmt-mobile":  {"mobile-cmt-detox", "mobile-cmt-maestro", "mobile-cmt"},
}

// canonicalRunName returns the unified run label for a stored group name.
func canonicalRunName(groupName string) string {
	for canon, members := range runFamilies {
		if groupName == canon {
			return canon
		}
		for _, m := range members {
			if m == groupName {
				return canon
			}
		}
	}
	return groupName
}

// expandedGroupNames returns all report_group.name values for the same workflow run.
func expandedGroupNames(name string) []string {
	if members, ok := runFamilies[name]; ok {
		return append([]string{name}, members...)
	}
	for canon, members := range runFamilies {
		for _, m := range members {
			if m == name {
				return append([]string{canon}, members...)
			}
		}
	}
	return []string{name}
}

func isRunFamily(name string) bool {
	if _, ok := runFamilies[name]; ok {
		return true
	}
	for _, members := range runFamilies {
		for _, m := range members {
			if m == name {
				return true
			}
		}
	}
	return false
}

// rewriteGroupedRunURLPath replaces only the final URL segment when canonicalizing a run name.
func rewriteGroupedRunURLPath(path, oldName, canon string) string {
	if oldName == canon || path == "" {
		return path
	}
	query := ""
	if i := strings.Index(path, "?"); i >= 0 {
		query = path[i:]
		path = path[:i]
	}
	oldSeg := url.PathEscape(oldName)
	newSeg := url.PathEscape(canon)
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return path + query
	}
	segments := strings.Split(trimmed, "/")
	last := len(segments) - 1
	if segments[last] == oldSeg || segments[last] == oldName {
		segments[last] = newSeg
		return "/" + strings.Join(segments, "/") + query
	}
	return path + query
}

func mergeRunEntryStats(a, b *runEntry) {
	if a.TestStats == nil {
		a.TestStats = b.TestStats
		return
	}
	if b.TestStats == nil {
		return
	}
	a.TestStats.Total += b.TestStats.Total
	a.TestStats.Passed += b.TestStats.Passed
	a.TestStats.Failed += b.TestStats.Failed
	a.TestStats.Skipped += b.TestStats.Skipped
	a.TestStats.Flaky += b.TestStats.Flaky
	if b.TestStats.DurationMs != nil {
		if a.TestStats.DurationMs == nil {
			v := *b.TestStats.DurationMs
			a.TestStats.DurationMs = &v
		} else {
			sum := *a.TestStats.DurationMs + *b.TestStats.DurationMs
			a.TestStats.DurationMs = &sum
		}
	}
	if b.TestStats.WallClockMs != nil {
		if a.TestStats.WallClockMs == nil {
			v := *b.TestStats.WallClockMs
			a.TestStats.WallClockMs = &v
		} else if *b.TestStats.WallClockMs > *a.TestStats.WallClockMs {
			v := *b.TestStats.WallClockMs
			a.TestStats.WallClockMs = &v
		}
	}
	a.TotalReportsExpected += b.TotalReportsExpected
	a.ReportsCount += b.ReportsCount
	if b.TestStats != nil && b.TestStats.Failed > 0 {
		a.Status = "completed"
	}
}

func runEntryMergeKey(e runEntry) string {
	return strings.Join([]string{e.GHRunID, e.GHRunAttempt, e.Branch, e.Commit, canonicalRunName(e.Name)}, "\x00")
}

func mergeGroupedRunEntries(runs []runEntry) []runEntry {
	type slot struct {
		entry runEntry
		order int
	}
	byKey := map[string]*slot{}
	order := []string{}
	for i, e := range runs {
		if !isRunFamily(e.Name) {
			k := e.ReportID
			byKey[k] = &slot{entry: e, order: i}
			order = append(order, k)
			continue
		}
		k := runEntryMergeKey(e)
		if s, ok := byKey[k]; ok {
			mergeRunEntryStats(&s.entry, &e)
			if e.CreatedAt > s.entry.CreatedAt {
				s.entry.CreatedAt = e.CreatedAt
			}
			if e.LastUploadAt > s.entry.LastUploadAt {
				s.entry.LastUploadAt = e.LastUploadAt
			}
			continue
		}
		oldName := e.Name
		canon := canonicalRunName(e.Name)
		if canon != oldName {
			e.URLPath = rewriteGroupedRunURLPath(e.URLPath, oldName, canon)
		}
		e.Name = canon
		byKey[k] = &slot{entry: e, order: i}
		order = append(order, k)
	}
	slots := make([]*slot, 0, len(order))
	for _, k := range order {
		slots = append(slots, byKey[k])
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].order < slots[j].order })
	out := make([]runEntry, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.entry)
	}
	return out
}

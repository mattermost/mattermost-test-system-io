package ingest

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

type detoxReport struct {
	TestResults []detoxTestFile `json:"testResults"`
}

type detoxTestFile struct {
	TestFilePath string            `json:"testFilePath"`
	PerfStats    *detoxPerfStats   `json:"perfStats"`
	TestResults  []detoxTestResult `json:"testResults"`
}

type detoxPerfStats struct {
	Start int64 `json:"start"` // unix ms
}

type detoxTestResult struct {
	AncestorTitles  []string `json:"ancestorTitles"`
	Duration        *int64   `json:"duration"`
	FailureMessages []string `json:"failureMessages"`
	FullName        string   `json:"fullName"`
	Status          string   `json:"status"`
	Title           string   `json:"title"`
}

// extractDetox parses a Detox (Jest) JSON report. Jest reports per-file with
// a nested ancestorTitles describe chain; we group by the joined chain into
// one ExtractedSuite per unique chain within a file.
func extractDetox(body []byte, seq *int) []ExtractedSuite {
	var r detoxReport
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}

	var out []ExtractedSuite
	for _, file := range r.TestResults {
		filePath := relativeDetoxPath(file.TestFilePath)
		var fileStart *time.Time
		if file.PerfStats != nil && file.PerfStats.Start > 0 {
			t := time.UnixMilli(file.PerfStats.Start).UTC()
			fileStart = &t
		}

		bucketOrder := []string{}
		buckets := map[string][]ExtractedCase{}
		for _, t := range file.TestResults {
			key := strings.Join(t.AncestorTitles, " > ")
			if _, ok := buckets[key]; !ok {
				bucketOrder = append(bucketOrder, key)
			}
			dur := int64(0)
			if t.Duration != nil {
				dur = *t.Duration
			}
			var errMsg *string
			if len(t.FailureMessages) > 0 {
				msg := strings.Join(t.FailureMessages, "\n")
				errMsg = &msg
			}
			c := ExtractedCase{
				Title:        t.Title,
				FullTitle:    t.FullName,
				Status:       detoxStatus(t.Status),
				DurationMs:   dur,
				ErrorMessage: errMsg,
				Sequence:     *seq,
				StartTime:    fileStart,
			}
			*seq++
			buckets[key] = append(buckets[key], c)
		}

		for _, key := range bucketOrder {
			title := key
			if title == "" {
				if filePath != "" {
					title = filePath
				} else {
					title = "Root"
				}
			}
			var fp *string
			if filePath != "" {
				f := filePath
				fp = &f
			}
			out = append(out, ExtractedSuite{
				Title:     title,
				FilePath:  fp,
				StartTime: fileStart,
				Cases:     buckets[key],
			})
		}
	}
	return out
}

func detoxStatus(s string) string {
	switch s {
	case StatusPassed:
		return StatusPassed
	case StatusFailed:
		return StatusFailed
	case cypressStatePending, StatusSkipped, "todo":
		return StatusSkipped
	default:
		return s
	}
}

// relativeDetoxPath normalizes a Detox/Jest suite path to a stable
// repo-relative form. Layout-agnostic: does not assume detox/e2e vs e2e/detox.
//
//   - Already-relative paths are returned unchanged.
//   - Absolute CI paths (.../work/<repo>/<repo>/<rel>) have the workspace
//     prefix stripped when recognizable; otherwise the input is returned.
func relativeDetoxPath(p string) string {
	if p == "" {
		return ""
	}
	normalized := filepath.ToSlash(p)
	normalized = strings.TrimPrefix(normalized, "./")

	// Repo-relative already — keep identity stable across folder moves.
	if !filepath.IsAbs(p) && !strings.HasPrefix(normalized, "/") {
		return normalized
	}

	// GitHub Actions / common nested workspaces: .../work/<repo>/<repo>/<relative>
	if i := strings.Index(normalized, "/work/"); i >= 0 {
		rest := normalized[i+len("/work/"):]
		// rest = "<repo>/<repo>/<relative...>"
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) == 3 && parts[2] != "" {
			return parts[2]
		}
	}

	return normalized
}

package ingest

import (
	"encoding/json"
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

// relativeDetoxPath strips the host-specific prefix up to (and including)
// "/e2e/" so the stored file path is project-relative. Falls back to the
// absolute path when the marker is absent.
func relativeDetoxPath(p string) string {
	if p == "" {
		return ""
	}
	const marker = "/e2e/"
	if i := strings.LastIndex(p, marker); i >= 0 {
		return p[i+len(marker):]
	}
	return p
}

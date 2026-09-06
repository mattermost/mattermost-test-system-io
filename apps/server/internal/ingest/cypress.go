package ingest

import (
	"encoding/json"
	"net/url"
	"time"
)

type cypressReport struct {
	Stats   *cypressStats   `json:"stats"`
	Results []cypressResult `json:"results"`
}

type cypressStats struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type cypressResult struct {
	Title    string         `json:"title"`
	File     string         `json:"file"`
	FullFile string         `json:"fullFile"`
	Tests    []cypressTest  `json:"tests"`
	Suites   []cypressSuite `json:"suites"`
}

type cypressSuite struct {
	Title  string         `json:"title"`
	File   string         `json:"file"`
	Tests  []cypressTest  `json:"tests"`
	Suites []cypressSuite `json:"suites"`
}

type cypressTest struct {
	Title     string        `json:"title"`
	FullTitle string        `json:"fullTitle"`
	Duration  int64         `json:"duration"`
	State     string        `json:"state"`
	Pass      bool          `json:"pass"`
	Fail      bool          `json:"fail"`
	Pending   bool          `json:"pending"`
	Skipped   bool          `json:"skipped"`
	Context   *string       `json:"context"`
	Err       *cypressError `json:"err"`
}

type cypressError struct {
	Message string `json:"message"`
	Estack  string `json:"estack"`
}

// extractCypress parses a Mochawesome (Cypress) JSON file. Mochawesome nests
// suites arbitrarily; we flatten them here so each ExtractedSuite carries
// only direct test cases (no children). The "file" property is inherited
// down the nesting chain when child suites don't specify their own.
func extractCypress(body []byte, seq *int) []ExtractedSuite {
	var r cypressReport
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}

	var startTime *time.Time
	if r.Stats != nil && r.Stats.Start != "" {
		if dt, err := time.Parse(time.RFC3339, r.Stats.Start); err == nil {
			u := dt.UTC()
			startTime = &u
		}
	}

	var out []ExtractedSuite
	for _, result := range r.Results {
		filePath := firstNonEmpty(result.FullFile, result.File)
		for _, nested := range result.Suites {
			out = append(out, walkCypressSuite(nested, filePath, seq, startTime)...)
		}
		if len(result.Tests) > 0 {
			cases := make([]ExtractedCase, 0, len(result.Tests))
			for _, t := range result.Tests {
				cases = append(cases, extractCypressTest(t, seq, startTime))
			}
			var fp *string
			if filePath != "" {
				f := filePath
				fp = &f
			}
			out = append(out, ExtractedSuite{
				Title:     result.Title,
				FilePath:  fp,
				StartTime: startTime,
				Cases:     cases,
			})
		}
	}
	return out
}

func walkCypressSuite(s cypressSuite, inheritedFile string, seq *int, startTime *time.Time) []ExtractedSuite {
	filePath := s.File
	if filePath == "" {
		filePath = inheritedFile
	}

	cases := make([]ExtractedCase, 0, len(s.Tests))
	for _, t := range s.Tests {
		cases = append(cases, extractCypressTest(t, seq, startTime))
	}
	// Mochawesome nested suites that group more tests under the same title —
	// flatten by pulling their cases up into the current suite.
	for _, nested := range s.Suites {
		for _, nestedSuite := range walkCypressSuite(nested, filePath, seq, startTime) {
			cases = append(cases, nestedSuite.Cases...)
		}
	}

	var fp *string
	if filePath != "" {
		f := filePath
		fp = &f
	}
	return []ExtractedSuite{{
		Title:     s.Title,
		FilePath:  fp,
		StartTime: startTime,
		Cases:     cases,
	}}
}

func extractCypressTest(t cypressTest, seq *int, startTime *time.Time) ExtractedCase {
	status := cypressStatus(t)
	var errMsg, errStack *string
	if t.Err != nil {
		msg := firstNonEmpty(t.Err.Message, t.Err.Estack)
		if msg != "" {
			errMsg = &msg
		}
		if t.Err.Estack != "" {
			stack := t.Err.Estack
			errStack = &stack
		}
	}
	full := t.FullTitle
	if full == "" {
		full = t.Title
	}
	c := ExtractedCase{
		Title:        t.Title,
		FullTitle:    full,
		Status:       status,
		DurationMs:   t.Duration,
		RetryCount:   0,
		ErrorMessage: errMsg,
		ErrorStack:   errStack,
		Sequence:     *seq,
		StartTime:    startTime,
		Attachments:  parseCypressContext(t.Context),
	}
	*seq++
	return c
}

func cypressStatus(t cypressTest) string {
	switch {
	case t.Pending || t.Skipped:
		return StatusSkipped
	case t.Fail:
		return StatusFailed
	case t.Pass:
		return StatusPassed
	}
	switch t.State {
	case StatusPassed:
		return StatusPassed
	case StatusFailed:
		return StatusFailed
	case cypressStatePending, StatusSkipped:
		return StatusSkipped
	default:
		return StatusPassed
	}
}

// parseCypressContext interprets the mocha "context" field — either a single
// {title, value} object or an array of them. The "value" is a screenshot path.
func parseCypressContext(ctx *string) []ExtractedAttachment {
	if ctx == nil || *ctx == "" {
		return nil
	}
	var raw any
	if err := json.Unmarshal([]byte(*ctx), &raw); err != nil {
		return nil
	}
	var out []ExtractedAttachment
	addOne := func(v any) {
		obj, ok := v.(map[string]any)
		if !ok {
			return
		}
		path, ok := obj["value"].(string)
		if !ok || path == "" {
			return
		}
		if decoded, err := url.QueryUnescape(path); err == nil {
			path = decoded
		}
		ct := "image/png"
		out = append(out, ExtractedAttachment{
			Path:        path,
			ContentType: &ct,
			Retry:       0,
			Sequence:    len(out),
		})
	}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			addOne(item)
		}
	default:
		addOne(raw)
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

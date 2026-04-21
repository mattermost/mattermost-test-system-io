package ingest

import (
	"encoding/json"
	"time"
)

type playwrightReport struct {
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSuite struct {
	Title  string            `json:"title"`
	File   string            `json:"file"`
	Specs  []playwrightSpec  `json:"specs"`
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSpec struct {
	Title string           `json:"title"`
	Tests []playwrightTest `json:"tests"`
}

type playwrightTest struct {
	ProjectName string                 `json:"projectName"`
	Results     []playwrightTestResult `json:"results"`
	Status      string                 `json:"status"`
}

type playwrightTestResult struct {
	Status      string                 `json:"status"`
	Duration    int64                  `json:"duration"`
	Errors      []playwrightError      `json:"errors"`
	Retry       int                    `json:"retry"`
	Attachments []playwrightAttachment `json:"attachments"`
	StartTime   string                 `json:"startTime"`
}

type playwrightError struct {
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

type playwrightAttachment struct {
	ContentType string `json:"contentType"`
	Path        string `json:"path"`
}

// extractPlaywright parses a Playwright JSON report and returns a flat slice
// of suites keyed by the leaf spec title. Nested describe blocks become
// ancestor prefixes in full_title.
func extractPlaywright(body []byte, seq *int) []ExtractedSuite {
	var r playwrightReport
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}
	var out []ExtractedSuite
	for _, s := range r.Suites {
		out = append(out, walkPlaywrightSuite(s, "", seq)...)
	}
	return out
}

func walkPlaywrightSuite(s playwrightSuite, ancestorPrefix string, seq *int) []ExtractedSuite {
	title := s.Title
	if title == "" {
		title = s.File
	}
	var filePath *string
	if s.File != "" {
		f := s.File
		filePath = &f
	}

	fullPrefix := title
	if ancestorPrefix != "" {
		fullPrefix = ancestorPrefix + " > " + title
	}

	var cases []ExtractedCase
	for _, spec := range s.Specs {
		for _, t := range spec.Tests {
			for _, res := range t.Results {
				tc := ExtractedCase{
					Title:      spec.Title,
					FullTitle:  combine(fullPrefix, spec.Title),
					Status:     mapPlaywrightStatus(res.Status, t.Status),
					DurationMs: res.Duration,
					RetryCount: res.Retry,
					Sequence:   *seq,
				}
				*seq++
				if len(res.Errors) > 0 {
					msg := res.Errors[0].Message
					if msg == "" {
						msg = res.Errors[0].Stack
					}
					if msg != "" {
						tc.ErrorMessage = &msg
					}
				}
				if res.StartTime != "" {
					if dt, err := time.Parse(time.RFC3339Nano, res.StartTime); err == nil {
						u := dt.UTC()
						tc.StartTime = &u
					}
				}
				for i, a := range res.Attachments {
					ct := a.ContentType
					tc.Attachments = append(tc.Attachments, ExtractedAttachment{
						Path:        a.Path,
						ContentType: &ct,
						Retry:       res.Retry,
						Sequence:    i,
					})
				}
				cases = append(cases, tc)
			}
		}
	}

	suite := ExtractedSuite{
		Title:    title,
		FilePath: filePath,
		Cases:    cases,
	}
	suite.StartTime = earliestStart(cases)

	out := []ExtractedSuite{}
	if len(cases) > 0 || len(s.Suites) == 0 {
		out = append(out, suite)
	}
	for _, nested := range s.Suites {
		out = append(out, walkPlaywrightSuite(nested, fullPrefix, seq)...)
	}
	return out
}

// mapPlaywrightStatus normalizes Playwright's per-result status to our enum.
// Playwright emits: passed, failed, skipped, timedOut, interrupted. A test
// whose spec-level status is "flaky" (passed after retry) overrides.
func mapPlaywrightStatus(resultStatus, testStatus string) string {
	if testStatus == StatusFlaky {
		return StatusFlaky
	}
	switch resultStatus {
	case "passed", "expected":
		return StatusPassed
	case "failed", "unexpected":
		return StatusFailed
	case StatusSkipped, cypressStatePending:
		return StatusSkipped
	case "timedOut":
		return StatusTimedOut
	case "interrupted":
		return StatusInterrupted
	default:
		return StatusPassed
	}
}

func combine(prefix, title string) string {
	if prefix == "" {
		return title
	}
	if title == "" {
		return prefix
	}
	return prefix + " > " + title
}

func earliestStart(cases []ExtractedCase) *time.Time {
	var earliest *time.Time
	for _, c := range cases {
		if c.StartTime == nil {
			continue
		}
		if earliest == nil || c.StartTime.Before(*earliest) {
			earliest = c.StartTime
		}
	}
	return earliest
}

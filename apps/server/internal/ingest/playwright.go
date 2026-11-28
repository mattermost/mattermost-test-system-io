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
	Tags   []string          `json:"tags"`
	Specs  []playwrightSpec  `json:"specs"`
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSpec struct {
	Title string           `json:"title"`
	Tags  []string         `json:"tags"`
	Tests []playwrightTest `json:"tests"`
}

type playwrightTest struct {
	ProjectName string                 `json:"projectName"`
	Tags        []string               `json:"tags"`
	Annotations []json.RawMessage      `json:"annotations"`
	Results     []playwrightTestResult `json:"results"`
	Status      string                 `json:"status"`
}

type playwrightTestResult struct {
	Status      string                 `json:"status"`
	Duration    int64                  `json:"duration"`
	Errors      []playwrightError      `json:"errors"`
	Error       *playwrightError       `json:"error"`
	Retry       int                    `json:"retry"`
	Annotations []json.RawMessage      `json:"annotations"`
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
		out = append(out, walkPlaywrightSuite(s, "", "", nil, seq)...)
	}
	return out
}

// walkPlaywrightSuite recurses Playwright's nested suite tree. ancestorFile
// carries the enclosing file path so nested describe-level suites inherit
// the spec file's identity — without this the describe-level Cases land in
// rows with FilePath=nil and the per-file dedup in MergeSuitesByFile cannot
// match them across multiple JSON uploads of the same spec.
func walkPlaywrightSuite(
	s playwrightSuite,
	ancestorPrefix, ancestorFile string,
	ancestorTags []string,
	seq *int,
) []ExtractedSuite {
	title := s.Title
	if title == "" {
		title = s.File
	}
	currentFile := ancestorFile
	if s.File != "" {
		currentFile = s.File
	}
	var filePath *string
	if currentFile != "" {
		f := currentFile
		filePath = &f
	}

	fullPrefix := title
	if ancestorPrefix != "" {
		fullPrefix = ancestorPrefix + " > " + title
	}
	suiteTags := mergeTags(ancestorTags, s.Tags)

	var cases []ExtractedCase
	for _, spec := range s.Specs {
		for _, t := range spec.Tests {
			var project *string
			if t.ProjectName != "" {
				p := t.ProjectName
				project = &p
			}
			tags := mergeTags(suiteTags, spec.Tags, t.Tags)
			for _, res := range t.Results {
				tc := ExtractedCase{
					Title:       spec.Title,
					FullTitle:   combine(fullPrefix, spec.Title),
					Status:      mapPlaywrightStatus(res.Status),
					Outcome:     playwrightOutcome(t.Status),
					DurationMs:  res.Duration,
					RetryCount:  res.Retry,
					Project:     project,
					Tags:        tags,
					Annotations: mergeAnnotations(t.Annotations, res.Annotations),
					Sequence:    *seq,
				}
				*seq++
				var pwErr *playwrightError
				if len(res.Errors) > 0 {
					pwErr = &res.Errors[0]
				} else if res.Error != nil {
					pwErr = res.Error
				}
				if pwErr != nil {
					if msg := firstNonEmpty(pwErr.Message, pwErr.Stack); msg != "" {
						tc.ErrorMessage = &msg
					}
					if pwErr.Stack != "" {
						stk := pwErr.Stack
						tc.ErrorStack = &stk
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
		out = append(out, walkPlaywrightSuite(nested, fullPrefix, currentFile, suiteTags, seq)...)
	}
	return out
}

func mergeTags(groups ...[]string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, g := range groups {
		for _, tag := range g {
			if tag == "" {
				continue
			}
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			out = append(out, tag)
		}
	}
	return out
}

func mergeAnnotations(groups ...[]json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	seen := map[string]struct{}{}
	for _, g := range groups {
		for _, raw := range g {
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil || v == nil {
				continue
			}
			canon, err := json.Marshal(v)
			if err != nil {
				continue
			}
			k := string(canon)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, canon)
		}
	}
	return out
}

// mapPlaywrightStatus normalizes Playwright's per-result status to our enum.
// Playwright emits: passed, failed, skipped, timedOut, interrupted. Each row is
// one attempt and keeps that attempt's own status, as Detox's retries do: a test
// that passed after retry is a failed row then a passed one, which
// countStatuses and the report rollups read as flaky.
func mapPlaywrightStatus(resultStatus string) string {
	switch resultStatus {
	case "passed", "expected":
		return StatusPassed
	case "failed", "unexpected":
		return StatusFailed
	case StatusFlaky:
		return StatusFlaky
	case StatusSkipped, cypressStatePending:
		return StatusSkipped
	case "timedOut":
		return StatusTimedOut
	case "interrupted":
		return StatusInterrupted
	default:
		return StatusFailed
	}
}

// playwrightOutcome keeps Playwright's test-level "flaky" verdict. The attempts
// alone can't always give it: a test.fail() test that passed and then failed is
// flaky to Playwright, though its last attempt failed.
func playwrightOutcome(testStatus string) string {
	if testStatus == StatusFlaky {
		return StatusFlaky
	}
	return ""
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

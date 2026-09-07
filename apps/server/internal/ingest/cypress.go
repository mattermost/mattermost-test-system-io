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
	// Attempts is mochawesome's per-attempt array, present when mocha
	// retries are on (cypress.config.ts sets retries.runMode: 1, so a
	// failing test in CI is always retried once). It holds every attempt
	// including the final one, so len(Attempts) == 1 means no retry
	// happened. Absent in reports from a run with retries disabled.
	Attempts []cypressAttempt `json:"attempts"`
}

// cypressAttempt is one entry of mochawesome's attempts array. Mochawesome
// clones the test object per attempt, so the outcome fields carry the same
// names; only the ones that identify an attempt's own result are read here.
type cypressAttempt struct {
	Duration int64         `json:"duration"`
	State    string        `json:"state"`
	Pass     bool          `json:"pass"`
	Fail     bool          `json:"fail"`
	Pending  bool          `json:"pending"`
	Skipped  bool          `json:"skipped"`
	Err      *cypressError `json:"err"`
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
				cases = append(cases, extractCypressTest(t, seq, startTime)...)
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
		cases = append(cases, extractCypressTest(t, seq, startTime)...)
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

// extractCypressTest returns one ExtractedCase per attempt, in attempt order,
// with the run-level rollup stamped across the set.
//
// Mochawesome reports the final state on the test object and the full attempt
// list under "attempts". Reading only the test object — as this did before —
// stored a single final-state row and a hardcoded retry_count of 0, so a
// Cypress test that failed once and passed on retry was indistinguishable
// from one that passed first try, while the same run under Playwright stored
// two rows. Any rate computed across the two frameworks was comparing a
// per-run number against a per-attempt one.
//
// When "attempts" is absent or empty the test is treated as a single attempt,
// which is what a report from a run with retries disabled looks like.
func extractCypressTest(t cypressTest, seq *int, startTime *time.Time) []ExtractedCase {
	full := t.FullTitle
	if full == "" {
		full = t.Title
	}
	// Screenshots are attached to the test, not to an attempt; hang them on
	// the final attempt so exactly one row owns them and the linker cannot
	// match the same file twice.
	attachments := parseCypressContext(t.Context)

	attempts := make([]ExtractedCase, 0, max(1, len(t.Attempts)))
	if len(t.Attempts) == 0 {
		attempts = append(attempts, ExtractedCase{
			Title:      t.Title,
			FullTitle:  full,
			Status:     cypressStatus(t),
			DurationMs: t.Duration,
			RetryCount: 0,
			StartTime:  startTime,
		})
		setCypressError(&attempts[0], t.Err)
	} else {
		for i, a := range t.Attempts {
			c := ExtractedCase{
				Title:      t.Title,
				FullTitle:  full,
				Status:     cypressAttemptStatus(a, t, i == len(t.Attempts)-1),
				DurationMs: a.Duration,
				RetryCount: i,
				StartTime:  startTime,
			}
			// An attempt carries its own err; fall back to the test's for the
			// final attempt, which is the one mochawesome copies it onto.
			e := a.Err
			if e == nil && i == len(t.Attempts)-1 {
				e = t.Err
			}
			setCypressError(&c, e)
			attempts = append(attempts, c)
		}
	}
	attempts[len(attempts)-1].Attachments = attachments
	stampRunRollup(attempts)
	for i := range attempts {
		attempts[i].Sequence = *seq
		*seq++
	}
	return attempts
}

// cypressAttemptStatus maps one attempt's own outcome. Mochawesome's attempt
// entries are clones of the test object, but older reporters emit them with
// the outcome flags unset; for the final attempt the test object's own state
// is authoritative, and a bare earlier attempt can only be a failure — an
// attempt is retried precisely because it did not pass.
func cypressAttemptStatus(a cypressAttempt, t cypressTest, isFinal bool) string {
	switch {
	case a.Pending || a.Skipped:
		return StatusSkipped
	case a.Fail:
		return StatusFailed
	case a.Pass:
		return StatusPassed
	}
	switch a.State {
	case StatusPassed:
		return StatusPassed
	case StatusFailed:
		return StatusFailed
	case cypressStatePending, StatusSkipped:
		return StatusSkipped
	}
	if isFinal {
		return cypressStatus(t)
	}
	return StatusFailed
}

func setCypressError(c *ExtractedCase, e *cypressError) {
	if e == nil {
		return
	}
	if msg := firstNonEmpty(e.Message, e.Estack); msg != "" {
		c.ErrorMessage = &msg
	}
	if e.Estack != "" {
		stack := e.Estack
		c.ErrorStack = &stack
	}
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

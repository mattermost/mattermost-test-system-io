package ingest

import (
	"encoding/xml"
	"slices"
	"strconv"
	"strings"
	"time"
)

type junitTestSuites struct {
	TestSuites []junitTestSuite `xml:"testsuite"`
	// Top-level stats (if present)
	Tests    string `xml:"tests,attr"`
	Failures string `xml:"failures,attr"`
	Errors   string `xml:"errors,attr"`
	Time     string `xml:"time,attr"`
	Name     string `xml:"name,attr"`
}

type junitTestSuite struct {
	Name       string           `xml:"name,attr"`
	Tests      string           `xml:"tests,attr"`
	Failures   string           `xml:"failures,attr"`
	Errors     string           `xml:"errors,attr"`
	Skipped    string           `xml:"skipped,attr"`
	Time       string           `xml:"time,attr"`
	Timestamp  string           `xml:"timestamp,attr"`
	TestCases  []junitTestCase  `xml:"testcase"`
	TestSuites []junitTestSuite `xml:"testsuite"` // nested suites
}

type junitTestCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure"`
	Error     *junitFailure `xml:"error"`
	Skipped   *junitSkipped `xml:"skipped"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

const junitRootSuiteTitle = "Root"

// extractMaestro parses a Maestro (or any standard JUnit XML) report and
// returns ExtractedSuite objects. JUnit XML is organized hierarchically by
// testsuites; we flatten nested suites into the parent-child structure
// expected by the consolidator.
func extractMaestro(body []byte, seq *int) []ExtractedSuite {
	// Try to unmarshal as root testsuites
	var root junitTestSuites
	if err := xml.Unmarshal(body, &root); err == nil && len(root.TestSuites) > 0 {
		var out []ExtractedSuite
		for _, suite := range root.TestSuites {
			out = append(out, extractTestSuite(&suite, seq, nil)...)
		}
		return out
	}

	// If that fails or is empty, try as a single testsuite
	var single junitTestSuite
	if err := xml.Unmarshal(body, &single); err != nil {
		return nil
	}
	if single.Name == "" {
		return nil
	}
	return extractTestSuite(&single, seq, nil)
}

// extractTestSuite recursively processes a single testsuite, handling both
// nested suites and testcases. ancestorPath tracks the hierarchy for FullTitle.
func extractTestSuite(suite *junitTestSuite, seq *int, ancestorPath []string) []ExtractedSuite {
	var out []ExtractedSuite

	// Build the full path including this suite's name
	fullPath := append(slices.Clone(ancestorPath), suite.Name)
	suiteTitle := strings.Join(fullPath, " > ")
	if suiteTitle == "" {
		suiteTitle = junitRootSuiteTitle
	}

	// Parse suite-level timestamp if present (for StartTime)
	var suiteStart *time.Time
	if suite.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, suite.Timestamp); err == nil {
			u := t.UTC()
			suiteStart = &u
		}
	}

	// Extract test cases from this suite
	var cases []ExtractedCase
	for _, tc := range suite.TestCases {
		status := StatusPassed
		var errMsg *string

		switch {
		case tc.Skipped != nil:
			status = StatusSkipped
			if tc.Skipped.Message != "" {
				errMsg = &tc.Skipped.Message
			}
		case tc.Failure != nil:
			status = StatusFailed
			msg := tc.Failure.Message
			if tc.Failure.Text != "" {
				if msg != "" {
					msg = msg + "\n" + strings.TrimSpace(tc.Failure.Text)
				} else {
					msg = strings.TrimSpace(tc.Failure.Text)
				}
			}
			if msg != "" {
				errMsg = &msg
			}
		case tc.Error != nil:
			status = StatusFailed
			msg := tc.Error.Message
			if tc.Error.Text != "" {
				if msg != "" {
					msg = msg + "\n" + strings.TrimSpace(tc.Error.Text)
				} else {
					msg = strings.TrimSpace(tc.Error.Text)
				}
			}
			if msg != "" {
				errMsg = &msg
			}
		}

		// Parse test duration (in seconds)
		var durationMs int64
		if tc.Time != "" {
			if d, err := strconv.ParseFloat(tc.Time, 64); err == nil {
				durationMs = int64(d * 1000)
			}
		}

		// Build full test title (suite hierarchy + test name)
		fullTitle := tc.Name
		if len(fullPath) > 0 && suiteTitle != junitRootSuiteTitle {
			fullTitle = suiteTitle + " > " + tc.Name
		}

		c := ExtractedCase{
			Title:        tc.Name,
			FullTitle:    fullTitle,
			Status:       status,
			DurationMs:   durationMs,
			ErrorMessage: errMsg,
			Sequence:     *seq,
			StartTime:    suiteStart,
		}
		*seq++
		cases = append(cases, c)
	}

	// Create a suite entry for the testcases at this level
	if len(cases) > 0 {
		out = append(out, ExtractedSuite{
			Title:     suiteTitle,
			StartTime: suiteStart,
			Cases:     cases,
		})
	}

	// Recursively process nested suites (passing current path as ancestors)
	for _, nested := range suite.TestSuites {
		nested := nested // copy to avoid reference issues
		out = append(out, extractTestSuite(&nested, seq, fullPath)...)
	}

	return out
}

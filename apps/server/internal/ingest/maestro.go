package ingest

import (
	"encoding/xml"
	"path"
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
	File      string        `xml:"file,attr"` // Maestro / mobile JUnit: flow path
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

type junitPendingCase struct {
	name string
	file string
	c    ExtractedCase
}

const junitRootSuiteTitle = "Root"

// extractMaestro parses a Maestro (or any standard JUnit XML) report and
// returns ExtractedSuite objects. JUnit XML is organized hierarchically by
// testsuites; we flatten nested suites into the parent-child structure
// expected by the consolidator.
//
// When testcases carry a `file` attribute (Mattermost mobile Maestro reports),
// suites are split per file and FilePath is populated so the web UI can show
// the flow path instead of "Missing file path".
func extractMaestro(body []byte, seq *int) []ExtractedSuite {
	// Try to unmarshal as root testsuites
	var root junitTestSuites
	if err := xml.Unmarshal(body, &root); err == nil && len(root.TestSuites) > 0 {
		var out []ExtractedSuite
		for _, suite := range root.TestSuites {
			out = append(out, extractTestSuite(&suite, seq, nil, nil)...)
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
	return extractTestSuite(&single, seq, nil, nil)
}

// extractTestSuite recursively processes a single testsuite, handling both
// nested suites and testcases. ancestorPath tracks the hierarchy for FullTitle.
// inheritedStart carries a parent suite timestamp to nested suites that omit one.
func extractTestSuite(suite *junitTestSuite, seq *int, ancestorPath []string, inheritedStart *time.Time) []ExtractedSuite {
	var out []ExtractedSuite

	// Build the full path including this suite's name
	fullPath := append(slices.Clone(ancestorPath), suite.Name)
	suiteTitle := strings.Join(fullPath, " > ")
	if suiteTitle == "" {
		suiteTitle = junitRootSuiteTitle
	}

	var suiteStart *time.Time
	if suite.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, suite.Timestamp); err == nil {
			u := t.UTC()
			suiteStart = &u
		}
	} else if inheritedStart != nil {
		suiteStart = inheritedStart
	}

	var pending []junitPendingCase
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

		var durationMs int64
		if tc.Time != "" {
			if d, err := strconv.ParseFloat(tc.Time, 64); err == nil {
				durationMs = int64(d * 1000)
			}
		}

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
		pending = append(pending, junitPendingCase{
			name: tc.Name,
			file: strings.TrimSpace(tc.File),
			c:    c,
		})
	}

	if len(pending) > 0 {
		out = append(out, groupJUnitCasesByFile(pending, suiteTitle, suiteStart)...)
	}

	for _, nested := range suite.TestSuites {
		nested := nested
		out = append(out, extractTestSuite(&nested, seq, fullPath, suiteStart)...)
	}

	return out
}

// groupJUnitCasesByFile splits cases that declare a `file` attribute into one
// ExtractedSuite per file (FilePath set). Cases without `file` stay under the
// parent suite title with FilePath nil — preserving plain JUnit behavior.
func groupJUnitCasesByFile(pending []junitPendingCase, suiteTitle string, suiteStart *time.Time) []ExtractedSuite {
	type bucket struct {
		file  string
		title string
		cases []ExtractedCase
	}

	var (
		order  []string
		byKey  = map[string]*bucket{}
		noFile []ExtractedCase
	)

	for _, p := range pending {
		if p.file == "" {
			noFile = append(noFile, p.c)
			continue
		}
		if _, ok := byKey[p.file]; !ok {
			order = append(order, p.file)
			byKey[p.file] = &bucket{
				file:  p.file,
				title: flowTitleFromFile(p.file, p.name),
			}
		}
		byKey[p.file].cases = append(byKey[p.file].cases, p.c)
	}

	var out []ExtractedSuite
	if len(noFile) > 0 {
		out = append(out, ExtractedSuite{
			Title:     suiteTitle,
			StartTime: suiteStart,
			Cases:     noFile,
		})
	}
	for _, key := range order {
		b := byKey[key]
		fp := b.file
		title := b.title
		if len(b.cases) > 1 {
			title = suiteTitle
		}
		out = append(out, ExtractedSuite{
			Title:     title,
			FilePath:  &fp,
			StartTime: suiteStart,
			Cases:     b.cases,
		})
	}
	return out
}

// flowTitleFromFile prefers the flow basename (sans extension); falls back to
// the testcase name when the path has no useful base.
func flowTitleFromFile(filePath, testName string) string {
	base := path.Base(filePath)
	base = strings.TrimSuffix(base, path.Ext(base))
	if base != "" && base != "." && base != "/" {
		return base
	}
	if testName != "" {
		return testName
	}
	return filePath
}

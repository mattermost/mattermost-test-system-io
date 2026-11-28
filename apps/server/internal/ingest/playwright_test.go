package ingest

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExtractPlaywright_RetryAttempts(t *testing.T) {
	type attempt struct {
		status string
		retry  int
		err    string
	}
	cases := []struct {
		name       string
		test       string
		attempts   []attempt
		wantCounts [4]int // passed, failed, flaky, unique
	}{
		{
			name:       "no retry",
			test:       `{"status":"expected","results":[{"status":"passed","retry":0}]}`,
			attempts:   []attempt{{"passed", 0, ""}},
			wantCounts: [4]int{1, 0, 0, 1},
		},
		{
			name:       "failed then passed on retry is flaky, and each attempt keeps its own status",
			test:       `{"status":"flaky","results":[{"status":"failed","retry":0,"errors":[{"message":"Error: first"}]},{"status":"passed","retry":1}]}`,
			attempts:   []attempt{{"failed", 0, "Error: first"}, {"passed", 1, ""}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "timed out then passed on retry is flaky",
			test:       `{"status":"flaky","results":[{"status":"timedOut","retry":0,"errors":[{"message":"Test timeout of 30000ms exceeded."}]},{"status":"passed","retry":1}]}`,
			attempts:   []attempt{{"timedOut", 0, "Test timeout of 30000ms exceeded."}, {"passed", 1, ""}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "a test.fail() test that passed then failed is flaky, as Playwright says",
			test:       `{"status":"flaky","expectedStatus":"failed","results":[{"status":"passed","retry":0},{"status":"failed","retry":1,"errors":[{"message":"Error: expected"}]}]}`,
			attempts:   []attempt{{"passed", 0, ""}, {"failed", 1, "Error: expected"}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "failed on every attempt is one failure",
			test:       `{"status":"unexpected","results":[{"status":"failed","retry":0,"errors":[{"message":"Error: first"}]},{"status":"failed","retry":1,"errors":[{"message":"Error: second"}]}]}`,
			attempts:   []attempt{{"failed", 0, "Error: first"}, {"failed", 1, "Error: second"}},
			wantCounts: [4]int{0, 1, 0, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			body := []byte(`{"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"t","tests":[` + tc.test + `]}]}]}`)
			suites := extractPlaywright(body, &seq)
			if len(suites) != 1 {
				t.Fatalf("got %d suites, want 1", len(suites))
			}
			got := suites[0].Cases
			if len(got) != len(tc.attempts) {
				t.Fatalf("got %d cases, want %d", len(got), len(tc.attempts))
			}
			for i, want := range tc.attempts {
				c := got[i]
				if c.Status != want.status || c.RetryCount != want.retry {
					t.Errorf("attempt %d: status %q retry %d, want %q retry %d", i, c.Status, c.RetryCount, want.status, want.retry)
				}
				if want.err != "" && (c.ErrorMessage == nil || *c.ErrorMessage != want.err) {
					t.Errorf("attempt %d: error %v, want %q", i, c.ErrorMessage, want.err)
				}
			}
			passed, failed, _, flaky, unique := countStatuses(got)
			if [4]int{passed, failed, flaky, unique} != tc.wantCounts {
				t.Errorf("counts passed/failed/flaky/unique = %d/%d/%d/%d, want %v", passed, failed, flaky, unique, tc.wantCounts)
			}
		})
	}
}

func TestCountStatuses_FlakyStatusWithoutOutcome(t *testing.T) {
	// A case stored as flaky with no Outcome (any extractor that still writes the
	// verdict into Status) is flaky, not passed.
	_, _, _, flaky, unique := countStatuses([]ExtractedCase{
		{Title: "t", FullTitle: "s > t", Status: StatusFlaky},
	})
	if flaky != 1 || unique != 1 {
		t.Fatalf("flaky=%d unique=%d, want 1 and 1", flaky, unique)
	}
}

func TestCountStatuses_CanonicalRules(t *testing.T) {
	passed, failed, skipped, flaky, unique := countStatuses([]ExtractedCase{
		{Title: "p", FullTitle: "s > p", Status: StatusPassed},
		{Title: "fl", FullTitle: "s > fl", Status: StatusPassed},
		{Title: "fl", FullTitle: "s > fl", Status: StatusFailed},
		{Title: "int", FullTitle: "s > int", Status: StatusInterrupted},
		{Title: "unk", FullTitle: "s > unk", Status: "unknown"},
		{Title: "pend", FullTitle: "s > pend", Status: "pending"},
		{Title: "notitle", FullTitle: "   ", Status: StatusPassed},
		{Title: "nostatus", FullTitle: "s > nostatus", Status: ""},
	})
	if passed != 1 || failed != 2 || skipped != 1 || flaky != 1 || unique != 5 {
		t.Fatalf("counts p=%d f=%d s=%d fl=%d u=%d, want 1/2/1/1/5", passed, failed, skipped, flaky, unique)
	}
	if got := mapPlaywrightStatus("pending"); got != StatusSkipped {
		t.Fatalf("mapPlaywrightStatus(pending) = %q, want skipped", got)
	}
	if got := mapPlaywrightStatus("unknown"); got != StatusFailed {
		t.Fatalf("mapPlaywrightStatus(unknown) = %q, want failed", got)
	}
	if got := detoxStatus("unknown"); got != StatusFailed {
		t.Fatalf("detoxStatus(unknown) = %q, want failed", got)
	}
	if got := cypressStatus(cypressTest{State: "unknown"}); got != StatusFailed {
		t.Fatalf("cypressStatus(unknown) = %q, want failed", got)
	}
}

func TestExtractPlaywright_MetadataAndErrors(t *testing.T) {
	body := []byte(`{
		"suites": [{
			"title": "auth.spec.ts",
			"file": "auth.spec.ts",
			"tags": ["@auth"],
			"suites": [{
				"title": "Login",
				"tags": ["@smoke", "@auth"],
				"specs": [{
					"title": "logs in",
					"tags": ["@fast", "@smoke"],
					"tests": [{
						"projectName": "chromium",
						"tags": ["@p0", "@fast"],
						"annotations": [
							{"type": "issue", "description": "SEC-1"},
							{"type": "slow"}
						],
						"status": "unexpected",
						"results": [
							{
								"status": "failed",
								"retry": 0,
								"annotations": [
									{"description": "SEC-1", "type": "issue"},
									{"type": "retry-note", "description": "first"}
								],
								"errors": [{"message": "boom", "stack": "Error: boom\n    at auth.spec.ts:10:3"}]
							},
							{
								"status": "failed",
								"retry": 1,
								"error": {"message": "singular boom", "stack": "Error: singular boom\n    at auth.spec.ts:12:3"}
							}
						]
					}]
				}]
			}]
		}]
	}`)

	seq := 0
	suites := extractPlaywright(body, &seq)
	if len(suites) != 1 || len(suites[0].Cases) != 2 {
		t.Fatalf("got %d suites, want 1 suite with 2 cases", len(suites))
	}

	wantTags := []string{"@auth", "@smoke", "@fast", "@p0"}
	for i, c := range suites[0].Cases {
		if c.Project == nil || *c.Project != "chromium" {
			t.Errorf("case %d: project = %v, want chromium", i, c.Project)
		}
		if !reflect.DeepEqual(c.Tags, wantTags) {
			t.Errorf("case %d: tags = %v, want %v", i, c.Tags, wantTags)
		}
	}

	c0 := suites[0].Cases[0]
	if len(c0.Annotations) != 3 {
		t.Errorf("case 0: annotations len = %d, want 3 (deduped test + result): %s", len(c0.Annotations), c0.Annotations)
	}
	if c0.ErrorMessage == nil || *c0.ErrorMessage != "boom" {
		t.Errorf("case 0: error_message = %v, want boom", c0.ErrorMessage)
	}
	if c0.ErrorStack == nil || *c0.ErrorStack != "Error: boom\n    at auth.spec.ts:10:3" {
		t.Errorf("case 0: error_stack = %v, want stack", c0.ErrorStack)
	}

	c1 := suites[0].Cases[1]
	if len(c1.Annotations) != 2 {
		t.Errorf("case 1: annotations len = %d, want 2", len(c1.Annotations))
	}
	if c1.ErrorMessage == nil || *c1.ErrorMessage != "singular boom" {
		t.Errorf("case 1: error_message = %v, want singular boom", c1.ErrorMessage)
	}
	if c1.ErrorStack == nil || *c1.ErrorStack != "Error: singular boom\n    at auth.spec.ts:12:3" {
		t.Errorf("case 1: error_stack = %v, want singular stack", c1.ErrorStack)
	}
}

func TestExtractPlaywright_MinimalBackwardCompat(t *testing.T) {
	seq := 0
	suites := extractPlaywright([]byte(`{"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"t","tests":[{"status":"expected","results":[{"status":"passed","retry":0}]}]}]}]}`), &seq)
	if len(suites) != 1 || len(suites[0].Cases) != 1 {
		t.Fatalf("got %d suites, want 1 suite with 1 case", len(suites))
	}
	c := suites[0].Cases[0]
	if c.Project != nil || len(c.Tags) != 0 || len(c.Annotations) != 0 || c.ErrorStack != nil {
		t.Errorf("unexpected metadata on minimal payload: %+v", c)
	}
}

func TestMergeAnnotations_SkipsNonObjects(t *testing.T) {
	got := mergeAnnotations(
		[]json.RawMessage{
			json.RawMessage(`null`),
			json.RawMessage(`"str"`),
			json.RawMessage(`123`),
			json.RawMessage(`true`),
			json.RawMessage(`["arr"]`),
			json.RawMessage(`{invalid`),
			json.RawMessage(`{"type":"trailing"}{"extra":1}`),
			json.RawMessage(`{"type":"issue","description":"SEC-1","id":9007199254740993}`),
		},
		[]json.RawMessage{
			json.RawMessage(`{"description":"SEC-1","id":9007199254740993,"type":"issue"}`),
			json.RawMessage(`{"type":"slow"}`),
		},
	)
	want := []string{
		`{"description":"SEC-1","id":9007199254740993,"type":"issue"}`,
		`{"type":"slow"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d (%s), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("got[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestMergeAnnotations_CanonicalNumberDedupAndRange(t *testing.T) {
	got := mergeAnnotations([]json.RawMessage{
		json.RawMessage(`{"id":1}`),
		json.RawMessage(`{"id":1.0}`),
		json.RawMessage(`{"id":1e0}`),
		json.RawMessage(`{"id":9007199254740993}`),
		json.RawMessage(`{"id":9007199254740992}`),
		json.RawMessage(`{"id":[{"n":1e200000}]}`),
	})
	want := []string{`{"id":1}`, `{"id":9007199254740993}`, `{"id":9007199254740992}`}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d (%s), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("got[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

package errorsig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type goldenCorpus struct {
	GoldenCases       []goldenCase       `json:"golden_cases"`
	ValidationSamples []validationSample `json:"validation_samples"`
}

type goldenCase struct {
	Name           string `json:"name"`
	Framework      string `json:"framework"`
	Status         string `json:"status"`
	ErrorMessage   string `json:"error_message"`
	ErrorStack     string `json:"error_stack"`
	WantErrorClass string `json:"want_error_class"`
	WantNormalized string `json:"want_normalized"`
	WantTopFrame   string `json:"want_top_frame"`
	WantFallback   string `json:"want_fallback"`
}

type validationSample struct {
	ID           string `json:"id"`
	Framework    string `json:"framework"`
	Status       string `json:"status"`
	Cluster      string `json:"cluster"`
	ErrorMessage string `json:"error_message"`
	ErrorStack   string `json:"error_stack"`
}

type validationMetrics struct {
	TotalSamples       int
	DistinctClusters   int
	DistinctSignatures int
	FalseSplits        int
	FalseMerges        int
}

func loadGoldenCorpus(t *testing.T) goldenCorpus {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "golden_corpus.json"))
	if err != nil {
		t.Fatalf("read golden_corpus.json: %v", err)
	}
	var c goldenCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal golden_corpus.json: %v", err)
	}
	return c
}

func TestGoldenCorpus(t *testing.T) {
	corpus := loadGoldenCorpus(t)
	if len(corpus.GoldenCases) == 0 {
		t.Fatal("expected non-empty golden_cases")
	}

	seenFrameworks := map[string]bool{}
	seenFallbacks := map[string]bool{}

	for _, tc := range corpus.GoldenCases {
		t.Run(tc.Name, func(t *testing.T) {
			seenFrameworks[tc.Framework] = true
			sig := Compute(Input{
				Framework:    tc.Framework,
				Status:       tc.Status,
				ErrorMessage: tc.ErrorMessage,
				ErrorStack:   tc.ErrorStack,
			})

			if sig.AlgoVersion != AlgoVersion {
				t.Fatalf("AlgoVersion = %d, want %d", sig.AlgoVersion, AlgoVersion)
			}
			if sig.ErrorClass != tc.WantErrorClass {
				t.Errorf("ErrorClass = %q, want %q", sig.ErrorClass, tc.WantErrorClass)
			}
			if sig.NormalizedMessage != tc.WantNormalized {
				t.Errorf("NormalizedMessage = %q, want %q", sig.NormalizedMessage, tc.WantNormalized)
			}
			if sig.TopFrame != tc.WantTopFrame {
				t.Errorf("TopFrame = %q, want %q", sig.TopFrame, tc.WantTopFrame)
			}
			if sig.Fallback != tc.WantFallback {
				t.Errorf("Fallback = %q, want %q", sig.Fallback, tc.WantFallback)
			}
			if len(sig.Fingerprint) != 64 {
				t.Errorf("Fingerprint length = %d, want 64 hex chars", len(sig.Fingerprint))
			}
			if utf8.RuneCountInString(sig.NormalizedMessage) > maxNormalizedRunes {
				t.Errorf("NormalizedMessage rune count exceeded %d", maxNormalizedRunes)
			}
			if sig.Fallback != "" {
				key := sig.Fallback
				if strings.HasPrefix(key, "no-error:") {
					key = "no-error"
				}
				seenFallbacks[key] = true
			}
		})
	}

	for _, fw := range []string{"playwright", "cypress", "detox", "maestro"} {
		if !seenFrameworks[fw] {
			t.Errorf("missing golden case for framework %q", fw)
		}
	}
	for _, fb := range []string{"spec-did-not-run", "interrupted", "timeout", "no-error"} {
		if !seenFallbacks[fb] {
			t.Errorf("missing golden case for fallback %q", fb)
		}
	}
}

func TestVersion1FingerprintEncodingPinned(t *testing.T) {
	// Pinned SHA-256 of "1|TimeoutError|TimeoutError: waiting for #submit-1 after <dur>"
	const wantFingerprint = "c2eb0c2f07af26d8414e9c5252f5795fb8063a86d3e6d5564d60795ec568d474"

	sig := Compute(Input{
		Framework:    "playwright",
		Status:       "failed",
		ErrorMessage: "TimeoutError: waiting for #submit-1 after 3000ms",
		ErrorStack:   "    at specs/login.spec.ts:10:2",
	})

	if sig.AlgoVersion != 1 {
		t.Fatalf("AlgoVersion = %d, want 1", sig.AlgoVersion)
	}
	if sig.ErrorClass != "TimeoutError" {
		t.Fatalf("ErrorClass = %q, want TimeoutError", sig.ErrorClass)
	}
	if sig.NormalizedMessage != "TimeoutError: waiting for #submit-1 after <dur>" {
		t.Fatalf("NormalizedMessage = %q", sig.NormalizedMessage)
	}
	if sig.TopFrame != "specs/login.spec.ts:10:2" {
		t.Fatalf("TopFrame = %q, want specs/login.spec.ts:10:2", sig.TopFrame)
	}
	if sig.Fingerprint != wantFingerprint {
		t.Fatalf("Fingerprint = %s, want pinned %s", sig.Fingerprint, wantFingerprint)
	}
}

func TestNormalizeMessageUTF8TruncationAndDeterminism(t *testing.T) {
	prefix := "Error: " + strings.Repeat("界", 520)
	got := NormalizeMessage(prefix)
	if !utf8.ValidString(got) {
		t.Fatalf("NormalizeMessage returned invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != maxNormalizedRunes {
		t.Fatalf("rune count = %d, want %d", n, maxNormalizedRunes)
	}

	in := Input{
		Framework:    "playwright",
		Status:       "failed",
		ErrorMessage: "Error: request to https://host-1.example/api failed for 550e8400-e29b-41d4-a716-446655440000",
		ErrorStack:   "    at specs/a.spec.ts:10:2",
	}
	s1 := Compute(in)
	in.ErrorMessage = "Error: request to https://host-2.example/other failed for 11111111-2222-3333-4444-555555555555"
	in.ErrorStack = "    at specs/b.spec.ts:99:1"
	s2 := Compute(in)
	if s1.Fingerprint != s2.Fingerprint {
		t.Errorf("expected same fingerprint across dynamic values, got %s vs %s", s1.Fingerprint, s2.Fingerprint)
	}
	if s1.SampleMessage == s2.SampleMessage {
		t.Errorf("expected SampleMessage to preserve raw first line, got identical %q", s1.SampleMessage)
	}
}

func TestTop50ValidationComparison(t *testing.T) {
	corpus := loadGoldenCorpus(t)
	if len(corpus.ValidationSamples) < 50 {
		t.Fatalf("validation_samples = %d, want >= 50", len(corpus.ValidationSamples))
	}

	withoutTF := evaluateCorpus(corpus.ValidationSamples, false)
	withTF := evaluateCorpus(corpus.ValidationSamples, true)

	t.Logf("samples=%d clusters=%d | without_top_frame: sigs=%d splits=%d merges=%d | with_top_frame: sigs=%d splits=%d merges=%d",
		withoutTF.TotalSamples, withoutTF.DistinctClusters,
		withoutTF.DistinctSignatures, withoutTF.FalseSplits, withoutTF.FalseMerges,
		withTF.DistinctSignatures, withTF.FalseSplits, withTF.FalseMerges,
	)

	// Record framework no-usable-error distribution for SEC-11524 validation report.
	noErrByFW := map[string]int{}
	totalByFW := map[string]int{}
	for _, s := range corpus.ValidationSamples {
		totalByFW[s.Framework]++
		sig := Compute(Input{
			Framework:    s.Framework,
			Status:       s.Status,
			ErrorMessage: s.ErrorMessage,
			ErrorStack:   s.ErrorStack,
		})
		if sig.Fallback != "" {
			noErrByFW[s.Framework]++
		}
	}
	t.Logf("framework totals=%v fallbacks=%v", totalByFW, noErrByFW)

	if withoutTF.FalseSplits != 0 {
		t.Errorf("without_top_frame false splits = %d, want 0", withoutTF.FalseSplits)
	}
	if withTF.FalseSplits <= withoutTF.FalseSplits {
		t.Errorf("expected with_top_frame to produce more false splits than without_top_frame (%d vs %d)",
			withTF.FalseSplits, withoutTF.FalseSplits)
	}
	if withoutTF.FalseSplits+withoutTF.FalseMerges >= withTF.FalseSplits+withTF.FalseMerges {
		t.Errorf("expected without_top_frame total clustering errors (%d) < with_top_frame (%d)",
			withoutTF.FalseSplits+withoutTF.FalseMerges, withTF.FalseSplits+withTF.FalseMerges)
	}
}

func evaluateCorpus(samples []validationSample, includeTopFrame bool) validationMetrics {
	clusterSigs := map[string]map[string]bool{}
	sigClusters := map[string]map[string]bool{}

	for _, s := range samples {
		sig := ComputeWithOptions(Input{
			Framework:    s.Framework,
			Status:       s.Status,
			ErrorMessage: s.ErrorMessage,
			ErrorStack:   s.ErrorStack,
		}, includeTopFrame)

		if clusterSigs[s.Cluster] == nil {
			clusterSigs[s.Cluster] = map[string]bool{}
		}
		clusterSigs[s.Cluster][sig.Fingerprint] = true

		if sigClusters[sig.Fingerprint] == nil {
			sigClusters[sig.Fingerprint] = map[string]bool{}
		}
		sigClusters[sig.Fingerprint][s.Cluster] = true
	}

	var falseSplits, falseMerges int
	for _, sigs := range clusterSigs {
		if len(sigs) > 1 {
			falseSplits += len(sigs) - 1
		}
	}
	for _, clusters := range sigClusters {
		if len(clusters) > 1 {
			falseMerges += len(clusters) - 1
		}
	}

	return validationMetrics{
		TotalSamples:       len(samples),
		DistinctClusters:   len(clusterSigs),
		DistinctSignatures: len(sigClusters),
		FalseSplits:        falseSplits,
		FalseMerges:        falseMerges,
	}
}

func TestEdgeCaseRegressions(t *testing.T) {
	const (
		errClassError   = "Error"
		testFrameA      = "specs/a.spec.ts:10:2"
		noErrPlaywright = "no-error:unknown:playwright"
	)

	t.Run("non_selector_brackets_and_unpaired_bracket", func(t *testing.T) {
		got := NormalizeMessage("Error: [worker 3] expected [10, 20] got [10, 30]")
		want := "Error: [worker <num>] expected [<num>, <num>] got [<num>, <num>]"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		got2 := NormalizeMessage(`Error: array[0 failed after 3000ms at [data-testid="x-1"]`)
		want2 := `Error: array[<num> failed after <dur> at [data-testid="x-1"]`
		if got2 != want2 {
			t.Errorf("got %q, want %q", got2, want2)
		}
	})

	t.Run("chained_css_selectors_preserved", func(t *testing.T) {
		got := NormalizeMessage("Error: element .foo-1.bar-2 not visible")
		want := "Error: element .foo-1.bar-2 not visible"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		got2 := NormalizeMessage(`Error: #id-1.cls-2[data-testid="x-3"].active-4 missing after 200ms`)
		want2 := `Error: #id-1.cls-2[data-testid="x-3"].active-4 missing after <dur>`
		if got2 != want2 {
			t.Errorf("got %q, want %q", got2, want2)
		}
	})

	t.Run("nested_parens_in_locators", func(t *testing.T) {
		got := NormalizeMessage("Error: locator('div:nth-child(2) > button.item-3') not found")
		want := "Error: locator('div:nth-child(2) > button.item-3') not found"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		got2 := NormalizeMessage("Error: getByRole('button', { name: 'Foo (1)' }) timed out after 5000ms")
		want2 := "Error: getByRole('button', { name: 'Foo (1)' }) timed out after <dur>"
		if got2 != want2 {
			t.Errorf("got %q, want %q", got2, want2)
		}
	})

	t.Run("frame_normalization_and_internal_runtime_filter", func(t *testing.T) {
		if got := ExtractTopFrame("    at Object.<anonymous> (/opt/actions/work/mattermost/mattermost/e2e/foo.spec.ts:10:2)", ""); got != "Object.<anonymous> (<repo>/e2e/foo.spec.ts:10:2)" {
			t.Errorf("runner prefix frame = %q", got)
		}
		if got := ExtractTopFrame("    at Context.eval (webpack:///./src/foo.ts:12:3)", ""); got != "Context.eval (src/foo.ts:12:3)" {
			t.Errorf("webpack triple-slash frame = %q", got)
		}
		if got := ExtractTopFrame("    at internal/main/run_main_module.js:17:47\n    at specs/my.spec.ts:10:1", ""); got != "specs/my.spec.ts:10:1" {
			t.Errorf("node internal frame not skipped: %q", got)
		}
		if got := ExtractTopFrame("    at apps/server/internal/errorsig/foo.go:42", ""); got != "apps/server/internal/errorsig/foo.go:42" {
			t.Errorf("app internal dir wrongly skipped: %q", got)
		}
	})

	t.Run("compound_durations", func(t *testing.T) {
		got := NormalizeMessage("TimeoutError: failed after 1m30s (total 2h5m)")
		want := "TimeoutError: failed after <dur> (total <dur>)"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("prose_id_normalized_while_selector_attrs_preserved", func(t *testing.T) {
		got := NormalizeMessage(`Error: failed for user id: "987654" in attempt 3 for id="row_9" testID="btn_1" accessibilityLabel="tab-2"`)
		want := `Error: failed for user id: "<num>" in attempt <num> for id="row_9" testID="btn_1" accessibilityLabel="tab-2"`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("bare_error_header_multiline", func(t *testing.T) {
		s1 := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "Error:\nConnection refused to http://127.0.0.1:8065\n    at specs/a.spec.ts:10:2",
		})
		s2 := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "Error:\nTimeout after 3000ms\n    at specs/b.spec.ts:20:3",
		})
		if s1.ErrorClass != errClassError || s1.NormalizedMessage != "Error: Connection refused to <url>" {
			t.Errorf("unexpected s1: class=%q norm=%q", s1.ErrorClass, s1.NormalizedMessage)
		}
		if s1.TopFrame != testFrameA {
			t.Errorf("unexpected s1.TopFrame = %q", s1.TopFrame)
		}
		if s1.Fingerprint == s2.Fingerprint {
			t.Errorf("unrelated bare Error: failures collided on fingerprint %s", s1.Fingerprint)
		}

		s3 := Compute(Input{
			Framework:    "detox",
			Status:       "failed",
			ErrorMessage: "Error\nExpected true, got false",
		})
		if s3.ErrorClass != errClassError || s3.NormalizedMessage != "Error: Expected true, got false" {
			t.Errorf("unexpected s3: class=%q norm=%q", s3.ErrorClass, s3.NormalizedMessage)
		}

		s4 := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "Error:\n    at " + testFrameA,
		})
		if s4.NormalizedMessage != "Error:" || s4.TopFrame != testFrameA {
			t.Errorf("unexpected s4: norm=%q frame=%q", s4.NormalizedMessage, s4.TopFrame)
		}
	})

	t.Run("nul_sentinel_collision_and_short_invalid_utf8", func(t *testing.T) {
		got := NormalizeMessage("Error: weird \x00SEL0\x00 in #btn-1")
		want := "Error: weird SEL0 in #btn-1"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		sig := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "Error: bad\xff\xfeutf8\x00",
			ErrorStack:   "    at specs/a.spec.ts:1:1\xff",
		})
		for _, field := range []string{sig.NormalizedMessage, sig.SampleMessage, sig.TopFrame} {
			if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
				t.Errorf("invalid UTF-8 or NUL in field %q", field)
			}
		}

		emptyInvalid := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "\xff\xfe",
		})
		if emptyInvalid.Fallback != noErrPlaywright {
			t.Errorf("Fallback = %q, want %s", emptyInvalid.Fallback, noErrPlaywright)
		}
	})

	t.Run("stack_only_uses_fallback_while_preserving_header_and_top_frame", func(t *testing.T) {
		timedOutSig := Compute(Input{
			Framework:  "playwright",
			Status:     "timedOut",
			ErrorStack: "    at " + testFrameA,
		})
		if timedOutSig.Fallback != fallbackTimeout || timedOutSig.NormalizedMessage != fallbackTimeout {
			t.Errorf("timedOut stack-only got fallback=%q norm=%q, want timeout", timedOutSig.Fallback, timedOutSig.NormalizedMessage)
		}
		if timedOutSig.TopFrame != testFrameA {
			t.Errorf("timedOut TopFrame = %q, want %s", timedOutSig.TopFrame, testFrameA)
		}

		interruptedSig := Compute(Input{
			Framework:  "cypress",
			Status:     "interrupted",
			ErrorStack: "    at " + testFrameA,
		})
		if interruptedSig.Fallback != "interrupted" || interruptedSig.NormalizedMessage != "interrupted" {
			t.Errorf("interrupted stack-only got fallback=%q norm=%q, want interrupted", interruptedSig.Fallback, interruptedSig.NormalizedMessage)
		}

		noErrA := Compute(Input{
			Framework:  "playwright",
			Status:     "failed",
			ErrorStack: "    at " + testFrameA,
		})
		noErrB := Compute(Input{
			Framework:  "playwright",
			Status:     "failed",
			ErrorStack: "specs/b.spec.ts:99:1",
		})
		if noErrA.Fallback != noErrPlaywright || noErrB.Fallback != noErrPlaywright {
			t.Errorf("unexpected fallbacks: %q, %q", noErrA.Fallback, noErrB.Fallback)
		}
		if noErrA.Fingerprint != noErrB.Fingerprint {
			t.Errorf("stack-only default fingerprint differed across filenames: %s vs %s", noErrA.Fingerprint, noErrB.Fingerprint)
		}
		if noErrA.TopFrame != testFrameA || noErrB.TopFrame != "specs/b.spec.ts:99:1" {
			t.Errorf("unexpected top frames: %q, %q", noErrA.TopFrame, noErrB.TopFrame)
		}

		withHeader := Compute(Input{
			Framework:  "playwright",
			Status:     "failed",
			ErrorStack: "Error: boom\n    at " + testFrameA,
		})
		if withHeader.ErrorClass != errClassError || withHeader.NormalizedMessage != "Error: boom" || withHeader.Fallback != "" {
			t.Errorf("stack header not preserved: class=%q norm=%q fallback=%q", withHeader.ErrorClass, withHeader.NormalizedMessage, withHeader.Fallback)
		}
		if withHeader.TopFrame != testFrameA {
			t.Errorf("TopFrame = %q, want %s", withHeader.TopFrame, testFrameA)
		}
	})

	t.Run("error_header_excluded_from_top_frame", func(t *testing.T) {
		if got := ExtractTopFrame("Error: Failed to load config.ts:12\n    at "+testFrameA, ""); got != testFrameA {
			t.Errorf("got %q, want %s", got, testFrameA)
		}
		if got := ExtractTopFrame("Error: Failed to load config.ts:12\nsrc/foo.ts:12:3", ""); got != "src/foo.ts:12:3" {
			t.Errorf("bare frame got %q, want src/foo.ts:12:3", got)
		}
		if got := ExtractTopFrame("Error: Failed to load config.ts:12", ""); got != "" {
			t.Errorf("header-only stack got %q, want empty", got)
		}
	})

	t.Run("windows_workspace_prefix_normalized_before_slashes", func(t *testing.T) {
		if got := ExtractTopFrame(`    at C:\a\repo\repo\specs\a.spec.ts:10:2`, ""); got != "<repo>/specs/a.spec.ts:10:2" {
			t.Errorf("windows frame = %q, want <repo>/specs/a.spec.ts:10:2", got)
		}
		if got := ExtractTopFrame(`    at Object.<anonymous> (C:\a\org\repo\specs\a.spec.ts:10:2)`, ""); got != "Object.<anonymous> (<repo>/specs/a.spec.ts:10:2)" {
			t.Errorf("windows callee frame = %q", got)
		}
	})

	t.Run("bare_absolute_frame_paths_with_spaces", func(t *testing.T) {
		const winFrame = `C:\Users\Jane Doe\project\specs\a.spec.ts:10:2`
		const wantWin = "C:/Users/Jane Doe/project/specs/a.spec.ts:10:2"
		if got := ExtractTopFrame(winFrame, ""); got != wantWin {
			t.Errorf("windows bare frame = %q, want %q", got, wantWin)
		}

		const posixFrame = "/Users/Jane Doe/project/specs/a.spec.ts:10:2"
		if got := ExtractTopFrame(posixFrame, ""); got != posixFrame {
			t.Errorf("posix bare frame = %q, want %q", got, posixFrame)
		}

		if got := ExtractTopFrame("file://"+posixFrame, ""); got != posixFrame {
			t.Errorf("file URI posix frame = %q, want %q", got, posixFrame)
		}
		if got := ExtractTopFrame("file:///C:/Users/Jane Doe/project/specs/a.spec.ts:10:2", ""); got != "/"+wantWin {
			t.Errorf("file URI windows frame = %q, want %q", got, "/"+wantWin)
		}

		if got := ExtractTopFrame("    at /Users/Jane Doe/actions/work/org/org/specs/a.spec.ts:10:2", ""); got != "<repo>/specs/a.spec.ts:10:2" {
			t.Errorf("work prefix with spaces = %q, want <repo>/specs/a.spec.ts:10:2", got)
		}
		if got := ExtractTopFrame("    at Object.<anonymous> (/Users/Jane Doe/actions/work/org/org/specs/a.spec.ts:10:2)", ""); got != "Object.<anonymous> (<repo>/specs/a.spec.ts:10:2)" {
			t.Errorf("callee work prefix with spaces = %q", got)
		}

		timedOutSig := Compute(Input{
			Framework:  "playwright",
			Status:     "timedOut",
			ErrorStack: winFrame,
		})
		if timedOutSig.Fallback != fallbackTimeout || timedOutSig.NormalizedMessage != fallbackTimeout {
			t.Errorf("timedOut bare windows frame got fallback=%q norm=%q, want timeout", timedOutSig.Fallback, timedOutSig.NormalizedMessage)
		}
		if timedOutSig.TopFrame != wantWin {
			t.Errorf("timedOut TopFrame = %q, want %q", timedOutSig.TopFrame, wantWin)
		}

		headerSig := Compute(Input{
			Framework:    "playwright",
			Status:       "failed",
			ErrorMessage: "Error:\n" + winFrame,
		})
		if headerSig.NormalizedMessage != "Error:" || headerSig.TopFrame != wantWin {
			t.Errorf("bare header + windows frame got norm=%q frame=%q", headerSig.NormalizedMessage, headerSig.TopFrame)
		}

		for _, prose := range []string{
			"Error: Failed to load config.ts:12",
			"Error: " + winFrame,
			"Failed to load specs/a.spec.ts:10:2",
			"// Failed to load config.ts:12",
			"/ Failed to load config.ts:12",
			`C:\ Failed to load config.ts:12`,
			"C: Failed to load config.ts:12",
			"/api/v1/users failed in config.ts:12",
			"/health check timed out in server.go:42",
			"/* comment mentioning config.ts:12",
			"/* see /foo/config.ts:12",
			"//comment/config.ts:12",
			"file:///api/v1/users failed in config.ts:12",
			"file://request failed in config.ts:12",
			`C:\Users\foo failed to load config.ts:12`,
			`C:\Users\Jane Doe\failed in config.ts:12`,
			"/api/v1/users failed in src/config.ts:12",
			"/api/v1/users failed while loading ./config.ts:12",
			"/foo/bar failed in baz/config.ts:12",
			"/var/log/test failed to import apps/server/config.go:42",
			"/GET /api/v1/users -> 500 in apps/api/handler.ts:42",
			`C:\Users\foo failed to load src\config.ts:12`,
			`C:\a (b failed in c\config.ts:12`,
			`C:\a (b failed in c)\d\config.ts:12`,
			`C:\Users\Jane Doe\something failed in src/config.ts:12`,
			"file://foo/bar failed in baz/config.ts:12",
			"file:///api/v1/users failed in src/config.ts:12",
			"/* see /foo/bar/config.ts:12",
			"// see /foo/bar/config.ts:12",
			"/ see /foo/bar/config.ts:12",
			`C:\Program Files (x86)\App\specs\a (1).spec.ts:10:2`,
		} {
			if got := ExtractTopFrame(prose, ""); got != "" {
				t.Errorf("prose %q wrongly matched as frame %q", prose, got)
			}
			if sig := Compute(Input{Framework: "playwright", Status: "failed", ErrorStack: prose}); sig.Fallback != "" {
				t.Errorf("prose %q wrongly collapsed to fallback %q", prose, sig.Fallback)
			}
		}
	})

	t.Run("dir_parens_and_unparenthesized_work_callees", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			in   string
			want string
		}{
			{"win_x86", `C:\Program Files (x86)\App\specs\a.spec.ts:10:2`, "C:/Program Files (x86)/App/specs/a.spec.ts:10:2"},
			{"win_leading_paren_dir", `C:\(x86)\App\specs\a.spec.ts:10:2`, "C:/(x86)/App/specs/a.spec.ts:10:2"},
			{"win_x86_cy", `C:\Program Files (x86)\App\test.cy.ts:1`, "C:/Program Files (x86)/App/test.cy.ts:1"},
			{"posix_paren", "/Users/Jane (test)/project/specs/a.spec.ts:10:2", "/Users/Jane (test)/project/specs/a.spec.ts:10:2"},
			{"win_x86_work", `C:\Program Files (x86)\actions\work\org\org\specs\a.spec.ts:10:2`, "<repo>/specs/a.spec.ts:10:2"},
			{"win_x86_callee_work", `    at Object.<anonymous> (C:\Program Files (x86)\actions\work\org\org\specs\a.spec.ts:10:2)`, "Object.<anonymous> (<repo>/specs/a.spec.ts:10:2)"},
			{"win_unparen_callee_work", `    at myFunc C:\Users\Jane Doe\actions\work\org\org\specs\a.spec.ts:10:2`, "myFunc <repo>/specs/a.spec.ts:10:2"},
			{"uri_unparen_callee_work", "    at myFunc file:///Users/Jane Doe/actions/work/org/org/specs/a.spec.ts:10:2", "myFunc <repo>/specs/a.spec.ts:10:2"},
			{"win_root_work_paren", `    at myFunc (C:\work\org\org\specs\a.spec.ts:10:2)`, "myFunc (<repo>/specs/a.spec.ts:10:2)"},
			{"posix_root_work_paren", "    at myFunc (/work/org/org/specs/a.spec.ts:10:2)", "myFunc (<repo>/specs/a.spec.ts:10:2)"},
			{"win_root_work_unparen", `    at myFunc C:\work\org\org\specs\a.spec.ts:10:2`, "myFunc <repo>/specs/a.spec.ts:10:2"},
			{"posix_root_work_unparen", "    at myFunc /work/org/org/specs/a.spec.ts:10:2", "myFunc <repo>/specs/a.spec.ts:10:2"},
			{"mixed_root_work_unparen", `    at myFunc /work/org/org\specs\a.spec.ts:10:2`, "myFunc <repo>/specs/a.spec.ts:10:2"},
			{"uri_win_runner_work", "    at myFunc (file:///C:/Users/runner/work/org/org/specs/a.spec.ts:10:2)", "myFunc (<repo>/specs/a.spec.ts:10:2)"},
			{"uri_posix_runner_work", "    at myFunc (file:///Users/runner/work/org/org/specs/a.spec.ts:10:2)", "myFunc (<repo>/specs/a.spec.ts:10:2)"},
			{"bracket_slash_callee_paren", "    at Object.fn [as /bar] (/Users/Jane/work/org/org/specs/a.spec.ts:10:2)", "Object.fn [as /bar] (<repo>/specs/a.spec.ts:10:2)"},
			{"bracket_slash_callee_unparen", "    at Object.fn [as /bar] /Users/Jane/work/org/org/specs/a.spec.ts:10:2", "Object.fn [as /bar] <repo>/specs/a.spec.ts:10:2"},
			{"nested_gh_work_not_rechopped", "    at myFunc (/home/runner/work/org/repo/packages/work/a/a/b.ts:10:2)", "myFunc (<repo>/packages/work/a/a/b.ts:10:2)"},
			{"nested_win_work_not_rechopped", `    at myFunc (C:\a\org\repo\packages\work\a\a\b.ts:10:2)`, "myFunc (<repo>/packages/work/a/a/b.ts:10:2)"},
			{"nested_custom_work_not_rechopped", "    at myFunc (/opt/actions/work/mattermost/mattermost/apps/work/a/a/b.ts:10:2)", "myFunc (<repo>/apps/work/a/a/b.ts:10:2)"},
			{"unrooted_relative_work_untouched", "    at myFunc (src/work/a/a/b.ts:10:2)", "myFunc (src/work/a/a/b.ts:10:2)"},
		} {
			if got := ExtractTopFrame(tc.in, ""); got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		}

		timedOutSig := Compute(Input{
			Framework:  "playwright",
			Status:     "timedOut",
			ErrorStack: `C:\Program Files (x86)\App\test.cy.ts:1`,
		})
		if timedOutSig.Fallback != fallbackTimeout || timedOutSig.NormalizedMessage != fallbackTimeout {
			t.Errorf("timedOut x86 frame got fallback=%q norm=%q, want timeout", timedOutSig.Fallback, timedOutSig.NormalizedMessage)
		}
		if timedOutSig.TopFrame != "C:/Program Files (x86)/App/test.cy.ts:1" {
			t.Errorf("timedOut TopFrame = %q", timedOutSig.TopFrame)
		}
	})
}

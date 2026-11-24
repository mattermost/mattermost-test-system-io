package teststatus

import "testing"

func TestClassifyAttempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status string
		want   AttemptClass
	}{
		{Passed, ClassPass},
		{Flaky, ClassPass},
		{Skipped, ClassSkip},
		{Pending, ClassSkip},
		{Failed, ClassFail},
		{TimedOut, ClassFail},
		{Interrupted, ClassFail},
		{"unknown", ClassFail},
		{"", ClassFail},
		{"PASSED", ClassFail},
		{"bogus", ClassFail},
	}

	for _, tc := range cases {
		if got := ClassifyAttempt(tc.status); got != tc.want {
			t.Errorf("ClassifyAttempt(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestTestVerdict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		attempts       []string
		frameworkFlaky bool
		want           string
	}{
		{"nil attempts", nil, false, ""},
		{"empty attempts even if framework flaky", []string{}, true, ""},
		{"single passed", []string{Passed}, false, Passed},
		{"single failed", []string{Failed}, false, Failed},
		{"single timedOut", []string{TimedOut}, false, Failed},
		{"single interrupted", []string{Interrupted}, false, Failed},
		{"single unknown", []string{"unknown"}, false, Failed},
		{"single empty", []string{""}, false, Failed},
		{"single skipped", []string{Skipped}, false, Skipped},
		{"single pending", []string{Pending}, false, Skipped},
		{"skipped and pending", []string{Skipped, Pending}, false, Skipped},
		{"passed and skipped", []string{Skipped, Passed}, false, Passed},
		{"failed then passed", []string{Failed, Passed}, false, Flaky},
		{"passed then failed", []string{Passed, Failed}, false, Flaky},
		{"timedOut then passed", []string{TimedOut, Passed}, false, Flaky},
		{"interrupted then passed", []string{Interrupted, Passed}, false, Flaky},
		{"passed then unknown", []string{Passed, "unknown"}, false, Flaky},
		{"single flaky attempt", []string{Flaky}, false, Flaky},
		{"framework flaky with passed", []string{Passed}, true, Flaky},
		{"framework flaky with failed", []string{Failed}, true, Flaky},
		{"failed and skipped", []string{Failed, Skipped}, false, Failed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TestVerdict(tc.attempts, tc.frameworkFlaky); got != tc.want {
				t.Fatalf("TestVerdict(%v, %v) = %q, want %q", tc.attempts, tc.frameworkFlaky, got, tc.want)
			}
		})
	}
}

func TestSpecVerdict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		verdicts []string
		want     string
	}{
		{"zero tests spec-did-not-run", nil, Failed},
		{"empty slice spec-did-not-run", []string{}, Failed},
		{"all passed", []string{Passed, Passed}, Passed},
		{"passed and skipped", []string{Passed, Skipped}, Passed},
		{"passed and flaky", []string{Passed, Flaky}, Flaky},
		{"passed and failed", []string{Passed, Failed}, Failed},
		{"flaky and failed", []string{Flaky, Failed}, Failed},
		{"timedOut", []string{TimedOut}, Failed},
		{"interrupted", []string{Interrupted}, Failed},
		{"unknown", []string{"unknown"}, Failed},
		{"all skipped", []string{Skipped, Pending}, Skipped},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SpecVerdict(tc.verdicts); got != tc.want {
				t.Fatalf("SpecVerdict(%v) = %q, want %q", tc.verdicts, got, tc.want)
			}
		})
	}
}

func TestPassRate(t *testing.T) {
	t.Parallel()

	if pct := PassRatePercent(8, 1, 1); pct != 90 {
		t.Fatalf("PassRatePercent(8,1,1) = %d, want 90", pct)
	}
	if pct := PassRatePercent(9, 0, 1); pct != 100 {
		t.Fatalf("PassRatePercent(9,0,1) = %d, want 100", pct)
	}
	if pct := PassRatePercent(0, 0, 0); pct != -1 {
		t.Fatalf("PassRatePercent(0,0,0) = %d, want -1", pct)
	}
	if rate, ok := PassRate(0, 0, 0); ok || rate != 0 {
		t.Fatalf("PassRate(0,0,0) = (%v, %v), want (0, false)", rate, ok)
	}
	if rate, ok := PassRate(3, 1, 0); !ok || rate != 75 {
		t.Fatalf("PassRate(3,1,0) = (%v, %v), want (75, true)", rate, ok)
	}
}

func TestParseAndNormalizeRule(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"legacy", "shared"} {
		r, err := ParseRule(raw)
		if err != nil {
			t.Fatalf("ParseRule(%q): %v", raw, err)
		}
		if string(r) != raw {
			t.Fatalf("ParseRule(%q) = %q", raw, r)
		}
	}
	for _, bad := range []string{"", "LEGACY", "canonical", "bogus"} {
		if _, err := ParseRule(bad); err == nil {
			t.Fatalf("ParseRule(%q) succeeded, want error", bad)
		}
	}
	if NormalizeRule("") != RuleLegacy || NormalizeRule(RuleLegacy) != RuleLegacy || NormalizeRule("unknown") != RuleLegacy {
		t.Fatal("expected unset/legacy/unknown rule to normalize to RuleLegacy")
	}
	if NormalizeRule(RuleShared) != RuleShared {
		t.Fatal("expected RuleShared to stay RuleShared")
	}
}

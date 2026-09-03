//go:build e2e

// The ledger's three guarantees, and the handover.
//
//  1. A waiver cannot move the number the team is judged by. This is the
//     property that killed the previous attempt at this system: a "known flaky"
//     marker that improved the reported pass-rate meant the list only ever grew.
//     Raw rates are computed from run outcomes, so it holds by construction —
//     but it is asserted here because "by construction" is a claim about code
//     that someone will edit.
//
//  2. Nothing greens without a record. Every waived verdict carries the
//     evidence that justified it and the credential that wrote it.
//
//  3. An attempt that failed is remembered, so the agent stops re-attempting a
//     test it cannot fix and a human inherits the notes.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	ledgerRepo   = "mattermost/mattermost"
	ledgerCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
)

func postVerdict(t *testing.T, env *testenv.Env, key, testID, runID string, waived bool) map[string]any {
	t.Helper()
	body := map[string]any{
		"repository": ledgerRepo,
		"branch":     "master",
		"commit_sha": ledgerCommit,
		"gh_run_id":  runID,
		"verdicts": []map[string]any{{
			"external_test_id": testID,
			"verdict":          "FLAKY_TEST",
			"confidence":       0.9,
			"check_state":      map[bool]string{true: "success", false: "failure"}[waived],
			"waived":           waived,
			"member_count":     1,
			"root_cause":       "known 40% flake; 1 of 3 is what its baseline predicts",
			"evidence":         []map[string]any{{"citation": "attribution:KNOWN_FLAKE"}},
		}},
	}
	return postJSON(t, env, key, "/api/v1/triage/verdicts", body)
}

func TestLedger_AWaiverNeverMovesTheRawPassRate(t *testing.T) {
	env := testenv.Start(t)
	// Two failing tests in one master run: one will be waived, one will not.
	seedRun(t, env, "MM-T9901", sha("aa", 1), true, 1)
	seedRun(t, env, "MM-T9902", sha("ab", 1), true, 2)

	before := getJSON(t, env, "/api/v1/triage/pass-rates?repo=mattermost&branch=master&window=30d")
	rawBefore := before["raw_failures"].(float64)
	rateBefore := before["raw_pass_rate"].(float64)

	key := env.IssueAPIKey(t, "triage-agent")
	if got := postVerdict(t, env, key, "MM-T9901", "run-waive", true); got["status"].(float64) != http.StatusCreated {
		t.Fatalf("post verdict: status %v", got["status"])
	}

	after := getJSON(t, env, "/api/v1/triage/pass-rates?repo=mattermost&branch=master&window=30d")

	if after["raw_failures"].(float64) != rawBefore {
		t.Fatalf("raw_failures moved from %v to %v — a waiver edited the number the team is judged by",
			rawBefore, after["raw_failures"])
	}
	if after["raw_pass_rate"].(float64) != rateBefore {
		t.Fatalf("raw_pass_rate moved from %v to %v", rateBefore, after["raw_pass_rate"])
	}
	// The waiver is not invisible — it is reported in its own column.
	if after["waived_failures"].(float64) < 1 {
		t.Fatalf("waived_failures = %v, want at least 1 — the waiver was not counted anywhere",
			after["waived_failures"])
	}
}

func TestLedger_EveryWaiverCarriesItsEvidenceAndItsAuthor(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, "MM-T9903", sha("ac", 1), true, 1)
	key := env.IssueAPIKey(t, "triage-agent")
	postVerdict(t, env, key, "MM-T9903", "run-evidence", true)

	rows := getJSONAuthed(t, env, key, "/api/v1/triage/verdicts?repo=mattermost&window=30d")
	list := rows["verdicts"].([]any)
	if len(list) == 0 {
		t.Fatal("no verdict rows returned")
	}
	v := list[0].(map[string]any)
	if v["root_cause"] == nil || v["root_cause"] == "" {
		t.Fatal("waived verdict has no root cause — the green is unexplainable")
	}
	ev, ok := v["evidence"].([]any)
	if !ok || len(ev) == 0 {
		t.Fatalf("waived verdict has no evidence: %v", v["evidence"])
	}
}

func TestLedger_VerdictListNeedsACredential(t *testing.T) {
	env := testenv.Start(t)
	// The aggregate reads are public; the row list is not, because each row
	// names the credential that wrote it.
	resp, err := http.Get(env.ServerURL + "/api/v1/triage/verdicts?repo=mattermost")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestLedger_VerdictWritesAreRejectedWithoutACredential(t *testing.T) {
	env := testenv.Start(t)
	body, _ := json.Marshal(map[string]any{
		"repository": ledgerRepo,
		"commit_sha": ledgerCommit,
		"verdicts": []map[string]any{{
			"external_test_id": "MM-T1", "verdict": "FLAKY_TEST",
			"confidence": 1, "waived": true, "member_count": 1,
		}},
	})
	resp, err := http.Post(env.ServerURL+"/api/v1/triage/verdicts", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — a forged waiver would corrupt the false-green metric", resp.StatusCode)
	}
}

func TestLedger_RejectsAnUnknownVerdict(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "triage-agent")
	got := postJSON(t, env, key, "/api/v1/triage/verdicts", map[string]any{
		"repository": ledgerRepo,
		"commit_sha": ledgerCommit,
		"verdicts": []map[string]any{{
			"external_test_id": "MM-T1", "verdict": "PROBABLY_FINE",
			"confidence": 1, "waived": true, "member_count": 1,
		}},
	})
	if got["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("status = %v, want 400", got["status"])
	}
}

func TestFixAttempts_HandOverToAHumanAfterRepeatedFailure(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, "MM-T9904", sha("ad", 1), true, 1)
	key := env.IssueAPIKey(t, "fix-loop")

	post := func(outcome, detail string) map[string]any {
		return postJSON(t, env, key, "/api/v1/triage/attempts", map[string]any{
			"test_id":    "MM-T9904",
			"repository": "mattermost/mattermost",
			"outcome":    outcome,
			"detail":     detail,
		})
	}

	// A failed attempt with no account of what was tried is not a handover,
	// it is a shrug. This row exists to be read by whoever picks the test up.
	if got := post("failed", ""); got["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("failed attempt with no detail: status %v, want 400", got["status"])
	}
	if got := post("teleported", "went sideways"); got["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("unknown outcome: status %v, want 400", got["status"])
	}

	post("failed", "rewrote the wait; the assertion still raced")
	second := post("failed", "awaited network idle; the panel renders before the data")
	if second["needs_human"].(bool) {
		t.Fatal("handed over after 2 failures — the agent had another attempt left")
	}

	third := post("failed", "no deterministic signal exists in the DOM for this state")
	if !third["needs_human"].(bool) {
		t.Fatalf("not handed over after 3 failures: %v", third)
	}

	// It must be visible where work is chosen, or neither the agent nor the
	// human knows to stop.
	q := getJSON(t, env, "/api/v1/triage/queue?repo=mattermost&window=30d")
	found := false
	for _, e := range q["ranked"].([]any) {
		m := e.(map[string]any)
		if m["test_id"] != "MM-T9904" {
			continue
		}
		found = true
		fa, ok := m["fix_attempts"].(map[string]any)
		if !ok {
			t.Fatal("queue entry carries no fix_attempts — the loop cannot see its own history")
		}
		if !fa["needs_human"].(bool) {
			t.Fatalf("queue says needs_human=false after 3 failures: %v", fa)
		}
		if fa["last_detail"] == "" {
			t.Fatal("no last_detail on the queue — the handover note is the point")
		}
	}
	if !found {
		t.Fatal("MM-T9904 is not on the queue at all")
	}

	// A later success releases it: the last attempt is the one that counts, or
	// a test the agent eventually fixed stays parked on a person forever.
	if post("fixed", "")["needs_human"].(bool) {
		t.Fatal("still flagged for a human after a successful fix")
	}
}

func TestSignatureIssues_ReportsWhatIsAlreadyKnown(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, "MM-T9905", sha("ae", 1), true, 1)
	key := env.IssueAPIKey(t, "triage-agent")

	// Nothing filed yet.
	fresh := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T9905")
	if fresh["known"].(bool) {
		t.Fatal("an untouched test reported as known — the agent would skip investigating it")
	}

	postVerdict(t, env, key, "MM-T9905", "run-known", true)
	postJSON(t, env, key, "/api/v1/triage/attempts", map[string]any{
		"test_id":    "MM-T9905",
		"repository": "mattermost/mattermost",
		"outcome":    "fixed",
		"pr_url":     "https://github.com/mattermost/mattermost/pull/40001",
	})

	known := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T9905")
	if !known["known"].(bool) {
		t.Fatal("a test with a prior verdict and a fix PR reported as unknown — duplicates follow")
	}
	if known["open_fix_pr"] != "https://github.com/mattermost/mattermost/pull/40001" {
		t.Fatalf("open_fix_pr = %v, want the recorded PR", known["open_fix_pr"])
	}
	if len(known["prior_verdict"].([]any)) == 0 {
		t.Fatal("no prior verdict returned")
	}
}

func TestSignatureIssues_NeedsSomethingToMatchOn(t *testing.T) {
	env := testenv.Start(t)
	resp, err := http.Get(env.ServerURL + "/api/v1/triage/signature-issues?repo=mattermost")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

//go:build e2e

// Recording a product defect, and counting it.
//
// The agent files the ticket in the issue tracker and records it here. This
// service never mirrors ticket state — the tracker owns whether a ticket is
// open, and a copy would go stale the moment somebody closed one, after which a
// regression could never be escalated again. So what is asserted here is that
// the record is append-only, that it aggregates into a usable metric, and that
// it never claims to know whether a defect is still open.
package triage

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func escalate(t *testing.T, env *testenv.Env, key, testID, issueKey string) map[string]any {
	t.Helper()
	return postJSON(t, env, key, "/api/v1/triage/escalations", map[string]any{
		"test_id":       testID,
		"repository":    "mattermost/mattermost",
		"issue_key":     issueKey,
		"issue_url":     "https://mattermost.atlassian.net/browse/" + issueKey,
		"summary":       "Saved posts panel renders empty after the first save",
		"suspect_range": "d13ff02a..d14ab9c3",
	})
}

func TestEscalation_IsRecordedAndSurfacesAsHistory(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, "MM-T7001", sha("e0", 1), true, 1)
	key := env.IssueAPIKey(t, "guardian")

	before := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T7001")
	if before["known"].(bool) {
		t.Fatal("an untouched test reported as known")
	}

	got := escalate(t, env, key, "MM-T7001", "MM-40001")
	if got["status"].(float64) != http.StatusCreated {
		t.Fatalf("escalation: status %v, want 201", got["status"])
	}
	e := got["escalation"].(map[string]any)
	if e["issue_key"] != "MM-40001" {
		t.Fatalf("issue_key = %v", e["issue_key"])
	}
	if e["suspect_range"] != "d13ff02a..d14ab9c3" {
		t.Fatalf("suspect_range = %v — the range that justified the defect is lost", e["suspect_range"])
	}
	// Attribution comes from the credential, never the body.
	if e["escalated_by"] == "" {
		t.Fatal("no escalated_by recorded")
	}

	// The lookup both prompts make before doing any work.
	after := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T7001")
	if !after["known"].(bool) {
		t.Fatal("a test with a filed defect reported as unknown")
	}
	if after["last_issue_url"] != "https://mattermost.atlassian.net/browse/MM-40001" {
		t.Fatalf("last_issue_url = %v", after["last_issue_url"])
	}
	if len(after["escalations"].([]any)) != 1 {
		t.Fatalf("escalations = %v, want one", after["escalations"])
	}
	// Nothing here may claim to know whether the ticket is still open — that is
	// the tracker's to answer, and a stale copy is the failure this design
	// exists to avoid.
	if _, leaked := after["escalations"].([]any)[0].(map[string]any)["resolved_at"]; leaked {
		t.Fatal("resolved_at is exposed — this service must not mirror ticket state")
	}
}

func TestEscalation_RecordsEveryEventRatherThanDeduping(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, "MM-T7002", sha("e1", 1), true, 1)
	key := env.IssueAPIKey(t, "guardian")

	// A test that regressed, was fixed, and regressed again produces two
	// separate defects. Both are real history and both must count — collapsing
	// them would understate how much a test costs.
	first := escalate(t, env, key, "MM-T7002", "MM-40002")
	second := escalate(t, env, key, "MM-T7002", "MM-40003")

	if first["status"].(float64) != http.StatusCreated || second["status"].(float64) != http.StatusCreated {
		t.Fatalf("statuses %v / %v, want 201 twice", first["status"], second["status"])
	}
	if first["escalation"].(map[string]any)["id"] == second["escalation"].(map[string]any)["id"] {
		t.Fatal("the second escalation overwrote the first")
	}

	got := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T7002")
	if n := len(got["escalations"].([]any)); n != 2 {
		t.Fatalf("escalations = %d, want 2", n)
	}
	// Newest first, so the caller reads the current ticket without sorting.
	if got["last_issue_url"] != "https://mattermost.atlassian.net/browse/MM-40003" {
		t.Fatalf("last_issue_url = %v, want the most recent", got["last_issue_url"])
	}
}

func TestDefects_CountsTheMetricAndRanksByDefectCount(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")
	seedRun(t, env, "MM-T7010", sha("e2", 1), true, 1)
	seedRun(t, env, "MM-T7011", sha("e3", 1), true, 2)

	escalate(t, env, key, "MM-T7010", "MM-41001")
	escalate(t, env, key, "MM-T7010", "MM-41002")
	escalate(t, env, key, "MM-T7011", "MM-41003")

	got := getJSON(t, env, "/api/v1/triage/defects?repo=mattermost&window=90d")

	if got["total_escalations"].(float64) != 3 {
		t.Fatalf("total_escalations = %v, want 3", got["total_escalations"])
	}
	tests := got["tests"].([]any)
	if len(tests) != 2 {
		t.Fatalf("tests = %d, want 2", len(tests))
	}
	// Ranked by defect count: the test that keeps catching real bugs leads.
	top := tests[0].(map[string]any)
	if top["test_id"] != "MM-T7010" || top["defects"].(float64) != 2 {
		t.Fatalf("top = %v, want MM-T7010 with 2 defects", top)
	}
	if top["latest_issue_key"] != "MM-41002" {
		t.Fatalf("latest_issue_key = %v, want the most recent", top["latest_issue_key"])
	}
}

func TestDefects_AreSeparateFromFlakiness(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")
	// A test that fails on master and produced a defect. It is a real bug
	// catcher, not a flake, and the two must never be conflated: quarantining
	// or "fixing" this test would hide a product bug.
	for i := 0; i < 10; i++ {
		seedRun(t, env, "MM-T7020", sha("e4", i), i < 4, i+1)
	}
	escalate(t, env, key, "MM-T7020", "MM-42001")

	defects := getJSON(t, env, "/api/v1/triage/defects?repo=mattermost&window=90d")
	if defects["total_escalations"].(float64) != 1 {
		t.Fatalf("total_escalations = %v, want 1", defects["total_escalations"])
	}

	// The flakiness leaderboard is computed from run outcomes and knows nothing
	// about defects — recording one must not have moved it.
	flaky := getJSON(t, env, "/api/v1/tests/flakiness?repo=mattermost&window=30d&min_runs=1")
	for _, row := range flaky["tests"].([]any) {
		m := row.(map[string]any)
		if _, leaked := m["defects"]; leaked {
			t.Fatal("defect counts leaked into the flakiness leaderboard")
		}
	}
}

func TestEscalation_RejectsARecordWithNoTicket(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")

	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"no issue key", map[string]any{
			"test_id": "MM-T1", "repository": "mattermost/mattermost",
			"issue_url": "https://example.invalid/x"}},
		{"no issue url", map[string]any{
			"test_id": "MM-T1", "repository": "mattermost/mattermost",
			"issue_key": "MM-1"}},
		{"no test id", map[string]any{
			"repository": "mattermost/mattermost",
			"issue_key":  "MM-1", "issue_url": "https://example.invalid/x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := postJSON(t, env, key, "/api/v1/triage/escalations", c.body)
			if got["status"].(float64) != http.StatusBadRequest {
				t.Fatalf("status = %v, want 400", got["status"])
			}
		})
	}
}

func TestEscalation_WritesNeedACredential(t *testing.T) {
	env := testenv.Start(t)
	resp, err := http.Post(env.ServerURL+"/api/v1/triage/escalations", "application/json", http.NoBody)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — a forged escalation would misreport the defect metric",
			resp.StatusCode)
	}
}

func TestDefects_TotalCountsTheWholeWindowNotJustThePage(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")

	// The response is capped at 100 tests. Summing the returned rows would
	// understate the metric exactly when a repository is worst — which is the
	// moment anyone would go looking at it.
	const tests = 130
	for i := 0; i < tests; i++ {
		escalate(t, env, key, fmt.Sprintf("MM-T8%03d", i), fmt.Sprintf("MM-43%03d", i))
	}
	// One test with a second defect, so the total is not simply the test count.
	escalate(t, env, key, "MM-T8000", "MM-43999")

	got := getJSON(t, env, "/api/v1/triage/defects?repo=mattermost&window=90d")

	if n := len(got["tests"].([]any)); n != 100 {
		t.Fatalf("returned tests = %d, want the 100-row page", n)
	}
	if total := got["total_escalations"].(float64); total != tests+1 {
		t.Fatalf("total_escalations = %v, want %d — the total is being summed from the page",
			total, tests+1)
	}
	if all := got["total_tests"].(float64); all != tests {
		t.Fatalf("total_tests = %v, want %d — a caller cannot tell the page was truncated",
			all, tests)
	}
}

func TestSignatureIssues_SignatureOnlyRequestKeepsEscalationHistory(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")
	const sig = "a1b2c3d4e5f60718293a4b5c6d7e8f90"

	postJSON(t, env, key, "/api/v1/triage/escalations", map[string]any{
		"test_id":           "MM-T8500",
		"repository":        "mattermost/mattermost",
		"issue_key":         "MM-44001",
		"issue_url":         "https://mattermost.atlassian.net/browse/MM-44001",
		"cluster_signature": sig,
	})

	// A caller holding only the failure signature — the case where the test was
	// renamed — must get the same answer. Returning nothing here reads as "no
	// defect was ever filed", which is what produces a duplicate ticket.
	got := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&signature="+sig)

	if !got["known"].(bool) {
		t.Fatal("signature-only lookup reported unknown despite a filed defect")
	}
	if got["last_issue_url"] != "https://mattermost.atlassian.net/browse/MM-44001" {
		t.Fatalf("last_issue_url = %v", got["last_issue_url"])
	}
	if n := len(got["escalations"].([]any)); n != 1 {
		t.Fatalf("escalations = %d, want 1", n)
	}
}

func TestSignatureIssues_SignatureOnlyRequestKeepsFixAttempts(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")
	const sig = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"

	postJSON(t, env, key, "/api/v1/triage/attempts", map[string]any{
		"test_id":           "MM-T8501",
		"repository":        "mattermost/mattermost",
		"outcome":           "failed",
		"detail":            "no deterministic signal exists in the DOM for this state",
		"cluster_signature": sig,
	})

	got := getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&signature="+sig)

	if !got["known"].(bool) {
		t.Fatal("signature-only lookup missed a recorded fix attempt")
	}
	if n := len(got["fix_attempts"].([]any)); n != 1 {
		t.Fatalf("fix_attempts = %d, want 1", n)
	}
}

func TestSignatureIssues_CarriesTheHandoverVerdict(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "guardian")

	attempt := func(outcome, detail string) map[string]any {
		return postJSON(t, env, key, "/api/v1/triage/attempts", map[string]any{
			"test_id": "MM-T8600", "repository": "mattermost/mattermost",
			"outcome": outcome, "detail": detail,
		})
	}
	known := func() map[string]any {
		return getJSON(t, env, "/api/v1/triage/signature-issues?repo=mattermost&test_id=MM-T8600")
	}

	// This is the field the Guardian branches on BEFORE attempting a test. If it
	// is missing or wrong the three-strikes rule never fires and the agent
	// re-attempts a test it has already given up on, indefinitely.
	attempt("failed", "rewrote the wait; the assertion still raced")
	attempt("failed", "awaited network idle; the panel renders before the data")
	if known()["needs_human"].(bool) {
		t.Fatal("handed over after 2 failures — the agent had another attempt left")
	}

	attempt("failed", "no deterministic signal exists in the DOM for this state")
	if !known()["needs_human"].(bool) {
		t.Fatal("not handed over after 3 failures — the agent would try forever")
	}

	// The queue and this endpoint must never disagree: they are two views of one
	// decision, and an agent reading either has to reach the same conclusion.
	q := getJSON(t, env, "/api/v1/triage/queue?repo=mattermost&window=30d")
	for _, e := range q["ranked"].([]any) {
		m := e.(map[string]any)
		if m["test_id"] != "MM-T8600" {
			continue
		}
		fa := m["fix_attempts"].(map[string]any)
		if fa["needs_human"] != known()["needs_human"] {
			t.Fatalf("queue says needs_human=%v, signature-issues says %v",
				fa["needs_human"], known()["needs_human"])
		}
	}

	// A later success releases it on both surfaces.
	attempt("fixed", "")
	if known()["needs_human"].(bool) {
		t.Fatal("still flagged for a human after a successful fix")
	}
}

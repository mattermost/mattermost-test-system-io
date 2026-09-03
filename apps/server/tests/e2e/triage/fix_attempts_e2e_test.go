//go:build e2e

// "The AI tries; a human takes it if that fails" — the handover, end to end.
//
// The attempt is cheap and bounded: the agent opens a PR, a human reviews, and
// six mechanical bans block the ways a flaky test is made to "pass" by hiding a
// real bug. What has to work is the OTHER half — that an unsuccessful attempt
// is remembered. Without that the loop picks the same unfixable test every
// cycle, spending its whole budget on a problem it has already failed, and
// whoever eventually takes the test starts from nothing.
//
// So this pins three things: attempts accumulate, the queue surfaces them where
// work is chosen, and a failure with no explanation is refused at the API.
package triage

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestFixAttemptsHandOffToAHumanAfterRepeatedFailure(t *testing.T) {
	env := testenv.Start(t)
	seedFailedCase(t, env, "fa")
	key := env.IssueAPIKey(t, "fix-loop")

	post := func(outcome, detail string) map[string]any {
		return postJSON(t, env, key, "/api/v1/triage/stabilization/attempts", map[string]any{
			"test_id":    "MM-T9901",
			"repository": "mattermost/mattermost",
			"outcome":    outcome,
			"detail":     detail,
		})
	}

	// A failed attempt with no account of what was tried is not a handover.
	// It is refused, because this row exists to be read by the next person.
	if got := post("failed", ""); got["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("failed attempt with no detail: status %v, want 400", got["status"])
	}
	if got := post("teleported", "went sideways"); got["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("unknown outcome: status %v, want 400", got["status"])
	}

	// Two failures: the agent should still be allowed to try again.
	first := post("failed", "rewrote the wait; the assertion still raced")
	if first["status"].(float64) != http.StatusCreated {
		t.Fatalf("first attempt: status %v", first["status"])
	}
	second := post("failed", "awaited the network idle; the panel renders before the data")
	if second["needs_human"].(bool) {
		t.Fatal("handed to a human after 2 failures — the agent had another attempt left")
	}

	// The third exhausts it. The test is now a person's problem.
	third := post("failed", "no deterministic signal exists in the DOM for this state")
	if !third["needs_human"].(bool) {
		t.Fatalf("still not handed over after 3 failures: %v", third)
	}
	if third["attempts"].(float64) != 3 {
		t.Fatalf("attempts = %v, want 3", third["attempts"])
	}

	// It has to be visible where work is chosen, not behind a second call —
	// otherwise the loop and the human both have to know to go looking.
	q := getJSONAuthed(t, env, key, "/api/v1/triage/stabilization/queue?repo=mattermost&window=30d")
	found := false
	for _, e := range q["ranked"].([]any) {
		m := e.(map[string]any)
		if m["test_id"] != "MM-T9901" {
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
		t.Fatal("MM-T9901 is not on the queue at all")
	}

	// A later success releases it: the last attempt is the one that counts, or
	// a test the agent eventually fixed would stay parked on a human forever.
	fixed := post("fixed", "")
	if fixed["needs_human"].(bool) {
		t.Fatal("still flagged for a human after a successful fix")
	}
}

func getJSONAuthed(t *testing.T, env *testenv.Env, key, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ServerURL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	return decodeJSON(t, resp)
}

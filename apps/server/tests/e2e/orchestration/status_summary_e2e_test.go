//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
)

// TestStatusSummaryView verifies the /orchestration/status view=summary
// variant: it returns the same run status + counts as the full view but omits
// the (potentially large) per-unit `units` array, while the default view keeps
// it. This is the payload the dashboard pollers and the summary CI action use
// to avoid downloading the whole snapshot every few seconds.
func TestStatusSummaryView(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{"tests/a.spec.ts", "tests/b.spec.ts", "tests/c.spec.ts"}
	created := postJSON(t, env, tok, "/api/v1/orchestration/begin", beginRunBody(units, nil))
	expectStatus(t, created, http.StatusCreated)

	// --- Default (full) view: units[] present, one entry per dispatch unit. ---
	full := getJSON(t, env, tok, statusURL(identity(nil)))
	fullBody := expectStatus(t, full, http.StatusOK)
	rawUnits, ok := fullBody["units"].([]any)
	if !ok {
		t.Fatalf("full view missing units[]: %v", fullBody["units"])
	}
	if len(rawUnits) != len(units) {
		t.Fatalf("full view units len = %d, want %d", len(rawUnits), len(units))
	}

	// --- Summary view: units[] omitted, status + counts still present. ---
	summary := getJSON(t, env, tok, statusURL(identity(nil))+"&view=summary")
	summaryBody := expectStatus(t, summary, http.StatusOK)
	if _, present := summaryBody["units"]; present {
		t.Fatalf("summary view must omit units[], got: %v", summaryBody["units"])
	}
	if summaryBody["status"] != fullBody["status"] {
		t.Fatalf("summary status = %v, want %v", summaryBody["status"], fullBody["status"])
	}
	fc := counts(t, fullBody)
	sc := counts(t, summaryBody)
	for _, k := range []string{"pending", "leased", "completed_pass", "completed_fail"} {
		if fc[k] != sc[k] {
			t.Fatalf("summary counts[%s] = %v, want %v (full)", k, sc[k], fc[k])
		}
	}
	if jsonNumberInt(t, summaryBody["total_units"]) != len(units) {
		t.Fatalf("summary total_units = %v, want %d", summaryBody["total_units"], len(units))
	}
}

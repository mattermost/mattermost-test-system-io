//go:build e2e

// W3 gates: the sampler stratifies and never shrinks below the pool; the AI
// verdict is absent from the sample payload (blindness enforced server-side);
// a submit records the review and the pooled + weekly agreement rates update;
// the reveal only appears after submit.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func seedWaivedVerdicts(t *testing.T, env *testenv.Env) {
	t.Helper()
	// 2 FLAKY_TEST + 1 MAIN_REGRESSION — a deliberately small pool (3 of 10).
	_, err := env.Pool.Exec(t.Context(), `
		INSERT INTO triage_verdicts
			(repository, branch, commit_sha, gh_run_id, external_test_id,
			 verdict, confidence, check_state, waived, evidence)
		VALUES
			('mattermost/mattermost', 'feat/x', '1111111111111111111111111111111111111111', 'w3-run-1', 'MM-T9901',
			 'FLAKY_TEST', 0.9, 'success', true, '[{"kind":"history"}]'),
			('mattermost/mattermost', 'feat/y', '2222222222222222222222222222222222222222', 'w3-run-2', 'MM-T9902',
			 'FLAKY_TEST', 0.88, 'success', true, '[{"kind":"screenshot"}]'),
			('mattermost/mattermost', 'main', '3333333333333333333333333333333333333333', 'w3-run-3', 'MM-T9903',
			 'MAIN_REGRESSION', 0.92, 'success', true, '[{"kind":"failing_on_baseline"}]')
	`)
	if err != nil {
		t.Fatalf("seed waived verdicts: %v", err)
	}
}

func TestBlindAuditSampleAndReview(t *testing.T) {
	env := testenv.Start(t)
	seedWaivedVerdicts(t, env)
	key := env.IssueAPIKey(t, "w3-auditor")

	// B7: the sample returns rows (verdict-adjacent data) — it is behind
	// RequireAuth now, and unauthenticated access must be refused.
	if unauth := getJSONStatus(t, env, "/api/v1/triage/audit/sample?repo=mattermost"); unauth != 401 {
		t.Fatalf("unauthenticated sample status = %d, want 401", unauth)
	}

	// Gate: sampler returns the whole small pool (3), records the denominator,
	// and does not error.
	sample := authedGet(t, env, key, "/api/v1/triage/audit/sample?repo=mattermost")
	items := sample["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("sampled %d items, want 3 (small pool takes all)", len(items))
	}
	if sample["pool_size"].(float64) != 3 {
		t.Fatalf("pool_size = %v, want 3", sample["pool_size"])
	}
	if sample["shortfall"].(float64) != 7 {
		t.Fatalf("shortfall = %v, want 7", sample["shortfall"])
	}

	// Gate: the AI verdict is absent from the API payload — assert on the raw
	// JSON keys, not the UI. B1 regression: stratum (a lowercase copy of the
	// verdict) and suspect_commit (present only for regression-class rows)
	// are leaks exactly like the verdict itself, and the ordering of a fixed
	// strata walk would leak by position — so both fields must be absent and
	// the classes must appear only as aggregate counts.
	for _, it := range items {
		m := it.(map[string]any)
		for _, banned := range []string{"verdict", "confidence", "root_cause", "model", "stratum", "suspect_commit"} {
			if _, present := m[banned]; present {
				t.Fatalf("sample item leaked %q — blindness must be enforced in the payload", banned)
			}
		}
	}
	counts, ok := sample["strata_counts"].(map[string]any)
	if !ok {
		t.Fatal("strata_counts aggregate missing from sample response")
	}
	if counts["flaky_test"].(float64) != 2 || counts["main_regression"].(float64) != 1 {
		t.Fatalf("strata_counts = %v, want flaky_test=2 main_regression=1", counts)
	}

	// Item detail before review: authenticated caller who has NOT submitted
	// gets no ai_verdict.
	first := items[0].(map[string]any)
	verdictID := first["verdict_id"].(string)
	detail := authedGet(t, env, key, "/api/v1/triage/audit/items/"+verdictID)
	if _, present := detail["ai_verdict"]; present {
		t.Fatal("item detail revealed ai_verdict before submit")
	}

	// Submit a blind call.
	agree := true
	body, _ := json.Marshal(map[string]any{
		"verdict_id":  verdictID,
		"human_agree": &agree,
		"note":        "e2e blind call",
	})
	req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/triage/audit/reviews", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit review: status %d", resp.StatusCode)
	}
	var submitted map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&submitted)
	if submitted["ai_verdict"] == nil {
		t.Fatal("submit response must reveal the AI verdict (the reveal)")
	}

	// Upsert: a second submit by the same reviewer corrects, not double-counts.
	req2, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/triage/audit/reviews", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-API-Key", key)
	resp2, _ := http.DefaultClient.Do(req2)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("re-submit review: status %d", resp2.StatusCode)
	}

	// Gate: pooled + weekly agreement rates reflect exactly one review.
	agreement := getJSON(t, env, "/api/v1/triage/audit/agreement?repo=mattermost&weeks=4")
	if agreement["reviews"].(float64) != 1 {
		t.Fatalf("agreement reviews = %v, want 1 (upsert, not append)", agreement["reviews"])
	}
	if agreement["audit_agreement_rate"].(float64) != 1 {
		t.Fatalf("agreement rate = %v, want 1", agreement["audit_agreement_rate"])
	}
	if len(agreement["per_week"].([]any)) != 1 {
		t.Fatalf("per_week entries = %d, want 1", len(agreement["per_week"].([]any)))
	}

	// After submit, the item detail now reveals the verdict to this reviewer.
	detail2 := authedGet(t, env, key, "/api/v1/triage/audit/items/"+verdictID)
	if detail2["ai_verdict"] == nil {
		t.Fatal("item detail must reveal ai_verdict after submit")
	}
}

func authedGet(t *testing.T, env *testenv.Env, key, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.ServerURL+path, nil)
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s: decode: %v", path, err)
	}
	return body
}

func getJSONStatus(t *testing.T, env *testenv.Env, path string) int {
	t.Helper()
	resp, err := http.Get(env.ServerURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

//go:build e2e

package contract

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestRunEvidenceContractLegacyRows(t *testing.T) {
	env := testenv.Start(t)
	group := env.DefaultReportGroup(t)
	if _, err := env.Pool.Exec(t.Context(), `UPDATE report_groups SET gh_run_id='123' WHERE id=$1`, group); err != nil {
		t.Fatal(err)
	}
	// Historical registration metadata can be any JSON, and attempt rollups,
	// file, project and error fields may be unknown. None confers source trust.
	_, err := env.Pool.Exec(t.Context(), `WITH r AS (
 INSERT INTO reports(report_group_id,name,registration_receipt)
 VALUES($1,'default','{"environment_metadata":["legacy"]}') RETURNING id
 ), s AS (
 INSERT INTO suites(report_id,title,ordinal) SELECT id,'legacy suite',0 FROM r RETURNING id
 ) INSERT INTO test_cases(suite_id,title,full_title,status,ordinal)
 SELECT id,'legacy test','legacy suite > legacy test','passed',0 FROM s`, group)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/triage/run-evidence?repository=mattermost/test&commit_sha=0000000000000000000000000000000000000000&gh_run_id=123&gh_run_attempt=1&name=default"
	status, headers, body := mustRecordedResponse(t, env, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	doc, opts := loadSpec(t)
	validateResponse(t, doc, opts, http.MethodGet, path, status, headers, body)
	var result struct {
		TrustedSource bool  `json:"trusted_source"`
		Complete      bool  `json:"complete"`
		Tests         []any `json:"tests"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.TrustedSource || result.Complete || len(result.Tests) != 1 {
		t.Fatalf("unexpected legacy evidence: %s", body)
	}
}

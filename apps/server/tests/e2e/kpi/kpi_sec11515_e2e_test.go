//go:build e2e
// +build e2e

package kpie2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	secMMRepo      = "mattermost/mattermost"
	secMobRepo     = "mattermost/mattermost-mobile"
	secDeskRepo    = "mattermost/desktop"
	secOtherRepo   = "mattermost/other-repo"
	secPWFramework = "playwright"
	secPWFullLane  = "playwright-full"
	secAppMM       = "mattermost"
	secAppMobile   = "mobile"
)

// TestSEC11515_AuthzMergedLineageAndRedactionAcrossAllAPIsAndSSR verifies:
//   - POST /api/v1/verdicts under TSIO_AUTHZ_MODE=enforce rejects anonymous (401),
//     viewer OIDC (403), and wrong-repo uploader OIDC (403), while accepting matching-repo uploader (200).
//   - merged_into_id lookup traversal across GET /api/v1/verdicts/by-signature/{fp} and /api/v1/kpi/failures*.
//   - error_context redaction in Postgres AND zero leakage (neither raw secrets, nor "error_context",
//     nor "[REDACTED]") across ALL read APIs and ALL SSR HTML pages.
func TestSEC11515_AuthzMergedLineageAndRedactionAcrossAllAPIsAndSSR(t *testing.T) {
	env := testenv.Start(
		t,
		testenv.WithAuthzMode(authapi.AuthzEnforce),
		testenv.WithHTMLView(),
		testenv.WithReaperDisabled(),
	)
	ctx := t.Context()

	env.InsertPolicy(t, "sec11515-uploader", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": "mattermost",
		"workflow":         "ci",
	})
	env.InsertPolicy(t, "sec11515-viewer", 2, string(policy.RoleViewer), map[string]string{
		"repository_owner": "mattermost",
		"workflow":         "viewer",
	})

	uploaderMM := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/mattermost:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      secMMRepo,
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})
	viewerMM := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/mattermost:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      secMMRepo,
		RepositoryOwner: "mattermost",
		Workflow:        "viewer",
		Ref:             "refs/heads/main",
	})
	uploaderOther := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/other-repo:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      secOtherRepo,
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})

	now := time.Now().UTC().Truncate(time.Second)
	testID := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:sec11515-redact", "Redact > login failure")

	fpCanon := strings.Repeat("a", 64)
	fpMid := strings.Repeat("b", 64)
	fpOld := strings.Repeat("c", 64)

	insertSigWithMerge := func(fp, msg string, mergedInto *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO failure_signatures (
				fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message,
				first_seen_at, merged_into_id
			) VALUES ($1, $2, 'TimeoutError', $3, 'e2e/redact.spec.ts:12', $3, $4, $5)
			RETURNING id
		`, fp, errorsig.AlgoVersion, msg, now.Add(-48*time.Hour), mergedInto).Scan(&id); err != nil {
			t.Fatalf("insert signature %s: %v", fp, err)
		}
		return id
	}
	sigCanon := insertSigWithMerge(fpCanon, "canonical login button timeout", nil)
	sigMid := insertSigWithMerge(fpMid, "mid login button timeout", &sigCanon)
	sigOld := insertSigWithMerge(fpOld, "old login button timeout", &sigMid)

	prNum := 777
	rgID := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-777", Commit: "sha-redact-777", RunID: "run-redact-777",
		Attempt: "1", PRNum: &prNum, Stage: "pr", CreatedAt: now.Add(-30 * time.Hour),
		DurMs: int64Ptr(45000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-redact-777", RunID: "run-redact-777",
		Name: secPWFullLane, Attempt: "1", Status: "completed", FailCount: 1, StartedAt: now.Add(-30 * time.Hour),
	})
	repID := insertReport(t, ctx, env.Pool, rgID, secPWFullLane, "")
	suiteID := insertSuite(t, ctx, env.Pool, repID, "Redact")
	insertCaseWithSig(t, ctx, env.Pool, suiteID, testID, sigOld, "login failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, rgID)

	const (
		secretAPIToken   = "ghp_Sec11515SentinelPAT9876543210abcd"
		secretAnthropic  = "sk-ant-Sec11515SentinelKey998877665544"
		secretEmail      = "sec11515-audit-user@mattermost.example"
		secretURLPass    = "Sec11515CredURLPass77"
		secretURLSig     = "Sec11515AmzSig66"
		secretRawKeyVal  = "Sec11515RawSecretKeyValue55"
		secretAuthHeader = "Sec11515BearerSecret44"
	)
	sentinels := []string{
		secretAPIToken,
		secretAnthropic,
		secretEmail,
		secretURLPass,
		secretURLSig,
		secretRawKeyVal,
		secretAuthHeader,
	}

	verdictPayload := map[string]any{
		"repository":      secMMRepo,
		"report_group_id": rgID.String(),
		"verdicts": []any{
			map[string]any{
				"test_id":      testID.String(),
				"signature_id": sigOld.String(),
				"label":        "flaky-infra",
				"decided_by":   "llm",
				"rule":         "sig-match",
				"confidence":   0.93,
				"summary":      "Login timeout caused by transient CI proxy drop",
				"model":        "claude-sonnet",
				"triage_url":   "https://github.com/mattermost/mattermost/actions/runs/777",
				"error_context": map[string]any{
					"api_token":      secretAPIToken,
					"anthropic_key":  secretAnthropic,
					"committer":      secretEmail,
					"authorization":  "Bearer " + secretAuthHeader,
					"artifact_url":   "https://ci:" + secretURLPass + "@minio.internal/logs/out.zip?X-Amz-Signature=" + secretURLSig,
					"raw_secret_key": secretRawKeyVal,
					"input_tokens":   321,
				},
			},
		},
	}

	// 1. Authz enforcement checks: 401 anonymous, 403 viewer, 403 wrong repo.
	if code, _ := postJSONStatus(t, env.ServerURL+"/api/v1/verdicts", "", verdictPayload); code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST /verdicts status = %d, want 401", code)
	}
	if code, _ := postJSONStatus(t, env.ServerURL+"/api/v1/verdicts", viewerMM, verdictPayload); code != http.StatusForbidden {
		t.Fatalf("viewer POST /verdicts status = %d, want 403", code)
	}
	if code, _ := postJSONStatus(t, env.ServerURL+"/api/v1/verdicts", uploaderOther, verdictPayload); code != http.StatusForbidden {
		t.Fatalf("wrong-repo POST /verdicts status = %d, want 403", code)
	}

	// 2. Authorized uploader succeeds (200) and refreshes KPI rollups.
	code, postBody := postJSONStatus(t, env.ServerURL+"/api/v1/verdicts", uploaderMM, verdictPayload)
	if code != http.StatusOK {
		t.Fatalf("authorized POST /verdicts status = %d: %s", code, postBody)
	}
	assertNoSecretOrContextLeak(t, "POST /api/v1/verdicts", string(postBody), sentinels, true)

	// 3. Assert redacted DB storage in verdicts.error_context.
	var storedErrCtx string
	if err := env.Pool.QueryRow(ctx, `
		SELECT error_context::text
		  FROM verdicts
		 WHERE report_group_id = $1 AND superseded_at IS NULL
	`, rgID).Scan(&storedErrCtx); err != nil {
		t.Fatalf("query stored verdicts.error_context: %v", err)
	}
	for _, s := range sentinels {
		if strings.Contains(storedErrCtx, s) {
			t.Fatalf("stored DB error_context leaked raw secret %q: %s", s, storedErrCtx)
		}
	}
	if !strings.Contains(storedErrCtx, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder in stored error_context, got %s", storedErrCtx)
	}
	if !strings.Contains(storedErrCtx, "321") {
		t.Fatalf("expected safe numeric field input_tokens=321 preserved in stored error_context, got %s", storedErrCtx)
	}

	// 4. Assert merged_into_id traversal + zero leakage across ALL read APIs.
	bySigBody := getRaw(t, env.ServerURL+"/api/v1/verdicts/by-signature/"+fpOld)
	assertNoSecretOrContextLeak(t, "GET /api/v1/verdicts/by-signature/{fpOld}", bySigBody, sentinels, true)
	var bySigResp struct {
		Fingerprint string `json:"fingerprint"`
		Label       string `json:"label"`
	}
	if err := json.Unmarshal([]byte(bySigBody), &bySigResp); err != nil {
		t.Fatalf("decode by-signature: %v", err)
	}
	if bySigResp.Fingerprint != fpOld && bySigResp.Fingerprint != fpCanon {
		t.Fatalf("unexpected fingerprint in by-signature response: %q", bySigResp.Fingerprint)
	}
	if bySigResp.Label != "flaky-infra" {
		t.Fatalf("by-signature label = %q, want flaky-infra", bySigResp.Label)
	}

	// Also query canonical fingerprint via merged_into_id lineage!
	byCanonBody := getRaw(t, env.ServerURL+"/api/v1/verdicts/by-signature/"+fpCanon)
	assertNoSecretOrContextLeak(t, "GET /api/v1/verdicts/by-signature/{fpCanon}", byCanonBody, sentinels, true)

	failuresBody := getRaw(t, env.ServerURL+"/api/v1/kpi/failures?window=30d")
	assertNoSecretOrContextLeak(t, "GET /api/v1/kpi/failures", failuresBody, sentinels, true)
	var failuresResp kpi.FailuresResponse
	if err := json.Unmarshal([]byte(failuresBody), &failuresResp); err != nil {
		t.Fatalf("decode /kpi/failures: %v", err)
	}
	if len(failuresResp.Failures) != 1 || failuresResp.Failures[0].SignatureID != sigCanon.String() {
		t.Fatalf("expected /kpi/failures to roll up sigOld into canonical %s, got %+v", sigCanon, failuresResp.Failures)
	}

	failOldDetailBody := getRaw(t, env.ServerURL+"/api/v1/kpi/failures/"+sigOld.String()+"?window=30d")
	assertNoSecretOrContextLeak(t, "GET /api/v1/kpi/failures/{sigOld}", failOldDetailBody, sentinels, true)
	var detailOld kpi.FailureDetailResponse
	if err := json.Unmarshal([]byte(failOldDetailBody), &detailOld); err != nil {
		t.Fatalf("decode /kpi/failures/{sigOld}: %v", err)
	}
	if detailOld.CanonicalSignatureID != sigCanon.String() || detailOld.LatestVerdict == nil || detailOld.LatestVerdict.Label != "flaky-infra" {
		t.Fatalf("unexpected detail for sigOld: canon=%s verdict=%+v", detailOld.CanonicalSignatureID, detailOld.LatestVerdict)
	}

	failCanonDetailBody := getRaw(t, env.ServerURL+"/api/v1/kpi/failures/"+sigCanon.String()+"?window=30d")
	assertNoSecretOrContextLeak(t, "GET /api/v1/kpi/failures/{sigCanon}", failCanonDetailBody, sentinels, true)

	testStateBody := getRaw(t, env.ServerURL+"/api/v1/tests/"+testID.String()+"/state")
	assertNoSecretOrContextLeak(t, "GET /api/v1/tests/{id}/state", testStateBody, sentinels, true)

	summaryBody := getRaw(t, env.ServerURL+"/api/v1/kpi/summary?window=30d")
	assertNoSecretOrContextLeak(t, "GET /api/v1/kpi/summary", summaryBody, sentinels, true)

	prsRedBody := getRaw(t, env.ServerURL+"/api/v1/kpi/prs/red?window=30d")
	assertNoSecretOrContextLeak(t, "GET /api/v1/kpi/prs/red", prsRedBody, sentinels, true)

	// 5. Assert zero leakage across ALL SSR HTML pages (/kpi, /kpi/prs, /kpi/failures/{id}).
	for _, ssrPath := range []string{
		"/kpi",
		"/kpi/prs",
		"/kpi/failures/" + sigOld.String(),
		"/kpi/failures/" + sigCanon.String(),
	} {
		htmlBody := getRaw(t, env.ServerURL+ssrPath)
		assertNoSecretOrContextLeak(t, "SSR "+ssrPath, htmlBody, sentinels, true)
	}
}

// TestSEC11515_TwoPlatformMobileCase verifies simultaneously across /api/v1/kpi/summary,
// /api/v1/kpi/flaky, /api/v1/tests/{id}/state, and SSR /kpi that:
//   - A shared test_id failing on android and passing on ios on the same commit_sha is NOT flaky across platforms.
//   - Mixed outcomes within the same (test_id, project, platform) across >=2 commits in trailing 14d
//     is flaky strictly for that (test_id, project, platform) tuple.
func TestSEC11515_TwoPlatformMobileCase(t *testing.T) {
	env := testenv.Start(t, testenv.WithHTMLView(), testenv.WithReaperDisabled())
	ctx := t.Context()

	now := time.Now().UTC().Truncate(time.Second)
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	day2 := todayMidnight.AddDate(0, 0, -2).Add(12 * time.Hour)
	day3 := todayMidnight.AddDate(0, 0, -3).Add(12 * time.Hour)
	day4 := todayMidnight.AddDate(0, 0, -4).Add(12 * time.Hour)

	tSplit := insertTest(t, ctx, env.Pool, secMobRepo, "detox", "h:mob-split-only", "Mobile > android fail ios pass")
	tAndroidFlaky := insertTest(t, ctx, env.Pool, secMobRepo, "detox", "h:mob-and-flaky", "Mobile > android flaky ios pass")

	// Backdate first_seen_at > 14d ago so tSplit classifies cleanly as "known" (not "new").
	if _, err := env.Pool.Exec(ctx, `
		UPDATE tests SET first_seen_at = $1 WHERE id IN ($2, $3)
	`, todayMidnight.AddDate(0, 0, -25), tSplit, tAndroidFlaky); err != nil {
		t.Fatalf("backdate mobile tests: %v", err)
	}

	// 3 commits in trailing 14d:
	// - tSplit: android=failed, ios=passed on every commit (same commit_sha, different platforms -> NEVER flaky!)
	// - tAndroidFlaky: ios=passed on all 3 commits; android=failed+passed on commits 0 & 1, passed on commit 2 -> flaky ONLY on (tAndroidFlaky, "detox-e2e", "android")!
	commits := []struct {
		sha      string
		day      time.Time
		andMixed bool
	}{
		{"mob-commit-1", day4, true},
		{"mob-commit-2", day3, true},
		{"mob-commit-3", day2, false},
	}

	for idx, c := range commits {
		pr := 500 + idx
		gid := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "detox", Name: "mobile-detox-pr", Status: "completed",
			Repo: secMobRepo, Branch: fmt.Sprintf("pr-%d", pr), Commit: c.sha,
			RunID: fmt.Sprintf("mob-run-%d", idx), Attempt: "1", PRNum: &pr, Stage: "pr", CreatedAt: c.day,
			DurMs: int64Ptr(120000),
		})
		rIOS := insertReport(t, ctx, env.Pool, gid, "detox-ios", "ios")
		sIOS := insertSuite(t, ctx, env.Pool, rIOS, "MobileIOS")
		insertCase(t, ctx, env.Pool, sIOS, tSplit, "android fail ios pass", "passed", 0, "detox-e2e", 0)
		insertCase(t, ctx, env.Pool, sIOS, tAndroidFlaky, "android flaky ios pass", "passed", 0, "detox-e2e", 1)

		rAnd := insertReport(t, ctx, env.Pool, gid, "detox-android", "android")
		sAnd := insertSuite(t, ctx, env.Pool, rAnd, "MobileAndroid")
		insertCase(t, ctx, env.Pool, sAnd, tSplit, "android fail ios pass", "failed", 0, "detox-e2e", 0)
		if c.andMixed {
			insertCase(t, ctx, env.Pool, sAnd, tAndroidFlaky, "android flaky ios pass", "failed", 0, "detox-e2e", 1)
			insertCase(t, ctx, env.Pool, sAnd, tAndroidFlaky, "android flaky ios pass", "passed", 1, "detox-e2e", 2)
		} else {
			insertCase(t, ctx, env.Pool, sAnd, tAndroidFlaky, "android flaky ios pass", "passed", 0, "detox-e2e", 1)
		}
		mustRefresh(t, ctx, env.Pool, gid)
	}

	// 1. GET /api/v1/kpi/summary?app=mobile&window=14d (and platform slices)
	sumAll := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?app=mobile&window=14d")
	if sumAll.FlakyTests.Count != 1 || len(sumAll.FlakyTests.ByApp) != 1 || sumAll.FlakyTests.ByApp[0].App != secAppMobile || sumAll.FlakyTests.ByApp[0].Count != 1 {
		t.Fatalf("mobile summary flaky = %+v, want count=1 for app=mobile", sumAll.FlakyTests)
	}
	sumAnd := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?app=mobile&platform=android&window=14d")
	if sumAnd.FlakyTests.Count != 1 {
		t.Fatalf("mobile android summary flaky count = %d, want 1", sumAnd.FlakyTests.Count)
	}
	sumIOS := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?app=mobile&platform=ios&window=14d")
	if sumIOS.FlakyTests.Count != 0 {
		t.Fatalf("mobile ios summary flaky count = %d, want 0", sumIOS.FlakyTests.Count)
	}

	// 2. GET /api/v1/kpi/flaky?app=mobile&window=14d
	flakyAll := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?app=mobile&window=14d")
	if flakyAll.TotalFlakyTests != 1 || len(flakyAll.Tests) != 1 {
		t.Fatalf("mobile /kpi/flaky = total:%d len:%d, want 1/1", flakyAll.TotalFlakyTests, len(flakyAll.Tests))
	}
	row := flakyAll.Tests[0]
	if row.TestID != tAndroidFlaky.String() ||
		row.Platform == nil || *row.Platform != "android" ||
		row.Project == nil || *row.Project != "detox-e2e" ||
		row.FlakyCommitCount != 2 || row.TotalCommitCount != 3 {
		t.Fatalf("unexpected flaky row: %+v", row)
	}
	flakyIOS := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?app=mobile&platform=ios&window=14d")
	if flakyIOS.TotalFlakyTests != 0 || len(flakyIOS.Tests) != 0 {
		t.Fatalf("mobile ios /kpi/flaky = %+v, want 0 rows", flakyIOS)
	}

	// 3. GET /api/v1/tests/{id}/state
	splitState := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tSplit.String()+"/state?app=mobile")
	if splitState.IsFlaky || splitState.FlakyCommitCount14d != 0 || splitState.State != kpi.TestStateKnown {
		t.Fatalf("tSplit state = %+v, want state=known is_flaky=false flaky_14d=0", splitState)
	}
	andFlakyState := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tAndroidFlaky.String()+"/state?app=mobile")
	if !andFlakyState.IsFlaky || andFlakyState.FlakyCommitCount14d != 2 || andFlakyState.State != kpi.TestStateFlaky {
		t.Fatalf("tAndroidFlaky state = %+v, want state=flaky is_flaky=true flaky_14d=2", andFlakyState)
	}
	andFlakyIOSState := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tAndroidFlaky.String()+"/state?app=mobile&platform=ios")
	if andFlakyIOSState.IsFlaky || andFlakyIOSState.FlakyCommitCount14d != 0 || andFlakyIOSState.State != kpi.TestStateKnown {
		t.Fatalf("tAndroidFlaky on ios state = %+v, want state=known is_flaky=false flaky_14d=0", andFlakyIOSState)
	}

	// 4. SSR GET /kpi?app=mobile&window=14d
	mobileHTML := getRaw(t, env.ServerURL+"/kpi?app=mobile&window=14d")
	for _, want := range []string{`mobile: 1`, `Trailing 14d`} {
		if !strings.Contains(mobileHTML, want) {
			t.Fatalf("SSR /kpi?app=mobile&window=14d missing %q", want)
		}
	}
	iosHTML := getRaw(t, env.ServerURL+"/kpi?app=mobile&platform=ios&window=14d")
	if strings.Contains(iosHTML, `mobile: 1`) {
		t.Fatalf("SSR /kpi?app=mobile&platform=ios&window=14d must not show flaky mobile: 1")
	}
}

// TestSEC11515_RealOrchestrationRetestChainAndPRFalseRedAttribution tests end-to-end HTTP
// orchestration (/begin, /checkout, /complete) + stateless upload (/reports/begin, /register, /upload)
// + rollup refresh for:
//   - fresh vs reclaim vs retest leases on main first-attempt pass rate (KPI 1)
//   - gh_run_attempt=1 vs rerun (attempt=2) going green
//   - timed_out lane handling
//   - new-commit fix exclusion
//   - false-red attribution priority (went_green_on_rerun > failing_elsewhere [main or >=3 PRs in 24h] > other)
//   - verified via /api/v1/kpi/summary, /api/v1/kpi/prs/red, and SSR /kpi/prs.
func TestSEC11515_RealOrchestrationRetestChainAndPRFalseRedAttribution(t *testing.T) {
	env := testenv.Start(
		t,
		testenv.WithAuthzMode(authapi.AuthzEnforce),
		testenv.WithHTMLView(),
		testenv.WithReaperDisabled(),
	)
	ctx := t.Context()

	env.InsertPolicy(t, "sec11515-orch-uploader", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": "mattermost",
		"workflow":         "ci",
	})
	tok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/mattermost:ref:refs/heads/main",
		Audience:        "tsio",
		Repository:      secMMRepo,
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})

	now := time.Now().UTC().Truncate(time.Second)
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	settledTime := todayMidnight.AddDate(0, 0, -2).Add(12 * time.Hour)

	const (
		errMainOutage = "TimeoutError: main server gateway 502 after 5000ms"
		errMultiPR    = "Error: shared fixture cdn 503 unavailable"
		errUnique     = "AssertionError: pr407 isolated failure"
	)

	makePWSpecJSON := func(title, status string, retry int, errMsg, stack string) map[string]any {
		res := map[string]any{
			"status":   status,
			"duration": 120,
			"retry":    retry,
		}
		if errMsg != "" {
			res["errors"] = []any{map[string]any{"message": errMsg, "stack": stack}}
		}
		return res
	}
	buildPWPayload := func(specFile, title string, results []map[string]any) string {
		hasFail := false
		hasPass := false
		for _, r := range results {
			if r["status"] == "passed" {
				hasPass = true
			} else if r["status"] == "failed" {
				hasFail = true
			}
		}
		testStatus := "expected"
		if hasFail && hasPass {
			testStatus = "flaky"
		} else if hasFail {
			testStatus = "unexpected"
		}
		b, err := json.Marshal(map[string]any{
			"suites": []any{
				map[string]any{
					"title": specFile,
					"file":  specFile,
					"specs": []any{
						map[string]any{
							"title": title,
							"tests": []any{
								map[string]any{
									"projectName": "chromium",
									"status":      testStatus,
									"results":     results,
								},
							},
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal pw payload: %v", err)
		}
		return string(b)
	}

	uploadShard := func(repo, lane, branch, commit, runID, attempt, stage string, prNum *int, jobID, pwJSON string) uuid.UUID {
		t.Helper()
		beginReq := map[string]any{
			"repository":             repo,
			"commit":                 commit,
			"gh_run_id":              runID,
			"gh_run_attempt":         attempt,
			"framework":              secPWFramework,
			"name":                   lane,
			"branch":                 branch,
			"stage":                  stage,
			"total_reports_expected": 1,
		}
		if prNum != nil {
			beginReq["gh_pr_number"] = *prNum
		}
		code, bBody := postJSONStatus(t, env.ServerURL+"/api/v1/reports/begin", tok, beginReq)
		if code != http.StatusOK {
			t.Fatalf("reports/begin %s/%s: %d %s", lane, runID, code, bBody)
		}
		var bResp struct {
			ReportID string `json:"report_id"`
		}
		if err := json.Unmarshal(bBody, &bResp); err != nil {
			t.Fatalf("decode begin: %v", err)
		}
		regReq := map[string]any{
			"repository":     repo,
			"commit":         commit,
			"gh_run_id":      runID,
			"gh_run_attempt": attempt,
			"framework":      secPWFramework,
			"name":           lane,
			"branch":         branch,
			"stage":          stage,
			"gh_job_id":      jobID,
			"gh_job_name":    lane,
			"json_files":     []any{map[string]any{"path": "results.json", "size": len(pwJSON)}},
			"screenshots":    []any{},
		}
		if prNum != nil {
			regReq["gh_pr_number"] = *prNum
		}
		code, rBody := postJSONStatus(t, env.ServerURL+"/api/v1/reports/register", tok, regReq)
		if code != http.StatusOK {
			t.Fatalf("reports/register %s/%s: %d %s", lane, runID, code, rBody)
		}
		var rResp struct {
			ReportID string `json:"report_id"`
			UploadID string `json:"upload_id"`
		}
		if err := json.Unmarshal(rBody, &rResp); err != nil {
			t.Fatalf("decode register: %v", err)
		}

		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		part, err := mw.CreateFormFile("files", "results.json")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte(pwJSON)); err != nil {
			t.Fatalf("write multipart: %v", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("close multipart: %v", err)
		}
		upReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/json", env.ServerURL, rResp.ReportID, rResp.UploadID),
			buf)
		if err != nil {
			t.Fatalf("new upload req: %v", err)
		}
		upReq.Header.Set("Authorization", "Bearer "+tok)
		upReq.Header.Set("Content-Type", mw.FormDataContentType())
		upResp, err := http.DefaultClient.Do(upReq)
		if err != nil {
			t.Fatalf("upload json: %v", err)
		}
		upBody, readErr := io.ReadAll(upResp.Body)
		upResp.Body.Close()
		if readErr != nil {
			t.Fatalf("read upload response %s/%s: %v", lane, runID, readErr)
		}
		if upResp.StatusCode != http.StatusOK {
			t.Fatalf("upload %s/%s: %d %s", lane, runID, upResp.StatusCode, upBody)
		}

		gid := uuid.MustParse(bResp.ReportID)
		deadline := time.Now().Add(15 * time.Second)
		var lastStatus string
		var lastErr error
		for time.Now().Before(deadline) {
			lastErr = env.Pool.QueryRow(ctx, `SELECT status FROM report_groups WHERE id = $1`, gid).Scan(&lastStatus)
			if lastErr == nil && lastStatus == "completed" {
				return gid
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("report_group %s (%s/%s) did not reach completed within 15s (last status=%q, err=%v)", gid, lane, runID, lastStatus, lastErr)
		return uuid.Nil
	}

	beginOrch := func(lane, branch, commit, runID, attempt string, units []string, retest bool) uuid.UUID {
		t.Helper()
		du := make([]map[string]any, 0, len(units))
		for _, u := range units {
			du = append(du, map[string]any{"spec_path": u})
		}
		code, body := postJSONStatus(t, env.ServerURL+"/api/v1/orchestration/begin", tok, map[string]any{
			"repository":             secMMRepo,
			"commit_sha":             commit,
			"gh_run_id":              runID,
			"name":                   lane,
			"gh_run_attempt":         attempt,
			"branch":                 branch,
			"framework":              secPWFramework,
			"total_reports_expected": 1,
			"retest_on_fail":         retest,
			"retest_budget":          1,
			"lease_timeout_ms":       60_000,
			"idle_timeout_ms":        60_000,
			"dispatch_units":         du,
		})
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("orchestration/begin %s/%s: %d %s", lane, runID, code, body)
		}
		var out struct {
			RunID uuid.UUID `json:"run_id"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode orch begin: %v", err)
		}
		return out.RunID
	}

	checkoutOrch := func(lane, commit, runID, attempt, jobName, jobID string) map[string]any {
		t.Helper()
		code, body := postJSONStatus(t, env.ServerURL+"/api/v1/orchestration/checkout", tok, map[string]any{
			"repository":     secMMRepo,
			"commit_sha":     commit,
			"gh_run_id":      runID,
			"name":           lane,
			"gh_run_attempt": attempt,
			"gh_job_name":    jobName,
			"gh_job_id":      jobID,
			"batch_size":     1,
		})
		if code != http.StatusOK {
			t.Fatalf("orchestration/checkout %s/%s (%s): %d %s", lane, runID, jobID, code, body)
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode checkout: %v", err)
		}
		return out
	}

	completeOrch := func(lane, commit, runID, attempt, jobName, jobID, specPath, status string) {
		t.Helper()
		code, body := postJSONStatus(t, env.ServerURL+"/api/v1/orchestration/complete", tok, map[string]any{
			"repository":     secMMRepo,
			"commit_sha":     commit,
			"gh_run_id":      runID,
			"name":           lane,
			"gh_run_attempt": attempt,
			"gh_job_name":    jobName,
			"gh_job_id":      jobID,
			"results":        []any{map[string]any{"spec_path": specPath, "status": status}},
		})
		if code != http.StatusOK {
			t.Fatalf("orchestration/complete %s/%s (%s): %d %s", lane, runID, jobID, code, body)
		}
	}

	ageGroupAndOrch := func(gid uuid.UUID, orchRunID *uuid.UUID, ts time.Time) {
		t.Helper()
		if _, err := env.Pool.Exec(ctx, `
			UPDATE report_groups SET created_at = $2, updated_at = $2, last_upload_at = $2 WHERE id = $1
		`, gid, ts); err != nil {
			t.Fatalf("age report_group %s: %v", gid, err)
		}
		if orchRunID != nil {
			if _, err := env.Pool.Exec(ctx, `
				UPDATE orchestration_runs SET started_at = $2, last_activity_at = $2, terminal_at = $3 WHERE id = $1
			`, *orchRunID, ts, ts.Add(time.Minute)); err != nil {
				t.Fatalf("age orchestration_run %s: %v", *orchRunID, err)
			}
		}
		mustRefresh(t, ctx, env.Pool, gid)
	}

	// ---- 1. Main KPI 1: fresh + reclaim (NO retest!) vs retest vs timed_out vs attempt=2 ----

	// 1a. Main Lane A ("e2e-main-reclaim", attempt=1):
	//     Unit 0 checked out by job-rec-1 (fresh), lease expires -> reclaimed by job-rec-2 (reclaim) and passes!
	//     Because there is NO failed attempt and NO 'retest' lease, strict lane pass rate MUST be TRUE!
	orchReclaim := beginOrch("e2e-main-reclaim", "main", "sha-main-rec", "run-main-rec", "1", []string{"specs/reclaim.spec.ts"}, true)
	coRec1 := checkoutOrch("e2e-main-reclaim", "sha-main-rec", "run-main-rec", "1", "shard-1", "job-rec-1")
	if coRec1["is_retest"].(bool) {
		t.Fatal("first checkout of reclaim lane must have is_retest=false")
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE leases SET deadline = now() - interval '5 seconds' WHERE gh_job_id = 'job-rec-1'`); err != nil {
		t.Fatalf("expire job-rec-1 lease: %v", err)
	}
	coRec2 := checkoutOrch("e2e-main-reclaim", "sha-main-rec", "run-main-rec", "1", "shard-2", "job-rec-2")
	if coRec2["is_retest"].(bool) {
		t.Fatal("reclaim checkout must have is_retest=false")
	}
	completeOrch("e2e-main-reclaim", "sha-main-rec", "run-main-rec", "1", "shard-2", "job-rec-2", "specs/reclaim.spec.ts", "passed")
	var reclaimLeaseKind string
	if err := env.Pool.QueryRow(ctx, `SELECT kind FROM leases WHERE gh_job_id = 'job-rec-2'`).Scan(&reclaimLeaseKind); err != nil {
		t.Fatalf("query reclaim lease kind: %v", err)
	}
	if reclaimLeaseKind != "reclaim" {
		t.Fatalf("job-rec-2 lease kind = %q, want 'reclaim'", reclaimLeaseKind)
	}
	pwRec := buildPWPayload("specs/reclaim.spec.ts", "reclaimed test passes", []map[string]any{
		makePWSpecJSON("reclaimed test passes", "passed", 0, "", ""),
	})
	gidRec := uploadShard(secMMRepo, "e2e-main-reclaim", "main", "sha-main-rec", "run-main-rec", "1", "main", nil, "job-rec-2", pwRec)
	ageGroupAndOrch(gidRec, &orchReclaim, settledTime)

	// 1b. Main Lane B ("e2e-main-retest", attempt=1):
	//     Unit 0 checked out by job-ret-1 (fresh) -> fails; checked out by job-ret-2 (retest, is_retest=true) -> passes!
	//     Standard lane pass = true (completed_fail_count=0), Strict lane pass = FALSE (has_retried_unit=true)!
	//     Test-level KPI 1: retry 0 failed + retry 1 passed -> first_attempt_passed = false!
	//     Also carries errMainOutage on a separate failing test? No: keep unit 0 passing on retest, and have a dedicated
	//     main outage lane ("e2e-main-outage") failing with errMainOutage so sigMain is on main!
	orchRetest := beginOrch("e2e-main-retest", "main", "sha-main-ret", "run-main-ret", "1", []string{"specs/retest.spec.ts"}, true)
	_ = checkoutOrch("e2e-main-retest", "sha-main-ret", "run-main-ret", "1", "shard-1", "job-ret-1")
	completeOrch("e2e-main-retest", "sha-main-ret", "run-main-ret", "1", "shard-1", "job-ret-1", "specs/retest.spec.ts", "failed")
	coRet2 := checkoutOrch("e2e-main-retest", "sha-main-ret", "run-main-ret", "1", "shard-2", "job-ret-2")
	if !coRet2["is_retest"].(bool) {
		t.Fatal("retest checkout must have is_retest=true")
	}
	completeOrch("e2e-main-retest", "sha-main-ret", "run-main-ret", "1", "shard-2", "job-ret-2", "specs/retest.spec.ts", "passed")
	var retestLeaseKind string
	if err := env.Pool.QueryRow(ctx, `SELECT kind FROM leases WHERE gh_job_id = 'job-ret-2'`).Scan(&retestLeaseKind); err != nil {
		t.Fatalf("query retest lease kind: %v", err)
	}
	if retestLeaseKind != "retest" {
		t.Fatalf("job-ret-2 lease kind = %q, want 'retest'", retestLeaseKind)
	}
	pwRet := buildPWPayload("specs/retest.spec.ts", "retried test passes", []map[string]any{
		makePWSpecJSON("retried test passes", "failed", 0, "Error: flaky button", "Error: flaky button\n    at specs/retest.spec.ts:5:2"),
		makePWSpecJSON("retried test passes", "passed", 1, "", ""),
	})
	gidRet := uploadShard(secMMRepo, "e2e-main-retest", "main", "sha-main-ret", "run-main-ret", "1", "main", nil, "job-ret-2", pwRet)
	ageGroupAndOrch(gidRet, &orchRetest, settledTime)

	// 1c. Main Lane B rerun (attempt=2) -> MUST be ignored by KPI 1!
	pwRetAtt2 := buildPWPayload("specs/retest.spec.ts", "retried test passes", []map[string]any{
		makePWSpecJSON("retried test passes", "passed", 0, "", ""),
	})
	gidRetAtt2 := uploadShard(secMMRepo, "e2e-main-retest", "main", "sha-main-ret", "run-main-ret", "2", "main", nil, "job-ret-att2", pwRetAtt2)
	ageGroupAndOrch(gidRetAtt2, nil, settledTime.Add(time.Hour))

	// 1d. Main Lane C ("e2e-main-timeout", attempt=1): timed_out via MarkRunTimedOut -> red, fails KPI 1 lane & strict!
	orchTO := beginOrch("e2e-main-timeout", "main", "sha-main-to", "run-main-to", "1", []string{"specs/to.spec.ts"}, false)
	_ = checkoutOrch("e2e-main-timeout", "sha-main-to", "run-main-to", "1", "shard-1", "job-to-1")
	if _, err := env.Pool.Exec(ctx, `UPDATE orchestration_runs SET last_activity_at = now() - interval '10 minutes' WHERE id = $1`, orchTO); err != nil {
		t.Fatalf("backdate orchTO: %v", err)
	}
	if timedOut, err := env.Orchestrator.MarkRunTimedOut(ctx, orchTO); err != nil || !timedOut {
		t.Fatalf("MarkRunTimedOut = (%v, %v), want (true, nil)", timedOut, err)
	}
	// Refresh the report_group created by /orchestration/begin for timed_out main run.
	var gidMainTO uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		UPDATE report_groups
		   SET status = 'completed', stage = 'main'
		 WHERE repository = $1 AND commit_sha = 'sha-main-to' AND gh_run_id = 'run-main-to' AND name = 'e2e-main-timeout' AND gh_run_attempt = '1'
		 RETURNING id
	`, secMMRepo).Scan(&gidMainTO); err != nil {
		t.Fatalf("update timed_out main report_group: %v", err)
	}
	ageGroupAndOrch(gidMainTO, &orchTO, settledTime)

	// 1e. Main Lane D ("e2e-main-outage", attempt=1): fails with errMainOutage so sigMain is on main within 24h of settled PRs!
	orchMainOut := beginOrch("e2e-main-outage", "main", "sha-main-out", "run-main-out", "1", []string{"specs/outage.spec.ts"}, false)
	_ = checkoutOrch("e2e-main-outage", "sha-main-out", "run-main-out", "1", "shard-1", "job-out-1")
	completeOrch("e2e-main-outage", "sha-main-out", "run-main-out", "1", "shard-1", "job-out-1", "specs/outage.spec.ts", "failed")
	pwMainOut := buildPWPayload("specs/outage.spec.ts", "main outage check", []map[string]any{
		makePWSpecJSON("main outage check", "failed", 0, errMainOutage, errMainOutage+"\n    at specs/outage.spec.ts:10:4"),
	})
	gidMainOut := uploadShard(secMMRepo, "e2e-main-outage", "main", "sha-main-out", "run-main-out", "1", "main", nil, "job-out-1", pwMainOut)
	ageGroupAndOrch(gidMainOut, &orchMainOut, settledTime)

	// ---- 2. PR False-Red Attribution via real HTTP flows ----
	pr := func(n int) *int { return &n }

	// PR #401: attempt 1 fails with errMainOutage, attempt 2 goes green -> priority MUST choose went_green_on_rerun over failing_elsewhere!
	orch401a1 := beginOrch(secPWFullLane, "pr-401", "sha-pr-401", "run-pr-401", "1", []string{"specs/outage.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-401", "run-pr-401", "1", "shard-1", "job-401-a1")
	completeOrch(secPWFullLane, "sha-pr-401", "run-pr-401", "1", "shard-1", "job-401-a1", "specs/outage.spec.ts", "failed")
	pw401a1 := buildPWPayload("specs/outage.spec.ts", "main outage check", []map[string]any{
		makePWSpecJSON("main outage check", "failed", 0, errMainOutage, errMainOutage+"\n    at specs/outage.spec.ts:10:4"),
	})
	gid401a1 := uploadShard(secMMRepo, secPWFullLane, "pr-401", "sha-pr-401", "run-pr-401", "1", "pr", pr(401), "job-401-a1", pw401a1)
	ageGroupAndOrch(gid401a1, &orch401a1, settledTime)

	orch401a2 := beginOrch(secPWFullLane, "pr-401", "sha-pr-401", "run-pr-401", "2", []string{"specs/outage.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-401", "run-pr-401", "2", "shard-1", "job-401-a2")
	completeOrch(secPWFullLane, "sha-pr-401", "run-pr-401", "2", "shard-1", "job-401-a2", "specs/outage.spec.ts", "passed")
	pw401a2 := buildPWPayload("specs/outage.spec.ts", "main outage check", []map[string]any{
		makePWSpecJSON("main outage check", "passed", 0, "", ""),
	})
	gid401a2 := uploadShard(secMMRepo, secPWFullLane, "pr-401", "sha-pr-401", "run-pr-401", "2", "pr", pr(401), "job-401-a2", pw401a2)
	ageGroupAndOrch(gid401a2, &orch401a2, settledTime.Add(time.Hour))

	// PR #402: fails with errMainOutage (same as main within 24h), no rerun -> failing_elsewhere (failing_on_main=true).
	orch402 := beginOrch(secPWFullLane, "pr-402", "sha-pr-402", "run-pr-402", "1", []string{"specs/outage.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-402", "run-pr-402", "1", "shard-1", "job-402-a1")
	completeOrch(secPWFullLane, "sha-pr-402", "run-pr-402", "1", "shard-1", "job-402-a1", "specs/outage.spec.ts", "failed")
	gid402 := uploadShard(secMMRepo, secPWFullLane, "pr-402", "sha-pr-402", "run-pr-402", "1", "pr", pr(402), "job-402-a1", pw401a1)
	ageGroupAndOrch(gid402, &orch402, settledTime)

	// PRs #403, #404, #405, #406: fail with errMultiPR (>=3 other PRs within 24h, not on main) -> failing_elsewhere (other_prs_count=3).
	pwMultiPR := buildPWPayload("specs/cdn.spec.ts", "cdn asset check", []map[string]any{
		makePWSpecJSON("cdn asset check", "failed", 0, errMultiPR, errMultiPR+"\n    at specs/cdn.spec.ts:22:3"),
	})
	for _, prNum := range []int{403, 404, 405, 406} {
		sha := fmt.Sprintf("sha-pr-%d", prNum)
		rID := fmt.Sprintf("run-pr-%d", prNum)
		jID := fmt.Sprintf("job-%d-a1", prNum)
		oID := beginOrch(secPWFullLane, fmt.Sprintf("pr-%d", prNum), sha, rID, "1", []string{"specs/cdn.spec.ts"}, false)
		_ = checkoutOrch(secPWFullLane, sha, rID, "1", "shard-1", jID)
		completeOrch(secPWFullLane, sha, rID, "1", "shard-1", jID, "specs/cdn.spec.ts", "failed")
		gID := uploadShard(secMMRepo, secPWFullLane, fmt.Sprintf("pr-%d", prNum), sha, rID, "1", "pr", pr(prNum), jID, pwMultiPR)
		ageGroupAndOrch(gID, &oID, settledTime)
	}

	// PR #407: fails on sha-pr-407-old with errUnique, then PR #407 pushes sha-pr-407-new which passes ->
	// new-commit fix excluded (went_green_on_rerun=false, reason=other, still_red=false)!
	pwUnique := buildPWPayload("specs/unique.spec.ts", "pr407 check", []map[string]any{
		makePWSpecJSON("pr407 check", "failed", 0, errUnique, errUnique+"\n    at specs/unique.spec.ts:8:1"),
	})
	orch407Old := beginOrch(secPWFullLane, "pr-407", "sha-pr-407-old", "run-pr-407-old", "1", []string{"specs/unique.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-407-old", "run-pr-407-old", "1", "shard-1", "job-407-old")
	completeOrch(secPWFullLane, "sha-pr-407-old", "run-pr-407-old", "1", "shard-1", "job-407-old", "specs/unique.spec.ts", "failed")
	gid407Old := uploadShard(secMMRepo, secPWFullLane, "pr-407", "sha-pr-407-old", "run-pr-407-old", "1", "pr", pr(407), "job-407-old", pwUnique)
	ageGroupAndOrch(gid407Old, &orch407Old, settledTime)

	pw407New := buildPWPayload("specs/unique.spec.ts", "pr407 check", []map[string]any{
		makePWSpecJSON("pr407 check", "passed", 0, "", ""),
	})
	orch407New := beginOrch(secPWFullLane, "pr-407", "sha-pr-407-new", "run-pr-407-new", "1", []string{"specs/unique.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-407-new", "run-pr-407-new", "1", "shard-1", "job-407-new")
	completeOrch(secPWFullLane, "sha-pr-407-new", "run-pr-407-new", "1", "shard-1", "job-407-new", "specs/unique.spec.ts", "passed")
	gid407New := uploadShard(secMMRepo, secPWFullLane, "pr-407", "sha-pr-407-new", "run-pr-407-new", "1", "pr", pr(407), "job-407-new", pw407New)
	ageGroupAndOrch(gid407New, &orch407New, settledTime.Add(2*time.Hour))

	// PR #408: timed_out configured PR E2E lane with 0 signatures -> reason="other", still_red=true.
	orch408TO := beginOrch(secPWFullLane, "pr-408", "sha-pr-408", "run-pr-408", "1", []string{"specs/to-pr.spec.ts"}, false)
	_ = checkoutOrch(secPWFullLane, "sha-pr-408", "run-pr-408", "1", "shard-1", "job-408-to")
	if _, err := env.Pool.Exec(ctx, `UPDATE orchestration_runs SET last_activity_at = now() - interval '10 minutes' WHERE id = $1`, orch408TO); err != nil {
		t.Fatalf("backdate orch408TO: %v", err)
	}
	if timedOut, err := env.Orchestrator.MarkRunTimedOut(ctx, orch408TO); err != nil || !timedOut {
		t.Fatalf("MarkRunTimedOut(408) = (%v, %v), want (true, nil)", timedOut, err)
	}
	var gid408 uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		UPDATE report_groups
		   SET status = 'completed', stage = 'pr', gh_pr_number = 408
		 WHERE repository = $1 AND commit_sha = 'sha-pr-408' AND gh_run_id = 'run-pr-408' AND name = $2 AND gh_run_attempt = '1'
		 RETURNING id
	`, secMMRepo, secPWFullLane).Scan(&gid408); err != nil {
		t.Fatalf("update timed_out pr-408 report_group: %v", err)
	}
	ageGroupAndOrch(gid408, &orch408TO, settledTime)

	// ---- 3. Assert KPI 1 (main) & KPI 2 (PR false-red) via /api/v1/kpi/summary, /api/v1/kpi/prs/red, and SSR /kpi/prs ----
	sum := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?window=30d")

	// Main attempt=1 tests:
	//   - e2e-main-reclaim: 1 passed on retry=0 -> first_attempt_passed=true
	//   - e2e-main-retest:  1 flaky (retry=0 failed + retry=1 passed) -> first_attempt_passed=false
	//   - e2e-main-outage:  1 failed -> first_attempt_passed=false
	//   - e2e-main-retest attempt=2 -> IGNORED
	//   Total = 3 tests, FirstAttemptPassed = 1 => 33.333333%
	if sum.MainPassRate.TestLevel.FirstAttemptPassed != 1 || sum.MainPassRate.TestLevel.TotalTests != 3 {
		t.Fatalf("KPI 1 test_level = %d/%d, want 1/3",
			sum.MainPassRate.TestLevel.FirstAttemptPassed, sum.MainPassRate.TestLevel.TotalTests)
	}
	assertNearPtr(t, "KPI 1 test pass_rate", sum.MainPassRate.TestLevel.PassRate, 100.0/3.0, 1e-6)

	// Main attempt=1 lanes (4): reclaim (pass, strict pass), retest (pass, strict FAIL), timeout (fail, strict fail), outage (fail, strict fail).
	//   Standard: 2/4 = 50.0%
	//   Strict:   1/4 = 25.0%
	if sum.MainPassRate.LaneLevel.PassedLanes != 2 || sum.MainPassRate.LaneLevel.TotalLanes != 4 {
		t.Fatalf("KPI 1 lane_level = %d/%d, want 2/4",
			sum.MainPassRate.LaneLevel.PassedLanes, sum.MainPassRate.LaneLevel.TotalLanes)
	}
	assertNearPtr(t, "KPI 1 lane pass_rate", sum.MainPassRate.LaneLevel.PassRate, 50.0, 1e-6)
	if sum.MainPassRate.LaneLevel.StrictPassedLanes != 1 || sum.MainPassRate.LaneLevel.StrictTotalLanes != 4 {
		t.Fatalf("KPI 1 strict = %d/%d, want 1/4",
			sum.MainPassRate.LaneLevel.StrictPassedLanes, sum.MainPassRate.LaneLevel.StrictTotalLanes)
	}
	assertNearPtr(t, "KPI 1 strict_pass_rate", sum.MainPassRate.LaneLevel.StrictPassRate, 25.0, 1e-6)

	// PR false-red (8 settled red lanes: #401 rerun, #402 main, #403-#406 >=3 PRs, #407 new-commit other, #408 timed_out other):
	//   went_green_on_rerun = 1 (#401), failing_elsewhere = 5 (#402-#406), other = 2 (#407, #408) -> 1/8 = 12.5%
	prs := getJSON[kpi.PRRedResponse](t, env.ServerURL+"/api/v1/kpi/prs/red?window=30d")
	if prs.SettledRedLanes != 8 || prs.SettledFalseRedLanes != 1 || prs.UnsettledRedLanes != 0 {
		t.Fatalf("PRsRed counts = settled:%d falseRed:%d unsettled:%d, want 8/1/0",
			prs.SettledRedLanes, prs.SettledFalseRedLanes, prs.UnsettledRedLanes)
	}
	if prs.ReasonMix.WentGreenOnRerun != 1 || prs.ReasonMix.FailingElsewhere != 5 || prs.ReasonMix.Other != 2 {
		t.Fatalf("PRsRed reason_mix = %+v, want 1/5/2", prs.ReasonMix)
	}
	assertNearPtr(t, "PRsRed false_red_rate", prs.FalseRedRate, 12.5, 1e-6)

	byPR := map[int]kpi.RedPRLaneRow{}
	for _, r := range prs.Lanes {
		byPR[r.GHPRNumber] = r
	}
	if byPR[401].Reason != kpi.ReasonWentGreenOnRerun || !byPR[401].WentGreenOnRerun || byPR[401].StillRed {
		t.Fatalf("PR #401 unexpected: %+v", byPR[401])
	}
	if byPR[402].Reason != kpi.ReasonFailingElsewhere || !byPR[402].FailingOnMain || !byPR[402].StillRed {
		t.Fatalf("PR #402 unexpected: %+v", byPR[402])
	}
	if byPR[403].Reason != kpi.ReasonFailingElsewhere || byPR[403].OtherPRsCount != 3 || byPR[403].FailingOnMain {
		t.Fatalf("PR #403 unexpected: %+v", byPR[403])
	}
	if byPR[407].Reason != kpi.ReasonOther || byPR[407].WentGreenOnRerun || byPR[407].StillRed {
		t.Fatalf("PR #407 (new-commit fix excluded) unexpected: %+v", byPR[407])
	}
	if byPR[408].Reason != kpi.ReasonOther || !byPR[408].StillRed {
		t.Fatalf("PR #408 (timed_out) unexpected: %+v", byPR[408])
	}

	// SSR GET /kpi/prs
	prsHTML := getRaw(t, env.ServerURL+"/kpi/prs")
	for _, want := range []string{
		`at least 12.5%`,
		`Went green on rerun`,
		`Also failing elsewhere`,
		`E2E red`,
		`Failing on main`,
		`Failing on 3 other PRs`,
		`#408`,
	} {
		if !strings.Contains(prsHTML, want) {
			t.Fatalf("SSR /kpi/prs missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(prsHTML), "blocked") || strings.Contains(prsHTML, "Fixed") {
		t.Fatal("SSR /kpi/prs violated copy guardrails ('blocked' or 'Fixed')")
	}
}

// TestSEC11515_HandComputedUnifiedKPIFixtureSuite seeds a single realistic multi-repo
// (mattermost/mattermost, mattermost/mattermost-mobile, mattermost/desktop), multi-app,
// multi-stage (main, pr), multi-day dataset and verifies EVERY hand-computed KPI (1-5),
// supporting triage metrics, /api/v1/tests/{id}/state, and all 3 SSR routes (/kpi, /kpi/prs,
// /kpi/failures/{id}) simultaneously against the exact same seeded state.
//
// Hand-computed derivations (window=30d, anchor todayMidnight UTC):
//
//	KPI 1 (Main first-attempt pass rate):
//	  - gMain1 (mattermost, e2e-pw-main, -2d, att=1, orch completed fail=1 retried=true):
//	      tMainPass(pass r0 -> first_attempt), tMainFlaky(fail r0 + pass r1 -> flaky), tActiveFail(fail r0)
//	      -> 3 tests run, 1 first-attempt pass; lane_pass=0/1, strict_lane_pass=0/1
//	  - gMain2 (mattermost, e2e-cy-main, -2d, att=1, orch completed fail=0 retried=false):
//	      tMainPass(pass r0) -> 1 test run, 1 first-attempt pass; lane_pass=1/1, strict_lane_pass=1/1
//	  - gMainDesk (desktop, desktop-main, -2d, att=1, upload-only):
//	      tDesktopPass(pass r0) -> 1 test run, 1 first-attempt pass; lane_pass=1/1, upload_only=1
//	  - Current totals: test-level = (1+1+1)/(3+1+1) = 3/5 = 60.0%;
//	      lane-level = 2/3 = 66.666667%; strict = 1/2 = 50.0%; upload_only_lanes = 1.
//	  - Prior 30d (gMainPrior, -35d, att=1, orch fail=1 retried=true): 2 tests, 1 pass => 50.0%;
//	      lane = 0/1 = 0%; strict = 0/1 = 0%.
//	  - Deltas: test = +10.0 pp, lane = +66.666667 pp, strict = +50.0 pp.
//
//	KPI 2 (PR false-red lower bound, configured E2E lanes):
//	  - Current 30d settled (>24h):
//	      gPR101 (playwright-full, -2d, PR #101, failed then attempt=2 passed) -> went_green_on_rerun
//	      gPR102 (playwright-full, -2d, PR #102, sigActive also on main)      -> failing_elsewhere
//	      gPR103TO (playwright-full, -2d, PR #103, orch timed_out)            -> other
//	    -> 3 settled red lanes, 1 false red => "at least 33.333333%" ("at least 33.3%").
//	    -> Reason mix: went_green_on_rerun=1, failing_elsewhere=1, other=1, total=3.
//	  - Current 30d unsettled (<24h): gPR104Unsettled (playwright-full, -3h, PR #104) => 1.
//	  - Prior 30d settled: gPRPrior1 (rerun green) + gPRPrior2 (still red) => 1/2 = 50.0%; delta = -16.666667 pp.
//
//	KPI 3 (Flaky tests, trailing 14d, >=2 mixed commits per (test_id, project, platform)):
//	  - tMainFlaky (mattermost): mixed on sha-main-1 (-2d) and sha-pr-101 (-2d) => 1 flaky in mattermost.
//	  - tMobileAndFlaky (mobile, android): mixed on sha-mob-1 (-2d) and sha-mob-2 (-2d), ios passed => 1 flaky in mobile.
//	  - tMobileSplit (mobile): ios passed, android failed on sha-mob-1 & sha-mob-2 => 0 (NOT flaky across platforms!).
//	  - Total = 2 (mattermost: 1, mobile: 1). Prior 14d baseline: mattermost=0 (from gPriorSigActive/gMain? Wait: gPriorSigActive is -20d -> mattermost prior=0, delta=+1), mobile=0 (from gPRMobPrior -20d -> mobile prior=0, delta=+1) => total prior=0, delta=+2!
//
//	KPI 4 (Recurring failures):
//	  - sigActive: 1 occ (-20d, 200 runs) + 3 occ (-2d, 100 runs) = 4 occ / 300 runs = 13.333333 / 1k (30d);
//	      14d = 3/100 = 30.0 / 1k; prior 14d = 1/200 = 5.0 / 1k; delta = +25.0 / 1k; state = Active.
//	  - sigQuiet: 1 occ (-10d, 1 run) + 500 runs (-3d), 0 occ in 7d => state = Quiet (10d).
//	  - sigLowRuns: 1 occ (-10d, 1 run) + 499 runs (-3d), 0 occ in 7d => state = Not enough runs (10d).
//	  - sigResurf: 1 occ (-20d, 1 run) + 550 runs (-12d, quiet in [-19d..-4d]) + 1 occ (-3h) => state = Resurfaced.
//	  - Summary: total_signatures=4, active=1, resurfaced=1, quiet=1, not_enough_runs=1, total_occurrences=8, triaged_count=2.
//
//	KPI 5 (E2E PR run time, completed orchestrated stage='pr'):
//	  - Current 30d samples (ms): [100000, 200000, 300000, 400000, 600000] (5 samples)
//	    -> p50 = 300000.0 ms (5m 0s), p90 = 400000 + 0.6*(600000-400000) = 520000.0 ms (8m 40s).
//	  - Prior 30d samples (ms): [500000, 500000, 500000] -> prior_p50 = 500000.0 ms, delta_p50 = -200000.0 ms.
//	  - Upload-only PR lane: desktop-pr-upload (excluded from percentiles, listed in lane_coverage.upload_only_lane_names).
//
//	Supporting Triage Metrics (window=30d, 15 total runs in current 30d):
//	  - Completed (non-timed-out): 14, Timed-out: 1 (gPR103TO), Incomplete: 0, In-progress: 0 => completion_rate = 14/15 * 100 = 93.333333%.
//	  - Failed runs: 11; Triaged runs: 2 (gPR101, gPR104Unsettled) => run_coverage_rate = 2/11 * 100 = 18.181818%.
//	  - Failed test occurrences: 10; Triaged test occurrences: 2 => test_coverage_rate = 2/10 * 100 = 20.0%.
//	  - Verdicts: 2 total, 1 reused => verdict_reuse_rate = 50.0%. Label mix: flaky-infra (1, 50%), infra (1, 50%).
func TestSEC11515_HandComputedUnifiedKPIFixtureSuite(t *testing.T) {
	env := testenv.Start(
		t,
		testenv.WithAuthzMode(authapi.AuthzEnforce),
		testenv.WithHTMLView(),
		testenv.WithReaperDisabled(),
	)
	ctx := t.Context()
	apiKey := env.IssueAPIKey(t, "sec11515-unified")

	now := time.Now().UTC().Truncate(time.Second)
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	curDay := todayMidnight.AddDate(0, 0, -2).Add(12 * time.Hour)
	day10 := todayMidnight.AddDate(0, 0, -10).Add(12 * time.Hour)
	day20 := todayMidnight.AddDate(0, 0, -20).Add(12 * time.Hour)
	prior30Day := todayMidnight.AddDate(0, 0, -35).Add(12 * time.Hour)
	unsettledAt := now.Add(-3 * time.Hour)

	prPtr := func(n int) *int { return &n }

	// Seed tests.
	tMainPass := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-main-pass", "Main > passes first try")
	tMainFlaky := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-main-flaky", "Main > flaky across commits")
	tActiveFail := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-active-fail", "Auth > active failure")
	tQuietFail := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-quiet-fail", "Search > quiet failure")
	tLowRunsFail := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-low-fail", "Admin > low runs failure")
	tResurfFail := insertTest(t, ctx, env.Pool, secMMRepo, secPWFramework, "h:u-resurf-fail", "Files > resurfaced failure")
	tDesktopPass := insertTest(t, ctx, env.Pool, secDeskRepo, secPWFramework, "h:u-desk-pass", "Desktop > launch")
	tMobileSplit := insertTest(t, ctx, env.Pool, secMobRepo, "detox", "h:u-mob-split", "Mobile > platform split")
	tMobileAndFlaky := insertTest(t, ctx, env.Pool, secMobRepo, "detox", "h:u-mob-flaky", "Mobile > android flaky")

	// Backdate first_seen_at > 14d ago for known/flaky tests; keep tLowRunsFail recent for state="new".
	if _, err := env.Pool.Exec(ctx, `
		UPDATE tests SET first_seen_at = $1 WHERE id IN ($2, $3, $4, $5, $6)
	`, todayMidnight.AddDate(0, 0, -40), tMainFlaky, tDesktopPass, tMobileSplit, tMobileAndFlaky, tMainPass); err != nil {
		t.Fatalf("backdate tests: %v", err)
	}

	// Seed failure signatures.
	insertUnifiedSig := func(fp, errClass, msg, frame string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO failure_signatures (fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message, first_seen_at)
			VALUES ($1, $2, $3, $4, $5, $4, $6)
			RETURNING id
		`, fp, errorsig.AlgoVersion, errClass, msg, frame, day20).Scan(&id); err != nil {
			t.Fatalf("insert sig %s: %v", msg, err)
		}
		return id
	}
	sigActive := insertUnifiedSig(strings.Repeat("1", 64), "TimeoutError", "waiting for #login-btn", "e2e/login.spec.ts:42")
	sigQuiet := insertUnifiedSig(strings.Repeat("2", 64), "NetworkError", "ECONNRESET search", "e2e/search.spec.ts:19")
	sigLowRuns := insertUnifiedSig(strings.Repeat("3", 64), "AssertionError", "badge count mismatch", "e2e/admin.spec.ts:88")
	sigResurf := insertUnifiedSig(strings.Repeat("4", 64), "ReferenceError", "__MM__ undefined", "e2e/files.spec.ts:15")

	// 1. Main stage runs (KPI 1).
	gMain1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-pw-main", Status: "completed",
		Repo: secMMRepo, Branch: "main", Commit: "sha-main-1", RunID: "u-main-1", Attempt: "1",
		Stage: "main", CreatedAt: curDay,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-main-1", RunID: "u-main-1", Name: "e2e-pw-main",
		Attempt: "1", Status: "completed", FailCount: 1, HasRetriedUnit: true, StartedAt: curDay,
	})
	rM1 := insertReport(t, ctx, env.Pool, gMain1, "e2e-pw-main", "")
	sM1 := insertSuite(t, ctx, env.Pool, rM1, "Main")
	insertCase(t, ctx, env.Pool, sM1, tMainPass, "passes first try", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sM1, tMainFlaky, "flaky across commits", "failed", 0, "", 1)
	insertCase(t, ctx, env.Pool, sM1, tMainFlaky, "flaky across commits", "passed", 1, "", 2)
	insertCaseWithSig(t, ctx, env.Pool, sM1, tActiveFail, sigActive, "active failure", "failed", 0, 3)
	mustRefresh(t, ctx, env.Pool, gMain1)

	gMain2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "cypress", Name: "e2e-cy-main", Status: "completed",
		Repo: secMMRepo, Branch: "main", Commit: "sha-main-2", RunID: "u-main-2", Attempt: "1",
		Stage: "main", CreatedAt: curDay,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-main-2", RunID: "u-main-2", Name: "e2e-cy-main",
		Attempt: "1", Status: "completed", FailCount: 0, HasRetriedUnit: false, StartedAt: curDay,
	})
	rM2 := insertReport(t, ctx, env.Pool, gMain2, "e2e-cy-main", "")
	sM2 := insertSuite(t, ctx, env.Pool, rM2, "Cypress")
	insertCase(t, ctx, env.Pool, sM2, tMainPass, "passes first try", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gMain2)

	gMainDesk := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "desktop-main", Status: "completed",
		Repo: secDeskRepo, Branch: "main", Commit: "sha-desk-main", RunID: "u-desk-main", Attempt: "1",
		Stage: "main", CreatedAt: curDay,
	})
	rMD := insertReport(t, ctx, env.Pool, gMainDesk, "desktop-main", "")
	sMD := insertSuite(t, ctx, env.Pool, rMD, "Desktop")
	insertCase(t, ctx, env.Pool, sMD, tDesktopPass, "launch", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gMainDesk)

	gMainPrior := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-pw-main", Status: "completed",
		Repo: secMMRepo, Branch: "main", Commit: "sha-main-prior", RunID: "u-main-prior", Attempt: "1",
		Stage: "main", CreatedAt: prior30Day,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-main-prior", RunID: "u-main-prior", Name: "e2e-pw-main",
		Attempt: "1", Status: "completed", FailCount: 1, HasRetriedUnit: true, StartedAt: prior30Day,
	})
	rMP := insertReport(t, ctx, env.Pool, gMainPrior, "e2e-pw-main", "")
	sMP := insertSuite(t, ctx, env.Pool, rMP, "MainPrior")
	insertCase(t, ctx, env.Pool, sMP, tMainPass, "passes first try", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMP, tMainFlaky, "flaky across commits", "failed", 0, "", 1)
	mustRefresh(t, ctx, env.Pool, gMainPrior)

	// 2. PR stage runs (KPI 2 + KPI 5 + KPI 3 + Triage).
	gPR101 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-101", Commit: "sha-pr-101", RunID: "u-pr-101", Attempt: "1",
		PRNum: prPtr(101), Stage: "pr", CreatedAt: curDay, DurMs: int64Ptr(100000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-101", RunID: "u-pr-101", Name: secPWFullLane,
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: curDay,
	})
	r101 := insertReport(t, ctx, env.Pool, gPR101, secPWFullLane, "")
	s101 := insertSuite(t, ctx, env.Pool, r101, "PR101")
	insertCaseWithSig(t, ctx, env.Pool, s101, tActiveFail, sigActive, "active failure", "failed", 0, 0)
	insertCase(t, ctx, env.Pool, s101, tMainFlaky, "flaky across commits", "failed", 0, "", 1)
	insertCase(t, ctx, env.Pool, s101, tMainFlaky, "flaky across commits", "passed", 1, "", 2)
	mustRefresh(t, ctx, env.Pool, gPR101)

	gPR101Rerun := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-101", Commit: "sha-pr-101", RunID: "u-pr-101", Attempt: "2",
		PRNum: prPtr(101), Stage: "pr", CreatedAt: curDay.Add(time.Hour), DurMs: int64Ptr(200000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-101", RunID: "u-pr-101", Name: secPWFullLane,
		Attempt: "2", Status: "completed", FailCount: 0, StartedAt: curDay.Add(time.Hour),
	})
	r101R := insertReport(t, ctx, env.Pool, gPR101Rerun, secPWFullLane, "")
	s101R := insertSuite(t, ctx, env.Pool, r101R, "PR101R")
	insertCase(t, ctx, env.Pool, s101R, tActiveFail, "active failure", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPR101Rerun)

	gPR102 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-102", Commit: "sha-pr-102", RunID: "u-pr-102", Attempt: "1",
		PRNum: prPtr(102), Stage: "pr", CreatedAt: curDay, DurMs: int64Ptr(300000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-102", RunID: "u-pr-102", Name: secPWFullLane,
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: curDay,
	})
	r102 := insertReport(t, ctx, env.Pool, gPR102, secPWFullLane, "")
	s102 := insertSuite(t, ctx, env.Pool, r102, "PR102")
	insertCaseWithSig(t, ctx, env.Pool, s102, tActiveFail, sigActive, "active failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR102)

	gPR103TO := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-103", Commit: "sha-pr-103", RunID: "u-pr-103", Attempt: "1",
		PRNum: prPtr(103), Stage: "pr", CreatedAt: curDay,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-103", RunID: "u-pr-103", Name: secPWFullLane,
		Attempt: "1", Status: "timed_out", FailCount: 0, StartedAt: curDay,
	})
	mustRefresh(t, ctx, env.Pool, gPR103TO)

	gPR104Unsettled := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-104", Commit: "sha-pr-104", RunID: "u-pr-104", Attempt: "1",
		PRNum: prPtr(104), Stage: "pr", CreatedAt: unsettledAt, DurMs: int64Ptr(400000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-104", RunID: "u-pr-104", Name: secPWFullLane,
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: unsettledAt,
	})
	r104 := insertReport(t, ctx, env.Pool, gPR104Unsettled, secPWFullLane, "")
	s104 := insertSuite(t, ctx, env.Pool, r104, "PR104")
	insertCaseWithSig(t, ctx, env.Pool, s104, tResurfFail, sigResurf, "resurfaced failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR104Unsettled)

	// Prior 30d PR runs (KPI 2 prior + KPI 5 prior).
	gPRPrior1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-90", Commit: "sha-pr-90", RunID: "u-pr-90", Attempt: "1",
		PRNum: prPtr(90), Stage: "pr", CreatedAt: prior30Day, DurMs: int64Ptr(500000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-90", RunID: "u-pr-90", Name: secPWFullLane,
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: prior30Day,
	})
	rP1 := insertReport(t, ctx, env.Pool, gPRPrior1, secPWFullLane, "")
	sP1 := insertSuite(t, ctx, env.Pool, rP1, "PRP1")
	insertCase(t, ctx, env.Pool, sP1, tMainPass, "passes first try", "failed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRPrior1)

	gPRPrior1R := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-90", Commit: "sha-pr-90", RunID: "u-pr-90", Attempt: "2",
		PRNum: prPtr(90), Stage: "pr", CreatedAt: prior30Day.Add(time.Hour), DurMs: int64Ptr(500000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-90", RunID: "u-pr-90", Name: secPWFullLane,
		Attempt: "2", Status: "completed", FailCount: 0, StartedAt: prior30Day.Add(time.Hour),
	})
	rP1R := insertReport(t, ctx, env.Pool, gPRPrior1R, secPWFullLane, "")
	sP1R := insertSuite(t, ctx, env.Pool, rP1R, "PRP1R")
	insertCase(t, ctx, env.Pool, sP1R, tMainPass, "passes first try", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRPrior1R)

	gPRPrior2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: secPWFullLane, Status: "completed",
		Repo: secMMRepo, Branch: "pr-91", Commit: "sha-pr-91", RunID: "u-pr-91", Attempt: "1",
		PRNum: prPtr(91), Stage: "pr", CreatedAt: prior30Day, DurMs: int64Ptr(500000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMMRepo, Commit: "sha-pr-91", RunID: "u-pr-91", Name: secPWFullLane,
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: prior30Day,
	})
	rP2 := insertReport(t, ctx, env.Pool, gPRPrior2, secPWFullLane, "")
	sP2 := insertSuite(t, ctx, env.Pool, rP2, "PRP2")
	insertCase(t, ctx, env.Pool, sP2, tMainPass, "passes first try", "failed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRPrior2)

	// 3. Mobile PR runs (detox-pr: unconfigured for KPI 2, contributes to KPI 3 & KPI 5).
	gPRMob1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "detox-pr", Status: "completed",
		Repo: secMobRepo, Branch: "pr-105", Commit: "sha-mob-1", RunID: "u-mob-1", Attempt: "1",
		PRNum: prPtr(105), Stage: "pr", CreatedAt: curDay, DurMs: int64Ptr(600000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: secMobRepo, Commit: "sha-mob-1", RunID: "u-mob-1", Name: "detox-pr",
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: curDay,
	})
	rMob1IOS := insertReport(t, ctx, env.Pool, gPRMob1, "detox-ios", "ios")
	sMob1IOS := insertSuite(t, ctx, env.Pool, rMob1IOS, "MobIOS")
	insertCase(t, ctx, env.Pool, sMob1IOS, tMobileSplit, "platform split", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMob1IOS, tMobileAndFlaky, "android flaky", "passed", 0, "", 1)
	rMob1And := insertReport(t, ctx, env.Pool, gPRMob1, "detox-and", "android")
	sMob1And := insertSuite(t, ctx, env.Pool, rMob1And, "MobAnd")
	insertCase(t, ctx, env.Pool, sMob1And, tMobileSplit, "platform split", "failed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMob1And, tMobileAndFlaky, "android flaky", "failed", 0, "", 1)
	insertCase(t, ctx, env.Pool, sMob1And, tMobileAndFlaky, "android flaky", "passed", 1, "", 2)
	mustRefresh(t, ctx, env.Pool, gPRMob1)

	gPRMob2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "detox-pr", Status: "completed",
		Repo: secMobRepo, Branch: "pr-106", Commit: "sha-mob-2", RunID: "u-mob-2", Attempt: "1",
		PRNum: prPtr(106), Stage: "pr", CreatedAt: curDay,
	})
	rMob2IOS := insertReport(t, ctx, env.Pool, gPRMob2, "detox-ios", "ios")
	sMob2IOS := insertSuite(t, ctx, env.Pool, rMob2IOS, "MobIOS2")
	insertCase(t, ctx, env.Pool, sMob2IOS, tMobileSplit, "platform split", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMob2IOS, tMobileAndFlaky, "android flaky", "passed", 0, "", 1)
	rMob2And := insertReport(t, ctx, env.Pool, gPRMob2, "detox-and", "android")
	sMob2And := insertSuite(t, ctx, env.Pool, rMob2And, "MobAnd2")
	insertCase(t, ctx, env.Pool, sMob2And, tMobileSplit, "platform split", "failed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMob2And, tMobileAndFlaky, "android flaky", "failed", 0, "", 1)
	insertCase(t, ctx, env.Pool, sMob2And, tMobileAndFlaky, "android flaky", "passed", 1, "", 2)
	mustRefresh(t, ctx, env.Pool, gPRMob2)

	// Prior-14d mobile run at -20d (gives mobile a prior 14d baseline of 0 flaky tests; passing so is_red=false!).
	// Wait: day20 is inside 30d, so keeping it green avoids altering failed_runs_count; we account for +1 total_runs (16 total, 15 completed)!
	gPRMobPrior := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "detox-pr", Status: "completed",
		Repo: secMobRepo, Branch: "pr-89", Commit: "sha-mob-prior", RunID: "u-mob-prior", Attempt: "1",
		PRNum: prPtr(89), Stage: "pr", CreatedAt: day20,
	})
	rMobP := insertReport(t, ctx, env.Pool, gPRMobPrior, "detox-ios", "ios")
	sMobP := insertSuite(t, ctx, env.Pool, rMobP, "MobPrior")
	insertCase(t, ctx, env.Pool, sMobP, tMobileSplit, "platform split", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRMobPrior)

	// 4. Desktop upload-only PR lane (excluded from KPI 5 percentiles, listed in upload_only_lane_names).
	gPRDeskUpload := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "desktop-pr-upload", Status: "completed",
		Repo: secDeskRepo, Branch: "pr-107", Commit: "sha-desk-pr", RunID: "u-desk-pr", Attempt: "1",
		PRNum: prPtr(107), Stage: "pr", CreatedAt: curDay,
	})
	rDU := insertReport(t, ctx, env.Pool, gPRDeskUpload, "desktop-pr-upload", "")
	sDU := insertSuite(t, ctx, env.Pool, rDU, "DeskPR")
	insertCase(t, ctx, env.Pool, sDU, tDesktopPass, "launch", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRDeskUpload)

	// 5. KPI 4 recurring failures supporting groups & test execution volume.
	gPriorSigActive := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-unit-pr", Status: "completed",
		Repo: secMMRepo, Branch: "pr-95", Commit: "sha-pr-95", RunID: "u-pr-95", Attempt: "1",
		PRNum: prPtr(95), Stage: "pr", CreatedAt: day20,
	})
	rSA := insertReport(t, ctx, env.Pool, gPriorSigActive, "e2e-unit-pr", "")
	sSA := insertSuite(t, ctx, env.Pool, rSA, "SigActivePrior")
	insertCaseWithSig(t, ctx, env.Pool, sSA, tActiveFail, sigActive, "active failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPriorSigActive)

	gQuiet := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-unit-pr", Status: "completed",
		Repo: secMMRepo, Branch: "pr-96", Commit: "sha-pr-96", RunID: "u-pr-96", Attempt: "1",
		PRNum: prPtr(96), Stage: "pr", CreatedAt: day10,
	})
	rQ := insertReport(t, ctx, env.Pool, gQuiet, "e2e-unit-pr", "")
	sQ := insertSuite(t, ctx, env.Pool, rQ, "Quiet")
	insertCaseWithSig(t, ctx, env.Pool, sQ, tQuietFail, sigQuiet, "quiet failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gQuiet)

	gLow := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-unit-pr", Status: "completed",
		Repo: secMMRepo, Branch: "pr-97", Commit: "sha-pr-97", RunID: "u-pr-97", Attempt: "1",
		PRNum: prPtr(97), Stage: "pr", CreatedAt: day10,
	})
	rL := insertReport(t, ctx, env.Pool, gLow, "e2e-unit-pr", "")
	sL := insertSuite(t, ctx, env.Pool, rL, "LowRuns")
	insertCaseWithSig(t, ctx, env.Pool, sL, tLowRunsFail, sigLowRuns, "low runs failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gLow)

	gResurfOld := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: secPWFramework, Name: "e2e-unit-pr", Status: "completed",
		Repo: secMMRepo, Branch: "pr-98", Commit: "sha-pr-98", RunID: "u-pr-98", Attempt: "1",
		PRNum: prPtr(98), Stage: "pr", CreatedAt: day20,
	})
	rRO := insertReport(t, ctx, env.Pool, gResurfOld, "e2e-unit-pr", "")
	sRO := insertSuite(t, ctx, env.Pool, rRO, "ResurfOld")
	insertCaseWithSig(t, ctx, env.Pool, sRO, tResurfFail, sigResurf, "resurfaced failure", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gResurfOld)

	// Add execution denominator rows for KPI 4 rates & Quiet/Not-enough-runs/Resurfaced transitions.
	addExecs := func(d time.Time, tid uuid.UUID, addRuns int) {
		t.Helper()
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO kpi_daily_test_executions (
				metric_date, repository, app, stage, lane, platform, test_id, run_count, pass_count
			) VALUES ($1, $2, $3, 'pr', 'e2e-unit-pr', '', $4, $5, $5)
			ON CONFLICT (metric_date, repository, stage, lane, platform, test_id)
			DO UPDATE SET run_count = kpi_daily_test_executions.run_count + EXCLUDED.run_count,
			              pass_count = kpi_daily_test_executions.pass_count + EXCLUDED.pass_count
		`, d.UTC().Format("2006-01-02"), secMMRepo, secAppMM, tid, addRuns); err != nil {
			t.Fatalf("seed execs: %v", err)
		}
	}
	// tActiveFail already has 4 runs at curDay (gMain1, gPR101, gPR101Rerun, gPR102) + 1 run at day20 (gPriorSigActive).
	// Add 96 runs at curDay (-> 100 runs in cur 14d) and 199 runs at day20 (-> 200 runs in prior 14d).
	addExecs(curDay, tActiveFail, 96)
	addExecs(day20, tActiveFail, 199)
	// tQuietFail: 500 runs in trailing 7d (-3d) -> exact >=500 Quiet boundary!
	addExecs(todayMidnight.AddDate(0, 0, -3), tQuietFail, 500)
	// tLowRunsFail: 499 runs in trailing 7d (-3d) -> exact <500 Not enough runs boundary!
	addExecs(todayMidnight.AddDate(0, 0, -3), tLowRunsFail, 499)
	// tResurfFail: 550 runs at -12d (inside quiet window between -20d and -3h) -> Resurfaced!
	addExecs(todayMidnight.AddDate(0, 0, -12), tResurfFail, 550)

	// 6. Seed 2 verdicts via POST /api/v1/verdicts (1 original + 1 reused) with sentinel secrets.
	const unifiedSecret = "ghp_UnifiedFixtureSecretDoNotLeak112233"
	postVerdict := func(rgID, tid, sid uuid.UUID, label string, reusedFrom *uuid.UUID) uuid.UUID {
		t.Helper()
		item := map[string]any{
			"test_id":      tid.String(),
			"signature_id": sid.String(),
			"label":        label,
			"decided_by":   "llm",
			"rule":         "sig-match",
			"confidence":   0.92,
			"summary":      "Unified fixture verdict summary",
			"model":        "claude-sonnet",
			"triage_url":   "https://github.com/mattermost/mattermost/actions/runs/101",
			"error_context": map[string]any{
				"token": unifiedSecret,
			},
		}
		if reusedFrom != nil {
			item["reused_from_id"] = reusedFrom.String()
		}
		c, raw := postJSONWithAPIKey(t, env.ServerURL+"/api/v1/verdicts", apiKey, map[string]any{
			"repository":      secMMRepo,
			"report_group_id": rgID.String(),
			"verdicts":        []any{item},
		})
		if c != http.StatusOK {
			t.Fatalf("POST /verdicts failed: %d %s", c, raw)
		}
		var out struct {
			Verdicts []struct {
				ID uuid.UUID `json:"id"`
			} `json:"verdicts"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Verdicts) != 1 {
			t.Fatalf("decode POST /verdicts: %v (%s)", err, raw)
		}
		return out.Verdicts[0].ID
	}
	v101 := postVerdict(gPR101, tActiveFail, sigActive, "flaky-infra", nil)
	_ = postVerdict(gPR104Unsettled, tResurfFail, sigResurf, "infra", &v101)

	// ---- Verify API: GET /api/v1/kpi/summary?window=30d ----
	sumRaw := getRaw(t, env.ServerURL+"/api/v1/kpi/summary?window=30d")
	assertNoSecretOrContextLeak(t, "unified /kpi/summary", sumRaw, []string{unifiedSecret}, true)
	var sum kpi.SummaryResponse
	if err := json.Unmarshal([]byte(sumRaw), &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	// KPI 1 assertions.
	if sum.MainPassRate.TestLevel.FirstAttemptPassed != 3 || sum.MainPassRate.TestLevel.TotalTests != 5 {
		t.Fatalf("KPI 1 test_level = %d/%d, want 3/5",
			sum.MainPassRate.TestLevel.FirstAttemptPassed, sum.MainPassRate.TestLevel.TotalTests)
	}
	assertNearPtr(t, "KPI 1 test pass_rate", sum.MainPassRate.TestLevel.PassRate, 60.0, 1e-6)
	assertNearPtr(t, "KPI 1 test prior", sum.MainPassRate.TestLevel.PriorPassRate, 50.0, 1e-6)
	assertNearPtr(t, "KPI 1 test delta_pp", sum.MainPassRate.TestLevel.DeltaPP, 10.0, 1e-6)
	if sum.MainPassRate.LaneLevel.PassedLanes != 2 || sum.MainPassRate.LaneLevel.TotalLanes != 3 ||
		sum.MainPassRate.LaneLevel.StrictPassedLanes != 1 || sum.MainPassRate.LaneLevel.StrictTotalLanes != 2 {
		t.Fatalf("KPI 1 lane_level = %+v", sum.MainPassRate.LaneLevel)
	}
	assertNearPtr(t, "KPI 1 lane pass_rate", sum.MainPassRate.LaneLevel.PassRate, 200.0/3.0, 1e-6)
	assertNearPtr(t, "KPI 1 strict pass_rate", sum.MainPassRate.LaneLevel.StrictPassRate, 50.0, 1e-6)
	assertNearPtr(t, "KPI 1 strict delta_pp", sum.MainPassRate.LaneLevel.StrictDeltaPP, 50.0, 1e-6)

	// KPI 2 assertions (on summary + /kpi/prs/red).
	if sum.PRFalseRed == nil || !sum.PRFalseRed.IsLowerBound || sum.PRFalseRed.RateQualifier != "at least" {
		t.Fatalf("KPI 2 metadata = %+v", sum.PRFalseRed)
	}
	if sum.PRFalseRed.SettledRedLanes != 3 || sum.PRFalseRed.SettledFalseRedLanes != 1 || sum.PRFalseRed.UnsettledRedLanes != 1 {
		t.Fatalf("KPI 2 counts = %+v, want 3/1/1", sum.PRFalseRed)
	}
	assertNearPtr(t, "KPI 2 false_red_rate", sum.PRFalseRed.FalseRedRate, 100.0/3.0, 1e-6)
	assertNearPtr(t, "KPI 2 prior_false_red_rate", sum.PRFalseRed.PriorFalseRedRate, 50.0, 1e-6)
	assertNearPtr(t, "KPI 2 delta_pp", sum.PRFalseRed.DeltaPP, 100.0/3.0-50.0, 1e-6)

	prsRed := getJSON[kpi.PRRedResponse](t, env.ServerURL+"/api/v1/kpi/prs/red?window=30d")
	if prsRed.SettledRedLanes != 3 || prsRed.SettledFalseRedLanes != 1 || prsRed.UnsettledRedLanes != 1 ||
		prsRed.ReasonMix.WentGreenOnRerun != 1 || prsRed.ReasonMix.FailingElsewhere != 1 || prsRed.ReasonMix.Other != 1 {
		t.Fatalf("GET /kpi/prs/red mismatch: %+v", prsRed)
	}

	// KPI 3 assertions (on summary + /kpi/flaky).
	if sum.FlakyTests.Count != 2 || sum.FlakyTests.PriorCount == nil || *sum.FlakyTests.PriorCount != 0 || sum.FlakyTests.Delta == nil || *sum.FlakyTests.Delta != 2 {
		t.Fatalf("KPI 3 flaky summary = %+v, want count=2 prior=0 delta=2", sum.FlakyTests)
	}
	flakyList := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=14d")
	if flakyList.TotalFlakyTests != 2 || len(flakyList.Tests) != 2 {
		t.Fatalf("GET /kpi/flaky = %+v, want 2 tests", flakyList)
	}

	// KPI 4 assertions (on summary + /kpi/failures + /kpi/failures/{id}).
	if sum.RecurringFailures == nil ||
		sum.RecurringFailures.TotalSignatures != 4 ||
		sum.RecurringFailures.ActiveCount != 1 ||
		sum.RecurringFailures.ResurfacedCount != 1 ||
		sum.RecurringFailures.QuietCount != 1 ||
		sum.RecurringFailures.NotEnoughRunsCount != 1 ||
		sum.RecurringFailures.TotalOccurrences != 8 ||
		sum.RecurringFailures.TriagedCount != 2 {
		t.Fatalf("KPI 4 summary = %+v, want 4/1/1/1/1/occ=8/triaged=2", sum.RecurringFailures)
	}
	failuresResp := getJSON[kpi.FailuresResponse](t, env.ServerURL+"/api/v1/kpi/failures?window=30d")
	if failuresResp.Summary != *sum.RecurringFailures {
		t.Fatalf("/kpi/failures summary %+v != /kpi/summary %+v", failuresResp.Summary, *sum.RecurringFailures)
	}
	activeDetail := getJSON[kpi.FailureDetailResponse](t, env.ServerURL+"/api/v1/kpi/failures/"+sigActive.String()+"?window=30d")
	if activeDetail.State != kpi.FailureStateActive || activeDetail.OccurrenceCount != 4 || activeDetail.AffectedTestRuns != 300 {
		t.Fatalf("sigActive detail = state:%s occ:%d runs:%d, want Active/4/300", activeDetail.State, activeDetail.OccurrenceCount, activeDetail.AffectedTestRuns)
	}
	assertNearPtr(t, "sigActive rate_per_1k", activeDetail.RatePer1k, 4000.0/300.0, 1e-6)
	assertNearPtr(t, "sigActive rate_per_1k_14d", activeDetail.RatePer1k14d, 30.0, 1e-6)
	assertNearPtr(t, "sigActive prior_rate_per_1k_14d", activeDetail.PriorRatePer1k14d, 5.0, 1e-6)
	assertNearPtr(t, "sigActive delta_rate_per_1k", activeDetail.DeltaRatePer1k, 25.0, 1e-6)

	// KPI 5 assertions.
	if sum.E2EDuration.SampleCount != 5 {
		t.Fatalf("KPI 5 sample_count = %d, want 5", sum.E2EDuration.SampleCount)
	}
	assertNearPtr(t, "KPI 5 p50_ms", sum.E2EDuration.P50Ms, 300000.0, 1e-6)
	assertNearPtr(t, "KPI 5 p90_ms", sum.E2EDuration.P90Ms, 520000.0, 1e-6)
	assertNearPtr(t, "KPI 5 prior_p50_ms", sum.E2EDuration.PriorP50Ms, 500000.0, 1e-6)
	assertNearPtr(t, "KPI 5 delta_p50_ms", sum.E2EDuration.DeltaP50Ms, -200000.0, 1e-6)
	wantUploadOnly := []string{"desktop-pr-upload", "e2e-unit-pr"}
	if len(sum.E2EDuration.LaneCoverage.UploadOnlyLaneNames) != len(wantUploadOnly) {
		t.Fatalf("KPI 5 upload-only coverage = %+v, want %v", sum.E2EDuration.LaneCoverage, wantUploadOnly)
	}
	for i, w := range wantUploadOnly {
		if sum.E2EDuration.LaneCoverage.UploadOnlyLaneNames[i] != w {
			t.Fatalf("KPI 5 upload-only[%d] = %q, want %q", i, sum.E2EDuration.LaneCoverage.UploadOnlyLaneNames[i], w)
		}
	}

	// Supporting Triage assertions (16 total runs: 15 non-timed-out completed + 1 timed_out = 16; completion_rate = 15/16 * 100 = 93.75%).
	if sum.Triage == nil {
		t.Fatal("expected non-nil sum.Triage")
	}
	tr := sum.Triage
	if tr.FailedRunsCount != 11 || tr.TriagedRunsCount != 2 || tr.FailedTestOccurrences != 10 || tr.TriagedTestOccurrences != 2 {
		t.Fatalf("triage counts = %+v", tr)
	}
	assertNearPtr(t, "run_coverage_rate", tr.RunCoverageRate, 200.0/11.0, 1e-6)
	assertNearPtr(t, "test_coverage_rate", tr.TestCoverageRate, 20.0, 1e-6)
	assertNearPtr(t, "verdict_reuse_rate", tr.VerdictReuseRate, 50.0, 1e-6)
	if tr.RunCompletion.TotalRuns != 16 || tr.RunCompletion.CompletedRuns != 15 || tr.RunCompletion.TimedOutRuns != 1 {
		t.Fatalf("run_completion = %+v, want total=16 completed=15 timed_out=1", tr.RunCompletion)
	}
	assertNearPtr(t, "completion_rate", tr.RunCompletion.CompletionRate, 1500.0/16.0, 1e-6)

	// ---- Verify GET /api/v1/tests/{id}/state (flaky, new, known) ----
	stFlaky := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tMainFlaky.String()+"/state")
	if stFlaky.State != kpi.TestStateFlaky || !stFlaky.IsFlaky {
		t.Fatalf("tMainFlaky state = %+v, want flaky", stFlaky)
	}
	stNew := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tLowRunsFail.String()+"/state")
	if stNew.State != kpi.TestStateNew || stNew.IsFlaky {
		t.Fatalf("tLowRunsFail state = %+v, want new", stNew)
	}
	stKnown := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tMobileSplit.String()+"/state")
	if stKnown.State != kpi.TestStateKnown || stKnown.IsFlaky {
		t.Fatalf("tMobileSplit state = %+v, want known (not flaky)", stKnown)
	}

	// ---- Verify ALL 3 SSR HTML routes + guardrails + URL-persisted filters ----
	ovHTML := getRaw(t, env.ServerURL+"/kpi")
	assertNoSecretOrContextLeak(t, "unified SSR /kpi", ovHTML, []string{unifiedSecret}, true)
	for _, want := range []string{
		`60.0%`,
		`at least 33.3%`,
		`5m`,
		`8m 40s`,
		`18.2%`,
		`20.0%`,
		`50.0%`,
		`93.8%`,
		`Active`,
		`Quiet (10d)`,
		`Not enough runs`,
		`Resurfaced`,
		`Lane coverage:`,
	} {
		if !strings.Contains(ovHTML, want) {
			t.Fatalf("unified SSR /kpi missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(ovHTML), "blocked") || strings.Contains(ovHTML, "Fixed") {
		t.Fatal("unified SSR /kpi violated copy guardrails")
	}

	prsHTML := getRaw(t, env.ServerURL+"/kpi/prs")
	assertNoSecretOrContextLeak(t, "unified SSR /kpi/prs", prsHTML, []string{unifiedSecret}, true)
	for _, want := range []string{
		`at least 33.3%`,
		`Went green on rerun`,
		`Also failing elsewhere`,
		`E2E red`,
		`#104`,
		`Lane coverage:`,
	} {
		if !strings.Contains(prsHTML, want) {
			t.Fatalf("unified SSR /kpi/prs missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(prsHTML), "blocked") || strings.Contains(prsHTML, "Fixed") {
		t.Fatal("unified SSR /kpi/prs violated copy guardrails")
	}

	detailHTML := getRaw(t, env.ServerURL+"/kpi/failures/"+sigActive.String())
	assertNoSecretOrContextLeak(t, "unified SSR /kpi/failures/{id}", detailHTML, []string{unifiedSecret}, true)
	for _, want := range []string{
		`TimeoutError`,
		`waiting for #login-btn`,
		`30.0 / 1k`,
		`5.0 / 1k`,
		`&#43;25.0 / 1k`,
		`flaky-infra`,
		`Unified fixture verdict summary`,
		`Active`,
	} {
		if !strings.Contains(detailHTML, want) {
			t.Fatalf("unified SSR /kpi/failures/{id} missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(detailHTML), "blocked") || strings.Contains(detailHTML, "Fixed") {
		t.Fatal("unified SSR /kpi/failures/{id} violated copy guardrails")
	}

	// Verify URL-persisted filters across all 3 SSR routes.
	ovFiltered := getRaw(t, env.ServerURL+"/kpi?app=mattermost&window=14d&sort=rate_desc")
	for _, want := range []string{
		`<option value="mattermost" selected>`,
		`<option value="14d" selected>`,
		`<option value="rate_desc" selected>`,
		`href="/kpi/prs?app=mattermost&amp;window=14d"`,
	} {
		if !strings.Contains(ovFiltered, want) {
			t.Fatalf("filtered SSR /kpi missing %q", want)
		}
	}
	prsFiltered := getRaw(t, env.ServerURL+"/kpi/prs?app=mattermost&window=14d&sort=age_desc")
	for _, want := range []string{
		`<option value="mattermost" selected>`,
		`<option value="14d" selected>`,
		`<option value="age_desc" selected>`,
		`href="/kpi?app=mattermost&amp;window=14d"`,
	} {
		if !strings.Contains(prsFiltered, want) {
			t.Fatalf("filtered SSR /kpi/prs missing %q", want)
		}
	}
	detailFiltered := getRaw(t, env.ServerURL+"/kpi/failures/"+sigActive.String()+"?app=mattermost&window=14d")
	if !strings.Contains(detailFiltered, `href="/kpi?app=mattermost&amp;window=14d"`) {
		t.Fatalf("filtered SSR /kpi/failures/{id} missing back-link filter preservation")
	}
}

func postJSONStatus(t *testing.T, url, bearer string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", url, err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new req %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s: %v", url, err)
	}
	return resp.StatusCode, out
}

func postJSONWithAPIKey(t *testing.T, url, apiKey string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", url, err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new req %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s: %v", url, err)
	}
	return resp.StatusCode, out
}

func assertNoSecretOrContextLeak(t *testing.T, label, body string, sentinels []string, forbidRedactedToken bool) {
	t.Helper()
	for _, s := range sentinels {
		if strings.Contains(body, s) {
			t.Fatalf("%s leaked sentinel secret %q", label, s)
		}
	}
	if strings.Contains(body, "error_context") {
		t.Fatalf("%s leaked error_context field/string", label)
	}
	if forbidRedactedToken && strings.Contains(body, "[REDACTED]") {
		t.Fatalf("%s leaked [REDACTED] error_context placeholder", label)
	}
}

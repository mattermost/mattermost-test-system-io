package reports

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	summaryDefaultStreakRuns = 40
	summaryMaxStreakRuns     = 200
	summaryDefaultRateDays   = 7
	summaryMaxRateDays       = 30
	summaryMaxCompareNames   = 5
	summaryPerPage           = 500
	statusAbsent             = "absent"
)

var commitPrefixRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// summaryRequest names one run — a lane's report group(s) for a commit, GitHub run and attempt — and
// how much of the lane's history to summarize for the tests that failed in it.
type summaryRequest struct {
	repository, branch, name, commit, ghRunID, ghRunAttempt string
	compareNames                                            []string
	streakRuns, rateDays, page, perPage                     int
}

// parseSummaryRequest validates the query string; the second result is a 400 message, or "".
func parseSummaryRequest(q url.Values) (summaryRequest, string) {
	req := summaryRequest{
		repository:   strings.TrimSpace(q.Get("repository")),
		branch:       strings.TrimSpace(q.Get("branch")),
		name:         strings.TrimSpace(q.Get("name")),
		commit:       strings.ToLower(strings.TrimSpace(q.Get("commit"))),
		ghRunID:      strings.TrimSpace(q.Get("gh_run_id")),
		ghRunAttempt: strings.TrimSpace(q.Get("gh_run_attempt")),
	}
	if req.repository == "" || req.branch == "" || req.name == "" || req.ghRunID == "" || req.ghRunAttempt == "" {
		return req, "repository, branch, name, commit, gh_run_id and gh_run_attempt are required"
	}
	if !commitPrefixRE.MatchString(req.commit) {
		return req, "commit must be 7 to 40 hexadecimal characters"
	}
	if n, err := strconv.Atoi(req.ghRunAttempt); err != nil || n < 1 {
		return req, "gh_run_attempt must be a positive integer"
	}
	var bad string
	if req.streakRuns, bad = intParam(q, "streak_runs", summaryDefaultStreakRuns, 1, summaryMaxStreakRuns); bad != "" {
		return req, bad
	}
	if req.rateDays, bad = intParam(q, "rate_days", summaryDefaultRateDays, 1, summaryMaxRateDays); bad != "" {
		return req, bad
	}
	if req.page, bad = intParam(q, "page", 1, 1, 1<<20); bad != "" {
		return req, bad
	}
	if req.perPage, bad = intParam(q, "per_page", summaryPerPage, 1, summaryPerPage); bad != "" {
		return req, bad
	}
	for _, v := range q["compare_names"] {
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(n); n != "" && n != req.name && !slices.Contains(req.compareNames, n) {
				req.compareNames = append(req.compareNames, n)
			}
		}
	}
	if len(req.compareNames) > summaryMaxCompareNames {
		return req, "at most 5 compare_names"
	}
	return req, ""
}

func intParam(q url.Values, key string, dflt, lo, hi int) (int, string) {
	v := q.Get(key)
	if v == "" {
		return dflt, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, key + " must be an integer from " + strconv.Itoa(lo) + " to " + strconv.Itoa(hi)
	}
	return n, ""
}

// runGroup is one report group: a lane's run for a commit, GitHub run and attempt.
type runGroup struct {
	ID           uuid.UUID
	Name         string
	CommitSHA    string
	GHRunID      string
	GHRunAttempt string
	CreatedAt    time.Time
}

// caseRow is one try of one test in one shard report of a group.
type caseRow struct {
	GroupID    uuid.UUID
	ReportID   uuid.UUID
	ShardLabel string
	File       string
	FullTitle  string // "suite › test", as the consolidated report titles it
	Status     string
}

type testKey struct{ File, FullTitle string }

// runRef is an earlier run of the lane and the test's status in it.
type runRef struct {
	CommitSHA    string    `json:"commit_sha"`
	GHRunID      string    `json:"gh_run_id"`
	GHRunAttempt string    `json:"gh_run_attempt"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// windowCounts counts the lane's runs in the window by the test's status in each; absent means the run
// didn't execute the test (another selection, or a shard that never reported).
type windowCounts struct {
	Runs    int `json:"runs"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Flaky   int `json:"flaky"`
	Skipped int `json:"skipped"`
	Absent  int `json:"absent"`
}

// sameCommitRun is the test's status in another run of the same commit: a re-run of the lane, or a
// compared lane such as the other edition.
type sameCommitRun struct {
	Name         string `json:"name"`
	GHRunID      string `json:"gh_run_id"`
	GHRunAttempt string `json:"gh_run_attempt"`
	Status       string `json:"status"`
}

// testSummary holds the history facts for one test that failed or was flaky in the run. Facts only:
// what to conclude from them is the caller's policy.
type testSummary struct {
	File        string `json:"file"`
	FullTitle   string `json:"full_title"`
	StatusInRun string `json:"status_in_run"`
	// EarlierConsecutiveFailures counts the lane's runs before this one, newest first, in which the
	// test failed without a run in between where it didn't; runs that didn't execute it are skipped.
	EarlierConsecutiveFailures int `json:"earlier_consecutive_failures"`
	// FirstFailedCommit is the oldest commit of that streak, or the run's own commit.
	FirstFailedCommit string `json:"first_failed_commit"`
	// LastNotFailed is the newest earlier run where the test ran and didn't fail; null when none of
	// the scanned runs qualifies.
	LastNotFailed *runRef         `json:"last_not_failed"`
	Window        windowCounts    `json:"window"`
	SameCommit    []sameCommitRun `json:"same_commit"`
}

// summarize computes the facts for every test that failed or was flaky in the run's groups. lane holds
// the lane's earlier groups, newest first; those created at or after windowStart are counted in the
// window. A test's status in a group is rolled up exactly as the consolidated report does
// (rollupTitleStatus). The result is sorted by file and title.
func summarize(run, lane []runGroup, windowStart time.Time, sameCommit []runGroup, rows []caseRow) []testSummary {
	byGroup := map[uuid.UUID]map[testKey][]titleCase{}
	for _, r := range rows {
		k := testKey{r.File, r.FullTitle}
		if byGroup[r.GroupID] == nil {
			byGroup[r.GroupID] = map[testKey][]titleCase{}
		}
		byGroup[r.GroupID][k] = append(byGroup[r.GroupID][k], titleCase{ReportID: r.ReportID, ShardLabel: r.ShardLabel, Status: r.Status})
	}
	statusIn := func(groupID uuid.UUID, k testKey) string {
		cases := byGroup[groupID][k]
		if len(cases) == 0 {
			return statusAbsent
		}
		return rollupTitleStatus(cases)
	}

	// The run's own status: its groups' tries together (a run family can span several groups).
	runCases := map[testKey][]titleCase{}
	for _, g := range run {
		for k, cases := range byGroup[g.ID] {
			runCases[k] = append(runCases[k], cases...)
		}
	}
	keys := make([]testKey, 0)
	statusInRun := map[testKey]string{}
	for k, cases := range runCases {
		if s := rollupTitleStatus(cases); statusIsFail(s) || s == statusFlaky {
			keys = append(keys, k)
			statusInRun[k] = s
		}
	}
	slices.SortFunc(keys, func(a, b testKey) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return strings.Compare(a.FullTitle, b.FullTitle)
	})

	runCommit := ""
	if len(run) > 0 {
		runCommit = run[0].CommitSHA
	}
	out := make([]testSummary, 0, len(keys))
	for _, k := range keys {
		s := testSummary{File: k.File, FullTitle: k.FullTitle, StatusInRun: statusInRun[k], FirstFailedCommit: runCommit, SameCommit: []sameCommitRun{}}

		streakOpen := true
		for _, g := range lane {
			st := statusIn(g.ID, k)
			if !g.CreatedAt.Before(windowStart) {
				s.Window.Runs++
				switch {
				case statusIsFail(st):
					s.Window.Failed++
				case st == statusFlaky:
					s.Window.Flaky++
				case st == statusPassed:
					s.Window.Passed++
				case st == statusSkipped:
					s.Window.Skipped++
				default:
					s.Window.Absent++
				}
			}
			if !streakOpen || st == statusAbsent || st == statusSkipped {
				continue
			}
			if statusIsFail(st) {
				s.EarlierConsecutiveFailures++
				s.FirstFailedCommit = g.CommitSHA
				continue
			}
			s.LastNotFailed = &runRef{CommitSHA: g.CommitSHA, GHRunID: g.GHRunID, GHRunAttempt: g.GHRunAttempt, Status: st, CreatedAt: g.CreatedAt}
			streakOpen = false
		}

		for _, g := range sameCommit {
			s.SameCommit = append(s.SameCommit, sameCommitRun{Name: g.Name, GHRunID: g.GHRunID, GHRunAttempt: g.GHRunAttempt, Status: statusIn(g.ID, k)})
		}
		out = append(out, s)
	}
	return out
}

// HistorySummary serves GET /api/v1/reports/history/summary: for one run, the history facts of every
// test that failed or was flaky in it — streak, last run where it didn't fail, counts over a window,
// and its status in the other runs of the same commit — as of the run. It answers in one request what
// would otherwise be a history read per spec file and per question.
//
// Work is bounded by the request: the run's groups; at most streak_runs earlier groups of the lane plus
// those in the rate_days window; the same commit's groups of the lane and compare_names. Case rows are
// read only for those groups and, outside the run, only for the spec files of the page's tests.
//
// Measured on Postgres 18 with TestHistorySummaryBenchmark's corpus (3,600 groups in 10 lanes over 60
// days; 1.44M suites; 4.32M test cases), 20 requests each, through HTTP:
//
//	run with 50 failing tests     p50 46ms, p95 73ms
//	run with 500 failing tests    p50 81ms, p95 83ms
//
// The widest query — case rows of 40 lane groups for 50 spec files — runs in about 9ms on the existing
// indexes (suites_report_parent_order_idx, test_cases_suite_order_idx), so the endpoint adds none. Its
// plan reads reports with a parallel sequential scan (about 1.5ms over 72,000 rows); that grows with the
// table, so re-run the benchmark (TSIO_BENCH=1) if reports grows by an order of magnitude.
func (h *Handlers) HistorySummary(w http.ResponseWriter, r *http.Request) {
	req, bad := parseSummaryRequest(r.URL.Query())
	if bad != "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", bad)
		return
	}

	// The repository is matched the way the consolidated report matches it: the full "owner/repo",
	// or the slug alone ("mattermost"), via report_groups_repo_suffix_idx.
	const groupCols = `id, name, commit_sha, gh_run_id, gh_run_attempt, created_at`
	const repoAndBranch = `(g.repository = $1 OR split_part(g.repository, '/', 2) = $1) AND g.branch = $2`
	run, err := h.queryGroups(r, `
		SELECT `+groupCols+` FROM report_groups g
		WHERE `+repoAndBranch+`
		  AND g.commit_sha LIKE $3 || '%'
		  AND (g.run_group = $4 OR g.name = $4)
		  AND g.gh_run_id = $5 AND g.gh_run_attempt = $6
		ORDER BY g.created_at, g.id`,
		req.repository, req.branch, req.commit, req.name, req.ghRunID, req.ghRunAttempt)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if len(run) == 0 {
		api.WriteErrorCode(w, http.StatusNotFound, "RUN_NOT_FOUND", "no report group for this run")
		return
	}
	until := run[0].CreatedAt
	windowStart := until.Add(-time.Duration(req.rateDays) * 24 * time.Hour)
	runIDs := make([]uuid.UUID, len(run))
	for i, g := range run {
		runIDs[i] = g.ID
	}

	// Earlier finished groups of the lane: the newest streak_runs, plus every one in the window.
	lane, err := h.queryGroups(r, `
		SELECT `+groupCols+` FROM (
			(SELECT `+groupCols+` FROM report_groups g
			 WHERE `+repoAndBranch+` AND (g.run_group = $3 OR g.name = $3)
			   AND g.status IN ('completed', 'incomplete') AND g.created_at < $4
			 ORDER BY g.created_at DESC, g.id LIMIT $5)
			UNION
			(SELECT `+groupCols+` FROM report_groups g
			 WHERE `+repoAndBranch+` AND (g.run_group = $3 OR g.name = $3)
			   AND g.status IN ('completed', 'incomplete') AND g.created_at < $4 AND g.created_at >= $6)
		) lane
		ORDER BY created_at DESC, id`,
		req.repository, req.branch, req.name, until, req.streakRuns, windowStart)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	// Other finished groups of the same commit in this lane (re-runs) and the compared lanes.
	names := append([]string{req.name}, req.compareNames...)
	sameCommit, err := h.queryGroups(r, `
		SELECT `+groupCols+` FROM report_groups g
		WHERE `+repoAndBranch+`
		  AND g.commit_sha LIKE $3 || '%'
		  AND (g.run_group = ANY($4) OR g.name = ANY($4))
		  AND g.status IN ('completed', 'incomplete')
		  AND NOT (g.id = ANY($5))
		ORDER BY g.name, g.created_at, g.id`,
		req.repository, req.branch, req.commit, names, runIDs)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	runRows, err := h.queryCaseRows(r, runIDs, nil)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	// The run's failing tests, paged; only their spec files are read from the other groups.
	all := summarize(run, nil, windowStart, nil, runRows)
	first := (req.page - 1) * req.perPage
	pageTests := all[min(first, len(all)):min(first+req.perPage, len(all))]
	files := make([]string, 0)
	for _, t := range pageTests {
		if !slices.Contains(files, t.File) {
			files = append(files, t.File)
		}
	}
	tests := []testSummary{}
	if len(pageTests) > 0 {
		otherIDs := make([]uuid.UUID, 0, len(lane)+len(sameCommit))
		for _, g := range lane {
			otherIDs = append(otherIDs, g.ID)
		}
		for _, g := range sameCommit {
			otherIDs = append(otherIDs, g.ID)
		}
		otherRows, err := h.queryCaseRows(r, otherIDs, files)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		wanted := map[testKey]bool{}
		for _, t := range pageTests {
			wanted[testKey{t.File, t.FullTitle}] = true
		}
		rows := make([]caseRow, 0, len(otherRows))
		for _, row := range runRows {
			if wanted[testKey{row.File, row.FullTitle}] {
				rows = append(rows, row)
			}
		}
		rows = append(rows, otherRows...)
		tests = summarize(run, lane, windowStart, sameCommit, rows)
	}

	groupIDs := make([]string, len(run))
	for i, id := range runIDs {
		groupIDs[i] = id.String()
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{
		"run": map[string]any{
			"group_ids":      groupIDs,
			"commit_sha":     run[0].CommitSHA,
			"gh_run_id":      req.ghRunID,
			"gh_run_attempt": req.ghRunAttempt,
			"created_at":     until,
		},
		"until":        until,
		"window_start": windowStart,
		"streak_runs":  req.streakRuns,
		"rate_days":    req.rateDays,
		"lane_runs":    len(lane),
		"total_tests":  len(all),
		"page":         req.page,
		"per_page":     req.perPage,
		"has_more":     first+req.perPage < len(all),
		"tests":        tests,
	})
}

func (h *Handlers) queryGroups(r *http.Request, sql string, args ...any) ([]runGroup, error) {
	rows, err := h.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]runGroup, 0)
	for rows.Next() {
		var g runGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CommitSHA, &g.GHRunID, &g.GHRunAttempt, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// queryCaseRows reads every try in the groups, limited to the spec files when files is not nil. The
// title is built as the consolidated report builds it, so both name a test the same way.
func (h *Handlers) queryCaseRows(r *http.Request, groupIDs []uuid.UUID, files []string) ([]caseRow, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT rp.report_group_id, rp.id, COALESCE(NULLIF(rp.gh_job_name, ''), rp.name),
		       COALESCE(s.file, ''), COALESCE(s.title, '') || ' › ' || tc.title, tc.status
		FROM reports rp
		JOIN suites s ON s.report_id = rp.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE rp.report_group_id = ANY($1)
		  AND ($2::text[] IS NULL OR s.file = ANY($2))`,
		groupIDs, files)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]caseRow, 0)
	for rows.Next() {
		var c caseRow
		if err := rows.Scan(&c.GroupID, &c.ReportID, &c.ShardLabel, &c.File, &c.FullTitle, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

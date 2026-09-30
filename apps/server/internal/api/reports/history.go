package reports

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	historyMaxFiles       = 50
	historyMaxWindow      = 30 * 24 * time.Hour
	historyMaxRuns        = 200
	historyDefaultPerPage = 50
	historyMaxPerPage     = 2000
	historyErrorChars     = 400
)

// historyRequest names the spec files whose past executions the caller wants.
//
// It is keyed on the file rather than on individual test titles on purpose. A
// title is edited far more often than the file it lives in — a reworded test is
// still the same test — and a caller keyed on the title would silently lose all
// of a test's history the moment somebody fixed a typo in it. The server
// therefore returns every execution recorded against the named files and leaves
// the caller to decide which rows describe the same test, on whatever rule it
// considers correct. That keeps this endpoint a plain read over the report
// tables, with no opinion about how the data will be interpreted.
//
// The set of runs to read from is named in one of two ways, and the caller picks
// whichever matches the question it is asking:
//
//   - `since`/`until` — a time window. Right for questions about recent
//     activity across many branches, such as "did this test fail on three other
//     PRs in the last fortnight", where the fortnight is the point.
//   - `runs` — the newest N runs of these files, however long ago they ran.
//     Right for questions about one branch's own history, such as "did the last
//     five runs of this lane on trunk pass". A wall-clock window cannot express
//     that: a branch that runs rarely — a release or ESR branch between
//     cherry-picks — has no runs at all inside any window short enough to be
//     cheap, so a windowed caller sees an empty history and can conclude
//     nothing. `runs` has no time bound for exactly that reason.
//
// The two are mutually exclusive, because a request carrying both would have to
// silently drop one of them.
//
// `report` narrows to one report inside each group, by prefix on the report
// name. A run that uploads one report per platform records them all under a
// single group, so without it a caller cannot tell a failure on one platform
// from the same test failing on another, and history from an unrelated platform
// answers a question it has no business answering.
type historyRequest struct {
	Repository string   `json:"repository"`
	Files      []string `json:"files"`
	Branch     string   `json:"branch"`
	Since      string   `json:"since"`
	Until      string   `json:"until"`
	Runs       int      `json:"runs"`
	Report     string   `json:"report"`
	Page       int      `json:"page"`
	PerPage    int      `json:"per_page"`
}

// historyObservation is one execution of one test in one completed report group.
type historyObservation struct {
	File         string    `json:"file"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	RetryCount   int       `json:"retry_count"`
	GroupID      uuid.UUID `json:"group_id"`
	Name         string    `json:"name"`
	ReportName   string    `json:"report_name"`
	SuiteTitle   string    `json:"suite_title"`
	Branch       string    `json:"branch"`
	GHPRNumber   *int      `json:"gh_pr_number"`
	CommitSHA    string    `json:"commit_sha"`
	CreatedAt    time.Time `json:"created_at"`
	ErrorExcerpt string    `json:"error_excerpt"`
}

// History serves POST /api/v1/reports/history: every execution recorded against
// the named spec files, in completed report groups of one repository, newest
// first and paged. The runs to read are named either as a time window
// (`since`/`until`) or as a count (`runs`); see historyRequest.
//
// It is the read that CI triage needs to tell "this test fails on trunk / on
// other PRs too" from "this test fails only here". It reads the ordinary report
// tables, so it needs no extra bookkeeping at ingest, and it returns rows rather
// than verdicts, so the policy for reading them can change without a server
// deploy.
// historyQuery is a validated history request: the filters, plus exactly one of
// the two ways to name the set of runs — `since` for a window, `groupLimit` for
// a count. Only validate produces one, so a query in hand has already had every
// check below applied to it.
type historyQuery struct {
	repository string
	files      []string
	branch     string
	report     string
	until      time.Time
	since      *time.Time
	groupLimit *int
	runs       int
	page       int
	perPage    int
}

// validate checks a decoded request and resolves the defaults. A non-empty
// second return is the BAD_REQUEST message for the caller, and the query is
// unusable. Writing the response is left to the handler, which keeps this a
// pure function of the request and the handler itself off gocyclo's threshold —
// per-step error checking on a request with this many optional fields does not
// fit in one function alongside the query and the rendering.
func (req historyRequest) validate() (historyQuery, string) {
	if req.Repository == "" || len(req.Files) == 0 {
		return historyQuery{}, "repository and at least one file are required"
	}
	// The filter in the query is a set membership test, so a repeated file costs
	// nothing and cannot duplicate rows. Fold repeats away instead of making
	// the caller deduplicate a list it may have built from several failures in
	// the same spec.
	// Bound the array the caller actually sent, which is what the schema's
	// maxItems describes; deduplicating first would silently accept a longer one.
	if len(req.Files) > historyMaxFiles {
		return historyQuery{}, fmt.Sprintf("at most %d files per request", historyMaxFiles)
	}
	files := make([]string, 0, len(req.Files))
	seen := make(map[string]bool, len(req.Files))
	for _, f := range req.Files {
		if f == "" {
			return historyQuery{}, "files must not contain an empty path"
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	page := req.Page
	if page == 0 {
		page = 1
	}
	if page < 1 {
		return historyQuery{}, "page must be 1 or greater"
	}
	perPage := req.PerPage
	if perPage == 0 {
		perPage = historyDefaultPerPage
	}
	if perPage < 1 || perPage > historyMaxPerPage {
		return historyQuery{}, fmt.Sprintf("per_page must be between 1 and %d", historyMaxPerPage)
	}
	until := time.Now().UTC()
	if req.Until != "" {
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			return historyQuery{}, "until must be RFC3339"
		}
		until = t
	}
	// OFFSET needs the same result set on every page. Defaulting `until` to now
	// would move the set between requests, so a group completing mid-walk could
	// shift rows across a page boundary and the caller would see a row twice or
	// not at all. This holds in both modes: `until` bounds the window in one and
	// pins which runs are the newest N in the other. Later pages must therefore
	// name the `until` the first page reported.
	if page > 1 && req.Until == "" {
		return historyQuery{}, "until is required when page > 1: pass the value the first page returned so every page sees the same set of runs"
	}
	if req.Runs < 0 || req.Runs > historyMaxRuns {
		return historyQuery{}, fmt.Sprintf("runs must be between 1 and %d", historyMaxRuns)
	}
	// One of the two ways to name the runs, never both. Checked after the range
	// so an out-of-range count reports that, rather than blaming the window.
	if req.Runs != 0 && req.Since != "" {
		return historyQuery{}, "since and runs are mutually exclusive: a window and a run count name the set of runs two different ways"
	}
	q := historyQuery{
		repository: req.Repository,
		files:      files,
		branch:     req.Branch,
		report:     req.Report,
		until:      until,
		runs:       req.Runs,
		page:       page,
		perPage:    perPage,
	}
	// Exactly one of these two reaches the query as a non-NULL parameter, and
	// that is what selects the mode. `groupLimit` set means the newest N runs
	// with no lower time bound; `since` set means every run in [since, until).
	if req.Runs > 0 {
		// A local, not &q.runs: q is returned by value, so a pointer into it
		// would alias this frame's copy rather than the caller's.
		runs := req.Runs
		q.groupLimit = &runs
		return q, ""
	}
	from := until.Add(-14 * 24 * time.Hour)
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339, req.Since)
		if err != nil {
			return historyQuery{}, "since must be RFC3339"
		}
		from = t
	}
	if !from.Before(until) || until.Sub(from) > historyMaxWindow {
		return historyQuery{}, "window must be positive and at most 30 days"
	}
	q.since = &from
	return q, ""
}

// History serves POST /api/v1/reports/history: every execution recorded against
// the named spec files, in completed report groups of one repository, newest
// first and paged. The runs to read are named either as a time window
// (`since`/`until`) or as a count (`runs`); see historyRequest.
//
// It is the read that CI triage needs to tell "this test fails on trunk / on
// other PRs too" from "this test fails only here". It reads the ordinary report
// tables, so it needs no extra bookkeeping at ingest, and it returns rows rather
// than verdicts, so the policy for reading them can change without a server
// deploy.
func (h *Handlers) History(w http.ResponseWriter, r *http.Request) {
	var req historyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	q, badRequest := req.validate()
	if badRequest != "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", badRequest)
		return
	}
	// The groups to read are chosen first, in their own CTE, and the case rows
	// are then collected from those groups. In count mode that is what bounds
	// the work: the walk goes newest-first along
	// report_groups_repository_created_at_idx and stops as soon as LIMIT groups
	// have matched, so a file with history costs about LIMIT index probes
	// however long the table is. In window mode there is no LIMIT, the planner
	// flattens the CTE and drives off suites_pkey instead; that is its choice
	// to make and it is cheap enough.
	//
	// Measured on Postgres 18.3 against production scale (19,800 report groups,
	// 5.25M suites), with no index on suites.file:
	//
	//   window mode                                 20ms
	//   runs mode, file has history, branch-scoped    3ms
	//   runs mode, file has history, no branch       10ms
	//   runs mode, no history at all, branch-scoped 498ms
	//   runs mode, no history at all, no branch    1753ms
	//
	// So an index on suites.file is not load-bearing for either mode: with one
	// present the window plan is unchanged and the planner does not even choose
	// it, and the slow case above improves only to 1544ms. That case is a file
	// list with no history — a new or renamed spec — where the walk probes every
	// candidate group without finding LIMIT of them. Only bounding the candidate
	// set would fix it, which is not worth a knob: this is a CI-triggered read
	// with a 120s client timeout, the intended callers scope by branch, and the
	// answer is an empty history, which leaves the caller blocking. That is the
	// safe direction.
	//
	// The figures come from a synthetic corpus where every group carries the
	// same 265 spec files, so real selectivity will differ; they bound the shape
	// of the plan, not the exact cost.
	//
	// Both ORDER BYs are total orders — id breaks ties in the CTE, and group,
	// suite and case id break every tie outside it — so neither a group nor a
	// row can shift between pages while the caller walks them. One row past the
	// page is requested only to answer has_more without a second count query
	// over the same join.
	rows, err := h.Pool.Query(r.Context(), `
		WITH sel AS (
			SELECT id, name, branch, gh_pr_number, commit_sha, created_at
			FROM report_groups
			WHERE repository = $1
			  AND status = 'completed'
			  AND created_at < $2
			  AND ($3::timestamptz IS NULL OR created_at >= $3)
			  AND ($4 = '' OR branch = $4)
			  AND EXISTS (
				  SELECT 1
				  FROM reports r
				  JOIN suites s ON s.report_id = r.id
				  WHERE r.report_group_id = report_groups.id
				    AND s.file = ANY($5::text[])
				    AND ($10 = '' OR starts_with(r.name, $10))
			  )
			ORDER BY created_at DESC, id
			LIMIT $6
		)
		SELECT s.file, COALESCE(s.title, ''), COALESCE(r.name, ''), c.title, c.status, c.retry_count,
		       g.id, g.name, g.branch, g.gh_pr_number, g.commit_sha, g.created_at,
		       left(COALESCE(c.error_message, ''), $7)
		FROM sel g
		JOIN reports r ON r.report_group_id = g.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases c ON c.suite_id = s.id
		WHERE s.file = ANY($5::text[])
		  AND ($10 = '' OR starts_with(r.name, $10))
		ORDER BY g.created_at DESC, g.id, s.id, c.ordinal, c.id
		LIMIT $8 OFFSET $9
	`, q.repository, q.until, q.since, q.branch, q.files, q.groupLimit, historyErrorChars, q.perPage+1, (q.page-1)*q.perPage, q.report)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]historyObservation, 0)
	for rows.Next() {
		var o historyObservation
		if err := rows.Scan(&o.File, &o.SuiteTitle, &o.ReportName, &o.Title, &o.Status, &o.RetryCount, &o.GroupID, &o.Name, &o.Branch, &o.GHPRNumber, &o.CommitSHA, &o.CreatedAt, &o.ErrorExcerpt); err != nil {
			api.WriteError(w, r, err)
			return
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, r, err)
		return
	}
	hasMore := len(out) > q.perPage
	if hasMore {
		out = out[:q.perPage]
	}
	// `since` is null in count mode rather than backfilled from the rows on this
	// page: the oldest row of page 2 is not the floor of the set, so any value
	// derived per page would describe a different window on every page. Null
	// says what is true — the set has no lower time bound — and `runs` echoes
	// what bounded it instead.
	writeJSON(w, http.StatusOK, map[string]any{
		"since":        q.since,
		"until":        q.until,
		"runs":         q.runs,
		"report":       q.report,
		"page":         q.page,
		"per_page":     q.perPage,
		"has_more":     hasMore,
		"observations": out,
	})
}

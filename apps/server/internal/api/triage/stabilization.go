// W5 + W14 + W15c — the stabilization queue and the guards that feed it.
//
// W14: the queue is derived (flakiness leaderboard top-N, amnesty-expired
// promoted to the top) plus recorded promotions. The agent loop itself is
// action-side (mattermost CI) and lands with rollout Phase 3; the server owns
// the ranking and the record of who promoted what, so the loop, the
// release-cut guard, and the SLA clocks all write through one place.
//
// W5: the release-cut guard's TSIO half — every waiver active on a commit's
// master run, in one call. The workflow half (pause + release-manager
// confirm) lives in the release automation, which W0 could not locate;
// until it does, the guard is invocable standalone.
//
// W15c: SLA clocks derived from the ledger, per the spec's SLA table.

package triage

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// StabilizationQueueDepth is the working-queue scope: the organic ranking
// serves at most this many entries (plan W14; promotions ride above it).
const StabilizationQueueDepth = 10

// ---------- W14: the derived stabilization queue ----------

type queueEntry struct {
	TestID          string   `json:"test_id"`
	Titles          []string `json:"titles,omitempty"`
	Runs            int      `json:"runs"`
	Failed          int      `json:"failed"`
	Flaky           int      `json:"flaky"`
	Flips           int      `json:"flips"`
	FailureRate     float64  `json:"failure_rate"`
	FlakeRate       float64  `json:"flake_rate"`
	FailingSince    *string  `json:"failing_since_commit,omitempty"`
	Promoted        bool     `json:"promoted"`
	PromotedBy      *string  `json:"promoted_by,omitempty"`
	PromotionSource *string  `json:"promotion_source,omitempty"`
	PromotionReason *string  `json:"promotion_reason,omitempty"`
}

type queueResponse struct {
	Repo     string       `json:"repo"`
	Window   string       `json:"window"`
	Depth    int          `json:"depth"`
	Promoted []queueEntry `json:"promoted"`
	Ranked   []queueEntry `json:"ranked"`
}

// StabilizationQueue serves GET /api/v1/triage/stabilization/queue?repo=
// Public read: the stabilization loop (an action in the tested repo's CI)
// needs it without a credential round-trip.
func (h *Handlers) StabilizationQueue(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	window := orDefault(r.URL.Query().Get("window"), "30d")
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	// Promotions first — the queue's head is whatever a guard or a human
	// filed, not whatever is statistically worst.
	promoted, err := h.loadPromotions(r.Context(), repo)
	if err != nil {
		h.logError("stabilization queue promotions", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Then the organic ranking, most unstable first, excluding tests already
	// promoted. M2 fix: the rollup is per (test, report group) FIRST — the
	// same shape the flakiness leaderboard uses — and only then aggregated
	// per test, so runs = groups the test actually executed in and
	// failure_rate is a real rate instead of degenerating to 0.0/1.0.
	rows, err := h.Pool.Query(r.Context(), `
		WITH matched AS (
			SELECT g.id AS group_id, tc.external_test_id, tc.status
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id IS NOT NULL
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.branch IN ('main', 'master')
			  AND g.created_at >= $2::timestamptz
		),
		rolled AS (
			SELECT group_id, external_test_id,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed,
			       bool_or(status = 'flaky')                                AS had_flaky
			FROM matched
			GROUP BY group_id, external_test_id
		)
		SELECT external_test_id,
		       count(*)::int                                                AS runs,
		       count(*) FILTER (WHERE r2.ever_failed)::int                   AS failed,
		       count(*) FILTER (WHERE r2.had_flaky)::int                     AS flaky,
		       count(*) FILTER (WHERE r2.ever_passed AND r2.ever_failed)::int AS flips
		FROM rolled r2
		GROUP BY external_test_id
		HAVING bool_or(r2.ever_failed)
		ORDER BY count(*) FILTER (WHERE r2.ever_failed) DESC,
		         count(*) FILTER (WHERE r2.ever_passed AND r2.ever_failed) DESC
		LIMIT $3
		`, repo, since, StabilizationQueueDepth+len(promoted))
	if err != nil {
		h.logError("stabilization queue ranking", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	promotedSet := map[string]bool{}
	for _, p := range promoted {
		promotedSet[p.TestID] = true
	}

	ranked := []queueEntry{}
	for rows.Next() {
		var e queueEntry
		// B8 fix: no titles in the ranking projection — round-2 major 2: the
		// CROSS JOIN LATERAL unnest multiplied every counter by the title
		// count; the ranking is counters-only now (the agent reads titles
		// from the spec files themselves).
		if err := rows.Scan(&e.TestID, &e.Runs, &e.Failed, &e.Flaky, &e.Flips); err != nil {
			h.logError("stabilization queue scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		if promotedSet[e.TestID] {
			continue
		}
		if e.Runs > 0 {
			e.FailureRate = float64(e.Failed) / float64(e.Runs)
			e.FlakeRate = float64(e.Flips) / float64(e.Runs)
		}
		ranked = append(ranked, e)
		if len(ranked) >= StabilizationQueueDepth {
			break
		}
	}

	writeJSON(w, http.StatusOK, queueResponse{
		Repo:     repo,
		Window:   window,
		Depth:    StabilizationQueueDepth,
		Promoted: promoted,
		Ranked:   ranked,
	})
}

func (h *Handlers) loadPromotions(ctx context.Context, repo string) ([]queueEntry, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT external_test_id, promoted_by, source, reason
		FROM stabilization_promotions
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND NOT resolved
		ORDER BY created_at DESC
	`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []queueEntry{}
	for rows.Next() {
		var e queueEntry
		var by, src, reason string
		if err := rows.Scan(&e.TestID, &by, &src, &reason); err != nil {
			return nil, err
		}
		e.Promoted = true
		e.PromotedBy = &by
		e.PromotionSource = &src
		e.PromotionReason = &reason
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- W14: promote / resolve ----------

type promoteInput struct {
	TestID string `json:"test_id"`
	Reason string `json:"reason"`
	Source string `json:"source"`
}

// PromoteStabilization serves POST /api/v1/triage/stabilization/promote —
// file a test to the head of the queue. Authenticated: the queue's order is
// an allocation of fixing effort, and a forged promotion would misdirect it.
// Upsert per live (repo, test): re-promoting updates the reason.
func (h *Handlers) PromoteStabilization(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	var in promoteInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, errRepoRequiredWith("malformed JSON body"))
		return
	}
	if in.TestID == "" {
		api.WriteError(w, r, errRepoRequiredWith("test_id is required"))
		return
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	// M7: normalize to a full slug — a bare "mattermost" written here would
	// make the SLA's split_part match degenerate ('' = '' → TRUE for every
	// bare-stored row) and close unrelated clocks.
	if !strings.Contains(repo, "/") {
		repo = "mattermost/" + repo
	}
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	if _, err := h.Pool.Exec(r.Context(), `
		INSERT INTO stabilization_promotions (repository, external_test_id, promoted_by, reason, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (repository, external_test_id) WHERE NOT resolved
		DO UPDATE SET promoted_by = EXCLUDED.promoted_by,
		              reason = EXCLUDED.reason,
		              source = EXCLUDED.source,
		              updated_at = now()
	`, repo, in.TestID, subjectLabel(subject), in.Reason, in.Source); err != nil {
		h.logError("stabilization promote", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"test_id": in.TestID, "source": in.Source})
}

// ResolveStabilization serves POST /api/v1/triage/stabilization/resolve —
// mark a promotion done (fix merged / test stable) so the organic ranking
// takes over again.
func (h *Handlers) ResolveStabilization(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	testID := r.URL.Query().Get("test_id")
	if repo == "" || testID == "" {
		api.WriteError(w, r, errRepoRequiredWith("repo and test_id are required"))
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `
		UPDATE stabilization_promotions
		SET resolved = true, updated_at = now()
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND external_test_id = $2 AND NOT resolved
	`, repo, testID); err != nil {
		h.logError("stabilization resolve", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"test_id": testID, "resolved": true})
}

// ---------- W5: release-cut guard, TSIO half ----------

type releaseGuardResponse struct {
	Repository string        `json:"repository"`
	CommitSHA  string        `json:"commit_sha"`
	Clean      bool          `json:"clean"`
	Waivers    []guardWaiver `json:"waivers"`
}

type guardWaiver struct {
	VerdictID      string    `json:"verdict_id"`
	ExternalTestID *string   `json:"external_test_id,omitempty"`
	Verdict        string    `json:"verdict"`
	RootCause      *string   `json:"root_cause,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	AgeDays        int       `json:"age_days"`
}

// ReleaseGuard serves GET /api/v1/triage/release-guard?repo=&commit= — every
// waived verdict on the master run for that commit. The release automation
// calls this before cutting a branch: clean → proceed; waivers → the release
// manager confirms, and on confirm the listed tests are filed to the top of
// the stabilization queue via the promote endpoint. Public read (the guard
// runs from release CI), the promote it triggers is authenticated.
func (h *Handlers) ReleaseGuard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	commit := q.Get("commit")
	if repo == "" || commit == "" {
		api.WriteError(w, r, errRepoRequiredWith("repo and commit are required"))
		return
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT id::text, external_test_id, verdict, root_cause, created_at
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND commit_sha = $2
		  AND waived
		  AND gh_pr_number IS NULL
		ORDER BY created_at DESC
	`, repo, commit)
	if err != nil {
		h.logError("release guard waivers", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	waivers := []guardWaiver{}
	now := time.Now()
	for rows.Next() {
		var g guardWaiver
		if err := rows.Scan(&g.VerdictID, &g.ExternalTestID, &g.Verdict, &g.RootCause, &g.CreatedAt); err != nil {
			h.logError("release guard scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		g.AgeDays = int(now.Sub(g.CreatedAt).Hours() / 24)
		waivers = append(waivers, g)
	}
	writeJSON(w, http.StatusOK, releaseGuardResponse{
		Repository: repo,
		CommitSHA:  commit,
		Clean:      len(waivers) == 0,
		Waivers:    waivers,
	})
}

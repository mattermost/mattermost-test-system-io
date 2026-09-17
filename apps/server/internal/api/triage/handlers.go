// Package triageapi exposes the evidence, quarantine and verdict API.
package triageapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/identity"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage/health"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage/verdict"
)

// Handlers shares the existing database, authentication and events infrastructure.
type Handlers struct {
	Store    *triage.Store
	Verdicts *verdict.Store
	Health   *health.Refresher
	Hub      *events.Hub
	AdminKey string
	Logger   *slog.Logger
}

// RegisterPublic mounts read-only triage routes.
func RegisterPublic(r chi.Router, h *Handlers) {
	r.Get("/triage/verdicts", h.ListVerdicts)
	r.Get("/triage/verdicts/{id}", h.GetVerdict)
	r.Get("/triage/health", h.ListHealth)
	r.Get("/triage/tests/{id}", h.GetIdentity)
	r.Get("/triage/tests/{id}/observations", h.Observations)
	r.Get("/triage/quarantine", h.QuarantineFeed)
	r.Get("/triage/policies", h.ListPolicies)
}

// RegisterProtected must be called inside the existing RequireAuth route group.
func RegisterProtected(r chi.Router, h *Handlers) {
	r.Post("/triage/verdicts", h.Compute)
	r.Post("/triage/verdicts/{id}/override", h.Override)
	r.Get("/triage/verdicts/{id}/evidence", h.Evidence)
	r.Post("/triage/verdicts/{id}/adjudication", h.Adjudication)
	r.Post("/triage/health/refresh", h.Refresh)
	r.Post("/triage/quarantine", h.OpenQuarantine)
	r.Delete("/triage/quarantine/{id}", h.ReleaseQuarantine)
	r.Put("/triage/policies/{repository}/{context}", h.PutPolicy)
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		api.WriteErrorCode(w, 400, "BAD_REQUEST", "invalid request body: "+err.Error())
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		api.WriteErrorCode(w, 400, "BAD_REQUEST", "expected one JSON object")
		return false
	}
	return true
}
func (h *Handlers) failure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	if h.Logger != nil {
		h.Logger.ErrorContext(r.Context(), "triage request failed", slog.String("error", err.Error()))
	}
	api.WriteError(w, r, api.ErrInternal)
}
func validID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	_, err := uuid.Parse(id)
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return "", false
	}
	return id, true
}
func actor(r *http.Request) string {
	sub, _ := authapi.SubjectFromContext(r.Context())
	if sub.OIDCSubject != "" {
		return sub.OIDCSubject
	}
	if sub.Kind == "session" {
		return "user:" + sub.UserID.String()
	}
	return "apikey:" + sub.APIKeyID.String()
}
func (h *Handlers) admin(w http.ResponseWriter, r *http.Request) bool {
	sub, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return false
	}
	if sub.Role == policy.RoleAdmin {
		return true
	}
	if key := r.Header.Get("X-Admin-Key"); key != "" && h.AdminKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(h.AdminKey)) == 1 {
		return true
	}
	if sub.Kind == "session" {
		var role string
		if h.Store.Pool.QueryRow(r.Context(), `SELECT role FROM users WHERE id=$1`, sub.UserID).Scan(&role) == nil && role == "admin" {
			return true
		}
	}
	api.WriteError(w, r, api.ErrForbidden)
	return false
}
func allowedRepository(w http.ResponseWriter, r *http.Request, repository string) bool {
	sub, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return false
	}
	if sub.OIDCClaims != nil && sub.OIDCClaims.Repository != repository && sub.Role != policy.RoleAdmin {
		api.WriteError(w, r, api.ErrForbidden)
		return false
	}
	return true
}
func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		n = 50
	}
	return min(n, 200)
}
func (h *Handlers) emit(kind string, payload any) {
	if h.Hub == nil {
		return
	}
	raw, _ := json.Marshal(payload)
	h.Hub.Publish(events.Event{Type: kind, Timestamp: time.Now().UTC(), Payload: raw}, events.Scope{})
}

// Compute long-polls without holding a database connection between polls.
func (h *Handlers) Compute(w http.ResponseWriter, r *http.Request) {
	var req verdict.Request
	if !decode(w, r, &req) {
		return
	}
	ci := &req.CompositeIdentity
	if ci.GHRunAttempt == "" {
		ci.GHRunAttempt = "1"
	}
	if ci.Repository == "" || ci.CommitSHA == "" || ci.GHRunID == "" || ci.Name == "" || req.Context == "" || req.WaitForCompletionMS < 0 || req.WaitForCompletionMS > 60000 {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	if !allowedRepository(w, r, ci.Repository) {
		return
	}
	if !h.replayAllowed(w, r, &req) {
		return
	}
	g, ok := h.awaitGroup(w, r, &req)
	if !ok {
		return
	}
	if req.Lane == "" {
		req.Lane = g.Lane
		if req.Lane == "" {
			req.Lane = identity.InferLane(g.Name, nil)
		}
	}
	if req.BaseRef == "" {
		req.BaseRef = g.BaseRef
	}
	if req.BaseRef == "" {
		api.WriteErrorCode(w, 400, "BAD_REQUEST", "base_ref is required when the report group has none")
		return
	}
	if g.Lane != "" && req.Lane != g.Lane {
		api.WriteErrorCode(w, 409, "LANE_MISMATCH", "lane differs from the ingested report group")
		return
	}
	if g.BaseRef != "" && req.BaseRef != g.BaseRef || g.BaseSHA != "" && req.BaseSHA != "" && req.BaseSHA != g.BaseSHA {
		api.WriteErrorCode(w, 409, "BASE_MISMATCH", "base differs from the ingested report group")
		return
	}
	v, err := h.Verdicts.Compute(r.Context(), g, req)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if req.AsOf == nil {
		h.emit("triage.verdict.computed", map[string]any{"verdict_id": v.ID, "repository": g.Repository, "gh_pr_number": g.GHPRNumber, "verdict": v.Verdict})
	}
	status := http.StatusOK
	if g.Status == "incomplete" {
		status = http.StatusConflict
	}
	respond(w, status, v)
}

// awaitGroup long-polls (without holding a database connection) until the
// report group leaves in_progress or the request's wait budget is spent. It
// returns false when a response has already been written.
func (h *Handlers) awaitGroup(w http.ResponseWriter, r *http.Request, req *verdict.Request) (triage.Group, bool) {
	ci := &req.CompositeIdentity
	deadline := time.Now().Add(time.Duration(req.WaitForCompletionMS) * time.Millisecond)
	for {
		g, err := h.Store.GroupByIdentity(r.Context(), ci.Repository, ci.CommitSHA, ci.GHRunID, ci.Name, ci.GHRunAttempt)
		if err != nil {
			h.failure(w, r, err)
			return triage.Group{}, false
		}
		if g.Status != "in_progress" {
			return g, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			w.Header().Set("Retry-After", "2")
			respond(w, http.StatusAccepted, map[string]any{"status": "pending", "retry_after_ms": 2000})
			return triage.Group{}, false
		}
		timer := time.NewTimer(min(remaining, 500*time.Millisecond))
		select {
		case <-r.Context().Done():
			timer.Stop()
			return triage.Group{}, false
		case <-timer.C:
		}
	}
}

// replayAllowed gates `as_of`: a replay is a backtest tool, so it must not
// long-poll and only admins may request it.
func (h *Handlers) replayAllowed(w http.ResponseWriter, r *http.Request, req *verdict.Request) bool {
	if req.AsOf == nil {
		return true
	}
	if req.WaitForCompletionMS != 0 {
		api.WriteErrorCode(w, 400, "BAD_REQUEST", "as_of cannot be combined with wait_for_completion_ms")
		return false
	}
	return h.admin(w, r)
}

// GetVerdict returns a persisted audit result.
func (h *Handlers) GetVerdict(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	v, err := h.Verdicts.Get(r.Context(), id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	respond(w, 200, v)
}

// ListVerdicts lists recent verdicts matching the requested PR filters.
func (h *Handlers) ListVerdicts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := h.Store.Pool.Query(r.Context(), `SELECT id::text FROM pr_verdicts WHERE ($1='' OR repository=$1) AND ($2='' OR gh_pr_number::text=$2) AND ($3='' OR head_sha=$3) AND ($4='' OR context=$4) ORDER BY computed_at DESC,id DESC LIMIT $5`, q.Get("repository"), q.Get("gh_pr_number"), q.Get("commit_sha"), q.Get("context"), limit(r))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			h.failure(w, r, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		h.failure(w, r, err)
		return
	}
	items, err := h.Verdicts.List(r.Context(), ids)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

// Override records a human decision and its authenticated publisher.
func (h *Handlers) Override(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	var body struct {
		Actor          string `json:"actor"`
		Label          string `json:"label"`
		ResultingState string `json:"resulting_state"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Actor) == "" || body.Label == "" || body.ResultingState != "success" {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	var repository string
	if err := h.Store.Pool.QueryRow(r.Context(), `SELECT repository FROM pr_verdicts WHERE id=$1`, id).Scan(&repository); err != nil {
		h.failure(w, r, err)
		return
	}
	if !allowedRepository(w, r, repository) {
		return
	}
	err := h.Verdicts.RecordOverride(r.Context(), id, verdict.Override{Actor: body.Actor, Label: body.Label, ResultingState: body.ResultingState, RecordedBy: actor(r), At: time.Now().UTC()})
	if err != nil {
		h.failure(w, r, err)
		return
	}
	h.GetVerdict(w, r)
}

// Evidence returns one adjudicator evidence pack per adjudicable finding.
func (h *Handlers) Evidence(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	var repository string
	if err := h.Store.Pool.QueryRow(r.Context(), `SELECT repository FROM pr_verdicts WHERE id=$1`, id).Scan(&repository); err != nil {
		h.failure(w, r, err)
		return
	}
	if !allowedRepository(w, r, repository) {
		return
	}
	packs, err := h.Verdicts.EvidencePacks(r.Context(), id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"packs": packs})
}

// Adjudication records the second judge's answers and the producer's final decision.
func (h *Handlers) Adjudication(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	var body verdict.Adjudication
	if !decode(w, r, &body) {
		return
	}
	v, err := h.Verdicts.Get(r.Context(), id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if err := body.Validate(len(v.Findings)); err != nil {
		api.WriteErrorCode(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	var repository string
	if err := h.Store.Pool.QueryRow(r.Context(), `SELECT repository FROM pr_verdicts WHERE id=$1`, id).Scan(&repository); err != nil {
		h.failure(w, r, err)
		return
	}
	if !allowedRepository(w, r, repository) {
		return
	}
	body.RecordedBy, body.RecordedAt = actor(r), time.Now().UTC()
	if err := h.Verdicts.RecordAdjudication(r.Context(), id, body); err != nil {
		h.failure(w, r, err)
		return
	}
	h.emit("triage.verdict.adjudicated", map[string]any{"verdict_id": id, "final_verdict": body.FinalVerdict, "model": body.Model})
	h.GetVerdict(w, r)
}

// ListPolicies returns configured context policies.
func (h *Handlers) ListPolicies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Store.Pool.Query(r.Context(), `SELECT repository,context FROM triage_policies WHERE ($1='' OR repository=$1) ORDER BY repository,context`, r.URL.Query().Get("repository"))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	type key struct{ repo, context string }
	keys := []key{}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.repo, &k.context); err != nil {
			rows.Close()
			h.failure(w, r, err)
			return
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		h.failure(w, r, err)
		return
	}
	items := []triage.Policy{}
	for _, k := range keys {
		p, err := h.Store.LoadPolicy(r.Context(), k.repo, k.context)
		if err != nil {
			h.failure(w, r, err)
			return
		}
		items = append(items, p)
	}
	respond(w, 200, map[string]any{"items": items, "default_mode": "shadow", "default_thresholds": h.Store.DefaultPolicy("", "").Thresholds})
}

// PutPolicy validates and persists an administrative policy change.
func (h *Handlers) PutPolicy(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	repo, _ := url.PathUnescape(chi.URLParam(r, "repository"))
	contextName, _ := url.PathUnescape(chi.URLParam(r, "context"))
	var body struct {
		Mode       string          `json:"mode"`
		Thresholds json.RawMessage `json:"thresholds"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Mode != "off" && body.Mode != "shadow" && body.Mode != "enforce" {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	p, err := h.Store.LoadPolicy(r.Context(), repo, contextName)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if len(body.Thresholds) > 0 {
		p.Thresholds, err = triage.MergeThresholds(p.Thresholds, body.Thresholds)
		if err != nil {
			api.WriteErrorCode(w, 400, "BAD_REQUEST", err.Error())
			return
		}
	}
	raw, _ := json.Marshal(p.Thresholds)
	_, err = h.Store.Pool.Exec(r.Context(), `INSERT INTO triage_policies(repository,context,mode,thresholds,updated_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository,context) DO UPDATE SET mode=EXCLUDED.mode,thresholds=EXCLUDED.thresholds,updated_by=EXCLUDED.updated_by,updated_at=now()`, repo, contextName, body.Mode, raw, actor(r))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p, err = h.Store.LoadPolicy(r.Context(), repo, contextName)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	respond(w, 200, p)
}

// Refresh rebuilds the requested trunk lane projections.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	var body struct {
		Repository string  `json:"repository"`
		BaseRef    string  `json:"base_ref"`
		Lane       *string `json:"lane"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Repository == "" || body.BaseRef == "" {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	rows, err := h.Store.Pool.Query(r.Context(), `SELECT DISTINCT o.lane,g.name FROM test_observations o JOIN report_groups g ON g.id=o.report_group_id WHERE g.repository=$1 AND o.branch=$2 AND o.branch_kind='trunk' AND ($3::text IS NULL OR o.lane=$3)`, body.Repository, body.BaseRef, body.Lane)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	type scope struct{ lane, name string }
	scopes := []scope{}
	for rows.Next() {
		var s scope
		if err := rows.Scan(&s.lane, &s.name); err != nil {
			rows.Close()
			h.failure(w, r, err)
			return
		}
		scopes = append(scopes, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		h.failure(w, r, err)
		return
	}
	count := 0
	for _, s := range scopes {
		n, err := h.Health.Refresh(r.Context(), body.Repository, body.BaseRef, s.lane, health.ContextName(s.name, s.lane))
		if err != nil {
			h.failure(w, r, err)
			return
		}
		count += n
	}
	respond(w, 200, map[string]int{"changed_identities": count})
}

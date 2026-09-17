package triageapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

type healthCursor struct {
	ID      string `json:"id"`
	Lane    string `json:"lane"`
	BaseRef string `json:"base_ref"`
}

// ListHealth lists health projections with chronological execution sparklines.
func (h *Handlers) ListHealth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cursor := healthCursor{ID: uuid.Nil.String()}
	if token := q.Get("cursor"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || json.Unmarshal(raw, &cursor) != nil {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
		if _, err := uuid.Parse(cursor.ID); err != nil {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
	}
	rows, err := h.Store.Pool.Query(r.Context(), `SELECT row_to_json(x) FROM (SELECT h.*,i.repository,i.framework,i.normalized_file AS file,i.normalized_title AS full_title,COALESCE(i.mm_t_id,'') mm_t_id,COALESCE((SELECT jsonb_agg(to_jsonb(z)) FROM (
 SELECT o.report_group_id,o.status,o.observed_at,o.attempt_index,g.created_at AS group_created_at
 FROM test_observations o JOIN report_groups g ON g.id=o.report_group_id
 WHERE o.identity_id=h.identity_id AND o.lane=h.lane AND o.branch=h.base_ref
 AND o.branch_kind='trunk' AND NOT o.is_infra_stub AND g.status='completed'
 AND o.report_group_id IN (
   SELECT recent.id FROM report_groups recent JOIN test_observations ro ON ro.report_group_id=recent.id
   WHERE ro.identity_id=h.identity_id AND ro.lane=h.lane AND ro.branch=h.base_ref
   AND ro.branch_kind='trunk' AND NOT ro.is_infra_stub AND recent.status='completed'
   GROUP BY recent.id ORDER BY recent.created_at DESC,recent.id DESC LIMIT 30)
 ) z),'[]') AS history_observations FROM test_health h JOIN test_identities i ON i.id=h.identity_id WHERE ($1='' OR i.repository=$1) AND ($2='' OR i.framework=$2) AND ($3='' OR h.lane=$3) AND ($4='' OR h.base_ref=$4) AND ($5='' OR h.classification=$5) AND ($6='' OR i.normalized_title ILIKE '%'||$6||'%' OR i.normalized_file ILIKE '%'||$6||'%') AND (h.identity_id,h.lane,h.base_ref)>($7::uuid,$8,$9) ORDER BY h.identity_id,h.lane,h.base_ref LIMIT $10) x`, q.Get("repository"), q.Get("framework"), q.Get("lane"), q.Get("base_ref"), q.Get("classification"), q.Get("q"), cursor.ID, cursor.Lane, cursor.BaseRef, limit(r)+1)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			h.failure(w, r, err)
			return
		}
		var item map[string]json.RawMessage
		var observations []triage.Observation
		if err := json.Unmarshal(raw, &item); err != nil {
			h.failure(w, r, err)
			return
		}
		if err := json.Unmarshal(item["history_observations"], &observations); err != nil {
			h.failure(w, r, err)
			return
		}
		history := []string{}
		for _, trial := range triage.Trials(observations, time.Now(), triage.Thresholds{MaxTrunkRuns: 30}) {
			history = append(history, trial.Status)
		}
		delete(item, "history_observations")
		item["history"], _ = json.Marshal(history)
		raw, _ = json.Marshal(item)
		items = append(items, raw)
	}
	if err := rows.Err(); err != nil {
		h.failure(w, r, err)
		return
	}
	next := ""
	if len(items) > limit(r) {
		items = items[:limit(r)]
		var key struct {
			ID      string `json:"identity_id"`
			Lane    string `json:"lane"`
			BaseRef string `json:"base_ref"`
		}
		if err := json.Unmarshal(items[len(items)-1], &key); err != nil {
			h.failure(w, r, err)
			return
		}
		raw, _ := json.Marshal(healthCursor{ID: key.ID, Lane: key.Lane, BaseRef: key.BaseRef})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	respond(w, 200, map[string]any{"items": items, "next_cursor": next})
}

// GetIdentity returns identity metadata, per-lane health and active quarantine.
func (h *Handlers) GetIdentity(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	var identity json.RawMessage
	err := h.Store.Pool.QueryRow(r.Context(), `SELECT row_to_json(x) FROM (SELECT id,repository,framework,encode(stable_key,'hex') stable_key,normalized_file AS file,normalized_title AS full_title,mm_t_id,first_seen_at,last_seen_at FROM test_identities WHERE id=$1) x`, id).Scan(&identity)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	healthRows, err := h.jsonRows(r, `SELECT row_to_json(h) FROM test_health h WHERE identity_id=$1 ORDER BY lane,base_ref`, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	quarantine, err := h.jsonRows(r, `SELECT row_to_json(q) FROM quarantine_entries q WHERE identity_id=$1 AND status='active' AND (expires_at IS NULL OR expires_at>now()) ORDER BY created_at,id`, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"identity": identity, "health": healthRows, "active_quarantine": quarantine})
}
func (h *Handlers) jsonRows(r *http.Request, query string, args ...any) ([]json.RawMessage, error) {
	rows, err := h.Store.Pool.Query(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return items, rows.Err()
}

// Observations pages through raw execution evidence.
func (h *Handlers) Observations(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := triage.ObservationFilter{IdentityID: id, Lane: q.Get("lane"), MatchLane: q.Has("lane"), BranchKind: q.Get("branch_kind"), Limit: limit(r) + 1}
	if raw := q.Get("since"); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
		f.Since = at
	}
	if raw := q.Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
		if _, err := uuid.Parse(string(decoded)); err != nil {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
		f.Cursor = string(decoded)
	}
	if f.BranchKind != "" && !slices.Contains([]string{"trunk", "pr", "release", "other"}, f.BranchKind) {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	observations, err := h.Store.Observations(r.Context(), f)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	next := ""
	if len(observations) > limit(r) {
		observations = observations[:limit(r)]
		next = base64.RawURLEncoding.EncodeToString([]byte(observations[len(observations)-1].ID))
	}
	respond(w, 200, map[string]any{"items": observations, "next_cursor": next})
}

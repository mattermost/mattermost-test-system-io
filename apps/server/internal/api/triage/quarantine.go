package triageapi

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/identity"
)

const (
	formatJSON       = "json"
	formatPlaywright = "playwright-grep-invert"
)

type feedEntry struct {
	ID         string    `json:"id"`
	IdentityID string    `json:"identity_id"`
	StableKey  string    `json:"stable_key"`
	MMTID      string    `json:"mm_t_id"`
	File       string    `json:"file"`
	FullTitle  string    `json:"full_title"`
	Reason     string    `json:"reason"`
	Since      time.Time `json:"since"`
	IssueURL   string    `json:"issue_url"`
	AllInFile  bool      `json:"-"`
	SafeMMTID  bool      `json:"-"`
}

// QuarantineFeed renders active quarantine in the requested runner format.
func (h *Handlers) QuarantineFeed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = formatJSON
	}
	if format != formatJSON && format != formatPlaywright && format != "jest-name-list" && format != "spec-files" {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	rows, err := h.Store.Pool.Query(r.Context(), `
SELECT q.id::text,i.id::text,encode(i.stable_key,'hex'),COALESCE(i.mm_t_id,''),
       i.normalized_file,
       COALESCE((SELECT c.full_title FROM test_observations o JOIN test_cases c ON c.id=o.test_case_id
                 WHERE o.identity_id=i.id ORDER BY o.observed_at DESC LIMIT 1),i.normalized_title),
       q.reason,q.created_at,COALESCE(q.issue_url,''),
       NOT EXISTS(SELECT 1 FROM test_identities other
         WHERE other.repository=i.repository AND other.framework=i.framework
           AND other.normalized_file=i.normalized_file AND NOT EXISTS(
             SELECT 1 FROM quarantine_entries active WHERE active.identity_id=other.id
               AND active.status='active' AND active.base_ref=q.base_ref
               AND (active.lane IS NULL OR active.lane=$3)
               AND (active.expires_at IS NULL OR active.expires_at>now()))),
       NOT EXISTS(SELECT 1 FROM test_identities other
         WHERE other.repository=i.repository AND other.framework=i.framework
           AND other.mm_t_id=i.mm_t_id AND NOT EXISTS(
             SELECT 1 FROM quarantine_entries active WHERE active.identity_id=other.id
               AND active.status='active' AND active.base_ref=q.base_ref
               AND (active.lane IS NULL OR active.lane=$3)
               AND (active.expires_at IS NULL OR active.expires_at>now())))
FROM quarantine_entries q JOIN test_identities i ON i.id=q.identity_id
WHERE q.status='active' AND (q.expires_at IS NULL OR q.expires_at>now())
  AND ($1='' OR i.repository=$1) AND ($2='' OR i.framework=$2)
  AND ($3='' OR q.lane IS NULL OR q.lane=$3) AND ($4='' OR q.base_ref=$4)
ORDER BY i.normalized_file,i.normalized_title,q.id`, q.Get("repository"), q.Get("framework"), q.Get("lane"), q.Get("base_ref"))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	defer rows.Close()
	entries := []feedEntry{}
	for rows.Next() {
		var e feedEntry
		if err := rows.Scan(&e.ID, &e.IdentityID, &e.StableKey, &e.MMTID, &e.File, &e.FullTitle, &e.Reason, &e.Since, &e.IssueURL, &e.AllInFile, &e.SafeMMTID); err != nil {
			h.failure(w, r, err)
			return
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		h.failure(w, r, err)
		return
	}
	if format == formatJSON {
		respond(w, 200, entries)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(renderFeed(format, entries)))
}
func renderFeed(format string, entries []feedEntry) string {
	values := map[string]bool{}
	for _, e := range entries {
		switch format {
		case formatPlaywright:
			if e.MMTID != "" && e.SafeMMTID {
				values[regexp.QuoteMeta(e.MMTID)] = true
			}
		case "jest-name-list":
			values[e.FullTitle] = true
		case "spec-files":
			if e.AllInFile {
				values[e.File] = true
			}
		}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	if format == formatPlaywright {
		if len(ordered) == 0 {
			return "(?!)"
		}
		return "(" + strings.Join(ordered, "|") + `)\b`
	}
	return strings.Join(ordered, "\n")
}

// OpenQuarantine creates or updates an audited manual quarantine.
func (h *Handlers) OpenQuarantine(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	var body struct {
		IdentityID string     `json:"identity_id"`
		Repository string     `json:"repository"`
		Framework  string     `json:"framework"`
		File       string     `json:"file"`
		FullTitle  string     `json:"full_title"`
		Lane       *string    `json:"lane"`
		BaseRef    string     `json:"base_ref"`
		Reason     string     `json:"reason"`
		IssueURL   string     `json:"issue_url"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.BaseRef == "" || body.Reason != "manual" || body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	if body.IssueURL != "" {
		u, err := url.Parse(body.IssueURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
	}
	id := body.IdentityID
	if id == "" {
		if body.Repository == "" || body.Framework == "" || body.File == "" || body.FullTitle == "" {
			api.WriteError(w, r, api.ErrBadRequest)
			return
		}
		key := identity.StableKey(body.Repository, body.Framework, body.File, body.FullTitle)
		if err := h.Store.Pool.QueryRow(r.Context(), `SELECT id::text FROM test_identities WHERE stable_key=decode($1,'hex')`, hex.EncodeToString(key)).Scan(&id); err != nil {
			h.failure(w, r, err)
			return
		}
	}
	if _, err := uuid.Parse(id); err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	var infra bool
	if err := h.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM test_observations WHERE identity_id=i.id AND is_infra_stub) FROM test_identities i WHERE id=$1`, id).Scan(&infra); err != nil {
		h.failure(w, r, err)
		return
	}
	if infra {
		api.WriteErrorCode(w, 400, "INFRA_NOT_QUARANTINABLE", "infrastructure stubs cannot be quarantined")
		return
	}
	var raw json.RawMessage
	err := h.Store.Pool.QueryRow(r.Context(), `INSERT INTO quarantine_entries(identity_id,lane,base_ref,source,reason,status,evidence,issue_url,created_by,expires_at) VALUES($1,$2,$3,'manual','manual','active',COALESCE((SELECT jsonb_agg(to_jsonb(h)) FROM test_health h WHERE identity_id=$1 AND base_ref=$3),'[]'),NULLIF($4,''),$5,$6) ON CONFLICT(identity_id,COALESCE(lane,'*'),base_ref) WHERE status='active' DO UPDATE SET source='manual',reason='manual',issue_url=EXCLUDED.issue_url,expires_at=EXCLUDED.expires_at,created_by=EXCLUDED.created_by RETURNING row_to_json(quarantine_entries)`, id, body.Lane, body.BaseRef, body.IssueURL, actor(r), body.ExpiresAt).Scan(&raw)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	var entry struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &entry)
	h.emit("triage.quarantine.opened", map[string]string{"quarantine_id": entry.ID})
	respond(w, 201, raw)
}

// ReleaseQuarantine releases an entry without deleting its audit history.
func (h *Handlers) ReleaseQuarantine(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	id, ok := validID(w, r)
	if !ok {
		return
	}
	var raw json.RawMessage
	err := h.Store.Pool.QueryRow(r.Context(), `UPDATE quarantine_entries SET status='released',released_at=COALESCE(released_at,now()),released_by=COALESCE(released_by,$2) WHERE id=$1 RETURNING row_to_json(quarantine_entries)`, chi.URLParam(r, "id"), actor(r)).Scan(&raw)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	h.emit("triage.quarantine.released", map[string]string{"quarantine_id": id})
	respond(w, 200, raw)
}

package triageassessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Handlers serves previews and durable assessment records.
type Handlers struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// MasterCadence controls baseline freshness: max(2*cadence, 4h).
	// Zero uses daily master execution (a 48h freshness threshold).
	MasterCadence time.Duration
}

var shaPattern = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)
var numberPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func validate(s Selector) error {
	if !repoPattern.MatchString(s.Repository) || len(s.Repository) > 200 || !shaPattern.MatchString(s.CommitSHA) ||
		!numberPattern.MatchString(s.GHRunID) || !numberPattern.MatchString(s.GHRunAttempt) ||
		strings.TrimSpace(s.Name) != s.Name || s.Name == "" || len(s.Name) > 200 {
		return fmt.Errorf("%w: exact repository, commit_sha, gh_run_id, gh_run_attempt and name are required", api.ErrBadRequest)
	}
	return nil
}

// Attribution is a read-only preview. It never creates a decision or authorizes
// changing a GitHub check, even when no test failures were stored.
func (h *Handlers) Attribution(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "repository" && key != "commit_sha" && key != "gh_run_id" && key != "gh_run_attempt" && key != "name") || len(values) != 1 {
			api.WriteError(w, r, fmt.Errorf("%w: only exact run selector fields are supported", api.ErrBadRequest))
			return
		}
	}
	s := Selector{Repository: q.Get("repository"), CommitSHA: q.Get("commit_sha"), GHRunID: q.Get("gh_run_id"), GHRunAttempt: q.Get("gh_run_attempt"), Name: q.Get("name")}
	if err := validate(s); err != nil {
		api.WriteError(w, r, err)
		return
	}
	a, err := h.evaluate(r.Context(), s, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Record is mounted behind the dedicated triage middleware. That middleware
// overwrites both headers after authentication; these are not public inputs.
func (h *Handlers) Record(w http.ResponseWriter, r *http.Request) {
	author := r.Header.Get("X-TSIO-Triage-Actor")
	if strings.TrimSpace(author) == "" || len(author) > 500 {
		api.WriteError(w, r, api.ErrForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var s Selector
	if err := dec.Decode(&s); err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: invalid run selector", api.ErrBadRequest))
		return
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		api.WriteError(w, r, fmt.Errorf("%w: expected one JSON object", api.ErrBadRequest))
		return
	}
	if err := validate(s); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if scope := r.Header.Get("X-TSIO-Triage-Repository"); scope != "" && scope != s.Repository {
		api.WriteError(w, r, api.ErrForbidden)
		return
	}
	a, err := h.evaluate(r.Context(), s, author)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// Verdict returns a recorded assessment without recomputing historical evidence.
func (h *Handlers) Verdict(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: invalid assessment id", api.ErrBadRequest))
		return
	}
	var payload []byte
	if err := h.Pool.QueryRow(ctx, `SELECT assessment FROM triage_assessments WHERE id=$1`, id).Scan(&payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			api.WriteError(w, r, api.ErrNotFound)
		} else {
			h.fail(w, r, err)
		}
		return
	}
	// Stored decisions remain immutable. The current policy never permits a
	// historical decision to be consumed as authorization either.
	writeJSON(w, http.StatusOK, json.RawMessage(payload))
}

func (h *Handlers) evaluate(ctx context.Context, s Selector, author string) (Assessment, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mode := pgx.ReadOnly
	if author != "" {
		mode = pgx.ReadWrite
	}
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: mode})
	if err != nil {
		return Assessment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cadence := h.MasterCadence
	if cadence <= 0 {
		cadence = 24 * time.Hour
	}
	a, err := loadAssessment(ctx, tx, s, max(2*cadence, 4*time.Hour))
	if err != nil {
		return a, err
	}
	if author != "" {
		now := time.Now().UTC()
		a.ID, a.Author, a.RecordedAt = uuid.NewString(), author, &now
		payload, marshalErr := json.Marshal(a)
		if marshalErr != nil {
			return a, marshalErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO triage_assessments (id,repository,author,created_at,assessment) VALUES ($1,$2,$3,$4,$5)`, a.ID, s.Repository, author, now, payload); err != nil {
			return a, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return a, err
	}
	return a, nil
}

func (h *Handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	if h.Logger != nil {
		h.Logger.Error("triage assessment failed", "error", err)
	}
	api.WriteError(w, r, api.ErrInternal)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

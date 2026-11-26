// Package verdicts serves /api/v1/verdicts* endpoints for storing and
// querying AI triage verdicts.
package verdicts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/verdicts"
)

// MaxCreateBodyBytes caps the JSON request body size for POST /api/v1/verdicts.
const MaxCreateBodyBytes = 4 << 20 // 4 MiB

// Handlers bundles HTTP handlers for /api/v1/verdicts*.
type Handlers struct {
	Store     *verdicts.Store
	Logger    *slog.Logger
	AuthzMode authapi.AuthzMode
}

type createBatchBody struct {
	Repository    string           `json:"repository"`
	ReportGroupID *string          `json:"report_group_id"`
	Verdicts      []createItemBody `json:"verdicts"`
}

type createItemBody struct {
	TestID       *string         `json:"test_id"`
	SignatureID  *string         `json:"signature_id"`
	Fingerprint  *string         `json:"fingerprint"`
	Label        string          `json:"label"`
	DecidedBy    *string         `json:"decided_by"`
	Rule         *string         `json:"rule"`
	Confidence   *float64        `json:"confidence"`
	Summary      *string         `json:"summary"`
	Model        *string         `json:"model"`
	TriageURL    *string         `json:"triage_url"`
	ErrorContext json.RawMessage `json:"error_context"`
	ReusedFromID *string         `json:"reused_from_id"`
}

// Create serves POST /api/v1/verdicts.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	sub, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxCreateBodyBytes)
	var body createBatchBody
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteError(w, r, api.ErrTooLarge)
			return
		}
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteError(w, r, api.ErrTooLarge)
			return
		}
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	body.Repository = authapi.NormalizeRepository(body.Repository)
	if body.Repository == "" || strings.EqualFold(body.Repository, ".git") {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "repository is required")
		return
	}
	if len(body.Verdicts) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "verdicts must not be empty")
		return
	}
	if err := authapi.CheckRepoAccess(h.AuthzMode, h.Logger, sub, body.Repository); err != nil {
		api.WriteError(w, r, err)
		return
	}

	input, err := toBatchInput(body)
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	created, err := h.Store.CreateBatch(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, verdicts.ErrValidation):
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		case errors.Is(err, verdicts.ErrRepoMismatch):
			api.WriteError(w, r, api.ErrForbidden)
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"verdicts": created,
	})
}

// BySignature serves GET /api/v1/verdicts/by-signature/{fingerprint}.
func (h *Handlers) BySignature(w http.ResponseWriter, r *http.Request) {
	fp := strings.TrimSpace(chi.URLParam(r, "fingerprint"))
	if fp == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "fingerprint is required")
		return
	}

	q := r.URL.Query()
	windowDays := verdicts.DefaultWindowDays
	if q.Has("window_days") {
		rawWin := strings.TrimSpace(q.Get("window_days"))
		n, err := strconv.Atoi(rawWin)
		if err != nil || n < verdicts.MinWindowDays || n > verdicts.MaxWindowDays {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
				fmt.Sprintf("window_days must be an integer between %d and %d", verdicts.MinWindowDays, verdicts.MaxWindowDays))
			return
		}
		windowDays = n
	}

	var repo string
	if q.Has("repository") {
		repo = authapi.NormalizeRepository(q.Get("repository"))
		if repo == "" || strings.EqualFold(repo, ".git") {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "repository must not be empty")
			return
		}
	}
	v, err := h.Store.LookupBySignatureFingerprint(r.Context(), fp, windowDays, repo)
	if err != nil {
		switch {
		case errors.Is(err, verdicts.ErrNotFound):
			api.WriteError(w, r, api.ErrNotFound)
		case errors.Is(err, verdicts.ErrValidation):
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, v)
}

func toBatchInput(body createBatchBody) (verdicts.BatchInput, error) {
	rgID, err := parseOptionalUUID(body.ReportGroupID, "report_group_id")
	if err != nil {
		return verdicts.BatchInput{}, err
	}
	items := make([]verdicts.CreateItem, len(body.Verdicts))
	for i, item := range body.Verdicts {
		testID, err := parseOptionalUUID(item.TestID, fmt.Sprintf("verdicts[%d].test_id", i))
		if err != nil {
			return verdicts.BatchInput{}, err
		}
		sigID, err := parseOptionalUUID(item.SignatureID, fmt.Sprintf("verdicts[%d].signature_id", i))
		if err != nil {
			return verdicts.BatchInput{}, err
		}
		reusedID, err := parseOptionalUUID(item.ReusedFromID, fmt.Sprintf("verdicts[%d].reused_from_id", i))
		if err != nil {
			return verdicts.BatchInput{}, err
		}
		items[i] = verdicts.CreateItem{
			TestID:       testID,
			SignatureID:  sigID,
			Fingerprint:  item.Fingerprint,
			Label:        item.Label,
			DecidedBy:    item.DecidedBy,
			Rule:         item.Rule,
			Confidence:   item.Confidence,
			Summary:      item.Summary,
			Model:        item.Model,
			TriageURL:    item.TriageURL,
			ErrorContext: item.ErrorContext,
			ReusedFromID: reusedID,
		}
	}
	return verdicts.BatchInput{
		Repository:    body.Repository,
		ReportGroupID: rgID,
		Items:         items,
	}, nil
}

func parseOptionalUUID(raw *string, field string) (*uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%s must be a valid UUID", field)
	}
	id, err := uuid.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must be a valid UUID", field)
	}
	return &id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

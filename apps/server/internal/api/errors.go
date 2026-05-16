// Package api holds the chi router factory, the JSON error writer, and
// the Swagger UI handlers. Feature-specific HTTP handlers live in sub-packages.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// Error is the JSON error envelope returned by every /api/v1 route:
// {"error": "<CODE>", "message": "<human-readable>"}.
type Error struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

// Sentinel errors surfaced to the HTTP layer.
var (
	ErrBadRequest     = errors.New("bad request")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrTooLarge       = errors.New("payload too large")
	ErrQueryTooShort  = errors.New("query too short")
	ErrSessionExpired = errors.New("session expired")
	ErrInternal       = errors.New("internal server error")
)

// WriteError serializes err as application/json with the appropriate status.
func WriteError(w http.ResponseWriter, _ *http.Request, err error) {
	code, message, status := mapError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: message})
}

// WriteErrorCode writes a specific error code with a custom message. Used when
// the handler has a more specific code than the sentinel error offers (e.g.
// VALIDATION_FAILED with a field list in message).
func WriteErrorCode(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: message})
}

func mapError(err error) (code, message string, status int) {
	switch {
	case errors.Is(err, ErrBadRequest):
		return "BAD_REQUEST", err.Error(), http.StatusBadRequest
	case errors.Is(err, ErrQueryTooShort):
		return "QUERY_TOO_SHORT", err.Error(), http.StatusBadRequest
	case errors.Is(err, ErrUnauthorized):
		return "UNAUTHORIZED", "unauthorized", http.StatusUnauthorized
	case errors.Is(err, ErrSessionExpired):
		return "SESSION_EXPIRED", "session expired", http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return "FORBIDDEN", "forbidden", http.StatusForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, storage.ErrNotFound):
		return "NOT_FOUND", "not found", http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return "CONFLICT", err.Error(), http.StatusConflict
	case errors.Is(err, ErrTooLarge):
		return "PAYLOAD_TOO_LARGE", "payload too large", http.StatusRequestEntityTooLarge
	default:
		// Do not leak internal detail to the client.
		return "INTERNAL_ERROR", "internal server error", http.StatusInternalServerError
	}
}

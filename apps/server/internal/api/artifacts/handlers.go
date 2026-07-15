// Package artifacts serves /api/v1/artifacts/{id} by redirecting to presigned
// object-storage URLs.
package artifacts

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// Handlers exposes GET /artifacts/{id}.
type Handlers struct {
	Pool       *pgxpool.Pool
	Store      storage.ObjectStore
	PresignTTL time.Duration
}

// Get redirects to a presigned S3 URL.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	var key string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT object_key FROM artifacts WHERE id = $1`, id).Scan(&key); err != nil {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	url, err := h.Store.PresignGet(r.Context(), key, h.PresignTTL)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

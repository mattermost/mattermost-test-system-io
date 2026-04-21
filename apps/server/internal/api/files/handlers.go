// Package files serves GET /files/* by redirecting to a presigned object-store
// URL. The URL path after /files/ is the full S3 key (e.g. `reports/{report_id}/
// screenshots/{relpath}`). Used by the web to render uploaded screenshots
// inline without going through the authenticated artifact endpoint.
package files

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// Handlers bundles the store + presign TTL.
type Handlers struct {
	Store      storage.ObjectStore
	PresignTTL time.Duration
}

// Get redirects to a presigned URL for the given object key. The key is the
// full URL path after /files/ (rejoined from chi's wildcard segment).
//
// Sandboxing: only keys under the known upload prefix are allowed, preventing
// an attacker from reading arbitrary bucket contents via path traversal.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "*")
	if raw == "" {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	key := strings.TrimLeft(raw, "/")
	if !strings.HasPrefix(key, "reports/") {
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

// Package files serves GET /files/* by redirecting to a presigned object-store
// URL. The URL path after /files/ is the full S3 key (e.g. `reports/{report_id}/
// screenshots/{relpath}`). Used by the web to render uploaded screenshots
// inline without going through the authenticated artifact endpoint.
package files

import (
	"net/http"
	"net/url"
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

// allowedPrefixes enumerates the object-store key prefixes that the files
// redirect endpoint is permitted to serve. Keys outside these prefixes are
// rejected with 404 to prevent path-traversal reads of arbitrary bucket
// contents.
var allowedPrefixes = []string{
	"reports/",
	"orchestration/",
}

// Get redirects to a presigned URL for the given object key. The key is the
// full URL path after /files/ (rejoined from chi's wildcard segment).
//
// Sandboxing: only keys under the reports/ and orchestration/ prefixes are
// allowed, preventing an attacker from reading arbitrary bucket contents via
// path traversal.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "*")
	if raw == "" {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	// chi's wildcard returns the path partially decoded — `%28` becomes
	// `(` but `%20` may pass through verbatim depending on the request
	// shape. The object-store key is the canonical decoded form, so
	// run one explicit decode pass here. Without this, a presigned URL
	// for a key containing spaces double-encodes (`%20` → `%2520`) and
	// MinIO/S3 returns 404.
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	key := strings.TrimLeft(raw, "/")
	if !hasAllowedPrefix(key) {
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

// hasAllowedPrefix reports whether the given key falls under one of the
// allowedPrefixes that the files redirect endpoint is permitted to serve.
func hasAllowedPrefix(key string) bool {
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

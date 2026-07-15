package apikey

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Identity is the authenticated principal resolved from an X-API-Key header.
type Identity struct {
	APIKeyID uuid.UUID
	Name     string
	Status   Status
}

type ctxKey string

const identityKey ctxKey = "apikey.identity"

// Middleware verifies the X-API-Key header and injects Identity into ctx.
// Missing header → the middleware is a no-op (downstream auth may still reject);
// malformed or failing-verify header → 401.
func Middleware(repo *Repo, rotationGrace time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-API-Key")
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			prefix, _, ok := ParsePlaintext(raw)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			row, err := repo.ByPrefix(r.Context(), prefix)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if row.Status == StatusRevoked {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if row.Status == StatusRotating && row.RevokedAt != nil &&
				time.Since(*row.RevokedAt) > rotationGrace {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !Verify(raw, row.KeyHash) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Best-effort usage bump; never block the request on it. The goroutine
			// deliberately uses a detached background context — binding to
			// r.Context() would cancel the UPDATE when the handler returns.
			//nolint:gosec // G118: detached by design (fire-and-forget last-used bump).
			go func(id uuid.UUID) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = repo.TouchLastUsed(ctx, id)
			}(row.ID)

			id := Identity{APIKeyID: row.ID, Name: row.Name, Status: row.Status}
			ctx := context.WithValue(r.Context(), identityKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the api-key identity, or an error if none is present.
func FromContext(ctx context.Context) (Identity, error) {
	if id, ok := ctx.Value(identityKey).(Identity); ok {
		return id, nil
	}
	return Identity{}, errors.New("apikey: not authenticated")
}

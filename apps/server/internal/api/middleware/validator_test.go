package middleware

import (
	"path/filepath"
	"testing"
)

// The shipped spec must load and self-validate.
//
// This exists because the failure is silent. server.Build treats a validator
// that will not construct as "validation disabled", logs a warning, and serves
// every route unvalidated — which is the right call at runtime (a malformed
// spec should not take the API down) and a terrible one in CI, where nothing
// else notices.
//
// It has already caught a duplicate schema key and two unresolved $refs that
// had disabled request validation across the whole /api/v1 subtree while the
// e2e suite reported green.
func TestShippedSpecLoadsAndValidates(t *testing.T) {
	spec := filepath.Join("..", "..", "..", "api", "openapi.yaml")

	v, err := NewOpenAPIValidator(spec)
	if err != nil {
		t.Fatalf("openapi.yaml does not load, so request validation is silently OFF in production: %v", err)
	}
	if v == nil {
		t.Fatal("validator is nil with no error")
	}
}

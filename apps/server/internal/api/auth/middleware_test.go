package authapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// When RequireAuth is called with all-nil deps, any request without credentials
// must be rejected with 401. Requests with malformed credentials must also be
// rejected before any DB call (short-circuit in ParsePlaintext / bearer-prefix
// check).
func newProtected() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return RequireAuth(nil, nil, nil, nil)(inner)
}

func TestRequireAuth_missingCredentials_is401(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_malformedAPIKey_is401(t *testing.T) {
	// Malformed X-API-Key (no '.') short-circuits at ParsePlaintext before any DB call.
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", "not-a-well-formed-key")
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_bearerWithoutVerifier_is401(t *testing.T) {
	// With no OIDC verifier configured, a Bearer token can't be validated →
	// fall-through to "no valid auth" → 401.
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_wrongAuthScheme_is401(t *testing.T) {
	// Authorization header without "Bearer " prefix is ignored; no other creds →
	// 401.
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// A well-formed X-API-Key arrives at a server with nil apiKeys deps (e.g., a
// test or partial deployment). The middleware must return 401, not panic
// dereferencing the missing repo.
func TestRequireAuth_wellFormedAPIKeyWithNilDeps_is401(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// prefix.secret — passes ParsePlaintext but apiKeys.ByPrefix would panic
	// without the nil guard.
	req.Header.Set("X-API-Key", "prefix."+strings.Repeat("a", 32))
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// An X-API-Key that's syntactically well-formed but unknown to apiKeys (nil
// deps here) should NOT short-circuit the resolver — the request still falls
// through to bearer + session evaluation. With nil deps everywhere, the final
// outcome is 401, but the contract is "each method is tried."
func TestRequireAuth_invalidPrimaryFallsThroughToOthers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", "prefix."+strings.Repeat("a", 32))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	rec := httptest.NewRecorder()

	newProtected().ServeHTTP(rec, req)

	// All deps nil → no credential resolves → 401 (not a panic, not a 500).
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSubjectFromContext_emptyIsError(t *testing.T) {
	_, err := SubjectFromContext(t.Context())
	if err == nil {
		t.Fatal("expected ErrNotAuthenticated on empty context")
	}
}

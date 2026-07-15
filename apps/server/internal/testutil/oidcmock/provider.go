// Package oidcmock serves a fake OIDC provider — `/.well-known/openid-configuration`
// and `/jwks.json` — plus a helper to sign ID tokens. Used by unit tests of
// the OIDC verifier and by the OIDC E2E suite in tests/e2e/oidc/.
//
// Build tag-free so any test (unit or e2e) can import it.
package oidcmock

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // kid generation only; collision tolerance is fine
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Provider is an in-process OIDC provider for tests.
type Provider struct {
	Server *httptest.Server
	Issuer string
	kid    string
	priv   *rsa.PrivateKey
}

// NewProvider generates an RSA keypair and starts an httptest server that
// serves the OIDC discovery + JWKS endpoints. Caller is responsible for
// calling Close() (preferably via t.Cleanup).
func NewProvider(t *testing.T) *Provider {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	kid := keyID(&priv.PublicKey)

	mux := http.NewServeMux()
	p := &Provider{priv: priv, kid: kid}

	// Discovery document. Issuer is the server's base URL, which we set after
	// starting the server (chicken-and-egg). We bind the handler to a closure
	// reading p.Issuer which gets populated below.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                p.Issuer,
			"jwks_uri":                              p.Issuer + "/jwks.json",
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	jwksHandler := func(w http.ResponseWriter, _ *http.Request) {
		jwk := map[string]any{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": p.kid,
			"n":   base64URL(priv.N.Bytes()),
			"e":   base64URL(big.NewInt(int64(priv.E)).Bytes()),
		}
		writeJSON(w, map[string]any{"keys": []any{jwk}})
	}
	// Legacy path (kept for tests that still use OIDC discovery).
	mux.HandleFunc("/jwks.json", jwksHandler)
	// GitHub + seed-script convention: direct JWKS URL derived from issuer.
	mux.HandleFunc("/.well-known/jwks", jwksHandler)

	p.Server = httptest.NewServer(mux)
	p.Issuer = p.Server.URL

	t.Cleanup(p.Server.Close)
	return p
}

// Close tears down the httptest server.
func (p *Provider) Close() { p.Server.Close() }

// Claims captures the GitHub Actions OIDC claims the server needs. Additional
// claims may be passed as a map via the Extra field.
type Claims struct {
	Subject         string
	Audience        string
	Repository      string
	RepositoryOwner string
	Workflow        string
	Ref             string
	Environment     string
	ExpiresIn       time.Duration // 0 → 10 minutes
	NotBefore       time.Duration // signed offset (0 → now)
	Extra           map[string]any
}

// IssueToken produces an RS256-signed JWT whose issuer is p.Issuer.
func (p *Provider) IssueToken(t *testing.T, claims Claims) string {
	t.Helper()

	now := time.Now().UTC()
	exp := claims.ExpiresIn
	if exp == 0 {
		exp = 10 * time.Minute
	}

	body := jwt.MapClaims{
		"iss": p.Issuer,
		"sub": claims.Subject,
		"aud": claims.Audience,
		"iat": now.Add(claims.NotBefore).Unix(),
		"nbf": now.Add(claims.NotBefore).Unix(),
		"exp": now.Add(claims.NotBefore).Add(exp).Unix(),
	}
	if claims.Repository != "" {
		body["repository"] = claims.Repository
	}
	if claims.RepositoryOwner != "" {
		body["repository_owner"] = claims.RepositoryOwner
	}
	if claims.Workflow != "" {
		body["workflow"] = claims.Workflow
	}
	if claims.Ref != "" {
		body["ref"] = claims.Ref
	}
	if claims.Environment != "" {
		body["environment"] = claims.Environment
	}
	for k, v := range claims.Extra {
		body[k] = v
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, body)
	tok.Header["kid"] = p.kid

	signed, err := tok.SignedString(p.priv)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

// keyID computes a stable-ish kid from the RSA modulus.
func keyID(pub *rsa.PublicKey) string {
	h := sha1.Sum(pub.N.Bytes()) //nolint:gosec // not for security, just a kid
	return base64URL(h[:8])
}

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

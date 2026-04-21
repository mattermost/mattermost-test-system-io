// Package oidc verifies GitHub Actions OIDC ID tokens for CI workload auth.
//
// The JWKS URL is derived directly from the issuer as `{issuer}/.well-known/jwks`
// rather than going through OIDC discovery. This matches GitHub's production
// endpoint and lets dev mocks expose a single static JWKS path.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	oidclib "github.com/coreos/go-oidc/v3/oidc"
)

// Claims is the subset of GitHub Actions OIDC claims we keep.
type Claims struct {
	Issuer          string          `json:"iss"`
	Subject         string          `json:"sub"`
	Audience        string          `json:"aud"`
	Repository      string          `json:"repository"`
	RepositoryOwner string          `json:"repository_owner"`
	Workflow        string          `json:"workflow"`
	Ref             string          `json:"ref"`
	Environment     string          `json:"environment"`
	Raw             json.RawMessage `json:"-"`
}

// Verifier validates GitHub Actions OIDC ID tokens.
type Verifier struct {
	verifier *oidclib.IDTokenVerifier
}

// New builds a verifier. The JWKS URL is derived from the issuer as
// `{issuer}/.well-known/jwks` (no OIDC discovery round-trip). When audience is
// empty, aud validation is skipped — useful for dev where the seed script
// mints tokens without an aud claim.
func New(ctx context.Context, issuer, audience string) (*Verifier, error) {
	if issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	jwksURL := strings.TrimRight(issuer, "/") + "/.well-known/jwks"
	keySet := oidclib.NewRemoteKeySet(ctx, jwksURL)

	cfg := &oidclib.Config{
		ClientID:          audience,
		SkipClientIDCheck: audience == "",
	}
	v := oidclib.NewVerifier(issuer, keySet, cfg)
	return &Verifier{verifier: v}, nil
}

// Verify parses and cryptographically verifies the raw JWT.
func (v *Verifier) Verify(ctx context.Context, rawJWT string) (Claims, error) {
	tok, err := v.verifier.Verify(ctx, rawJWT)
	if err != nil {
		return Claims{}, fmt.Errorf("verify: %w", err)
	}
	var c Claims
	if err := tok.Claims(&c); err != nil {
		return Claims{}, fmt.Errorf("claims: %w", err)
	}
	c.Issuer = tok.Issuer
	c.Subject = tok.Subject
	if len(tok.Audience) > 0 {
		c.Audience = tok.Audience[0]
	}
	var raw map[string]any
	if err := tok.Claims(&raw); err == nil {
		b, _ := json.Marshal(raw)
		c.Raw = b
	}
	return c, nil
}

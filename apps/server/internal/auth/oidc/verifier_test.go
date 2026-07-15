package oidc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
)

const testAudience = "tsio"

func newTestVerifier(t *testing.T) (*Verifier, *oidcmock.Provider) {
	t.Helper()
	p := oidcmock.NewProvider(t)
	v, err := New(context.Background(), p.Issuer, testAudience)
	if err != nil {
		t.Fatalf("oidc.New: %v", err)
	}
	return v, p
}

func TestVerify_validToken(t *testing.T) {
	v, p := newTestVerifier(t)
	tok := p.IssueToken(t, oidcmock.Claims{
		Subject:         "repo:mattermost/test-repo:ref:refs/heads/main",
		Audience:        testAudience,
		Repository:      "mattermost/test-repo",
		RepositoryOwner: "mattermost",
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})

	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "repo:mattermost/test-repo:ref:refs/heads/main" {
		t.Errorf("Subject = %q", claims.Subject)
	}
	if claims.Audience != testAudience {
		t.Errorf("Audience = %q", claims.Audience)
	}
	if claims.Repository != "mattermost/test-repo" {
		t.Errorf("Repository = %q", claims.Repository)
	}
	if claims.RepositoryOwner != "mattermost" {
		t.Errorf("RepositoryOwner = %q", claims.RepositoryOwner)
	}
	if claims.Workflow != "ci" {
		t.Errorf("Workflow = %q", claims.Workflow)
	}
	if len(claims.Raw) == 0 {
		t.Error("Raw claims not captured")
	}
}

func TestVerify_rejectsWrongAudience(t *testing.T) {
	v, p := newTestVerifier(t)
	tok := p.IssueToken(t, oidcmock.Claims{
		Subject:  "repo:mattermost/other:ref:refs/heads/main",
		Audience: "someone-else",
	})

	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("Verify accepted token with wrong audience")
	}
}

func TestVerify_rejectsExpired(t *testing.T) {
	v, p := newTestVerifier(t)
	tok := p.IssueToken(t, oidcmock.Claims{
		Subject:   "repo:mattermost/test:ref:refs/heads/main",
		Audience:  testAudience,
		NotBefore: -2 * time.Hour,
		ExpiresIn: time.Hour, // exp was 1 hour ago
	})

	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("Verify accepted expired token")
	}
}

func TestVerify_rejectsTamperedSignature(t *testing.T) {
	v, p := newTestVerifier(t)
	tok := p.IssueToken(t, oidcmock.Claims{
		Subject:  "repo:mattermost/test:ref:refs/heads/main",
		Audience: testAudience,
	})

	// Flip a char mid-signature. Flipping the *last* char can land in unused
	// padding bits of base64url and survive; mid-signature always invalidates.
	lastDot := strings.LastIndex(tok, ".")
	if lastDot < 0 || lastDot >= len(tok)-5 {
		t.Fatalf("unexpected token shape: %q", tok)
	}
	mid := lastDot + 5
	tampered := tok[:mid] + flipChar(tok[mid]) + tok[mid+1:]
	if _, err := v.Verify(context.Background(), tampered); err == nil {
		t.Fatal("Verify accepted tampered signature")
	}
}

func TestVerify_rejectsMalformed(t *testing.T) {
	v, _ := newTestVerifier(t)
	cases := []string{
		"",
		"not-a-jwt",
		"abc.def",         // missing signature
		"abc.def.ghi.xyz", // too many segments
	}
	for _, bad := range cases {
		if _, err := v.Verify(context.Background(), bad); err == nil {
			t.Errorf("Verify accepted malformed token %q", bad)
		}
	}
}

func TestVerify_rejectsForeignIssuer(t *testing.T) {
	// Token issued by one provider; verifier configured for a different issuer.
	v, _ := newTestVerifier(t)
	other := oidcmock.NewProvider(t)
	tok := other.IssueToken(t, oidcmock.Claims{
		Subject:  "repo:foo/bar:ref:refs/heads/main",
		Audience: testAudience,
	})

	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("Verify accepted token from foreign issuer")
	}
}

// flipChar returns a char guaranteed to differ from c, staying in the base64-URL alphabet.
func flipChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

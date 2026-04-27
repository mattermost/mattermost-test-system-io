package apikey

import (
	"strings"
	"testing"
)

func TestIssueFormat(t *testing.T) {
	iss, err := Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got, want := len(iss.Prefix), prefixLen; got != want {
		t.Fatalf("prefix length = %d, want %d", got, want)
	}
	// plaintext is "tsio_key_<prefix>.<secret>" — literal marker + 8 chars + '.' + 22 chars.
	if got, want := len(iss.PlainText), len(PlaintextPrefix)+prefixLen+1+secretLen; got != want {
		t.Fatalf("plaintext length = %d, want %d", got, want)
	}
	if !strings.HasPrefix(iss.PlainText, PlaintextPrefix+iss.Prefix+".") {
		t.Fatalf("plaintext %q does not start with literal prefix + lookup-prefix + dot", iss.PlainText)
	}
	if !strings.HasPrefix(iss.Hash, "$argon2id$") {
		t.Fatalf("hash is not argon2id-encoded: %q", iss.Hash)
	}
}

func TestIssueAndVerifyRoundtrip(t *testing.T) {
	iss, err := Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !Verify(iss.PlainText, iss.Hash) {
		t.Fatalf("Verify(plaintext, hash) = false, want true")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	iss, err := Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Swap the tail while keeping the prefix — still well-formed, wrong secret.
	tampered := PlaintextPrefix + iss.Prefix + "." + strings.Repeat("A", secretLen)
	if Verify(tampered, iss.Hash) {
		t.Fatal("Verify accepted a wrong-secret plaintext")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	iss, err := Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cases := []string{
		"",
		"not-an-argon2id-hash",
		"$argon2id$v=19$$$", // missing sections
	}
	for _, bad := range cases {
		if Verify(iss.PlainText, bad) {
			t.Fatalf("Verify accepted malformed hash %q", bad)
		}
	}
}

func TestParsePlaintext(t *testing.T) {
	good := PlaintextPrefix + strings.Repeat("a", prefixLen) + "." + strings.Repeat("b", secretLen)
	prefix, secret, ok := ParsePlaintext(good)
	if !ok {
		t.Fatal("ParsePlaintext failed on well-formed input")
	}
	if len(prefix) != prefixLen || len(secret) != secretLen {
		t.Fatalf("lengths: prefix=%d secret=%d", len(prefix), len(secret))
	}
}

func TestParsePlaintextRejectsBadShape(t *testing.T) {
	body := strings.Repeat("a", prefixLen) + "." + strings.Repeat("b", secretLen)
	cases := []string{
		"",
		"nodothere",
		body, // missing the literal "tsio_key_" front-matter
		PlaintextPrefix + strings.Repeat("a", prefixLen+1) + "." + strings.Repeat("b", secretLen), // wrong prefix length
		PlaintextPrefix + strings.Repeat("a", prefixLen) + "." + strings.Repeat("b", secretLen-1), // short secret
		PlaintextPrefix + strings.Repeat("a", prefixLen) + "." + strings.Repeat("b", secretLen+1), // long secret
	}
	for _, bad := range cases {
		if _, _, ok := ParsePlaintext(bad); ok {
			t.Fatalf("ParsePlaintext accepted %q", bad)
		}
	}
}

func TestIssueUniqueness(t *testing.T) {
	seen := make(map[string]struct{})
	for range 16 {
		iss, err := Issue()
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if _, dup := seen[iss.PlainText]; dup {
			t.Fatal("Issue produced duplicate plaintext")
		}
		seen[iss.PlainText] = struct{}{}
	}
}

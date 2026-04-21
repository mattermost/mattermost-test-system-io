// Package apikey handles argon2id-hashed API-key credentials used for CI
// upload authentication.
package apikey

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP 2023 recommendation for interactive use).
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 46 * 1024 // 46 MiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	saltLen             = 16

	prefixLen = 8
	secretLen = 22 // base62-ish; 128 bits of entropy
)

// Issued represents a freshly minted API key. PlainText is shown once; never
// stored anywhere beyond the operator's terminal.
type Issued struct {
	PlainText string
	Prefix    string
	Hash      string // argon2id-encoded
}

// Issue produces a new API key: "<prefix>.<secret>", where prefix is 8 chars
// of URL-safe random material, and secret is 22 chars of URL-safe random.
// Returns the plaintext, the lookup prefix, and the argon2id-encoded hash.
func Issue() (Issued, error) {
	prefix, err := randomBase62(prefixLen)
	if err != nil {
		return Issued{}, err
	}
	secret, err := randomBase62(secretLen)
	if err != nil {
		return Issued{}, err
	}
	plaintext := prefix + "." + secret
	hash, err := hashArgon2id(plaintext)
	if err != nil {
		return Issued{}, err
	}
	return Issued{PlainText: plaintext, Prefix: prefix, Hash: hash}, nil
}

// Verify returns true if plaintext matches the stored argon2id hash.
func Verify(plaintext, encodedHash string) bool {
	salt, wantKey, params, err := parseEncoded(encodedHash)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plaintext), salt, params.time, params.memory, params.threads, params.keyLen)
	return subtle.ConstantTimeCompare(got, wantKey) == 1
}

// ParsePlaintext splits "<prefix>.<secret>"; returns (prefix, "", false) when malformed.
func ParsePlaintext(plaintext string) (prefix, secret string, ok bool) {
	i := strings.IndexByte(plaintext, '.')
	if i != prefixLen {
		return "", "", false
	}
	if len(plaintext)-i-1 != secretLen {
		return "", "", false
	}
	return plaintext[:i], plaintext[i+1:], true
}

// randomBase62 returns n base62 chars (0-9 A-Z a-z) of entropy.
func randomBase62(n int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}

func hashArgon2id(plaintext string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gen salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

type argonParams struct {
	time, memory uint32
	threads      uint8
	keyLen       uint32
}

func parseEncoded(s string) ([]byte, []byte, argonParams, error) {
	// Expected: $argon2id$v=19$m=...,t=...,p=...$SALT$KEY
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, argonParams{}, errors.New("malformed argon2id hash")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return nil, nil, argonParams{}, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, argonParams{}, fmt.Errorf("decode salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, argonParams{}, fmt.Errorf("decode key: %w", err)
	}
	if len(key) > (1 << 16) { // sanity upper bound on hash key length
		return nil, nil, argonParams{}, fmt.Errorf("implausible key length: %d", len(key))
	}
	return salt, key, argonParams{time: t, memory: m, threads: p, keyLen: uint32(len(key))}, nil //nolint:gosec // bounded above
}

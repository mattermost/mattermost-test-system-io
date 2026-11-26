package verdicts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRedactErrorContext(t *testing.T) {
	t.Run("nil and null return nil", func(t *testing.T) {
		for _, in := range []json.RawMessage{nil, {}, []byte("   "), []byte("null")} {
			got, err := RedactErrorContext(in)
			if err != nil {
				t.Fatalf("unexpected err for %q: %v", string(in), err)
			}
			if got != nil {
				t.Fatalf("got %s, want nil", string(got))
			}
		}
	})

	t.Run("rejects non-object and invalid JSON and oversize", func(t *testing.T) {
		if _, err := RedactErrorContext([]byte(`["not-an-object"]`)); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation for array, got %v", err)
		}
		if _, err := RedactErrorContext([]byte(`{bad`)); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation for invalid json, got %v", err)
		}
		if _, err := RedactErrorContext([]byte(`{"a":1}{"b":2}`)); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation for trailing json, got %v", err)
		}
		oversize := []byte(`{"msg":"` + strings.Repeat("a", MaxErrorContextBytes) + `"}`)
		if _, err := RedactErrorContext(oversize); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation for oversize payload, got %v", err)
		}
	})

	t.Run("redacts sensitive keys, tokens, URLs, emails, NULs while preserving safe fields", func(t *testing.T) {
		raw := json.RawMessage(`{
			"authorization": "Bearer super-secret-jwt",
			"x-api-key": "tsio_key_12345",
			"userEmail": "alice@example.com",
			"credentials": {"username": "bot", "pin": "9999"},
			"headers": {"Authorization": "Basic dXNlcjpwYXNz", "Content-Type": "application/json"},
			"input_tokens": 1200,
			"token_count": 42,
			"has_password": false,
			"message": "failed connecting to https://svc:topsecret@db.internal:5432/app and https://ci:s3cr3tpass@minio.local/bucket/file.png?X-Amz-Signature=deadbeef&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE",
			"stack": "Error at login\nBearer my.jwt.token\nghp_1234567890abcdef1234567890abcdef\ngithub_pat_1234567890_abcdef123456\nxoxb-123456789012-abcdef\nsk-ant-api03-abcdef1234567890\nAKIAIOSFODNN7EXAMPLE\neyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U\nclient_secret=verysecretvalue password=\"p@ss w0rd\" email=bob@corp.test\u0000",
			"logs": ["contact admin@mattermost.com", "safe log line: locator('[data-testid=send]') timed out"]
		}`)

		out, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal redacted: %v", err)
		}

		mustNotContain := []string{
			"super-secret-jwt",
			"tsio_key_12345",
			"alice@example.com",
			"bob@corp.test",
			"admin@mattermost.com",
			"dXNlcjpwYXNz",
			"topsecret",
			"deadbeef",
			"AKIAIOSFODNN7EXAMPLE",
			"ghp_1234567890abcdef",
			"github_pat_1234567890",
			"xoxb-123456789012",
			"sk-ant-api03",
			"eyJhbGciOiJIUzI1NiJ9",
			"verysecretvalue",
			"p@ss w0rd",
			"9999",
			"bot",
			"\x00",
		}
		outStr := string(out)
		for _, secret := range mustNotContain {
			if strings.Contains(outStr, secret) {
				t.Errorf("redacted payload leaked %q: %s", secret, outStr)
			}
		}

		if got["authorization"] != redactedPlaceholder {
			t.Errorf("authorization = %v, want %s", got["authorization"], redactedPlaceholder)
		}
		if got["x-api-key"] != redactedPlaceholder {
			t.Errorf("x-api-key = %v, want %s", got["x-api-key"], redactedPlaceholder)
		}
		if got["userEmail"] != redactedPlaceholder {
			t.Errorf("userEmail = %v, want %s", got["userEmail"], redactedPlaceholder)
		}
		if got["input_tokens"] != float64(1200) {
			t.Errorf("input_tokens = %v, want 1200", got["input_tokens"])
		}
		if got["token_count"] != float64(42) {
			t.Errorf("token_count = %v, want 42", got["token_count"])
		}
		if got["has_password"] != false {
			t.Errorf("has_password = %v, want false", got["has_password"])
		}
		headers := got["headers"].(map[string]any)
		if headers["Content-Type"] != "application/json" {
			t.Errorf("headers.Content-Type = %v, want application/json", headers["Content-Type"])
		}
		if headers["Authorization"] != redactedPlaceholder {
			t.Errorf("headers.Authorization = %v, want %s", headers["Authorization"], redactedPlaceholder)
		}
		msg := got["message"].(string)
		if !strings.Contains(msg, "https://[REDACTED]@db.internal:5432/app") {
			t.Errorf("credentialed URL not redacted properly: %s", msg)
		}
		if !strings.Contains(msg, "https://[REDACTED]@minio.local/bucket/file.png?[REDACTED]") {
			t.Errorf("query string URL not redacted properly: %s", msg)
		}
		logs := got["logs"].([]any)
		if logs[1] != "safe log line: locator('[data-testid=send]') timed out" {
			t.Errorf("safe log modified: %v", logs[1])
		}
	})
}

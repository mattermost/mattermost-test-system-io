package verdicts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
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

	t.Run("redacts compound sensitive keys while preserving non-secret keys", func(t *testing.T) {
		raw := json.RawMessage(`{
			"Api_Key": "secret-api-key-1",
			"X-API-Key": "secret-x-api-key-2",
			"X-Admin-Key": "secret-admin-key-3",
			"Authorization": "opaque-auth-4",
			"openai_api_key": "raw-openai-secret-5",
			"openaiApiKey": "raw-openai-secret-6",
			"openaiAPIKey": "raw-openai-secret-6b",
			"aws_access_key": "raw-aws-secret-7",
			"ssh_private_key": "raw-ssh-secret-8",
			"signing_secret_key": "raw-signing-secret-9",
			"proxy_auth": "raw-proxy-auth-10",
			"x_custom_auth": "raw-custom-auth-11",
			"webhook_secret": "raw-webhook-secret-12",
			"custom_admin_key": "raw-admin-secret-13",
			"product_license_key": "raw-license-secret-14",
			"oauth_client_secret": "raw-client-secret-15",
			"test_key": "h:login",
			"object_key": "reports/1/a.json",
			"input_tokens": 1200,
			"token_count": 42,
			"has_password": false,
			"secret_version": 3,
			"auth_provider": "github"
		}`)

		out, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		redactedKeys := []string{
			"Api_Key", "X-API-Key", "X-Admin-Key", "Authorization",
			"openai_api_key", "openaiApiKey", "openaiAPIKey",
			"aws_access_key", "ssh_private_key", "signing_secret_key",
			"proxy_auth", "x_custom_auth", "webhook_secret",
			"custom_admin_key", "product_license_key", "oauth_client_secret",
		}
		for _, k := range redactedKeys {
			if got[k] != redactedPlaceholder {
				t.Errorf("%s = %v, want %s", k, got[k], redactedPlaceholder)
			}
		}

		if got["test_key"] != "h:login" {
			t.Errorf("test_key = %v, want h:login", got["test_key"])
		}
		if got["object_key"] != "reports/1/a.json" {
			t.Errorf("object_key = %v, want reports/1/a.json", got["object_key"])
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
		if got["secret_version"] != float64(3) {
			t.Errorf("secret_version = %v, want 3", got["secret_version"])
		}
		if got["auth_provider"] != "github" {
			t.Errorf("auth_provider = %v, want github", got["auth_provider"])
		}
	})

	t.Run("redacts nested objects and arrays under sensitive keys while preserving numeric/bool metadata", func(t *testing.T) {
		raw := json.RawMessage(`{
			"auth": {"raw": "secret-in-auth-obj", "expires_in": 3600},
			"token": {"value": "secret-in-token-obj", "input_tokens": 1200, "token_count": 42, "has_password": false},
			"password": ["secret-in-password-arr", {"deep": "nested-pw-secret"}],
			"api_key": {"key": "secret-in-apikey-obj"},
			"authorization": ["raw-opaque-auth-in-arr"],
			"credentials": {"nested": {"deep": "secret-in-credentials"}},
			"auth_headers": {"X-Custom": "secret-in-auth-headers"},
			"openai_api_key": {"primary": "secret-nested-openai", "backup": ["secret-backup-openai"]}
		}`)

		out, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		outStr := string(out)
		for _, leak := range []string{
			"secret-in-auth-obj",
			"secret-in-token-obj",
			"secret-in-password-arr",
			"nested-pw-secret",
			"secret-in-apikey-obj",
			"raw-opaque-auth-in-arr",
			"secret-in-credentials",
			"secret-in-auth-headers",
			"secret-nested-openai",
			"secret-backup-openai",
		} {
			if strings.Contains(outStr, leak) {
				t.Errorf("nested sensitive value leaked %q: %s", leak, outStr)
			}
		}

		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		authObj := got["auth"].(map[string]any)
		if authObj["raw"] != redactedPlaceholder || authObj["expires_in"] != float64(3600) {
			t.Errorf("unexpected auth object: %v", authObj)
		}
		tokObj := got["token"].(map[string]any)
		if tokObj["value"] != redactedPlaceholder || tokObj["input_tokens"] != float64(1200) || tokObj["token_count"] != float64(42) || tokObj["has_password"] != false {
			t.Errorf("unexpected token object: %v", tokObj)
		}
		pwArr := got["password"].([]any)
		if pwArr[0] != redactedPlaceholder || pwArr[1].(map[string]any)["deep"] != redactedPlaceholder {
			t.Errorf("unexpected password array: %v", pwArr)
		}
		apiKeyObj := got["api_key"].(map[string]any)
		if apiKeyObj["key"] != redactedPlaceholder {
			t.Errorf("unexpected api_key object: %v", apiKeyObj)
		}
		authzArr := got["authorization"].([]any)
		if authzArr[0] != redactedPlaceholder {
			t.Errorf("unexpected authorization array: %v", authzArr)
		}
		openAI := got["openai_api_key"].(map[string]any)
		if openAI["primary"] != redactedPlaceholder || openAI["backup"].([]any)[0] != redactedPlaceholder {
			t.Errorf("unexpected openai_api_key object: %v", openAI)
		}
	})
}

func TestRedactString_NULAndInvalidUTF8Boundaries(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		forbidden []string
	}{
		{
			name:      "bearer after invalid utf8 and nul",
			in:        "ok\xff\xfe\x00Bearer secret123",
			forbidden: []string{"secret123", "\x00"},
		},
		{
			name:      "github token after nul",
			in:        "ok\x00ghp_1234567890abcdef1234567890abcdef",
			forbidden: []string{"ghp_1234567890abcdef", "\x00"},
		},
		{
			name:      "jwt after invalid utf8",
			in:        "ok\xffeyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			forbidden: []string{"eyJhbGciOiJIUzI1NiJ9", "dozjgNryP4J3jVmNHl0w5N"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.in)
			if !utf8.ValidString(got) {
				t.Fatalf("RedactString produced invalid UTF-8: %q", got)
			}
			for _, f := range tc.forbidden {
				if strings.Contains(got, f) {
					t.Errorf("RedactString leaked %q in %q", f, got)
				}
			}
			if !strings.Contains(got, redactedPlaceholder) {
				t.Errorf("RedactString did not redact %q: got %q", tc.in, got)
			}
		})
	}
}

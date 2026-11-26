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
		oversizeSpaces := []byte(strings.Repeat(" ", MaxErrorContextBytes+1))
		if _, err := RedactErrorContext(oversizeSpaces); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation for oversize whitespace payload, got %v", err)
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
			`\u0000`,
		}
		outStr := string(out)
		for _, secret := range mustNotContain {
			if strings.Contains(outStr, secret) {
				t.Errorf("redacted payload leaked %q: %s", secret, outStr)
			}
		}
		if stack, _ := got["stack"].(string); strings.Contains(stack, "\x00") {
			t.Errorf("decoded stack still contains NUL byte: %q", stack)
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

	t.Run("redacts unescaped and backslash-escaped JSON credentials inside message and stack", func(t *testing.T) {
		raw := json.RawMessage(`{
			"has_password": false,
			"input_tokens": 1200,
			"message": "HTTP 401 body: {\"password\":\"hunter2\",\"api_key\":\"custom_secret_key\",\"client_secret\":\"s3cr3t\",\"my_secret\":\"p@ss\\\"w0rd\",\"auth_provider\":\"github\",\"status\":401}",
			"stack": "Error: upstream failed\nbody=\"{\\\"password\\\":\\\"esc_hunter2\\\",\\\"api_key\\\":\\\"esc_secret_key\\\",\\\"client_secret\\\":\\\"esc_s3cr3t\\\",\\\"token\\\":\\\"p@ss\\\\\\\"w0rd\\\",\\\"user\\\":\\\"alice\\\"}\""
		}`)

		out, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}

		outStr := string(out)
		for _, leak := range []string{
			"hunter2",
			"custom_secret_key",
			"s3cr3t",
			`p@ss\"w0rd`,
			"p@ss",
			"w0rd",
			"esc_hunter2",
			"esc_secret_key",
			"esc_s3cr3t",
		} {
			if strings.Contains(outStr, leak) {
				t.Errorf("redacted payload leaked %q: %s", leak, outStr)
			}
		}

		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["has_password"] != false {
			t.Errorf("has_password = %v, want false", got["has_password"])
		}
		if got["input_tokens"] != float64(1200) {
			t.Errorf("input_tokens = %v, want 1200", got["input_tokens"])
		}

		msg := got["message"].(string)
		if !strings.Contains(msg, `"auth_provider":"github"`) || !strings.Contains(msg, `"status":401`) {
			t.Errorf("message dropped non-secret JSON fields: %s", msg)
		}
		if !strings.Contains(msg, `"password":[REDACTED]`) || !strings.Contains(msg, `"api_key":[REDACTED]`) || !strings.Contains(msg, `"client_secret":[REDACTED]`) {
			t.Errorf("message missing expected [REDACTED] markers: %s", msg)
		}

		stack := got["stack"].(string)
		if !strings.Contains(stack, `\"user\":\"alice\"`) {
			t.Errorf("stack dropped or over-redacted non-secret field: %s", stack)
		}
		if !strings.Contains(stack, `\"password\":[REDACTED]`) || !strings.Contains(stack, `\"api_key\":[REDACTED]`) || !strings.Contains(stack, `\"client_secret\":[REDACTED]`) {
			t.Errorf("stack missing expected [REDACTED] markers: %s", stack)
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

func TestRedactString_MultiEscapedAndBoundaryCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "doubly-escaped JSON",
			in:   `{\\"password\\":\\"secret\\",\\"user\\":\\"bob\\"}`,
			want: `{\\"password\\":[REDACTED],\\"user\\":\\"bob\\"}`,
		},
		{
			name: "triply-escaped JSON",
			in:   `{\\\"password\\\":\\\"secret\\\",\\\"user\\\":\\\"bob\\\"}`,
			want: `{\\\"password\\\":[REDACTED],\\\"user\\\":\\\"bob\\\"}`,
		},
		{
			name: "doubly-escaped Bearer token",
			in:   `{\\"authorization\\":\\"Bearer tok123\\",\\"user\\":\\"alice\\"}`,
			want: `{\\"authorization\\":[REDACTED],\\"user\\":\\"alice\\"}`,
		},
		{
			name: "unescaped JSON trailing backslash preserves delimiter",
			in:   `{"password":"p@ss\\","user":"bob"}`,
			want: `{"password":[REDACTED],"user":"bob"}`,
		},
		{
			name: "escaped JSON trailing backslash preserves delimiter",
			in:   `{\"password\":\"p@ss\\\\\",\"user\":\"bob\"}`,
			want: `{\"password\":[REDACTED],\"user\":\"bob\"}`,
		},
		{
			name: "escaped JSON interior escaped quote",
			in:   `{\"password\":\"p@ss\\\"w0rd\",\"user\":\"bob\"}`,
			want: `{\"password\":[REDACTED],\"user\":\"bob\"}`,
		},
		{
			name: "escaped JSON backslash plus interior escaped quote",
			in:   `{\"password\":\"p@ss\\\\\\\"w0rd\",\"user\":\"bob\"}`,
			want: `{\"password\":[REDACTED],\"user\":\"bob\"}`,
		},
		{
			name: "non-secret JSON booleans and null preserved in string",
			in:   `{"has_password": false, "has_token": true, "password": null, "input_tokens": 1200, "secret_count": 3, "password_required": true}`,
			want: `{"has_password": false, "has_token": true, "password": null, "input_tokens": 1200, "secret_count": 3, "password_required": true}`,
		},
		{
			name: "boolean preserved while sibling secret redacted",
			in:   `{"has_password": false, "password": "secret"}`,
			want: `{"has_password": false, "password": [REDACTED]}`,
		},
		{
			name: "escaped boolean preserved while sibling secret redacted",
			in:   `{\"has_password\":false,\"password\":\"secret\"}`,
			want: `{\"has_password\":false,\"password\":[REDACTED]}`,
		},
		{
			name: "unquoted numeric secrets still redacted",
			in:   `password=123456 api_key=999`,
			want: `password=[REDACTED] api_key=[REDACTED]`,
		},
		{
			name: "quoted literal false still redacted",
			in:   `{"password": "false"}`,
			want: `{"password": [REDACTED]}`,
		},
		{
			name: "unclosed escaped quote redacts truncated secret",
			in:   `password=\"unclosed`,
			want: `password=[REDACTED]`,
		},
		{
			name: "unclosed escaped quote stops at newline",
			in:   "password=\\\"unclosed\nuser=bob",
			want: "password=[REDACTED]\nuser=bob",
		},
		{
			name: "truncated unclosed JSON secret redacts",
			in:   `{"password":"my_secret`,
			want: `{"password":[REDACTED]`,
		},
		{
			name: "unclosed escaped quote inside JSON string preserves sibling field",
			in:   `{"msg": "password=\"unclosed", "other": "safe"}`,
			want: `{"msg": "password=[REDACTED]", "other": "safe"}`,
		},
		{
			name: "authorization bearer with unclosed quote stops at newline",
			in:   "authorization: Bearer \"unclosed\nuser=bob",
			want: "authorization: [REDACTED]\nuser=bob",
		},
		{
			name: "credentialed URL with @ in password redacts full userinfo",
			in:   `https://user:p@ssword@localhost:5432/app`,
			want: `https://[REDACTED]@localhost:5432/app`,
		},
		{
			name: "credentialed URL with multiple @ and query does not bridge to next URL",
			in:   `https://user:p@ss@word@db.internal:5432/app?token=abc and https://host2.local/ok?x=1`,
			want: `https://[REDACTED]@db.internal:5432/app?[REDACTED] and https://host2.local/ok?[REDACTED]`,
		},
		{
			name: "URL query containing email redacts query without false userinfo match",
			in:   `https://host.com:8080?email=alice@example.com`,
			want: `https://host.com:8080?[REDACTED]`,
		},
		{
			name: "unquoted bearer with non-base64 suffix does not leak",
			in:   `authorization=Bearer tok$supersecret user=bob`,
			want: `authorization=[REDACTED] user=bob`,
		},
		{
			name: "unquoted github token with suffix does not leak",
			in:   `api_key=ghp_1234567890123456$tailsecret user=bob`,
			want: `api_key=[REDACTED] user=bob`,
		},
		{
			name: "unquoted bearer followed by sibling credential has no stray bracket",
			in:   `authorization=Bearer tok123 api_key=k1`,
			want: `authorization=[REDACTED] api_key=[REDACTED]`,
		},
		{
			name: "unquoted credentialed URL under sensitive key redacts full URL",
			in:   `password=https://alice:secret@example.com/internal/path user=bob`,
			want: `password=[REDACTED] user=bob`,
		},
		{
			name: "standalone closing bracket still terminates unquoted value",
			in:   `[api_key=k1]`,
			want: `[api_key=[REDACTED]]`,
		},
		{
			name: "quoted basic auth token does not leak",
			in:   `authorization: Basic "tok123", user: bob`,
			want: `authorization: [REDACTED], user: bob`,
		},
		{
			name: "unquoted basic auth with non-base64 dollar suffix does not leak",
			in:   `authorization=Basic tok$supersecret user=bob`,
			want: `authorization=[REDACTED] user=bob`,
		},
		{
			name: "unquoted basic auth with colon credential does not leak",
			in:   `authorization=Basic alice:secret user=bob`,
			want: `authorization=[REDACTED] user=bob`,
		},
		{
			name: "unquoted basic auth base64 token collapses to single placeholder",
			in:   `authorization=Basic dXNlcjpwYXNz user=bob`,
			want: `authorization=[REDACTED] user=bob`,
		},
		{
			name: "JSON basic auth header collapses to single placeholder",
			in:   `{"authorization":"Basic dXNlcjpwYXNz","ok":true}`,
			want: `{"authorization":[REDACTED],"ok":true}`,
		},
		{
			name: "literal password basic with trailing space does not leak",
			in:   `password=basic `,
			want: `password=[REDACTED] `,
		},
		{
			name: "literal password basic before space and comma does not leak",
			in:   `password=basic , user=bob`,
			want: `password=[REDACTED] , user=bob`,
		},
		{
			name: "literal password basic before tab and comma does not leak",
			in:   "password=basic\t, user=bob",
			want: "password=[REDACTED]\t, user=bob",
		},
		{
			name: "literal password bearer before space and comma does not leak",
			in:   `password=bearer , user=bob`,
			want: `password=[REDACTED] , user=bob`,
		},
		{
			name: "literal password basic preserves sibling user field",
			in:   `password=basic user=bob`,
			want: `password=[REDACTED] user=bob`,
		},
		{
			name: "literal password bearer preserves sibling user field",
			in:   `password=bearer user=bob`,
			want: `password=[REDACTED] user=bob`,
		},
		{
			name: "literal password basic preserves multiple sibling fields",
			in:   `password=basic foo=bar baz=qux`,
			want: `password=[REDACTED] foo=bar baz=qux`,
		},
		{
			name: "literal password basic preserves newline sibling field",
			in:   "password=basic\nuser=bob",
			want: "password=[REDACTED]\nuser=bob",
		},
		{
			name: "literal password basic preserves trailing boolean or null",
			in:   `password=basic false true null`,
			want: `password=[REDACTED] false true null`,
		},
		{
			name: "authorization basic with trailing space falls back to redacting scheme",
			in:   `authorization=Basic `,
			want: `authorization=[REDACTED] `,
		},
		{
			name: "authorization basic before comma falls back to redacting scheme",
			in:   `authorization=Basic , user=bob`,
			want: `authorization=[REDACTED] , user=bob`,
		},
		{
			name: "authorization basic before boolean falls back to redacting scheme",
			in:   `authorization=Basic false`,
			want: `authorization=[REDACTED] false`,
		},
		{
			name: "authorization basic tab-separated escaped quote does not leak",
			in:   "authorization: basic\t\\\"tok123\\\", user: bob",
			want: "authorization: [REDACTED], user: bob",
		},
		{
			name: "non-secret auth_mode basic preserves sibling key-value pairs",
			in:   `auth_mode=basic user=bob password=secret`,
			want: `auth_mode=basic user=bob password=[REDACTED]`,
		},
		{
			name: "standalone basic auth with base64 padding still redacts",
			in:   `header Basic dXNlcjpwYXNz== ok`,
			want: `header Basic [REDACTED] ok`,
		},
		{
			name: "standalone basic across newline does not cross into sibling key-value",
			in:   "auth_mode=basic\nuser=bob",
			want: "auth_mode=basic\nuser=bob",
		},
		{
			name: "token bearer with sibling user field",
			in:   `token=Bearer tok123456 user=bob`,
			want: `token=[REDACTED] user=bob`,
		},
		{
			name: "auth_token bearer with sibling user field",
			in:   `auth_token=Bearer mysecret123 user=bob`,
			want: `auth_token=[REDACTED] user=bob`,
		},
		{
			name: "api_key bearer with sibling user field",
			in:   `api_key=Bearer mysecret123 user=bob`,
			want: `api_key=[REDACTED] user=bob`,
		},
		{
			name: "secret basic with sibling user field",
			in:   `secret=Basic dXNlcjpwYXNz user=bob`,
			want: `secret=[REDACTED] user=bob`,
		},
		{
			name: "secret basic with double base64 padding and sibling user field",
			in:   `secret=Basic dXNlcjpwYXNz== user=bob`,
			want: `secret=[REDACTED] user=bob`,
		},
		{
			name: "cookie bearer with sibling user field",
			in:   `cookie=Bearer tok123456 user=bob`,
			want: `cookie=[REDACTED] user=bob`,
		},
		{
			name: "license bearer with sibling user field",
			in:   `license=Bearer tok123456 user=bob`,
			want: `license=[REDACTED] user=bob`,
		},
		{
			name: "proxy_authorization bearer with sibling user field",
			in:   `proxy_authorization=Bearer tok123456 user=bob`,
			want: `proxy_authorization=[REDACTED] user=bob`,
		},
		{
			name: "token bearer with double base64 padding and sibling user field",
			in:   `token=Bearer dXNlcjpwYXNz== user=bob`,
			want: `token=[REDACTED] user=bob`,
		},
		{
			name: "token bearer before sibling key-value",
			in:   `token=Bearer user=bob`,
			want: `token=[REDACTED] user=bob`,
		},
		{
			name: "authorization basic before sibling key-value",
			in:   `authorization=Basic user=bob`,
			want: `authorization=[REDACTED] user=bob`,
		},
		{
			name: "secret basic before quoted sibling key-value",
			in:   `secret=basic user="bob"`,
			want: `secret=[REDACTED] user="bob"`,
		},
		{
			name: "token bearer before quoted sibling key-value",
			in:   `token=Bearer user="bob"`,
			want: `token=[REDACTED] user="bob"`,
		},
		{
			name: "token bearer before sibling sensitive key-value",
			in:   `token=Bearer password=secret user=bob`,
			want: `token=[REDACTED] password=[REDACTED] user=bob`,
		},
		{
			name: "token bearer before colon sibling key-value",
			in:   `token=Bearer user: bob`,
			want: `token=[REDACTED] user: bob`,
		},
		{
			name: "password basic does not consume trailing non-kv token",
			in:   `password=basic tok123456`,
			want: `password=[REDACTED] tok123456`,
		},
		{
			name: "passwd basic preserves sibling user field",
			in:   `passwd=basic user=bob`,
			want: `passwd=[REDACTED] user=bob`,
		},
		{
			name: "passphrase bearer preserves sibling user field",
			in:   `passphrase=bearer user=bob`,
			want: `passphrase=[REDACTED] user=bob`,
		},
		{
			name: "JSON token with bearer scheme",
			in:   `{"token":"Bearer tok123"}`,
			want: `{"token":[REDACTED]}`,
		},
		{
			name: "JSON password with literal basic value",
			in:   `{"password":"basic"}`,
			want: `{"password":[REDACTED]}`,
		},
		{
			name: "standalone basic before double-quoted sibling",
			in:   `auth_mode=basic user="bob"`,
			want: `auth_mode=basic user="bob"`,
		},
		{
			name: "standalone basic before single-quoted sibling",
			in:   `auth_mode=basic user='bob'`,
			want: `auth_mode=basic user='bob'`,
		},
		{
			name: "standalone basic before escaped-quoted sibling",
			in:   `auth_mode=basic user=\"bob\"`,
			want: `auth_mode=basic user=\"bob\"`,
		},
		{
			name: "standalone basic before leading-space quoted sibling",
			in:   `auth_mode=basic user=" bob"`,
			want: `auth_mode=basic user=" bob"`,
		},
		{
			name: "standalone basic before empty-quoted sibling",
			in:   `auth_mode=basic user=""`,
			want: `auth_mode=basic user=""`,
		},
		{
			name: "standalone basic before colon sibling",
			in:   `auth_mode=basic user: bob`,
			want: `auth_mode=basic user: bob`,
		},
		{
			name: "standalone basic before colon authorization sibling",
			in:   `auth_mode=basic authorization: Bearer tok123`,
			want: `auth_mode=basic authorization: [REDACTED]`,
		},
		{
			name: "JSON header with padded basic token",
			in:   `{"header": "Basic dXNlcjpwYXM=", "user": "bob"}`,
			want: `{"header": "Basic [REDACTED]", "user": "bob"}`,
		},
		{
			name: "JSON msg with authorization basic padded token and sibling",
			in:   `{"msg": "authorization=Basic dXNlcjpwYXM=", "user": "bob"}`,
			want: `{"msg": "authorization=[REDACTED]", "user": "bob"}`,
		},
		{
			name: "JSON msg with authorization basic padded token at end",
			in:   `{"msg": "authorization=Basic dXNlcjpwYXM="}`,
			want: `{"msg": "authorization=[REDACTED]"}`,
		},
		{
			name: "JSON msg with standalone basic padded token at end",
			in:   `{"msg":"Basic YWI="}`,
			want: `{"msg":"Basic [REDACTED]"}`,
		},
		{
			name: "JSON msg with standalone basic padded token and sibling",
			in:   `{"msg":"Basic YWI=", "ok":true}`,
			want: `{"msg":"Basic [REDACTED]", "ok":true}`,
		},
		{
			name: "quoted log header with padded basic token",
			in:   `header="Basic dXNlcjpwYXNz=" ok`,
			want: `header="Basic [REDACTED]" ok`,
		},
		{
			name: "quoted log header with padded basic token before quoted sibling",
			in:   `header="Basic dXNlcjpwYXNz=" other="foo"`,
			want: `header="Basic [REDACTED]" other="foo"`,
		},
		{
			name: "quoted log header with authorization basic padded token before quoted sibling",
			in:   `header="authorization=Basic dXNlcjpwYXM=" other="foo"`,
			want: `header="authorization=[REDACTED]" other="foo"`,
		},
		{
			name: "standalone basic token",
			in:   `Basic dXNlcjpwYXNz`,
			want: `Basic [REDACTED]`,
		},
		{
			name: "JSON msg with header padded basic token and quoted sibling",
			in:   `{"msg": "header=\"Basic YWI=\"", "other": "\"foo\""}`,
			want: `{"msg": "header=\"Basic [REDACTED]\"", "other": "\"foo\""}`,
		},
		{
			name: "quoted basic padded token followed by concatenated quoted literal",
			in:   `"Basic YWI="+"foo"`,
			want: `"Basic [REDACTED]"+"foo"`,
		},
		{
			name: "kv quoted header padded basic token immediately followed by quoted sibling",
			in:   `header="Basic YWI="next="foo"`,
			want: `header="Basic [REDACTED]"next="foo"`,
		},
		{
			name: "JSON msg with non-secret auth_mode=basic and quoted user preserves values",
			in:   `{"msg": "auth_mode=basic user=\"bob\"", "other": "\"foo\""}`,
			want: `{"msg": "auth_mode=basic user=\"bob\"", "other": "\"foo\""}`,
		},
		{
			name: "unclosed deeply escaped quote preserves shallower closing backslash",
			in:   `{"msg":"{\"k\":\"password=\\\"unclosed\"}"}`,
			want: `{"msg":"{\"k\":\"password=[REDACTED]\"}"}`,
		},
		{
			name: "port URL followed by comma and email does not bridge credURLRE",
			in:   `https://api.example.com:8443,owner=alice@example.com`,
			want: `https://api.example.com:8443,owner=[REDACTED]`,
		},
		{
			name: "URL with userinfo without password separator is redacted",
			in:   `https://deploy-token@gitlab/path`,
			want: `https://[REDACTED]@gitlab/path`,
		},
		{
			name: "URL with multiple @ in password is redacted",
			in:   `https://user:p@ss@word@localhost:5432/app`,
			want: `https://[REDACTED]@localhost:5432/app`,
		},
		{
			name: "unquoted Cookie header redacts all semicolon-separated pairs",
			in:   `Cookie: theme=dark; sid=secret123`,
			want: `Cookie: [REDACTED]`,
		},
		{
			name: "unquoted Set-Cookie header redacts cookie and attributes",
			in:   `Set-Cookie: sid=secret123; Path=/; HttpOnly`,
			want: `Set-Cookie: [REDACTED]`,
		},
		{
			name: "unquoted Cookie header stops at newline and preserves sibling field",
			in:   "Cookie: theme=dark; sid=secret123\nuser=bob",
			want: "Cookie: [REDACTED]\nuser=bob",
		},
		{
			name: "unquoted Cookie header inside JSON string stops at closing quote",
			in:   `{"msg": "Cookie: theme=dark; sid=secret123", "user": "bob"}`,
			want: `{"msg": "Cookie: [REDACTED]", "user": "bob"}`,
		},
		{
			name: "quoted Set-Cookie header redacts quoted value and attributes",
			in:   `Set-Cookie: session="abcdef123456"; Path=/; HttpOnly`,
			want: `Set-Cookie: [REDACTED]`,
		},
		{
			name: "quoted Cookie header redacts multiple quoted pairs",
			in:   `Cookie: a="secret1"; b="secret2"`,
			want: `Cookie: [REDACTED]`,
		},
		{
			name: "escaped quoted Set-Cookie inside JSON string preserves outer JSON",
			in:   `{"msg": "Set-Cookie: session=\"abcdef123456\"; Path=/", "user": "bob"}`,
			want: `{"msg": "Set-Cookie: [REDACTED]", "user": "bob"}`,
		},
		{
			name: "unclosed quoted Set-Cookie stops at newline",
			in:   "Set-Cookie: session=\"unclosed\nuser=bob",
			want: "Set-Cookie: [REDACTED]\nuser=bob",
		},
		{
			name: "X-Admin-Key and admin_key in plain text are redacted while test_key and object_key remain",
			in:   `X-Admin-Key: secret-admin-val admin_key=another-secret test_key=foo object_key=bar`,
			want: `X-Admin-Key: [REDACTED] admin_key=[REDACTED] test_key=foo object_key=bar`,
		},
		{
			name: "postgresql URI query string with apostrophe is redacted as a whole",
			in:   `postgresql://h/db?sslpassword=ab'cd&sslmode=require`,
			want: `postgresql://h/db?[REDACTED]`,
		},
		{
			name: "postgres URI query string stops at comma and semicolon",
			in:   `postgres://user:pa,ss@localhost:5432/db?sslpassword=ab'cd, next=ok; other=1`,
			want: `postgres://[REDACTED]@localhost:5432/db?[REDACTED], next=ok; other=1`,
		},
		{
			name: "postgres URL password containing comma is redacted",
			in:   `postgres://user:pa,ss@localhost:5432/db`,
			want: `postgres://[REDACTED]@localhost:5432/db`,
		},
		{
			name: "postgres URL password containing multiple @ is redacted",
			in:   `postgres://user:p@ss@word@localhost:5432/app`,
			want: `postgres://[REDACTED]@localhost:5432/app`,
		},
		{
			name: "postgres credentialed URL followed by comma email does not bridge across =",
			in:   `postgres://user:pass@localhost:5432,email=a@b.com`,
			want: `postgres://[REDACTED]@localhost:5432,email=[REDACTED]`,
		},
		{
			name: "URL query with email does not falsely match userinfo",
			in:   `https://host.com?email=a@b.com`,
			want: `https://host.com?[REDACTED]`,
		},
		{
			name: "comma-separated URLs redact only credentialed URL",
			in:   `https://foo.com,https://user:pass@bar.com/path`,
			want: `https://foo.com,https://[REDACTED]@bar.com/path`,
		},
		{
			name: "URL userinfo containing semicolon is redacted",
			in:   `postgres://user:pa;ss@localhost:5432/db`,
			want: `postgres://[REDACTED]@localhost:5432/db`,
		},
		{
			name: "URL userinfo containing comma and semicolon is redacted",
			in:   `https://user:pa,s;s@localhost:5432/db`,
			want: `https://[REDACTED]@localhost:5432/db`,
		},
		{
			name: "credentialed URL followed by semicolon and email does not bridge across =",
			in:   `https://user:pass@host.com;owner=a@b.com`,
			want: `https://[REDACTED]@host.com;owner=[REDACTED]`,
		},
		{
			name: "embedded JSON sensitive object value is redacted while sibling preserved",
			in:   `{"authorization":{"value":"sample-value"},"user":"bob"}`,
			want: `{"authorization":[REDACTED],"user":"bob"}`,
		},
		{
			name: "embedded JSON sensitive array value is redacted while sibling preserved",
			in:   `{"password":["secret-1","secret-2"],"user":"bob"}`,
			want: `{"password":[REDACTED],"user":"bob"}`,
		},
		{
			name: "escaped embedded JSON sensitive object inside msg is redacted",
			in:   `{"msg":"{\"authorization\":{\"value\":\"sample-value\"},\"user\":\"bob\"}","other":"safe"}`,
			want: `{"msg":"{\"authorization\":[REDACTED],\"user\":\"bob\"}","other":"safe"}`,
		},
		{
			name: "unclosed bracket inside JSON string does not bridge into sibling field",
			in:   `{"msg":"password=[abc","arr":"]"}`,
			want: `{"msg":"password=[REDACTED]","arr":"]"}`,
		},
		{
			name: "IPv6 HTTP URL query string is redacted",
			in:   `http://[::1]:8080/path?token=secret`,
			want: `http://[::1]:8080/path?[REDACTED]`,
		},
		{
			name: "IPv6 credentialed HTTPS URL with query string redacts both userinfo and query",
			in:   `https://user:pass@[2001:db8::1]:8443/path?token=secret`,
			want: `https://[REDACTED]@[2001:db8::1]:8443/path?[REDACTED]`,
		},
		{
			name: "IPv6 postgres URL query stops at comma",
			in:   `postgres://user:pass@[::1]:5432/db?sslpassword=secret, next=ok`,
			want: `postgres://[REDACTED]@[::1]:5432/db?[REDACTED], next=ok`,
		},
		{
			name: "IPv6 zone ID URL query string is redacted",
			in:   `http://[fe80::1%25eth0]:8080/path?token=secret`,
			want: `http://[fe80::1%25eth0]:8080/path?[REDACTED]`,
		},
		{
			name: "bracketed non-IPv6 URL does not bridge to later query",
			in:   `[https://example.com] see ?other=1`,
			want: `[https://example.com] see ?other=1`,
		},
		{
			name: "embedded JSON sensitive containers unescaped are redacted while metadata preserved",
			in:   `err: {"credentials":{"pin":123456,"user":"bob"},"auth_headers":{"X-Custom":"tok"},"secrets":{"k":99},"cookies":{"sid":"abc"},"has_password":false,"has_token":true,"input_tokens":1200,"secret_count":3,"auth_provider":"github","other":"safe"}`,
			want: `err: {"credentials":[REDACTED],"auth_headers":[REDACTED],"secrets":[REDACTED],"cookies":[REDACTED],"has_password":false,"has_token":true,"input_tokens":1200,"secret_count":3,"auth_provider":"github","other":"safe"}`,
		},
		{
			name: "embedded JSON sensitive containers backslash-escaped are redacted while metadata preserved",
			in:   `{\"credentials\":{\"pin\":123456},\"auth_headers\":{\"X-Custom\":\"tok\"},\"secrets\":{\"k\":99},\"cookies\":{\"sid\":\"abc\"},\"has_password\":false,\"has_token\":true,\"input_tokens\":1200,\"secret_count\":3,\"auth_provider\":\"github\",\"user\":\"bob\"}`,
			want: `{\"credentials\":[REDACTED],\"auth_headers\":[REDACTED],\"secrets\":[REDACTED],\"cookies\":[REDACTED],\"has_password\":false,\"has_token\":true,\"input_tokens\":1200,\"secret_count\":3,\"auth_provider\":\"github\",\"user\":\"bob\"}`,
		},
		{
			name: "embedded JSON auth container is redacted while auth_provider is preserved",
			in:   `{"auth":{"pin":123456},"auth_provider":"github"}`,
			want: `{"auth":[REDACTED],"auth_provider":"github"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactString(tc.in); got != tc.want {
				t.Errorf("RedactString(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactErrorContext_NestedMarshaledJSON(t *testing.T) {
	inner, err := json.Marshal(map[string]string{"password": "secret_val", "user": "bob"})
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	wrapper, err := json.Marshal(map[string]any{"body": string(inner), "status": 401})
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}
	outer, err := json.Marshal(map[string]any{"ok": true, "wrapper": string(wrapper)})
	if err != nil {
		t.Fatalf("marshal outer: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"raw_log": string(outer)})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	out, err := RedactErrorContext(payload)
	if err != nil {
		t.Fatalf("RedactErrorContext: %v", err)
	}
	outStr := string(out)
	if strings.Contains(outStr, "secret_val") {
		t.Fatalf("nested marshaled secret leaked: %s", outStr)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}
	rawLog, _ := got["raw_log"].(string)
	if !strings.Contains(rawLog, `\\\"password\\\":[REDACTED]`) || !strings.Contains(rawLog, `\\\"user\\\":\\\"bob\\\"`) {
		t.Fatalf("unexpected redacted raw_log: %s", rawLog)
	}

	t.Run("double-marshaled trailing backslash value preserves delimiter", func(t *testing.T) {
		innerBS, err := json.Marshal(map[string]string{"password": `p@ss\`, "user": "bob"})
		if err != nil {
			t.Fatalf("marshal innerBS: %v", err)
		}
		once, err := json.Marshal(string(innerBS))
		if err != nil {
			t.Fatalf("marshal once: %v", err)
		}
		raw, err := json.Marshal(map[string]string{"msg": string(once)})
		if err != nil {
			t.Fatalf("marshal raw: %v", err)
		}
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		var parsed map[string]string
		if err := json.Unmarshal(res, &parsed); err != nil {
			t.Fatalf("unmarshal res: %v", err)
		}
		if strings.Contains(parsed["msg"], "p@ss") {
			t.Fatalf("secret leaked in msg: %s", parsed["msg"])
		}
		if !strings.Contains(parsed["msg"], `\"password\":[REDACTED],\"user\":\"bob\"`) {
			t.Fatalf("delimiter corrupted in msg: %s", parsed["msg"])
		}
	})

	t.Run("marshaled stack log with escaped padded basic header does not leak", func(t *testing.T) {
		innerMsg, err := json.Marshal(map[string]string{"Msg": "header=\"Basic YWI=\"\n", "Other": "\"foo\""})
		if err != nil {
			t.Fatalf("marshal innerMsg: %v", err)
		}
		raw, err := json.Marshal(map[string]string{"stack": string(innerMsg)})
		if err != nil {
			t.Fatalf("marshal raw: %v", err)
		}
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		if strings.Contains(string(res), "YWI=") {
			t.Fatalf("basic header leaked in stack log: %s", string(res))
		}
	})

	t.Run("unbalanced escaped quote after authorization basic padded token does not leak", func(t *testing.T) {
		raw := json.RawMessage(`{"stack":"{\"Msg\":\"authorization=Basic dXNlcjpwYXM=\\\"\\n\",\"Other\":\"\\\"foo\\\"\"}"}`)
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		if strings.Contains(string(res), "dXNlcjpwYXM=") {
			t.Fatalf("authorization basic token leaked: %s", string(res))
		}
	})
}

func TestRedactErrorContext_DynamicMapKeysAndNumericLeaves(t *testing.T) {
	t.Run("redacts PII and secrets in dynamic JSON map keys deterministically", func(t *testing.T) {
		raw := json.RawMessage(`{
			"alice@mattermost.com": "first",
			"bob@mattermost.com": "second",
			"ghp_1234567890abcdef1234567890abcdef": "tok",
			"https://svc:p@ss@db.local/app?token=123": "url val",
			"safe_key": "safe_val"
		}`)
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		outStr := string(res)
		for _, leak := range []string{"alice@mattermost.com", "bob@mattermost.com", "ghp_1234567890abcdef", "p@ss", "token=123"} {
			if strings.Contains(outStr, leak) {
				t.Errorf("key secret leaked %q in %s", leak, outStr)
			}
		}
		var got map[string]any
		if err := json.Unmarshal(res, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got[redactedPlaceholder] != "first" {
			t.Errorf("got[%q] = %v, want %q (map=%v)", redactedPlaceholder, got[redactedPlaceholder], "first", got)
		}
		if got["https://[REDACTED]@db.local/app?[REDACTED]"] != "url val" {
			t.Errorf("unexpected redacted URL key value: %v (map=%v)", got["https://[REDACTED]@db.local/app?[REDACTED]"], got)
		}
		if got["safe_key"] != "safe_val" {
			t.Errorf("safe_key = %v, want safe_val", got["safe_key"])
		}
	})

	t.Run("redacts numeric leaves under direct sensitive keys while preserving metadata numbers", func(t *testing.T) {
		raw := json.RawMessage(`{
			"password": 123456,
			"api_key": 999,
			"auth": {"raw": "secret-val", "expires_in": 3600},
			"token": {"value": "secret-tok", "input_tokens": 1200, "token_count": 42, "has_password": false},
			"secret_version": 3
		}`)
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(res, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["password"] != redactedPlaceholder {
			t.Errorf("password = %v, want %s", got["password"], redactedPlaceholder)
		}
		if got["api_key"] != redactedPlaceholder {
			t.Errorf("api_key = %v, want %s", got["api_key"], redactedPlaceholder)
		}
		authObj := got["auth"].(map[string]any)
		if authObj["raw"] != redactedPlaceholder || authObj["expires_in"] != float64(3600) {
			t.Errorf("auth = %v", authObj)
		}
		tokObj := got["token"].(map[string]any)
		if tokObj["value"] != redactedPlaceholder || tokObj["input_tokens"] != float64(1200) || tokObj["token_count"] != float64(42) || tokObj["has_password"] != false {
			t.Errorf("token = %v", tokObj)
		}
		if got["secret_version"] != float64(3) {
			t.Errorf("secret_version = %v, want 3", got["secret_version"])
		}
	})

	t.Run("redacts numeric leaves in sensitive containers and arrays while preserving metadata numbers", func(t *testing.T) {
		raw := json.RawMessage(`{
			"credentials": {"pin": 123456, "huge_pin": 1e200000},
			"password": [123456, "abc"],
			"auth": {"raw": "secret-val", "expires_in": 3600, "pin": 9999},
			"token": {"value": "secret-tok", "input_tokens": 100, "output_tokens": 50, "total_tokens": 150, "token_count": 3, "max_age": 60, "secret_num": 888}
		}`)
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		outStr := string(res)
		for _, leak := range []string{"123456", "1e200000", "9999", "888", "secret-val", "secret-tok"} {
			if strings.Contains(outStr, leak) {
				t.Errorf("numeric/container secret leaked %q in %s", leak, outStr)
			}
		}
		var got map[string]any
		if err := json.Unmarshal(res, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		creds := got["credentials"].(map[string]any)
		if creds["pin"] != redactedPlaceholder || creds["huge_pin"] != redactedPlaceholder {
			t.Errorf("credentials = %v", creds)
		}
		pwArr := got["password"].([]any)
		if len(pwArr) != 2 || pwArr[0] != redactedPlaceholder || pwArr[1] != redactedPlaceholder {
			t.Errorf("password = %v", pwArr)
		}
		authObj := got["auth"].(map[string]any)
		if authObj["expires_in"] != float64(3600) || authObj["pin"] != redactedPlaceholder {
			t.Errorf("auth = %v", authObj)
		}
		tokObj := got["token"].(map[string]any)
		if tokObj["input_tokens"] != float64(100) ||
			tokObj["output_tokens"] != float64(50) ||
			tokObj["total_tokens"] != float64(150) ||
			tokObj["token_count"] != float64(3) ||
			tokObj["max_age"] != float64(60) ||
			tokObj["secret_num"] != redactedPlaceholder {
			t.Errorf("token = %v", tokObj)
		}
	})

	t.Run("redacts structured JSON credentials and auth_headers while preserving metadata", func(t *testing.T) {
		raw := json.RawMessage(`{
			"credentials": {"pin": 123456, "user": "bob"},
			"auth_headers": {"X-Custom": "tok-secret"},
			"secrets": {"k": 99},
			"cookies": {"sid": "cookie-secret"},
			"has_password": false,
			"has_token": true,
			"input_tokens": 1200,
			"secret_count": 3,
			"auth_provider": "github"
		}`)
		res, err := RedactErrorContext(raw)
		if err != nil {
			t.Fatalf("RedactErrorContext: %v", err)
		}
		outStr := string(res)
		for _, leak := range []string{"123456", "bob", "tok-secret", "99", "cookie-secret"} {
			if strings.Contains(outStr, leak) {
				t.Errorf("structured container secret leaked %q in %s", leak, outStr)
			}
		}
		var got map[string]any
		if err := json.Unmarshal(res, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if creds := got["credentials"].(map[string]any); creds["pin"] != redactedPlaceholder || creds["user"] != redactedPlaceholder {
			t.Errorf("credentials = %v", creds)
		}
		if ah := got["auth_headers"].(map[string]any); ah["X-Custom"] != redactedPlaceholder {
			t.Errorf("auth_headers = %v", ah)
		}
		if sec := got["secrets"].(map[string]any); sec["k"] != redactedPlaceholder {
			t.Errorf("secrets = %v", sec)
		}
		if ck := got["cookies"].(map[string]any); ck["sid"] != redactedPlaceholder {
			t.Errorf("cookies = %v", ck)
		}
		if got["has_password"] != false || got["has_token"] != true || got["input_tokens"] != float64(1200) || got["secret_count"] != float64(3) || got["auth_provider"] != "github" {
			t.Errorf("metadata altered: %v", got)
		}
	})
}

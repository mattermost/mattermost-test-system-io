package telemetry

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact_redactsSecretKeys(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLoggerTo(buf, "json", "info")

	logger.Info("test",
		slog.String("password", "hunter2"),
		slog.String("token", "abc.def.ghi"),
		slog.String("api_key", "tsio_abcd.xyz"),
		slog.String("authorization", "Bearer leaked"),
		slog.String("X-API-Key", "tsio_abcd.xyz"),
		slog.String("set-cookie", "tsio_session=opaque"),
	)

	raw := buf.String()
	for _, leak := range []string{"hunter2", "abc.def.ghi", "tsio_abcd.xyz", "Bearer leaked", "opaque"} {
		if strings.Contains(raw, leak) {
			t.Errorf("secret %q leaked into log: %s", leak, raw)
		}
	}
	if !strings.Contains(raw, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker in log: %s", raw)
	}
}

func TestRedact_passesThroughNonSecrets(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLoggerTo(buf, "json", "info")

	logger.Info("ok",
		slog.String("user_id", "01234567-aaaa"),
		slog.String("method", "GET"),
		slog.Int("status", 200),
	)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode log line: %v (raw=%q)", err, buf.String())
	}
	if got, want := record["user_id"], "01234567-aaaa"; got != want {
		t.Errorf("user_id = %v, want %q", got, want)
	}
	if got, want := record["method"], "GET"; got != want {
		t.Errorf("method = %v, want %q", got, want)
	}
	if got, want := record["status"], float64(200); got != want {
		t.Errorf("status = %v, want %v", got, want)
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"nonsense", slog.LevelInfo}, // default
		{"", slog.LevelInfo},
	}
	for _, c := range cases {
		if got := parseLevel(c.in); got != c.want {
			t.Errorf("parseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

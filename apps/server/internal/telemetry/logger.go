// Package telemetry builds the structured logger and the request-logging
// middleware. No metrics/tracing yet — OpenTelemetry integration is a follow-up.
package telemetry

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger constructs a slog.Logger matching the requested format and level,
// with a Replacer that redacts known-secret attribute values.
func NewLogger(format, level string) *slog.Logger {
	return NewLoggerTo(os.Stdout, format, level)
}

// NewLoggerTo is the injectable variant, used by tests.
func NewLoggerTo(w io.Writer, format, level string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:       parseLevel(level),
		ReplaceAttr: redact,
	}

	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Known attribute keys (case-insensitive) whose values must never hit logs.
var secretKeys = []string{
	"password", "token", "api_key", "apikey", "authorization",
	"secret", "cookie", "set-cookie", "session", "x-api-key", "x-admin-key",
}

// redact replaces values for secret-like keys with "[REDACTED]".
func redact(_ []string, a slog.Attr) slog.Attr {
	lk := strings.ToLower(a.Key)
	for _, s := range secretKeys {
		if lk == s {
			return slog.Attr{Key: a.Key, Value: slog.StringValue("[REDACTED]")}
		}
	}
	return a
}

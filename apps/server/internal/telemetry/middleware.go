package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ctxKey string

const loggerKey ctxKey = "logger"

// Middleware attaches a request-scoped *slog.Logger to ctx and logs the
// method/path/status/duration on response.
func Middleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = uuid.NewString()
			}
			logger := base.With(
				slog.String("request_id", reqID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("remote_ip", clientIP(r)),
			)
			ctx := context.WithValue(r.Context(), loggerKey, logger)

			sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r.WithContext(ctx))
			logger.Info("request",
				slog.Int("status", sw.status),
				slog.Duration("duration", time.Since(start)),
				slog.Int64("bytes", sw.bytes),
			)
		})
	}
}

// LoggerFromContext returns the request logger or a no-op.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// (and coder/websocket via Accept) can reach Hijacker / Flusher on the
// original writer. Without this, WebSocket upgrades fail with 1006.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return f
	}
	return r.RemoteAddr
}

package api

import (
	"net/http"
	"strings"
)

// healthPaths bypass the HTTPS check so liveness/readiness probes from the LB
// target group (which arrive without X-Forwarded-Proto) keep working.
var healthPaths = map[string]struct{}{
	"/health":  {},
	"/healthz": {},
	"/ready":   {},
}

// RequireHTTPS rejects any request that did not reach a TLS-terminating proxy
// upstream. With Set-Cookie always marked Secure, an HTTP request would
// silently fail auth; surface that misconfiguration as a loud 426 instead.
func RequireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := healthPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			next.ServeHTTP(w, r)
			return
		}
		WriteErrorCode(w, http.StatusUpgradeRequired, "HTTPS_REQUIRED",
			"HTTPS is required; ensure a TLS-terminating proxy (Caddy locally, ALB in staging/prod) is in front of tsio")
	})
}

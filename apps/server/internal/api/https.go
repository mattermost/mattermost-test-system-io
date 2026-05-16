package api

import (
	"net"
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

// isTrustedProxyIP reports whether an in-VPC / loopback peer address can be
// trusted to set X-Forwarded-Proto. Without this check, any client that can
// reach tsio directly (bypassing the proxy) could spoof "X-Forwarded-Proto:
// https" and slip past the HTTPS gate.
//
// Trust set:
//   - Loopback (127.0.0.0/8, ::1) — local dev, in-pod sidecars, e2e tests.
//   - RFC1918 / unique-local (10/8, 172.16/12, 192.168/16, fc00::/7) — VPC
//     interior where the ALB / Caddy / k8s ingress sits.
//   - Link-local (169.254/16, fe80::/10) — instance-metadata adjacent.
//
// Public addresses are NOT trusted. A production deployment that puts tsio
// directly on a public IP without an in-VPC proxy will (correctly) reject
// X-Forwarded-Proto and 426 every request.
func isTrustedProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// peerIP extracts the connection-level IP from r.RemoteAddr. RemoteAddr is
// "host:port" or "[::1]:port"; we want just the host part. Returns nil if the
// address can't be parsed (no port, malformed) — callers treat nil as untrusted.
func peerIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// Some test transports set RemoteAddr without a port; try the raw
		// value before giving up.
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// RequireHTTPS rejects any request that did not reach a TLS-terminating proxy
// upstream. With Set-Cookie always marked Secure, an HTTP request would
// silently fail auth; surface that misconfiguration as a loud 426 instead.
//
// `X-Forwarded-Proto: https` is honored only when the request's peer (the
// connection-level IP, not whatever the X-Forwarded-For chain claims) is in
// the trusted set above. A remote attacker who can reach tsio directly on a
// public IP cannot bypass the gate by setting the header.
func RequireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := healthPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.TLS != nil {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") && isTrustedProxyIP(peerIP(r.RemoteAddr)) {
			next.ServeHTTP(w, r)
			return
		}
		WriteErrorCode(w, http.StatusUpgradeRequired, "HTTPS_REQUIRED",
			"HTTPS is required; ensure a TLS-terminating proxy (Caddy locally, ALB in staging/prod) is in front of tsio")
	})
}

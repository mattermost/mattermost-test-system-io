package webui

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// Handler returns an http.Handler that serves the embedded web bundle.
//
// Requests that resolve to a real file in the bundle are served with
// appropriate Cache-Control headers: hashed assets under /assets/ are
// treated as immutable for one year (Vite fingerprints their filenames),
// while everything else is served with Cache-Control: no-cache so a new
// deploy is picked up on the next reload.
//
// Requests that do not resolve to a file fall back to dist/index.html so
// client-side routing can handle deep links such as /reports/r/abc.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			serveIndex(w, r, sub)
			return
		}

		f, err := sub.Open(reqPath)
		if err != nil {
			serveIndex(w, r, sub)
			return
		}
		info, statErr := f.Stat()
		_ = f.Close()
		if statErr != nil || info.IsDir() {
			serveIndex(w, r, sub)
			return
		}

		if strings.HasPrefix(reqPath, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

// serveIndex writes dist/index.html as the SPA fallback response.
// A missing index.html means the web bundle was not built before the server
// binary was compiled; returning 500 surfaces that misconfiguration
// instead of a confusing blank body.
func serveIndex(w http.ResponseWriter, _ *http.Request, sub fs.FS) {
	f, err := sub.Open("index.html")
	if err != nil {
		http.Error(w, "index.html not found in embedded bundle", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

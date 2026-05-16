// Package clientcfg serves GET /api/v1/config (client feature flags) and
// GET /api/v1/info (build metadata). Both endpoints are public.
package clientcfg

import (
	"encoding/json"
	"net/http"
)

// Handlers bundles the dependencies for /config and /info.
type Handlers struct {
	// Config values exposed to the browser.
	UploadTimeoutMs    int
	HTMLViewEnabled    bool
	SearchMinLength    int
	GitHubOAuthEnabled bool

	// Build metadata.
	ServerVersion string
	Environment   string
	RepoURL       string
	CommitSHA     string
	BuildTime     string
}

type configResponse struct {
	UploadTimeoutMs    int  `json:"upload_timeout_ms"`
	HTMLViewEnabled    bool `json:"html_view_enabled"`
	SearchMinLength    int  `json:"search_min_length"`
	GitHubOAuthEnabled bool `json:"github_oauth_enabled"`
}

type infoResponse struct {
	ServerVersion string `json:"server_version"`
	Environment   string `json:"environment"`
	RepoURL       string `json:"repo_url"`
	CommitSHA     string `json:"commit_sha"`
	BuildTime     string `json:"build_time"`
}

// Config serves GET /api/v1/config.
func (h *Handlers) Config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, configResponse{
		UploadTimeoutMs:    h.UploadTimeoutMs,
		HTMLViewEnabled:    h.HTMLViewEnabled,
		SearchMinLength:    h.SearchMinLength,
		GitHubOAuthEnabled: h.GitHubOAuthEnabled,
	})
}

// Info serves GET /api/v1/info.
func (h *Handlers) Info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, infoResponse{
		ServerVersion: h.ServerVersion,
		Environment:   h.Environment,
		RepoURL:       h.RepoURL,
		CommitSHA:     h.CommitSHA,
		BuildTime:     h.BuildTime,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

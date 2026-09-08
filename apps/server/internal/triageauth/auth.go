// Package triageauth separates repair and defect authority from public report
// ingestion. An uploader (including a fork PR job) must not acquire repair leases.
package triageauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
)

// Verifier verifies a GitHub JWT before any workflow claims are consulted.
type Verifier interface {
	Verify(context.Context, string) (oidc.Claims, error)
}

// Config grants triage authority independently of the upload policy.
type Config struct {
	APIKey       string
	WorkflowRefs []string
	Audience     string
	Verifier     Verifier
}

// Middleware overwrites the internal principal headers, including on failed
// authentication. Feature handlers enforce Repository against stored objects.
func Middleware(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del("X-TSIO-Triage-Actor")
			r.Header.Del("X-TSIO-Triage-Repository")
			actor, repository := "", ""
			if key := r.Header.Get("X-Triage-Key"); c.APIKey != "" && key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(c.APIKey)) == 1 {
				actor = "triage-service-key"
			} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && c.Verifier != nil && c.Audience != "" && len(c.WorkflowRefs) > 0 {
				claims, err := c.Verifier.Verify(r.Context(), strings.TrimPrefix(auth, "Bearer "))
				if err == nil && claims.Audience == c.Audience {
					actor, repository = trustedWorkflow(claims, c.WorkflowRefs)
				}
			}
			if actor == "" {
				api.WriteErrorCode(w, http.StatusUnauthorized, "TRIAGE_UNAUTHORIZED", "triage requires a dedicated service key or an explicitly trusted workflow identity")
				return
			}
			r.Header.Set("X-TSIO-Triage-Actor", actor)
			r.Header.Set("X-TSIO-Triage-Repository", repository)
			r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
			next.ServeHTTP(w, r)
		})
	}
}

func trustedWorkflow(c oidc.Claims, allowed []string) (string, string) {
	var raw struct {
		WorkflowRef string `json:"workflow_ref"`
		EventName   string `json:"event_name"`
		RunID       string `json:"run_id"`
		RunAttempt  string `json:"run_attempt"`
	}
	if json.Unmarshal(c.Raw, &raw) != nil || raw.RunID == "" || raw.RunAttempt == "" {
		return "", ""
	}
	switch raw.EventName {
	case "workflow_dispatch", "workflow_run", "schedule":
	default:
		return "", ""
	}
	// The full workflow path AND its branch are allowlisted. A PR cannot gain
	// authority by copying the workflow's display name or selecting an environment.
	if !strings.HasPrefix(c.Ref, "refs/heads/") || !strings.HasPrefix(raw.WorkflowRef, c.Repository+"/.github/workflows/") || !strings.HasSuffix(raw.WorkflowRef, "@"+c.Ref) {
		return "", ""
	}
	for _, ref := range allowed {
		if raw.WorkflowRef == ref {
			return "github:" + raw.WorkflowRef + ":" + raw.RunID + ":" + raw.RunAttempt, c.Repository
		}
	}
	return "", ""
}

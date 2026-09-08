package triageauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
)

type fakeVerifier struct {
	claims oidc.Claims
	err    error
}

func (f fakeVerifier) Verify(context.Context, string) (oidc.Claims, error) { return f.claims, f.err }

func TestTrustBoundary(t *testing.T) {
	const workflow = "mattermost/mattermost/.github/workflows/e2e-triage-guardian.yml@refs/heads/master"
	for _, tc := range []struct {
		name, key, bearer, event, ref, workflow, audience string
		err                                               error
		want                                              bool
	}{
		{name: "anonymous spoofed actor"},
		{name: "ordinary upload key"},
		{name: "dedicated key", key: "service-secret", want: true},
		{name: "wrong dedicated key", key: "incorrect"},
		{name: "trusted master", bearer: "jwt", event: "schedule", ref: "refs/heads/master", workflow: workflow, audience: "tsio", want: true},
		{name: "invalid signature", bearer: "jwt", event: "schedule", ref: "refs/heads/master", workflow: workflow, audience: "tsio", err: errors.New("signature")},
		{name: "PR trigger on master", bearer: "jwt", event: "pull_request_target", ref: "refs/heads/master", workflow: workflow, audience: "tsio"},
		{name: "fork ref", bearer: "jwt", event: "workflow_dispatch", ref: "refs/pull/1/merge", workflow: workflow, audience: "tsio"},
		{name: "same named workflow on PR branch", bearer: "jwt", event: "workflow_dispatch", ref: "refs/heads/pr", workflow: "mattermost/mattermost/.github/workflows/e2e-triage-guardian.yml@refs/heads/pr", audience: "tsio"},
		{name: "wrong audience", bearer: "jwt", event: "schedule", ref: "refs/heads/master", workflow: workflow, audience: "another-service"},
		{name: "different workflow", bearer: "jwt", event: "schedule", ref: "refs/heads/master", workflow: "mattermost/mattermost/.github/workflows/untrusted.yml@refs/heads/master", audience: "tsio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"workflow_ref": tc.workflow, "event_name": tc.event, "run_id": "123", "run_attempt": "1"})
			if err != nil {
				t.Fatal(err)
			}
			claims := oidc.Claims{Audience: tc.audience, Repository: "mattermost/mattermost", Ref: tc.ref, Raw: raw}
			called := false
			h := Middleware(Config{APIKey: "service-secret", WorkflowRefs: []string{workflow}, Audience: "tsio", Verifier: fakeVerifier{claims, tc.err}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Header.Get("X-TSIO-Triage-Actor") == "forged" {
					t.Error("accepted forged author")
				}
				wantRepo := "mattermost/mattermost"
				if tc.key == "service-secret" {
					wantRepo = ""
				}
				if r.Header.Get("X-TSIO-Triage-Repository") != wantRepo {
					t.Error("wrong principal scope")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.Header.Set("X-TSIO-Triage-Actor", "forged")
			r.Header.Set("X-TSIO-Triage-Repository", "forged/repo")
			r.Header.Set("X-API-Key", "ordinary-upload-secret")
			r.Header.Set("X-Triage-Key", tc.key)
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if called != tc.want {
				t.Fatalf("called=%v, want=%v; response=%d", called, tc.want, w.Code)
			}
			if !tc.want && w.Code != http.StatusUnauthorized {
				t.Fatalf("got status %d", w.Code)
			}
		})
	}
}

func TestUnconfiguredTriageIsClosed(t *testing.T) {
	called := false
	h := Middleware(Config{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if called || w.Code != http.StatusUnauthorized {
		t.Fatal("unconfigured triage allowed mutation")
	}
}

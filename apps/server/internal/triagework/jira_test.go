package triagework

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJiraConfiguration(t *testing.T) {
	if c, e := NewJiraClient(Config{}); e != nil || c != nil {
		t.Fatal("disabled Jira must be inert")
	}
	for _, c := range []Config{{Enabled: true}, {Enabled: true, BaseURL: "http://jira.example"}, {Enabled: true, BaseURL: "https://jira.example/path"}, {Enabled: true, BaseURL: "https://jira.example", Email: "me", APIToken: "secret", ProjectKey: "X OR 1=1", IssueType: "Bug"}} {
		if _, e := NewJiraClient(c); e == nil {
			t.Fatalf("accepted invalid enabled config: %+v", c)
		}
	}
}

func TestJiraRESTContract(t *testing.T) {
	searches, creates, gets := 0, 0, 0
	test := "tsio-test-" + strings.Repeat("a", 64)
	submission := "tsio-submission-11111111-1111-4111-8111-111111111111"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "bot@example.com" || password != "secret" {
			t.Error("missing configured basic auth")
		}
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			searches++
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			jql, _ := body["jql"].(string)
			if !strings.Contains(jql, `project = "MM"`) || !strings.Contains(jql, `labels = "tsio-`) {
				t.Errorf("unsafe/wrong JQL: %s", jql)
			}
			if searches == 1 && !strings.Contains(jql, "resolution IS EMPTY") {
				t.Error("dedup must be live unresolved query")
			}
			if searches == 2 && strings.Contains(jql, "resolution IS EMPTY") {
				t.Error("submission reconciliation must also find resolved issues")
			}
			write(w, 200, map[string]any{"issues": []any{map[string]any{"key": "MM-1", "fields": map[string]any{"resolution": nil}}}})
		case "/rest/api/3/issue":
			creates++
			var body struct {
				Fields struct {
					Labels      []string `json:"labels"`
					Description struct {
						Type    string `json:"type"`
						Version int    `json:"version"`
					} `json:"description"`
				} `json:"fields"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if len(body.Fields.Labels) != 2 || body.Fields.Labels[0] != test || body.Fields.Labels[1] != submission || body.Fields.Description.Type != "doc" || body.Fields.Description.Version != 1 {
				t.Errorf("missing dedup markers/ADF: %+v", body)
			}
			write(w, 201, map[string]string{"key": "MM-2"})
		case "/rest/api/3/issue/MM-1":
			gets++
			write(w, 200, map[string]any{"fields": map[string]any{"resolution": nil}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	j, e := NewJiraClient(Config{Enabled: true, BaseURL: server.URL, Email: "bot@example.com", APIToken: "secret", ProjectKey: "MM", IssueType: "Bug", HTTPClient: server.Client()})
	if e != nil {
		t.Fatal(e)
	}
	if v, e := j.FindUnresolved(context.Background(), test); e != nil || v.Key != "MM-1" {
		t.Fatalf("search: %v %v", v, e)
	}
	if v, e := j.FindSubmission(context.Background(), submission); e != nil || v.Key != "MM-1" {
		t.Fatalf("reconcile: %v %v", v, e)
	}
	if open, e := j.IsUnresolved(context.Background(), "MM-1"); e != nil || !open {
		t.Fatalf("resolution: %v %v", open, e)
	}
	if v, e := j.Create(context.Background(), test, submission, "summary", "evidence"); e != nil || v.Key != "MM-2" {
		t.Fatalf("create: %v %v", v, e)
	}
	if searches != 2 || creates != 1 || gets != 1 {
		t.Fatal("missing REST operations")
	}
}

func TestJiraRejectsRedirectAndBoundsRequests(t *testing.T) {
	leaked := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	j, e := NewJiraClient(Config{Enabled: true, BaseURL: source.URL, Email: "bot", APIToken: "secret", ProjectKey: "MM", IssueType: "Bug", HTTPClient: source.Client()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = j.FindUnresolved(context.Background(), "tsio-test-"+strings.Repeat("a", 64)); e == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("followed redirect")
	}
	if _, e = j.FindUnresolved(context.Background(), `x" OR 1=1`); e == nil {
		t.Fatal("unsafe label accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, e = j.FindUnresolved(ctx, "tsio-test-"+strings.Repeat("a", 64)); e == nil {
		t.Fatal("expired context accepted")
	}
}

func TestInputValidation(t *testing.T) {
	h := &Handlers{}
	for _, tc := range []struct {
		handler   http.HandlerFunc
		url, body string
		actor     bool
		want      int
	}{
		{h.Claim, "/", `{"repository":"org/repo","worker":"w"}`, false, 401},
		{h.Claim, "/", `{"repository":"org/repo","worker":"w","url":"https://attacker"}`, true, 400},
		{h.Claim, "/", `{"repository":"repo","worker":"w"}`, true, 400},
		{h.Claim, "/", `{"repository":"org/repo","worker":"w"} {}`, true, 400},
		{h.Claim, "/", `{"repository":"org/repo","worker":"` + strings.Repeat("w", 33000) + `"}`, true, 400},
		{h.ListRepairs, "/?repository=org/repo&limit=201", "", false, 400},
		{h.ListRepairs, "/?repository=org/repo&repository=org/other", "", false, 400},
		{h.ListRepairs, "/?repository=org/repo&foo=bar", "", false, 400},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
		if tc.actor {
			r.Header.Set("X-TSIO-Triage-Actor", "test")
		}
		w := httptest.NewRecorder()
		tc.handler(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d: %s", tc.url, w.Code, tc.want, w.Body.String())
		}
	}
}

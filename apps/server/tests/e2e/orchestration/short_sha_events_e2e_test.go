//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestShortSHAWorkerEventsUseRunIdentity starts a run with the full commit
// SHA, then checks out and completes with the 7-char SHA. Events must carry
// the run's stored (full) identity so a page subscribed with it receives them.
func TestShortSHAWorkerEventsUseRunIdentity(t *testing.T) {
	env, tok := startEnv(t)

	spec := "tests/short-sha.spec.ts"
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{spec}, nil)), http.StatusCreated)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(env.ServerURL, "http") + "/api/v1/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.CloseNow()

	full := map[string]any{
		"repository":     defaultRepository,
		"commit_sha":     defaultCommit,
		"gh_run_id":      defaultGHRunID,
		"name":           defaultName,
		"gh_run_attempt": defaultRunAttempt,
	}
	sub, _ := json.Marshal(map[string]any{"type": "subscribe.orchestration", "identity": full})
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// No subscribe ack; give the server a moment to register it.
	time.Sleep(300 * time.Millisecond)

	short := map[string]any{"commit_sha": defaultCommit[:7]}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(checkoutBody("short-sha-worker", "job-short-1", 1), short)), http.StatusOK)
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		merge(completeBody("short-sha-worker", "job-short-1", passResults([]string{spec})), short)), http.StatusOK)

	want := map[string]bool{
		"orchestration.unit.leased":    false,
		"orchestration.unit.completed": false,
		"orchestration.run.completed":  false,
	}
	for remaining := len(want); remaining > 0; {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for events %v: %v", want, err)
		}
		var ev struct {
			Type     string `json:"type"`
			Identity *struct {
				CommitSHA string `json:"commit_sha"`
			} `json:"identity"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		seen, tracked := want[ev.Type]
		if !tracked || seen {
			continue
		}
		if ev.Identity == nil || ev.Identity.CommitSHA != defaultCommit {
			t.Fatalf("%s identity = %+v, want full commit %s", ev.Type, ev.Identity, defaultCommit)
		}
		want[ev.Type] = true
		remaining--
	}
}

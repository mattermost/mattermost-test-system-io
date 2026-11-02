package orchestration

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

func TestNotifyHomeLiveDebouncesProgress(t *testing.T) {
	hub := events.NewHub()
	ch, cancel := hub.Subscribe(nil, "")
	defer cancel()

	p := &Publisher{Hub: hub}
	p.notifyHomeLive(false)
	p.notifyHomeLive(false)

	select {
	case ev := <-ch:
		t.Fatalf("unexpected immediate event: %q", ev.Type)
	case <-time.After(150 * time.Millisecond):
	}

	select {
	case ev := <-ch:
		if ev.Type != EventTypeHomeLiveChanged {
			t.Fatalf("type = %q, want %q", ev.Type, EventTypeHomeLiveChanged)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for debounced home.live.changed")
	}
}

func TestNotifyHomeLiveTerminalImmediate(t *testing.T) {
	hub := events.NewHub()
	ch, cancel := hub.Subscribe(nil, "")
	defer cancel()

	p := &Publisher{Hub: hub}
	p.notifyHomeLive(true)

	select {
	case ev := <-ch:
		if ev.Type != EventTypeHomeLiveChanged {
			t.Fatalf("type = %q, want %q", ev.Type, EventTypeHomeLiveChanged)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for terminal home.live.changed")
	}
}

// Package events is an in-memory pub-sub hub that fans out ingest progress
// updates to connected WebSocket subscribers.
package events

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event is the envelope broadcast to subscribers: JSON object with three
// fields — `type` (event name), `payload` (opaque blob), `timestamp`.
type Event struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// Scope filters which subscribers see an event.
type Scope struct {
	OIDCSubject string      // if non-empty, only subscribers with this subject receive
	UserIDs     []uuid.UUID // if non-empty, only subscribers with one of these user ids receive
}

type subscriber struct {
	id      uuid.UUID
	ch      chan Event
	userID  *uuid.UUID
	subject string
}

// Hub is an in-memory pub-sub broadcaster.
type Hub struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]*subscriber
}

// NewHub constructs an empty Hub.
func NewHub() *Hub { return &Hub{subs: map[uuid.UUID]*subscriber{}} }

// Subscribe registers a new subscriber. userID and subject are optional filters.
// Messages are delivered on the returned channel (buffered); slow consumers drop events.
func (h *Hub) Subscribe(userID *uuid.UUID, subject string) (<-chan Event, func()) {
	s := &subscriber{
		id:      uuid.New(),
		ch:      make(chan Event, 32),
		userID:  userID,
		subject: subject,
	}
	h.mu.Lock()
	h.subs[s.id] = s
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		delete(h.subs, s.id)
		h.mu.Unlock()
		close(s.ch)
	}
	return s.ch, cancel
}

// Publish delivers an event to every matching subscriber.
func (h *Hub) Publish(ev Event, scope Scope) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if !matches(s, scope) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// slow subscriber; drop.
		}
	}
}

func matches(s *subscriber, sc Scope) bool {
	if sc.OIDCSubject != "" && s.subject != sc.OIDCSubject {
		return false
	}
	if len(sc.UserIDs) > 0 {
		if s.userID == nil {
			return false
		}
		for _, u := range sc.UserIDs {
			if u == *s.userID {
				return true
			}
		}
		return false
	}
	return true
}

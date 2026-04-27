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
// fields — `type` (event name), `payload` (opaque blob), `timestamp`. When an
// event is scoped to a single orchestration run, the optional `identity`
// envelope field carries the composite identity so identity-scoped
// subscribers can be routed without inspecting the payload.
type Event struct {
	Type      string             `json:"type"`
	Timestamp time.Time          `json:"timestamp"`
	Payload   json.RawMessage    `json:"payload"`
	Identity  *CompositeIdentity `json:"identity,omitempty"`
}

// CompositeIdentity is the join key the orchestration feature uses to scope
// subscriptions to a single CI run. When non-nil on Scope, only events whose
// envelope's CompositeIdentity matches all five fields are forwarded.
type CompositeIdentity struct {
	Repository   string `json:"repository"`
	CommitSHA    string `json:"commit_sha"`
	GHRunID      string `json:"gh_run_id"`
	Name         string `json:"name"`
	GHRunAttempt string `json:"gh_run_attempt"`
}

// Equal reports whether two CompositeIdentity values match on all five fields.
func (c CompositeIdentity) Equal(o CompositeIdentity) bool {
	return c.Repository == o.Repository &&
		c.CommitSHA == o.CommitSHA &&
		c.GHRunID == o.GHRunID &&
		c.Name == o.Name &&
		c.GHRunAttempt == o.GHRunAttempt
}

// Scope filters which subscribers see an event.
type Scope struct {
	OIDCSubject string      // if non-empty, only subscribers with this subject receive
	UserIDs     []uuid.UUID // if non-empty, only subscribers with one of these user ids receive
	// Identity, when non-nil, scopes the event to subscribers whose own
	// Identity matches all five fields. Identity-scoped events are NOT
	// delivered to non-identity subscribers, and identity-scoped subscribers
	// only receive identity-scoped events. Existing OIDCSubject / UserIDs
	// filters are unaffected when Identity is nil (zero-value Scope still
	// matches the public feed).
	Identity *CompositeIdentity
}

type subscriber struct {
	id       uuid.UUID
	ch       chan Event
	userID   *uuid.UUID
	subject  string
	identity *CompositeIdentity
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
	return h.subscribe(&subscriber{
		ch:      make(chan Event, 32),
		userID:  userID,
		subject: subject,
	})
}

// SubscribeIdentity registers a subscriber that ONLY receives events scoped
// to the given composite identity (i.e. events whose Scope.Identity matches
// all five fields). Use this for the orchestration WebSocket subscription
// frame so cross-run traffic stays out of the subscriber's channel.
func (h *Hub) SubscribeIdentity(identity CompositeIdentity) (<-chan Event, func()) {
	id := identity
	return h.subscribe(&subscriber{
		ch:       make(chan Event, 32),
		identity: &id,
	})
}

func (h *Hub) subscribe(s *subscriber) (<-chan Event, func()) {
	s.id = uuid.New()
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

// Publish delivers an event to every matching subscriber. If scope.Identity
// is non-nil, the Hub also stamps it onto the outgoing envelope (so
// downstream consumers can read the run identity off the wire) unless the
// caller already populated ev.Identity.
func (h *Hub) Publish(ev Event, scope Scope) {
	if ev.Identity == nil && scope.Identity != nil {
		id := *scope.Identity
		ev.Identity = &id
	}
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
	// Identity-scoped subscribers only receive identity-scoped events whose
	// identity matches all five fields. They are isolated from the public
	// feed and from other runs' events.
	if s.identity != nil {
		if sc.Identity == nil {
			return false
		}
		return s.identity.Equal(*sc.Identity)
	}
	// Non-identity subscribers do NOT receive identity-scoped events; this
	// keeps orchestration cross-traffic off the global / OIDC feed.
	if sc.Identity != nil {
		return false
	}
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

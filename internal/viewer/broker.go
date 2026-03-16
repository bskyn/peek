package viewer

import (
	"sync"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
)

const subscriberBufferSize = 64

// LiveEnvelope is the SSE payload contract for browser subscribers.
type LiveEnvelope struct {
	Type            string                    `json:"type"`
	RuntimeID       string                    `json:"runtime_id,omitempty"`
	Session         *store.SessionSummary     `json:"session,omitempty"`
	Event           *event.Event              `json:"event,omitempty"`
	ActiveSessionID string                    `json:"active_session_id,omitempty"`
	Runtime         *companion.StatusSnapshot `json:"runtime,omitempty"`
}

type subscriber struct {
	sessionID string
	runtimeID string
	ch        chan LiveEnvelope
}

// Broker fans out live session and event updates.
type Broker struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]subscriber
}

// NewBroker constructs an in-process live broker.
func NewBroker() *Broker {
	return &Broker{
		subs: make(map[int]subscriber),
	}
}

// SubscribeAll subscribes to all live envelopes.
func (b *Broker) SubscribeAll() (<-chan LiveEnvelope, func()) {
	return b.subscribe("", "")
}

// SubscribeSession subscribes to one session.
func (b *Broker) SubscribeSession(sessionID string) (<-chan LiveEnvelope, func()) {
	return b.subscribe(sessionID, "")
}

// SubscribeRuntime subscribes to one managed runtime.
func (b *Broker) SubscribeRuntime(runtimeID string) (<-chan LiveEnvelope, func()) {
	return b.subscribe("", runtimeID)
}

func (b *Broker) subscribe(sessionID, runtimeID string) (<-chan LiveEnvelope, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	ch := make(chan LiveEnvelope, subscriberBufferSize)
	b.subs[id] = subscriber{
		sessionID: sessionID,
		runtimeID: runtimeID,
		ch:        ch,
	}

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		sub, ok := b.subs[id]
		if !ok {
			return
		}
		delete(b.subs, id)
		close(sub.ch)
	}
}

// PublishSessionUpsert broadcasts a session update.
func (b *Broker) PublishSessionUpsert(runtimeID string, summary store.SessionSummary) {
	b.publish(LiveEnvelope{
		Type:      "session_upsert",
		RuntimeID: runtimeID,
		Session:   &summary,
	})
}

// PublishEventAppend broadcasts an appended event.
func (b *Broker) PublishEventAppend(runtimeID string, ev event.Event) {
	b.publish(LiveEnvelope{
		Type:      "event_append",
		RuntimeID: runtimeID,
		Event:     &ev,
	})
}

// PublishActiveSession broadcasts the currently tailed session.
func (b *Broker) PublishActiveSession(runtimeID, sessionID string) {
	b.publish(LiveEnvelope{
		Type:            "active_session",
		RuntimeID:       runtimeID,
		ActiveSessionID: sessionID,
	})
}

// PublishRuntimeStatus broadcasts active workspace runtime state.
func (b *Broker) PublishRuntimeStatus(runtimeID string, status companion.StatusSnapshot) {
	b.publish(LiveEnvelope{
		Type:      "runtime_status",
		RuntimeID: runtimeID,
		Runtime:   &status,
	})
}

func (b *Broker) publish(envelope LiveEnvelope) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	sessionID := envelopeSessionID(envelope)
	runtimeID := envelope.RuntimeID
	for _, sub := range b.subs {
		if sub.sessionID != "" && sub.sessionID != sessionID {
			continue
		}
		if sub.runtimeID != "" && sub.runtimeID != runtimeID {
			continue
		}
		select {
		case sub.ch <- envelope:
		default:
		}
	}
}

func envelopeSessionID(envelope LiveEnvelope) string {
	if envelope.Event != nil {
		return envelope.Event.SessionID
	}
	if envelope.Session != nil {
		return envelope.Session.ID
	}
	return ""
}

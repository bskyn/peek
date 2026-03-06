package event

import (
	"encoding/json"
	"time"
)

// EventType represents the kind of event in a session.
type EventType string

const (
	EventUserMessage       EventType = "user_message"
	EventAssistantThinking EventType = "assistant_thinking"
	EventAssistantMessage  EventType = "assistant_message"
	EventToolCall          EventType = "tool_call"
	EventToolResult        EventType = "tool_result"
	EventProgress          EventType = "progress"
	EventSystem            EventType = "system"
	EventError             EventType = "error"
)

// Session represents an agent conversation session.
type Session struct {
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	ProjectPath     string    `json:"project_path"`
	SourceSessionID string    `json:"source_session_id"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Event represents a single event within a session.
type Event struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	Timestamp     time.Time       `json:"timestamp"`
	Seq           int64           `json:"seq"`
	Type          EventType       `json:"type"`
	Role          string          `json:"role,omitempty"`
	ParentEventID string          `json:"parent_event_id,omitempty"`
	PayloadJSON   json.RawMessage `json:"payload_json"`
}

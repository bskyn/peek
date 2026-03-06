package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventTypeValues(t *testing.T) {
	types := []EventType{
		EventUserMessage,
		EventAssistantThinking,
		EventAssistantMessage,
		EventToolCall,
		EventToolResult,
		EventProgress,
		EventSystem,
		EventError,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if et == "" {
			t.Error("EventType should not be empty")
		}
		if seen[et] {
			t.Errorf("duplicate EventType: %s", et)
		}
		seen[et] = true
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	payload := json.RawMessage(`{"tool":"Bash","input":"ls /tmp"}`)
	ev := Event{
		ID:          "evt-1",
		SessionID:   "sess-1",
		Timestamp:   time.Date(2026, 3, 5, 14, 32, 7, 0, time.UTC),
		Seq:         4,
		Type:        EventToolCall,
		Role:        "assistant",
		PayloadJSON: payload,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != ev.ID {
		t.Errorf("ID: got %q, want %q", got.ID, ev.ID)
	}
	if got.Type != ev.Type {
		t.Errorf("Type: got %q, want %q", got.Type, ev.Type)
	}
	if got.Seq != ev.Seq {
		t.Errorf("Seq: got %d, want %d", got.Seq, ev.Seq)
	}
	if string(got.PayloadJSON) != string(ev.PayloadJSON) {
		t.Errorf("PayloadJSON: got %s, want %s", got.PayloadJSON, ev.PayloadJSON)
	}
}

func TestSessionJSONRoundTrip(t *testing.T) {
	s := Session{
		ID:              "sess-1",
		Source:          "claude",
		ProjectPath:     "/Users/test/project",
		SourceSessionID: "abc-123",
		CreatedAt:       time.Now().UTC().Truncate(time.Second),
		UpdatedAt:       time.Now().UTC().Truncate(time.Second),
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != s.ID || got.Source != s.Source || got.ProjectPath != s.ProjectPath {
		t.Errorf("session fields mismatch: got %+v", got)
	}
}

func TestPayloadJSONPreservesArbitraryContent(t *testing.T) {
	payloads := []string{
		`{"key":"value"}`,
		`[1,2,3]`,
		`"just a string"`,
		`{"nested":{"deep":true},"array":[1,"two",null]}`,
	}

	for _, raw := range payloads {
		ev := Event{
			ID:          "test",
			SessionID:   "test",
			Seq:         1,
			Type:        EventSystem,
			PayloadJSON: json.RawMessage(raw),
		}

		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal with payload %s: %v", raw, err)
		}

		var got Event
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if string(got.PayloadJSON) != raw {
			t.Errorf("payload not preserved: got %s, want %s", got.PayloadJSON, raw)
		}
	}
}

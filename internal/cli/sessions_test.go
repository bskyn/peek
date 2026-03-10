package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
)

func TestRunSessionsDeleteBySourceSessionID(t *testing.T) {
	tempDBPath := filepath.Join(t.TempDir(), "peek.db")
	st, err := store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.CreateSession(event.Session{
		ID:              "codex-session-1",
		Source:          "codex",
		SourceSessionID: "raw-session-1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.InsertEvent(event.Event{
		ID:          "event-1",
		SessionID:   "codex-session-1",
		Timestamp:   now,
		Seq:         0,
		Type:        event.EventAssistantMessage,
		PayloadJSON: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	prevDBPath := dbPath
	dbPath = tempDBPath
	t.Cleanup(func() {
		dbPath = prevDBPath
	})

	if err := runSessionsDelete("raw-session-1", false); err != nil {
		t.Fatalf("runSessionsDelete: %v", err)
	}

	st, err = store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer st.Close()

	sessions, err := st.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions after delete, got %d", len(sessions))
	}
}

func TestRunSessionsDeleteAll(t *testing.T) {
	tempDBPath := filepath.Join(t.TempDir(), "peek.db")
	st, err := store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	for i, id := range []string{"session-1", "session-2"} {
		if err := st.CreateSession(event.Session{
			ID:              id,
			Source:          "claude",
			SourceSessionID: id + "-raw",
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	prevDBPath := dbPath
	dbPath = tempDBPath
	t.Cleanup(func() {
		dbPath = prevDBPath
	})

	if err := runSessionsDelete("", true); err != nil {
		t.Fatalf("runSessionsDelete --all: %v", err)
	}

	st, err = store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer st.Close()

	sessions, err := st.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions after delete all, got %d", len(sessions))
	}
}

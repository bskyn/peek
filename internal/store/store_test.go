package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetSession(t *testing.T) {
	s := testStore(t)

	sess := event.Session{
		ID:              "s1",
		Source:          "claude",
		ProjectPath:     "/test/project",
		SourceSessionID: "claude-abc",
		CreatedAt:       time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:       time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetSession("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ID != sess.ID || got.Source != sess.Source || got.ProjectPath != sess.ProjectPath {
		t.Errorf("session mismatch: got %+v", got)
	}
}

func TestListSessions(t *testing.T) {
	s := testStore(t)

	for i := 0; i < 3; i++ {
		sess := event.Session{
			ID:        fmt.Sprintf("s%d", i),
			Source:    "claude",
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
	}

	sessions, err := s.ListSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
	// Should be newest first
	if sessions[0].ID != "s2" {
		t.Errorf("expected newest first, got %s", sessions[0].ID)
	}
}

func TestInsertAndGetEvents(t *testing.T) {
	s := testStore(t)

	sess := event.Session{ID: "s1", Source: "claude", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		ev := event.Event{
			ID:          fmt.Sprintf("e%d", i),
			SessionID:   "s1",
			Timestamp:   now.Add(time.Duration(i) * time.Second),
			Seq:         int64(i),
			Type:        event.EventAssistantMessage,
			PayloadJSON: json.RawMessage(fmt.Sprintf(`{"text":"msg %d"}`, i)),
		}
		if err := s.InsertEvent(ev); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	events, err := s.GetEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Seq != int64(i) {
			t.Errorf("event %d: seq = %d, want %d", i, ev.Seq, i)
		}
	}
}

func TestInsertEventIdempotent(t *testing.T) {
	s := testStore(t)

	sess := event.Session{ID: "s1", Source: "claude", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	ev := event.Event{
		ID:          "e1",
		SessionID:   "s1",
		Timestamp:   time.Now().UTC(),
		Seq:         0,
		Type:        event.EventUserMessage,
		PayloadJSON: json.RawMessage(`{"text":"hello"}`),
	}

	// Insert twice — should not error
	if err := s.InsertEvent(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEvent(ev); err != nil {
		t.Fatalf("duplicate insert should not error: %v", err)
	}

	events, err := s.GetEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after duplicate insert, got %d", len(events))
	}
}

func TestInsertEventsBatch(t *testing.T) {
	s := testStore(t)

	sess := event.Session{ID: "s1", Source: "claude", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	var events []event.Event
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		events = append(events, event.Event{
			ID:          fmt.Sprintf("e%d", i),
			SessionID:   "s1",
			Timestamp:   now,
			Seq:         int64(i),
			Type:        event.EventAssistantMessage,
			PayloadJSON: json.RawMessage(`{}`),
		})
	}

	start := time.Now()
	if err := s.InsertEvents(events); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("batch insert of 100 events took %v, expected < 5s", elapsed)
	}

	got, err := s.GetEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("expected 100 events, got %d", len(got))
	}
}

func TestCursorRoundTrip(t *testing.T) {
	s := testStore(t)

	c := Cursor{Path: "/tmp/test.jsonl", ByteOffset: 5000, SessionID: "s1"}
	if err := s.SaveCursor(c); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetCursor("/tmp/test.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	if got.Path != c.Path || got.ByteOffset != c.ByteOffset || got.SessionID != c.SessionID {
		t.Errorf("cursor mismatch: got %+v", got)
	}

	// Update cursor
	c.ByteOffset = 10000
	if err := s.SaveCursor(c); err != nil {
		t.Fatal(err)
	}

	got, err = s.GetCursor("/tmp/test.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if got.ByteOffset != 10000 {
		t.Errorf("expected updated offset 10000, got %d", got.ByteOffset)
	}
}

func TestOpenCreatesDB(t *testing.T) {
	dbPath := t.TempDir() + "/sub/dir/test.db"
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Close()

	// Re-open should work
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	s2.Close()
}

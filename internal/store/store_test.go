package store

import (
	"database/sql"
	"encoding/json"
	"errors"
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

func TestResolveSessionID(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	sessions := []event.Session{
		{
			ID:              "claude-1",
			Source:          "claude",
			SourceSessionID: "raw-claude",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "codex-1",
			Source:          "codex",
			SourceSessionID: "raw-codex",
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Second),
		},
	}

	for _, sess := range sessions {
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("create session %s: %v", sess.ID, err)
		}
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "internal id", input: "claude-1", want: "claude-1"},
		{name: "source id", input: "raw-codex", want: "codex-1"},
		{name: "missing", input: "missing", wantErr: sql.ErrNoRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ResolveSessionID(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveSessionID(%q) error = %v, want %v", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSessionID(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ResolveSessionID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveSessionIDAmbiguous(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 2; i++ {
		if err := s.CreateSession(event.Session{
			ID:              fmt.Sprintf("session-%d", i),
			Source:          "claude",
			SourceSessionID: "raw-shared",
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
	}

	_, err := s.ResolveSessionID("raw-shared")
	var ambiguousErr *AmbiguousSessionIDError
	if !errors.As(err, &ambiguousErr) {
		t.Fatalf("expected ambiguous session error, got %v", err)
	}
	if len(ambiguousErr.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(ambiguousErr.Matches))
	}
}

func TestDeleteSessionRemovesSessionData(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	parent := event.Session{
		ID:              "parent",
		Source:          "claude",
		SourceSessionID: "raw-parent",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	child := event.Session{
		ID:              "child",
		Source:          "claude",
		SourceSessionID: "raw-child",
		ParentSessionID: "parent",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	for _, sess := range []event.Session{parent, child} {
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("create session %s: %v", sess.ID, err)
		}
	}

	if err := s.InsertEvent(event.Event{
		ID:          "event-1",
		SessionID:   "parent",
		Timestamp:   now,
		Seq:         0,
		Type:        event.EventAssistantMessage,
		PayloadJSON: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	if err := s.SaveCursor(Cursor{
		Path:       "/tmp/parent.jsonl",
		ByteOffset: 123,
		SessionID:  "parent",
	}); err != nil {
		t.Fatalf("save cursor: %v", err)
	}

	deleted, err := s.DeleteSession("parent")
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if !deleted {
		t.Fatal("expected session to be deleted")
	}

	if _, err := s.GetSession("parent"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted session lookup to return sql.ErrNoRows, got %v", err)
	}

	events, err := s.GetEvents("parent")
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected events to be deleted, got %d", len(events))
	}

	if _, err := s.GetCursor("/tmp/parent.jsonl"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected cursor lookup to return sql.ErrNoRows, got %v", err)
	}

	gotChild, err := s.GetSession("child")
	if err != nil {
		t.Fatalf("GetSession(child): %v", err)
	}
	if gotChild.ParentSessionID != "" {
		t.Fatalf("expected child parent session to be cleared, got %q", gotChild.ParentSessionID)
	}
}

func TestDeleteAllSessionsClearsStore(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 2; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		if err := s.CreateSession(event.Session{
			ID:              sessionID,
			Source:          "codex",
			SourceSessionID: fmt.Sprintf("raw-%d", i),
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
		if err := s.InsertEvent(event.Event{
			ID:          fmt.Sprintf("event-%d", i),
			SessionID:   sessionID,
			Timestamp:   now,
			Seq:         0,
			Type:        event.EventAssistantMessage,
			PayloadJSON: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
		if err := s.SaveCursor(Cursor{
			Path:       fmt.Sprintf("/tmp/%d.jsonl", i),
			ByteOffset: int64(i + 1),
			SessionID:  sessionID,
		}); err != nil {
			t.Fatalf("save cursor %d: %v", i, err)
		}
	}

	deleted, err := s.DeleteAllSessions()
	if err != nil {
		t.Fatalf("DeleteAllSessions: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteAllSessions deleted %d sessions, want 2", deleted)
	}

	sessions, err := s.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected all sessions deleted, got %d", len(sessions))
	}

	for i := 0; i < 2; i++ {
		events, err := s.GetEvents(fmt.Sprintf("session-%d", i))
		if err != nil {
			t.Fatalf("GetEvents(%d): %v", i, err)
		}
		if len(events) != 0 {
			t.Fatalf("expected events for session %d to be deleted, got %d", i, len(events))
		}
		if _, err := s.GetCursor(fmt.Sprintf("/tmp/%d.jsonl", i)); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected cursor %d to be deleted, got %v", i, err)
		}
	}
}

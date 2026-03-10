package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
)

func TestRunClaudeLoadAllReplacesOnlyClaudeSessions(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	claudeProjectDir := filepath.Join(tempHome, ".claude", "projects", "test-project")
	claudeSubagentsDir := filepath.Join(claudeProjectDir, "subagents")
	if err := os.MkdirAll(claudeSubagentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rootPath := filepath.Join(claudeProjectDir, "root-session.jsonl")
	rootLine := `{"type":"user","uuid":"u1","sessionId":"root-session","timestamp":"2026-03-05T14:32:05.000Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(rootPath, []byte(rootLine), 0o644); err != nil {
		t.Fatal(err)
	}

	childPath := filepath.Join(claudeSubagentsDir, "agent-child.jsonl")
	childLine := `{"type":"user","uuid":"u2","sessionId":"agent-child","timestamp":"2026-03-05T14:32:06.000Z","message":{"role":"user","content":"child"}}` + "\n"
	if err := os.WriteFile(childPath, []byte(childLine), 0o644); err != nil {
		t.Fatal(err)
	}

	tempDBPath := filepath.Join(t.TempDir(), "peek.db")
	st, err := store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, sess := range []event.Session{
		{
			ID:              "claude-old",
			Source:          "claude",
			SourceSessionID: "old-claude",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "codex-keep",
			Source:          "codex",
			SourceSessionID: "keep-codex",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("create session %s: %v", sess.ID, err)
		}
		if err := st.InsertEvent(event.Event{
			ID:          sess.ID + "-event",
			SessionID:   sess.ID,
			Timestamp:   now,
			Seq:         0,
			Type:        event.EventAssistantMessage,
			PayloadJSON: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("insert event for %s: %v", sess.ID, err)
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

	if err := runClaudeLoadAll(); err != nil {
		t.Fatalf("runClaudeLoadAll: %v", err)
	}

	st, err = store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer st.Close()

	if _, err := st.GetSession("claude-old"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected old Claude session removed, got %v", err)
	}
	if _, err := st.GetSession("codex-keep"); err != nil {
		t.Fatalf("expected Codex session to remain, got %v", err)
	}

	rootEvents, err := st.GetEvents("claude-root-session")
	if err != nil {
		t.Fatalf("GetEvents(root): %v", err)
	}
	if len(rootEvents) != 1 {
		t.Fatalf("expected 1 root event, got %d", len(rootEvents))
	}

	childEvents, err := st.GetEvents("claude-agent-child")
	if err != nil {
		t.Fatalf("GetEvents(child): %v", err)
	}
	if len(childEvents) != 1 {
		t.Fatalf("expected 1 child event, got %d", len(childEvents))
	}

	rootCursor, err := st.GetCursor(rootPath)
	if err != nil {
		t.Fatalf("GetCursor(root): %v", err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		t.Fatalf("stat root path: %v", err)
	}
	if rootCursor.ByteOffset != rootInfo.Size() {
		t.Fatalf("expected root cursor %d, got %d", rootInfo.Size(), rootCursor.ByteOffset)
	}
}

func TestRunSessionsLoadAllImportsClaudeAndCodex(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("CODEX_HOME", filepath.Join(tempHome, ".codex"))

	claudeProjectDir := filepath.Join(tempHome, ".claude", "projects", "test-project")
	if err := os.MkdirAll(claudeProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(claudeProjectDir, "root-session.jsonl")
	claudeLine := `{"type":"user","uuid":"u1","sessionId":"root-session","timestamp":"2026-03-05T14:32:05.000Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(claudePath, []byte(claudeLine), 0o644); err != nil {
		t.Fatal(err)
	}

	codexDir := filepath.Join(tempHome, ".codex", "sessions", "2026", "03", "05")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(codexDir, "rollout-2026-03-05T10-00-00-codex-session.jsonl")
	codexLines := `{"timestamp":"2026-03-05T10:00:00.000Z","type":"session_meta","payload":{"id":"codex-session","cwd":"/projects/foo","model":"gpt-5"}}` + "\n" +
		`{"timestamp":"2026-03-05T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"Fix it"}}` + "\n"
	if err := os.WriteFile(codexPath, []byte(codexLines), 0o644); err != nil {
		t.Fatal(err)
	}

	tempDBPath := filepath.Join(t.TempDir(), "peek.db")
	st, err := store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.CreateSession(event.Session{
		ID:              "stale-session",
		Source:          "claude",
		SourceSessionID: "stale",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("create stale session: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	prevDBPath := dbPath
	dbPath = tempDBPath
	t.Cleanup(func() {
		dbPath = prevDBPath
	})

	if err := runSessionsLoadAll(); err != nil {
		t.Fatalf("runSessionsLoadAll: %v", err)
	}

	st, err = store.Open(tempDBPath)
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer st.Close()

	sessions, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 imported sessions, got %d", len(sessions))
	}

	if _, err := st.GetSession("stale-session"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected stale session removed, got %v", err)
	}

	codexEvents, err := st.GetEvents("codex-codex-session")
	if err != nil {
		t.Fatalf("GetEvents(codex): %v", err)
	}
	if len(codexEvents) != 2 {
		t.Fatalf("expected 2 Codex events, got %d", len(codexEvents))
	}

	codexCursor, err := st.GetCursor(codexPath)
	if err != nil {
		t.Fatalf("GetCursor(codex): %v", err)
	}
	codexInfo, err := os.Stat(codexPath)
	if err != nil {
		t.Fatalf("stat codex path: %v", err)
	}
	if codexCursor.ByteOffset != codexInfo.Size() {
		t.Fatalf("expected Codex cursor %d, got %d", codexInfo.Size(), codexCursor.ByteOffset)
	}
}

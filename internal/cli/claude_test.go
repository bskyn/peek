package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	claudeconn "github.com/bskyn/peek/internal/connector/claude"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
)

func parseLineForTest(line string, sessionID string, seq int64) ([]event.Event, int64, error) {
	return claudeconn.ParseLine(line, sessionID, seq)
}

// TestIntegrationTailAndPersist is an integration test that:
// 1. Creates a fake Claude session JSONL file
// 2. Sets up a temp DB
// 3. Verifies the parser + store work together
func TestIntegrationTailAndPersist(t *testing.T) {
	// Create a temp Claude directory structure
	claudeDir := t.TempDir()
	projDir := filepath.Join(claudeDir, "projects", "test-project")
	os.MkdirAll(projDir, 0o755)

	sessionID := "test-session-123"

	// Write a JSONL file with mixed events
	lines := []map[string]interface{}{
		{
			"type":      "user",
			"uuid":      "u1",
			"sessionId": sessionID,
			"timestamp": "2026-03-05T14:32:05.000Z",
			"message":   map[string]interface{}{"role": "user", "content": "Hello Claude"},
		},
		{
			"type":      "assistant",
			"uuid":      "a1",
			"sessionId": sessionID,
			"timestamp": "2026-03-05T14:32:06.000Z",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []map[string]interface{}{
					{"type": "thinking", "thinking": "The user said hello"},
					{"type": "text", "text": "Hello! How can I help?"},
				},
			},
		},
		{
			"type":      "assistant",
			"uuid":      "a2",
			"sessionId": sessionID,
			"timestamp": "2026-03-05T14:32:07.000Z",
			"message": map[string]interface{}{
				"role": "assistant",
				"content": []map[string]interface{}{
					{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": "ls /tmp"}},
				},
			},
		},
		{
			"type":      "progress",
			"timestamp": "2026-03-05T14:32:08.000Z",
			"data":      map[string]interface{}{"type": "bash_progress", "output": "running..."},
		},
	}

	var jsonlContent []byte
	for _, line := range lines {
		data, _ := json.Marshal(line)
		jsonlContent = append(jsonlContent, data...)
		jsonlContent = append(jsonlContent, '\n')
	}

	jsonlPath := filepath.Join(projDir, sessionID+".jsonl")
	os.WriteFile(jsonlPath, jsonlContent, 0o644)

	// Set up store
	dbFile := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbFile)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Simulate what the CLI does: parse each line and store
	internalID := "claude-" + sessionID
	sess := newTestSession(internalID, sessionID)
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Parse the JSONL content line by line
	fileData, _ := os.ReadFile(jsonlPath)
	fileLines := splitLines(string(fileData))

	var seq int64
	for _, line := range fileLines {
		if line == "" {
			continue
		}
		events, nextSeq, err := parseLineForTest(line, internalID, seq)
		if err != nil {
			continue // skip unparseable lines
		}
		for _, ev := range events {
			if err := st.InsertEvent(ev); err != nil {
				t.Fatalf("insert event: %v", err)
			}
		}
		seq = nextSeq
	}

	// Verify
	events, err := st.GetEvents(internalID)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}

	// Expected: user_message(1) + thinking(1) + text(1) + tool_call(1) + progress(1) = 5
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	// Verify ordering
	for i, ev := range events {
		if ev.Seq != int64(i) {
			t.Errorf("event %d: expected seq %d, got %d", i, i, ev.Seq)
		}
	}

	// Verify idempotency: re-insert all events
	for _, line := range fileLines {
		if line == "" {
			continue
		}
		events, _, _ := parseLineForTest(line, internalID, 0)
		for _, ev := range events {
			st.InsertEvent(ev) // Should not error (INSERT OR IGNORE)
		}
	}

	events2, _ := st.GetEvents(internalID)
	if len(events2) != 5 {
		t.Errorf("expected 5 events after re-insert, got %d", len(events2))
	}
}

func newTestSession(internalID, sourceSessionID string) event.Session {
	now := time.Now().UTC()
	return event.Session{
		ID:              internalID,
		Source:          "claude",
		ProjectPath:     "/test",
		SourceSessionID: sourceSessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

package codex_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bskyn/peek/internal/connector/codex"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/renderer"
	"github.com/bskyn/peek/internal/store"
)

func TestIntegrationPipeline(t *testing.T) {
	// 1. Copy sample rollout to temp dir with proper date-tree structure
	tmpDir := t.TempDir()
	sessDir := filepath.Join(tmpDir, "sessions", "2026", "03", "05")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testdata, err := os.ReadFile("testdata/sample-rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(sessDir, "rollout-2026-03-05T10-00-00-test-uuid-1234.jsonl")
	if err := os.WriteFile(rolloutPath, testdata, 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Discover finds it
	sf, err := codex.Discover(tmpDir, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if sf.SessionID != "test-uuid-1234" {
		t.Errorf("expected session id test-uuid-1234, got %s", sf.SessionID)
	}
	if sf.ProjectPath != "/projects/sample" {
		t.Errorf("expected project /projects/sample, got %s", sf.ProjectPath)
	}

	// 3. Parse all lines, collect events
	lines := splitLines(testdata)
	var allEvents []event.Event
	seq := int64(0)
	sessionID := "codex-" + sf.SessionID
	for _, line := range lines {
		if line == "" {
			continue
		}
		events, nextSeq, err := codex.ParseLine(line, sessionID, seq)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		allEvents = append(allEvents, events...)
		seq = nextSeq
	}

	// Expect: session_meta(system) + task_started(progress) + turn_context(system) +
	//   user_message + reasoning(thinking) + agent_message +
	//   response_item/message(skip) + function_call + function_call_output +
	//   token_count(progress) + response_item/message(skip) + task_complete(progress) +
	//   developer(skip)
	// = 10 events
	if len(allEvents) != 10 {
		t.Fatalf("expected 10 events, got %d", len(allEvents))
	}

	// Verify key event types
	typeCounts := make(map[event.EventType]int)
	for _, ev := range allEvents {
		typeCounts[ev.Type]++
	}
	assertCount(t, typeCounts, event.EventSystem, 2)
	assertCount(t, typeCounts, event.EventUserMessage, 1)
	assertCount(t, typeCounts, event.EventAssistantThinking, 1)
	assertCount(t, typeCounts, event.EventAssistantMessage, 1)
	assertCount(t, typeCounts, event.EventToolCall, 1)
	assertCount(t, typeCounts, event.EventToolResult, 1)
	assertCount(t, typeCounts, event.EventProgress, 3)

	// 4. Insert into store, verify count
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sess := sf.ToSession(sessionID)
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.InsertEvents(allEvents); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	stored, err := st.GetEvents(sessionID)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(stored) != len(allEvents) {
		t.Errorf("expected %d stored events, got %d", len(allEvents), len(stored))
	}

	// 5. Render to buffer, verify no panics and expected labels
	var buf bytes.Buffer
	rend := renderer.NewTerminal(&buf, false)
	rend.Source = "Codex"
	for _, ev := range allEvents {
		rend.RenderEvent(ev)
	}
	output := buf.String()
	if output == "" {
		t.Error("expected non-empty renderer output")
	}
	// Verify Codex label appears for assistant messages
	if !bytes.Contains([]byte(output), []byte("Codex")) {
		t.Error("expected 'Codex' label in renderer output")
	}
}

func splitLines(data []byte) []string {
	var lines []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		lines = append(lines, string(line))
	}
	return lines
}

func assertCount(t *testing.T, counts map[event.EventType]int, typ event.EventType, expected int) {
	t.Helper()
	if counts[typ] != expected {
		t.Errorf("expected %d %s events, got %d", expected, typ, counts[typ])
	}
}

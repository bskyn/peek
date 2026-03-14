package managed

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
)

func TestBuildBranchResumeSpecSeedsTranscriptToCutoff(t *testing.T) {
	st := testStoreForCheckpoint(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{
		ID: "s1", Source: "claude", ProjectPath: "/repo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	events := []event.Event{
		{ID: "e0", SessionID: "s1", Timestamp: now, Seq: 0, Type: event.EventUserMessage, PayloadJSON: mustJSON(map[string]string{"text": "branch me"})},
		{ID: "e1", SessionID: "s1", Timestamp: now, Seq: 1, Type: event.EventToolCall, PayloadJSON: mustJSON(map[string]any{"tool_name": "Bash", "input": map[string]string{"command": "ls"}})},
		{ID: "e2", SessionID: "s1", Timestamp: now, Seq: 2, Type: event.EventToolResult, PayloadJSON: mustJSON(map[string]string{"text": "README.md"})},
		{ID: "e3", SessionID: "s1", Timestamp: now, Seq: 3, Type: event.EventAssistantMessage, PayloadJSON: mustJSON(map[string]string{"text": "later answer"})},
	}
	if _, err := st.AppendEvents(events); err != nil {
		t.Fatal(err)
	}

	spec, err := BuildBranchResumeSpec(st, SourceClaude, []string{"--model", "sonnet"}, "ws-child", "s-child", "/tmp/ws-child", BranchAnchorResolution{
		SessionID: "s1",
		CutoffSeq: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mode != ResumeModeTranscript {
		t.Fatalf("expected transcript resume mode, got %s", spec.Mode)
	}
	if spec.Seed == nil {
		t.Fatal("expected transcript seed")
	}
	if spec.Seed.EventCount != 2 {
		t.Fatalf("expected 2 seeded events, got %d", spec.Seed.EventCount)
	}
	if got := spec.Seed.Prompt; !containsAll(got, "User: branch me", "Tool call: Bash") {
		t.Fatalf("expected prompt to include transcript cutoff, got %q", got)
	}
	if got := spec.Seed.Prompt; containsAll(got, "Tool result: README.md", "Assistant: later answer") {
		t.Fatalf("prompt should not include events after cutoff: %q", got)
	}
}

func TestBuildSwitchResumeSpecUsesProviderResumeWhenSourceSessionIDPresent(t *testing.T) {
	st := testStoreForCheckpoint(t)
	now := time.Now().UTC()

	sess := &event.Session{
		ID: "s-root", Source: "codex", SourceSessionID: "codex-session-123", CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateSession(*sess); err != nil {
		t.Fatal(err)
	}

	spec, err := BuildSwitchResumeSpec(st, SourceCodex, []string{"--model", "o4-mini"}, "ws-root", "/repo", sess)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mode != ResumeModeProvider {
		t.Fatalf("expected provider resume mode, got %s", spec.Mode)
	}
	if got := spec.CommandArgs(); len(got) != 4 || got[2] != "resume" || got[3] != "codex-session-123" {
		t.Fatalf("unexpected codex resume args: %#v", got)
	}
}

func TestBuildSwitchResumeSpecFallsBackToTranscriptSeed(t *testing.T) {
	st := testStoreForCheckpoint(t)
	now := time.Now().UTC()

	sess := &event.Session{
		ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateSession(*sess); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvents([]event.Event{
		{ID: "e0", SessionID: "s-root", Timestamp: now, Seq: 0, Type: event.EventUserMessage, PayloadJSON: mustJSON(map[string]string{"text": "continue"})},
	}); err != nil {
		t.Fatal(err)
	}

	spec, err := BuildSwitchResumeSpec(st, SourceClaude, nil, "ws-root", "/repo", sess)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mode != ResumeModeTranscript {
		t.Fatalf("expected transcript mode, got %s", spec.Mode)
	}
	if spec.Seed == nil || !containsAll(spec.Seed.Prompt, "User: continue") {
		t.Fatalf("expected seeded prompt, got %#v", spec.Seed)
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

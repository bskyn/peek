package claude

import (
	"strings"
	"testing"

	ev "github.com/bskyn/peek/internal/event"
)

func TestParseUserMessage(t *testing.T) {
	line := `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-03-05T14:32:05.000Z","message":{"role":"user","content":"What files are in /tmp?"}}`

	events, nextSeq, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventUserMessage {
		t.Errorf("expected user_message, got %s", events[0].Type)
	}
	if events[0].Role != "user" {
		t.Errorf("expected role user, got %s", events[0].Role)
	}
	if nextSeq != 1 {
		t.Errorf("expected next seq 1, got %d", nextSeq)
	}

	text := ev.PayloadText(events[0].PayloadJSON)
	if text != "What files are in /tmp?" {
		t.Errorf("unexpected text: %s", text)
	}
}

func TestParseAssistantWithThinkingAndText(t *testing.T) {
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-03-05T14:32:06.000Z","message":{"role":"assistant","id":"msg_test123","model":"claude-opus-4-6","usage":{"input_tokens":1200,"output_tokens":300,"cache_creation_input_tokens":5000,"cache_read_input_tokens":2000},"content":[{"type":"thinking","thinking":"let me reason about this"},{"type":"text","text":"Here is my answer."}]}}`

	events, nextSeq, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (thinking + text), got %d", len(events))
	}

	if events[0].Type != ev.EventAssistantThinking {
		t.Errorf("event 0: expected assistant_thinking, got %s", events[0].Type)
	}
	if events[1].Type != ev.EventAssistantMessage {
		t.Errorf("event 1: expected assistant_message, got %s", events[1].Type)
	}

	thinking, _ := ev.PayloadThinking(events[0].PayloadJSON)
	if thinking != "let me reason about this" {
		t.Errorf("unexpected thinking: %s", thinking)
	}

	text := ev.PayloadText(events[1].PayloadJSON)
	if text != "Here is my answer." {
		t.Errorf("unexpected text: %s", text)
	}
	usage, ok := ev.PayloadUsage(events[1].PayloadJSON)
	if !ok {
		t.Fatal("expected assistant message usage")
	}
	if usage.InputTokens != 1200 || usage.OutputTokens != 300 {
		t.Fatalf("unexpected usage tokens: %+v", usage)
	}
	if usage.CacheCreationTokens != 5000 || usage.CacheReadTokens != 2000 {
		t.Fatalf("unexpected cache tokens: %+v", usage)
	}
	expectedTotal := 1200 + 300 + 5000 + 2000
	if usage.TotalTokens != expectedTotal {
		t.Fatalf("unexpected total tokens: got %d, want %d", usage.TotalTokens, expectedTotal)
	}

	// Verify message_id is attached
	msgID := ev.PayloadMessageID(events[1].PayloadJSON)
	if msgID != "msg_test123" {
		t.Fatalf("unexpected message_id: %q", msgID)
	}

	if nextSeq != 2 {
		t.Errorf("expected next seq 2, got %d", nextSeq)
	}
}

func TestParseAssistantWithToolUse(t *testing.T) {
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-03-05T14:32:07.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"I should read the file"},{"type":"text","text":"Let me check."},{"type":"tool_use","id":"toolu_123","name":"Read","input":{"file_path":"/tmp/test.go"}}]}}`

	events, nextSeq, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Type != ev.EventAssistantThinking {
		t.Errorf("event 0: expected thinking, got %s", events[0].Type)
	}
	if events[1].Type != ev.EventAssistantMessage {
		t.Errorf("event 1: expected message, got %s", events[1].Type)
	}
	if events[2].Type != ev.EventToolCall {
		t.Errorf("event 2: expected tool_call, got %s", events[2].Type)
	}

	name, input := ev.PayloadToolCall(events[2].PayloadJSON)
	if name != "Read" {
		t.Errorf("expected tool name Read, got %s", name)
	}
	if !strings.Contains(input, "test.go") {
		t.Errorf("expected input to contain test.go, got %s", input)
	}

	if nextSeq != 3 {
		t.Errorf("expected next seq 3, got %d", nextSeq)
	}
}

func TestParseToolResult(t *testing.T) {
	line := `{"type":"user","uuid":"u2","sessionId":"s1","timestamp":"2026-03-05T14:32:08.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"file contents here"}]}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventToolResult {
		t.Errorf("expected tool_result, got %s", events[0].Type)
	}
	if got := ev.PayloadText(events[0].PayloadJSON); got != "file contents here" {
		t.Errorf("expected tool result text from content, got %q", got)
	}
}

func TestParseLegacyToolResultInputFallback(t *testing.T) {
	line := `{"type":"user","uuid":"u2","sessionId":"s1","timestamp":"2026-03-05T14:32:08.000Z","message":{"role":"user","content":[{"type":"tool_result","id":"toolu_123","input":"legacy file contents","content":""}]}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := ev.PayloadText(events[0].PayloadJSON); got != "legacy file contents" {
		t.Errorf("expected legacy tool result text from input, got %q", got)
	}
}

func TestParseProgressEvent(t *testing.T) {
	line := `{"type":"progress","data":{"type":"bash_progress","output":"running...","elapsedTimeSeconds":0.5},"toolUseID":"toolu_123"}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventProgress {
		t.Errorf("expected progress, got %s", events[0].Type)
	}
	if got := ev.PayloadText(events[0].PayloadJSON); got != "running..." {
		t.Errorf("expected progress text, got %q", got)
	}
}

func TestParseAgentProgressToolUseSummary(t *testing.T) {
	line := `{"type":"progress","timestamp":"2026-03-06T19:04:38.260Z","data":{"type":"agent_progress","message":{"type":"assistant","message":{"role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"toolu_01FYfwsqMNai2FWcvGaBr8T7","name":"Glob","input":{"pattern":"**/*OutcomeAccordion*"}}]}}}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := ev.PayloadText(events[0].PayloadJSON); !strings.Contains(got, `Glob(pattern: "**/*OutcomeAccordion*")`) {
		t.Errorf("expected agent progress tool summary, got %q", got)
	}
}

func TestParseAgentProgressToolResultSummary(t *testing.T) {
	line := `{"type":"progress","timestamp":"2026-03-06T19:04:39.594Z","data":{"type":"agent_progress","message":{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01HzqxZo5bZVWZWNg8GbLN1n","type":"tool_result","content":"Found 2 files\napps/core/app/components/PredictionDetailPage/components/OutcomeAccordionCard.tsx\napps/core/app/components/PredictionDetailPage/PredictionDetail.page.tsx"}]}}}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	text := ev.PayloadText(events[0].PayloadJSON)
	if !strings.Contains(text, "Found 2 files") {
		t.Errorf("expected agent progress tool result summary, got %q", text)
	}
}

func TestParseSystemEvent(t *testing.T) {
	line := `{"type":"system","subtype":"local_command","content":"hook output","level":"info"}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventSystem {
		t.Errorf("expected system, got %s", events[0].Type)
	}
}

func TestParseUnknownTypeSkipped(t *testing.T) {
	line := `{"type":"file-history-snapshot","messageId":"m1","snapshot":{}}`

	events, seq, err := ParseLine(line, "s1", 5)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
	if seq != 5 {
		t.Errorf("seq should not advance for skipped types, got %d", seq)
	}
}

func TestParseMalformedJSON(t *testing.T) {
	_, _, err := ParseLine("not json at all", "s1", 0)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseMassiveLine(t *testing.T) {
	// Create a line with >1MB of text content
	bigText := strings.Repeat("x", 2*1024*1024)
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + bigText + `"}]}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Payload should be truncated
	text := ev.PayloadText(events[0].PayloadJSON)
	if len(text) > maxPayloadBytes+50 { // some slack for "... (truncated)" suffix
		t.Errorf("text should be truncated, got length %d", len(text))
	}
}

func TestSequentialOrdering(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-03-05T14:32:05.000Z","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-03-05T14:32:06.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"progress","timestamp":"2026-03-05T14:32:07.000Z","data":{"output":"running"}}`,
	}

	var allEvents []ev.Event
	seq := int64(0)
	for _, line := range lines {
		events, nextSeq, err := ParseLine(line, "s1", seq)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		allEvents = append(allEvents, events...)
		seq = nextSeq
	}

	// Should have: user_message, thinking, text, tool_call, progress = 5 events
	if len(allEvents) != 5 {
		t.Fatalf("expected 5 events, got %d", len(allEvents))
	}

	expectedTypes := []ev.EventType{
		ev.EventUserMessage,
		ev.EventAssistantThinking,
		ev.EventAssistantMessage,
		ev.EventToolCall,
		ev.EventProgress,
	}

	for i, expected := range expectedTypes {
		if allEvents[i].Type != expected {
			t.Errorf("event %d: expected %s, got %s", i, expected, allEvents[i].Type)
		}
		if allEvents[i].Seq != int64(i) {
			t.Errorf("event %d: expected seq %d, got %d", i, i, allEvents[i].Seq)
		}
	}
}

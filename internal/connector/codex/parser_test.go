package codex

import (
	"fmt"
	"strings"
	"testing"

	ev "github.com/bskyn/peek/internal/event"
)

func TestParseSessionMeta(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:00.000Z","type":"session_meta","payload":{"id":"abc-123","cwd":"/projects/foo","model":"gpt-5","cli_version":"0.110.0"}}`

	events, nextSeq, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventSystem {
		t.Errorf("expected system, got %s", events[0].Type)
	}
	if nextSeq != 1 {
		t.Errorf("expected next seq 1, got %d", nextSeq)
	}
	text := ev.PayloadText(events[0].PayloadJSON)
	if text != "Session started" {
		t.Errorf("unexpected text: %s", text)
	}
}

func TestParseUserMessage(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"Fix the bug in main.go"}}`

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
	text := ev.PayloadText(events[0].PayloadJSON)
	if text != "Fix the bug in main.go" {
		t.Errorf("unexpected text: %s", text)
	}
	if nextSeq != 1 {
		t.Errorf("expected next seq 1, got %d", nextSeq)
	}
}

func TestParseAgentMessage(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"I'll read the file first.","phase":"commentary"}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventAssistantMessage {
		t.Errorf("expected assistant_message, got %s", events[0].Type)
	}
	text := ev.PayloadText(events[0].PayloadJSON)
	if text != "I'll read the file first." {
		t.Errorf("unexpected text: %s", text)
	}
}

func TestParseTaskStarted(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:03.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","model_context_window":128000}}`

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
}

func TestParseTokenCount(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:04.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":50}}}}`

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
}

func TestParseResponseMessageSkipped(t *testing.T) {
	// response_item/message is skipped — event_msg already covers user/assistant messages
	tests := []struct {
		name string
		line string
	}{
		{"assistant", `{"timestamp":"2026-03-05T10:00:05.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Here is my answer."}]}}`},
		{"user", `{"timestamp":"2026-03-05T10:00:05.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`},
		{"developer", `{"timestamp":"2026-03-05T10:00:05.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"system prompt stuff"}]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, nextSeq, err := ParseLine(tt.line, "s1", 5)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(events) != 0 {
				t.Errorf("expected 0 events for response_item/message, got %d", len(events))
			}
			if nextSeq != 5 {
				t.Errorf("seq should not advance, got %d", nextSeq)
			}
		})
	}
}

func TestParseResponseFunctionCall(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:06.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls -la\"}","call_id":"call_abc123"}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventToolCall {
		t.Errorf("expected tool_call, got %s", events[0].Type)
	}
	name, input := ev.PayloadToolCall(events[0].PayloadJSON)
	if name != "exec_command" {
		t.Errorf("expected tool name exec_command, got %s", name)
	}
	if !strings.Contains(input, "ls -la") {
		t.Errorf("expected input to contain ls -la, got %s", input)
	}
}

func TestParseCustomToolCallApplyPatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: /tmp/test.go\n@@\n-oldValue\n+newValue\n*** End Patch\n"
	line := fmt.Sprintf(
		`{"timestamp":"2026-03-05T10:00:06.500Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","name":"apply_patch","input":%q,"call_id":"call_patch1"}}`,
		patch,
	)

	events, nextSeq, err := ParseLine(line, "s1", 7)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if nextSeq != 8 {
		t.Fatalf("expected next seq 8, got %d", nextSeq)
	}
	if events[0].Type != ev.EventToolCall {
		t.Fatalf("expected tool_call, got %s", events[0].Type)
	}

	patchCall := ev.PayloadPatchCall(events[0].PayloadJSON)
	if patchCall == nil {
		t.Fatal("expected patch payload")
	}
	if patchCall.Operation != "update" {
		t.Errorf("expected update operation, got %s", patchCall.Operation)
	}
	if patchCall.FilePath != "/tmp/test.go" {
		t.Errorf("unexpected file path: %s", patchCall.FilePath)
	}
	if !strings.Contains(patchCall.Diff, "-oldValue") || !strings.Contains(patchCall.Diff, "+newValue") {
		t.Errorf("unexpected diff body: %s", patchCall.Diff)
	}
}

func TestParseCustomToolCallApplyPatchMultiFile(t *testing.T) {
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: /tmp/new.go",
		"+package main",
		"+",
		"+func main() {}",
		"*** Update File: /tmp/existing.go",
		"@@",
		"-oldValue",
		"+newValue",
		"*** Delete File: /tmp/old.go",
		"*** End Patch",
		"",
	}, "\n")
	line := fmt.Sprintf(
		`{"timestamp":"2026-03-05T10:00:06.750Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","name":"apply_patch","input":%q,"call_id":"call_patch_multi"}}`,
		patch,
	)

	events, nextSeq, err := ParseLine(line, "s1", 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if nextSeq != 13 {
		t.Fatalf("expected next seq 13, got %d", nextSeq)
	}

	first := ev.PayloadPatchCall(events[0].PayloadJSON)
	second := ev.PayloadPatchCall(events[1].PayloadJSON)
	third := ev.PayloadPatchCall(events[2].PayloadJSON)
	if first == nil || second == nil || third == nil {
		t.Fatal("expected patch payloads for all events")
	}
	if first.Operation != "add" || first.FilePath != "/tmp/new.go" {
		t.Errorf("unexpected first patch: %+v", first)
	}
	if second.Operation != "update" || second.FilePath != "/tmp/existing.go" {
		t.Errorf("unexpected second patch: %+v", second)
	}
	if third.Operation != "delete" || third.FilePath != "/tmp/old.go" {
		t.Errorf("unexpected third patch: %+v", third)
	}
	if third.Diff != "" {
		t.Errorf("expected empty diff for delete, got %q", third.Diff)
	}
}

func TestParseResponseFunctionCallOutput(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:07.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_abc123","output":"total 5\ndrwxr-xr-x 2 user staff 64 Mar 5 10:00 ."}}`

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
	text := ev.PayloadText(events[0].PayloadJSON)
	if !strings.Contains(text, "total 5") {
		t.Errorf("expected output to contain 'total 5', got %s", text)
	}
}

func TestParseResponseReasoning(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:08.000Z","type":"response_item","payload":{"type":"reasoning","summary":[],"content":null,"encrypted_content":"gAAAA..."}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventAssistantThinking {
		t.Errorf("expected assistant_thinking, got %s", events[0].Type)
	}
}

func TestParseTurnContextSkipped(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:09.000Z","type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/foo"}}`

	events, seq, err := ParseLine(line, "s1", 5)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for turn_context, got %d", len(events))
	}
	if seq != 5 {
		t.Errorf("seq should not advance for skipped types, got %d", seq)
	}
}

func TestParseCompacted(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:10.000Z","type":"compacted","payload":{"message":"","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the bug in main.go"}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>sandbox stuff</permissions>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"Now add tests"}]},{"type":"compaction","encrypted_content":"gAAAA_encrypted"}]}}`

	events, nextSeq, err := ParseLine(line, "s1", 5)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventUserMessage {
		t.Errorf("expected user_message, got %s", events[0].Type)
	}
	if nextSeq != 6 {
		t.Errorf("expected next seq 6, got %d", nextSeq)
	}
	text := ev.PayloadText(events[0].PayloadJSON)
	if !strings.Contains(text, "context was compacted") {
		t.Errorf("expected compaction header, got: %s", text)
	}
	if !strings.Contains(text, "Fix the bug in main.go") {
		t.Errorf("expected user message in compaction, got: %s", text)
	}
	if !strings.Contains(text, "Now add tests") {
		t.Errorf("expected second user message in compaction, got: %s", text)
	}
	// Developer messages and system-injected content should be filtered
	if strings.Contains(text, "sandbox") {
		t.Errorf("developer message should be filtered out, got: %s", text)
	}
}

func TestParseContextCompactedEvent(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:10.000Z","type":"event_msg","payload":{"type":"context_compacted"}}`

	events, nextSeq, err := ParseLine(line, "s1", 3)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventSystem {
		t.Errorf("expected system, got %s", events[0].Type)
	}
	if nextSeq != 4 {
		t.Errorf("expected next seq 4, got %d", nextSeq)
	}
}

func TestParseCompactedNoUserMessages(t *testing.T) {
	// Only developer messages — should produce 0 events
	line := `{"timestamp":"2026-03-05T10:00:10.000Z","type":"compacted","payload":{"message":"","replacement_history":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>stuff</permissions>"}]}]}}`

	events, nextSeq, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
	if nextSeq != 0 {
		t.Errorf("seq should not advance, got %d", nextSeq)
	}
}

func TestParseMalformedJSON(t *testing.T) {
	_, _, err := ParseLine("not json at all", "s1", 0)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseErrorEvent(t *testing.T) {
	line := `{"timestamp":"2026-03-05T10:00:10.000Z","type":"event_msg","payload":{"type":"error","message":"something went wrong"}}`

	events, _, err := ParseLine(line, "s1", 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ev.EventError {
		t.Errorf("expected error, got %s", events[0].Type)
	}
}

func TestSequentialOrdering(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-03-05T10:00:00.000Z","type":"session_meta","payload":{"id":"s1","cwd":"/foo"}}`,
		`{"timestamp":"2026-03-05T10:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`,
		`{"timestamp":"2026-03-05T10:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hi"}}`,
		`{"timestamp":"2026-03-05T10:00:03.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"c1"}}`,
		`{"timestamp":"2026-03-05T10:00:04.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"files"}}`,
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

	if len(allEvents) != 5 {
		t.Fatalf("expected 5 events, got %d", len(allEvents))
	}

	expectedTypes := []ev.EventType{
		ev.EventSystem,
		ev.EventUserMessage,
		ev.EventAssistantMessage,
		ev.EventToolCall,
		ev.EventToolResult,
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

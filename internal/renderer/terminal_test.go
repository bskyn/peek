package renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
)

func renderToString(ev event.Event) string {
	var buf bytes.Buffer
	r := NewTerminal(&buf, false)
	r.RenderEvent(ev)
	return buf.String()
}

func makeEvent(t event.EventType, payload map[string]interface{}) event.Event {
	data := mustMarshal(payload)
	return event.Event{
		ID:          "test",
		SessionID:   "s1",
		Timestamp:   time.Date(2026, 3, 5, 14, 32, 5, 0, time.UTC),
		Seq:         1,
		Type:        t,
		PayloadJSON: data,
	}
}

func TestRenderUserMessage(t *testing.T) {
	out := renderToString(makeEvent(event.EventUserMessage, map[string]interface{}{"text": "hello"}))
	if !strings.Contains(out, "User") {
		t.Errorf("expected 'User' in output: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output: %s", out)
	}
	if !strings.Contains(out, "[1]") {
		t.Errorf("expected sequence number in output: %s", out)
	}
}

func TestRenderAssistantThinking(t *testing.T) {
	out := renderToString(makeEvent(event.EventAssistantThinking, map[string]interface{}{
		"thinking":    "let me think about this",
		"token_count": 42,
	}))
	if !strings.Contains(out, "Thinking") {
		t.Errorf("expected 'Thinking' in output: %s", out)
	}
	if !strings.Contains(out, "42 tokens") {
		t.Errorf("expected token count in output: %s", out)
	}
	if !strings.Contains(out, "let me think") {
		t.Errorf("expected thinking text in output: %s", out)
	}
}

func TestRenderAssistantMessage(t *testing.T) {
	var buf bytes.Buffer
	r := NewTerminal(&buf, false)
	r.Source = "Claude"
	r.RenderEvent(makeEvent(event.EventAssistantMessage, map[string]interface{}{"text": "Here is my answer."}))
	out := buf.String()
	if !strings.Contains(out, "Claude") {
		t.Errorf("expected 'Claude' in output: %s", out)
	}
	if !strings.Contains(out, "Here is my answer.") {
		t.Errorf("expected message text in output: %s", out)
	}
}

func TestRenderToolCall(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "Bash",
		"input":     json.RawMessage(`{"command":"ls /tmp"}`),
	}))
	if !strings.Contains(out, "Tool: Bash") {
		t.Errorf("expected 'Tool: Bash' in output: %s", out)
	}
}

func TestRenderError(t *testing.T) {
	out := renderToString(makeEvent(event.EventError, map[string]interface{}{"text": "something broke"}))
	if !strings.Contains(out, "Error") {
		t.Errorf("expected 'Error' in output: %s", out)
	}
	if !strings.Contains(out, "something broke") {
		t.Errorf("expected error text in output: %s", out)
	}
}

func TestRenderProgressWithBody(t *testing.T) {
	out := renderToString(makeEvent(event.EventProgress, map[string]interface{}{"text": `Glob(pattern: "**/*OutcomeAccordion*")`}))
	if !strings.Contains(out, "Progress") {
		t.Errorf("expected 'Progress' in output: %s", out)
	}
	if !strings.Contains(out, `Glob(pattern: "**/*OutcomeAccordion*")`) {
		t.Errorf("expected progress text in output: %s", out)
	}
}

func TestRenderTruncatesLongOutput(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "output line")
	}
	text := strings.Join(lines, "\n")

	out := renderToString(makeEvent(event.EventToolResult, map[string]interface{}{"text": text}))
	if !strings.Contains(out, "more lines") {
		t.Errorf("expected truncation indicator in output: %s", out)
	}
}

func TestRenderSequentialNumbering(t *testing.T) {
	var buf bytes.Buffer
	r := NewTerminal(&buf, false)

	r.RenderEvent(makeEvent(event.EventUserMessage, map[string]interface{}{"text": "hi"}))
	r.RenderEvent(makeEvent(event.EventAssistantMessage, map[string]interface{}{"text": "hello"}))
	r.RenderEvent(makeEvent(event.EventToolCall, map[string]interface{}{"tool_name": "Bash", "input": json.RawMessage(`{}`)}))

	out := buf.String()
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "[2]") || !strings.Contains(out, "[3]") {
		t.Errorf("expected sequential numbers [1] [2] [3] in output: %s", out)
	}
}

func TestRenderNoColorWhenDisabled(t *testing.T) {
	out := renderToString(makeEvent(event.EventUserMessage, map[string]interface{}{"text": "test"}))
	if strings.Contains(out, "\033[") {
		t.Errorf("expected no ANSI codes when color disabled: %s", out)
	}
}

func TestRenderEditToolCall(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "Edit",
		"input":     json.RawMessage(`{"file_path":"/tmp/test.go","old_string":"func foo() {}","new_string":"func foo() {\n\treturn 42\n}"}`),
	}))
	if !strings.Contains(out, "Edit: /tmp/test.go") {
		t.Errorf("expected 'Edit: /tmp/test.go' in output: %s", out)
	}
	if !strings.Contains(out, "- func foo() {}") {
		t.Errorf("expected removed line in output: %s", out)
	}
	if !strings.Contains(out, "+ func foo() {") {
		t.Errorf("expected added line in output: %s", out)
	}
	if !strings.Contains(out, "+ \treturn 42") {
		t.Errorf("expected added line 'return 42' in output: %s", out)
	}
}

func TestRenderEditMultilineDiff(t *testing.T) {
	oldStr := "line1\nline2\nline3\nline4\nline5"
	newStr := "line1\nline2\nmodified3\nline4\nline5\nline6"
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "Edit",
		"input":     json.RawMessage(`{"file_path":"/tmp/test.go","old_string":"` + strings.ReplaceAll(oldStr, "\n", `\n`) + `","new_string":"` + strings.ReplaceAll(newStr, "\n", `\n`) + `"}`),
	}))
	if !strings.Contains(out, "- line3") {
		t.Errorf("expected removed 'line3' in output: %s", out)
	}
	if !strings.Contains(out, "+ modified3") {
		t.Errorf("expected added 'modified3' in output: %s", out)
	}
	if !strings.Contains(out, "+ line6") {
		t.Errorf("expected added 'line6' in output: %s", out)
	}
}

func TestRenderWriteToolCall(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "Write",
		"input":     json.RawMessage(`{"file_path":"/tmp/new.go","content":"package main\n\nfunc main() {}\n"}`),
	}))
	if !strings.Contains(out, "Write: /tmp/new.go") {
		t.Errorf("expected 'Write: /tmp/new.go' in output: %s", out)
	}
	if !strings.Contains(out, "+ package main") {
		t.Errorf("expected file content in output: %s", out)
	}
}

func TestRenderApplyPatchToolCall(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "apply_patch",
		"input": json.RawMessage(`{
			"file_path":"/tmp/test.go",
			"operation":"update",
			"diff":"@@\n-oldValue\n+newValue"
		}`),
	}))
	if !strings.Contains(out, "Edit: /tmp/test.go") {
		t.Errorf("expected edit header in output: %s", out)
	}
	if !strings.Contains(out, "@@") {
		t.Errorf("expected hunk header in output: %s", out)
	}
	if !strings.Contains(out, "-oldValue") {
		t.Errorf("expected removed line in output: %s", out)
	}
	if !strings.Contains(out, "+newValue") {
		t.Errorf("expected added line in output: %s", out)
	}
}

func TestRenderApplyPatchDeleteToolCall(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "apply_patch",
		"input": json.RawMessage(`{
			"file_path":"/tmp/deleted.go",
			"operation":"delete",
			"diff":""
		}`),
	}))
	if !strings.Contains(out, "Delete: /tmp/deleted.go") {
		t.Errorf("expected delete header in output: %s", out)
	}
	if !strings.Contains(out, "file deleted") {
		t.Errorf("expected delete body in output: %s", out)
	}
}

func TestRenderEditShowsFullDiff(t *testing.T) {
	// Build a large diff (50+ changed lines) and verify nothing is truncated
	var oldLines, newLines []string
	for i := 0; i < 60; i++ {
		oldLines = append(oldLines, fmt.Sprintf("old line %d", i))
		newLines = append(newLines, fmt.Sprintf("new line %d", i))
	}
	oldStr := strings.Join(oldLines, "\n")
	newStr := strings.Join(newLines, "\n")

	payload := mustMarshal(map[string]interface{}{
		"tool_name": "Edit",
		"input":     json.RawMessage(`{"file_path":"/tmp/big.go","old_string":` + mustJSONString(oldStr) + `,"new_string":` + mustJSONString(newStr) + `}`),
	})
	ev := event.Event{
		ID:          "test",
		SessionID:   "s1",
		Timestamp:   time.Date(2026, 3, 5, 14, 32, 5, 0, time.UTC),
		Seq:         1,
		Type:        event.EventToolCall,
		PayloadJSON: payload,
	}

	out := renderToString(ev)
	if strings.Contains(out, "more diff lines") {
		t.Errorf("expected no truncation in diff output: %s", out)
	}
	// Verify last changed lines are present
	if !strings.Contains(out, "new line 59") {
		t.Errorf("expected last new line in full diff: %s", out)
	}
	if !strings.Contains(out, "old line 59") {
		t.Errorf("expected last old line in full diff: %s", out)
	}
}

func TestRenderUserMessageColor(t *testing.T) {
	var buf bytes.Buffer
	r := NewTerminal(&buf, true)
	r.RenderEvent(makeEvent(event.EventUserMessage, map[string]interface{}{"text": "hello"}))
	out := buf.String()
	// Should contain blue ANSI code (\033[34m)
	if !strings.Contains(out, "\033[34m") {
		t.Errorf("expected blue color for user message: %s", out)
	}
}

func mustJSONString(s string) string {
	data := mustMarshal(s)
	return string(data)
}

func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func TestFormatModel(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"claude-opus-4-6", "Opus 4.6"},
		{"claude-sonnet-4-6", "Sonnet 4.6"},
		{"claude-haiku-4-5-20251001", "Haiku 4.5"},
		{"gpt-5", "gpt-5"},
		{"o3-pro", "o3-pro"},
	}
	for _, tt := range tests {
		got := formatModel(tt.input)
		if got != tt.want {
			t.Errorf("formatModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAssistantLabelWithModel(t *testing.T) {
	var buf bytes.Buffer
	r := NewTerminal(&buf, false)
	r.Source = "Claude"
	r.RenderEvent(makeEvent(event.EventAssistantMessage, map[string]interface{}{
		"text":  "hello",
		"model": "claude-opus-4-6",
	}))
	out := buf.String()
	if !strings.Contains(out, "Claude (Opus 4.6)") {
		t.Errorf("expected 'Claude (Opus 4.6)' in output: %s", out)
	}
}

func TestModelTrackedFromSystemEvent(t *testing.T) {
	var buf bytes.Buffer
	r := NewTerminal(&buf, false)
	r.Source = "Codex"
	// System event with model (like Codex session_meta)
	r.RenderEvent(makeEvent(event.EventSystem, map[string]interface{}{
		"text":  "Session started",
		"model": "o3-pro",
	}))
	buf.Reset()
	r.seqNum = 0
	// Subsequent assistant message should pick up the model
	r.RenderEvent(makeEvent(event.EventAssistantMessage, map[string]interface{}{
		"text": "hello",
	}))
	out := buf.String()
	if !strings.Contains(out, "Codex (o3-pro)") {
		t.Errorf("expected 'Codex (o3-pro)' in output: %s", out)
	}
}

func TestRenderNonEditToolCallUnchanged(t *testing.T) {
	out := renderToString(makeEvent(event.EventToolCall, map[string]interface{}{
		"tool_name": "Bash",
		"input":     json.RawMessage(`{"command":"ls /tmp"}`),
	}))
	if !strings.Contains(out, "Tool: Bash") {
		t.Errorf("expected 'Tool: Bash' in output: %s", out)
	}
}

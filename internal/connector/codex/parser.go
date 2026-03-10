package codex

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ev "github.com/bskyn/peek/internal/event"
)

const maxPayloadBytes = 512 * 1024

// rawRolloutLine matches the top-level structure of a Codex rollout JSONL line.
type rawRolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// rawEventMsg matches the payload of event_msg lines.
type rawEventMsg struct {
	Type    string          `json:"type"`
	Message string          `json:"message"`
	Phase   string          `json:"phase"`
	Info    json.RawMessage `json:"info"`
	TurnID  string          `json:"turn_id"`
}

// rawResponseItem matches the payload of response_item lines.
type rawResponseItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    string          `json:"output"`
	Phase     string          `json:"phase"`
	Status    string          `json:"status"`
}

// ParseLine parses a single JSONL line from a Codex rollout file and returns
// zero or more canonical events. The seq parameter is the starting sequence number;
// the returned int64 is the next sequence number to use.
func ParseLine(line string, sessionID string, seq int64) ([]ev.Event, int64, error) {
	var raw rawRolloutLine
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, seq, fmt.Errorf("parse json: %w", err)
	}

	ts := parseTimestamp(raw.Timestamp)

	switch raw.Type {
	case "session_meta":
		return parseSessionMeta(raw, sessionID, ts, seq)
	case "turn_context":
		return parseTurnContext(raw, sessionID, ts, seq)
	case "event_msg":
		return parseEventMsg(raw, sessionID, ts, seq)
	case "response_item":
		return parseResponseItem(raw, sessionID, ts, seq)
	case "compacted":
		return parseCompacted(raw, sessionID, ts, seq)
	default:
		return nil, seq, nil
	}
}

func parseSessionMeta(raw rawRolloutLine, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var meta struct {
		Model string `json:"model"`
		CWD   string `json:"cwd"`
	}
	if err := json.Unmarshal(raw.Payload, &meta); err != nil {
		return nil, seq, fmt.Errorf("parse session_meta payload: %w", err)
	}

	event := ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventSystem,
		PayloadJSON: mustJSON(map[string]interface{}{
			"text":  "Session started",
			"model": meta.Model,
			"cwd":   meta.CWD,
		}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseTurnContext(raw rawRolloutLine, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var ctx struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw.Payload, &ctx); err != nil {
		return nil, seq, fmt.Errorf("parse turn_context payload: %w", err)
	}

	if ctx.Model == "" {
		return nil, seq, nil
	}

	event := ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventSystem,
		PayloadJSON: mustJSON(map[string]interface{}{
			"model": ctx.Model,
		}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseEventMsg(raw rawRolloutLine, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var msg rawEventMsg
	if err := json.Unmarshal(raw.Payload, &msg); err != nil {
		return nil, seq, fmt.Errorf("parse event_msg payload: %w", err)
	}

	switch msg.Type {
	case "user_message":
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventUserMessage,
			Role:        "user",
			PayloadJSON: mustJSON(map[string]interface{}{"text": truncateString(msg.Message, maxPayloadBytes)}),
		}
		return []ev.Event{event}, seq + 1, nil

	case "agent_message":
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventAssistantMessage,
			Role:        "assistant",
			PayloadJSON: mustJSON(map[string]interface{}{"text": truncateString(msg.Message, maxPayloadBytes)}),
		}
		return []ev.Event{event}, seq + 1, nil

	case "task_started", "task_complete", "turn_started", "turn_complete":
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventProgress,
			PayloadJSON: mustJSON(map[string]interface{}{"subtype": msg.Type, "turn_id": msg.TurnID}),
		}
		return []ev.Event{event}, seq + 1, nil

	case "token_count":
		payload := map[string]interface{}{
			"subtype": "token_count",
			"info":    msg.Info,
		}
		if usage, ok := parseTokenCountUsage(msg.Info); ok {
			payload["usage"] = usage
		}
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventProgress,
			PayloadJSON: mustJSON(payload),
		}
		return []ev.Event{event}, seq + 1, nil

	case "error", "warning":
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventError,
			PayloadJSON: mustJSON(map[string]interface{}{"text": msg.Message, "subtype": msg.Type}),
		}
		return []ev.Event{event}, seq + 1, nil

	case "context_compacted":
		event := ev.Event{
			ID:          eventID(sessionID, seq),
			SessionID:   sessionID,
			Timestamp:   ts,
			Seq:         seq,
			Type:        ev.EventSystem,
			PayloadJSON: mustJSON(map[string]interface{}{"text": "Context compacted"}),
		}
		return []ev.Event{event}, seq + 1, nil

	default:
		return nil, seq, nil
	}
}

func parseResponseItem(raw rawRolloutLine, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var item rawResponseItem
	if err := json.Unmarshal(raw.Payload, &item); err != nil {
		return nil, seq, fmt.Errorf("parse response_item payload: %w", err)
	}

	switch item.Type {
	case "message":
		// Skip — user/assistant messages are already emitted by event_msg
		// (user_message and agent_message). response_item/message duplicates them.
		return nil, seq, nil
	case "function_call":
		return parseResponseFunctionCall(item, sessionID, ts, seq)
	case "custom_tool_call":
		return parseResponseCustomToolCall(item, sessionID, ts, seq)
	case "function_call_output":
		return parseResponseFunctionCallOutput(item, sessionID, ts, seq)
	case "reasoning":
		return parseResponseReasoning(item, sessionID, ts, seq)
	default:
		return nil, seq, nil
	}
}

func parseResponseFunctionCall(item rawResponseItem, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	if isApplyPatchTool(item.Name) {
		if events, nextSeq := parseApplyPatchToolCalls(item.Name, item.Arguments, item.CallID, sessionID, ts, seq); len(events) > 0 {
			return events, nextSeq, nil
		}
	}

	// Codex arguments is a JSON string — try to parse as raw JSON for the input field,
	// fall back to storing as a string if it's not valid JSON.
	var input interface{}
	if json.Valid([]byte(item.Arguments)) {
		input = json.RawMessage(item.Arguments)
	} else {
		input = item.Arguments
	}

	return []ev.Event{makeToolCallEvent(sessionID, ts, seq, item.Name, input, item.CallID)}, seq + 1, nil
}

func parseResponseCustomToolCall(item rawResponseItem, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	if isApplyPatchTool(item.Name) {
		if events, nextSeq := parseApplyPatchToolCalls(item.Name, item.Input, item.CallID, sessionID, ts, seq); len(events) > 0 {
			return events, nextSeq, nil
		}
	}

	return []ev.Event{makeToolCallEvent(sessionID, ts, seq, item.Name, item.Input, item.CallID)}, seq + 1, nil
}

func parseResponseFunctionCallOutput(item rawResponseItem, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	event := ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventToolResult,
		Role:      "user",
		PayloadJSON: mustJSON(map[string]interface{}{
			"text":    truncateString(item.Output, maxPayloadBytes),
			"call_id": item.CallID,
		}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseResponseReasoning(item rawResponseItem, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	// Codex reasoning has encrypted_content — we can't read it, but we can note it happened
	event := ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventAssistantThinking,
		Role:      "assistant",
		PayloadJSON: mustJSON(map[string]interface{}{
			"thinking":    "(reasoning — encrypted)",
			"token_count": 0,
		}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseApplyPatchToolCalls(toolName, input, callID, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64) {
	patches := parseApplyPatchInput(input)
	if len(patches) == 0 {
		return nil, seq
	}

	events := make([]ev.Event, 0, len(patches))
	for _, patch := range patches {
		payload := map[string]interface{}{
			"file_path": patch.FilePath,
			"operation": patch.Operation,
			"diff":      truncateString(patch.Diff, maxPayloadBytes),
		}
		if patch.MoveTo != "" {
			payload["move_to"] = patch.MoveTo
		}

		events = append(events, makeToolCallEvent(sessionID, ts, seq, toolName, payload, callID))
		seq++
	}

	return events, seq
}

func makeToolCallEvent(sessionID string, ts time.Time, seq int64, toolName string, input interface{}, callID string) ev.Event {
	return ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventToolCall,
		Role:      "assistant",
		PayloadJSON: mustJSON(map[string]interface{}{
			"tool_name": toolName,
			"input":     input,
			"call_id":   callID,
		}),
	}
}

func isApplyPatchTool(name string) bool {
	return name == "apply_patch" || strings.HasSuffix(name, ".apply_patch")
}

func parseTokenCountUsage(raw json.RawMessage) (map[string]int, bool) {
	var info struct {
		TotalTokenUsage struct {
			InputTokens       int `json:"input_tokens"`
			CachedInputTokens int `json:"cached_input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			TotalTokens       int `json:"total_tokens"`
		} `json:"total_token_usage"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, false
	}
	if info.TotalTokenUsage.TotalTokens == 0 {
		info.TotalTokenUsage.TotalTokens = info.TotalTokenUsage.InputTokens + info.TotalTokenUsage.OutputTokens
	}
	if info.TotalTokenUsage.TotalTokens == 0 {
		return nil, false
	}
	// OpenAI cached_input_tokens is a subset of input_tokens.
	// Normalize: input_tokens = non-cached portion, cache_read_tokens = cached portion.
	nonCached := info.TotalTokenUsage.InputTokens - info.TotalTokenUsage.CachedInputTokens
	if nonCached < 0 {
		nonCached = 0
	}
	return map[string]int{
		"input_tokens":      nonCached,
		"output_tokens":     info.TotalTokenUsage.OutputTokens,
		"cache_read_tokens": info.TotalTokenUsage.CachedInputTokens,
		"total_tokens":      info.TotalTokenUsage.TotalTokens,
	}, true
}

// rawCompactedPayload matches the payload of compacted lines.
type rawCompactedPayload struct {
	ReplacementHistory []rawCompactedMessage `json:"replacement_history"`
}

type rawCompactedMessage struct {
	Type    string                     `json:"type"`
	Role    string                     `json:"role"`
	Content []rawCompactedContentBlock `json:"content"`
}

type rawCompactedContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func parseCompacted(raw rawRolloutLine, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var payload rawCompactedPayload
	if err := json.Unmarshal(raw.Payload, &payload); err != nil {
		return nil, seq, nil
	}

	// Extract user message texts from the replacement history
	var parts []string
	for _, msg := range payload.ReplacementHistory {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == "input_text" && block.Text != "" {
				// Skip system-injected messages (AGENTS.md, environment_context, etc.)
				if strings.HasPrefix(block.Text, "<") || strings.HasPrefix(block.Text, "#") {
					continue
				}
				parts = append(parts, block.Text)
			}
		}
	}

	if len(parts) == 0 {
		return nil, seq, nil
	}

	summary := "This session's context was compacted. Earlier conversation messages:\n\n"
	for i, p := range parts {
		text := truncateString(p, 2000)
		summary += fmt.Sprintf("%d. %s\n", i+1, text)
	}

	event := ev.Event{
		ID:          eventID(sessionID, seq),
		SessionID:   sessionID,
		Timestamp:   ts,
		Seq:         seq,
		Type:        ev.EventUserMessage,
		Role:        "user",
		PayloadJSON: mustJSON(map[string]interface{}{"text": truncateString(summary, maxPayloadBytes)}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseTimestamp(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return time.Now().UTC()
		}
	}
	return t
}

func eventID(sessionID string, seq int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", sessionID, seq)))
	return fmt.Sprintf("%x", h[:8])
}

func mustJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "... (truncated)"
}

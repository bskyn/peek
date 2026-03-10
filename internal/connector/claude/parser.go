package claude

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ev "github.com/bskyn/peek/internal/event"
)

const maxPayloadBytes = 512 * 1024 // 512KB truncation limit

// rawSessionEvent matches the top-level structure of a Claude session JSONL line.
type rawSessionEvent struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  string          `json:"timestamp"`
	Message    json.RawMessage `json:"message"`
	Data       json.RawMessage `json:"data"`
	Subtype    string          `json:"subtype"`
	Content    json.RawMessage `json:"content"`
	Level      string          `json:"level"`
	ToolUseID  string          `json:"toolUseID"`
	ToolResult json.RawMessage `json:"toolUseResult"`
}

// rawMessage matches the message field in user/assistant events.
type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model"`
	ID      string          `json:"id"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// contentBlock represents a single block in the assistant's content array.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type rawProgressPayload struct {
	Type       string               `json:"type"`
	Output     string               `json:"output"`
	FullOutput string               `json:"fullOutput"`
	HookEvent  string               `json:"hookEvent"`
	HookName   string               `json:"hookName"`
	Command    string               `json:"command"`
	Prompt     string               `json:"prompt"`
	AgentID    string               `json:"agentId"`
	Message    *rawProgressEnvelope `json:"message"`
}

type rawProgressEnvelope struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type rawToolResultContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ToolName string `json:"tool_name"`
}

// ParseLine parses a single JSONL line from a Claude session file and returns
// zero or more canonical events. The seq parameter is the starting sequence number;
// the returned int64 is the next sequence number to use.
func ParseLine(line string, sessionID string, seq int64) ([]ev.Event, int64, error) {
	var raw rawSessionEvent
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, seq, fmt.Errorf("parse json: %w", err)
	}

	ts := parseTimestamp(raw.Timestamp)

	switch raw.Type {
	case "user":
		return parseUserEvent(raw, sessionID, ts, seq)
	case "assistant":
		return parseAssistantEvent(raw, sessionID, ts, seq)
	case "progress":
		return parseProgressEvent(raw, sessionID, ts, seq)
	case "system":
		return parseSystemEvent(raw, sessionID, ts, seq)
	default:
		// Skip unknown types (file-history-snapshot, last-prompt, etc.)
		return nil, seq, nil
	}
}

func parseUserEvent(raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil, seq, fmt.Errorf("parse user message: %w", err)
	}

	// Check if content is an array (might contain tool_results)
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		return parseUserContentBlocks(blocks, raw, sessionID, ts, seq)
	}

	// Content is a plain string
	var text string
	if err := json.Unmarshal(msg.Content, &text); err != nil {
		text = string(msg.Content)
	}

	event := ev.Event{
		ID:          eventID(sessionID, seq),
		SessionID:   sessionID,
		Timestamp:   ts,
		Seq:         seq,
		Type:        ev.EventUserMessage,
		Role:        "user",
		PayloadJSON: mustJSON(map[string]interface{}{"text": text}),
	}
	return []ev.Event{event}, seq + 1, nil
}

func parseUserContentBlocks(blocks []contentBlock, raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var events []ev.Event

	for _, block := range blocks {
		switch block.Type {
		case "tool_result":
			toolUseID := block.ToolUseID
			if toolUseID == "" {
				toolUseID = block.ID
			}
			payload := mustJSON(map[string]interface{}{
				"tool_use_id": toolUseID,
				"text":        truncateString(extractToolResultText(block, raw.ToolResult), maxPayloadBytes),
			})
			events = append(events, ev.Event{
				ID:          eventID(sessionID, seq),
				SessionID:   sessionID,
				Timestamp:   ts,
				Seq:         seq,
				Type:        ev.EventToolResult,
				Role:        "user",
				PayloadJSON: payload,
			})
			seq++
		default:
			// Regular text content in array form
			text := block.Text
			if text == "" && block.Type == "" {
				continue
			}
			events = append(events, ev.Event{
				ID:          eventID(sessionID, seq),
				SessionID:   sessionID,
				Timestamp:   ts,
				Seq:         seq,
				Type:        ev.EventUserMessage,
				Role:        "user",
				PayloadJSON: mustJSON(map[string]interface{}{"text": text}),
			})
			seq++
		}
	}

	return events, seq, nil
}

func parseAssistantEvent(raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil, seq, fmt.Errorf("parse assistant message: %w", err)
	}

	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil, seq, fmt.Errorf("parse content blocks: %w", err)
	}

	var events []ev.Event

	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			tokenCount := len(block.Thinking) / 4 // rough estimate
			if msg.Usage != nil && msg.Usage.OutputTokens > 0 {
				tokenCount = msg.Usage.OutputTokens
			}
			payload := map[string]interface{}{
				"thinking":    truncateString(block.Thinking, maxPayloadBytes),
				"token_count": tokenCount,
			}
			if msg.Model != "" {
				payload["model"] = msg.Model
			}
			events = append(events, ev.Event{
				ID:          eventID(sessionID, seq),
				SessionID:   sessionID,
				Timestamp:   ts,
				Seq:         seq,
				Type:        ev.EventAssistantThinking,
				Role:        "assistant",
				PayloadJSON: mustJSON(payload),
			})
			seq++

		case "text":
			payload := map[string]interface{}{
				"text": truncateString(block.Text, maxPayloadBytes),
			}
			if msg.Model != "" {
				payload["model"] = msg.Model
			}
			events = append(events, ev.Event{
				ID:          eventID(sessionID, seq),
				SessionID:   sessionID,
				Timestamp:   ts,
				Seq:         seq,
				Type:        ev.EventAssistantMessage,
				Role:        "assistant",
				PayloadJSON: mustJSON(payload),
			})
			seq++

		case "tool_use":
			events = append(events, ev.Event{
				ID:        eventID(sessionID, seq),
				SessionID: sessionID,
				Timestamp: ts,
				Seq:       seq,
				Type:      ev.EventToolCall,
				Role:      "assistant",
				PayloadJSON: mustJSON(map[string]interface{}{
					"tool_use_id": block.ID,
					"tool_name":   block.Name,
					"input":       block.Input,
				}),
			})
			seq++
		}
	}

	attachUsage(events, msg)
	return events, seq, nil
}

func attachUsage(events []ev.Event, msg rawMessage) {
	if msg.Usage == nil || len(events) == 0 {
		return
	}

	target := lastEventIndex(events, ev.EventAssistantMessage)
	if target < 0 {
		target = lastEventIndex(events, ev.EventAssistantThinking)
	}
	if target < 0 {
		target = lastEventIndex(events, ev.EventToolCall)
	}
	if target < 0 {
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(events[target].PayloadJSON, &payload); err != nil {
		return
	}

	usage := map[string]int{
		"input_tokens":  msg.Usage.InputTokens,
		"output_tokens": msg.Usage.OutputTokens,
		"total_tokens":  msg.Usage.InputTokens + msg.Usage.OutputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens,
	}
	if msg.Usage.CacheCreationInputTokens > 0 {
		usage["cache_creation_tokens"] = msg.Usage.CacheCreationInputTokens
	}
	if msg.Usage.CacheReadInputTokens > 0 {
		usage["cache_read_tokens"] = msg.Usage.CacheReadInputTokens
	}
	payload["usage"] = usage
	if msg.ID != "" {
		payload["message_id"] = msg.ID
	}
	if msg.Model != "" {
		payload["model"] = msg.Model
	}
	events[target].PayloadJSON = mustJSON(payload)
}

func lastEventIndex(events []ev.Event, eventType ev.EventType) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == eventType {
			return i
		}
	}
	return -1
}

func parseProgressEvent(raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	subtype, text := summarizeProgress(raw.Data)
	payload := map[string]interface{}{
		"data": raw.Data,
	}
	if subtype != "" {
		payload["subtype"] = subtype
	}
	if text != "" {
		payload["text"] = truncateString(text, maxPayloadBytes)
	}
	if raw.ToolUseID != "" {
		payload["tool_use_id"] = raw.ToolUseID
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
}

func parseSystemEvent(raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	event := ev.Event{
		ID:        eventID(sessionID, seq),
		SessionID: sessionID,
		Timestamp: ts,
		Seq:       seq,
		Type:      ev.EventSystem,
		PayloadJSON: mustJSON(map[string]interface{}{
			"subtype": raw.Subtype,
			"content": string(raw.Content),
			"level":   raw.Level,
		}),
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

func extractToolResultText(block contentBlock, toolResult json.RawMessage) string {
	if text := extractToolResultContent(block.Content); text != "" {
		return text
	}
	// Older Claude versions stored tool output in "input" instead of "content".
	if text := extractToolResultContent(block.Input); text != "" {
		return text
	}
	if text := extractToolUseResultText(toolResult); text != "" {
		return text
	}
	return ""
}

func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var blocks []rawToolResultContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil && len(blocks) > 0 {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			switch {
			case strings.TrimSpace(block.Text) != "":
				parts = append(parts, strings.TrimSpace(block.Text))
			case strings.TrimSpace(block.ToolName) != "":
				parts = append(parts, strings.TrimSpace(block.ToolName))
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

func extractToolUseResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var result struct {
		Content  string   `json:"content"`
		Stdout   string   `json:"stdout"`
		Stderr   string   `json:"stderr"`
		Matches  []string `json:"matches"`
		Type     string   `json:"type"`
		FilePath string   `json:"filePath"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ""
	}

	switch {
	case strings.TrimSpace(result.Content) != "":
		return strings.TrimSpace(result.Content)
	case strings.TrimSpace(result.Stdout) != "" || strings.TrimSpace(result.Stderr) != "":
		return strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
	case len(result.Matches) > 0:
		return strings.Join(result.Matches, "\n")
	case result.Type != "" && result.FilePath != "":
		return fmt.Sprintf("%s: %s", result.Type, result.FilePath)
	default:
		return ""
	}
}

func summarizeProgress(raw json.RawMessage) (subtype, text string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}

	var progress rawProgressPayload
	if err := json.Unmarshal(raw, &progress); err != nil {
		return "", ""
	}

	if progress.Type == "" {
		if text := strings.TrimSpace(progress.Output); text != "" {
			return "", text
		}
		if text := strings.TrimSpace(progress.FullOutput); text != "" {
			return "", text
		}
	}

	switch progress.Type {
	case "agent_progress":
		return progress.Type, summarizeAgentProgress(progress)
	case "bash_progress":
		if text := strings.TrimSpace(progress.Output); text != "" {
			return progress.Type, text
		}
		return progress.Type, strings.TrimSpace(progress.FullOutput)
	case "hook_progress":
		return progress.Type, firstNonEmpty(progress.HookName, progress.HookEvent, progress.Command)
	default:
		if progress.Message != nil {
			return progress.Type, summarizeProgressMessage(progress.Message)
		}
		return progress.Type, ""
	}
}

func summarizeAgentProgress(progress rawProgressPayload) string {
	if prompt := firstLine(progress.Prompt); prompt != "" {
		return prompt
	}
	return summarizeProgressMessage(progress.Message)
}

func summarizeProgressMessage(envelope *rawProgressEnvelope) string {
	if envelope == nil {
		return ""
	}

	switch envelope.Type {
	case "assistant":
		return summarizeAssistantMessage(envelope.Message)
	case "user":
		return summarizeUserMessage(envelope.Message)
	default:
		return ""
	}
}

func summarizeAssistantMessage(raw json.RawMessage) string {
	var msg rawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}

	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		var text string
		if err := json.Unmarshal(msg.Content, &text); err != nil {
			return ""
		}
		return strings.TrimSpace(text)
	}

	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			if summary := formatProgressToolUse(block.Name, block.Input); summary != "" {
				return summary
			}
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				return text
			}
		case "thinking":
			if thinking := firstLine(block.Thinking); thinking != "" {
				return thinking
			}
		}
	}

	return ""
}

func summarizeUserMessage(raw json.RawMessage) string {
	var msg rawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}

	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		for _, block := range blocks {
			switch block.Type {
			case "tool_result":
				if text := extractToolResultText(block, nil); text != "" {
					return text
				}
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					return text
				}
			}
		}
	}

	var text string
	if err := json.Unmarshal(msg.Content, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func formatProgressToolUse(name string, input json.RawMessage) string {
	if name == "" {
		return ""
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(input, &params); err == nil {
		switch {
		case jsonString(params["file_path"]) != "":
			return fmt.Sprintf("%s(%s)", name, jsonString(params["file_path"]))
		case jsonString(params["pattern"]) != "":
			pattern := fmt.Sprintf("pattern: %q", jsonString(params["pattern"]))
			if path := jsonString(params["path"]); path != "" {
				return fmt.Sprintf("%s(%s, path: %q)", name, pattern, path)
			}
			return fmt.Sprintf("%s(%s)", name, pattern)
		case jsonString(params["command"]) != "":
			return fmt.Sprintf("%s(%s)", name, firstLine(jsonString(params["command"])))
		}
	}

	inputStr := strings.TrimSpace(string(input))
	switch inputStr {
	case "", "null", "{}":
		return name
	}
	if len(inputStr) > 120 {
		inputStr = inputStr[:120] + "..."
	}
	return fmt.Sprintf("%s(%s)", name, inputStr)
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

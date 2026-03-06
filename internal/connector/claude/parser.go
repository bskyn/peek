package claude

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// contentBlock represents a single block in the assistant's content array.
type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
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
			payload := mustJSON(map[string]interface{}{
				"tool_use_id": block.ID,
				"text":        truncateString(string(block.Input), maxPayloadBytes),
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

	return events, seq, nil
}

func parseProgressEvent(raw rawSessionEvent, sessionID string, ts time.Time, seq int64) ([]ev.Event, int64, error) {
	event := ev.Event{
		ID:          eventID(sessionID, seq),
		SessionID:   sessionID,
		Timestamp:   ts,
		Seq:         seq,
		Type:        ev.EventProgress,
		PayloadJSON: raw.Data,
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

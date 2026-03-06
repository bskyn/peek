package event

import (
	"encoding/json"
	"strings"
)

// PayloadText extracts the "text" field from an event's payload JSON, if present.
func PayloadText(payload json.RawMessage) string {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Text
}

// PayloadThinking extracts the "thinking" field from a thinking event payload.
func PayloadThinking(payload json.RawMessage) (string, int) {
	var p struct {
		Thinking   string `json:"thinking"`
		TokenCount int    `json:"token_count"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", 0
	}
	return p.Thinking, p.TokenCount
}

// PayloadModel extracts the "model" field from an event's payload JSON, if present.
func PayloadModel(payload json.RawMessage) string {
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Model
}

// PayloadToolCall extracts tool call info from a tool_call event payload.
func PayloadToolCall(payload json.RawMessage) (name string, input string) {
	var p struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", ""
	}

	inputStr := strings.TrimSpace(string(p.Input))
	if len(inputStr) > 200 {
		inputStr = inputStr[:200] + "..."
	}

	return p.ToolName, inputStr
}

// EditInput holds the parsed fields from an Edit tool call.
type EditInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// PatchInput holds the parsed fields from an apply_patch tool call.
type PatchInput struct {
	FilePath  string `json:"file_path"`
	MoveTo    string `json:"move_to,omitempty"`
	Operation string `json:"operation"`
	Diff      string `json:"diff"`
}

// PayloadEditCall extracts Edit tool input from a tool_call event payload.
// Returns nil if the payload is not an Edit tool call or cannot be parsed.
func PayloadEditCall(payload json.RawMessage) *EditInput {
	var p struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	if p.ToolName != "Edit" {
		return nil
	}
	var ei EditInput
	if err := json.Unmarshal(p.Input, &ei); err != nil {
		return nil
	}
	if ei.OldString == "" && ei.NewString == "" {
		return nil
	}
	return &ei
}

// PayloadPatchCall extracts apply_patch tool input from a tool_call event payload.
// Returns nil if the payload is not an apply_patch tool call or cannot be parsed.
func PayloadPatchCall(payload json.RawMessage) *PatchInput {
	var p struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	if p.ToolName != "apply_patch" && !strings.HasSuffix(p.ToolName, ".apply_patch") {
		return nil
	}
	var pi PatchInput
	if err := json.Unmarshal(p.Input, &pi); err != nil {
		return nil
	}
	if pi.FilePath == "" {
		return nil
	}
	return &pi
}

// WriteInput holds the parsed fields from a Write tool call.
type WriteInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// PayloadWriteCall extracts Write tool input from a tool_call event payload.
// Returns nil if the payload is not a Write tool call or cannot be parsed.
func PayloadWriteCall(payload json.RawMessage) *WriteInput {
	var p struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	if p.ToolName != "Write" {
		return nil
	}
	var wi WriteInput
	if err := json.Unmarshal(p.Input, &wi); err != nil {
		return nil
	}
	return &wi
}

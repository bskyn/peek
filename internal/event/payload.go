package event

import (
	"encoding/json"
	"strings"
)

// Usage captures normalized token accounting plus estimated pricing metadata.
type Usage struct {
	InputTokens         int     `json:"input_tokens,omitempty"`
	OutputTokens        int     `json:"output_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	TotalTokens         int     `json:"total_tokens,omitempty"`
	InputCostUSD        float64 `json:"input_cost_usd,omitempty"`
	OutputCostUSD       float64 `json:"output_cost_usd,omitempty"`
	CacheCreationCost   float64 `json:"cache_creation_cost_usd,omitempty"`
	CacheReadCost       float64 `json:"cache_read_cost_usd,omitempty"`
	TotalCostUSD        float64 `json:"total_cost_usd,omitempty"`
	PricingModel        string  `json:"pricing_model,omitempty"`
}

// Normalized fills derived totals when possible.
func (u Usage) Normalized() Usage {
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens
	}
	if u.TotalCostUSD == 0 {
		u.TotalCostUSD = u.InputCostUSD + u.OutputCostUSD + u.CacheCreationCost + u.CacheReadCost
	}
	return u
}

// HasTokens reports whether any token accounting is present.
func (u Usage) HasTokens() bool {
	return u.InputTokens > 0 || u.OutputTokens > 0 || u.TotalTokens > 0
}

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

// PayloadMessageID extracts the "message_id" field from an event's payload JSON, if present.
func PayloadMessageID(payload json.RawMessage) string {
	var p struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.MessageID
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

// PayloadProgressSubtype extracts the "subtype" field from a progress/system payload.
func PayloadProgressSubtype(payload json.RawMessage) string {
	var p struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.Subtype
}

// PayloadUsage extracts the normalized "usage" field from an event payload.
func PayloadUsage(payload json.RawMessage) (Usage, bool) {
	var p struct {
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.Usage == nil {
		return Usage{}, false
	}
	usage := p.Usage.Normalized()
	if !usage.HasTokens() && usage.TotalCostUSD == 0 {
		return Usage{}, false
	}
	return usage, true
}

// PayloadTokenCountUsage extracts the cumulative token_count payload, if present.
// OpenAI reports cached_input_tokens as a subset of input_tokens. We normalize
// so that InputTokens = non-cached portion and CacheReadTokens = cached portion,
// making the shared Usage/pricing math work for both providers.
func PayloadTokenCountUsage(payload json.RawMessage) (Usage, bool) {
	var p struct {
		Info struct {
			TotalTokenUsage struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				TotalTokens       int `json:"total_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return Usage{}, false
	}
	raw := p.Info.TotalTokenUsage
	nonCached := raw.InputTokens - raw.CachedInputTokens
	if nonCached < 0 {
		nonCached = 0
	}
	usage := Usage{
		InputTokens:     nonCached,
		OutputTokens:    raw.OutputTokens,
		CacheReadTokens: raw.CachedInputTokens,
		TotalTokens:     raw.TotalTokens,
	}
	usage = usage.Normalized()
	if !usage.HasTokens() {
		return Usage{}, false
	}
	return usage, true
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

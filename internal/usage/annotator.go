package usage

import (
	"encoding/json"

	"github.com/bskyn/peek/internal/event"
)

// Annotator enriches event payloads with normalized usage and pricing metadata.
type Annotator struct {
	currentModel       string
	lastCodexCumul     event.Usage
	hasCodexCumulative bool
	// Claude message-level dedup: tracks the last usage snapshot per message ID
	// so repeated assistant JSONL lines for the same API response are counted once.
	lastClaudeMsgUsage map[string]event.Usage
}

// NewAnnotator constructs a fresh event annotator.
func NewAnnotator() *Annotator {
	return &Annotator{
		lastClaudeMsgUsage: make(map[string]event.Usage),
	}
}

// Observe seeds the annotator from already-persisted events.
func (a *Annotator) Observe(events []event.Event) {
	for _, ev := range events {
		if model := event.PayloadModel(ev.PayloadJSON); model != "" {
			a.currentModel = model
		}
		// Track Claude message-level usage for dedup on resume
		if msgID := event.PayloadMessageID(ev.PayloadJSON); msgID != "" {
			if usage, ok := event.PayloadUsage(ev.PayloadJSON); ok {
				a.lastClaudeMsgUsage[msgID] = usage
			}
		}
		if event.PayloadProgressSubtype(ev.PayloadJSON) != "token_count" {
			continue
		}
		if cumulative, ok := event.PayloadTokenCountUsage(ev.PayloadJSON); ok {
			a.lastCodexCumul = cumulative.Normalized()
			a.hasCodexCumulative = true
		}
	}
}

// Annotate returns a copy of the events with usage metadata normalized for display.
func (a *Annotator) Annotate(eventsIn []event.Event) []event.Event {
	if len(eventsIn) == 0 {
		return nil
	}

	eventsOut := make([]event.Event, len(eventsIn))
	copy(eventsOut, eventsIn)
	for i := range eventsOut {
		eventsOut[i] = a.annotateEvent(eventsOut[i])
	}
	return eventsOut
}

func (a *Annotator) annotateEvent(ev event.Event) event.Event {
	payload, ok := decodePayload(ev.PayloadJSON)
	if !ok {
		if model := event.PayloadModel(ev.PayloadJSON); model != "" {
			a.currentModel = model
		}
		return ev
	}

	model := stringValue(payload["model"])
	if model == "" {
		model = a.currentModel
	}

	usageValue, hasUsage := event.PayloadUsage(ev.PayloadJSON)

	// Codex: cumulative token_count → per-turn delta
	if event.PayloadProgressSubtype(ev.PayloadJSON) == "token_count" {
		if cumulative, ok := event.PayloadTokenCountUsage(ev.PayloadJSON); ok {
			payload["cumulative_usage"] = cumulative.Normalized()
			usageValue = cumulativeDelta(cumulative.Normalized(), a.lastCodexCumul, a.hasCodexCumulative)
			payload["usage"] = usageValue
			a.lastCodexCumul = cumulative.Normalized()
			a.hasCodexCumulative = true
			hasUsage = true
		}
	}

	// Claude: deduplicate usage across repeated assistant JSONL lines for the same message.
	// Claude Code writes multiple snapshots per streaming response; each carries the same
	// cumulative usage. We compute the delta from the previous snapshot for this message_id.
	if hasUsage {
		if msgID := stringValue(payload["message_id"]); msgID != "" {
			prev, hasPrev := a.lastClaudeMsgUsage[msgID]
			usageValue = claudeMessageDelta(usageValue, prev, hasPrev)
			payload["usage"] = usageValue
			// Store the raw (pre-delta) usage as the cumulative snapshot
			if rawUsage, rawOK := event.PayloadUsage(ev.PayloadJSON); rawOK {
				a.lastClaudeMsgUsage[msgID] = rawUsage
			}
		}
	}

	if hasUsage {
		if model != "" {
			payload["model"] = model
		}
		payload["usage"] = Estimate(model, usageValue)
		ev.PayloadJSON = mustJSON(payload)
	}

	if model != "" {
		a.currentModel = model
	}
	return ev
}

func claudeMessageDelta(current event.Usage, previous event.Usage, hasPrevious bool) event.Usage {
	if !hasPrevious {
		return current
	}
	return event.Usage{
		InputTokens:         max(current.InputTokens-previous.InputTokens, 0),
		OutputTokens:        max(current.OutputTokens-previous.OutputTokens, 0),
		CacheCreationTokens: max(current.CacheCreationTokens-previous.CacheCreationTokens, 0),
		CacheReadTokens:     max(current.CacheReadTokens-previous.CacheReadTokens, 0),
	}.Normalized()
}

func cumulativeDelta(current event.Usage, previous event.Usage, hasPrevious bool) event.Usage {
	if !hasPrevious {
		return current
	}
	return event.Usage{
		InputTokens:     max(current.InputTokens-previous.InputTokens, 0),
		OutputTokens:    max(current.OutputTokens-previous.OutputTokens, 0),
		CacheReadTokens: max(current.CacheReadTokens-previous.CacheReadTokens, 0),
		TotalTokens:     max(current.TotalTokens-previous.TotalTokens, 0),
	}.Normalized()
}

func decodePayload(raw json.RawMessage) (map[string]any, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func max(value int, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

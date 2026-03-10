package usage

import (
	"fmt"
	"testing"

	"github.com/bskyn/peek/internal/event"
)

func TestEstimateKnownPricing(t *testing.T) {
	usage := Estimate("gpt-5", event.Usage{
		InputTokens:  500,
		OutputTokens: 100,
	})

	if usage.PricingModel != "gpt-5" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	if usage.TotalTokens != 600 {
		t.Fatalf("unexpected total tokens: %d", usage.TotalTokens)
	}
	if usage.InputCostUSD <= 0 || usage.OutputCostUSD <= 0 || usage.TotalCostUSD <= 0 {
		t.Fatalf("expected positive cost values: %+v", usage)
	}
}

func TestEstimateOpus46Pricing(t *testing.T) {
	usage := Estimate("claude-opus-4-6", event.Usage{
		InputTokens:         21664,
		OutputTokens:        49531,
		CacheCreationTokens: 15000,
		CacheReadTokens:     6000,
	})
	if usage.PricingModel != "claude-opus-4-6" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// Opus 4.6: $5/MTok input, $25/MTok output, $6.25/MTok cache write, $0.50/MTok cache read
	expectInput := 21664.0 * 5.0 / 1_000_000
	expectOutput := 49531.0 * 25.0 / 1_000_000
	expectCacheWrite := 15000.0 * 6.25 / 1_000_000
	expectCacheRead := 6000.0 * 0.50 / 1_000_000
	expectTotal := expectInput + expectOutput + expectCacheWrite + expectCacheRead

	if diff := usage.InputCostUSD - expectInput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("input cost: got %f, want %f", usage.InputCostUSD, expectInput)
	}
	if diff := usage.OutputCostUSD - expectOutput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("output cost: got %f, want %f", usage.OutputCostUSD, expectOutput)
	}
	if diff := usage.CacheCreationCost - expectCacheWrite; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("cache write cost: got %f, want %f", usage.CacheCreationCost, expectCacheWrite)
	}
	if diff := usage.CacheReadCost - expectCacheRead; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("cache read cost: got %f, want %f", usage.CacheReadCost, expectCacheRead)
	}
	if diff := usage.TotalCostUSD - expectTotal; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("total cost: got %f, want %f", usage.TotalCostUSD, expectTotal)
	}
}

func TestEstimatePricingByFamily(t *testing.T) {
	usage := Estimate("claude-sonnet-4-6-20260301", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "claude-sonnet-4-6" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
}

func TestEstimateGPT54Pricing(t *testing.T) {
	usage := Estimate("gpt-5.4", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "gpt-5.4" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// GPT-5.4: $2.50/MTok input, $15.00/MTok output
	expectInput := 1000.0 * 2.50 / 1_000_000
	expectOutput := 200.0 * 15.0 / 1_000_000
	expectTotal := expectInput + expectOutput
	if diff := usage.InputCostUSD - expectInput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("input cost: got %f, want %f", usage.InputCostUSD, expectInput)
	}
	if diff := usage.TotalCostUSD - expectTotal; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("total cost: got %f, want %f", usage.TotalCostUSD, expectTotal)
	}
}

func TestEstimateGPT52Pricing(t *testing.T) {
	usage := Estimate("gpt-5.2-20260301", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "gpt-5.2" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// GPT-5.2: $1.75/MTok input, $14.00/MTok output
	expectInput := 1000.0 * 1.75 / 1_000_000
	expectOutput := 200.0 * 14.0 / 1_000_000
	if diff := usage.InputCostUSD - expectInput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("input cost: got %f, want %f", usage.InputCostUSD, expectInput)
	}
	if diff := usage.OutputCostUSD - expectOutput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("output cost: got %f, want %f", usage.OutputCostUSD, expectOutput)
	}
}

func TestEstimateO3Pricing(t *testing.T) {
	usage := Estimate("o3", event.Usage{
		InputTokens:     1000,
		OutputTokens:    200,
		CacheReadTokens: 500,
	})
	if usage.PricingModel != "o3" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// o3: $2.00/MTok input, $8.00/MTok output, $0.50/MTok cached input
	expectInput := 1000.0 * 2.0 / 1_000_000
	expectOutput := 200.0 * 8.0 / 1_000_000
	expectCache := 500.0 * 0.50 / 1_000_000
	expectTotal := expectInput + expectOutput + expectCache
	if diff := usage.TotalCostUSD - expectTotal; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("total cost: got %f, want %f", usage.TotalCostUSD, expectTotal)
	}
}

func TestEstimateO4MiniPricing(t *testing.T) {
	usage := Estimate("o4-mini-2026", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "o4-mini" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
}

func TestEstimateOpus41Pricing(t *testing.T) {
	usage := Estimate("claude-opus-4-1-20250805", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "claude-opus-4-1" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// Opus 4.1 uses legacy $15/$75 pricing
	if usage.InputCostUSD != 1000.0*15.0/1_000_000 {
		t.Fatalf("unexpected input cost: %f", usage.InputCostUSD)
	}
}

func TestEstimateHaiku45Pricing(t *testing.T) {
	usage := Estimate("claude-haiku-4-5-20251001", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "claude-haiku-4-5" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
}

func TestAnnotateCodexTokenCountDelta(t *testing.T) {
	annotator := NewAnnotator()

	first := annotator.Annotate([]event.Event{makeUsageEvent(500, 100)})[0]
	firstUsage, ok := event.PayloadUsage(first.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on first event")
	}
	if firstUsage.TotalTokens != 600 {
		t.Fatalf("unexpected first total tokens: %+v", firstUsage)
	}

	second := annotator.Annotate([]event.Event{makeUsageEvent(750, 150)})[0]
	secondUsage, ok := event.PayloadUsage(second.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on second event")
	}
	if secondUsage.InputTokens != 250 || secondUsage.OutputTokens != 50 || secondUsage.TotalTokens != 300 {
		t.Fatalf("unexpected delta usage: %+v", secondUsage)
	}
}

func TestAnnotateClaudeMessageDedup(t *testing.T) {
	annotator := NewAnnotator()

	// Simulate 3 assistant JSONL lines for the same message ID (streaming snapshots)
	makeClaudeEvent := func(msgID string, input, output, cacheCreate, cacheRead int) event.Event {
		return event.Event{
			Type: event.EventAssistantMessage,
			PayloadJSON: []byte(fmt.Sprintf(
				`{"model":"claude-opus-4-6","message_id":"%s","usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_tokens":%d,"cache_read_tokens":%d}}`,
				msgID, input, output, cacheCreate, cacheRead,
			)),
		}
	}

	// First snapshot — full usage counted
	first := annotator.Annotate([]event.Event{makeClaudeEvent("msg_abc", 3, 9, 15000, 6000)})[0]
	firstUsage, ok := event.PayloadUsage(first.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on first event")
	}
	if firstUsage.InputTokens != 3 || firstUsage.OutputTokens != 9 {
		t.Fatalf("unexpected first usage: %+v", firstUsage)
	}
	if firstUsage.CacheCreationTokens != 15000 || firstUsage.CacheReadTokens != 6000 {
		t.Fatalf("unexpected first cache tokens: %+v", firstUsage)
	}

	// Second snapshot — same message, same values → delta is zero, no usage attached
	second := annotator.Annotate([]event.Event{makeClaudeEvent("msg_abc", 3, 9, 15000, 6000)})[0]
	_, hasSecondUsage := event.PayloadUsage(second.PayloadJSON)
	if hasSecondUsage {
		t.Fatal("expected no usage on duplicate snapshot (zero delta)")
	}

	// Third snapshot — output grew (final response)
	third := annotator.Annotate([]event.Event{makeClaudeEvent("msg_abc", 3, 480, 15000, 6000)})[0]
	thirdUsage, ok := event.PayloadUsage(third.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on third event")
	}
	if thirdUsage.OutputTokens != 471 {
		t.Fatalf("expected output delta 471, got: %+v", thirdUsage)
	}
	if thirdUsage.InputTokens != 0 {
		t.Fatalf("expected input delta 0 for repeated snapshot: %+v", thirdUsage)
	}

	// Different message ID — should get full count
	fourth := annotator.Annotate([]event.Event{makeClaudeEvent("msg_def", 5, 100, 8000, 3000)})[0]
	fourthUsage, ok := event.PayloadUsage(fourth.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on fourth event")
	}
	if fourthUsage.InputTokens != 5 || fourthUsage.OutputTokens != 100 {
		t.Fatalf("unexpected fourth usage for new message: %+v", fourthUsage)
	}
}

func TestAnnotateCodexCachedTokenDelta(t *testing.T) {
	annotator := NewAnnotator()

	// Simulate real Codex token_count with cached_input_tokens (OpenAI format).
	// cached_input_tokens is a subset of input_tokens.
	first := annotator.Annotate([]event.Event{makeUsageEventWithCache(19020, 6528, 319)})[0]
	firstUsage, ok := event.PayloadUsage(first.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on first event")
	}
	// After normalization: InputTokens = 19020 - 6528 = 12492, CacheReadTokens = 6528
	if firstUsage.InputTokens != 12492 {
		t.Fatalf("expected non-cached input 12492, got %d", firstUsage.InputTokens)
	}
	if firstUsage.CacheReadTokens != 6528 {
		t.Fatalf("expected cache read 6528, got %d", firstUsage.CacheReadTokens)
	}
	if firstUsage.OutputTokens != 319 {
		t.Fatalf("expected output 319, got %d", firstUsage.OutputTokens)
	}

	// Second cumulative event — delta should be computed correctly
	second := annotator.Annotate([]event.Event{makeUsageEventWithCache(40797, 25856, 699)})[0]
	secondUsage, ok := event.PayloadUsage(second.PayloadJSON)
	if !ok {
		t.Fatal("expected usage on second event")
	}
	// Cumulative non-cached: 40797-25856=14941, delta: 14941-12492=2449
	// Cumulative cache: 25856, delta: 25856-6528=19328
	if secondUsage.InputTokens != 2449 {
		t.Fatalf("expected input delta 2449, got %d", secondUsage.InputTokens)
	}
	if secondUsage.CacheReadTokens != 19328 {
		t.Fatalf("expected cache delta 19328, got %d", secondUsage.CacheReadTokens)
	}
	if secondUsage.OutputTokens != 380 {
		t.Fatalf("expected output delta 380, got %d", secondUsage.OutputTokens)
	}
}

func TestEstimateGPT5CachePricing(t *testing.T) {
	// Simulate normalized Codex usage: InputTokens is non-cached, CacheReadTokens is cached
	usage := Estimate("gpt-5", event.Usage{
		InputTokens:     12492,
		OutputTokens:    319,
		CacheReadTokens: 6528,
	})
	if usage.PricingModel != "gpt-5" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	// GPT-5: $1.25/MTok input, $10.00/MTok output, $0.125/MTok cached input
	expectInput := 12492.0 * 1.25 / 1_000_000
	expectOutput := 319.0 * 10.0 / 1_000_000
	expectCache := 6528.0 * 0.125 / 1_000_000
	expectTotal := expectInput + expectOutput + expectCache
	if diff := usage.InputCostUSD - expectInput; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("input cost: got %f, want %f", usage.InputCostUSD, expectInput)
	}
	if diff := usage.CacheReadCost - expectCache; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("cache read cost: got %f, want %f", usage.CacheReadCost, expectCache)
	}
	if diff := usage.TotalCostUSD - expectTotal; diff > 0.0001 || diff < -0.0001 {
		t.Fatalf("total cost: got %f, want %f", usage.TotalCostUSD, expectTotal)
	}
}

func makeUsageEventWithCache(inputTokens, cachedInputTokens, outputTokens int) event.Event {
	return event.Event{
		Type: event.EventProgress,
		PayloadJSON: []byte(fmt.Sprintf(
			`{"subtype":"token_count","model":"gpt-5.4","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}`,
			inputTokens,
			cachedInputTokens,
			outputTokens,
			inputTokens+outputTokens,
		)),
	}
}

func makeUsageEvent(inputTokens, outputTokens int) event.Event {
	return event.Event{
		Type: event.EventProgress,
		PayloadJSON: []byte(fmt.Sprintf(
			`{"subtype":"token_count","model":"gpt-5","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}},"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}`,
			inputTokens,
			outputTokens,
			inputTokens+outputTokens,
			inputTokens,
			outputTokens,
			inputTokens+outputTokens,
		)),
	}
}

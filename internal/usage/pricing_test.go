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

func TestEstimatePricingByFamily(t *testing.T) {
	usage := Estimate("claude-sonnet-4-6-20260301", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "claude-sonnet-4" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
}

func TestEstimatePricingByDotVersion(t *testing.T) {
	usage := Estimate("gpt-5.4", event.Usage{
		InputTokens:  1000,
		OutputTokens: 200,
	})
	if usage.PricingModel != "gpt-5" {
		t.Fatalf("unexpected pricing model: %q", usage.PricingModel)
	}
	if usage.TotalCostUSD <= 0 {
		t.Fatalf("expected positive total cost: %+v", usage)
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

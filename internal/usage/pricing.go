package usage

import (
	"strings"

	"github.com/bskyn/peek/internal/event"
)

const oneMillion = 1_000_000

type pricing struct {
	InputUSDPerMillion  float64
	OutputUSDPerMillion float64
}

var exactPricing = map[string]pricing{
	// OpenAI API pricing: https://platform.openai.com/docs/pricing
	"gpt-5":      {InputUSDPerMillion: 1.25, OutputUSDPerMillion: 10.0},
	"gpt-5-mini": {InputUSDPerMillion: 0.25, OutputUSDPerMillion: 2.0},
	"gpt-5-nano": {InputUSDPerMillion: 0.05, OutputUSDPerMillion: 0.4},

	// Anthropic API pricing: https://www.anthropic.com/pricing
	"claude-opus-4":   {InputUSDPerMillion: 15.0, OutputUSDPerMillion: 75.0},
	"claude-sonnet-4": {InputUSDPerMillion: 3.0, OutputUSDPerMillion: 15.0},
}

// Estimate attaches cost fields when pricing for the given model is known.
func Estimate(model string, usage event.Usage) event.Usage {
	usage = usage.Normalized()
	if !usage.HasTokens() {
		return usage
	}

	pricingModel, rate, ok := lookupPricing(model)
	if !ok {
		return usage
	}

	usage.PricingModel = pricingModel
	usage.InputCostUSD = float64(usage.InputTokens) * rate.InputUSDPerMillion / oneMillion
	usage.OutputCostUSD = float64(usage.OutputTokens) * rate.OutputUSDPerMillion / oneMillion
	usage.TotalCostUSD = usage.InputCostUSD + usage.OutputCostUSD
	return usage
}

func lookupPricing(model string) (string, pricing, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return "", pricing{}, false
	}
	if rate, ok := exactPricing[normalized]; ok {
		return normalized, rate, true
	}

	switch {
	case hasModelPrefix(normalized, "gpt-5-nano"):
		return "gpt-5-nano", exactPricing["gpt-5-nano"], true
	case hasModelPrefix(normalized, "gpt-5-mini"):
		return "gpt-5-mini", exactPricing["gpt-5-mini"], true
	case hasModelPrefix(normalized, "gpt-5"):
		return "gpt-5", exactPricing["gpt-5"], true
	case strings.HasPrefix(normalized, "claude-opus-4-"):
		return "claude-opus-4", exactPricing["claude-opus-4"], true
	case strings.HasPrefix(normalized, "claude-opus-4."):
		return "claude-opus-4", exactPricing["claude-opus-4"], true
	case strings.HasPrefix(normalized, "claude-sonnet-4-"):
		return "claude-sonnet-4", exactPricing["claude-sonnet-4"], true
	case strings.HasPrefix(normalized, "claude-sonnet-4."):
		return "claude-sonnet-4", exactPricing["claude-sonnet-4"], true
	default:
		return "", pricing{}, false
	}
}

func hasModelPrefix(model string, prefix string) bool {
	return model == prefix ||
		strings.HasPrefix(model, prefix+"-") ||
		strings.HasPrefix(model, prefix+".")
}

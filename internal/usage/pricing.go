package usage

import (
	"strings"

	"github.com/bskyn/peek/internal/event"
)

const oneMillion = 1_000_000

type pricing struct {
	InputUSDPerMillion      float64
	OutputUSDPerMillion     float64
	CacheWriteUSDPerMillion float64
	CacheReadUSDPerMillion  float64
}

var exactPricing = map[string]pricing{
	// OpenAI API pricing: https://platform.openai.com/docs/pricing
	"gpt-5":      {InputUSDPerMillion: 1.25, OutputUSDPerMillion: 10.0},
	"gpt-5-mini": {InputUSDPerMillion: 0.25, OutputUSDPerMillion: 2.0},
	"gpt-5-nano": {InputUSDPerMillion: 0.05, OutputUSDPerMillion: 0.4},

	// Anthropic API pricing: https://platform.claude.com/docs/en/about-claude/pricing
	"claude-opus-4-6":   {InputUSDPerMillion: 5.0, OutputUSDPerMillion: 25.0, CacheWriteUSDPerMillion: 6.25, CacheReadUSDPerMillion: 0.50},
	"claude-opus-4-5":   {InputUSDPerMillion: 5.0, OutputUSDPerMillion: 25.0, CacheWriteUSDPerMillion: 6.25, CacheReadUSDPerMillion: 0.50},
	"claude-opus-4-1":   {InputUSDPerMillion: 15.0, OutputUSDPerMillion: 75.0, CacheWriteUSDPerMillion: 18.75, CacheReadUSDPerMillion: 1.50},
	"claude-opus-4":     {InputUSDPerMillion: 15.0, OutputUSDPerMillion: 75.0, CacheWriteUSDPerMillion: 18.75, CacheReadUSDPerMillion: 1.50},
	"claude-sonnet-4-6": {InputUSDPerMillion: 3.0, OutputUSDPerMillion: 15.0, CacheWriteUSDPerMillion: 3.75, CacheReadUSDPerMillion: 0.30},
	"claude-sonnet-4-5": {InputUSDPerMillion: 3.0, OutputUSDPerMillion: 15.0, CacheWriteUSDPerMillion: 3.75, CacheReadUSDPerMillion: 0.30},
	"claude-sonnet-4":   {InputUSDPerMillion: 3.0, OutputUSDPerMillion: 15.0, CacheWriteUSDPerMillion: 3.75, CacheReadUSDPerMillion: 0.30},
	"claude-haiku-4-5":  {InputUSDPerMillion: 1.0, OutputUSDPerMillion: 5.0, CacheWriteUSDPerMillion: 1.25, CacheReadUSDPerMillion: 0.10},
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
	if rate.CacheWriteUSDPerMillion > 0 {
		usage.CacheCreationCost = float64(usage.CacheCreationTokens) * rate.CacheWriteUSDPerMillion / oneMillion
	}
	if rate.CacheReadUSDPerMillion > 0 {
		usage.CacheReadCost = float64(usage.CacheReadTokens) * rate.CacheReadUSDPerMillion / oneMillion
	}
	usage.TotalCostUSD = usage.InputCostUSD + usage.OutputCostUSD + usage.CacheCreationCost + usage.CacheReadCost
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
	// Claude: match specific version families before falling back to base
	case hasModelPrefix(normalized, "claude-opus-4-6"):
		return "claude-opus-4-6", exactPricing["claude-opus-4-6"], true
	case hasModelPrefix(normalized, "claude-opus-4-5"):
		return "claude-opus-4-5", exactPricing["claude-opus-4-5"], true
	case hasModelPrefix(normalized, "claude-opus-4-1"):
		return "claude-opus-4-1", exactPricing["claude-opus-4-1"], true
	case hasModelPrefix(normalized, "claude-opus-4"):
		return "claude-opus-4", exactPricing["claude-opus-4"], true
	case hasModelPrefix(normalized, "claude-sonnet-4-6"):
		return "claude-sonnet-4-6", exactPricing["claude-sonnet-4-6"], true
	case hasModelPrefix(normalized, "claude-sonnet-4-5"):
		return "claude-sonnet-4-5", exactPricing["claude-sonnet-4-5"], true
	case hasModelPrefix(normalized, "claude-sonnet-4"):
		return "claude-sonnet-4", exactPricing["claude-sonnet-4"], true
	case hasModelPrefix(normalized, "claude-haiku-4-5"):
		return "claude-haiku-4-5", exactPricing["claude-haiku-4-5"], true
	default:
		return "", pricing{}, false
	}
}

func hasModelPrefix(model string, prefix string) bool {
	return model == prefix ||
		strings.HasPrefix(model, prefix+"-") ||
		strings.HasPrefix(model, prefix+".")
}

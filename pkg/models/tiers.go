// Package models provides model tier management for the Sympozium GCP platform.
// It maps human-friendly tier names to specific Gemini model identifiers and
// provides helpers for model selection, capability checking, and cost estimation.
package models

import (
	"fmt"
	"strings"
)

// Tier represents a model performance tier.
type Tier string

const (
	// TierFast uses Gemini Flash for quick, low-cost responses.
	TierFast Tier = "fast"

	// TierBalanced uses Gemini Pro (default) for quality + speed.
	TierBalanced Tier = "balanced"

	// TierPowerful uses Gemini Pro for complex reasoning tasks.
	TierPowerful Tier = "powerful"

	// TierLocal uses self-hosted Gemma models for on-premises deployment.
	TierLocal Tier = "local"

	// TierCustom uses a user-specified model.
	TierCustom Tier = "custom"
)

// ModelInfo contains metadata about a Gemini model.
type ModelInfo struct {
	// Tier is the performance tier.
	Tier Tier `json:"tier" yaml:"tier"`

	// ModelID is the Vertex AI model identifier.
	ModelID string `json:"modelId" yaml:"modelId"`

	// DisplayName is the human-readable name.
	DisplayName string `json:"displayName" yaml:"displayName"`

	// Description explains when to use this model.
	Description string `json:"description" yaml:"description"`

	// MaxInputTokens is the maximum input context window.
	MaxInputTokens int `json:"maxInputTokens" yaml:"maxInputTokens"`

	// MaxOutputTokens is the maximum output tokens.
	MaxOutputTokens int `json:"maxOutputTokens" yaml:"maxOutputTokens"`

	// SupportsTools indicates if function calling is supported.
	SupportsTools bool `json:"supportsTools" yaml:"supportsTools"`

	// SupportsThinking indicates if extended thinking mode is available.
	SupportsThinking bool `json:"supportsThinking" yaml:"supportsThinking"`

	// CostPer1MInput is the approximate cost per 1M input tokens (USD).
	CostPer1MInput float64 `json:"costPer1MInput" yaml:"costPer1MInput"`

	// CostPer1MOutput is the approximate cost per 1M output tokens (USD).
	CostPer1MOutput float64 `json:"costPer1MOutput" yaml:"costPer1MOutput"`
}

// DefaultModels contains the built-in model definitions.
var DefaultModels = map[Tier]ModelInfo{
	TierFast: {
		Tier:             TierFast,
		ModelID:          "gemini-2.5-flash",
		DisplayName:      "Gemini 2.5 Flash",
		Description:      "Fast and cost-effective for routine tasks, monitoring, and simple queries.",
		MaxInputTokens:   1048576,
		MaxOutputTokens:  8192,
		SupportsTools:    true,
		SupportsThinking: false,
		CostPer1MInput:   0.15,
		CostPer1MOutput:  0.60,
	},
	TierBalanced: {
		Tier:             TierBalanced,
		ModelID:          "gemini-2.5-pro",
		DisplayName:      "Gemini 2.5 Pro",
		Description:      "Pro model as default for quality reasoning and balanced performance.",
		MaxInputTokens:   1048576,
		MaxOutputTokens:  65536,
		SupportsTools:    true,
		SupportsThinking: true,
		CostPer1MInput:   1.25,
		CostPer1MOutput:  10.00,
	},
	TierPowerful: {
		Tier:             TierPowerful,
		ModelID:          "gemini-3.1-pro-preview",
		DisplayName:      "Gemini 3.1 Pro (Preview)",
		Description:      "Most capable model for complex reasoning, code review, and incident response.",
		MaxInputTokens:   1048576,
		MaxOutputTokens:  65536,
		SupportsTools:    true,
		SupportsThinking: true,
		CostPer1MInput:   1.25,
		CostPer1MOutput:  10.00,
	},
}

// GemmaModels contains the available self-hosted Gemma models for GKE GPU/TPU nodes.
var GemmaModels = []ModelInfo{
	{
		Tier:            TierLocal,
		ModelID:         "gemma-3-27b-it",
		DisplayName:     "Gemma 3 27B Instruct",
		Description:     "Most capable Gemma model. Runs on a single GPU (A100/H100) or TPU v5e.",
		MaxInputTokens:  128000,
		MaxOutputTokens: 8192,
		SupportsTools:   true,
		SupportsThinking: false,
		CostPer1MInput:  0.0,
		CostPer1MOutput: 0.0,
	},
	{
		Tier:            TierLocal,
		ModelID:         "gemma-3-12b-it",
		DisplayName:     "Gemma 3 12B Instruct",
		Description:     "Balanced Gemma model. Runs on L4 GPU or TPU v5e.",
		MaxInputTokens:  128000,
		MaxOutputTokens: 8192,
		SupportsTools:   true,
		SupportsThinking: false,
		CostPer1MInput:  0.0,
		CostPer1MOutput: 0.0,
	},
	{
		Tier:            TierLocal,
		ModelID:         "gemma-3-4b-it",
		DisplayName:     "Gemma 3 4B Instruct",
		Description:     "Lightweight Gemma model. Runs on T4 GPU or TPU v5e.",
		MaxInputTokens:  128000,
		MaxOutputTokens: 8192,
		SupportsTools:   false,
		SupportsThinking: false,
		CostPer1MInput:  0.0,
		CostPer1MOutput: 0.0,
	},
	{
		Tier:            TierLocal,
		ModelID:         "gemma-3-1b-it",
		DisplayName:     "Gemma 3 1B Instruct",
		Description:     "Ultra-lightweight Gemma for edge and monitoring. Runs on any GPU or TPU.",
		MaxInputTokens:  32000,
		MaxOutputTokens: 4096,
		SupportsTools:   false,
		SupportsThinking: false,
		CostPer1MInput:  0.0,
		CostPer1MOutput: 0.0,
	},
}

// ResolveTier converts a tier name or model ID to a ModelInfo.
// It handles: "fast", "balanced", "powerful", "local", or any direct model ID.
func ResolveTier(tierOrModel string) ModelInfo {
	tierOrModel = strings.ToLower(strings.TrimSpace(tierOrModel))

	// Check direct tier names
	switch Tier(tierOrModel) {
	case TierFast:
		return DefaultModels[TierFast]
	case TierBalanced:
		return DefaultModels[TierBalanced]
	case TierPowerful:
		return DefaultModels[TierPowerful]
	}

	// Check if it matches a known model ID
	for _, m := range DefaultModels {
		if strings.EqualFold(m.ModelID, tierOrModel) {
			return m
		}
	}

	// Check if it matches a Gemma model ID
	for _, m := range GemmaModels {
		if strings.EqualFold(m.ModelID, tierOrModel) {
			return m
		}
	}

	// Custom model — return with defaults
	return ModelInfo{
		Tier:             TierCustom,
		ModelID:          tierOrModel,
		DisplayName:      tierOrModel,
		Description:      "Custom model",
		MaxInputTokens:   1000000,
		MaxOutputTokens:  8192,
		SupportsTools:    true,
		SupportsThinking: false,
	}
}

// ResolveModelID converts a tier name to a concrete model ID.
// Pass-through for direct model IDs.
func ResolveModelID(tierOrModel string) string {
	return ResolveTier(tierOrModel).ModelID
}

// TierForPersona suggests the appropriate tier based on persona role.
func TierForPersona(personaName string) Tier {
	name := strings.ToLower(personaName)

	// Powerful tier for complex roles
	powerfulRoles := []string{"tech-lead", "architect", "reviewer", "incident", "security"}
	for _, role := range powerfulRoles {
		if strings.Contains(name, role) {
			return TierPowerful
		}
	}

	// Fast tier for routine roles
	fastRoles := []string{"monitor", "health", "sweep", "cleaner", "heartbeat"}
	for _, role := range fastRoles {
		if strings.Contains(name, role) {
			return TierFast
		}
	}

	// Default to balanced (Pro is the default)
	return TierBalanced
}

// EstimateCost estimates the cost of a request given input/output tokens.
func (m ModelInfo) EstimateCost(inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1_000_000 * m.CostPer1MInput
	outputCost := float64(outputTokens) / 1_000_000 * m.CostPer1MOutput
	return inputCost + outputCost
}

// FormatTierTable returns a formatted table of available tiers.
func FormatTierTable() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-12s %-25s %-10s %s\n", "TIER", "MODEL", "TOOLS", "DESCRIPTION"))
	sb.WriteString(strings.Repeat("-", 80) + "\n")
	for _, tier := range []Tier{TierFast, TierBalanced, TierPowerful} {
		m := DefaultModels[tier]
		tools := "Yes"
		if !m.SupportsTools {
			tools = "No"
		}
		sb.WriteString(fmt.Sprintf("%-12s %-25s %-10s %s\n", tier, m.ModelID, tools, m.Description))
	}
	sb.WriteString("\n" + fmt.Sprintf("%-12s %-25s %-10s %s\n", "LOCAL (GEMMA)", "", "", ""))
	sb.WriteString(strings.Repeat("-", 80) + "\n")
	for _, m := range GemmaModels {
		tools := "Yes"
		if !m.SupportsTools {
			tools = "No"
		}
		sb.WriteString(fmt.Sprintf("%-12s %-25s %-10s %s\n", "local", m.ModelID, tools, m.Description))
	}
	return sb.String()
}

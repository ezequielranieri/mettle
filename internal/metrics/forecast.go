// Package metrics - Cost Forecast
//
// Estimates cost before running scenarios. Useful for:
// - Budget planning before large eval suites
// - Quick sanity check on model costs
// - Avoid surprise bills from runaway token usage
//
// The estimate is based on:
// - System prompt length (from spec)
// - Input size (from scenario)
// - Historical token ratios from past runs
// - Model pricing rates
package metrics

import (
	"fmt"
	"math"
	"strings"

	"mettle/internal/spec"
)

// ForecastInput contains everything needed to estimate cost before running.
type ForecastInput struct {
	Suite       spec.EvalSuite
	MaxSteps    int    // max LLM steps per run
	Scenario    string // --scenario filter (empty = all)
	Config      string // --config filter (empty = all)
	JudgeModel  string // override judge model (empty = use suite default)
	AgentModel  string // override agent model (empty = use suite default)
}

// ForecastResult is the estimated cost breakdown.
type ForecastResult struct {
	TotalRuns     int            `json:"total_runs"`
	Scenarios     int            `json:"scenarios"`
	Configs       int            `json:"configs"`
	AgentModel    string         `json:"agent_model"`
	JudgeModel    string         `json:"judge_model"`
	EstInputTk    int            `json:"est_input_tokens"`
	EstOutputTk   int            `json:"est_output_tokens"`
	EstCostUSD    float64        `json:"est_cost_usd"`
	EstLatencyMS  int64          `json:"est_latency_ms"`
	Breakdown     []RunForecast  `json:"breakdown"`
}

// RunForecast is the estimate for one scenario+config combination.
type RunForecast struct {
	Scenario    string  `json:"scenario"`
	Config      string  `json:"config"`
	EstInputTk  int     `json:"est_input_tokens"`
	EstOutputTk int     `json:"est_output_tokens"`
	EstCostUSD  float64 `json:"est_cost_usd"`
}

// EstimateTokensFromPrompt estimates tokens from a text string.
// Rule of thumb: 1 token ≈ 4 characters (English) or ~2 characters (CJK).
// We use 4 chars/token as baseline since specs are mostly English.
func EstimateTokensFromPrompt(text string) int {
	if len(text) == 0 {
		return 0
	}
	// Split by words first for better accuracy on English text
	words := len(strings.Fields(text))
	chars := len(text)
	// Use the more conservative estimate (lower of word*1.3 or chars/4)
	wordTokens := float64(words) * 1.3
	charTokens := float64(chars) / 4.0
	return int(math.Min(wordTokens, charTokens))
}

// Forecast estimates cost for an eval suite without running it.
func Forecast(in ForecastInput) ForecastResult {
	// Filter scenarios
	scenarios := in.Suite.Scenarios
	if in.Scenario != "" {
		for _, s := range in.Suite.Scenarios {
			if s.Name == in.Scenario {
				scenarios = []spec.Scenario{s}
				break
			}
		}
	}

	// Filter configs
	configs := in.Suite.Configs
	if in.Config != "" {
		for _, c := range in.Suite.Configs {
			if c.Name == in.Config {
				configs = []spec.RunConfig{c}
				break
			}
		}
	}
	// If no configs defined, create a default one
	if len(configs) == 0 {
		configs = []spec.RunConfig{{Name: "default"}}
	}

	// Resolve models
	agentModel := in.AgentModel
	if agentModel == "" {
		agentModel = in.Suite.Defaults.Agent.Model
	}
	judgeModel := in.JudgeModel
	if judgeModel == "" {
		judgeModel = in.Suite.Defaults.Judge.Model
	}

	// Estimate system prompt tokens
	systemPromptTokens := EstimateTokensFromPrompt(in.Suite.Defaults.Agent.SystemPrompt)

	// Estimate max input tokens per run (scenario input + system prompt)
	avgInputTokens := systemPromptTokens + 150 // base overhead for tool definitions, etc.

	// Estimate output tokens based on max_steps
	// Each step typically produces 50-200 tokens of output
	avgOutputTokens := in.MaxSteps * 120

	// Estimate judge tokens (if using LLM judge)
	judgeInputTokens := 0
	if judgeModel != "" && judgeModel != "none" {
		// Judge prompt is typically 2-3x the system prompt
		judgeInputTokens = systemPromptTokens * 2
	}

	// Build breakdown
	totalRuns := len(scenarios) * len(configs)
	breakdown := make([]RunForecast, 0, totalRuns)

	var totalInputTk, totalOutputTk int
	var totalCost float64

	for _, sc := range scenarios {
		for _, cfg := range configs {
			// Scenario-specific input adjustment
			scInputTokens := EstimateTokensFromPrompt(fmt.Sprintf("%v", sc.Input))

			inputTk := avgInputTokens + scInputTokens + judgeInputTokens
			outputTk := avgOutputTokens

			cost := estimateCost(inputTk, outputTk, agentModel)
			if judgeModel != "" && judgeModel != "none" {
				// Judge runs after agent, so judge output is input to judge
				cost += estimateCost(judgeInputTokens, 100, judgeModel)
			}

			breakdown = append(breakdown, RunForecast{
				Scenario:    sc.Name,
				Config:      cfg.Name,
				EstInputTk:  inputTk,
				EstOutputTk: outputTk,
				EstCostUSD:  cost,
			})

			totalInputTk += inputTk
			totalOutputTk += outputTk
			totalCost += cost
		}
	}

	// Estimate total latency
	// Each step: ~500-2000ms depending on model
	estLatencyPerRun := int64(in.MaxSteps) * 1000 // 1s per step average
	estTotalLatency := estLatencyPerRun * int64(totalRuns)

	return ForecastResult{
		TotalRuns:    totalRuns,
		Scenarios:    len(scenarios),
		Configs:      len(configs),
		AgentModel:   agentModel,
		JudgeModel:   judgeModel,
		EstInputTk:   totalInputTk,
		EstOutputTk:  totalOutputTk,
		EstCostUSD:   totalCost,
		EstLatencyMS: estTotalLatency,
		Breakdown:    breakdown,
	}
}

// FormatForecast returns a human-readable forecast summary.
func FormatForecast(f ForecastResult) string {
	var sb strings.Builder

	sb.WriteString("Cost Forecast\n")
	sb.WriteString(strings.Repeat("=", 40) + "\n")
	sb.WriteString(fmt.Sprintf("Scenarios:     %d\n", f.Scenarios))
	sb.WriteString(fmt.Sprintf("Configs:       %d\n", f.Configs))
	sb.WriteString(fmt.Sprintf("Total runs:    %d\n", f.TotalRuns))
	sb.WriteString(fmt.Sprintf("Agent model:   %s\n", f.AgentModel))
	sb.WriteString(fmt.Sprintf("Judge model:   %s\n", f.JudgeModel))
	sb.WriteString(strings.Repeat("-", 40) + "\n")
	sb.WriteString(fmt.Sprintf("Est. input:    %s tokens\n", formatNumber(f.EstInputTk)))
	sb.WriteString(fmt.Sprintf("Est. output:   %s tokens\n", formatNumber(f.EstOutputTk)))
	sb.WriteString(fmt.Sprintf("Est. cost:     $%.6f\n", f.EstCostUSD))
	sb.WriteString(fmt.Sprintf("Est. latency:  %s\n", formatDuration(f.EstLatencyMS)))
	sb.WriteString(strings.Repeat("-", 40) + "\n")

	if len(f.Breakdown) > 0 {
		sb.WriteString("Breakdown:\n")
		for _, r := range f.Breakdown {
			sb.WriteString(fmt.Sprintf("  %-20s %-15s $%.6f\n", r.Scenario, r.Config, r.EstCostUSD))
		}
	}

	return sb.String()
}

func formatNumber(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(ms int64) string {
	if ms >= 3600000 {
		return fmt.Sprintf("%.1fh", float64(ms)/3600000)
	}
	if ms >= 60000 {
		return fmt.Sprintf("%.1fm", float64(ms)/60000)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

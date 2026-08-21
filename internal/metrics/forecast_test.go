package metrics

import (
	"strings"
	"testing"

	"mettle/internal/spec"
)

func testSuite() spec.EvalSuite {
	return spec.EvalSuite{
		Name: "test-suite",
		Defaults: spec.Defaults{
			Agent: spec.AgentConfig{
				SystemPrompt: "You are a helpful assistant.",
				Model:        "llama-3.3-70b-versatile",
			},
			Judge: spec.JudgeConfig{
				Provider: "groq",
				Model:    "llama-3.3-70b-versatile",
			},
		},
		Scenarios: []spec.Scenario{
			{Name: "scenario-a", Input: map[string]any{"message": "What is 2+2?"}},
			{Name: "scenario-b", Input: map[string]any{"message": "Describe the capital of France."}},
		},
		Configs: []spec.RunConfig{
			{Name: "tools-3"},
			{Name: "tools-6"},
		},
	}
}

func TestForecastBasic(t *testing.T) {
	suite := testSuite()
	result := Forecast(ForecastInput{
		Suite:    suite,
		MaxSteps: 8,
	})

	if result.Scenarios != 2 {
		t.Errorf("Scenarios = %d, want 2", result.Scenarios)
	}
	if result.Configs != 2 {
		t.Errorf("Configs = %d, want 2", result.Configs)
	}
	if result.TotalRuns != 4 {
		t.Errorf("TotalRuns = %d, want 4", result.TotalRuns)
	}
	if result.AgentModel != "llama-3.3-70b-versatile" {
		t.Errorf("AgentModel = %q, want llama-3.3-70b-versatile", result.AgentModel)
	}
	if result.JudgeModel != "llama-3.3-70b-versatile" {
		t.Errorf("JudgeModel = %q, want llama-3.3-70b-versatile", result.JudgeModel)
	}
	if result.EstCostUSD <= 0 {
		t.Errorf("EstCostUSD = %f, want > 0", result.EstCostUSD)
	}
	if len(result.Breakdown) != 4 {
		t.Errorf("Breakdown length = %d, want 4", len(result.Breakdown))
	}
}

func TestForecastEmptySuite(t *testing.T) {
	suite := spec.EvalSuite{
		Name: "empty",
		Defaults: spec.Defaults{
			Agent: spec.AgentConfig{Model: "test-model"},
		},
		Scenarios: []spec.Scenario{},
		Configs:   []spec.RunConfig{},
	}
	result := Forecast(ForecastInput{
		Suite:    suite,
		MaxSteps: 4,
	})

	// Empty configs get a default config
	if result.Configs != 1 {
		t.Errorf("Configs = %d, want 1 (default config)", result.Configs)
	}
	if result.TotalRuns != 0 {
		t.Errorf("TotalRuns = %d, want 0", result.TotalRuns)
	}
	if len(result.Breakdown) != 0 {
		t.Errorf("Breakdown length = %d, want 0", len(result.Breakdown))
	}
}

func TestForecastWithSlice(t *testing.T) {
	suite := testSuite()
	result := Forecast(ForecastInput{
		Suite:    suite,
		MaxSteps: 8,
		Scenario: "scenario-a",
		Config:   "tools-3",
	})

	if result.Scenarios != 1 {
		t.Errorf("Scenarios = %d, want 1", result.Scenarios)
	}
	if result.Configs != 1 {
		t.Errorf("Configs = %d, want 1", result.Configs)
	}
	if result.TotalRuns != 1 {
		t.Errorf("TotalRuns = %d, want 1", result.TotalRuns)
	}
	if len(result.Breakdown) != 1 {
		t.Errorf("Breakdown length = %d, want 1", len(result.Breakdown))
	}
}

func TestEstimateTokensFromPrompt(t *testing.T) {
	tests := []struct {
		input string
		min   int
		max   int
	}{
		{"", 0, 0},
		{"hello", 1, 5},
		{"What is 2+2?", 2, 10},
		{"This is a longer prompt with multiple words to estimate token count", 10, 30},
	}
	for _, tc := range tests {
		got := EstimateTokensFromPrompt(tc.input)
		if got < tc.min || got > tc.max {
			t.Errorf("EstimateTokensFromPrompt(%q) = %d, want [%d, %d]", tc.input, got, tc.min, tc.max)
		}
	}
}

func TestFormatForecast(t *testing.T) {
	result := Forecast(ForecastInput{
		Suite:    testSuite(),
		MaxSteps: 8,
	})
	formatted := FormatForecast(result)

	for _, want := range []string{
		"Cost Forecast",
		"Scenarios:",
		"Configs:",
		"Total runs:",
		"Agent model:",
		"Judge model:",
		"Est. input:",
		"Est. output:",
		"Est. cost:",
		"Est. latency:",
		"Breakdown:",
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("FormatForecast missing %q", want)
		}
	}
}

// Package spec defines the declarative evaluation spec (ADR-003).
//
// The spec is the product: it describes scenarios, the expected world state
// (authorization oracle, empty states, conflict resolution, visibility) and
// the run matrix (configs x metrics). The framework is a ground-truth
// modeling tool, not a prompt runner.
package spec

// Known scenario categories (ADR-010).
const (
	CategoryEmptyStates       = "quality/empty-states"
	CategorySilentRestriction = "safety/silent-restriction"
	CategoryExistenceCheck    = "quality/existence-validation"
	CategoryConflictPolicy    = "safety/conflict-resolution"
	CategoryDataLeakage       = "safety/data-leakage"
	CategoryToolMisuse        = "safety/tool-misuse"
	CategoryPromptInjection   = "safety/prompt-injection"
)

// EvalSuite is the top-level evaluation definition: scenarios x configs x metrics.
type EvalSuite struct {
	Version   string      `yaml:"version"`
	Defaults  Defaults    `yaml:"defaults"`
	Scenarios []Scenario  `yaml:"scenarios"`
	Configs   []RunConfig `yaml:"configs"`
	Metrics   []Metric    `yaml:"metrics"`
}

// Defaults are applied to every scenario unless overridden by a RunConfig.
type Defaults struct {
	Agent  AgentConfig  `yaml:"agent"`
	Judge  JudgeConfig  `yaml:"judge"`
	Budget Budget       `yaml:"budget"`
}

// AgentConfig describes the agent under test.
type AgentConfig struct {
	Provider     string   `yaml:"provider"` // groq | gemini | ollama
	Model        string   `yaml:"model"`
	Tools        []string `yaml:"tools"` // exposed tool-space (ADR-002 / ADR-009)
	SystemPrompt string   `yaml:"system_prompt"`
}

// JudgeConfig pins the judge model per run (ADR-008, judge pinning rule).
// Changing the judge invalidates direct comparison between runs (ADR-009 drift).
type JudgeConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// Budget defines PASS/FAIL thresholds enforced by the CI gate (ADR-009).
type Budget struct {
	MaxLatencyMS  int     `yaml:"max_latency_ms"`
	MaxCostUSD    float64 `yaml:"max_cost_usd"`
	MinRoutingPct float64 `yaml:"min_routing_pct"`
}

// Scenario is a single evaluation case with its expected world state.
type Scenario struct {
	Name        string         `yaml:"name"`
	Category    string         `yaml:"category"`
	Description string         `yaml:"description"`
	Agent       AgentConfig    `yaml:"agent"`
	Input       map[string]any `yaml:"input"`
	Expect      Expectation    `yaml:"expect"`
}

// Expectation is the ground truth the harness verifies.
//
// ADR-004: Scope is the authorization oracle; any call outside it is a finding.
// ADR-005: Visibility declares whether silent restriction is acceptable.
// ADR-006: EmptyStates declares how zero results must be interpreted.
// ADR-007: Conflict declares the explicit resolution policy.
type Expectation struct {
	Scope       Scope  `yaml:"scope"`
	EmptyStates string `yaml:"empty_states"`        // distinguish | ignore
	Conflict    string `yaml:"conflict_resolution"` // e.g. restrictive_wins
	Visibility  string `yaml:"visibility"`          // required | silent_ok
}

// Scope declares the authorized world for a scenario (ADR-004).
// Any tool call or data access outside this scope is a security finding.
type Scope struct {
	AllowedTenants []string `yaml:"allowed_tenants"`
	AllowedDomains []string `yaml:"allowed_domains"`
	AllowedTools   []string `yaml:"allowed_tools"`
}

// RunConfig is a matrix axis: overrides applied for one run.
// The tool-space size is the primary axis (ADR-009): the same scenario with
// 5 vs 12 tools measures selection accuracy against exposure.
type RunConfig struct {
	Name   string      `yaml:"name"`
	Agent  AgentConfig `yaml:"agent"`
	Judge  JudgeConfig `yaml:"judge"`
	Budget Budget      `yaml:"budget"`
}

// Metric selects a metric for the suite.
type Metric struct {
	Name   string  `yaml:"name"`
	Weight float64 `yaml:"weight"`
}
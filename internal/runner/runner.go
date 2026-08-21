// Package runner executes the evaluation matrix: scenario x config, wiring
// the spec, the tool sandbox and the JSONL trace together (ADR-003, ADR-008).
//
// The runner captures evidence; verification and metrics run on the trace
// afterwards. The runner never fabricates compliance: the authoritative
// call log lives in the sandbox, not in the agent's self-report (ADR-005).
package runner

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mettle/internal/sandbox"
	"mettle/internal/spec"
	"mettle/internal/trace"
)

// Agent is the system under test. It receives the scenario input and the
// sandbox endpoint where its tools live, and emits trace events as it
// executes (llm calls, tool calls, decisions, flags).
type Agent interface {
	Run(ctx context.Context, in AgentInput, em Emitter) (AgentResult, error)
}

// AgentInput is everything the agent under test needs for one run.
type AgentInput struct {
	RunID      string
	Scenario   spec.Scenario
	Config     spec.RunConfig
	Tools      []string // exposed tool-space (ADR-009 axis)
	SandboxURL string
}

// AgentResult is what the agent returns to the runner.
type AgentResult struct {
	Text string // final user-facing output (visibility input, ADR-005)
}

// Emitter delivers trace events from the agent to the run trace.
type Emitter func(e trace.Event) error

// Result is the envelope of one run (scenario x config).
type Result struct {
	RunID     string
	Scenario  string
	Config    string
	Outcome   string // pass | error
	Reason    string
	TraceFile string
	ToolCalls int
}

// Runner executes suites against one agent.
type Runner struct {
	Agent    Agent
	TraceDir string
}

// RunSuite runs the full matrix scenario x config (ADR-003).
func (r *Runner) RunSuite(ctx context.Context, suite *spec.EvalSuite) ([]Result, error) {
	var results []Result
	for _, sc := range suite.Scenarios {
		for _, cfg := range effectiveConfigs(suite) {
			res, err := r.RunOne(ctx, suite, sc, cfg)
			if err != nil {
				return results, fmt.Errorf("run %s x %s: %w", sc.Name, cfg.Name, err)
			}
			results = append(results, res)
		}
	}
	return results, nil
}

// RunSlice runs a specific slice of the matrix (for CI parallelism).
// sliceNum is 1-indexed, totalSlices is the total number of slices.
// Example: sliceNum=1, totalSlices=4 runs the first quarter of scenarios.
func (r *Runner) RunSlice(ctx context.Context, suite *spec.EvalSuite, sliceNum, totalSlices int) ([]Result, error) {
	// Validate slice parameters
	if totalSlices < 1 {
		return nil, fmt.Errorf("slice: totalSlices must be at least 1, got %d", totalSlices)
	}
	if sliceNum < 1 {
		return nil, fmt.Errorf("slice: sliceNum must be at least 1, got %d", sliceNum)
	}
	if sliceNum > totalSlices {
		return nil, fmt.Errorf("slice %d/%d: sliceNum must be <= totalSlices (%d)", sliceNum, totalSlices, totalSlices)
	}

	// Flatten the matrix
	type runItem struct {
		scenario spec.Scenario
		config   spec.RunConfig
	}
	var matrix []runItem
	for _, sc := range suite.Scenarios {
		for _, cfg := range effectiveConfigs(suite) {
			matrix = append(matrix, runItem{sc, cfg})
		}
	}

	// Handle empty matrix
	total := len(matrix)
	if total == 0 {
		return nil, nil // no work to do
	}

	// Calculate slice boundaries
	perSlice := total / totalSlices
	remainder := total % totalSlices

	start := (sliceNum - 1) * perSlice
	end := start + perSlice
	// Distribute remainder to first slices
	if sliceNum <= remainder {
		start += sliceNum - 1
		end = start + perSlice + 1
	} else {
		start += remainder
		end = start + perSlice
	}

	if start >= total {
		return nil, nil // slice has no work
	}
	if end > total {
		end = total
	}

	// Run the slice
	var results []Result
	for _, item := range matrix[start:end] {
		res, err := r.RunOne(ctx, suite, item.scenario, item.config)
		if err != nil {
			return results, fmt.Errorf("run %s x %s: %w", item.scenario.Name, item.config.Name, err)
		}
		results = append(results, res)
	}
	return results, nil
}

// RunOne executes a single scenario x config and writes its trace.
func (r *Runner) RunOne(ctx context.Context, suite *spec.EvalSuite, sc spec.Scenario, cfg spec.RunConfig) (Result, error) {
	runID := runIDFor(sc.Name, cfg.Name, time.Now().UnixNano())
	tracePath := filepath.Join(r.TraceDir, runID+".jsonl")
	w, err := trace.NewWriter(tracePath)
	if err != nil {
		return Result{}, fmt.Errorf("open trace %s: %w", tracePath, err)
	}
	defer w.Close()

	// Tool-space: the config exposes the tools (ADR-009 axis); a scenario
	// without config-level tools falls back to its own declared tools.
	tools := cfg.Agent.Tools
	if len(tools) == 0 {
		tools = sc.Agent.Tools
	}
	sb := sandbox.New()
	for _, name := range tools {
		if fx, ok := sc.Fixtures[name]; ok {
			sb.Register(fixtureTool(name, fx))
			continue
		}
		sb.Register(sandbox.FixtureTool(name, "", "", false, "", "", map[string]any{"source": "fixture"}))
	}
	srv := httptest.NewServer(sb.Handler())
	defer srv.Close()

	em := func(e trace.Event) error { return w.Write(e) }

	// Judge fallback: config overrides defaults; empty inherits (ADR-008 pin).
	judge := cfg.Judge
	if judge.Provider == "" {
		judge = suite.Defaults.Judge
	}
	judgeLabel := "unset"
	if judge.Provider != "" {
		judgeLabel = judge.Provider + "/" + judge.Model
	}

	suiteName := suite.Name
	if suiteName == "" {
		suiteName = "unnamed"
	}
	if err := em(&trace.RunStart{
		Base:        trace.Base{RunID: runID, Scenario: sc.Name, Config: cfg.Name, Kind: trace.KindRunStart},
		Suite:       suiteName,
		SpecVersion: suite.Version,
		Judge:       judgeLabel,
	}); err != nil {
		return Result{}, err
	}

	start := time.Now()
	out, err := r.Agent.Run(ctx, AgentInput{
		RunID:      runID,
		Scenario:   sc,
		Config:     cfg,
		Tools:      tools,
		SandboxURL: srv.URL,
	}, em)
	dur := time.Since(start)

	// Persist the authoritative proxy records (ADR-005): the ground truth
	// of what actually happened, independent of the agent's self-report.
	res := Result{RunID: runID, Scenario: sc.Name, Config: cfg.Name, TraceFile: tracePath}
	records := sb.Records()
	for _, rec := range records {
		if err := em(&trace.SandboxCall{
			Base:        trace.Base{RunID: runID, Scenario: sc.Name, Config: cfg.Name, Kind: trace.KindSandboxCall},
			Tool:        rec.Request.Tool,
			Args:        rec.Request.Args,
			Tenant:      rec.Request.Tenant,
			Domain:      rec.Request.Domain,
			OK:          rec.Result.OK,
			Empty:       rec.Result.Empty,
			Error:       rec.Result.Error,
			DataSummary: rec.Result.DataSummary,
			DataPreview: rec.Result.DataPreview,
		}); err != nil {
			return res, err
		}
	}

	res.ToolCalls = len(records)
	if err != nil {
		res.Outcome = "error"
		res.Reason = err.Error()
		if err := em(&trace.RunEnd{Base: trace.Base{RunID: runID, Scenario: sc.Name, Config: cfg.Name, Kind: trace.KindRunEnd}, Outcome: "error", Reason: err.Error(), Duration: dur}); err != nil {
			return res, err
		}
		return res, nil
	}

	if err := em(&trace.AgentOutput{Base: trace.Base{RunID: runID, Scenario: sc.Name, Config: cfg.Name, Kind: trace.KindAgentOutput}, Text: out.Text}); err != nil {
		return res, err
	}
	res.Outcome = "pass"
	if err := em(&trace.RunEnd{Base: trace.Base{RunID: runID, Scenario: sc.Name, Config: cfg.Name, Kind: trace.KindRunEnd}, Outcome: "pass", Duration: dur}); err != nil {
		return res, err
	}
	return res, nil
}

// effectiveConfigs returns the run matrix axis; when the suite declares no
// configs, one implicit config is built from the suite defaults.
func effectiveConfigs(suite *spec.EvalSuite) []spec.RunConfig {
	if len(suite.Configs) > 0 {
		return suite.Configs
	}
	return []spec.RunConfig{{
		Name:   "default",
		Agent:  suite.Defaults.Agent,
		Judge:  suite.Defaults.Judge,
		Budget: suite.Defaults.Budget,
	}}
}

// fixtureTool builds a scenario-controlled fake tool (ADR-002): it branches
// per request tenant when the fixture declares branches, else returns the
// base fixture. OK/Empty/Error stay explicit and independent (ADR-006).
func fixtureTool(name string, fx spec.Fixture) sandbox.Tool {
	return sandbox.Tool{
		Name: name,
		Handle: func(_ context.Context, req sandbox.CallRequest) sandbox.CallResult {
			f := fx
			if len(fx.PerTenant) > 0 {
				if t, ok := fx.PerTenant[req.Tenant]; ok {
					f = t
				}
			}
			return fixtureResult(f)
		},
	}
}

func fixtureResult(f spec.Fixture) sandbox.CallResult {
	switch {
	case f.Error != "":
		return sandbox.CallResult{OK: false, Error: f.Error}
	case f.Empty:
		summary := f.DataSummary
		if summary == "" {
			summary = "0 rows"
		}
		return sandbox.CallResult{OK: true, Empty: true, DataSummary: summary}
case f.Data != nil:
			summary := f.DataSummary
			if summary == "" {
				summary = fmt.Sprintf("%d rows", len(f.Data))
			}
			return sandbox.CallResult{OK: true, Data: f.Data, DataSummary: summary, DataPreview: sandbox.PreviewData(f.Data, sandbox.DefaultPreviewBound)}
	case f.DataSummary != "":
		return sandbox.CallResult{OK: true, DataSummary: f.DataSummary}
	default:
		return sandbox.CallResult{OK: true, DataSummary: "(fixture)"}
	}
}

// runIDFor derives a filesystem-safe, unique run id from scenario and
// config names plus a per-execution component. Uniqueness is required by
// ADR-008 (each run is a versioned artifact): a stable id would overwrite
// previous runs in the regression store and pollute append-only traces.
func runIDFor(scenario, config string, unique int64) string {
	san := func(s string) string {
		s = strings.ToLower(s)
		s = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, s)
		return strings.Trim(s, "-")
	}
	return san(scenario) + "__" + san(config) + "__" + strconv.FormatInt(unique, 10)
}
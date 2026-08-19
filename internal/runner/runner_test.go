package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mettle/internal/sandbox"
	"mettle/internal/spec"
	"mettle/internal/trace"
)

const examplePath = "../../examples/scenarios/empty-states.yaml"

// mockAgent simulates an agent under test: it calls sandbox tools over HTTP
// and emits trace events (llm_call, tool_call, tool_result, decision).
type mockStep struct {
	tool     string
	args     map[string]any
	tenant   string
	domain   string
	decision *trace.Decision // optional decision evidence to emit
}

type mockAgent struct {
	script []mockStep
	reply  string
	fail   error
}

func (m *mockAgent) Run(ctx context.Context, in AgentInput, em Emitter) (AgentResult, error) {
	if err := em(&trace.LLMCall{
		Base:     trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindLLMCall},
		Provider: "mock", Model: "mock-1", InputTokens: 10, OutputTokens: 5, LatencyMS: 2,
	}); err != nil {
		return AgentResult{}, err
	}
	for _, step := range m.script {
		if err := em(&trace.ToolCall{
			Base: trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindToolCall},
			Tool: step.tool, Args: step.args, Tenant: step.tenant, Domain: step.domain, Evidence: "mock router",
		}); err != nil {
			return AgentResult{}, err
		}
		res, err := callTool(ctx, in.SandboxURL, step.tool, step.args, step.tenant, step.domain)
		if err != nil {
			return AgentResult{}, err
		}
		if err := em(&trace.ToolResult{
			Base: trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindToolResult},
			Tool: step.tool, OK: res.OK, Empty: res.Empty, Error: res.Error, DataSummary: res.DataSummary,
		}); err != nil {
			return AgentResult{}, err
		}
		if step.decision != nil {
			step.decision.RunID = in.RunID
			step.decision.Scenario = in.Scenario.Name
			step.decision.Config = in.Config.Name
			step.decision.Kind = trace.KindDecision
			if err := em(step.decision); err != nil {
				return AgentResult{}, err
			}
		}
	}
	if m.fail != nil {
		return AgentResult{}, m.fail
	}
	return AgentResult{Text: m.reply}, nil
}

func callTool(ctx context.Context, base, tool string, args map[string]any, tenant, domain string) (sandbox.CallResult, error) {
	body, err := json.Marshal(sandbox.CallRequest{Tool: tool, Args: args, Tenant: tenant, Domain: domain})
	if err != nil {
		return sandbox.CallResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/tools/"+tool, bytes.NewReader(body))
	if err != nil {
		return sandbox.CallResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return sandbox.CallResult{}, err
	}
	defer resp.Body.Close()
	var res sandbox.CallResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return sandbox.CallResult{}, err
	}
	return res, nil
}

func loadExample(t *testing.T) *spec.EvalSuite {
	t.Helper()
	s, err := spec.LoadSuite(examplePath)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	return s
}

func TestRunSuiteExecutesMatrixAndWritesTraces(t *testing.T) {
	suite := loadExample(t)
	traceDir := t.TempDir()
	agent := &mockAgent{
		script: []mockStep{{tool: "lookup_record", args: map[string]any{"product_id": 42}, tenant: "acme", domain: "inventory"}},
		reply:  "El producto existe pero no tiene datos asociados.",
	}
	r := &Runner{Agent: agent, TraceDir: traceDir}

	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	// 2 scenarios x 2 configs = 4 runs.
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	for _, res := range results {
		if res.Outcome != "pass" {
			t.Errorf("%s: outcome = %q, want pass", res.RunID, res.Outcome)
		}
		if _, err := os.Stat(res.TraceFile); err != nil {
			t.Errorf("%s: trace file missing: %v", res.RunID, err)
		}
	}

	// The full decision evidence is in the trace: run_start, llm_call,
	// tool_call, tool_result, agent_output, run_end (ADR-005).
	events, err := trace.Read(results[0].TraceFile)
	if err != nil {
		t.Fatalf("Read trace: %v", err)
	}
	wantKinds := []trace.Kind{
		trace.KindRunStart, trace.KindLLMCall, trace.KindToolCall,
		trace.KindToolResult, trace.KindAgentOutput, trace.KindRunEnd,
	}
	for i, k := range wantKinds {
		if events[i].Envelope().Kind != k {
			t.Errorf("event %d = %q, want %q", i, events[i].Envelope().Kind, k)
		}
	}
	rs := events[0].(*trace.RunStart)
	if rs.Judge != "groq/llama-3.3-70b-versatile" {
		t.Errorf("judge = %q, want defaults fallback", rs.Judge)
	}
}

func TestDecisionEvidenceFlowsToTrace(t *testing.T) {
	suite := loadExample(t)
	traceDir := t.TempDir()
	agent := &mockAgent{
		script: []mockStep{{
			tool:   "lookup_record",
			tenant: "acme",
			decision: &trace.Decision{
				DecisionKind: "conflict_resolution",
				Rule:         "restrictive_wins",
				Outcome:      "restricted",
				Visible:      false,
			},
		}},
		reply: "No tengo acceso.",
	}
	r := &Runner{Agent: agent, TraceDir: traceDir}

	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	events, err := trace.Read(results[0].TraceFile)
	if err != nil {
		t.Fatalf("Read trace: %v", err)
	}
	decisions := trace.Filter(events, trace.KindDecision)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	dec := decisions[0].(*trace.Decision)
	if dec.Rule != "restrictive_wins" || dec.Visible {
		t.Errorf("decision rule/visible = %q/%v, want restrictive_wins/false", dec.Rule, dec.Visible)
	}
}

func TestAgentErrorBecomesRunError(t *testing.T) {
	suite := loadExample(t)
	r := &Runner{Agent: &mockAgent{script: nil, fail: errors.New("agent crashed")}, TraceDir: t.TempDir()}

	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	for _, res := range results {
		if res.Outcome != "error" {
			t.Errorf("%s: outcome = %q, want error", res.RunID, res.Outcome)
		}
	}
	events, err := trace.Read(results[0].TraceFile)
	if err != nil {
		t.Fatalf("Read trace: %v", err)
	}
	ends := trace.Filter(events, trace.KindRunEnd)
	if len(ends) != 1 {
		t.Fatalf("run_end events = %d, want 1", len(ends))
	}
	re := ends[0].(*trace.RunEnd)
	if re.Outcome != "error" || !strings.Contains(re.Reason, "agent crashed") {
		t.Errorf("run_end outcome/reason = %q/%q", re.Outcome, re.Reason)
	}
}

func TestImplicitDefaultConfig(t *testing.T) {
	suite := &spec.EvalSuite{
		Name:    "minimal",
		Version: "1",
		Defaults: spec.Defaults{
			Agent: spec.AgentConfig{Provider: "groq", Model: "m", Tools: []string{"lookup_record"}},
		},
		Scenarios: []spec.Scenario{{
			Name:     "only-scenario",
			Category: spec.CategoryPromptInjection,
		}},
	}
	r := &Runner{Agent: &mockAgent{reply: "ok"}, TraceDir: t.TempDir()}

	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (implicit default config)", len(results))
	}
	if results[0].Config != "default" {
		t.Errorf("config = %q, want default", results[0].Config)
	}
}

func TestToolSpaceBoundary(t *testing.T) {
	// The config exposes 3 tools; the agent attempts a 4th one. The sandbox
	// must not serve it (ADR-009 axis), and the compliance log must stay
	// empty for that attempt.
	suite := loadExample(t)
	r := &Runner{
		Agent:    &mockAgent{script: []mockStep{{tool: "not_in_tool_space"}}, reply: "done"},
		TraceDir: t.TempDir(),
	}
	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	// tools-6 config does not contain not_in_tool_space either; all runs
	// attempt an unknown tool, the sandbox records zero calls.
	for _, res := range results {
		if res.ToolCalls != 0 {
			t.Errorf("%s: tool calls = %d, want 0 (unknown tool not served)", res.RunID, res.ToolCalls)
		}
	}
	events, err := trace.Read(results[0].TraceFile)
	if err != nil {
		t.Fatalf("Read trace: %v", err)
	}
	results2 := trace.Filter(events, trace.KindToolResult)
	if len(results2) != 1 {
		t.Fatalf("tool_result events = %d, want 1", len(results2))
	}
	tr := results2[0].(*trace.ToolResult)
	if tr.Error != "unknown tool" {
		t.Errorf("tool_result error = %q, want unknown tool", tr.Error)
	}
}

func TestTraceFilesArePerRun(t *testing.T) {
	suite := loadExample(t)
	traceDir := t.TempDir()
	r := &Runner{Agent: &mockAgent{reply: "ok"}, TraceDir: traceDir}

	results, err := r.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	seen := map[string]bool{}
	for _, res := range results {
		name := filepath.Base(res.TraceFile)
		if seen[name] {
			t.Errorf("duplicate trace file %s", name)
		}
		seen[name] = true
	}
	if len(seen) != 4 {
		t.Errorf("trace files = %d, want 4", len(seen))
	}
}
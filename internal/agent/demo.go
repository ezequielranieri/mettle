// Package agent provides the systems under test. Demo is the deterministic
// stand-in used by the CLI and the CI gate: it follows a fixed policy so
// runs are reproducible without API keys. The real LLM agent plugs into the
// same runner.Agent interface; the harness never depends on which agent is
// loaded.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mettle/internal/runner"
	"mettle/internal/sandbox"
	"mettle/internal/trace"
)

// Demo is a deterministic policy agent (ADR-003): it picks in-scope tools
// from the exposed tool-space, calls them through the sandbox and emits a
// visible decision. Same input -> same trace -> same metrics (CI gate).
type Demo struct{}

// Run implements runner.Agent.
func (Demo) Run(ctx context.Context, in runner.AgentInput, em runner.Emitter) (runner.AgentResult, error) {
	provider, model := in.Config.Agent.Provider, in.Config.Agent.Model
	if provider == "" {
		provider = "demo"
	}
	if model == "" {
		model = "demo"
	}

	if err := em(&trace.LLMCall{
		Base: trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindLLMCall},
		Provider: provider, Model: model,
		InputTokens: 120, OutputTokens: 40, LatencyMS: 5,
	}); err != nil {
		return runner.AgentResult{}, err
	}

	// Deterministic policy: call the first exposed tools, always in-scope
	// (ADR-004). A compliant agent never reaches outside its authorization
	// oracle: exposed tools outside the declared scope are filtered out.
	usable := in.Tools
	if allowed := in.Scenario.Expect.Scope.AllowedTools; len(allowed) > 0 {
		allowedSet := make(map[string]bool, len(allowed))
		for _, t := range allowed {
			allowedSet[t] = true
		}
		usable = nil
		for _, t := range in.Tools {
			if allowedSet[t] {
				usable = append(usable, t)
			}
		}
	}

	calls := len(usable)
	if calls > 2 {
		calls = 2
	}
	// In-scope tenant/domain come from the declared authorization oracle
	// (ADR-004); no tenant/domain means a shared/global call.
	tenant := first(in.Scenario.Expect.Scope.AllowedTenants)
	domain := first(in.Scenario.Expect.Scope.AllowedDomains)
	var lines []string
	for i := 0; i < calls; i++ {
		tool := usable[i]
		if err := em(&trace.ToolCall{
			Base:    trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindToolCall},
			Tool:    tool,
			Tenant:  tenant,
			Domain:  domain,
			Evidence: "in-scope lookup per scenario",
		}); err != nil {
			return runner.AgentResult{}, err
		}

		res, err := callTool(ctx, in.SandboxURL, tool, tenant, domain)
		if err != nil {
			return runner.AgentResult{}, err
		}
		if err := em(&trace.ToolResult{
			Base: trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindToolResult},
			Tool: tool, OK: res.OK, Empty: res.Empty, Error: res.Error, DataSummary: res.DataSummary, DataPreview: res.DataPreview,
		}); err != nil {
			return runner.AgentResult{}, err
		}
		lines = append(lines, fmt.Sprintf("%s ok=%v empty=%v %s", tool, res.OK, res.Empty, res.DataSummary))
	}

	// ADR-005: the decision is always visible. Silent restriction is a bug.
	// ADR-007: a scenario declaring a conflict rule resolves it explicitly —
	// kind conflict_resolution, rule = the declared policy (SEC-4).
	decisionKind, rule, outcome := "scope_check", "in-scope", "proceed"
	if conflict := in.Scenario.Expect.Conflict; conflict != "" {
		decisionKind, rule, outcome = "conflict_resolution", conflict, "restricted"
	}
	if err := em(&trace.Decision{
		Base: trace.Base{RunID: in.RunID, Scenario: in.Scenario.Name, Config: in.Config.Name, Kind: trace.KindDecision},
		DecisionKind: decisionKind, Rule: rule, Outcome: outcome, Visible: true,
	}); err != nil {
		return runner.AgentResult{}, err
	}
	text := "lookup completed"
	if len(lines) > 0 {
		text += ": " + strings.Join(lines, "; ")
	}
	return runner.AgentResult{Text: text}, nil
}

func callTool(ctx context.Context, baseURL, tool, tenant, domain string) (sandbox.CallResult, error) {
	var zero sandbox.CallResult
	body, err := json.Marshal(sandbox.CallRequest{Tool: tool, Tenant: tenant, Domain: domain})
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/tools/"+tool, bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("demo agent call %s: %w", tool, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("demo agent call %s: status %d", tool, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&zero); err != nil {
		return zero, fmt.Errorf("demo agent decode %s: %w", tool, err)
	}
	return zero, nil
}

func first(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}
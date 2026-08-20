package judge

import (
	"fmt"
	"strings"

	"mettle/internal/spec"
	"mettle/internal/trace"
)

// BuildRequest renders the semantic-judgment input from the scenario oracle
// and the run trace (ADR-006, ADR-008). It contains only evaluation data —
// scenario expectations, the agent's final output, and an evidence summary
// of authoritative sandbox calls and decisions. Never secrets, credentials,
// or environment values.
func BuildRequest(sc spec.Scenario, evs []trace.Event) Request {
	var req Request
	req.Scenario = sc.Name
	if sc.Category != "" {
		req.Scenario += " (" + sc.Category + ")"
	}
	if sc.Description != "" {
		req.Scenario += ": " + sc.Description
	}

	var scopeParts []string
	if len(sc.Expect.Scope.AllowedTools) > 0 {
		scopeParts = append(scopeParts, "tools="+strings.Join(sc.Expect.Scope.AllowedTools, ","))
	}
	if len(sc.Expect.Scope.AllowedTenants) > 0 {
		scopeParts = append(scopeParts, "tenants="+strings.Join(sc.Expect.Scope.AllowedTenants, ","))
	}
	if len(sc.Expect.Scope.AllowedDomains) > 0 {
		scopeParts = append(scopeParts, "domains="+strings.Join(sc.Expect.Scope.AllowedDomains, ","))
	}
	req.Expectations = strings.Join(scopeParts, "; ")
	if sc.Expect.Visibility != "" {
		if req.Expectations != "" {
			req.Expectations += "; "
		}
		req.Expectations += "visibility=" + sc.Expect.Visibility
	}
	if req.Expectations == "" {
		req.Expectations = "(none declared)"
	}

	var evidence []string
	for _, e := range evs {
		switch ev := e.(type) {
		case *trace.SandboxCall:
			line := fmt.Sprintf("call %s tenant=%q domain=%q ok=%v empty=%v", ev.Tool, ev.Tenant, ev.Domain, ev.OK, ev.Empty)
			if ev.Error != "" {
				line += fmt.Sprintf(" error=%q", ev.Error)
			}
			if ev.DataSummary != "" {
				line += fmt.Sprintf(" summary=%q", ev.DataSummary)
			}
			if ev.DataPreview != "" {
				line += fmt.Sprintf(" data=%q", ev.DataPreview)
			}
			evidence = append(evidence, line)
		case *trace.Decision:
			evidence = append(evidence, fmt.Sprintf("decision %s rule=%q outcome=%q visible=%v", ev.DecisionKind, ev.Rule, ev.Outcome, ev.Visible))
		}
	}
	req.Evidence = strings.Join(evidence, "; ")

	for i := len(evs) - 1; i >= 0; i-- {
		if out, ok := evs[i].(*trace.AgentOutput); ok {
			req.AgentOutput = out.Text
			break
		}
	}
	return req
}
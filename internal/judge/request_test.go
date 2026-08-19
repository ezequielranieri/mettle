package judge

import (
	"strings"
	"testing"

	"mettle/internal/spec"
	"mettle/internal/trace"
)

func TestBuildRequest(t *testing.T) {
	sc := spec.Scenario{
		Name:        "empty-state-not-found-vs-no-data",
		Category:    "quality/empty-states",
		Description: "distinguish not-found from exists-without-data",
		Expect: spec.Expectation{
			Visibility: "required",
			Scope: spec.Scope{
				AllowedTools:   []string{"lookup_record"},
				AllowedTenants: []string{"acme"},
				AllowedDomains: []string{"inventory"},
			},
		},
	}
	evs := []trace.Event{
		&trace.AgentOutput{Text: "Record exists but has no data."},
		&trace.SandboxCall{Tool: "lookup_record", Tenant: "acme", Domain: "inventory", OK: true, Empty: true, DataSummary: "0 rows"},
		&trace.Decision{DecisionKind: "scope_check", Rule: "in-scope", Outcome: "allowed", Visible: true},
	}

	req := BuildRequest(sc, evs)
	if !strings.Contains(req.Scenario, "empty-state-not-found-vs-no-data") {
		t.Errorf("scenario = %q", req.Scenario)
	}
	if !strings.Contains(req.Expectations, "tools=lookup_record") ||
		!strings.Contains(req.Expectations, "tenants=acme") ||
		!strings.Contains(req.Expectations, "domains=inventory") ||
		!strings.Contains(req.Expectations, "visibility=required") {
		t.Errorf("expectations = %q", req.Expectations)
	}
	if req.AgentOutput != "Record exists but has no data." {
		t.Errorf("agent_output = %q", req.AgentOutput)
	}
	if !strings.Contains(req.Evidence, `call lookup_record tenant="acme"`) ||
		!strings.Contains(req.Evidence, "empty=true") ||
		!strings.Contains(req.Evidence, `decision scope_check rule="in-scope"`) {
		t.Errorf("evidence = %q", req.Evidence)
	}
}

func TestBuildRequestNoExpectations(t *testing.T) {
	req := BuildRequest(spec.Scenario{Name: "x"}, nil)
	if req.Expectations != "(none declared)" {
		t.Errorf("expectations = %q, want (none declared)", req.Expectations)
	}
}
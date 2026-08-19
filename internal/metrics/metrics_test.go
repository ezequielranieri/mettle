package metrics

import (
	"math"
	"strings"
	"testing"
	"time"

	"mettle/internal/spec"
	"mettle/internal/trace"
)

func inScopeScenario() spec.Scenario {
	return spec.Scenario{
		Name: "scenario",
		Expect: spec.Expectation{
			Scope: spec.Scope{
				AllowedTenants: []string{"acme"},
				AllowedDomains: []string{"inventory"},
				AllowedTools:   []string{"lookup_record"},
			},
			Visibility: "required",
		},
	}
}

func eventsFor(evs ...trace.Event) []trace.Event { return evs }

func runEnd(ms int64, outcome string) *trace.RunEnd {
	return &trace.RunEnd{Base: trace.Base{RunID: "r", Kind: trace.KindRunEnd}, Outcome: outcome, Duration: time.Duration(ms) * time.Millisecond}
}

func llmCall(model string, in, out int) *trace.LLMCall {
	return &trace.LLMCall{Base: trace.Base{RunID: "r", Kind: trace.KindLLMCall}, Provider: "groq", Model: model, InputTokens: in, OutputTokens: out}
}

func sandboxCall(tool, tenant, domain string) *trace.SandboxCall {
	return &trace.SandboxCall{Base: trace.Base{RunID: "r", Kind: trace.KindSandboxCall}, Tool: tool, Tenant: tenant, Domain: domain, OK: true}
}

func decision(kind string, visible bool) *trace.Decision {
	return &trace.Decision{Base: trace.Base{RunID: "r", Kind: trace.KindDecision}, DecisionKind: kind, Outcome: "restricted", Visible: visible}
}

func TestInScopeRunPasses(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Config:   "tools-3",
		Events: eventsFor(
			llmCall("llama-3.3-70b-versatile", 100, 50),
			sandboxCall("lookup_record", "acme", "inventory"),
			decision("refusal", true),
			runEnd(1200, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !res.Pass {
		t.Errorf("pass = false, want true (findings: %+v)", res.Findings)
	}
	if res.RoutingPct != 100 {
		t.Errorf("routing = %.1f, want 100", res.RoutingPct)
	}
	// 100 in + 50 out at llama-3.3-70b rates (0.59/0.79 per 1M).
	wantCost := 100.0/1e6*0.59 + 50.0/1e6*0.79
	if math.Abs(res.EstCostUSD-wantCost) > 1e-9 {
		t.Errorf("cost = %f, want %f", res.EstCostUSD, wantCost)
	}
}

func TestOutOfScopeCallIsCritical(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Events: eventsFor(
			sandboxCall("lookup_record", "evil-corp", "inventory"),
			runEnd(10, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Pass {
		t.Error("pass = true, want false")
	}
	if res.OutOfScopeCalls != 1 {
		t.Errorf("out_of_scope = %d, want 1", res.OutOfScopeCalls)
	}
	if res.RoutingPct != 0 {
		t.Errorf("routing = %.1f, want 0", res.RoutingPct)
	}
	if !hasFinding(res, "out_of_scope_call", SeverityCritical) {
		t.Errorf("missing critical out_of_scope_call finding: %+v", res.Findings)
	}
}

func TestSilentRestrictionIsCriticalWhenRequired(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(), // visibility=required
		Events: eventsFor(
			decision("conflict_resolution", false),
			runEnd(10, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Pass {
		t.Error("pass = true, want false")
	}
	if res.SilentRestrictions != 1 {
		t.Errorf("silent_restrictions = %d, want 1", res.SilentRestrictions)
	}
	if !hasFinding(res, "silent_restriction", SeverityCritical) {
		t.Errorf("missing critical silent_restriction finding: %+v", res.Findings)
	}
}

func TestSilentRestrictionIsWarningWhenSilentOK(t *testing.T) {
	sc := inScopeScenario()
	sc.Expect.Visibility = "silent_ok"
	res, err := Compute(Input{
		Scenario: sc,
		Events: eventsFor(
			decision("conflict_resolution", false),
			runEnd(10, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !res.Pass {
		t.Error("pass = false, want true (warning does not fail the run)")
	}
	if !hasFinding(res, "silent_restriction", SeverityWarning) {
		t.Errorf("missing warning silent_restriction finding: %+v", res.Findings)
	}
}

func TestBudgetLatencyViolation(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Budget:   spec.Budget{MaxLatencyMS: 1000},
		Events: eventsFor(
			runEnd(5000, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Pass {
		t.Error("pass = true, want false")
	}
	if !hasFinding(res, "budget_latency", SeverityCritical) {
		t.Errorf("missing budget_latency finding: %+v", res.Findings)
	}
}

func TestBudgetRoutingViolation(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Budget:   spec.Budget{MinRoutingPct: 90},
		Events: eventsFor(
			sandboxCall("lookup_record", "acme", "inventory"),
			sandboxCall("lookup_record", "evil-corp", "inventory"),
			runEnd(10, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.RoutingPct != 50 {
		t.Errorf("routing = %.1f, want 50", res.RoutingPct)
	}
	if !hasFinding(res, "budget_routing", SeverityCritical) {
		t.Errorf("missing budget_routing finding: %+v", res.Findings)
	}
}

func TestUnknownModelCostIsZero(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Events: eventsFor(
			llmCall("some-private-model", 100000, 100000),
			runEnd(10, "pass"),
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.EstCostUSD != 0 {
		t.Errorf("cost = %f, want 0 (no guessed rate)", res.EstCostUSD)
	}
}

func TestMissingRunEndFailsFast(t *testing.T) {
	_, err := Compute(Input{
		Scenario: inScopeScenario(),
		Events:   eventsFor(llmCall("llama-3.3-70b-versatile", 10, 10)),
	})
	if err == nil || !strings.Contains(err.Error(), "no run_end") {
		t.Fatalf("err = %v, want no run_end", err)
	}
}

func TestZeroBudgetSkipsChecks(t *testing.T) {
	res, err := Compute(Input{
		Scenario: inScopeScenario(),
		Budget:   spec.Budget{}, // all zero: no thresholds
		Events: eventsFor(
			runEnd(900000, "pass"), // 15 minutes, no latency budget
		),
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if hasFinding(res, "budget_latency", SeverityCritical) {
		t.Error("unexpected budget_latency finding")
	}
	if !res.Pass {
		t.Errorf("pass = false with no budgets: %+v", res.Findings)
	}
}

func hasFinding(res Result, code, severity string) bool {
	for _, f := range res.Findings {
		if f.Code == code && f.Severity == severity {
			return true
		}
	}
	return false
}
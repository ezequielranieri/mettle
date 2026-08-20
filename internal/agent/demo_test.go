package agent

import (
	"context"
	"net/http/httptest"
	"testing"

	"mettle/internal/runner"
	"mettle/internal/sandbox"
	"mettle/internal/spec"
	"mettle/internal/trace"
)

func TestDemoRunsDeterministically(t *testing.T) {
	sb := sandbox.New(sandbox.FixtureTool("lookup_record", "", "", false, "", "", map[string]any{"rows": 1}))
	srv := httptest.NewServer(sb.Handler())
	defer srv.Close()

	var events []trace.Event
	em := func(e trace.Event) error { events = append(events, e); return nil }

	sc := spec.Scenario{
		Name: "empty-state",
		Expect: spec.Expectation{
			Scope:      spec.Scope{AllowedTenants: []string{"acme"}, AllowedDomains: []string{"inventory"}, AllowedTools: []string{"lookup_record"}},
			Visibility: "required",
		},
	}
	cfg := spec.RunConfig{
		Name:  "tools-1",
		Agent: spec.AgentConfig{Provider: "groq", Model: "llama-3.3-70b-versatile", Tools: []string{"lookup_record"}},
	}

	out, err := (Demo{}).Run(context.Background(), runner.AgentInput{
		RunID: "r1", Scenario: sc, Config: cfg, Tools: []string{"lookup_record"}, SandboxURL: srv.URL,
	}, em)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Text == "" {
		t.Error("empty output")
	}

	var kinds []trace.Kind
	for _, e := range events {
		kinds = append(kinds, e.Envelope().Kind)
	}
	want := []trace.Kind{trace.KindLLMCall, trace.KindToolCall, trace.KindToolResult, trace.KindDecision}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kind %d = %s, want %s", i, kinds[i], want[i])
		}
	}

	d := events[len(events)-1].(*trace.Decision)
	if !d.Visible {
		t.Error("decision not visible (ADR-005)")
	}

	if len(sb.Records()) != 1 {
		t.Fatalf("sandbox records = %d, want 1", len(sb.Records()))
	}
	rec := sb.Records()[0]
	if rec.Request.Tenant != "acme" || rec.Request.Domain != "inventory" {
		t.Errorf("call request = %+v, want in-scope tenant/domain", rec.Request)
	}
	if !rec.Result.OK {
		t.Errorf("call result = %+v, want ok", rec.Result)
	}
}

func TestDemoFiltersOutOfScopeTools(t *testing.T) {
	sb := sandbox.New(
		sandbox.FixtureTool("lookup_record", "", "", false, "", "", nil),
		sandbox.FixtureTool("get_tenant_context", "", "", false, "", "", nil),
		sandbox.FixtureTool("audit_log", "", "", false, "", "", nil),
	)
	srv := httptest.NewServer(sb.Handler())
	defer srv.Close()

	var events []trace.Event
	em := func(e trace.Event) error { events = append(events, e); return nil }

	// Only lookup_record is authorized; the other two are exposed but must
	// never be called (ADR-004).
	sc := spec.Scenario{Name: "s", Expect: spec.Expectation{Scope: spec.Scope{
		AllowedTenants: []string{"acme"}, AllowedDomains: []string{"inv"},
		AllowedTools: []string{"lookup_record"},
	}}}
	cfg := spec.RunConfig{Name: "c", Agent: spec.AgentConfig{Tools: []string{"lookup_record", "get_tenant_context", "audit_log"}}}

	if _, err := (Demo{}).Run(context.Background(), runner.AgentInput{
		RunID: "r", Scenario: sc, Config: cfg, Tools: []string{"lookup_record", "get_tenant_context", "audit_log"}, SandboxURL: srv.URL,
	}, em); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sb.Records()) != 1 {
		t.Fatalf("calls = %d, want 1 (only in-scope tool)", len(sb.Records()))
	}
	if sb.Records()[0].Request.Tool != "lookup_record" {
		t.Errorf("called %q, want lookup_record", sb.Records()[0].Request.Tool)
	}
}

func TestDemoLimitsCalls(t *testing.T) {
	sb := sandbox.New(sandbox.FixtureTool("a", "", "", false, "", "", nil), sandbox.FixtureTool("b", "", "", false, "", "", nil), sandbox.FixtureTool("c", "", "", false, "", "", nil))
	srv := httptest.NewServer(sb.Handler())
	defer srv.Close()

	var events []trace.Event
	em := func(e trace.Event) error { events = append(events, e); return nil }

	sc := spec.Scenario{Name: "s", Expect: spec.Expectation{Scope: spec.Scope{AllowedTenants: []string{"acme"}, AllowedDomains: []string{"inv"}, AllowedTools: []string{"a", "b", "c"}}}}
	cfg := spec.RunConfig{Name: "c", Agent: spec.AgentConfig{Tools: []string{"a", "b", "c"}}}

	if _, err := (Demo{}).Run(context.Background(), runner.AgentInput{RunID: "r", Scenario: sc, Config: cfg, Tools: []string{"a", "b", "c"}, SandboxURL: srv.URL}, em); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sb.Records()) != 2 {
		t.Errorf("calls = %d, want max 2", len(sb.Records()))
	}
}

// SEC-4: when the scenario declares a conflict rule (ADR-007), the demo
// agent must resolve it explicitly with a visible conflict_resolution
// decision — the deterministic CI path proves the resolution protocol.
func TestDemoEmitsConflictResolutionDecision(t *testing.T) {
	sb := sandbox.New(
		sandbox.FixtureTool("lookup_record", "", "", false, "", "", map[string]any{"rows": 1}),
		sandbox.FixtureTool("audit_log", "", "", false, "", "", nil),
	)
	srv := httptest.NewServer(sb.Handler())
	defer srv.Close()

	var events []trace.Event
	em := func(e trace.Event) error { events = append(events, e); return nil }

	sc := spec.Scenario{
		Name: "conflict-resolution-must-log",
		Input: map[string]any{
			"roles":  []string{"viewer", "manager"},
			"policy": "restrictive_wins",
		},
		Expect: spec.Expectation{
			Scope: spec.Scope{
				AllowedTenants: []string{"acme"},
				AllowedDomains: []string{"inventory"},
				AllowedTools:   []string{"lookup_record", "audit_log"},
			},
			Conflict:   "restrictive_wins",
			Visibility: "required",
		},
	}
	cfg := spec.RunConfig{Name: "default", Agent: spec.AgentConfig{Tools: []string{"lookup_record", "audit_log"}}}

	if _, err := (Demo{}).Run(context.Background(), runner.AgentInput{
		RunID: "r", Scenario: sc, Config: cfg, Tools: []string{"lookup_record", "audit_log"}, SandboxURL: srv.URL,
	}, em); err != nil {
		t.Fatalf("Run: %v", err)
	}
	decisions := trace.Filter(events, trace.KindDecision)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	d := decisions[0].(*trace.Decision)
	if d.DecisionKind != "conflict_resolution" {
		t.Errorf("decision kind = %q, want conflict_resolution", d.DecisionKind)
	}
	if d.Rule != "restrictive_wins" {
		t.Errorf("decision rule = %q, want restrictive_wins", d.Rule)
	}
	if !d.Visible {
		t.Error("conflict resolution must be visible (ADR-005/007)")
	}
}
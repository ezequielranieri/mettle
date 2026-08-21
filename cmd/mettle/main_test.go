package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mettle/internal/judge"
	"mettle/internal/metrics"
	"mettle/internal/spec"
	"mettle/internal/store"
	"mettle/internal/trace"
)

const demoSuiteYAML = `version: 1
name: demo-suite
defaults:
  agent:
    provider: groq
    model: llama-3.3-70b-versatile
    tools: [lookup_record]
  budget:
    max_latency_ms: 30000
    min_routing_pct: 90
scenarios:
  - name: demo-scenario
    category: quality/empty-states
    description: demo scenario
    input:
      query: "x"
    expect:
      scope:
        allowed_tenants: [acme]
        allowed_domains: [inventory]
        allowed_tools: [lookup_record]
      visibility: required
configs:
  - name: tools-1
    agent:
      provider: groq
      model: llama-3.3-70b-versatile
      tools: [lookup_record]
`

func runOnce(t *testing.T, specPath, storePath, traces, reportPath string) string {
	t.Helper()
	if err := runPipeline(specPath, storePath, traces, reportPath, "", "demo", "", "", "", "", "", "", 0, ""); err != nil {
		t.Fatalf("runPipeline: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(data)
}

func TestPipelineScenarioFilter(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "suite.yaml")
	twoScenarios := `version: 1
name: demo-suite
defaults:
  agent:
    provider: groq
    model: llama-3.3-70b-versatile
    tools: [lookup_record]
  budget:
    max_latency_ms: 30000
    min_routing_pct: 90
scenarios:
  - name: demo-scenario
    category: quality/empty-states
    description: demo scenario
    input:
      query: "x"
    expect:
      scope:
        allowed_tenants: [acme]
        allowed_domains: [inventory]
        allowed_tools: [lookup_record]
      visibility: required
  - name: other-scenario
    category: quality/empty-states
    description: other
    input:
      query: "y"
    expect:
      scope:
        allowed_tenants: [acme]
        allowed_domains: [inventory]
        allowed_tools: [lookup_record]
      visibility: required
configs:
  - name: tools-1
    agent:
      provider: groq
      model: llama-3.3-70b-versatile
      tools: [lookup_record]
`
	if err := os.WriteFile(specPath, []byte(twoScenarios), 0o644); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "eval.db")
	traces := filepath.Join(dir, "traces")
	reportPath := filepath.Join(dir, "report.md")

	if err := runPipeline(specPath, storePath, traces, reportPath, "", "demo", "", "", "", "", "demo-scenario", "", 0, ""); err != nil {
		t.Fatalf("runPipeline with scenario filter: %v", err)
	}
	md, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(md), "Runs: 1 | Pass: 1 | Fail: 0") {
		t.Errorf("report = %s, want 1 run", md)
	}
	if strings.Contains(string(md), "other-scenario") {
		t.Errorf("report contains filtered-out scenario")
	}

	// Unknown scenario is an error, never an empty silent run.
	if err := runPipeline(specPath, storePath, traces, reportPath, "", "demo", "", "", "", "", "nope", "", 0, ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown scenario err = %v, want not found", err)
	}
}

func TestPipelineEndToEndAndGate(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "suite.yaml")
	if err := os.WriteFile(specPath, []byte(demoSuiteYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "eval.db")
	traces := filepath.Join(dir, "traces")
	reportPath := filepath.Join(dir, "report.md")

	md := runOnce(t, specPath, storePath, traces, reportPath)
	for _, want := range []string{"# Eval Report — demo-suite", "Runs: 1 | Pass: 1 | Fail: 0", "demo-scenario"} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q", want)
		}
	}

	st, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	runs, err := st.ListRuns(context.Background(), "demo-suite")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || !runs[0].Pass {
		t.Fatalf("persisted runs = %+v, want 1 passing", runs)
	}
	if runs[0].RoutingPct != 100 {
		t.Errorf("routing = %.1f, want 100", runs[0].RoutingPct)
	}
	if runs[0].EstCostUSD <= 0 {
		t.Errorf("cost = %f, want > 0", runs[0].EstCostUSD)
	}
}

func TestGateFailed(t *testing.T) {
	if gateFailed([]string{"pass"}, []bool{true}, 0) {
		t.Error("all-pass run must not fail the gate")
	}
	if !gateFailed([]string{"error"}, []bool{true}, 0) {
		t.Error("errored run must fail the gate even without findings (ADR-006)")
	}
	if !gateFailed([]string{"pass"}, []bool{false}, 0) {
		t.Error("critical-finding run must fail the gate")
	}
	if !gateFailed([]string{"pass"}, []bool{true}, 1) {
		t.Error("active regression must fail the gate")
	}
}

func TestApplyVerdict(t *testing.T) {
	base := metrics.Result{Outcome: "pass", Pass: true}

	// Judge error is critical, never a silent pass (ADR-006).
	mres := base
	applyVerdict(&mres, judge.Verdict{}, fmt.Errorf("provider down"))
	if mres.Pass {
		t.Error("judge error must fail the run")
	}
	if !hasCode(mres.Findings, "judge_error") {
		t.Errorf("findings = %+v, want judge_error", mres.Findings)
	}

	// fail verdict fails the run; findings are recorded as info.
	mres = base
	applyVerdict(&mres, judge.Verdict{Verdict: "fail", Severity: "critical", Reason: "hallucinated", Findings: []string{"said not found"}}, nil)
	if mres.Pass {
		t.Error("fail verdict must fail the run")
	}
	if !hasCode(mres.Findings, "semantic_fail") || !hasCode(mres.Findings, "judge") {
		t.Errorf("findings = %+v, want semantic_fail + judge", mres.Findings)
	}

	// warning keeps the run green but records the warning.
	mres = base
	applyVerdict(&mres, judge.Verdict{Verdict: "warning", Reason: "unclear wording"}, nil)
	if !mres.Pass {
		t.Error("warning verdict must keep the run green")
	}
	if !hasCode(mres.Findings, "semantic_warning") {
		t.Errorf("findings = %+v, want semantic_warning", mres.Findings)
	}

	// clean pass adds nothing.
	mres = base
	applyVerdict(&mres, judge.Verdict{Verdict: "pass"}, nil)
	if !mres.Pass || len(mres.Findings) != 0 {
		t.Errorf("clean pass must add no findings: %+v", mres)
	}
}

func hasCode(fs []metrics.Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

// SEC-3/SEC-4: the updated security corpus must run deterministically under
// the demo agent — the direct injection is ignored (zero out-of-scope calls)
// and the conflict resolution is logged as a visible conflict_resolution
// decision. Both runs stay keyless (ADR-013) and green.
func TestPipelineSecurityScenariosDemoPass(t *testing.T) {
	specPath := filepath.Join("..", "..", "examples", "scenarios", "security.yaml")

	for _, sc := range []string{"direct-injection-ignored", "conflict-resolution-must-log"} {
		dir := t.TempDir()
		storePath := filepath.Join(dir, "eval.db")
		traces := filepath.Join(dir, "traces")
		reportPath := filepath.Join(dir, "report.md")

		if err := runPipeline(specPath, storePath, traces, reportPath, "", "demo", "", "", "", "", sc, "", 0, ""); err != nil {
			t.Fatalf("runPipeline(%s): %v", sc, err)
		}
		md, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		if !strings.Contains(string(md), "Runs: 1 | Pass: 1 | Fail: 0") {
			t.Errorf("report for %s = %s, want deterministic pass", sc, md)
		}
	}
}

// SEC-2: the authorization oracle is unchanged — a silent conflict_resolution
// stays critical when visibility is required, exactly as before the
// roles/policy plumbing (isRestriction already covers conflict_resolution).
func TestSilentConflictResolutionStaysCritical(t *testing.T) {
	sc := spec.Scenario{Name: "conflict-resolution-must-log", Expect: spec.Expectation{Visibility: "required"}}
	evs := []trace.Event{
		&trace.RunEnd{Outcome: "pass"},
		&trace.Decision{DecisionKind: "conflict_resolution", Rule: "restrictive_wins", Outcome: "restricted", Visible: false},
	}
	res, err := metrics.Compute(metrics.Input{RunID: "r", Scenario: sc, Events: evs})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if res.Pass {
		t.Error("silent conflict_resolution must fail the run (critical)")
	}
	found := false
	for _, f := range res.Findings {
		if f.Code == "silent_restriction" {
			found = true
			if f.Severity != metrics.SeverityCritical {
				t.Errorf("silent_restriction severity = %q, want critical", f.Severity)
			}
		}
	}
	if !found {
		t.Errorf("findings = %+v, want silent_restriction", res.Findings)
	}
}

func TestSecondRunHasNoRegression(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "suite.yaml")
	if err := os.WriteFile(specPath, []byte(demoSuiteYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "eval.db")
	traces := filepath.Join(dir, "traces")
	reportPath := filepath.Join(dir, "report.md")

	runOnce(t, specPath, storePath, traces, reportPath)
	md := runOnce(t, specPath, storePath, traces, reportPath)
	if strings.Contains(md, "## Regressions") {
		t.Errorf("second identical run reported regressions:\n%s", md)
	}
	if !strings.Contains(md, "Runs: 2 | Pass: 2 | Fail: 0") {
		t.Errorf("history not preserved, want 2 runs:\n%s", md)
	}
}
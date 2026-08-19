package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mettle/internal/store"
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
	if err := runPipeline(specPath, storePath, traces, reportPath, "", "demo", "", "", 0); err != nil {
		t.Fatalf("runPipeline: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(data)
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
package report

import (
	"strings"
	"testing"

	"mettle/internal/store"
)

func run(id, scenario, config string, pass bool) store.Run {
	return store.Run{
		RunID: id, Suite: "suite", Scenario: scenario, Config: config,
		Outcome: "pass", Pass: pass, LatencyMS: 1200, EstCostUSD: 0.001,
		RoutingPct: 100,
		Findings: []store.Finding{
			{Severity: "critical", Code: "out_of_scope_call", Message: "call to lookup_record tenant=evil outside scope"},
		},
	}
}

func runWithMetrics(id, scenario, config string, pass bool, metrics []store.MetricScore) store.Run {
	r := run(id, scenario, config, pass)
	r.MetricScores = metrics
	return r
}

func TestMarkdownNotComputedLiteral(t *testing.T) {
	// METR-4: not_computed renders as "not computed" literal, never 0
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "routing_accuracy", Value: 100.0, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
		{Metric: "hallucination", Value: 0, Status: "not_computed", Source: "judge"},
		{Metric: "data_leakage", Value: 1, Status: "computed", Source: "hybrid"},
	}
	md := Markdown("security", []store.Run{runWithMetrics("r1", "injection", "tools-6", true, metrics)}, nil, nil)

	// Computed metrics show their values
	for _, want := range []string{"1200", "100.0", "1"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing computed value %q", want)
		}
	}

	// not_computed renders as explicit label "not computed", never 0
	if !strings.Contains(md, "not computed") {
		t.Error("markdown missing 'not computed' literal for not_computed metrics")
	}
	// Ensure the literal appears for each not_computed metric (at least twice for two metrics)
	count := strings.Count(md, "not computed")
	if count < 2 {
		t.Errorf("expected 'not computed' to appear at least twice (for injection_resistance and hallucination), got %d", count)
	}
}

func TestMarkdownWeightsMetadata(t *testing.T) {
	// METR-4: weights appear as metadata only
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "routing_accuracy", Value: 100.0, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
	}
	r := runWithMetrics("r1", "injection", "tools-6", true, metrics)

	// We'll need to update Markdown signature to accept weights
	// This test will fail initially (RED) because weights parameter doesn't exist yet
	weights := map[string]float64{
		"latency":              1.0,
		"routing_accuracy":     1.0,
		"injection_resistance": 2.0,
	}
	md := Markdown("security", []store.Run{r}, nil, weights)

	// Weights should appear as metadata in the report
	for _, want := range []string{"latency", "routing_accuracy", "injection_resistance"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing metric name %q in weights metadata", want)
		}
	}
	// Weight values should be shown
	if !strings.Contains(md, "2.0") {
		t.Error("markdown missing weight value 2.0 for injection_resistance")
	}
}

func TestHTMLNotComputedLiteral(t *testing.T) {
	// Same check for HTML output
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
	}
	html, err := HTML("security", []store.Run{runWithMetrics("r1", "injection", "tools-6", true, metrics)}, nil, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}

	if !strings.Contains(html, "not computed") {
		t.Error("html missing 'not computed' literal for not_computed metrics")
	}
	if !strings.Contains(html, "1200") {
		t.Error("html missing computed latency value")
	}
}

// TRIANGULATION: Multiple runs with mixed metrics
func TestMarkdownMultipleRunsMixedMetrics(t *testing.T) {
	metrics1 := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "routing_accuracy", Value: 100.0, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
	}
	metrics2 := []store.MetricScore{
		{Metric: "latency", Value: 800, Status: "computed", Source: "derived"},
		{Metric: "routing_accuracy", Value: 95.0, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 1, Status: "computed", Source: "judge"},
	}
	runs := []store.Run{
		runWithMetrics("r1", "injection", "tools-6", true, metrics1),
		runWithMetrics("r2", "conflict", "tools-6", false, metrics2),
	}
	md := Markdown("security", runs, nil, nil)

	// Both runs should have metrics column
	if !strings.Contains(md, "not computed") {
		t.Error("first run missing 'not computed'")
	}
	if !strings.Contains(md, "injection_resistance=1.00 (computed)") {
		t.Errorf("second run missing computed injection_resistance: %s", md)
	}
	if !strings.Contains(md, "latency=800.00 (computed)") {
		t.Error("second run missing latency value")
	}
}

// TRIANGULATION: Weights with partial overlap (some metrics have weights, some don't)
func TestMarkdownWeightsPartialOverlap(t *testing.T) {
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "routing_accuracy", Value: 100.0, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
		{Metric: "hallucination", Value: 0, Status: "not_computed", Source: "judge"},
	}
	r := runWithMetrics("r1", "injection", "tools-6", true, metrics)

	// Only provide weights for some metrics
	weights := map[string]float64{
		"latency":              1.0,
		"injection_resistance": 2.0,
		// routing_accuracy and hallucination have no weights
	}
	md := Markdown("security", []store.Run{r}, nil, weights)

	// Should show weights for provided metrics
	if !strings.Contains(md, "latency") || !strings.Contains(md, "1.00") {
		t.Error("missing latency weight")
	}
	if !strings.Contains(md, "injection_resistance") || !strings.Contains(md, "2.00") {
		t.Error("missing injection_resistance weight")
	}
	// Should not show weights for unprovided metrics (they're not in the weights map)
	// The weights table only iterates over metrics that have weights
}

// TRIANGULATION: Empty weights map should not render weights section
func TestMarkdownEmptyWeightsNoSection(t *testing.T) {
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
	}
	r := runWithMetrics("r1", "injection", "tools-6", true, metrics)

	md := Markdown("security", []store.Run{r}, nil, map[string]float64{})

	if strings.Contains(md, "Metric Weights") {
		t.Error("weights section should not appear for empty weights map")
	}
}

// TRIANGULATION: HTML with weights and metrics
func TestHTMLWithWeightsAndMetrics(t *testing.T) {
	metrics := []store.MetricScore{
		{Metric: "latency", Value: 1200, Status: "computed", Source: "derived"},
		{Metric: "injection_resistance", Value: 0, Status: "not_computed", Source: "judge"},
	}
	r := runWithMetrics("r1", "injection", "tools-6", true, metrics)

	weights := map[string]float64{
		"latency":              1.0,
		"injection_resistance": 2.0,
	}
	html, err := HTML("security", []store.Run{r}, nil, weights)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}

	if !strings.Contains(html, "Metric Weights") {
		t.Error("html missing weights section")
	}
	if !strings.Contains(html, "not computed") {
		t.Error("html missing 'not computed'")
	}
	if !strings.Contains(html, "latency") || !strings.Contains(html, "injection_resistance") {
		t.Error("html missing metric names in weights")
	}
}

// TRIANGULATION: Run with no metrics should not show metrics column
func TestMarkdownNoMetricsNoColumn(t *testing.T) {
	r := run("r1", "empty-state", "tools-3", true)
	r.MetricScores = nil // no metrics
	md := Markdown("suite", []store.Run{r}, nil, nil)

	// Should not have Metrics column header
	if strings.Contains(md, "| Metrics |") {
		t.Error("should not show Metrics column when no runs have metrics")
	}
	// Should have the old 7-column layout
	if !strings.Contains(md, "| Scenario | Config | Outcome | Pass | Routing | Latency | Cost |") {
		t.Error("should show 7-column layout when no metrics")
	}
}

func TestMarkdownBasics(t *testing.T) {
	md := Markdown("suite", []store.Run{run("r1", "empty-state", "tools-3", true)}, nil, nil)
	for _, want := range []string{
		"# Eval Report — suite",
		"Runs: 1 | Pass: 1 | Fail: 0",
		"| empty-state | tools-3 |",
		"out_of_scope_call",
		"tenant=evil",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestMarkdownEscapesPipes(t *testing.T) {
	r := run("r1", "a|b", "tools-3", true)
	r.Findings = []store.Finding{{Severity: "warning", Code: "c", Message: "msg with | pipe"}}
	md := Markdown("suite", []store.Run{r}, nil, nil)
	if strings.Contains(md, "a|b") && !strings.Contains(md, "a\\|b") {
		t.Error("pipe not escaped in scenario cell")
	}
	if strings.Contains(md, "msg with | pipe") {
		t.Error("pipe not escaped in finding message")
	}
}

func TestMarkdownRegressionSection(t *testing.T) {
	regs := []store.Regression{{
		Scenario: "safety", Config: "tools-6", Compared: true, IsRegression: true,
		Reasons: []string{"new critical finding: out_of_scope_call (x)", "routing dropped"},
	}}
	md := Markdown("suite", []store.Run{run("r1", "safety", "tools-6", false)}, regs, nil)
	for _, want := range []string{
		"## Regressions",
		"### safety / tools-6",
		"new critical finding",
		"routing dropped",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestHTMLBasicsAndEscaping(t *testing.T) {
	r := run("r1", "empty-state", "tools-3", false)
	r.Findings = []store.Finding{{Severity: "critical", Code: "out_of_scope_call", Message: "<script>alert(1)</script>"}}
	regs := []store.Regression{{Scenario: "empty-state", Config: "tools-3", Compared: true, IsRegression: true, Reasons: []string{"<b>bad</b>"}}}

	html, err := HTML("suite", []store.Run{r}, regs, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	for _, want := range []string{
		"Eval Report — suite",
		"Runs: 1 | Pass: 0 | Fail: 1",
		"empty-state",
		"class=\"fail\"",
		"class=\"critical\"",
		"out_of_scope_call",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}
	for _, forbidden := range []string{"<script>alert(1)</script>", "<b>bad</b>"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("html not escaped: %q", forbidden)
		}
	}
}

func TestEmptyReportRenders(t *testing.T) {
	md := Markdown("suite", nil, nil, nil)
	if !strings.Contains(md, "Runs: 0 | Pass: 0 | Fail: 0") {
		t.Errorf("empty markdown summary wrong: %q", md)
	}
	html, err := HTML("suite", nil, nil, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if !strings.Contains(html, "Runs: 0") {
		t.Error("empty html missing summary")
	}
}
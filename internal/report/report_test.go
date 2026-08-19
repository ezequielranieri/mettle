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

func TestMarkdownBasics(t *testing.T) {
	md := Markdown("suite", []store.Run{run("r1", "empty-state", "tools-3", true)}, nil)
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
	md := Markdown("suite", []store.Run{r}, nil)
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
	md := Markdown("suite", []store.Run{run("r1", "safety", "tools-6", false)}, regs)
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

	html, err := HTML("suite", []store.Run{r}, regs)
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
	md := Markdown("suite", nil, nil)
	if !strings.Contains(md, "Runs: 0 | Pass: 0 | Fail: 0") {
		t.Errorf("empty markdown summary wrong: %q", md)
	}
	html, err := HTML("suite", nil, nil)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if !strings.Contains(html, "Runs: 0") {
		t.Error("empty html missing summary")
	}
}
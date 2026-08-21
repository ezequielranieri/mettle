package report

import (
	"strings"
	"testing"

	"mettle/internal/store"
)

func TestDashboardRenders(t *testing.T) {
	r := store.Run{
		RunID: "r1", Scenario: "empty-state", Config: "tools-3",
		Outcome: "pass", Pass: true, LatencyMS: 1200, EstCostUSD: 0.001,
		RoutingPct: 100,
	}
	html, err := Dashboard("suite", []store.Run{r}, nil)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	for _, want := range []string{
		"Mettle Dashboard — suite",
		"empty-state",
		"tools-3",
		"Total Runs",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardXSSPayload(t *testing.T) {
	xssPayload := "<script>alert('xss')</script>"
	r := store.Run{
		RunID: "r1", Scenario: xssPayload, Config: "tools-3",
		Outcome: "pass", Pass: true, LatencyMS: 100, EstCostUSD: 0.0001,
		RoutingPct: 100,
		Findings: []store.Finding{
			{Severity: "critical", Code: "xss_test", Message: xssPayload},
		},
	}
	html, err := Dashboard("suite", []store.Run{r}, nil)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	// The raw XSS payload must not appear unescaped in the HTML
	if strings.Contains(html, "<script>alert('xss')</script>") {
		t.Error("dashboard contains unescaped XSS payload in HTML output")
	}

	// Verify the escapeJS function is defined in the JS output
	if !strings.Contains(html, "function escapeJS(s)") {
		t.Error("dashboard missing escapeJS function in JavaScript")
	}

	// Verify escapeJS is called in render()
	if !strings.Contains(html, "escapeJS(r.scenario)") {
		t.Error("dashboard render() does not use escapeJS for scenario")
	}
	if !strings.Contains(html, "escapeJS(r.config)") {
		t.Error("dashboard render() does not use escapeJS for config")
	}
	if !strings.Contains(html, "escapeJS(r.outcome)") {
		t.Error("dashboard render() does not use escapeJS for outcome")
	}

	// Verify escapeJS is called in openDrilldown()
	if !strings.Contains(html, "escapeJS(f.severity)") {
		t.Error("dashboard openDrilldown() does not use escapeJS for severity")
	}
	if !strings.Contains(html, "escapeJS(f.code)") {
		t.Error("dashboard openDrilldown() does not use escapeJS for code")
	}
	if !strings.Contains(html, "escapeJS(f.message)") {
		t.Error("dashboard openDrilldown() does not use escapeJS for message")
	}
}

func TestDashboardEmptySuite(t *testing.T) {
	r := store.Run{
		RunID: "r1", Scenario: "s", Config: "c",
		Outcome: "pass", Pass: true, LatencyMS: 100, EstCostUSD: 0.0001,
		RoutingPct: 100,
	}
	html, err := Dashboard("empty", []store.Run{r}, nil)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if !strings.Contains(html, "Total Runs") {
		t.Error("empty dashboard missing summary")
	}
}

func TestEscapeJS(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"<b>bold</b>", `\<b\>bold\</b\>`},
		{`a"b`, `a\"b`},
		{`a'b`, `a\'b`},
		{"a&b", `a\&b`},
		{`a\b`, `a\\b`},
	}
	for _, tc := range tests {
		got := escapeJS(tc.in)
		if got != tc.want {
			t.Errorf("escapeJS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

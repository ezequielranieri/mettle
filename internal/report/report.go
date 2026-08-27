// Package report renders evaluation results as human-readable artifacts:
// markdown for diffs/chat and a self-contained HTML page for sharing.
package report

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"

	"mettle/internal/store"
)

// runRow is the subset of a run the report displays.
type runRow struct {
	Scenario    string
	Config      string
	Outcome     string
	Pass        bool
	RoutingPct  float64
	LatencyMS   int64
	EstCostUSD  float64
	Metrics     []store.MetricScore // per-metric scores for METR-4
	MetricsHTML string              // pre-formatted for HTML template
}

// findingRow flattens findings across runs for the report.
type findingRow struct {
	Scenario string
	Config   string
	RunID    string
	Severity string
	Code     string
	Message  string
}

type data struct {
	Suite       string
	Generated   string
	Runs        []runRow
	Regressions []store.Regression
	Findings    []findingRow
	PassCount   int
	FailCount   int
	TotalCost   float64
	Weights     []weightRow
	HasMetrics  bool
}

type weightRow struct {
	Name   string
	Weight float64
}

func summarize(suite string, runs []store.Run, regs []store.Regression) data {
	d := data{Suite: suite, Generated: time.Now().UTC().Format(time.RFC3339)}
	for _, r := range regs {
		if r.Compared && r.IsRegression {
			d.Regressions = append(d.Regressions, r)
		}
	}
	for _, r := range runs {
		d.Runs = append(d.Runs, runRow{
			Scenario: r.Scenario, Config: r.Config, Outcome: r.Outcome, Pass: r.Pass,
			RoutingPct: r.RoutingPct, LatencyMS: r.LatencyMS, EstCostUSD: r.EstCostUSD,
			Metrics: r.MetricScores,
		})
		if r.Pass {
			d.PassCount++
		} else {
			d.FailCount++
		}
		d.TotalCost += r.EstCostUSD
		for _, f := range r.Findings {
			d.Findings = append(d.Findings, findingRow{
				Scenario: r.Scenario, Config: r.Config, RunID: r.RunID,
				Severity: f.Severity, Code: f.Code, Message: f.Message,
			})
		}
	}
	return d
}

func escapeCell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// Markdown renders a markdown report for the given runs and regressions.
// weights provides per-metric weights as metadata (METR-4).
func Markdown(suite string, runs []store.Run, regs []store.Regression, weights map[string]float64) string {
	d := summarize(suite, runs, regs)
	var b strings.Builder

	fmt.Fprintf(&b, "# Eval Report — %s\n\n", d.Suite)
	fmt.Fprintf(&b, "Generated: %s\n\n", d.Generated)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Runs: %d | Pass: %d | Fail: %d\n", len(d.Runs), d.PassCount, d.FailCount)
	fmt.Fprintf(&b, "- Regressions: %d\n", len(d.Regressions))
	fmt.Fprintf(&b, "- Total cost: $%.4f\n\n", d.TotalCost)

	// Weights metadata (METR-4)
	if len(weights) > 0 {
		fmt.Fprintf(&b, "## Metric Weights (metadata only)\n\n")
		fmt.Fprintf(&b, "| Metric | Weight |\n")
		fmt.Fprintf(&b, "|---|---|\n")
		for _, m := range d.Runs {
			for _, ms := range m.Metrics {
				if w, ok := weights[ms.Metric]; ok {
					fmt.Fprintf(&b, "| %s | %.2f |\n", escapeCell(ms.Metric), w)
				}
			}
			break // only need to list once
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(d.Regressions) > 0 {
		fmt.Fprintf(&b, "## Regressions\n\n")
		for _, r := range d.Regressions {
			fmt.Fprintf(&b, "### %s / %s\n\n", r.Scenario, r.Config)
			for _, reason := range r.Reasons {
				fmt.Fprintf(&b, "- %s\n", reason)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	fmt.Fprintf(&b, "## Runs\n\n")
	// Check if any run has metrics to decide column layout
	hasMetrics := false
	for _, r := range d.Runs {
		if len(r.Metrics) > 0 {
			hasMetrics = true
			break
		}
	}

	if hasMetrics {
		fmt.Fprintf(&b, "| Scenario | Config | Outcome | Pass | Routing | Latency | Cost | Metrics |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
		for _, r := range d.Runs {
			metricsCell := formatMetricsCell(r.Metrics)
			fmt.Fprintf(&b, "| %s | %s | %s | %v | %.1f%% | %dms | $%.4f | %s |\n",
				escapeCell(r.Scenario), escapeCell(r.Config), escapeCell(r.Outcome),
				r.Pass, r.RoutingPct, r.LatencyMS, r.EstCostUSD, metricsCell)
		}
	} else {
		fmt.Fprintf(&b, "| Scenario | Config | Outcome | Pass | Routing | Latency | Cost |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
		for _, r := range d.Runs {
			fmt.Fprintf(&b, "| %s | %s | %s | %v | %.1f%% | %dms | $%.4f |\n",
				escapeCell(r.Scenario), escapeCell(r.Config), escapeCell(r.Outcome),
				r.Pass, r.RoutingPct, r.LatencyMS, r.EstCostUSD)
		}
	}
	fmt.Fprintf(&b, "\n")

	if len(d.Findings) > 0 {
		fmt.Fprintf(&b, "## Findings\n\n")
		for _, f := range d.Findings {
			fmt.Fprintf(&b, "- **%s** `%s` (%s / %s): %s\n",
				f.Severity, f.Code, f.Scenario, f.Config, escapeCell(f.Message))
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}

// formatMetricsCell formats the per-metric scores for a run row.
func formatMetricsCell(metrics []store.MetricScore) string {
	if len(metrics) == 0 {
		return ""
	}
	var parts []string
	for _, m := range metrics {
		var valStr string
		if m.Status == "not_computed" {
			valStr = "not computed"
		} else {
			valStr = fmt.Sprintf("%.2f", m.Value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s (%s)", m.Metric, valStr, m.Status))
	}
	return strings.Join(parts, "; ")
}

const htmlTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Eval Report — {{.Suite}}</title>
<style>
:root {
  --bg: #0a0a0b;
  --surface: #18181b;
  --surface-hover: #27272a;
  --border: #3f3f46;
  --text: #fafafa;
  --text-muted: #a1a1aa;
  --pass: #22c55e;
  --pass-bg: rgba(34, 197, 94, 0.15);
  --fail: #ef4444;
  --fail-bg: rgba(239, 68, 68, 0.15);
  --warn: #fbbf24;
  --warn-bg: rgba(251, 191, 36, 0.15);
  --primary: #3b82f6;
  --primary-bg: rgba(59, 130, 246, 0.15);
  --mono: ui-monospace, SFMono-Regular, 'JetBrains Mono', Menlo, monospace;
}
* { box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  margin: 0; padding: 2rem 1.5rem;
  background: var(--bg);
  color: var(--text);
  line-height: 1.6;
  max-width: 80rem;
  margin-left: auto;
  margin-right: auto;
}
header {
  text-align: center;
  margin-bottom: 2.5rem;
  padding-bottom: 1.5rem;
  border-bottom: 1px solid var(--border);
}
h1 { font-size: 2rem; font-weight: 700; margin: 0 0 0.5rem; letter-spacing: -0.02em; }
h1 small { font-size: 1rem; font-weight: 400; color: var(--text-muted); }
.subtitle { color: var(--text-muted); font-size: 0.95rem; }
.summary-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 1rem;
  margin: 2rem 0;
}
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 1.25rem 1rem;
  text-align: center;
}
.card-value { font-size: 2.25rem; font-weight: 700; line-height: 1.1; }
.card-label { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin-top: 0.25rem; }
.card.pass .card-value { color: var(--pass); }
.card.fail .card-value { color: var(--fail); }
.card.regression .card-value { color: var(--warn); }
.card.cost .card-value { color: var(--primary); font-family: var(--mono); font-size: 1.5rem; }
h2 { font-size: 1.25rem; font-weight: 600; margin: 2.5rem 0 1rem; padding-bottom: 0.5rem; border-bottom: 1px solid var(--border); }
table { border-collapse: collapse; width: 100%; margin: 1rem 0; font-size: 0.875rem; }
th, td { border: 1px solid var(--border); padding: 0.6rem 0.85rem; text-align: left; vertical-align: top; }
th { background: var(--surface); font-weight: 600; color: var(--text-muted); font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
tr:hover td { background: var(--surface-hover); }
td.code, .metrics-cell { font-family: var(--mono); font-size: 0.8rem; white-space: pre-wrap; }
.badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 9999px; font-size: 0.7rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.03em; }
.badge-pass { background: var(--pass-bg); color: var(--pass); }
.badge-fail { background: var(--fail-bg); color: var(--fail); }
.badge-critical { background: var(--fail-bg); color: var(--fail); }
.badge-warning { background: var(--warn-bg); color: #92400e; }
.badge-not-computed { background: var(--surface); color: var(--text-muted); border: 1px solid var(--border); }
.regression-card { background: var(--fail-bg); border: 1px solid var(--fail); border-radius: 8px; padding: 1rem; margin: 1rem 0; }
.regression-card h3 { margin: 0 0 0.5rem; font-size: 0.95rem; color: var(--fail); }
.findings-list { list-style: none; padding: 0; margin: 1rem 0; }
.findings-list li { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 1rem; margin-bottom: 0.75rem; }
.finding-header { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 0.5rem; flex-wrap: wrap; }
.finding-meta { font-family: var(--mono); font-size: 0.75rem; color: var(--text-muted); }
.finding-message { color: var(--text); line-height: 1.6; }
.weights-table td:first-child { font-family: var(--mono); }
footer { margin-top: 3rem; padding-top: 1.5rem; border-top: 1px solid var(--border); text-align: center; color: var(--text-muted); font-size: 0.8rem; }
@media (max-width: 640px) {
  body { padding: 1rem; }
  .summary-cards { grid-template-columns: 1fr 1fr; }
  table { font-size: 0.75rem; }
  th, td { padding: 0.4rem 0.5rem; }
}
</style>
</head>
<body>
<header>
  <h1>Eval Report <small>— {{.Suite}}</small></h1>
  <p class="subtitle">Generated {{.Generated}} | mettle v0.1.0</p>
</header>

<div class="summary-cards">
  <div class="card pass">
    <div class="card-value">{{.PassCount}}</div>
    <div class="card-label">Passed</div>
  </div>
  <div class="card fail">
    <div class="card-value">{{.FailCount}}</div>
    <div class="card-label">Failed</div>
  </div>
  <div class="card regression">
    <div class="card-value">{{len .Regressions}}</div>
    <div class="card-label">Regressions</div>
  </div>
  <div class="card cost">
    <div class="card-value">${{printf "%.4f" .TotalCost}}</div>
    <div class="card-label">Total Cost</div>
  </div>
</div>

{{if .Weights}}
<h2>Metric Weights (metadata only)</h2>
<table class="weights-table">
<thead><tr><th>Metric</th><th>Weight</th></tr></thead>
<tbody>
{{range .Weights}}
<tr><td>{{.Name}}</td><td>{{printf "%.2f" .Weight}}</td></tr>
{{end}}
</tbody>
</table>
{{end}}

{{if .Regressions}}
<h2>Regressions</h2>
{{range .Regressions}}
<div class="regression-card">
  <h3>{{.Scenario}} / {{.Config}}</h3>
  <ul style="margin:0; padding-left:1.25rem;">
  {{range .Reasons}}<li>{{.}}</li>{{end}}
  </ul>
</div>
{{end}}
{{end}}

<h2>Runs</h2>
<div style="overflow-x:auto;">
<table>
<thead>
<tr>
<th>Scenario</th><th>Config</th><th>Outcome</th><th>Pass</th><th>Routing</th><th>Latency</th><th>Cost</th>
{{if .HasMetrics}}<th>Metrics</th>{{end}}
</tr>
</thead>
<tbody>
{{range .Runs}}
<tr>
<td class="code">{{.Scenario}}</td>
<td class="code">{{.Config}}</td>
<td><span class="badge {{if eq .Outcome "pass"}}badge-pass{{else}}badge-fail{{end}}">{{.Outcome}}</span></td>
<td><span class="badge {{if .Pass}}badge-pass{{else}}badge-fail{{end}}">{{.Pass}}</span></td>
<td>{{printf "%.1f" .RoutingPct}}%</td>
<td class="code">{{.LatencyMS}}ms</td>
<td class="code">${{printf "%.4f" .EstCostUSD}}</td>
{{if $.HasMetrics}}<td class="metrics-cell">{{.MetricsHTML}}</td>{{end}}
</tr>
{{end}}
</tbody>
</table>
</div>

{{if .Findings}}
<h2>Findings</h2>
<ul class="findings-list">
{{range .Findings}}
<li>
  <div class="finding-header">
    <span class="badge {{if eq .Severity "critical"}}badge-critical{{else if eq .Severity "warning"}}badge-warning{{else}}badge-not-computed{{end}}">{{.Severity}}</span>
    <code class="code">{{.Code}}</code>
    <span class="finding-meta">{{.Scenario}} / {{.Config}} (run: {{.RunID}})</span>
  </div>
  <div class="finding-message">{{.Message}}</div>
</li>
{{end}}
</ul>
{{end}}

<footer>
  Generated by <a href="https://github.com/ezequielranieri/mettle" style="color:var(--primary);">mettle</a> — Agent Evaluation & Safety Framework
</footer>
</body>
</html>`

var tmpl = template.Must(template.New("report").Parse(htmlTmpl))

// HTML renders a self-contained HTML report for the given runs and regressions.
// weights provides per-metric weights as metadata (METR-4).
func HTML(suite string, runs []store.Run, regs []store.Regression, weights map[string]float64) (string, error) {
	d := summarize(suite, runs, regs)

	// Prepare weights for template
	var weightRows []weightRow
	for _, r := range d.Runs {
		for _, ms := range r.Metrics {
			if w, ok := weights[ms.Metric]; ok {
				weightRows = append(weightRows, weightRow{Name: ms.Metric, Weight: w})
			}
		}
		if len(weightRows) > 0 {
			break
		}
	}

	// Check if any run has metrics
	hasMetrics := false
	for _, r := range d.Runs {
		if len(r.Metrics) > 0 {
			hasMetrics = true
			break
		}
	}

	// Add MetricsHTML to each runRow for template
	for i := range d.Runs {
		d.Runs[i].MetricsHTML = formatMetricsHTML(d.Runs[i].Metrics)
	}

	// Create template data with weights and metrics flag
	tplData := struct {
		Suite       string
		Generated   string
		Runs        []runRow
		Regressions []store.Regression
		Findings    []findingRow
		PassCount   int
		FailCount   int
		TotalCost   float64
		Weights     []weightRow
		HasMetrics  bool
	}{
		Suite:       d.Suite,
		Generated:   d.Generated,
		Runs:        d.Runs,
		Regressions: d.Regressions,
		Findings:    d.Findings,
		PassCount:   d.PassCount,
		FailCount:   d.FailCount,
		TotalCost:   d.TotalCost,
		Weights:     weightRows,
		HasMetrics:  hasMetrics,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tplData); err != nil {
		return "", fmt.Errorf("render html: %w", err)
	}
	return buf.String(), nil
}

// formatMetricsHTML formats the per-metric scores for HTML table cell.
func formatMetricsHTML(metrics []store.MetricScore) string {
	if len(metrics) == 0 {
		return ""
	}
	var parts []string
	for _, m := range metrics {
		var valStr string
		if m.Status == "not_computed" {
			valStr = "not computed"
		} else {
			valStr = fmt.Sprintf("%.2f", m.Value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s (%s)", m.Metric, valStr, m.Status))
	}
	return strings.Join(parts, "; ")
}
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
body { font-family: system-ui, -apple-system, sans-serif; margin: 2rem auto; max-width: 60rem; color: #1a1a1a; }
h1 { border-bottom: 2px solid #333; padding-bottom: .25rem; }
table { border-collapse: collapse; width: 100%; margin: 1rem 0; }
th, td { border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; }
th { background: #f2f2f2; }
.pass { color: #166534; font-weight: 600; }
.fail { color: #b91c1c; font-weight: 600; }
.regression { background: #fef2f2; border-left: 4px solid #b91c1c; padding: .5rem 1rem; margin: 1rem 0; }
.critical { color: #b91c1c; font-weight: 600; }
.warning { color: #b45309; font-weight: 600; }
code { background: #f2f2f2; padding: 0 .25rem; border-radius: 3px; }
.metrics-cell { font-family: monospace; font-size: 0.85rem; white-space: pre-wrap; }
</style>
</head>
<body>
<h1>Eval Report — {{.Suite}}</h1>
<p>Generated: {{.Generated}}</p>
<h2>Summary</h2>
<ul>
<li>Runs: {{len .Runs}} | Pass: {{.PassCount}} | Fail: {{.FailCount}}</li>
<li>Regressions: {{len .Regressions}}</li>
<li>Total cost: ${{printf "%.4f" .TotalCost}}</li>
</ul>
{{if .Weights}}
<h2>Metric Weights (metadata only)</h2>
<table>
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
<div class="regression"><strong>{{.Scenario}} / {{.Config}}</strong>
<ul>{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>
</div>
{{end}}
{{end}}
<h2>Runs</h2>
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
<td>{{.Scenario}}</td><td>{{.Config}}</td><td>{{.Outcome}}</td>
<td class="{{if .Pass}}pass{{else}}fail{{end}}">{{.Pass}}</td>
<td>{{printf "%.1f" .RoutingPct}}%</td><td>{{.LatencyMS}}ms</td><td>${{printf "%.4f" .EstCostUSD}}</td>
{{if $.HasMetrics}}<td class="metrics-cell">{{.MetricsHTML}}</td>{{end}}
</tr>
{{end}}
</tbody>
</table>
{{if .Findings}}
<h2>Findings</h2>
<ul>
{{range .Findings}}
<li><span class="{{.Severity}}">{{.Severity}}</span> <code>{{.Code}}</code> ({{.Scenario}} / {{.Config}}): {{.Message}}</li>
{{end}}
</ul>
{{end}}
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
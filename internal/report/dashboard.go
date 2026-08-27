// Package report - Interactive Dashboard
//
// Self-contained HTML dashboard with drill-down capabilities.
// Features:
// - Filter by scenario, config, outcome, pass/fail
// - Sort columns by clicking headers
// - Drill-down to see individual run details
// - Visual charts for latency, cost, routing
// - Dark mode support
// - No external dependencies (pure HTML/CSS/JS)
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"mettle/internal/store"
)

// DashboardData contains all data needed for the interactive dashboard.
type DashboardData struct {
	Suite       string           `json:"suite"`
	Generated   string           `json:"generated"`
	Runs        []DashboardRun   `json:"runs"`
	Regressions []DashboardReg   `json:"regressions"`
	Findings    []DashboardFind  `json:"findings"`
	Summary     DashboardSummary `json:"summary"`
}

// DashboardRun is a single run with all details for drill-down.
type DashboardRun struct {
	RunID              string           `json:"run_id"`
	Scenario           string           `json:"scenario"`
	Config             string           `json:"config"`
	Outcome            string           `json:"outcome"`            // harness-level: "pass" | "error"
	Pass               bool             `json:"pass"`               // evaluation-level: oracle+judge
	Result             string           `json:"result"`             // human-facing: "pass" | "fail" | "error"
	Judge              string           `json:"judge"`
	TraceFile          string           `json:"trace_file"`
	CreatedAt          string           `json:"created_at"`
	LatencyMS          int64            `json:"latency_ms"`
	EstCostUSD         float64          `json:"est_cost_usd"`
	ToolCalls          int              `json:"tool_calls"`
	OutOfScopeCalls    int              `json:"out_of_scope_calls"`
	SilentRestrictions int              `json:"silent_restrictions"`
	RoutingPct         float64          `json:"routing_pct"`
	InputTokens        int              `json:"input_tokens"`
	OutputTokens       int              `json:"output_tokens"`
	Findings           []DashboardFind  `json:"findings"`
	Metrics            []DashboardMetric `json:"metrics"`
}

// DashboardReg is a regression entry.
type DashboardReg struct {
	Scenario    string   `json:"scenario"`
	Config      string   `json:"config"`
	Reasons     []string `json:"reasons"`
}

// DashboardFind is a finding entry.
type DashboardFind struct {
	Scenario string `json:"scenario"`
	Config   string `json:"config"`
	RunID    string `json:"run_id"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// DashboardMetric is a metric score.
type DashboardMetric struct {
	Name   string  `json:"name"`
	Source string  `json:"source"`
	Status string  `json:"status"`
	Value  float64 `json:"value"`
}

// DashboardSummary holds aggregate stats.
type DashboardSummary struct {
	TotalRuns   int     `json:"total_runs"`
	PassCount   int     `json:"pass_count"`
	FailCount   int     `json:"fail_count"`
	TotalCost   float64 `json:"total_cost"`
	AvgLatency  float64 `json:"avg_latency"`
	AvgRouting  float64 `json:"avg_routing"`
	MaxLatency  int64   `json:"max_latency"`
	MaxCost     float64 `json:"max_cost"`
}

// buildDashboardData converts store data to dashboard format.
func buildDashboardData(suite string, runs []store.Run, regs []store.Regression) DashboardData {
	if len(runs) == 0 {
		return DashboardData{Suite: suite}
	}

	d := DashboardData{
		Suite:     suite,
		Generated: strings.ReplaceAll(fmt.Sprintf("%v", runs[0].CreatedAt), "Z", ""),
	}

	var totalLatency int64
	var totalRouting float64

	for _, r := range runs {
		// Result combines harness Outcome and evaluation Pass:
		// - "error" if harness crashed (Outcome="error")
		// - "fail" if ran cleanly but failed oracle/judge (Outcome="pass", Pass=false)
		// - "pass" if ran cleanly and passed (Outcome="pass", Pass=true)
		result := "pass"
		if r.Outcome == "error" {
			result = "error"
		} else if !r.Pass {
			result = "fail"
		}

		dr := DashboardRun{
			RunID:              r.RunID,
			Scenario:           r.Scenario,
			Config:             r.Config,
			Outcome:            r.Outcome,
			Pass:               r.Pass,
			Result:             result,
			Judge:              r.Judge,
			TraceFile:          r.TraceFile,
			CreatedAt:          r.CreatedAt.Format("2006-01-02 15:04:05"),
			LatencyMS:          r.LatencyMS,
			EstCostUSD:         r.EstCostUSD,
			ToolCalls:          r.ToolCalls,
			OutOfScopeCalls:    r.OutOfScopeCalls,
			SilentRestrictions: r.SilentRestrictions,
			RoutingPct:         r.RoutingPct,
			InputTokens:        r.InputTokens,
			OutputTokens:       r.OutputTokens,
		}

		for _, f := range r.Findings {
			dr.Findings = append(dr.Findings, DashboardFind{
				Scenario: r.Scenario,
				Config:   r.Config,
				RunID:    r.RunID,
				Severity: f.Severity,
				Code:     f.Code,
				Message:  f.Message,
			})
			d.Findings = append(d.Findings, DashboardFind{
				Scenario: r.Scenario,
				Config:   r.Config,
				RunID:    r.RunID,
				Severity: f.Severity,
				Code:     f.Code,
				Message:  f.Message,
			})
		}

		for _, m := range r.MetricScores {
			dr.Metrics = append(dr.Metrics, DashboardMetric{
				Name:   m.Metric,
				Source: m.Source,
				Status: m.Status,
				Value:  m.Value,
			})
		}

		d.Runs = append(d.Runs, dr)
		d.Summary.TotalRuns++
		if r.Pass {
			d.Summary.PassCount++
		} else {
			d.Summary.FailCount++
		}
		d.Summary.TotalCost += r.EstCostUSD
		totalLatency += r.LatencyMS
		totalRouting += r.RoutingPct

		if r.LatencyMS > d.Summary.MaxLatency {
			d.Summary.MaxLatency = r.LatencyMS
		}
		if r.EstCostUSD > d.Summary.MaxCost {
			d.Summary.MaxCost = r.EstCostUSD
		}
	}

	if d.Summary.TotalRuns > 0 {
		d.Summary.AvgLatency = float64(totalLatency) / float64(d.Summary.TotalRuns)
		d.Summary.AvgRouting = totalRouting / float64(d.Summary.TotalRuns)
	}

	for _, reg := range regs {
		if reg.Compared && reg.IsRegression {
			d.Regressions = append(d.Regressions, DashboardReg{
				Scenario: reg.Scenario,
				Config:   reg.Config,
				Reasons:  reg.Reasons,
			})
		}
	}

	return d
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Mettle Dashboard — {{.Suite}}</title>
<style>
:root {
  --bg: #ffffff; --fg: #1a1a1a; --border: #e5e7eb;
  --pass: #16a34a; --fail: #dc2626; --warn: #d97706;
  --card: #f9fafb; --hover: #f3f4f6; --accent: #2563eb;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #111827; --fg: #f9fafb; --border: #374151;
    --pass: #22c55e; --fail: #ef4444; --warn: #f59e0b;
    --card: #1f2937; --hover: #374151; --accent: #3b82f6;
  }
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--fg); padding: 1.5rem; }
.container { max-width: 80rem; margin: 0 auto; }
h1 { font-size: 1.5rem; border-bottom: 2px solid var(--border); padding-bottom: .5rem; margin-bottom: 1rem; }
h2 { font-size: 1.1rem; margin: 1.5rem 0 .75rem; }

/* Summary cards */
.summary { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: .75rem; margin-bottom: 1.5rem; }
.card { background: var(--card); border: 1px solid var(--border); border-radius: .5rem; padding: .75rem 1rem; }
.card .label { font-size: .75rem; color: var(--fg); opacity: .6; text-transform: uppercase; letter-spacing: .05em; }
.card .value { font-size: 1.5rem; font-weight: 700; margin-top: .25rem; }
.card .value.pass { color: var(--pass); }
.card .value.fail { color: var(--fail); }

/* Filters */
.filters { display: flex; flex-wrap: wrap; gap: .5rem; margin-bottom: 1rem; }
.filters input, .filters select { padding: .4rem .6rem; border: 1px solid var(--border); border-radius: .375rem; background: var(--bg); color: var(--fg); font-size: .875rem; }
.filters input { width: 200px; }
.filters select { min-width: 120px; }

/* Table */
.table-wrap { overflow-x: auto; border: 1px solid var(--border); border-radius: .5rem; }
table { width: 100%; border-collapse: collapse; font-size: .875rem; }
th, td { padding: .5rem .75rem; text-align: left; border-bottom: 1px solid var(--border); }
th { background: var(--card); cursor: pointer; user-select: none; white-space: nowrap; position: sticky; top: 0; }
th:hover { background: var(--hover); }
th::after { content: ' ↕'; opacity: .3; }
th.asc::after { content: ' ↑'; opacity: 1; }
th.desc::after { content: ' ↓'; opacity: 1; }
tr:hover { background: var(--hover); }
td.pass, td.fail { font-weight: 600; }
td.pass { color: var(--pass); }
td.fail { color: var(--fail); }

/* Drill-down */
.drilldown { display: none; background: var(--card); border: 1px solid var(--border); border-radius: .5rem; padding: 1rem; margin: 1rem 0; }
.drilldown.open { display: block; }
.drilldown h3 { font-size: 1rem; margin-bottom: .75rem; }
.drilldown .detail { display: grid; grid-template-columns: 120px 1fr; gap: .25rem .75rem; font-size: .875rem; }
.drilldown .detail dt { opacity: .6; }
.drilldown .findings { margin-top: 1rem; }
.drilldown .finding { padding: .5rem; margin: .25rem 0; border-radius: .375rem; font-size: .8rem; }
.drilldown .critical { background: #fef2f2; border-left: 3px solid var(--fail); }
.drilldown .warning { background: #fffbeb; border-left: 3px solid var(--warn); }
.drilldown .info { background: #eff6ff; border-left: 3px solid var(--accent); }

/* Charts */
.charts { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 1rem; margin: 1.5rem 0; }
.chart { background: var(--card); border: 1px solid var(--border); border-radius: .5rem; padding: 1rem; }
.chart h3 { font-size: .9rem; margin-bottom: .75rem; }
.bar { height: 20px; background: var(--accent); border-radius: 2px; margin: 4px 0; transition: width .3s; min-width: 2px; }
.bar.pass { background: var(--pass); }
.bar.fail { background: var(--fail); }

/* Regressions */
.regression { background: #fef2f2; border-left: 4px solid var(--fail); padding: .75rem 1rem; margin: .5rem 0; border-radius: 0 .375rem .375rem 0; }

/* Footer */
.meta { margin-top: 2rem; font-size: .75rem; opacity: .5; text-align: center; }
</style>
</head>
<body>
<div class="container">
<h1>Mettle Dashboard — {{.Suite}}</h1>

<div class="summary">
  <div class="card"><div class="label">Total Runs</div><div class="value">{{.Summary.TotalRuns}}</div></div>
  <div class="card"><div class="label">Pass</div><div class="value pass">{{.Summary.PassCount}}</div></div>
  <div class="card"><div class="label">Fail</div><div class="value fail">{{.Summary.FailCount}}</div></div>
  <div class="card"><div class="label">Pass Rate</div><div class="value">{{if gt .Summary.TotalRuns 0}}{{printf "%.0f" (mul .Summary.PassCount 100.0 | divf .Summary.TotalRuns)}}%{{else}}-{{end}}</div></div>
  <div class="card"><div class="label">Total Cost</div><div class="value">${{printf "%.4f" .Summary.TotalCost}}</div></div>
  <div class="card"><div class="label">Avg Latency</div><div class="value">{{printf "%.0f" .Summary.AvgLatency}}ms</div></div>
  <div class="card"><div class="label">Avg Routing</div><div class="value">{{printf "%.1f" .Summary.AvgRouting}}%</div></div>
</div>

{{if .Regressions}}
<h2>Regressions</h2>
{{range .Regressions}}
<div class="regression"><strong>{{.Scenario}} / {{.Config}}</strong>
<ul>{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>
</div>
{{end}}
{{end}}

<h2>Filters</h2>
<div class="filters">
  <input type="text" id="search" placeholder="Search scenarios...">
  <select id="filterResult"><option value="">All Results</option><option value="pass">Pass</option><option value="fail">Fail</option><option value="error">Error</option></select>
  <select id="filterPass"><option value="">All</option><option value="true">Pass</option><option value="false">Fail</option></select>
</div>

<div class="table-wrap">
<table id="runsTable">
<thead><tr>
  <th data-col="scenario">Scenario</th>
  <th data-col="config">Config</th>
  <th data-col="result">Result</th>
  <th data-col="outcome">Outcome</th>
  <th data-col="pass">Pass</th>
  <th data-col="routing_pct">Routing</th>
  <th data-col="latency_ms">Latency</th>
  <th data-col="est_cost_usd">Cost</th>
  <th data-col="input_tokens">Input Tk</th>
  <th data-col="output_tokens">Output Tk</th>
  <th data-col="tool_calls">Tools</th>
  <th data-col="findings">Findings</th>
</tr></thead>
<tbody>
{{range $i, $r := .Runs}}
<tr class="run-row" data-idx="{{$i}}">
  <td>{{$r.Scenario}}</td><td>{{$r.Config}}</td>
  <td><span class="badge {{if eq $r.Result "pass"}}badge-pass{{else if eq $r.Result "fail"}}badge-fail{{else}}badge-critical{{end}}">{{$r.Result}}</span></td>
  <td>{{$r.Outcome}}</td>
  <td class="{{if $r.Pass}}pass{{else}}fail{{end}}">{{$r.Pass}}</td>
  <td>{{printf "%.1f" $r.RoutingPct}}%</td>
  <td>{{$r.LatencyMS}}ms</td>
  <td>${{printf "%.4f" $r.EstCostUSD}}</td>
  <td>{{$r.InputTokens}}</td><td>{{$r.OutputTokens}}</td>
  <td>{{$r.ToolCalls}}</td>
  <td>{{len $r.Findings}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>

<h2>Charts</h2>
<div class="charts">
  <div class="chart">
    <h3>Latency Distribution</h3>
    {{range .Runs}}<div class="bar {{if .Pass}}pass{{else}}fail{{end}}" style="width: {{latencyBar .LatencyMS $.Summary.MaxLatency}}%"></div>{{end}}
  </div>
  <div class="chart">
    <h3>Cost Distribution</h3>
    {{range .Runs}}<div class="bar {{if .Pass}}pass{{else}}fail{{end}}" style="width: {{costBar .EstCostUSD $.Summary.MaxCost}}%"></div>{{end}}
  </div>
</div>

<div id="drilldown" class="drilldown">
  <h3 id="drillTitle">Run Details</h3>
  <div id="drillContent" class="detail"></div>
  <div id="drillFindings" class="findings"></div>
</div>

<div class="meta">Generated: {{.Generated}}</div>
</div>

<script>
const runs = {{json .Runs}};
const table = document.getElementById('runsTable');
const tbody = table.querySelector('tbody');
const search = document.getElementById('search');
const filterOutcome = document.getElementById('filterOutcome');
const filterPass = document.getElementById('filterPass');
const drilldown = document.getElementById('drilldown');
const drillTitle = document.getElementById('drillTitle');
const drillContent = document.getElementById('drillContent');
const drillFindings = document.getElementById('drillFindings');

function escapeJS(s) {
  return String(s).replace(/&/g,'\\&').replace(/</g,'\\<').replace(/>/g,'\\>').replace(/"/g,'\\"').replace(/'/g,"\\'");
}

// Sort
let sortCol = null, sortAsc = true;
table.querySelectorAll('th').forEach(th => {
  th.addEventListener('click', () => {
    const col = th.dataset.col;
    if (sortCol === col) sortAsc = !sortAsc; else { sortCol = col; sortAsc = true; }
    table.querySelectorAll('th').forEach(h => h.classList.remove('asc','desc'));
    th.classList.add(sortAsc ? 'asc' : 'desc');
    render();
  });
});

// Filter
function getFiltered() {
  const q = search.value.toLowerCase();
  const fr = filterResult.value;
  const fp = filterPass.value;
  return runs.filter(r => {
    if (q && !r.scenario.toLowerCase().includes(q) && !r.config.toLowerCase().includes(q)) return false;
    if (fr && r.result !== fr) return false;
    if (fp && String(r.pass) !== fp) return false;
    return true;
  });
}

function render() {
  let data = getFiltered();
  if (sortCol) {
    data.sort((a,b) => {
      let va = a[sortCol], vb = b[sortCol];
      if (typeof va === 'string') return sortAsc ? va.localeCompare(vb) : vb.localeCompare(va);
      return sortAsc ? va - vb : vb - va;
    });
  }
  tbody.innerHTML = data.map((r,i) => '<tr class="run-row" data-idx="'+runs.indexOf(r)+'">'
    +'<td>'+escapeJS(r.scenario)+'</td><td>'+escapeJS(r.config)+'</td><td>'+escapeJS(r.outcome)+'</td>'
    +'<td class="'+(r.pass?'pass':'fail')+'">'+r.pass+'</td>'
    +'<td>'+r.routing_pct.toFixed(1)+'%</td><td>'+r.latency_ms+'ms</td>'
    +'<td>$'+r.est_cost_usd.toFixed(4)+'</td><td>'+r.input_tokens+'</td>'
    +'<td>'+r.output_tokens+'</td><td>'+r.tool_calls+'</td><td>'+r.findings.length+'</td></tr>'
  ).join('');
  tbody.querySelectorAll('.run-row').forEach(tr => {
    tr.style.cursor = 'pointer';
    tr.addEventListener('click', () => openDrilldown(runs[tr.dataset.idx]));
  });
}

function openDrilldown(r) {
  drillTitle.textContent = r.scenario + ' / ' + r.config;
  drillContent.innerHTML = [
    ['Run ID', r.run_id], ['Result', r.result], ['Outcome', r.outcome], ['Pass', r.pass],
    ['Latency', r.latency_ms+'ms'], ['Cost', '$'+r.est_cost_usd.toFixed(4)],
    ['Routing', r.routing_pct.toFixed(1)+'%'], ['Tool Calls', r.tool_calls],
    ['Input Tokens', r.input_tokens], ['Output Tokens', r.output_tokens],
    ['Judge', r.judge], ['Trace', r.trace_file]
  ].map(([k,v]) => '<dt>'+escapeJS(k)+'</dt><dd>'+escapeJS(String(v))+'</dd>').join('');
  drillFindings.innerHTML = r.findings.length
    ? '<h4>Findings</h4>' + r.findings.map(f =>
      '<div class="'+escapeJS(f.severity)+'"><strong>'+escapeJS(f.severity)+'</strong> <code>'+escapeJS(f.code)+'</code>: '+escapeJS(f.message)+'</div>'
    ).join('')
    : '<p style="opacity:.5">No findings</p>';
  drilldown.classList.add('open');
  drilldown.scrollIntoView({behavior:'smooth'});
}

search.addEventListener('input', render);
filterResult.addEventListener('change', render);
filterPass.addEventListener('change', render);
render();
</script>
</body>
</html>`

// escapeJS escapes a string for safe use inside JavaScript string literals.
// It replaces characters that have special meaning in JS or HTML contexts.
func escapeJS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `&`, `\&`)
	s = strings.ReplaceAll(s, `<`, `\<`)
	s = strings.ReplaceAll(s, `>`, `\>`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// toFloat64 converts a numeric value to float64 for template math functions.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	default:
		return 0
	}
}

// DashboardFuncs adds custom template functions for the dashboard.
var dashboardFuncs = template.FuncMap{
	"mul": func(a, b any) float64 {
		return toFloat64(a) * toFloat64(b)
	},
	"divf": func(a, b any) float64 {
		d := toFloat64(b)
		if d == 0 { return 0 }
		return toFloat64(a) / d
	},
	"latencyBar": func(v, max int64) float64 {
		if max == 0 { return 0 }
		return float64(v) / float64(max) * 100
	},
	"costBar": func(v, max float64) float64 {
		if max == 0 { return 0 }
		return v / max * 100
	},
	"json": func(v any) template.JS {
		b, _ := json.Marshal(v)
		return template.JS(b)
	},
	"escapeJS": func(s string) template.JS {
		return template.JS(escapeJS(s))
	},
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(dashboardFuncs).Parse(dashboardHTML))

// Dashboard renders an interactive HTML dashboard with drill-down.
func Dashboard(suite string, runs []store.Run, regs []store.Regression) (string, error) {
	d := buildDashboardData(suite, runs, regs)
	var buf bytes.Buffer
	if err := dashboardTmpl.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render dashboard: %w", err)
	}
	return buf.String(), nil
}

// Command mettle runs evaluation suites end-to-end and enforces the CI gate
// (ADR-009): spec -> runner -> metrics -> regression store -> report.
//
// Usage:
//
//	mettle run --spec <file.yaml> [--store eval.db] [--traces traces] [--report report.md] [--html report.html]
//	mettle report [--store eval.db] [--suite NAME] [--report report.md] [--html report.html]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mettle/internal/agent"
	"mettle/internal/metrics"
	"mettle/internal/report"
	"mettle/internal/runner"
	"mettle/internal/spec"
	"mettle/internal/store"
	"mettle/internal/trace"
)

const (
	defaultStore  = "eval.db"
	defaultTraces = "traces"
	defaultReport = "report.md"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mettle:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mettle <run|report> [flags]")
	fmt.Fprintln(os.Stderr, "  run    --spec <file.yaml> [--store path] [--traces dir] [--report path] [--html path]")
	fmt.Fprintln(os.Stderr, "  report [--store path] [--suite name] [--report path] [--html path]")
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to the evaluation spec (YAML)")
	storePath := fs.String("store", defaultStore, "SQLite regression store")
	tracesDir := fs.String("traces", defaultTraces, "directory for run traces")
	reportPath := fs.String("report", defaultReport, "markdown report output")
	htmlPath := fs.String("html", "", "optional HTML report output")
	_ = fs.Parse(args)
	if *specPath == "" {
		return fmt.Errorf("--spec is required")
	}
	return runPipeline(*specPath, *storePath, *tracesDir, *reportPath, *htmlPath)
}

// runPipeline executes the evaluation matrix and enforces the CI gate.
// It is a separate function so the end-to-end flow is testable.
func runPipeline(specPath, storePath, tracesDir, reportPath, htmlPath string) error {
	suite, err := spec.LoadSuite(specPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		return fmt.Errorf("create traces dir: %w", err)
	}

	st, err := store.Open(storePath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	r := &runner.Runner{Agent: agent.Demo{}, TraceDir: tracesDir}
	results, err := r.RunSuite(ctx, suite)
	if err != nil {
		return err
	}

	scByName := make(map[string]spec.Scenario, len(suite.Scenarios))
	for _, sc := range suite.Scenarios {
		scByName[sc.Name] = sc
	}
	cfgByName := make(map[string]spec.RunConfig, len(suite.Configs))
	for _, cfg := range suite.Configs {
		cfgByName[cfg.Name] = cfg
	}
	if len(suite.Configs) == 0 {
		cfgByName["default"] = spec.RunConfig{Name: "default", Agent: suite.Defaults.Agent, Judge: suite.Defaults.Judge, Budget: suite.Defaults.Budget}
	}

	failedRuns := 0
	for _, res := range results {
		sc, ok := scByName[res.Scenario]
		if !ok {
			return fmt.Errorf("scenario %q not found", res.Scenario)
		}
		cfg, ok := cfgByName[res.Config]
		if !ok {
			return fmt.Errorf("config %q not found", res.Config)
		}
		evs, err := trace.Read(res.TraceFile)
		if err != nil {
			return err
		}
		mres, err := metrics.Compute(metrics.Input{
			RunID: res.RunID, Scenario: sc, Config: cfg.Name, Budget: cfg.Budget, Events: evs,
		})
		if err != nil {
			return err
		}
		if !mres.Pass {
			failedRuns++
		}
		meta := store.Meta{Suite: suite.Name, Judge: judgeLabel(suite, cfg), TraceFile: res.TraceFile}
		if err := st.SaveRun(ctx, mres, meta); err != nil {
			return err
		}
	}

	runs, err := st.ListRuns(ctx, suite.Name)
	if err != nil {
		return err
	}
	regs, err := st.CompareSuite(ctx, suite.Name)
	if err != nil {
		return err
	}

	md := report.Markdown(suite.Name, runs, regs)
	if reportPath != "" {
		if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
			return fmt.Errorf("create report dir: %w", err)
		}
		if err := os.WriteFile(reportPath, []byte(md), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	fmt.Print(md)
	if htmlPath != "" {
		h, err := report.HTML(suite.Name, runs, regs)
		if err != nil {
			return err
		}
		if err := os.WriteFile(htmlPath, []byte(h), 0o644); err != nil {
			return fmt.Errorf("write html report: %w", err)
		}
	}

	// CI gate (ADR-009): this run fails on critical findings or regressions.
	regressions := 0
	for _, reg := range regs {
		if reg.Compared && reg.IsRegression {
			regressions++
		}
	}
	if failedRuns > 0 || regressions > 0 {
		return fmt.Errorf("gate failed: %d failing run(s), %d regression(s)", failedRuns, regressions)
	}
	return nil
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	storePath := fs.String("store", defaultStore, "SQLite regression store")
	suiteName := fs.String("suite", "", "filter by suite name (default: all)")
	reportPath := fs.String("report", defaultReport, "markdown report output")
	htmlPath := fs.String("html", "", "optional HTML report output")
	_ = fs.Parse(args)

	st, err := store.Open(*storePath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	runs, err := st.ListRuns(ctx, *suiteName)
	if err != nil {
		return err
	}
	regs, err := st.CompareSuite(ctx, *suiteName)
	if err != nil {
		return err
	}

	title := *suiteName
	if title == "" {
		title = "all suites"
	}
	md := report.Markdown(title, runs, regs)
	if *reportPath != "" {
		if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
			return fmt.Errorf("create report dir: %w", err)
		}
		if err := os.WriteFile(*reportPath, []byte(md), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	fmt.Print(md)
	if *htmlPath != "" {
		h, err := report.HTML(title, runs, regs)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*htmlPath, []byte(h), 0o644); err != nil {
			return fmt.Errorf("write html report: %w", err)
		}
	}
	return nil
}

// judgeLabel mirrors the runner's pinning logic for the store meta.
func judgeLabel(suite *spec.EvalSuite, cfg spec.RunConfig) string {
	judge := cfg.Judge
	if judge.Provider == "" {
		judge = suite.Defaults.Judge
	}
	if judge.Provider == "" {
		return "unset"
	}
	return judge.Provider + "/" + judge.Model
}
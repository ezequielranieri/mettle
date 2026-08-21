// Command mettle runs evaluation suites end-to-end and enforces the CI gate
// (ADR-009): spec -> runner -> metrics -> regression store -> report.
//
// Usage:
//
//	mettle run --spec <file.yaml> [--store eval.db] [--traces traces] [--report report.md] [--html report.html]
//	mettle report [--store eval.db] [--suite NAME] [--report report.md] [--html report.html]
//	mettle calibrate [--store path]... [--golden path]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mettle/internal/agent"
	"mettle/internal/export"
	"mettle/internal/judge"
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

// version is set at build time via -ldflags.
var version = "dev"

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
	case "dashboard":
		err = cmdDashboard(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "calibrate":
		err = cmdCalibrate(os.Args[2:])
	case "version":
		fmt.Printf("mettle %s\n", version)
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
	fmt.Fprintln(os.Stderr, "usage: mettle <run|report|dashboard|export|calibrate|version> [flags]")
	fmt.Fprintln(os.Stderr, "  run       --spec <file.yaml> [--store path] [--traces dir] [--report path] [--html path]")
	fmt.Fprintln(os.Stderr, "            [--agent demo|llm] [--provider p] [--model m] [--judge-provider p] [--judge-model m]")
	fmt.Fprintln(os.Stderr, "            [--scenario name] [--config name] [--max-steps n] [--dry-run] [--slice N/M]")
	fmt.Fprintln(os.Stderr, "  report    [--store path] [--suite name] [--report path] [--html path]")
	fmt.Fprintln(os.Stderr, "  dashboard [--store path] [--suite name] [--output path]")
	fmt.Fprintln(os.Stderr, "  export    --platform langsmith|braintrust|json [--store path] [--suite name] [--endpoint path]")
	fmt.Fprintln(os.Stderr, "  calibrate [--store path]... [--golden path]")
	fmt.Fprintln(os.Stderr, "  version   print version")
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to the evaluation spec (YAML)")
	storePath := fs.String("store", defaultStore, "SQLite regression store")
	tracesDir := fs.String("traces", defaultTraces, "directory for run traces")
	reportPath := fs.String("report", defaultReport, "markdown report output")
	htmlPath := fs.String("html", "", "optional HTML report output")
	agentKind := fs.String("agent", "demo", "agent under test: demo (deterministic, CI) | llm (chat endpoint, needs API keys)")
	provider := fs.String("provider", "", "provider for --agent llm: groq | gemini | ollama (default: spec defaults)")
	model := fs.String("model", "", "model for --agent llm (default: spec defaults)")
	judgeProvider := fs.String("judge-provider", "", "provider for the semantic judge (default: spec defaults)")
	judgeModel := fs.String("judge-model", "", "model for the semantic judge (default: spec defaults)")
	scenarioFilter := fs.String("scenario", "", "run only this scenario name from the suite")
	configFilter := fs.String("config", "", "run only this config name from the suite")
	maxSteps := fs.Int("max-steps", agent.DefaultMaxSteps, "max LLM steps per run (--agent llm)")
	dryRun := fs.Bool("dry-run", false, "estimate cost without running the suite")
	sliceFlag := fs.String("slice", "", "run slice N of M for CI parallelism (e.g., '1/4')")
	_ = fs.Parse(args)
	if *specPath == "" {
		return fmt.Errorf("--spec is required")
	}
	if *dryRun {
		return runForecast(*specPath, *provider, *model, *judgeProvider, *judgeModel, *scenarioFilter, *configFilter, *maxSteps, *sliceFlag)
	}
	return runPipeline(*specPath, *storePath, *tracesDir, *reportPath, *htmlPath, *agentKind, *provider, *model, *judgeProvider, *judgeModel, *scenarioFilter, *configFilter, *maxSteps, *sliceFlag)
}

// runForecast estimates cost without running the suite. Useful for budget
// planning and sanity checks before committing to a large eval run.
func runForecast(specPath, provider, model, judgeProvider, judgeModel, scenarioFilter, configFilter string, maxSteps int, sliceFlag string) error {
	suite, err := spec.LoadSuite(specPath)
	if err != nil {
		return err
	}

	// Apply filters
	if scenarioFilter != "" {
		var kept []spec.Scenario
		for _, sc := range suite.Scenarios {
			if sc.Name == scenarioFilter {
				kept = append(kept, sc)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("scenario %q not found in suite %s", scenarioFilter, suite.Name)
		}
		suite.Scenarios = kept
	}
	if configFilter != "" {
		var kept []spec.RunConfig
		for _, cfg := range suite.Configs {
			if cfg.Name == configFilter {
				kept = append(kept, cfg)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("config %q not found in suite %s", configFilter, suite.Name)
		}
		suite.Configs = kept
	}

	// Resolve models
	if provider != "" && model == "" {
		model = suite.Defaults.Agent.Model
	}
	if judgeProvider != "" && judgeModel == "" {
		judgeModel = suite.Defaults.Judge.Model
	}

	// Handle slice flag - calculate slice boundaries
	if sliceFlag != "" {
		var sliceNum, totalSlices int
		if _, err := fmt.Sscanf(sliceFlag, "%d/%d", &sliceNum, &totalSlices); err != nil {
			return fmt.Errorf("invalid --slice format %q (expected N/M)", sliceFlag)
		}
		if sliceNum < 1 || sliceNum > totalSlices {
			return fmt.Errorf("slice %d/%d: sliceNum must be between 1 and %d", sliceNum, totalSlices, totalSlices)
		}

		// Count total matrix size
		totalRuns := len(suite.Scenarios) * len(suite.Configs)
		if totalRuns == 0 {
			totalRuns = len(suite.Scenarios) // no configs = default config
		}

		// Calculate slice boundaries
		perSlice := totalRuns / totalSlices
		remainder := totalRuns % totalSlices

		start := (sliceNum - 1) * perSlice
		end := start + perSlice
		if sliceNum <= remainder {
			start += sliceNum - 1
			end = start + perSlice + 1
		} else {
			start += remainder
			end = start + perSlice
		}
		if start >= totalRuns {
			return fmt.Errorf("slice %d/%d: no runs in this slice", sliceNum, totalSlices)
		}
		if end > totalRuns {
			end = totalRuns
		}

		// For forecast, we estimate the slice size
		sliceSize := end - start
		fullScenarios := len(suite.Scenarios)
		fullConfigs := len(suite.Configs)
		if fullConfigs == 0 {
			fullConfigs = 1
		}
		totalMatrix := fullScenarios * fullConfigs

		// Scale scenarios proportionally
		scaledScenarios := (fullScenarios * sliceSize) / totalMatrix
		if scaledScenarios == 0 {
			scaledScenarios = 1
		}
		suite.Scenarios = suite.Scenarios[:scaledScenarios]
	}

	f := metrics.Forecast(metrics.ForecastInput{
		Suite:      *suite,
		MaxSteps:   maxSteps,
		Scenario:   scenarioFilter,
		Config:     configFilter,
		JudgeModel: judgeModel,
		AgentModel: model,
	})

	fmt.Print(metrics.FormatForecast(f))
	return nil
}

// runPipeline executes the evaluation matrix and enforces the CI gate.
// It is a separate function so the end-to-end flow is testable.
func runPipeline(specPath, storePath, tracesDir, reportPath, htmlPath, agentKind, provider, model, judgeProvider, judgeModel, scenarioFilter, configFilter string, maxSteps int, sliceFlag string) error {
	suite, err := spec.LoadSuite(specPath)
	if err != nil {
		return err
	}
	// Selective runs: --scenario/--config narrow the suite before the runner
	// sees it. Useful for cheap live validation of a single case
	// (ADR-015/016/017) and for model comparisons on the same scenario.
	if scenarioFilter != "" {
		var kept []spec.Scenario
		for _, sc := range suite.Scenarios {
			if sc.Name == scenarioFilter {
				kept = append(kept, sc)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("scenario %q not found in suite %s", scenarioFilter, suite.Name)
		}
		suite.Scenarios = kept
	}
	if configFilter != "" {
		var kept []spec.RunConfig
		for _, cfg := range suite.Configs {
			if cfg.Name == configFilter {
				kept = append(kept, cfg)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("config %q not found in suite %s", configFilter, suite.Name)
		}
		suite.Configs = kept
	}
	ag, err := buildAgent(agentKind, provider, model, maxSteps, suite)
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
	r := &runner.Runner{Agent: ag, TraceDir: tracesDir}

	// Parse slice flag (e.g., "1/4" means slice 1 of 4)
	var results []runner.Result
	if sliceFlag != "" {
		var sliceNum, totalSlices int
		if _, err := fmt.Sscanf(sliceFlag, "%d/%d", &sliceNum, &totalSlices); err != nil {
			return fmt.Errorf("invalid --slice format %q (expected N/M)", sliceFlag)
		}
		results, err = r.RunSlice(ctx, suite, sliceNum, totalSlices)
		if err != nil {
			return err
		}
	} else {
		results, err = r.RunSuite(ctx, suite)
		if err != nil {
			return err
		}
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
	var outcomes []string
	var passes []bool
	// Semantic judging (ADR-006/008): one LLM-as-judge per completed run,
	// using the run's pinned judge from the spec defaults. Built lazily;
	// only for --agent llm so the deterministic CI path needs no keys.
	var semantic *judge.Client
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
		// The effective judge is a single source of truth: it both labels the
		// persisted run (ADR-008 pin) and builds the semantic client, so the
		// store pin always matches the judge that produced the verdict.
		judgeCfg := effectiveJudge(cfg, suite, judgeProvider, judgeModel)
		if agentKind == "llm" && mres.Outcome == "pass" && semantic == nil && judgeCfg.Provider != "" {
			semantic, err = buildLLMClient(judgeCfg.Provider, judgeCfg.Model)
			if err != nil {
				return err
			}
		}
		if semantic != nil && mres.Outcome == "pass" {
			v, judgeErr := semantic.Judge(ctx, judge.BuildRequest(sc, evs))
			applyVerdict(&mres, v, judgeErr)
		}
		outcomes = append(outcomes, mres.Outcome)
		passes = append(passes, mres.Pass)
		if mres.Outcome != "pass" || !mres.Pass {
			failedRuns++
		}
		meta := store.Meta{Suite: suite.Name, Judge: judgeLabel(judgeCfg), TraceFile: res.TraceFile}
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

	// CI gate (ADR-009): this run fails on errored runs, critical findings
	// or regressions. An errored run is NOT a pass — reporting it green would
	// be lying by omission (ADR-006).
	regressions := 0
	for _, reg := range regs {
		if reg.Compared && reg.IsRegression {
			regressions++
		}
	}
	if gateFailed(outcomes, passes, regressions) {
		return fmt.Errorf("gate failed: %d failing run(s), %d regression(s)", failedRuns, regressions)
	}
	return nil
}

// applyVerdict folds a semantic judgment into the run result. A judge that
// cannot produce a verdict is a critical finding, never a silent pass
// (ADR-006). verdict "fail" fails the run; "warning" is a warning; a clean
// "pass" adds nothing.
func applyVerdict(mres *metrics.Result, v judge.Verdict, judgeErr error) {
	if judgeErr != nil {
		mres.Findings = append(mres.Findings, metrics.Finding{
			Severity: metrics.SeverityCritical,
			Code:     "judge_error",
			Message:  fmt.Sprintf("semantic judgment failed: %v", judgeErr),
		})
		mres.Pass = metrics.PassFromFindings(mres.Findings)
		return
	}
	switch v.Verdict {
	case "fail":
		mres.Findings = append(mres.Findings, metrics.Finding{
			Severity: metrics.SeverityCritical,
			Code:     "semantic_fail",
			Message:  v.Reason,
		})
	case "warning":
		mres.Findings = append(mres.Findings, metrics.Finding{
			Severity: metrics.SeverityWarning,
			Code:     "semantic_warning",
			Message:  v.Reason,
		})
	}
	for _, f := range v.Findings {
		mres.Findings = append(mres.Findings, metrics.Finding{
			Severity: metrics.SeverityInfo,
			Code:     "judge",
			Message:  f,
		})
	}
	mres.Pass = metrics.PassFromFindings(mres.Findings)
}

// gateFailed is the CI gate predicate, extracted for testing: any run that
// errored or produced critical findings, or any active regression, fails.
func gateFailed(outcomes []string, passes []bool, regressions int) bool {
	for i := range outcomes {
		if outcomes[i] != "pass" || !passes[i] {
			return true
		}
	}
	return regressions > 0
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

func cmdDashboard(args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	storePath := fs.String("store", defaultStore, "SQLite regression store")
	suiteName := fs.String("suite", "", "filter by suite name (default: all)")
	outputPath := fs.String("output", "dashboard.html", "dashboard HTML output")
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

	h, err := report.Dashboard(title, runs, regs)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		return fmt.Errorf("create dashboard dir: %w", err)
	}
	if err := os.WriteFile(*outputPath, []byte(h), 0o644); err != nil {
		return fmt.Errorf("write dashboard: %w", err)
	}
	fmt.Printf("Dashboard written to %s\n", *outputPath)
	return nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	storePath := fs.String("store", defaultStore, "SQLite regression store")
	suiteName := fs.String("suite", "", "filter by suite name (default: all)")
	platform := fs.String("platform", "", "export platform: langsmith | braintrust | json (required)")
	endpoint := fs.String("endpoint", "", "API endpoint or output path for json")
	_ = fs.Parse(args)

	if *platform == "" {
		return fmt.Errorf("--platform is required (langsmith|braintrust|json)")
	}

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

	if len(runs) == 0 {
		fmt.Println("No runs to export")
		return nil
	}

	cfg := export.Config{
		APIKey:   os.Getenv("LANGCHAIN_API_KEY"),
		Endpoint: *endpoint,
		Project:  *suiteName,
	}

	// Set platform-specific defaults from environment
	switch *platform {
	case "langsmith":
		if cfg.APIKey == "" {
			cfg.APIKey = os.Getenv("LANGSMITH_API_KEY")
		}
		if cfg.Endpoint == "" {
			cfg.Endpoint = os.Getenv("LANGCHAIN_ENDPOINT")
		}
	case "braintrust":
		if cfg.APIKey == "" {
			cfg.APIKey = os.Getenv("BRAINTRUST_API_KEY")
		}
	}

	exp, err := export.New(*platform, cfg)
	if err != nil {
		return err
	}

	return exp.Export(ctx, runs)
}

// buildAgent constructs the agent under test. demo is deterministic (CI
// gate, no keys); llm is the real chat agent (ADR-012) and defaults its
// provider/model from the suite spec.
func buildAgent(kind, provider, model string, maxSteps int, suite *spec.EvalSuite) (runner.Agent, error) {
	switch kind {
	case "demo":
		return agent.Demo{}, nil
	case "llm":
		if provider == "" {
			provider = suite.Defaults.Agent.Provider
		}
		if model == "" {
			model = suite.Defaults.Agent.Model
		}
		c, err := buildLLMClient(provider, model)
		if err != nil {
			return nil, err
		}
		return &agent.LLM{Client: c, MaxSteps: maxSteps}, nil
	default:
		return nil, fmt.Errorf("unknown agent %q (demo|llm)", kind)
	}
}

func buildLLMClient(provider, model string) (*judge.Client, error) {
	switch provider {
	case "groq":
		return judge.NewGroq(model), nil
	case "gemini":
		return judge.NewGemini(model), nil
	case "ollama":
		return judge.NewOllama(model), nil
	case "cerebras":
		return judge.NewCerebras(model), nil
	case "sambanova":
		return judge.NewSambaNova(model), nil
	case "openrouter":
		return judge.NewOpenRouter(model), nil
	case "":
		return nil, fmt.Errorf("--agent llm requires a provider (groq|gemini|ollama|cerebras|sambanova|openrouter) in the spec or --provider")
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}

// effectiveJudge resolves the judge for a run: scenario config first, suite
// defaults second, explicit CLI overrides last. The same value labels the
// persisted run (ADR-008) and builds the semantic client, so the store pin
// always matches the judge that actually produced the verdict.
func effectiveJudge(cfg spec.RunConfig, suite *spec.EvalSuite, judgeProvider, judgeModel string) spec.JudgeConfig {
	judge := cfg.Judge
	if judge.Provider == "" {
		judge = suite.Defaults.Judge
	}
	if judgeProvider != "" {
		judge.Provider = judgeProvider
	}
	if judgeModel != "" {
		judge.Model = judgeModel
	}
	return judge
}

// judgeLabel renders the effective judge as a stable store pin.
func judgeLabel(judge spec.JudgeConfig) string {
	if judge.Provider == "" {
		return "unset"
	}
	return judge.Provider + "/" + judge.Model
}

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"mettle/internal/metrics"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func result(runID string, pass bool, findings ...metrics.Finding) metrics.Result {
	return metrics.Result{
		RunID:        runID,
		Scenario:     "scenario-a",
		Config:       "tools-3",
		Outcome:      "pass",
		Pass:         pass,
		LatencyMS:    1200,
		EstCostUSD:   0.001,
		ToolCalls:    2,
		RoutingPct:   100,
		InputTokens:  100,
		OutputTokens: 50,
		Findings:     findings,
	}
}

func critical(code, message string) metrics.Finding {
	return metrics.Finding{Severity: "critical", Code: code, Message: message}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	s := openTest(t)
	res := result("run-1", false, critical("out_of_scope_call", "call to lookup_record tenant=evil outside scope"))
	if err := s.SaveRun(context.Background(), res, Meta{Suite: "suite", Judge: "groq/m", TraceFile: "traces/run-1.jsonl"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	got, err := s.LatestRun(context.Background(), "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got == nil {
		t.Fatal("LatestRun = nil, want run")
	}
	if got.RunID != "run-1" || got.Pass || got.Outcome != "pass" {
		t.Errorf("run = %+v", got)
	}
	if got.Judge != "groq/m" || got.TraceFile != "traces/run-1.jsonl" {
		t.Errorf("meta = judge %q trace %q", got.Judge, got.TraceFile)
	}
	if len(got.Findings) != 1 || got.Findings[0].Code != "out_of_scope_call" {
		t.Errorf("findings = %+v", got.Findings)
	}
}

func TestSaveIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.SaveRun(ctx, result("run-1", true), Meta{Suite: "s"}); err != nil {
			t.Fatalf("SaveRun %d: %v", i, err)
		}
	}
	got, err := s.LatestRun(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got == nil || got.RunID != "run-1" {
		t.Fatalf("run = %+v, want single run-1", got)
	}
}

func TestLatestRunReturnsNilWithoutHistory(t *testing.T) {
	s := openTest(t)
	got, err := s.LatestRun(context.Background(), "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got != nil {
		t.Fatalf("LatestRun = %+v, want nil", got)
	}
}

func TestCompareInsufficientHistory(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveRun(ctx, result("run-1", true), Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	reg, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if reg.Compared {
		t.Error("Compared = true, want false (single run)")
	}
	if reg.IsRegression {
		t.Error("IsRegression = true with insufficient history")
	}
}

func TestCompareNewCriticalFindingIsRegression(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveRun(ctx, result("run-1", true), Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	reg := result("run-2", false, critical("out_of_scope_call", "call to lookup_record tenant=evil outside scope"))
	if err := s.SaveRun(ctx, reg, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !got.Compared || !got.IsRegression {
		t.Fatalf("Compared/IsRegression = %v/%v, want true/true", got.Compared, got.IsRegression)
	}
	if len(got.Reasons) == 0 {
		t.Error("no reasons")
	}
}

func TestCompareRoutingDropIsRegression(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	good := result("run-1", true)
	good.RoutingPct = 100
	if err := s.SaveRun(ctx, good, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	bad := result("run-2", true)
	bad.RoutingPct = 80
	if err := s.SaveRun(ctx, bad, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !got.IsRegression {
		t.Fatal("IsRegression = false, want true (routing 100 -> 80)")
	}
}

func TestCompareCleanRunIsNotRegression(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveRun(ctx, result("run-1", true), Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	second := result("run-2", true)
	second.RoutingPct = 100
	if err := s.SaveRun(ctx, second, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !got.Compared {
		t.Fatal("Compared = false, want true")
	}
	if got.IsRegression {
		t.Errorf("IsRegression = true, want false: %v", got.Reasons)
	}
}

func TestCompareLatencyIncreaseIsRegression(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	fast := result("run-1", true)
	fast.LatencyMS = 1000
	if err := s.SaveRun(ctx, fast, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	slow := result("run-2", true)
	slow.LatencyMS = 5000 // +400%
	if err := s.SaveRun(ctx, slow, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !got.IsRegression {
		t.Error("IsRegression = false, want true (latency +400%)")
	}
}

func TestCompareIgnoresSubSecondLatencyNoise(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	fast := result("run-1", true)
	fast.LatencyMS = 1
	if err := s.SaveRun(ctx, fast, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}
	slow := result("run-2", true)
	slow.LatencyMS = 7 // +600% but sub-second: CI jitter, not a regression
	if err := s.SaveRun(ctx, slow, Meta{Suite: "s"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Compare(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if got.IsRegression {
		t.Errorf("IsRegression = true, want false (sub-second noise): %v", got.Reasons)
	}
}

func TestListRunsMostRecentFirst(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		r := result("run-"+string(rune('0'+i)), true)
		if err := s.SaveRun(ctx, r, Meta{Suite: "suite-a"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListRuns(ctx, "suite-a")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("runs = %d, want 3", len(all))
	}
	if all[0].RunID != "run-3" {
		t.Errorf("first run = %s, want run-3 (most recent first)", all[0].RunID)
	}
	filtered, err := s.ListRuns(ctx, "other-suite")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(filtered) != 0 {
		t.Errorf("filtered runs = %d, want 0", len(filtered))
	}
}

func TestCompareSuiteOverPairs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveRun(ctx, result("run-1", true), Meta{Suite: "suite-a"}); err != nil {
		t.Fatal(err)
	}
	bad := result("run-2", false, critical("out_of_scope_call", "call to lookup_record tenant=evil outside scope"))
	if err := s.SaveRun(ctx, bad, Meta{Suite: "suite-a"}); err != nil {
		t.Fatal(err)
	}
	other := result("run-3", true)
	other.Scenario = "scenario-b"
	if err := s.SaveRun(ctx, other, Meta{Suite: "suite-a"}); err != nil {
		t.Fatal(err)
	}

	regs, err := s.CompareSuite(ctx, "suite-a")
	if err != nil {
		t.Fatalf("CompareSuite: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("regressions = %d, want 2 pairs", len(regs))
	}
	for _, r := range regs {
		if r.Scenario == "scenario-a" && !r.IsRegression {
			t.Error("scenario-a should be a regression")
		}
		if r.Scenario == "scenario-b" && r.IsRegression {
			t.Error("scenario-b (single run) must not be a regression")
		}
	}
}

// --- metric_scores persistence (METR-3) ---

func metricScores() []metrics.MetricScore {
	return []metrics.MetricScore{
		{Name: "routing_accuracy", Value: 100, Status: metrics.MetricStatusComputed, Source: metrics.MetricSourceDerived},
		{Name: "data_leakage", Value: 0, Status: metrics.MetricStatusComputed, Source: metrics.MetricSourceHybrid},
		{Name: "injection_resistance", Value: 0, Status: metrics.MetricStatusNotComputed, Source: metrics.MetricSourceJudge},
	}
}

func TestSaveRunTwiceNoDuplicateMetricScores(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	r := result("run-1", true)
	r.Metrics = metricScores()
	for i := 0; i < 2; i++ {
		if err := s.SaveRun(ctx, r, Meta{Suite: "s"}); err != nil {
			t.Fatalf("SaveRun %d: %v", i, err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM metric_scores WHERE run_id = ?`, "run-1").Scan(&n); err != nil {
		t.Fatalf("count metric_scores: %v", err)
	}
	if n != 3 {
		t.Errorf("metric_scores rows = %d, want 3 (no duplicate run_id,metric)", n)
	}
	got, err := s.LatestRun(ctx, "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if len(got.MetricScores) != 3 {
		t.Fatalf("MetricScores = %d, want 3", len(got.MetricScores))
	}
	// runs columns untouched by the score upsert.
	if got.LatencyMS != 1200 || !got.Pass || got.Scenario != "scenario-a" {
		t.Errorf("run columns changed: %+v", got)
	}
}

func TestOpenMigratesLegacyDBWithEmptyScores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	// Pre-metric_scores schema: only the runs table exists.
	if _, err := legacy.Exec(`CREATE TABLE runs (
		run_id TEXT PRIMARY KEY,
		suite TEXT NOT NULL,
		scenario TEXT NOT NULL,
		config TEXT NOT NULL,
		outcome TEXT NOT NULL,
		pass INTEGER NOT NULL,
		judge TEXT NOT NULL,
		trace_file TEXT NOT NULL,
		created_at TEXT NOT NULL,
		latency_ms INTEGER NOT NULL,
		est_cost_usd REAL NOT NULL,
		tool_calls INTEGER NOT NULL,
		out_of_scope_calls INTEGER NOT NULL,
		silent_restrictions INTEGER NOT NULL,
		routing_pct REAL NOT NULL,
		input_tokens INTEGER NOT NULL,
		output_tokens INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy runs: %v", err)
	}
	legacy.Close()

	s, err := Open(path) // migrates: adds metric_scores
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer s.Close()

	if err := s.SaveRun(context.Background(), result("run-1", true), Meta{Suite: "s"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	got, err := s.LatestRun(context.Background(), "scenario-a", "tools-3")
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got == nil || got.RunID != "run-1" {
		t.Fatalf("legacy run lost: %+v", got)
	}
	if len(got.MetricScores) != 0 {
		t.Errorf("legacy run MetricScores = %v, want empty (pre-change runs have no scores)", got.MetricScores)
	}
}

func TestListRunsLoadsMetricScores(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	r := result("run-1", true)
	r.Metrics = metricScores()
	if err := s.SaveRun(ctx, r, Meta{Suite: "s"}); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	all, err := s.ListRuns(ctx, "s")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("runs = %d, want 1", len(all))
	}
	got := all[0]
	if len(got.MetricScores) != 3 {
		t.Fatalf("MetricScores = %d, want 3", len(got.MetricScores))
	}
	byName := map[string]MetricScore{}
	for _, m := range got.MetricScores {
		byName[m.Metric] = m
	}
	ra, ok := byName["routing_accuracy"]
	if !ok || ra.Value != 100 || ra.Status != string(metrics.MetricStatusComputed) || ra.Source != string(metrics.MetricSourceDerived) {
		t.Errorf("routing_accuracy score = %+v, want computed/derived 100", ra)
	}
	inj, ok := byName["injection_resistance"]
	if !ok || inj.Value != 0 || inj.Status != string(metrics.MetricStatusNotComputed) || inj.Source != string(metrics.MetricSourceJudge) {
		t.Errorf("injection_resistance score = %+v, want not_computed/judge 0", inj)
	}
}

// Package store persists run results in SQLite for regression comparison
// (ADR-008). Each run is a versioned artifact: the store is the regression
// memory of the framework, enabling before/after comparisons over time
// (ADR-009). SQLite runs in-process via modernc.org/sqlite (pure Go, no CGO).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"mettle/internal/metrics"
)

// Finding mirrors metrics.Finding for persistence.
type Finding struct {
	Severity string
	Code     string
	Message  string
}

// Run is one persisted evaluation run.
type Run struct {
	RunID              string
	Suite              string
	Scenario           string
	Config             string
	Outcome            string
	Pass               bool
	Judge              string
	TraceFile          string
	CreatedAt          time.Time
	LatencyMS          int64
	EstCostUSD         float64
	ToolCalls          int
	OutOfScopeCalls    int
	SilentRestrictions int
	RoutingPct         float64
	InputTokens        int
	OutputTokens       int
	Findings           []Finding
}

// Meta carries the run context the metrics result does not have.
type Meta struct {
	Suite     string
	Judge     string
	TraceFile string
}

// Store is a SQLite-backed regression store.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (
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
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
			severity TEXT NOT NULL,
			code TEXT NOT NULL,
			message TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_lookup ON runs(scenario, config, created_at)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate store: %w", err)
		}
	}
	return nil
}

// SaveRun persists a run result with its findings. Saving the same run_id
// again replaces it (idempotent).
func (s *Store) SaveRun(ctx context.Context, res metrics.Result, meta Meta) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save run: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO runs (
			run_id, suite, scenario, config, outcome, pass, judge, trace_file,
			created_at, latency_ms, est_cost_usd, tool_calls, out_of_scope_calls,
			silent_restrictions, routing_pct, input_tokens, output_tokens
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		res.RunID, meta.Suite, res.Scenario, res.Config, res.Outcome, boolInt(res.Pass),
		meta.Judge, meta.TraceFile, time.Now().UTC().Format(time.RFC3339Nano),
		res.LatencyMS, res.EstCostUSD, res.ToolCalls, res.OutOfScopeCalls,
		res.SilentRestrictions, res.RoutingPct, res.InputTokens, res.OutputTokens,
	); err != nil {
		return fmt.Errorf("save run: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM findings WHERE run_id = ?`, res.RunID); err != nil {
		return fmt.Errorf("save findings: %w", err)
	}
	for _, f := range res.Findings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO findings (run_id, severity, code, message) VALUES (?, ?, ?, ?)`,
			res.RunID, f.Severity, f.Code, f.Message); err != nil {
			return fmt.Errorf("save finding: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run: %w", err)
	}
	return nil
}

// LatestRun returns the most recent run for a scenario x config pair, or
// nil when no run exists yet.
func (s *Store) LatestRun(ctx context.Context, scenario, config string) (*Run, error) {
	rows, err := s.queryRuns(ctx, `WHERE scenario = ? AND config = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, scenario, config)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// ListRuns returns persisted runs for a suite (all suites when suite is
// empty), most recent first.
func (s *Store) ListRuns(ctx context.Context, suite string) ([]Run, error) {
	if suite != "" {
		return s.queryRuns(ctx, `WHERE suite = ? ORDER BY created_at DESC, rowid DESC`, suite)
	}
	return s.queryRuns(ctx, `ORDER BY created_at DESC, rowid DESC`)
}

// CompareSuite compares the two most recent runs of every scenario x config
// pair present in the store (optionally filtered by suite).
func (s *Store) CompareSuite(ctx context.Context, suite string) ([]Regression, error) {
	q := `SELECT DISTINCT scenario, config FROM runs`
	var args []any
	if suite != "" {
		q += ` WHERE suite = ?`
		args = append(args, suite)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query pairs: %w", err)
	}
	defer rows.Close()

	var out []Regression
	for rows.Next() {
		var sc, cfg string
		if err := rows.Scan(&sc, &cfg); err != nil {
			return nil, fmt.Errorf("scan pair: %w", err)
		}
		reg, err := s.Compare(ctx, sc, cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query pairs: %w", err)
	}
	return out, nil
}

// Regression is the verdict between the two most recent runs of a
// scenario x config pair.
type Regression struct {
	Scenario     string
	Config       string
	Compared     bool   // false when history is insufficient
	IsRegression bool
	Reasons      []string
	PrevRun      *Run
	LatestRun    *Run
}

// Compare detects regressions between the two most recent runs of a pair.
// A single run is not a regression; it just means history is insufficient.
func (s *Store) Compare(ctx context.Context, scenario, config string) (Regression, error) {
	reg := Regression{Scenario: scenario, Config: config}

	rows, err := s.queryRuns(ctx, `WHERE scenario = ? AND config = ? ORDER BY created_at DESC, rowid DESC LIMIT 2`, scenario, config)
	if err != nil {
		return reg, err
	}
	if len(rows) == 0 {
		return reg, nil
	}
	reg.LatestRun = &rows[0]
	if len(rows) == 1 {
		reg.Reasons = append(reg.Reasons, "insufficient history (need at least two runs)")
		return reg, nil
	}
	reg.Compared = true
	reg.PrevRun = &rows[1]

	latest, prev := reg.LatestRun, reg.PrevRun
	if latest.Pass && !prev.Pass {
		// Improvement, not a regression.
	} else if !latest.Pass && prev.Pass {
		reg.IsRegression = true
		reg.Reasons = append(reg.Reasons, "run regressed from pass to fail")
	}

	for _, f := range latest.Findings {
		if f.Severity != "critical" {
			continue
		}
		if !hasFinding(prev.Findings, f) {
			reg.IsRegression = true
			reg.Reasons = append(reg.Reasons, fmt.Sprintf("new critical finding: %s (%s)", f.Code, f.Message))
		}
	}

	if latest.RoutingPct < prev.RoutingPct-5 {
		reg.IsRegression = true
		reg.Reasons = append(reg.Reasons, fmt.Sprintf("routing accuracy dropped %.1f%% -> %.1f%%", prev.RoutingPct, latest.RoutingPct))
	}
	// Latency regression needs a meaningful baseline: sub-second runs are
	// noise in CI (1ms vs 7ms is scheduler jitter, not a regression).
	if prev.LatencyMS >= 100 && latest.LatencyMS > prev.LatencyMS*120/100 {
		reg.IsRegression = true
		reg.Reasons = append(reg.Reasons, fmt.Sprintf("latency increased %dms -> %dms", prev.LatencyMS, latest.LatencyMS))
	}
	if prev.EstCostUSD > 0 && latest.EstCostUSD > prev.EstCostUSD*120/100 {
		reg.IsRegression = true
		reg.Reasons = append(reg.Reasons, fmt.Sprintf("cost increased $%.4f -> $%.4f", prev.EstCostUSD, latest.EstCostUSD))
	}
	return reg, nil
}

func (s *Store) queryRuns(ctx context.Context, where string, args ...any) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, suite, scenario, config, outcome, pass, judge, trace_file,
			created_at, latency_ms, est_cost_usd, tool_calls, out_of_scope_calls,
			silent_restrictions, routing_pct, input_tokens, output_tokens
		FROM runs `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		var pass int
		var created string
		if err := rows.Scan(
			&r.RunID, &r.Suite, &r.Scenario, &r.Config, &r.Outcome, &pass, &r.Judge,
			&r.TraceFile, &created, &r.LatencyMS, &r.EstCostUSD, &r.ToolCalls,
			&r.OutOfScopeCalls, &r.SilentRestrictions, &r.RoutingPct,
			&r.InputTokens, &r.OutputTokens,
		); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		r.Pass = pass != 0
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			r.CreatedAt = t
		}
		r.Findings, err = s.loadFindings(ctx, r.RunID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	return out, nil
}

func (s *Store) loadFindings(ctx context.Context, runID string) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, code, message FROM findings WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("query findings: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.Severity, &f.Code, &f.Message); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func hasFinding(list []Finding, want Finding) bool {
	for _, f := range list {
		if f.Severity == want.Severity && f.Code == want.Code && f.Message == want.Message {
			return true
		}
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
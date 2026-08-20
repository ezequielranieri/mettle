// Package calib reports per-judge verdict accuracy against a human-authored
// golden set (ADR-008). The confusion matrix compares each judge's verdict
// (the persisted run pass/fail) with ground truth authored by a human after
// inspecting the run trace.
package calib

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"mettle/internal/store"
)

// Golden is one human-authored ground-truth verdict for a stored run.
type Golden struct {
	RunID        string `yaml:"run_id"`
	ExpectedPass bool   `yaml:"expected_pass"`
	Note         string `yaml:"note,omitempty"` // evidence for the human verdict
}

// LoadGolden reads and validates a golden set from a YAML file. An empty or
// duplicate run_id is an error: the set must unambiguously map one verdict
// per run.
func LoadGolden(path string) ([]Golden, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden: %w", err)
	}
	var doc struct {
		Runs []Golden `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse golden: %w", err)
	}
	seen := make(map[string]bool, len(doc.Runs))
	for i := range doc.Runs {
		g := &doc.Runs[i]
		if g.RunID == "" {
			return nil, fmt.Errorf("golden run %d: run_id is required", i)
		}
		if seen[g.RunID] {
			return nil, fmt.Errorf("golden: duplicate run_id %q", g.RunID)
		}
		seen[g.RunID] = true
	}
	return doc.Runs, nil
}

// Confusion is a confusion matrix where a FAIL verdict is the positive class
// (the judge detected a defect): TP means the judge flagged a real defect,
// FP that it flagged a compliant run, TN that it cleared a compliant run,
// and FN that it missed a real defect.
type Confusion struct {
	TP int // judge fail, expected fail
	FP int // judge fail, expected pass
	TN int // judge pass, expected pass
	FN int // judge pass, expected fail
}

// Agreement returns (TP+TN)/total, the share of verdicts matching ground
// truth; 0 when there are no checked runs.
func (c Confusion) Agreement() float64 {
	if c.Total() == 0 {
		return 0
	}
	return float64(c.TP+c.TN) / float64(c.Total())
}

// Precision returns TP/(TP+FP), how often a fail verdict is correct; 0 when
// the judge never issued a fail.
func (c Confusion) Precision() float64 {
	if c.TP+c.FP == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FP)
}

// Recall returns TP/(TP+FN), the share of real defects the judge caught; 0
// when there are no defects to catch.
func (c Confusion) Recall() float64 {
	if c.TP+c.FN == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FN)
}

// F1 returns the harmonic mean of precision and recall; 0 when either is
// undefined.
func (c Confusion) F1() float64 {
	p, r := c.Precision(), c.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// Total returns the number of checked verdicts.
func (c Confusion) Total() int {
	return c.TP + c.FP + c.TN + c.FN
}

// Result is the outcome of comparing runs against a golden set.
type Result struct {
	ByJudge   map[string]Confusion // keyed by run.Judge; empty judges bucket to "unknown"
	Aggregate Confusion
	Missing   []string // golden run_ids with no matching run
	Checked   int      // golden records with a matching run
}

// Evaluate compares judge verdicts in runs against the golden set and
// returns the confusion matrix per judge plus an aggregate. Golden records
// whose run is absent from runs are reported in Result.Missing rather than
// as an error, so a stale golden file is visible in the report instead of
// failing silently.
func Evaluate(runs []store.Run, goldens []Golden) (Result, error) {
	res := Result{ByJudge: make(map[string]Confusion)}
	runByID := make(map[string]store.Run, len(runs))
	for _, r := range runs {
		runByID[r.RunID] = r
	}
	for _, g := range goldens {
		run, ok := runByID[g.RunID]
		if !ok {
			res.Missing = append(res.Missing, g.RunID)
			continue
		}
		res.Checked++
		var c Confusion
		switch {
		case !run.Pass && !g.ExpectedPass:
			c.TP = 1
		case run.Pass && g.ExpectedPass:
			c.TN = 1
		case !run.Pass && g.ExpectedPass:
			c.FP = 1
		case run.Pass && !g.ExpectedPass:
			c.FN = 1
		}
		name := run.Judge
		if name == "" {
			name = "unknown"
		}
		res.ByJudge[name] = add(res.ByJudge[name], c)
		res.Aggregate = add(res.Aggregate, c)
	}
	return res, nil
}

func add(c, o Confusion) Confusion {
	c.TP += o.TP
	c.FP += o.FP
	c.TN += o.TN
	c.FN += o.FN
	return c
}

// Render formats an evaluation as a table: missing runs first, then one row
// per judge (sorted, "unknown" last) and a final aggregate row.
func Render(r Result) string {
	var b strings.Builder
	if len(r.Missing) > 0 {
		fmt.Fprintln(&b, "missing runs:")
		ids := append([]string(nil), r.Missing...)
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}
	for _, name := range sortedJudges(r.ByJudge) {
		b.WriteString(renderRow(name, r.ByJudge[name]))
	}
	b.WriteString(renderRow("aggregate", r.Aggregate))
	return b.String()
}

func sortedJudges(m map[string]Confusion) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		if name != "unknown" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := m["unknown"]; ok {
		names = append(names, "unknown")
	}
	return names
}

func renderRow(name string, c Confusion) string {
	return fmt.Sprintf("%-12s  TP=%d FP=%d TN=%d FN=%d  agreement=%.3f precision=%.3f recall=%.3f f1=%.3f  n=%d\n",
		name, c.TP, c.FP, c.TN, c.FN, c.Agreement(), c.Precision(), c.Recall(), c.F1(), c.Total())
}

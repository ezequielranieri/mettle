package calib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mettle/internal/store"
)

func writeGolden(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "golden.yaml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadGolden(t *testing.T) {
	path := writeGolden(t, `runs:
  - run_id: silent-restriction-must-log__tools-3__1755600000
    expected_pass: false
    note: "agent restricted without logging - confirmed defect"
  - run_id: privileged-tool-misuse__default__1755600001
    expected_pass: true
`)
	got, err := LoadGolden(path)
	if err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("goldens = %d, want 2", len(got))
	}
	if got[0].RunID != "silent-restriction-must-log__tools-3__1755600000" || got[0].ExpectedPass {
		t.Errorf("golden[0] = %+v", got[0])
	}
	if got[1].RunID != "privileged-tool-misuse__default__1755600001" || !got[1].ExpectedPass {
		t.Errorf("golden[1] = %+v", got[1])
	}
}

func TestLoadGoldenDuplicateRunID(t *testing.T) {
	path := writeGolden(t, `runs:
  - run_id: same
    expected_pass: true
  - run_id: same
    expected_pass: false
`)
	if _, err := LoadGolden(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("LoadGolden err = %v, want duplicate run_id", err)
	}
}

func TestLoadGoldenEmptyRunID(t *testing.T) {
	path := writeGolden(t, `runs:
  - expected_pass: true
`)
	if _, err := LoadGolden(path); err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Errorf("LoadGolden err = %v, want missing run_id", err)
	}
}

func TestLoadGoldenMissingFile(t *testing.T) {
	if _, err := LoadGolden(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("LoadGolden = nil, want read error")
	}
}

func TestConfusionMath(t *testing.T) {
	perfect := Confusion{TP: 3, TN: 7}
	if perfect.Agreement() != 1 || perfect.Precision() != 1 || perfect.Recall() != 1 || perfect.F1() != 1 {
		t.Errorf("perfect = %v/%v/%v/%v, want all 1",
			perfect.Agreement(), perfect.Precision(), perfect.Recall(), perfect.F1())
	}
	empty := Confusion{}
	if empty.Agreement() != 0 || empty.Precision() != 0 || empty.Recall() != 0 || empty.F1() != 0 {
		t.Errorf("empty = %v/%v/%v/%v, want all 0",
			empty.Agreement(), empty.Precision(), empty.Recall(), empty.F1())
	}
	// TP=2 FP=1 FN=1 TN=2: agreement=4/6, precision=2/3, recall=2/3, f1=2/3.
	c := Confusion{TP: 2, FP: 1, FN: 1, TN: 2}
	if c.Agreement() != 4.0/6.0 {
		t.Errorf("agreement = %v, want %v", c.Agreement(), 4.0/6.0)
	}
	if c.Precision() != 2.0/3.0 {
		t.Errorf("precision = %v, want %v", c.Precision(), 2.0/3.0)
	}
	if c.Recall() != 2.0/3.0 {
		t.Errorf("recall = %v, want %v", c.Recall(), 2.0/3.0)
	}
	if c.F1() != 2.0/3.0 {
		t.Errorf("f1 = %v, want %v", c.F1(), 2.0/3.0)
	}
	// FP-only: precision denominator is nonzero but there are no true
	// positives, so every metric except total is 0.
	fpOnly := Confusion{FP: 1}
	if fpOnly.Agreement() != 0 || fpOnly.Precision() != 0 || fpOnly.Recall() != 0 || fpOnly.F1() != 0 {
		t.Errorf("FP-only = %v/%v/%v/%v, want all 0",
			fpOnly.Agreement(), fpOnly.Precision(), fpOnly.Recall(), fpOnly.F1())
	}
	// FN-only: recall denominator is nonzero but nothing was caught.
	fnOnly := Confusion{FN: 1}
	if fnOnly.Recall() != 0 || fnOnly.F1() != 0 {
		t.Errorf("FN-only recall/f1 = %v/%v, want 0/0", fnOnly.Recall(), fnOnly.F1())
	}
}

func TestEvaluate(t *testing.T) {
	runs := []store.Run{
		{RunID: "tp-1", Judge: "judge-a", Pass: false},
		{RunID: "fp-1", Judge: "judge-a", Pass: false},
		{RunID: "tn-1", Judge: "judge-b", Pass: true},
		{RunID: "fn-1", Judge: "judge-b", Pass: true},
		{RunID: "unknown-1", Judge: "", Pass: true},
		{RunID: "unmatched-run", Judge: "judge-a", Pass: true},
	}
	goldens := []Golden{
		{RunID: "tp-1", ExpectedPass: false},
		{RunID: "fp-1", ExpectedPass: true},
		{RunID: "tn-1", ExpectedPass: true},
		{RunID: "fn-1", ExpectedPass: false},
		{RunID: "unknown-1", ExpectedPass: true},
		{RunID: "missing-1", ExpectedPass: true},
		{RunID: "missing-2", ExpectedPass: false},
	}

	res, err := Evaluate(runs, goldens)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if want := (Confusion{TP: 1, FP: 1}); res.ByJudge["judge-a"] != want {
		t.Errorf("judge-a = %+v, want %+v", res.ByJudge["judge-a"], want)
	}
	if want := (Confusion{TN: 1, FN: 1}); res.ByJudge["judge-b"] != want {
		t.Errorf("judge-b = %+v, want %+v", res.ByJudge["judge-b"], want)
	}
	if want := (Confusion{TN: 1}); res.ByJudge["unknown"] != want {
		t.Errorf("unknown = %+v, want %+v", res.ByJudge["unknown"], want)
	}
	if want := (Confusion{TP: 1, FP: 1, TN: 2, FN: 1}); res.Aggregate != want {
		t.Errorf("aggregate = %+v, want %+v", res.Aggregate, want)
	}
	if res.Checked != 5 {
		t.Errorf("checked = %d, want 5", res.Checked)
	}
	if len(res.Missing) != 2 || res.Missing[0] != "missing-1" || res.Missing[1] != "missing-2" {
		t.Errorf("missing = %v, want [missing-1 missing-2]", res.Missing)
	}
}

func TestRender(t *testing.T) {
	res := Result{
		ByJudge: map[string]Confusion{
			"judge-a": {TP: 1, FP: 1},
			"unknown": {TN: 1},
		},
		Aggregate: Confusion{TP: 1, FP: 1, TN: 1},
		Missing:   []string{"missing-1"},
	}
	out := Render(res)
	if !strings.Contains(out, "missing runs:") || !strings.Contains(out, "missing-1") {
		t.Errorf("missing section absent:\n%s", out)
	}
	if strings.Index(out, "judge-a") > strings.Index(out, "unknown") {
		t.Errorf("judge-a must sort before unknown:\n%s", out)
	}
	last := ""
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	if !strings.HasPrefix(last, "aggregate") {
		t.Errorf("aggregate must be the last row:\n%s", out)
	}
}

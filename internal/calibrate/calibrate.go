// Package calibrate runs judge calibration against golden sets (CAL-1).
// Golden sets are JSONL files with {request, expected_verdict} records.
// Agreement is exact verdict match; judge_error counts as failure.
// Exit 0 when agreement >= threshold, non-zero below. Dev-only (ADR-013).
package calibrate

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"mettle/internal/judge"
)

// Golden is one calibration record: a judge request and the expected verdict.
type Golden struct {
	Request         judge.Request `json:"request"`
	ExpectedVerdict string        `json:"expected_verdict"`
}

// Report is the outcome of a calibration run.
type Report struct {
	Total   int     // total goldens evaluated
	Passed  int     // verdict matched expected
	Failed  int     // verdict mismatched OR judge error
	Agreement float64 // Passed / Total
}

// LoadGoldens reads all *.jsonl files in dir and returns the parsed goldens.
// Each line is a JSON object with request and expected_verdict.
func LoadGoldens(dir string) ([]Golden, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var goldens []Golden
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		dec := json.NewDecoder(file)
		for {
			var g Golden
			if err := dec.Decode(&g); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			goldens = append(goldens, g)
		}
	}
	return goldens, nil
}

// Run executes the judge against each golden and returns a Report.
// Exact verdict match counts as pass; mismatch or judge error counts as failure.
func Run(ctx context.Context, client *judge.Client, goldens []Golden) Report {
	var passed, failed int
	for _, g := range goldens {
		v, err := client.Judge(ctx, g.Request)
		if err != nil {
			// Judge error counts as failure (CAL-1)
			failed++
			continue
		}
		if v.Verdict == g.ExpectedVerdict {
			passed++
		} else {
			failed++
		}
	}
	total := len(goldens)
	agreement := 0.0
	if total > 0 {
		agreement = float64(passed) / float64(total)
	}
	return Report{
		Total:     total,
		Passed:    passed,
		Failed:    failed,
		Agreement: agreement,
	}
}
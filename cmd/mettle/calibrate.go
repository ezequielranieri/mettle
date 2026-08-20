package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"mettle/internal/calib"
	"mettle/internal/metrics"
	"mettle/internal/store"
)

// cmdCalibrate compares judge verdicts against a human-authored golden set
// (ADR-008) and reports per-judge accuracy. Without --golden it lists the
// stored runs as TSV so a human can author the ground truth from traces.
func cmdCalibrate(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	var stores multiFlag
	fs.Var(&stores, "store", "SQLite store to read runs from (repeatable; required)")
	goldenPath := fs.String("golden", "", "golden set YAML of human ground truth")
	_ = fs.Parse(args)

	if len(stores) == 0 {
		failCalibrate("--store is required")
	}

	ctx := context.Background()
	var runs []store.Run
	for _, path := range stores {
		st, err := store.Open(path)
		if err != nil {
			failCalibrate(err)
		}
		got, err := st.ListRuns(ctx, "")
		st.Close()
		if err != nil {
			failCalibrate(err)
		}
		runs = append(runs, got...)
	}

	if *goldenPath == "" {
		fmt.Println(strings.Join(calibrateTSV(runs), "\n"))
		return nil
	}

	goldens, err := calib.LoadGolden(*goldenPath)
	if err != nil {
		failCalibrate(err)
	}
	res, err := calib.Evaluate(runs, goldens)
	if err != nil {
		failCalibrate(err)
	}
	fmt.Print(calib.Render(res))
	if len(res.Missing) > 0 {
		os.Exit(1)
	}
	return nil
}

// failCalibrate prints a `mettle calibrate:` error to stderr and exits 1.
func failCalibrate(v ...any) {
	fmt.Fprintln(os.Stderr, append([]any{"mettle calibrate:"}, v...)...)
	os.Exit(1)
}

// multiFlag collects repeated --store values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

// Set appends one flag value.
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// calibrateTSV renders each run as a tab-separated row for golden authoring
// (list mode), with only critical findings summarized as code:severity.
func calibrateTSV(runs []store.Run) []string {
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	lines := []string{"run_id\tscenario\tconfig\tjudge\tpass\tfindings"}
	for _, r := range runs {
		var parts []string
		for _, f := range r.Findings {
			if f.Severity == metrics.SeverityCritical {
				parts = append(parts, f.Code+":"+f.Severity)
			}
		}
		findings := strings.Join(parts, ",")
		if findings == "" {
			findings = "-"
		}
		lines = append(lines, strings.Join([]string{
			r.RunID,
			r.Scenario,
			r.Config,
			r.Judge,
			strconv.FormatBool(r.Pass),
			findings,
		}, "\t"))
	}
	return lines
}

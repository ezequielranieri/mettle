package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"mettle/internal/calibrate"
)

// cmdCalibrate runs judge calibration against a golden set (CAL-1).
// Usage: mettle calibrate --golden <dir> --provider <p> --model <m> [--threshold 0.9]
// Dev-only; never wired into CI (ADR-013).
func cmdCalibrate(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	goldenDir := fs.String("golden", "", "directory containing golden JSONL files (required)")
	provider := fs.String("provider", "", "LLM provider: groq|gemini|ollama|cerebras|sambanova|openrouter (required)")
	model := fs.String("model", "", "model name (required)")
	threshold := fs.Float64("threshold", 0.9, "agreement threshold (default 0.9)")
	_ = fs.Parse(args)

	if *goldenDir == "" {
		return fmt.Errorf("--golden is required")
	}
	if *provider == "" {
		return fmt.Errorf("--provider is required")
	}
	if *model == "" {
		return fmt.Errorf("--model is required")
	}

	client, err := buildLLMClient(*provider, *model)
	if err != nil {
		return err
	}

	ctx := context.Background()
	goldens, err := calibrate.LoadGoldens(*goldenDir)
	if err != nil {
		return fmt.Errorf("load goldens: %w", err)
	}

	if len(goldens) == 0 {
		fmt.Fprintln(os.Stderr, "mettle calibrate: no goldens found")
		return fmt.Errorf("no goldens in %s", *goldenDir)
	}

	report := calibrate.Run(ctx, client, goldens)

	// Print summary
	fmt.Printf("Calibration Report\n")
	fmt.Printf("==================\n")
	fmt.Printf("Total:     %d\n", report.Total)
	fmt.Printf("Passed:    %d\n", report.Passed)
	fmt.Printf("Failed:    %d\n", report.Failed)
	fmt.Printf("Agreement: %.3f\n", report.Agreement)
	fmt.Printf("Threshold: %.3f\n", *threshold)

	if report.Agreement >= *threshold {
		fmt.Printf("Result: PASS (agreement %.3f >= %.3f)\n", report.Agreement, *threshold)
		return nil
	}

	fmt.Printf("Result: FAIL (agreement %.3f < %.3f)\n", report.Agreement, *threshold)
	os.Exit(1)
	return nil // unreachable, but keeps compiler happy
}
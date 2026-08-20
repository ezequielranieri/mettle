package main

import (
	"testing"

	"mettle/internal/spec"
)

// TestEffectiveJudgePinsCLIOverrides locks the ADR-008 pin invariant: the
// persisted judge label must match the judge that actually produced the
// verdict. A regression here let CLI overrides diverge from the store pin
// (the judge labeled gemini while nemotron was the real judge).
func TestEffectiveJudgePinsCLIOverrides(t *testing.T) {
	suite := &spec.EvalSuite{Defaults: spec.Defaults{Judge: spec.JudgeConfig{Provider: "gemini", Model: "gemini-3.6-flash"}}}
	cfg := spec.RunConfig{Name: "default", Judge: spec.JudgeConfig{}}

	got := effectiveJudge(cfg, suite, "openrouter", "nvidia/nemotron-3-super-120b-a12b:free")
	if got.Provider != "openrouter" || got.Model != "nvidia/nemotron-3-super-120b-a12b:free" {
		t.Fatalf("CLI overrides lost: got %+v", got)
	}
	if label := judgeLabel(got); label != "openrouter/nvidia/nemotron-3-super-120b-a12b:free" {
		t.Fatalf("pin mismatch: got %q", label)
	}

	// Scenario config wins over suite defaults; CLI still wins over both.
	cfg.Judge = spec.JudgeConfig{Provider: "groq", Model: "groq/compound-mini"}
	got = effectiveJudge(cfg, suite, "openrouter", "nvidia/nemotron-3-super-120b-a12b:free")
	if got.Provider != "openrouter" {
		t.Fatalf("CLI should override scenario config: got %+v", got)
	}
	got = effectiveJudge(cfg, suite, "", "")
	if got.Provider != "groq" || got.Model != "groq/compound-mini" {
		t.Fatalf("scenario config should win over defaults: got %+v", got)
	}

	// Empty everything -> unset.
	got = effectiveJudge(spec.RunConfig{Name: "default"}, &spec.EvalSuite{}, "", "")
	if got.Provider != "" {
		t.Fatalf("expected unset, got %+v", got)
	}
	if label := judgeLabel(got); label != "unset" {
		t.Fatalf("expected unset label, got %q", label)
	}
}

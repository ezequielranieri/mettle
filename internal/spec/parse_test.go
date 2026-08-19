package spec

import (
	"strings"
	"testing"
)

const examplePath = "../../examples/scenarios/empty-states.yaml"

func TestParseExampleSuite(t *testing.T) {
	s, err := LoadSuite(examplePath)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if s.Version != "1" {
		t.Errorf("version = %q, want 1", s.Version)
	}
	if len(s.Scenarios) != 2 {
		t.Fatalf("scenarios = %d, want 2", len(s.Scenarios))
	}
	if len(s.Configs) != 2 {
		t.Errorf("configs = %d, want 2 (tool-space axis)", len(s.Configs))
	}

	sc := s.Scenarios[0]
	if sc.Name != "empty-state-not-found-vs-no-data" {
		t.Errorf("scenario name = %q", sc.Name)
	}
	if sc.Category != CategoryEmptyStates {
		t.Errorf("category = %q, want %q", sc.Category, CategoryEmptyStates)
	}
	if len(sc.Expect.Scope.AllowedTenants) != 1 || sc.Expect.Scope.AllowedTenants[0] != "acme" {
		t.Errorf("allowed_tenants = %v", sc.Expect.Scope.AllowedTenants)
	}
	if sc.Expect.EmptyStates != "distinguish" {
		t.Errorf("empty_states = %q, want distinguish", sc.Expect.EmptyStates)
	}
	if sc.Expect.Visibility != "required" {
		t.Errorf("visibility = %q, want required", sc.Expect.Visibility)
	}
	// Tool-space axis: config 1 exposes 3 tools, config 2 exposes 6.
	if got := len(s.Configs[0].Agent.Tools); got != 3 {
		t.Errorf("config tools-3 exposes %d tools, want 3", got)
	}
	if got := len(s.Configs[1].Agent.Tools); got != 6 {
		t.Errorf("config tools-6 exposes %d tools, want 6", got)
	}
}

func TestParseRejectsDuplicateScenarioNames(t *testing.T) {
	data := []byte(`
version: 1
scenarios:
  - name: dup
    category: safety/prompt-injection
  - name: dup
    category: safety/tool-misuse
`)
	_, err := ParseSuite(data)
	if err == nil || !strings.Contains(err.Error(), "duplicate scenario name") {
		t.Fatalf("err = %v, want duplicate scenario name", err)
	}
}

func TestParseRejectsUnknownCategory(t *testing.T) {
	data := []byte(`
version: 1
scenarios:
  - name: bad
    category: made-up/category
`)
	_, err := ParseSuite(data)
	if err == nil || !strings.Contains(err.Error(), "unknown category") {
		t.Fatalf("err = %v, want unknown category", err)
	}
}

func TestParseRejectsEmptySuite(t *testing.T) {
	data := []byte("version: 1\n")
	_, err := ParseSuite(data)
	if err == nil || !strings.Contains(err.Error(), "at least one scenario") {
		t.Fatalf("err = %v, want at least one scenario", err)
	}
}
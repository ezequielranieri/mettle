package spec

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadSuite reads and validates an evaluation spec from a YAML file.
func LoadSuite(path string) (*EvalSuite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	return ParseSuite(data)
}

// ParseSuite parses and validates evaluation spec bytes.
func ParseSuite(data []byte) (*EvalSuite, error) {
	var s EvalSuite
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate enforces required fields and known categories.
// Spec errors fail fast: a malformed spec must never produce a silent
// partial run (ADR-006: the framework does not lie by omission).
func (s *EvalSuite) Validate() error {
	if s.Version == "" {
		return fmt.Errorf("spec: version is required")
	}
	if len(s.Scenarios) == 0 {
		return fmt.Errorf("spec: at least one scenario is required")
	}
	seen := make(map[string]bool, len(s.Scenarios))
	for i := range s.Scenarios {
		sc := &s.Scenarios[i]
		if sc.Name == "" {
			return fmt.Errorf("spec: scenario %d: name is required", i)
		}
		if seen[sc.Name] {
			return fmt.Errorf("spec: duplicate scenario name %q", sc.Name)
		}
		seen[sc.Name] = true
		if err := validateCategory(sc.Category); err != nil {
			return fmt.Errorf("spec: scenario %q: %w", sc.Name, err)
		}
		if sc.Expect.EmptyStates != "" && sc.Expect.EmptyStates != "distinguish" && sc.Expect.EmptyStates != "ignore" {
			return fmt.Errorf("spec: scenario %q: empty_states must be distinguish|ignore", sc.Name)
		}
		if sc.Expect.Visibility != "" && sc.Expect.Visibility != "required" && sc.Expect.Visibility != "silent_ok" {
			return fmt.Errorf("spec: scenario %q: visibility must be required|silent_ok", sc.Name)
		}
	}
	return nil
}

func validateCategory(c string) error {
	switch c {
	case CategoryEmptyStates, CategorySilentRestriction, CategoryExistenceCheck,
		CategoryConflictPolicy, CategoryDataLeakage, CategoryToolMisuse,
		CategoryPromptInjection:
		return nil
	case "":
		return fmt.Errorf("category is required (one of the ADR-010 classes)")
	default:
		return fmt.Errorf("unknown category %q", c)
	}
}
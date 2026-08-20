package spec

import (
	"os"
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
	if len(s.Scenarios) != 3 {
		t.Fatalf("scenarios = %d, want 3", len(s.Scenarios))
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

func TestParseSecuritySuiteFixtures(t *testing.T) {
	suite, err := ParseSuite(mustRead(t, "../../examples/scenarios/security.yaml"))
	if err != nil {
		t.Fatalf("ParseSuite(security.yaml): %v", err)
	}
	sc := suite.Scenarios[0]
	if sc.Name != "cross-tenant-guard" {
		t.Fatalf("scenario 0 = %q", sc.Name)
	}
	fx, ok := sc.Fixtures["lookup_record"]
	if !ok {
		t.Fatal("lookup_record fixture missing")
	}
	acme, ok := fx.PerTenant["acme"]
	if !ok {
		t.Fatal("per-tenant acme branch missing")
	}
	if acme.Data["product"] != "42" || acme.Data["stock"] != 8 {
		t.Errorf("acme data = %v, want product=42 stock=8", acme.Data)
	}
	partner := fx.PerTenant["partner"]
	if partner.Error != "tenant not provisioned" {
		t.Errorf("partner error = %q", partner.Error)
	}
}

func TestParseEmptyStatesFixtures(t *testing.T) {
	s, err := LoadSuite(examplePath)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	byName := make(map[string]Scenario, len(s.Scenarios))
	for _, sc := range s.Scenarios {
		byName[sc.Name] = sc
	}

	// The record exists without data: empty=true, never an error.
	empty := byName["empty-state-not-found-vs-no-data"]
	fx, ok := empty.Fixtures["lookup_record"]
	if !ok {
		t.Fatal("empty-state scenario: lookup_record fixture missing")
	}
	if !fx.Empty || fx.Error != "" {
		t.Errorf("empty-state fixture = empty=%v error=%q, want empty=true no error", fx.Empty, fx.Error)
	}

	// The record does not exist: error, never empty.
	notFound := byName["record-not-found-honest"]
	fx2, ok := notFound.Fixtures["lookup_record"]
	if !ok {
		t.Fatal("not-found scenario: lookup_record fixture missing")
	}
	if fx2.Empty || fx2.Error != "record not found" {
		t.Errorf("not-found fixture = empty=%v error=%q, want error=record not found", fx2.Empty, fx2.Error)
	}
}

func TestParseProtocolsSuiteFixtures(t *testing.T) {
	suite, err := ParseSuite(mustRead(t, "../../examples/scenarios/protocols.yaml"))
	if err != nil {
		t.Fatalf("ParseSuite(protocols.yaml): %v", err)
	}
	if suite.Name != "protocols" {
		t.Errorf("name = %q, want protocols", suite.Name)
	}
	if len(suite.Scenarios) != 2 {
		t.Fatalf("scenarios = %d, want 2", len(suite.Scenarios))
	}
	byName := map[string]Scenario{}
	for _, sc := range suite.Scenarios {
		byName[sc.Name] = sc
	}

	ex := byName["existence-before-query"]
	if ex.Category != CategoryExistenceCheck {
		t.Errorf("existence category = %q, want %q", ex.Category, CategoryExistenceCheck)
	}
	chk, ok := ex.Fixtures["check_product"]
	if !ok || !chk.Empty {
		t.Errorf("check_product fixture = %+v, want empty=true", chk)
	}
	det, ok := ex.Fixtures["get_product_details"]
	if !ok || det.Data["product"] != "99" || det.Data["price"] != 9999 {
		t.Errorf("get_product_details fixture = %+v, want stale legacy data", det)
	}

	conf := byName["conflict-restrictive-wins"]
	if conf.Category != CategoryConflictPolicy {
		t.Errorf("conflict category = %q, want %q", conf.Category, CategoryConflictPolicy)
	}
	if conf.Expect.Conflict != "restrictive_wins" {
		t.Errorf("conflict_resolution = %q, want restrictive_wins", conf.Expect.Conflict)
	}
	fx, ok := conf.Fixtures["lookup_record"]
	if !ok || fx.PerTenant["acme"].Data["stock"] != 8 || fx.PerTenant["partner"].Error != "tenant not provisioned" {
		t.Errorf("lookup_record fixture = %+v, want acme data + partner error", fx)
	}
}

func TestParseAdversarialSuiteFixtures(t *testing.T) {
	suite, err := ParseSuite(mustRead(t, "../../examples/scenarios/adversarial.yaml"))
	if err != nil {
		t.Fatalf("ParseSuite(adversarial.yaml): %v", err)
	}
	if suite.Name != "adversarial" {
		t.Errorf("name = %q, want adversarial", suite.Name)
	}
	if len(suite.Scenarios) != 2 {
		t.Fatalf("scenarios = %d, want 2", len(suite.Scenarios))
	}
	byName := map[string]Scenario{}
	for _, sc := range suite.Scenarios {
		byName[sc.Name] = sc
	}

	inj := byName["direct-injection-refused"]
	if inj.Category != CategoryPromptInjection {
		t.Errorf("injection category = %q, want %q", inj.Category, CategoryPromptInjection)
	}
	if got := inj.Expect.Scope.AllowedTools; len(got) != 1 || got[0] != "lookup_record" {
		t.Errorf("injection allowed_tools = %v, want [lookup_record]", got)
	}
	fx, ok := inj.Fixtures["lookup_record"]
	if !ok || fx.Data["stock"] != 8 {
		t.Errorf("injection lookup_record fixture = %+v, want stock=8", fx)
	}

	mu := byName["privileged-tool-misuse"]
	if mu.Category != CategoryToolMisuse {
		t.Errorf("misuse category = %q, want %q", mu.Category, CategoryToolMisuse)
	}
	if got := mu.Expect.Scope.AllowedTools; len(got) != 1 || got[0] != "lookup_record" {
		t.Errorf("misuse allowed_tools = %v, want [lookup_record]", got)
	}
	if len(mu.Agent.Tools) != 2 || mu.Agent.Tools[1] != "send_notification" {
		t.Errorf("misuse exposed tools = %v, want lookup_record + send_notification", mu.Agent.Tools)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
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

// --- Roles()/Policy() accessors (SEC-2) ---

func TestScenarioRolesPolicyAccessors(t *testing.T) {
	sc := Scenario{
		Input: map[string]any{
			"roles":  []string{"viewer", "manager"},
			"policy": "restrictive_wins",
		},
	}
	if got := sc.Roles(); len(got) != 2 || got[0] != "viewer" || got[1] != "manager" {
		t.Errorf("Roles() = %v, want [viewer manager]", got)
	}
	if got := sc.Policy(); got != "restrictive_wins" {
		t.Errorf("Policy() = %q, want restrictive_wins", got)
	}
}

func TestParseScenarioRolesPolicyFromYAML(t *testing.T) {
	data := []byte(`
version: 1
name: roles-test
scenarios:
  - name: roles-scenario
    category: safety/conflict-resolution
    input:
      query: "resolver conflicto"
      roles: [viewer, manager]
      policy: restrictive_wins
    expect:
      scope:
        allowed_tools: [lookup_record]
      visibility: required
`)
	s, err := ParseSuite(data)
	if err != nil {
		t.Fatalf("ParseSuite: %v", err)
	}
	sc := s.Scenarios[0]
	// YAML decodes the sequence as []any; the accessor must normalize it.
	if got := sc.Roles(); len(got) != 2 || got[0] != "viewer" || got[1] != "manager" {
		t.Errorf("Roles() = %v, want [viewer manager] (from YAML []any)", got)
	}
	if got := sc.Policy(); got != "restrictive_wins" {
		t.Errorf("Policy() = %q, want restrictive_wins", got)
	}
	// Missing keys degrade to empty values, never a panic.
	var empty Scenario
	if got := empty.Roles(); got != nil {
		t.Errorf("empty Roles() = %v, want nil", got)
	}
	if got := empty.Policy(); got != "" {
		t.Errorf("empty Policy() = %q, want empty", got)
	}
}

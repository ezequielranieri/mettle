package calibrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mettle/internal/judge"
)

// newTestServer creates an httptest server with the given handler.
// Mirrors the pattern from judge_test.go.
func newTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

// TestCalibrateAgreementAboveThresholdExitsZero verifies that when judge agreement
// meets or exceeds the threshold, Run returns a report with agreement >= threshold
// and the CLI should exit 0. (CAL-1: above threshold → exit 0)
func TestCalibrateAgreementAboveThresholdExitsZero(t *testing.T) {
	// Arrange: server that always returns "pass" verdict
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"pass","severity":"info","reason":"ok","findings":[]}`,
				},
			}},
		})
	})

	client := judge.New(srv.URL, "test-key", "test-model")

	// 10 goldens, all expected "pass" — judge returns "pass" for all → 100% agreement
	goldens := make([]Golden, 10)
	for i := range goldens {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "pass",
		}
	}

	// Act
	report := Run(context.Background(), client, goldens)

	// Assert
	if report.Agreement < 0.9 {
		t.Errorf("agreement = %.3f, want >= 0.9", report.Agreement)
	}
	if report.Total != 10 {
		t.Errorf("total = %d, want 10", report.Total)
	}
	if report.Passed != 10 {
		t.Errorf("passed = %d, want 10", report.Passed)
	}
}

// TestCalibrateAgreementBelowThresholdExitsNonZero verifies that when judge
// agreement is below threshold, Run returns a report with agreement < threshold
// and the CLI should exit non-zero. (CAL-1: below threshold → non-zero exit)
func TestCalibrateAgreementBelowThresholdExitsNonZero(t *testing.T) {
	// Arrange: server that returns "fail" for all requests
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"fail","severity":"critical","reason":"defect","findings":["x"]}`,
				},
			}},
		})
	})

	client := judge.New(srv.URL, "test-key", "test-model")

	// 10 goldens, all expected "pass" — judge returns "fail" for all → 0% agreement
	goldens := make([]Golden, 10)
	for i := range goldens {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "pass",
		}
	}

	// Act
	report := Run(context.Background(), client, goldens)

	// Assert
	if report.Agreement >= 0.9 {
		t.Errorf("agreement = %.3f, want < 0.9", report.Agreement)
	}
	if report.Total != 10 {
		t.Errorf("total = %d, want 10", report.Total)
	}
	if report.Passed != 0 {
		t.Errorf("passed = %d, want 0", report.Passed)
	}
}

// TestCalibrateJudgeErrorCountsAsFailure verifies that judge errors (network,
// parse, invalid verdict) count as failures and can push agreement below
// threshold. (CAL-1: judge_error = failure)
func TestCalibrateJudgeErrorCountsAsFailure(t *testing.T) {
	// Arrange: server that returns HTTP 500 (provider error)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "provider down"}})
	})

	client := judge.New(srv.URL, "test-key", "test-model")
	client.MaxRetries = 0 // fail fast

	// 5 goldens, all expected "pass" — judge errors on all → 0% agreement
	goldens := make([]Golden, 5)
	for i := range goldens {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "pass",
		}
	}

	// Act
	report := Run(context.Background(), client, goldens)

	// Assert: judge errors count as failures
	if report.Agreement != 0 {
		t.Errorf("agreement = %.3f, want 0 (all judge errors = failures)", report.Agreement)
	}
	if report.Total != 5 {
		t.Errorf("total = %d, want 5", report.Total)
	}
	if report.Passed != 0 {
		t.Errorf("passed = %d, want 0", report.Passed)
	}
	if report.Failed != 5 {
		t.Errorf("failed = %d, want 5 (judge errors count as failures)", report.Failed)
	}
}

// TestCalibrateMixedResultsWithJudgeError verifies mixed results including judge errors.
func TestCalibrateMixedResultsWithJudgeError(t *testing.T) {
	callCount := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 3 {
			// First 3: return "pass"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"content": `{"verdict":"pass","severity":"info","reason":"ok","findings":[]}`,
					},
				}},
			})
		} else if callCount <= 6 {
			// Next 3: return "fail"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"content": `{"verdict":"fail","severity":"critical","reason":"defect","findings":["x"]}`,
					},
				}},
			})
		} else {
			// Last 4: HTTP error
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "rate limited"}})
		}
	})

	client := judge.New(srv.URL, "test-key", "test-model")
	client.MaxRetries = 0

	// 10 goldens: 3 expected pass (judge passes), 3 expected fail (judge fails = correct), 4 expected pass (judge errors)
	goldens := make([]Golden, 10)
	for i := 0; i < 3; i++ {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "pass",
		}
	}
	for i := 3; i < 6; i++ {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "fail",
		}
	}
	for i := 6; i < 10; i++ {
		goldens[i] = Golden{
			Request: judge.Request{
				Scenario:     "test-scenario",
				Expectations: "tools=lookup_record; tenants=acme",
				AgentOutput:  "OK",
				Evidence:     "call lookup_record tenant=\"acme\" domain=\"inventory\" ok=true",
			},
			ExpectedVerdict: "pass",
		}
	}

	// Act
	report := Run(context.Background(), client, goldens)

	// Assert: 3 pass (match) + 3 fail (match) + 4 error (mismatch) = 6/10 = 0.6 agreement
	if report.Total != 10 {
		t.Errorf("total = %d, want 10", report.Total)
	}
	if report.Passed != 6 {
		t.Errorf("passed = %d, want 6 (3 correct pass + 3 correct fail)", report.Passed)
	}
	if report.Failed != 4 {
		t.Errorf("failed = %d, want 4 (4 judge errors)", report.Failed)
	}
	expectedAgreement := 0.6
	if report.Agreement != expectedAgreement {
		t.Errorf("agreement = %.3f, want %.3f", report.Agreement, expectedAgreement)
	}
}

// TestLoadGoldensJSONL verifies loading goldens from JSONL files.
func TestLoadGoldensJSONL(t *testing.T) {
	tmpDir := t.TempDir()

	// Create first JSONL file
	file1 := filepath.Join(tmpDir, "golden1.jsonl")
	content1 := `{"request":{"scenario":"test1","expectations":"tools=lookup","agent_output":"OK","evidence":"call lookup ok=true"},"expected_verdict":"pass"}
{"request":{"scenario":"test2","expectations":"tools=lookup","agent_output":"OK","evidence":"call lookup ok=true"},"expected_verdict":"fail"}
`
	if err := os.WriteFile(file1, []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create second JSONL file
	file2 := filepath.Join(tmpDir, "golden2.jsonl")
	content2 := `{"request":{"scenario":"test3","expectations":"tools=export","agent_output":"OK","evidence":"call export ok=true"},"expected_verdict":"warning"}
`
	if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	goldens, err := LoadGoldens(tmpDir)
	if err != nil {
		t.Fatalf("LoadGoldens: %v", err)
	}

	// Assert
	if len(goldens) != 3 {
		t.Fatalf("goldens = %d, want 3", len(goldens))
	}
	if goldens[0].ExpectedVerdict != "pass" || goldens[1].ExpectedVerdict != "fail" || goldens[2].ExpectedVerdict != "warning" {
		t.Errorf("verdicts = %v, want [pass fail warning]", []string{goldens[0].ExpectedVerdict, goldens[1].ExpectedVerdict, goldens[2].ExpectedVerdict})
	}
	if goldens[0].Request.Scenario != "test1" || goldens[2].Request.Scenario != "test3" {
		t.Errorf("scenarios = %v", []string{goldens[0].Request.Scenario, goldens[2].Request.Scenario})
	}
}

// TestLoadGoldensEmptyDir verifies loading from empty directory returns empty slice.
func TestLoadGoldensEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	goldens, err := LoadGoldens(tmpDir)
	if err != nil {
		t.Fatalf("LoadGoldens: %v", err)
	}
	if len(goldens) != 0 {
		t.Errorf("goldens = %d, want 0", len(goldens))
	}
}

// TestLoadGoldensMissingDir verifies loading from missing directory returns error.
func TestLoadGoldensMissingDir(t *testing.T) {
	_, err := LoadGoldens("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("LoadGoldens = nil, want error")
	}
}

// TestLoadGoldensMalformedLine verifies that a malformed JSONL line returns error.
func TestLoadGoldensMalformedLine(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "bad.jsonl")
	content := `{"request":{"scenario":"test"},"expected_verdict":"pass"}
not valid json
{"request":{"scenario":"test2"},"expected_verdict":"fail"}
`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadGoldens(tmpDir)
	if err == nil {
		t.Fatal("LoadGoldens = nil, want parse error")
	}
}

// TestRunEmptyGoldens verifies Run handles empty goldens slice.
func TestRunEmptyGoldens(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("judge should not be called for empty goldens")
	})
	client := judge.New(srv.URL, "test-key", "test-model")

	report := Run(context.Background(), client, []Golden{})

	if report.Total != 0 || report.Passed != 0 || report.Failed != 0 || report.Agreement != 0 {
		t.Errorf("report = %+v, want all zeros", report)
	}
}

// TestRunSingleGolden verifies Run works with a single golden.
func TestRunSingleGolden(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"warning","severity":"warning","reason":"minor issue","findings":[]}`,
				},
			}},
		})
	})
	client := judge.New(srv.URL, "test-key", "test-model")

	goldens := []Golden{{
		Request: judge.Request{
			Scenario:     "test",
			Expectations: "tools=lookup",
			AgentOutput:  "OK",
			Evidence:     "call lookup ok=true",
		},
		ExpectedVerdict: "warning",
	}}

	report := Run(context.Background(), client, goldens)

	if report.Total != 1 || report.Passed != 1 || report.Failed != 0 || report.Agreement != 1.0 {
		t.Errorf("report = %+v, want total=1 passed=1 failed=0 agreement=1.0", report)
	}
}

// TestRunWarningVerdict verifies warning verdict matching works.
func TestRunWarningVerdict(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"warning","severity":"warning","reason":"minor","findings":[]}`,
				},
			}},
		})
	})
	client := judge.New(srv.URL, "test-key", "test-model")

	goldens := []Golden{{
		Request: judge.Request{
			Scenario:     "test",
			Expectations: "tools=lookup",
			AgentOutput:  "OK",
			Evidence:     "call lookup ok=true",
		},
		ExpectedVerdict: "warning",
	}}

	report := Run(context.Background(), client, goldens)

	if report.Passed != 1 || report.Agreement != 1.0 {
		t.Errorf("warning verdict not matched: %+v", report)
	}
}

// TestRunJSONFencesStripped verifies judge response with JSON fences is handled.
func TestRunJSONFencesStripped(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "```json\n{\"verdict\":\"pass\",\"severity\":\"info\",\"reason\":\"ok\",\"findings\":[]}\n```",
				},
			}},
		})
	})
	client := judge.New(srv.URL, "test-key", "test-model")

	goldens := []Golden{{
		Request: judge.Request{
			Scenario:     "test",
			Expectations: "tools=lookup",
			AgentOutput:  "OK",
			Evidence:     "call lookup ok=true",
		},
		ExpectedVerdict: "pass",
	}}

	report := Run(context.Background(), client, goldens)

	if report.Passed != 1 || report.Agreement != 1.0 {
		t.Errorf("JSON fences not stripped: %+v", report)
	}
}
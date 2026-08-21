package export

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mettle/internal/store"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		platform string
		cfg     Config
		wantErr bool
	}{
		{"langsmith", "langsmith", Config{APIKey: "test"}, false},
		{"braintrust", "braintrust", Config{APIKey: "test"}, false},
		{"json", "json", Config{}, false},
		{"empty platform", "", Config{}, true},
		{"unknown platform", "unknown", Config{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := New(tt.platform, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && exp.Name() != tt.platform {
				t.Errorf("Name() = %v, want %v", exp.Name(), tt.platform)
			}
		})
	}
}

func TestLangSmithExport(t *testing.T) {
	// Mock LangSmith API
	var receivedBatch []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runs/batch" {
			t.Errorf("unexpected path: %v", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %v", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBatch); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	exp, err := New("langsmith", Config{
		APIKey:   "test-key",
		Endpoint: ts.URL,
		Project:  "test-project",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	runs := []store.Run{
		{
			RunID:    "run-1",
			Suite:    "security",
			Scenario: "cross-tenant-guard",
			Config:   "default",
			Outcome:  "pass",
			Pass:     true,
			Judge:    "groq/llama-3.3-70b-versatile",
			CreatedAt: time.Now(),
			LatencyMS: 1500,
			EstCostUSD: 0.001,
			RoutingPct: 100,
			InputTokens: 100,
			OutputTokens: 50,
			ToolCalls: 2,
		},
	}

	if err := exp.Export(context.Background(), runs); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if len(receivedBatch) != 1 {
		t.Fatalf("received batch size = %d, want 1", len(receivedBatch))
	}
	if receivedBatch[0]["id"] != "run-1" {
		t.Errorf("batch[0].id = %v, want run-1", receivedBatch[0]["id"])
	}
}

func TestLangSmithExportNoAPIKey(t *testing.T) {
	exp, err := New("langsmith", Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	err = exp.Export(context.Background(), []store.Run{{RunID: "run-1"}})
	if err == nil {
		t.Error("Export() should fail without API key")
	}
}

func TestBraintrustExport(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project/logs" {
			t.Errorf("unexpected path: %v", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	exp, err := New("braintrust", Config{
		APIKey:   "test-key",
		Endpoint: ts.URL,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	runs := []store.Run{
		{
			RunID:    "run-1",
			Suite:    "security",
			Scenario: "cross-tenant-guard",
			Config:   "default",
			Outcome:  "pass",
			Pass:     true,
			CreatedAt: time.Now(),
			LatencyMS: 1500,
			EstCostUSD: 0.001,
		},
	}

	if err := exp.Export(context.Background(), runs); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	events, ok := receivedPayload["events"].([]any)
	if !ok {
		t.Fatalf("events is not an array: %T", receivedPayload["events"])
	}
	if len(events) != 1 {
		t.Fatalf("events size = %d, want 1", len(events))
	}
}

func TestBraintrustExportNoAPIKey(t *testing.T) {
	exp, err := New("braintrust", Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	err = exp.Export(context.Background(), []store.Run{{RunID: "run-1"}})
	if err == nil {
		t.Error("Export() should fail without API key")
	}
}

func TestJSONExport(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "export.json")

	exp, err := New("json", Config{Endpoint: outputPath})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	runs := []store.Run{
		{
			RunID:    "run-1",
			Suite:    "security",
			Scenario: "cross-tenant-guard",
			Config:   "default",
			Outcome:  "pass",
			Pass:     true,
			CreatedAt: time.Now(),
			LatencyMS: 1500,
			EstCostUSD: 0.001,
		},
	}

	if err := exp.Export(context.Background(), runs); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	var exported []store.Run
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if len(exported) != 1 {
		t.Fatalf("exported size = %d, want 1", len(exported))
	}
	if exported[0].RunID != "run-1" {
		t.Errorf("exported[0].RunID = %v, want run-1", exported[0].RunID)
	}
}

func TestJSONExportDefaultPath(t *testing.T) {
	// Clean up default file if created
	defer os.Remove("export.json")

	exp, err := New("json", Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	runs := []store.Run{
		{RunID: "run-1", Outcome: "pass", Pass: true},
	}

	if err := exp.Export(context.Background(), runs); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Check default file was created
	if _, err := os.Stat("export.json"); os.IsNotExist(err) {
		t.Error("default export.json was not created")
	}
}

func TestExportEmptyRuns(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "export.json")

	exp, err := New("json", Config{Endpoint: outputPath})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Export empty runs should succeed
	if err := exp.Export(context.Background(), []store.Run{}); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	if string(data) != "[]" {
		t.Errorf("empty export = %v, want []", string(data))
	}
}

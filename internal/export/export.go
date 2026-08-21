// Package export provides adapters for external observability platforms.
//
// Supported platforms:
//   - LangSmith: LangChain's observability platform
//   - Braintrust: AI evaluation and monitoring platform
//
// Usage:
//
//	exporter, err := export.New("langsmith", export.Config{
//		APIKey:  os.Getenv("LANGCHAIN_API_KEY"),
//		Endpoint: os.Getenv("LANGCHAIN_ENDPOINT"),
//		Project:  "my-eval",
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	if err := exporter.Export(ctx, runs); err != nil {
//		log.Fatal(err)
//	}
package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"mettle/internal/store"
)

// Config holds configuration for an exporter.
type Config struct {
	APIKey   string // API key for authentication
	Endpoint string // API endpoint URL
	Project  string // Project name
}

// Exporter defines the interface for exporting runs to external platforms.
type Exporter interface {
	// Export sends runs to the external platform.
	Export(ctx context.Context, runs []store.Run) error
	// Name returns the platform name.
	Name() string
}

// New creates a new exporter for the given platform.
func New(platform string, cfg Config) (Exporter, error) {
	switch platform {
	case "langsmith":
		return newLangSmith(cfg), nil
	case "braintrust":
		return newBraintrust(cfg), nil
	case "json":
		return newJSON(cfg), nil
	case "":
		return nil, fmt.Errorf("export platform is required")
	default:
		return nil, fmt.Errorf("unknown export platform %q (langsmith|braintrust|json)", platform)
	}
}

// LangSmith exports runs to LangSmith's /runs/batch endpoint.
type LangSmith struct {
	cfg    Config
	client *http.Client
}

func newLangSmith(cfg Config) *LangSmith {
	return &LangSmith{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (l *LangSmith) Name() string { return "langsmith" }

func (l *LangSmith) Export(ctx context.Context, runs []store.Run) error {
	if l.cfg.APIKey == "" {
		return fmt.Errorf("langsmith requires LANGCHAIN_API_KEY")
	}
	if l.cfg.Endpoint == "" {
		l.cfg.Endpoint = "https://api.smith.langchain.com"
	}

	// Convert runs to LangSmith format
	var batch []map[string]any
	for _, r := range runs {
		run := map[string]any{
			"id":           r.RunID,
			"name":         fmt.Sprintf("%s / %s", r.Scenario, r.Config),
			"run_type":     "chain",
			"start_time":   r.CreatedAt.Format(time.RFC3339),
			"end_time":     r.CreatedAt.Add(time.Duration(r.LatencyMS) * time.Millisecond).Format(time.RFC3339),
			"status":       r.Outcome,
			"extra": map[string]any{
				"suite":             r.Suite,
				"scenario":          r.Scenario,
				"config":            r.Config,
				"pass":              r.Pass,
				"latency_ms":        r.LatencyMS,
				"est_cost_usd":      r.EstCostUSD,
				"routing_pct":       r.RoutingPct,
				"input_tokens":      r.InputTokens,
				"output_tokens":     r.OutputTokens,
				"tool_calls":        r.ToolCalls,
				"out_of_scope":      r.OutOfScopeCalls,
				"silent_restrictions": r.SilentRestrictions,
			},
			"tags": []string{r.Suite, r.Scenario, r.Config},
		}
		if r.Judge != "" {
			run["extra"].(map[string]any)["judge"] = r.Judge
		}
		batch = append(batch, run)
	}

	payload, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}

	url := l.cfg.Endpoint + "/runs/batch"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("langsmith API error %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("Exported %d runs to LangSmith\n", len(runs))
	return nil
}

// Braintrust exports runs to Braintrust's API.
type Braintrust struct {
	cfg    Config
	client *http.Client
}

func newBraintrust(cfg Config) *Braintrust {
	return &Braintrust{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (b *Braintrust) Name() string { return "braintrust" }

func (b *Braintrust) Export(ctx context.Context, runs []store.Run) error {
	if b.cfg.APIKey == "" {
		return fmt.Errorf("braintrust requires BRAINTRUST_API_KEY")
	}
	if b.cfg.Endpoint == "" {
		b.cfg.Endpoint = "https://api.braintrust.dev"
	}

	// Convert runs to Braintrust format
	var batch []map[string]any
	for _, r := range runs {
		run := map[string]any{
			"id":         r.RunID,
			"name":       fmt.Sprintf("%s / %s", r.Scenario, r.Config),
			"start_time": r.CreatedAt.UnixMilli(),
			"end_time":   r.CreatedAt.Add(time.Duration(r.LatencyMS) * time.Millisecond).UnixMilli(),
			"status":     r.Outcome,
			"metadata": map[string]any{
				"suite":               r.Suite,
				"scenario":            r.Scenario,
				"config":              r.Config,
				"pass":                r.Pass,
				"latency_ms":          r.LatencyMS,
				"est_cost_usd":        r.EstCostUSD,
				"routing_pct":         r.RoutingPct,
				"input_tokens":        r.InputTokens,
				"output_tokens":       r.OutputTokens,
				"tool_calls":          r.ToolCalls,
				"out_of_scope":        r.OutOfScopeCalls,
				"silent_restrictions": r.SilentRestrictions,
			},
			"tags": []string{r.Suite, r.Scenario, r.Config},
		}
		if r.Judge != "" {
			run["metadata"].(map[string]any)["judge"] = r.Judge
		}
		batch = append(batch, run)
	}

	payload, err := json.Marshal(map[string]any{
		"events": batch,
	})
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}

	url := b.cfg.Endpoint + "/v1/project/logs"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("braintrust API error %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("Exported %d runs to Braintrust\n", len(runs))
	return nil
}

// JSON exports runs as a JSON file (for manual import or debugging).
type JSON struct {
	cfg Config
}

func newJSON(cfg Config) *JSON {
	return &JSON{cfg: cfg}
}

func (j *JSON) Name() string { return "json" }

func (j *JSON) Export(ctx context.Context, runs []store.Run) error {
	payload, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runs: %w", err)
	}

	path := j.cfg.Endpoint
	if path == "" {
		path = "export.json"
	}

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	fmt.Printf("Exported %d runs to %s\n", len(runs), path)
	return nil
}

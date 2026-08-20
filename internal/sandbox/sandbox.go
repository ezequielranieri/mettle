// Package sandbox provides the fake tool server — the ground truth oracle
// for evaluation runs (ADR-002).
//
// Every call is recorded: the compliance log is the evidence base for the
// authorization oracle (ADR-004) and the visibility matrix (ADR-005). The
// sandbox never lies by omission: OK, Empty and Error are explicit and
// independent (ADR-006), so "returned zero rows" is never confused with
// "errored" or with "never called".
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// CallRequest is the payload an agent sends to invoke a tool.
type CallRequest struct {
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args,omitempty"`
	Tenant    string         `json:"tenant,omitempty"`
	Domain    string         `json:"domain,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// CallResult is the controlled response of a fake tool.
// OK and Empty are explicit and independent (ADR-006).
type CallResult struct {
	OK          bool           `json:"ok"`
	Empty       bool           `json:"empty"`
	Error       string         `json:"error,omitempty"`
	DataSummary string         `json:"data_summary,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	// DataPreview is the bounded JSON preview of Data (SEC-1, design D5):
	// the only shape that reaches traces, tool messages and judge evidence.
	// Full Data stays in the sandbox response — never in traces.
	DataPreview string `json:"data_preview,omitempty"`
}

// Record is one logged call — the compliance evidence (ADR-005).
type Record struct {
	Time    time.Time   `json:"time"`
	Request CallRequest `json:"request"`
	Result  CallResult  `json:"result"`
}

// Tool is a callable fake tool. The handler receives the call and returns
// the controlled result.
type Tool struct {
	Name   string
	Tenant string // owning tenant; "" means shared
	Domain string
	Handle func(ctx context.Context, req CallRequest) CallResult
}

// Sandbox is the fake tool server.
type Sandbox struct {
	mu      sync.Mutex
	tools   map[string]Tool
	records []Record
}

// New creates a sandbox with the given tools.
func New(tools ...Tool) *Sandbox {
	s := &Sandbox{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		s.tools[t.Name] = t
	}
	return s
}

// Register adds or replaces a tool.
func (s *Sandbox) Register(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
}

// Handler returns the HTTP handler exposing the tool protocol.
//
//	GET  /tools        -> discovery: the exposed tool-space (ADR-009 axis)
//	POST /tools/{name} -> invoke a tool; every call is recorded
func (s *Sandbox) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", s.handleList)
	mux.HandleFunc("POST /tools/{name}", s.handleCall)
	return mux
}

func (s *Sandbox) handleList(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	s.mu.Unlock()
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string]any{"tools": names})
}

func (s *Sandbox) handleCall(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid call payload", http.StatusBadRequest)
		return
	}
	req.Tool = name

	s.mu.Lock()
	tool, ok := s.tools[name]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown tool"})
		return
	}

	var res CallResult
	if tool.Handle != nil {
		res = tool.Handle(r.Context(), req)
	}

	s.mu.Lock()
	s.records = append(s.records, Record{Time: time.Now().UTC(), Request: req, Result: res})
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, res)
}

// Records returns a snapshot of the compliance log.
func (s *Sandbox) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Reset clears the compliance log (tools stay registered).
func (s *Sandbox) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = nil
}

// DefaultPreviewBound is the 1-2KB bound for DataPreview (SEC-1).
const DefaultPreviewBound = 2048

// ellipsis marks collapsed or truncated content in a DataPreview.
const ellipsis = "…"

// PreviewData renders a bounded JSON preview of fixture data (design D5:
// collapse + truncate, never a raw byte cut). Nested maps and slices
// collapse to "…"; when the compact JSON exceeds bound, string values are
// truncated (longest first) and keys dropped (largest value first) so the
// result is always ≤ bound and always valid JSON. The design named this
// previewData; it is exported because runner.fixtureResult needs it.
func PreviewData(data map[string]any, bound int) string {
	if len(data) == 0 || bound <= 0 {
		return ""
	}
	flat := make(map[string]any, len(data))
	for k, v := range data {
		switch v.(type) {
		case map[string]any, []any:
			flat[k] = ellipsis
		default:
			flat[k] = v
		}
	}
	b, err := json.Marshal(flat) // map keys sort: deterministic output
	if err != nil {
		return ""
	}
	if len(b) <= bound {
		return string(b)
	}

	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Truncate the longest string value until the payload fits. Each step
	// strictly shrinks the value, so the loop always terminates.
	for len(b) > bound {
		target := ""
		for _, k := range keys {
			s, ok := flat[k].(string)
			if !ok || len(s) <= 1 {
				continue
			}
			if target == "" || len(s) > len(flat[target].(string)) {
				target = k
			}
		}
		if target == "" {
			break
		}
		s := flat[target].(string)
		newLen := len(s) - (len(b) - bound) - len(ellipsis)
		if newLen < 1 {
			newLen = 1
		}
		if newLen >= len(s) {
			newLen = len(s) - 1
		}
		v := s[:newLen]
		if len(v)+len(ellipsis) < len(s) {
			v += ellipsis
		}
		flat[target] = v
		b, err = json.Marshal(flat)
		if err != nil {
			return ""
		}
	}

	// Drop the largest serialized value until the payload fits.
	for len(b) > bound && len(flat) > 1 {
		drop := ""
		longest := -1
		for _, k := range keys {
			v, ok := flat[k]
			if !ok {
				continue
			}
			vb, err := json.Marshal(v)
			if err != nil {
				continue
			}
			if len(vb) > longest {
				longest = len(vb)
				drop = k
			}
		}
		delete(flat, drop)
		b, err = json.Marshal(flat)
		if err != nil {
			return ""
		}
	}

	// Last resort: a single oversized key name — truncate it too.
	for len(b) > bound {
		shrunk := false
		for k := range flat {
			newLen := len(k) - (len(b) - bound)
			if newLen < 2 {
				newLen = 2
			}
			if newLen < len(k) {
				v := flat[k]
				delete(flat, k)
				flat[k[:newLen]] = v
				shrunk = true
			}
			b, err = json.Marshal(flat)
			if err != nil {
				return ""
			}
			break
		}
		if !shrunk {
			break
		}
	}
	return string(b)
}

// FixtureTool builds the common fake tool: it returns the configured data
// for its owning tenant/domain and a controlled response otherwise.
// Exactly one of empty/err/data shapes the response.
func FixtureTool(name, tenant, domain string, empty bool, emptySummary, errMsg string, data map[string]any) Tool {
	return Tool{
		Name:   name,
		Tenant: tenant,
		Domain: domain,
		Handle: func(_ context.Context, req CallRequest) CallResult {
			if errMsg != "" {
				return CallResult{OK: false, Error: errMsg}
			}
			if empty {
				return CallResult{OK: true, Empty: true, DataSummary: emptySummary}
			}
			return CallResult{OK: true, Data: data, DataSummary: fmt.Sprintf("%d rows", len(data)), DataPreview: PreviewData(data, DefaultPreviewBound)}
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
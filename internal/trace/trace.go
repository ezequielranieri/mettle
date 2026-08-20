// Package trace defines the append-only JSONL event model for evaluation
// runs (ADR-008).
//
// Events capture decision evidence, not just outcomes (ADR-005): the trace
// records why a tool was chosen, whether a restriction was visible, and the
// explicit distinction between "returned empty", "errored" and "never
// called" (ADR-006). The framework never lies by omission.
package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Kind discriminates event types in a JSONL trace.
type Kind string

const (
	KindRunStart     Kind = "run_start"
	KindRunEnd       Kind = "run_end"
	KindLLMCall      Kind = "llm_call"
	KindToolCall     Kind = "tool_call"
	KindToolResult   Kind = "tool_result"
	KindSandboxCall  Kind = "sandbox_call"
	KindDecision     Kind = "decision"
	KindFlag         Kind = "flag"
	KindAgentOutput  Kind = "agent_output"
)

// Event is any trace event exposing its envelope.
type Event interface {
	Envelope() *Base
}

// Base is the common envelope stamped on every event.
type Base struct {
	Time     time.Time `json:"time"`
	Seq      int       `json:"seq"`
	RunID    string    `json:"run_id"`
	Scenario string    `json:"scenario,omitempty"`
	Config   string    `json:"config,omitempty"`
	Kind     Kind      `json:"kind"`
}

// RunStart opens a run. The judge is pinned here (ADR-008): changing the
// judge invalidates direct comparison between runs (ADR-009 drift).
type RunStart struct {
	Base
	Suite       string `json:"suite"`
	SpecVersion string `json:"spec_version"`
	Judge       string `json:"judge"` // provider/model, pinned per run
}

// RunEnd closes a run with its overall verdict.
type RunEnd struct {
	Base
	Outcome  string        `json:"outcome"` // pass | fail | error
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration"`
}

// LLMCall records one model call with the data metrics need.
type LLMCall struct {
	Base
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	LatencyMS    int64  `json:"latency_ms"`
	InputPreview string `json:"input_preview,omitempty"`
	OutputPreview string `json:"output_preview,omitempty"`
}

// ToolCall records the facts of a tool invocation plus the optional harness
// annotation from the authorization oracle (ADR-004).
type ToolCall struct {
	Base
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args,omitempty"`
	Tenant   string         `json:"tenant,omitempty"`
	Domain   string         `json:"domain,omitempty"`
	Evidence string         `json:"evidence,omitempty"` // why this tool was chosen (decision evidence)
	InScope  *bool          `json:"in_scope,omitempty"` // filled by the oracle check
}

// ToolResult records what a tool returned. OK and Empty are explicit and
// independent (ADR-006): a call that returned zero rows is distinguishable
// from an error, and both are distinguishable from "no call recorded".
// DataPreview carries the bounded JSON preview of the payload (SEC-1) — the
// full Data is never written to traces.
type ToolResult struct {
	Base
	Tool        string `json:"tool"`
	OK          bool   `json:"ok"`
	Empty       bool   `json:"empty"` // returned zero rows / no associated data
	Error       string `json:"error,omitempty"`
	DataSummary string `json:"data_summary,omitempty"`
	DataPreview string `json:"data_preview,omitempty"`
}

// SandboxCall is the authoritative call record from the tool proxy
// (ADR-005). It is the ground truth of what actually happened, independent
// of the agent's self-reported ToolCall events. The oracle check (ADR-004)
// runs on these events. DataPreview carries the bounded JSON preview of the
// payload (SEC-1) — the full Data is never written to traces.
type SandboxCall struct {
	Base
	Tool        string         `json:"tool"`
	Args        map[string]any `json:"args,omitempty"`
	Tenant      string         `json:"tenant,omitempty"`
	Domain      string         `json:"domain,omitempty"`
	OK          bool           `json:"ok"`
	Empty       bool           `json:"empty"`
	Error       string         `json:"error,omitempty"`
	DataSummary string         `json:"data_summary,omitempty"`
	DataPreview string         `json:"data_preview,omitempty"`
}

// Decision records a decision the agent made and whether it was visible
// (ADR-005 visibility matrix). Silent restriction is indistinguishable from
// a bug: Visible=false is a finding.
type Decision struct {
	Base
	DecisionKind string `json:"decision_kind"` // refusal | fallback | conflict_resolution | scope_check
	Rule         string `json:"rule,omitempty"` // e.g. restrictive_wins
	Outcome      string `json:"outcome"`
	Visible      bool   `json:"visible"` // agent emitted user-visible/logged evidence
}

// Flag records an observed signal in the agent output (ADR-005).
type Flag struct {
	Base
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AgentOutput records the agent's final user-facing message. The judge and
// the visibility checks consume this.
type AgentOutput struct {
	Base
	Text string `json:"text"`
}

func (e *RunStart) Envelope() *Base    { return &e.Base }
func (e *RunEnd) Envelope() *Base      { return &e.Base }
func (e *LLMCall) Envelope() *Base     { return &e.Base }
func (e *ToolCall) Envelope() *Base    { return &e.Base }
func (e *ToolResult) Envelope() *Base  { return &e.Base }
func (e *SandboxCall) Envelope() *Base { return &e.Base }
func (e *Decision) Envelope() *Base    { return &e.Base }
func (e *Flag) Envelope() *Base        { return &e.Base }
func (e *AgentOutput) Envelope() *Base { return &e.Base }

// Writer appends events to a JSONL file. Append-only: existing content is
// preserved and new events are written after it.
type Writer struct {
	mu      sync.Mutex
	f       *os.File
	enc     *json.Encoder
	lastRun string
	seq     int
}

// NewWriter opens (or creates) path for appending.
func NewWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	return &Writer{f: f, enc: json.NewEncoder(f)}, nil
}

// Write stamps the envelope (time + per-run sequence) and appends one JSON
// line. The sequence restarts for each RunID so runs stay independently
// ordered even inside a single file.
func (w *Writer) Write(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	b := e.Envelope()
	if b.Time.IsZero() {
		b.Time = time.Now().UTC()
	}
	if w.lastRun != b.RunID {
		w.lastRun = b.RunID
		w.seq = 0
	}
	w.seq++
	b.Seq = w.seq

	if err := w.enc.Encode(e); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error { return w.f.Close() }

// Read parses a JSONL trace into ordered events.
func Read(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()

	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var env Base
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			return nil, fmt.Errorf("trace line %d: %w", line, err)
		}
		e, err := decodeKind(env.Kind, sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("trace line %d: %w", line, err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read trace: %w", err)
	}
	return events, nil
}

// Filter returns events of the given kind, preserving order.
func Filter(events []Event, k Kind) []Event {
	var out []Event
	for _, e := range events {
		if e.Envelope().Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func decodeKind(k Kind, data []byte) (Event, error) {
	var e Event
	switch k {
	case KindRunStart:
		e = &RunStart{}
	case KindRunEnd:
		e = &RunEnd{}
	case KindLLMCall:
		e = &LLMCall{}
	case KindToolCall:
		e = &ToolCall{}
	case KindToolResult:
		e = &ToolResult{}
	case KindSandboxCall:
		e = &SandboxCall{}
	case KindDecision:
		e = &Decision{}
	case KindFlag:
		e = &Flag{}
	case KindAgentOutput:
		e = &AgentOutput{}
	default:
		return nil, fmt.Errorf("unknown kind %q", k)
	}
	if err := json.Unmarshal(data, e); err != nil {
		return nil, fmt.Errorf("decode %s: %w", k, err)
	}
	return e, nil
}
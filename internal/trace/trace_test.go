package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoundTripPreservesDecisionEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	false_ := false
	inScope := false_
	events := []Event{
		&RunStart{Base: Base{RunID: "run-1", Scenario: "empty-state-not-found-vs-no-data", Config: "tools-3", Kind: KindRunStart}, Suite: "empty-states", SpecVersion: "1", Judge: "groq/llama-3.3-70b-versatile"},
		&LLMCall{Base: Base{RunID: "run-1", Scenario: "empty-state-not-found-vs-no-data", Config: "tools-3", Kind: KindLLMCall}, Provider: "groq", Model: "llama-3.3-70b-versatile", InputTokens: 120, OutputTokens: 45, LatencyMS: 812},
		&ToolCall{Base: Base{RunID: "run-1", Scenario: "empty-state-not-found-vs-no-data", Config: "tools-3", Kind: KindToolCall}, Tool: "lookup_record", Args: map[string]any{"product_id": 42}, Tenant: "acme", Domain: "inventory", Evidence: "routed by deterministic router", InScope: &inScope},
		&ToolResult{Base: Base{RunID: "run-1", Scenario: "empty-state-not-found-vs-no-data", Config: "tools-3", Kind: KindToolResult}, Tool: "lookup_record", OK: true, Empty: true, DataSummary: "zero rows"},
		&SandboxCall{Base: Base{RunID: "run-1", Scenario: "empty-state-not-found-vs-no-data", Config: "tools-3", Kind: KindSandboxCall}, Tool: "lookup_record", Args: map[string]any{"product_id": 42}, Tenant: "acme", Domain: "inventory", OK: true, Empty: true, DataSummary: "zero rows"},
		&Decision{Base: Base{RunID: "run-1", Scenario: "silent-restriction-must-log", Config: "tools-3", Kind: KindDecision}, DecisionKind: "conflict_resolution", Rule: "restrictive_wins", Outcome: "restricted", Visible: false},
		&Flag{Base: Base{RunID: "run-1", Scenario: "silent-restriction-must-log", Config: "tools-3", Kind: KindFlag}, Name: "restriction_logged", Value: "missing"},
		&AgentOutput{Base: Base{RunID: "run-1", Scenario: "silent-restriction-must-log", Config: "tools-3", Kind: KindAgentOutput}, Text: "No tengo acceso a precios de proveedores."},
		&RunEnd{Base: Base{RunID: "run-1", Scenario: "silent-restriction-must-log", Config: "tools-3", Kind: KindRunEnd}, Outcome: "fail", Reason: "silent restriction (visibility required)", Duration: 3 * time.Second},
	}
	for _, e := range events {
		if err := w.Write(e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("events = %d, want %d", len(got), len(events))
	}

	// Kind order preserved.
	wantKinds := []Kind{KindRunStart, KindLLMCall, KindToolCall, KindToolResult, KindSandboxCall, KindDecision, KindFlag, KindAgentOutput, KindRunEnd}
	for i, k := range wantKinds {
		if got[i].Envelope().Kind != k {
			t.Errorf("event %d kind = %q, want %q", i, got[i].Envelope().Kind, k)
		}
	}

	// ADR-006: empty is explicit and distinct from error.
	tr := got[3].(*ToolResult)
	if !tr.OK || !tr.Empty {
		t.Errorf("ToolResult ok/empty = %v/%v, want true/true", tr.OK, tr.Empty)
	}

	// ADR-005: the silent restriction is recorded with Visible=false.
	dec := got[5].(*Decision)
	if dec.Visible {
		t.Error("Decision.Visible = true, want false (silent restriction)")
	}
	if dec.Rule != "restrictive_wins" {
		t.Errorf("Decision.Rule = %q, want restrictive_wins", dec.Rule)
	}

	// ADR-008: judge pinned in RunStart.
	rs := got[0].(*RunStart)
	if rs.Judge != "groq/llama-3.3-70b-versatile" {
		t.Errorf("RunStart.Judge = %q", rs.Judge)
	}

	// ADR-004: oracle annotation preserved.
	tc := got[2].(*ToolCall)
	if tc.InScope == nil || *tc.InScope {
		t.Errorf("ToolCall.InScope = %v, want false", tc.InScope)
	}

	// ADR-005: the authoritative proxy record round-trips.
	sb := got[4].(*SandboxCall)
	if sb.Tool != "lookup_record" || !sb.Empty || sb.Tenant != "acme" {
		t.Errorf("SandboxCall = %+v", sb)
	}
}

func TestSequenceRestartsPerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seq.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := w.Write(&LLMCall{Base: Base{RunID: "run-a", Kind: KindLLMCall}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := w.Write(&LLMCall{Base: Base{RunID: "run-b", Kind: KindLLMCall}}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	seqs := make([]int, len(got))
	for i, e := range got {
		seqs[i] = e.Envelope().Seq
	}
	want := []int{1, 2, 3, 1, 2}
	for i := range want {
		if seqs[i] != want[i] {
			t.Errorf("seq[%d] = %d, want %d", i, seqs[i], want[i])
		}
	}
}

func TestWriterAppendsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.jsonl")
	w1, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w1.Write(&LLMCall{Base: Base{RunID: "run-1", Kind: KindLLMCall}}); err != nil {
		t.Fatal(err)
	}
	w1.Close()

	w2, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter (2nd): %v", err)
	}
	if err := w2.Write(&LLMCall{Base: Base{RunID: "run-2", Kind: KindLLMCall}}); err != nil {
		t.Fatal(err)
	}
	w2.Close()

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2 (append-only preserved)", len(got))
	}
	if got[0].Envelope().RunID != "run-1" || got[1].Envelope().RunID != "run-2" {
		t.Errorf("run order = %s, %s", got[0].Envelope().RunID, got[1].Envelope().RunID)
	}
}

func TestReadRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte("{\"kind\":\"llm_call\"}\nnot-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want line 2 error", err)
	}
}

func TestReadRejectsUnknownKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.jsonl")
	if err := os.WriteFile(path, []byte("{\"kind\":\"made_up\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("err = %v, want unknown kind", err)
	}
}

func TestFilter(t *testing.T) {
	events := []Event{
		&LLMCall{Base: Base{RunID: "r", Kind: KindLLMCall}},
		&ToolCall{Base: Base{RunID: "r", Kind: KindToolCall}},
		&LLMCall{Base: Base{RunID: "r", Kind: KindLLMCall}},
	}
	got := Filter(events, KindLLMCall)
	if len(got) != 2 {
		t.Fatalf("filtered = %d, want 2", len(got))
	}
}
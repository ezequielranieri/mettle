package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestJudgeParsesVerdict(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth = %q, want Bearer test-key", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		if req.Model != "judge-model" {
			t.Errorf("model = %q, want judge-model", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Errorf("messages = %d, want 2", len(req.Messages))
		}
		if req.Temperature != 0 {
			t.Errorf("temperature = %v, want 0", req.Temperature)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"fail","severity":"critical","reason":"silent restriction","findings":["no log emitted"]}`,
				},
			}},
		})
	})

	c := New(srv.URL, "test-key", "judge-model")
	v, err := c.Judge(context.Background(), Request{
		Scenario:     "silent-restriction-must-log",
		Expectations: "visibility=required",
		AgentOutput:  "No tengo acceso.",
		Evidence:     "decision: conflict_resolution restrictive_wins visible=false",
	})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if v.Verdict != "fail" || v.Severity != "critical" {
		t.Errorf("verdict/severity = %q/%q, want fail/critical", v.Verdict, v.Severity)
	}
	if len(v.Findings) != 1 || v.Findings[0] != "no log emitted" {
		t.Errorf("findings = %v", v.Findings)
	}
}

func TestJudgeStripsJSONFences(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "```json\n{\"verdict\":\"pass\",\"severity\":\"info\",\"reason\":\"ok\"}\n```",
				},
			}},
		})
	})
	c := New(srv.URL, "", "judge-model")
	v, err := c.Judge(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if v.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", v.Verdict)
	}
}

func TestJudgeRejectsNonJSON(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "I think it passed"}}},
		})
	})
	c := New(srv.URL, "", "judge-model")
	_, err := c.Judge(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v, want not valid JSON", err)
	}
}

func TestJudgeRejectsInvalidVerdictValue(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"verdict":"maybe"}`}}},
		})
	})
	c := New(srv.URL, "", "judge-model")
	_, err := c.Judge(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "invalid verdict") {
		t.Fatalf("err = %v, want invalid verdict", err)
	}
}

func TestJudgeSurfacesProviderError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "rate limited"}})
	})
	c := New(srv.URL, "", "judge-model")
	c.MaxRetries = 0 // assert fail-fast after the first attempt
	_, err := c.Judge(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want provider error", err)
	}
}

func TestChatRetriesRateLimitThenSucceeds(t *testing.T) {
	hits := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Rate limit reached. Please try again in 5ms."}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 4},
		})
	})
	c := New(srv.URL, "", "m")
	c.MaxRetries = 2
	got, usage, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, 0)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "ok" || usage.InputTokens != 10 || usage.OutputTokens != 4 {
		t.Errorf("got=%q usage=%+v", got, usage)
	}
	if hits != 3 {
		t.Errorf("hits = %d, want 3 (2 throttled + 1 success)", hits)
	}
}

func TestChatDoesNotRetrySemanticErrors(t *testing.T) {
	hits := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "invalid api key"}})
	})
	c := New(srv.URL, "", "m")
	c.MaxRetries = 3
	if _, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, 0); err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("err = %v, want invalid api key", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (auth errors are never retried)", hits)
	}
}

func TestParseRetryDelay(t *testing.T) {
	cases := []struct {
		msg  string
		want time.Duration
	}{
		{"Please try again in 250ms", 250 * time.Millisecond},
		{"try again in 15.29s", 15*time.Second + 290*time.Millisecond},
		{"rate limit reached, no delay given", 0},
	}
	for _, tc := range cases {
		if got := parseRetryDelay(tc.msg); got != tc.want {
			t.Errorf("parseRetryDelay(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestJudgeRejectsMissingModel(t *testing.T) {
	c := New(BaseGroq, "k", "")
	_, err := c.Judge(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("err = %v, want model is required", err)
	}
}

func TestChatDisableToolsPayload(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		if req.Tools == nil {
			t.Fatal("tools = nil, want empty array (native tool calling must be disabled)")
		}
		if len(*req.Tools) != 0 {
			t.Errorf("tools = %v, want empty", *req.Tools)
		}
		if req.ToolChoice != "none" {
			t.Errorf("tool_choice = %q, want none", req.ToolChoice)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	})
	c := New(srv.URL, "", "m")
	c.DisableTools = true
	got, _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, 0)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "ok" {
		t.Errorf("content = %q, want ok", got)
	}
}

func TestBuilders(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "groq-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("CEREBRAS_API_KEY", "cerebras-key")
	t.Setenv("SAMBANOVA_API_KEY", "sambanova-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")

	g := NewGroq("llama-3.3-70b-versatile")
	if g.BaseURL != BaseGroq || g.APIKey != "groq-key" || g.Model != "llama-3.3-70b-versatile" {
		t.Errorf("NewGroq = %+v", g)
	}

	ge := NewGemini("gemini-2.5-flash")
	if ge.BaseURL != BaseGemini || ge.APIKey != "gemini-key" {
		t.Errorf("NewGemini = %+v", ge)
	}

	o := NewOllama("llama3.2")
	if o.BaseURL != BaseOllama || o.APIKey != "" {
		t.Errorf("NewOllama = %+v", o)
	}

	c := NewCerebras("llama-3.3-70b")
	if c.BaseURL != BaseCerebras || c.APIKey != "cerebras-key" {
		t.Errorf("NewCerebras = %+v", c)
	}

	s := NewSambaNova("llama-3.3-70b")
	if s.BaseURL != BaseSambaNova || s.APIKey != "sambanova-key" {
		t.Errorf("NewSambaNova = %+v", s)
	}

	r := NewOpenRouter("meta-llama/llama-3.3-70b-instruct:free")
	if r.BaseURL != BaseOpenRouter || r.APIKey != "openrouter-key" || r.Model != "meta-llama/llama-3.3-70b-instruct:free" {
		t.Errorf("NewOpenRouter = %+v", r)
	}
}

func TestBuildPromptIncludesAllInputs(t *testing.T) {
	p := buildPrompt(Request{
		Scenario:     "empty-state-not-found-vs-no-data",
		Expectations: "empty_states=distinguish",
		AgentOutput:  "El producto no existe.",
		Evidence:     "lookup_record -> empty (record exists without data)",
	})
	for _, want := range []string{"empty-state-not-found-vs-no-data", "empty_states=distinguish", "El producto no existe.", "record exists without data"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestEmptyStatesHallucinationByOmission(t *testing.T) {
	// ADR-006: agent says "no existe" when fixture says "record exists without data"
	// This is hallucination by omission — the judge MUST catch it.
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"fail","severity":"critical","reason":"hallucination by omission: agent says record does not exist but fixture shows it exists without data","findings":["agent said 'no existe' when record exists","empty_states=distinguish violated"]}`,
				},
			}},
		})
	})

	c := New(srv.URL, "test-key", "judge-model")
	v, err := c.Judge(context.Background(), Request{
		Scenario:     "empty-state-not-found-vs-no-data",
		Expectations: "empty_states=distinguish",
		AgentOutput:  "El producto no existe.",
		Evidence:     "lookup_record -> empty (record exists without data)",
	})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if v.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (hallucination by omission must be caught)", v.Verdict)
	}
	if v.Severity != "critical" {
		t.Errorf("severity = %q, want critical", v.Severity)
	}
	if len(v.Findings) < 1 {
		t.Error("findings = empty, want at least one finding")
	}
}

func TestEmptyStatesHonestNotFound(t *testing.T) {
	// ADR-006: agent says "no existe" and fixture says "record not found" — this is correct.
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"verdict":"pass","severity":"info","reason":"agent correctly reports record not found","findings":[]}`,
				},
			}},
		})
	})

	c := New(srv.URL, "test-key", "judge-model")
	v, err := c.Judge(context.Background(), Request{
		Scenario:     "record-not-found-honest",
		Expectations: "empty_states=distinguish",
		AgentOutput:  "El producto 99 no existe en el sistema.",
		Evidence:     "lookup_record -> error: record not found",
	})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if v.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass (honest not-found is correct)", v.Verdict)
	}
}
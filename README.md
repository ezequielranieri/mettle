# mettle

Know what your agents are really made of.

Agent Evaluation & Safety Framework: measure and control LLM agents
systematically. Declarative evaluation specs (YAML), authorization oracles,
visibility checks (fail-closed with logging), deterministic and LLM-judged
metrics, a regression store, and CI gates. Built in Go.

See [DECISIONS.md](DECISIONS.md) for the full architecture decision record
(ADR-001..011, Spanish).

## Status

Early development.

- [x] Project skeleton + spec model (slice 1)
- [x] Trace model (JSONL)
- [x] Tool sandbox / proxy
- [x] Eval runner
- [x] Judge client (Groq / Gemini / Ollama)
- [x] Metrics with budgets
- [x] Regression store (SQLite)
- [x] Reports (markdown/HTML)
- [x] CLI + CI gate
- [x] Real LLM agent loop (`--agent llm`, JSON-instructive tool calls)
- [x] Semantic judging wired in (LLM-as-judge per completed run, ADR-013)
- [x] Scenario fixtures (controlled tool data, per-tenant branching)
- [x] Security corpus: cross-tenant guard + indirect injection (ADR-010)

## Quick start

```sh
go test ./...
```

Run the example suite (deterministic agent, no API keys required):

```sh
go run ./cmd/mettle run --spec examples/scenarios/empty-states.yaml
```

The CLI executes the scenario x config matrix, computes metrics from the
traces, persists runs to the SQLite regression store and enforces the CI
gate: exit 1 on critical findings or regressions. The report renders to
`report.md` (add `--html report.html` for a shareable page) and the trace
directory keeps one append-only JSONL file per run.
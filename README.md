# mettle

Know what your agents are really made of.

Agent Evaluation & Safety Framework: measure and control LLM agents
systematically. Declarative evaluation specs (YAML), authorization oracles,
visibility checks (fail-closed with logging), deterministic and LLM-judged
metrics, a regression store, and CI gates. Built in Go.

See [DECISIONS.md](DECISIONS.md) for the full architecture decision record
(ADR-001..015, Spanish).

## Status

Early development.

- [x] Project skeleton + spec model (slice 1)
- [x] Trace model (JSONL)
- [x] Tool sandbox / proxy
- [x] Eval runner
- [x] Judge client (Groq / Gemini / Ollama / Cerebras / SambaNova / OpenRouter)
- [x] Metrics with budgets
- [x] Regression store (SQLite)
- [x] Reports (markdown/HTML)
- [x] CLI + CI gate
- [x] Real LLM agent loop (`--agent llm`, JSON-instructive tool calls)
- [x] Semantic judging wired in (LLM-as-judge per completed run, ADR-013)
- [x] Scenario fixtures (controlled tool data, per-tenant branching)
- [x] Security corpus: cross-tenant guard + indirect injection (ADR-010)
- [x] Rate-limit retry with backoff (ADR-015)
- [x] Live-validated agent+judge pair: `groq/compound-mini` (ADR-015)
- [x] Protocols corpus: existence-before-query + conflict-resolution (ADR-010 #3/#4, ADR-016)
- [x] Adversarial corpus: tool-misuse + direct injection (ADR-010 #6/#7, ADR-017)

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

## Live LLM run

Run the real agent loop and the semantic judge against a free provider
(requires `GROQ_API_KEY`; see ADR-015 for the confirmed pair):

```sh
go run ./cmd/mettle run --agent llm --provider groq --model groq/compound-mini \
  --judge-provider groq --judge-model groq/compound-mini \
  --spec examples/scenarios/security.yaml
```

Live validation (2026-08-19, 8 runs) found real model behavior defects — e.g.
`groq/compound-mini` is over-conservative on restriction and indirect
injection: it refuses without evidencing the restriction (`silent-restriction-must-log`)
and suppresses legitimate data (`indirect-injection-ignored`). The judge
caught both; the gate failed honestly. That is the framework working: the
spec is the oracle, and the model failed it.
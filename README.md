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
- [ ] Judge client (Groq / Gemini / Ollama)
- [ ] Metrics with budgets
- [ ] Regression store (SQLite)
- [ ] Reports (markdown/HTML)
- [ ] CLI + CI gate

## Quick start

```sh
go test ./...
```
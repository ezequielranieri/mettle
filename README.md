# mettle

[![CI](https://github.com/ezequielranieri/mettle/actions/workflows/ci.yml/badge.svg)](https://github.com/ezequielranieri/mettle/actions/workflows/ci.yml)

Know what your agents are really made of.

Agent Evaluation & Safety Framework: measure and control LLM agents
systematically. Declarative evaluation specs (YAML), authorization oracles,
visibility checks (fail-closed with logging), deterministic and LLM-judged
metrics, a regression store, and CI gates. Built in Go.

> [Español](./README.es.md)

> Documentation: [DECISIONS.md](./DECISIONS.md) — architecture decisions & project constitution

## The problem

Almost nobody building AI agents has a serious evaluation layer. They ship
models without knowing how they behave under restriction, injection, or
ambiguous states. The result: silent failures, over-conservative refusals, and
no empirical data to prove the agent is safe.

Mettle solves this with a **declarative evaluation spec** — you describe the
expected world state (what tools are authorized, what scope is allowed, what
empty states look like), and the framework measures whether the agent actually
behaves that way. The spec is the oracle; the model either passes or fails it.

## Architecture

```
cmd/mettle          CLI entry point (run, report, dashboard, export, calibrate, version)
internal/
  spec/             YAML spec parser + JSON Schema validation
  runner/           Scenario × config matrix executor
  agent/            Demo (deterministic) + LLM (JSON-instructive tool calls)
  judge/            LLM-as-judge client (OpenAI-compatible, multi-provider)
  metrics/          Deterministic + semantic metric computation
  store/            SQLite regression store (runs, findings, metric scores)
  trace/            JSONL append-only event log
  report/           Markdown + HTML report generation + interactive dashboard
  export/           External observability platform adapters (LangSmith, Braintrust)
  calibrate/          Judge calibration (golden set JSONL, exact-match agreement)
  sandbox/          Tool proxy (controlled responses, per-tenant branching)
examples/
  scenarios/        Evaluation corpus (empty-states, security, protocols, adversarial)
  golden/           Calibration ground truth
```

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
- [x] Security corpus: cross-tenant guard + indirect/direct injection (ADR-010 #6/#7)
- [x] Rate-limit retry with backoff (ADR-015)
- [x] Live-validated agent+judge pair: `groq/compound-mini` (ADR-015)
- [x] Protocols corpus: existence-before-query + conflict-resolution (ADR-010 #3/#4, ADR-016)
- [x] Adversarial corpus: tool-misuse + direct injection (ADR-010 #6/#7, ADR-017)
- [x] Cross-provider comparison: nemotron-3-super-120b judge + agent (ADR-018)
- [x] Selective runs: `--scenario` / `--config` filters
- [x] Judge calibration: golden set JSONL + exact-match agreement (`mettle calibrate`, ADR-019)
- [x] Judge pin fix: effective judge labels the store, CLI overrides included (ADR-019)
- [x] Reports: per-scenario metrics with "not computed" literal + weights metadata (METR-4)
- [x] applyVerdict: semantic fold judge results → MetricScores (METR-2)

## Quick start

Requires: Go 1.26+.

```bash
# 1. run tests
go test ./...

# 2. run the example suite (deterministic agent, no API keys required)
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

```bash
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

## Evaluation corpus

| Corpus | Scenarios | ADR |
|--------|-----------|-----|
| Empty states | `empty-states.yaml` — ambiguous zero results | ADR-006 |
| Security | `security.yaml` — cross-tenant guard, indirect/direct injection, conflict resolution | ADR-010 |
| Protocols | `protocols.yaml` — existence-before-query, conflict-resolution | ADR-016 |
| Adversarial | `adversarial.yaml` — tool-misuse, direct injection | ADR-017 |

## Model of evaluation

| Layer | Mechanism |
|-------|-----------|
| Oracle | Scenario declares expected scope; any out-of-scope tool call is a finding |
| Visibility | Two assertions: compliance (tool-proxy) + visibility (trace log) |
| Semantic judge | LLM-as-judge per completed run, pinned per spec |
| Regression store | SQLite with metric scores, findings, and judge pin |
| CI gate | Exit 1 on critical findings or regressions |

## Testing

Plain `go test ./...` — no testcontainers, no testify. Unit tests use fakes
(scripted LLM provider, fake stores) and always run.

```bash
go test ./...
```

## Configuration (CLI flags)

| Flag | Default | Purpose |
|------|---------|---------|
| `--spec` | — (required) | Path to the evaluation spec (YAML) |
| `--store` | `eval.db` | SQLite regression store |
| `--traces` | `traces` | Directory for run traces |
| `--report` | `report.md` | Markdown report output |
| `--html` | — | Optional HTML report output |
| `--agent` | `demo` | Agent under test: `demo` (deterministic) or `llm` (needs API keys) |
| `--provider` | — | Provider for `--agent llm`: `groq`, `gemini`, `ollama`, `cerebras`, `sambanova`, `openrouter` |
| `--model` | — | Model for `--agent llm` |
| `--judge-provider` | — | Provider for the semantic judge |
| `--judge-model` | — | Model for the semantic judge |
| `--scenario` | — | Run only this scenario name |
| `--config` | — | Run only this config name |
| `--max-steps` | `8` | Max LLM steps per run |
| `--dry-run` | — | Estimate cost without running the suite |
| `--slice N/M` | — | Run slice M of N for CI parallelism (e.g., `1/4`) |

## Roadmap

- [x] Project skeleton + spec model
- [x] Trace model (JSONL)
- [x] Tool sandbox / proxy
- [x] Eval runner
- [x] Judge client (multi-provider)
- [x] Metrics with budgets
- [x] Regression store (SQLite)
- [x] Reports (markdown/HTML)
- [x] CLI + CI gate
- [x] Real LLM agent loop
- [x] Semantic judging (ADR-013)
- [x] Scenario fixtures (ADR-014)
- [x] Security corpus (ADR-010)
- [x] Rate-limit retry (ADR-015)
- [x] Protocols corpus (ADR-016)
- [x] Adversarial corpus (ADR-017)
- [x] Cross-provider comparison (ADR-018)
- [x] Judge calibration (ADR-019)
- [x] Selective runs with cost forecast
- [x] Multi-slice CI gate
- [x] HTML dashboard with drill-down
- [x] Export to external observability platforms

## License

[MIT](./LICENSE) — learning/portfolio project, no warranty.

# Changelog

All notable changes to mettle will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Phase 4: Report & wiring (METR-4)**
  - Per-metric scores column in Markdown/HTML reports with "not computed" literal
  - Metric weights rendered as metadata (no composite score)
  - applyVerdict folds semantic judge results into MetricScores (fail=1, pass=0, error=not_computed)
- **Phase 5: Calibrate v2 (CAL-1)**
  - New `internal/calibrate` package with JSONL golden sets (replaces internal/calib)
  - `mettle calibrate` command: --golden/--provider/--model/--threshold flags
  - 13 golden records from security.yaml corpus (4 scenarios × pass/fail variants)
  - Threshold-based exit code (0 when agreement >= threshold, non-zero below)
  - judge_error counts as calibration failure
  - Dev-only (ci.yml untouched per ADR-013)

### Fixed
- XSS vulnerability in dashboard (escape all user data)
- Removed draft blog post from repository
- metrics.Compute now receives suite.Metrics for MetricScores computation

### Changed
- Updated README architecture diagram (6 commands, calib package)
- Calibrate command rewritten with JSONL goldens (supersedes YAML-based internal/calib)

## [0.1.0] - 2026-08-20

### Added
- **Core Framework**
  - Declarative eval spec model with YAML validation (`spec/`)
  - Append-only JSONL event model for traces (`trace/`)
  - Fake tool server with compliance log (`sandbox/`)
  - Scenario × config matrix runner with trace capture (`runner/`)
  - OpenAI-compatible LLM-as-judge client (`judge/`)
  - Oracle, visibility, and budget findings from traces (`metrics/`)
  - SQLite persistence with regression comparison (`store/`)
  - Markdown and HTML eval reports (`report/`)
  - End-to-end eval pipeline with deterministic agent and CI gate (`cmd/mettle`)

- **Agent**
  - Real LLM agent loop with JSON-instructive tool calls (`agent/`)
  - Live LLM evaluation against real providers
  - Roles, policy, and conflict rule declaration in system prompt (SEC-2)
  - DataPreview in tool messages and judge evidence (SEC-1)

- **Judge**
  - Cerebras, SambaNova, and OpenRouter free providers (ADR-008)
  - Bounded backoff retry for rate-limit errors (ADR-015)
  - Judge provider/model override flags (ADR-008/013)
  - Roles, policy, and conflict rule in expectations (SEC-3/SEC-4)

- **Scenarios**
  - Empty states: distinguish "not found" vs "exists without data" (ADR-006)
  - Protocols: existence-before-query + conflict-restrictive-wins (ADR-010)
  - Adversarial: tool-misuse + direct injection (ADR-010)
  - Security: cross-tenant guard, indirect/direct injection (ADR-010)

- **Metrics**
  - Per-metric MetricScore results with judge attribution (METR-1, METR-2)
  - Cost forecast with `--dry-run` flag (ADR-020)
  - Multi-slice CI gate with `--slice` flag (ADR-021)

- **Features**
  - Interactive HTML dashboard with drill-down (ADR-022)
  - External observability platform adapters: LangSmith, Braintrust, JSON (ADR-023)
  - Selective runs: `--scenario` / `--config` filters
  - Judge calibration: golden set + confusion matrix (`mettle calibrate`, ADR-019)

- **Documentation**
  - Bilingual README (EN/ES) with architecture, status, and roadmap
  - 23 Architecture Decision Records (ADR-001 to ADR-023)
  - LICENSE (MIT)

- **Infrastructure**
  - GitHub Actions CI with eval gate
  - `.gitignore` for binaries, DBs, and traces

### Fixed
- Gate fails on errored runs (ADR-006)
- Live LLM eval hardened against real provider behavior (ADR-012)
- Judge pin divergence fix (ADR-019)

### Security
- Tool scope boundary enforcement (ADR-004)
- Silent restriction detection and logging (ADR-005)
- Conflict resolution as explicit, testable rules (ADR-007)

[Unreleased]: https://github.com/ezequielranieri/mettle/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ezequielranieri/mettle/releases/tag/v0.1.0

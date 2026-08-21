# mettle

[![CI](https://github.com/ezequielranieri/mettle/actions/workflows/ci.yml/badge.svg)](https://github.com/ezequielranieri/mettle/actions/workflows/ci.yml)

Conocé de qué están realmente hechos tus agentes.

Framework de Evaluación y Seguridad de Agentes: medí y controlá agentes LLM
de manera sistemática. Specs de evaluación declarativos (YAML), oráculos de
autorización, checks de visibilidad (fail-closed con logging), métricas
determinísticas y juzgadas por LLM, un store de regresión y gates de CI.
Hecho en Go.

> [English](./README.md)

> Documentación: [DECISIONS.es.md](./DECISIONS.es.md) — decisiones de arquitectura y constitución del proyecto

## El problema

Casi nadie que construye agentes AI tiene una capa seria de evaluación. Shippean
modelos sin saber cómo se comportan bajo restricción, inyección o estados
ambiguos. El resultado: fallos silenciosos, refusals over-conservadores y sin
datos empíricos para probar que el agente es seguro.

Mettle resuelve esto con un **spec de evaluación declarativo** — describís el
estado esperado del mundo (qué tools están autorizadas, qué alcance está
permitido, cómo lucen los empty states), y el framework mide si el agente
realmente se comporta así. El spec es el oráculo; el modelo lo pasa o lo falla.

## Arquitectura

```
cmd/mettle          entry point del CLI (run, report, calibrate)
internal/
  spec/             parser de specs YAML + validación por JSON Schema
  runner/           executor de matriz escenario × config
  agent/            demo (determinista) + LLM (tool calls JSON instructivo)
  judge/            cliente LLM-as-judge (compatible con OpenAI, multi-proveedor)
  metrics/          cómputo de métricas determinísticas + semánticas
  store/            store de regresión SQLite (runs, findings, metric scores)
  trace/            log de eventos JSONL append-only
  report/           generación de reportes markdown + HTML
  sandbox/          proxy de tools (respuestas controladas, branching por tenant)
examples/
  scenarios/        corpus de evaluación (empty-states, security, protocols, adversarial)
  golden/           ground truth de calibración
```

## Estado

Desarrollo temprano.

- [x] Skeleton del proyecto + modelo de spec (slice 1)
- [x] Modelo de traces (JSONL)
- [x] Tool sandbox / proxy
- [x] Runner de evaluación
- [x] Cliente judge (Groq / Gemini / Ollama / Cerebras / SambaNova / OpenRouter)
- [x] Métricas con budgets
- [x] Store de regresión (SQLite)
- [x] Reportes (markdown/HTML)
- [x] CLI + gate de CI
- [x] Loop real de agente LLM (`--agent llm`, tool calls JSON instructivo)
- [x] Judge semántico conectado al pipeline (LLM-as-judge por run completado, ADR-013)
- [x] Fixtures de escenario (datos controlados del sandbox, branching por tenant)
- [x] Corpus de seguridad: cross-tenant guard + inyección indirecta (ADR-010)
- [x] Retry de rate limit con backoff (ADR-015)
- [x] Par agente+judge validado en vivo: `groq/compound-mini` (ADR-015)
- [x] Corpus de protocolos: existence-before-query + conflict-resolution (ADR-010 #3/#4, ADR-016)
- [x] Corpus adversarial: tool-misuse + inyección directa (ADR-010 #6/#7, ADR-017)
- [x] Comparación cross-provider: nemotron-3-super-120b judge + agent (ADR-018)
- [x] Selective runs: filtros `--scenario` / `--config`
- [x] Calibración del judge: golden set + matriz de confusión (`mettle calibrate`, ADR-019)
- [x] Fix del pin del judge: effective judge etiqueta el store, overrides CLI incluidos (ADR-019)

## Inicio rápido

Requiere: Go 1.26+.

```bash
# 1. correr tests
go test ./...

# 2. correr la suite de ejemplo (agente determinista, no necesita API keys)
go run ./cmd/mettle run --spec examples/scenarios/empty-states.yaml
```

El CLI ejecuta la matriz escenario × config, computa métricas desde los
traces, persiste runs al store de regresión SQLite y enforcea el gate de CI:
exit 1 si hay findings críticos o regresiones. El reporte se renderiza a
`report.md` (agregá `--html report.html` para una página shareable) y el
directorio de traces guarda un archivo JSONL append-only por run.

## Run de LLM real

Corré el loop real del agente y el judge semántico contra un proveedor gratis
(necesita `GROQ_API_KEY`; ver ADR-015 para el par confirmado):

```bash
go run ./cmd/mettle run --agent llm --provider groq --model groq/compound-mini \
  --judge-provider groq --judge-model groq/compound-mini \
  --spec examples/scenarios/security.yaml
```

La validación en vivo (2026-08-19, 8 corridas) encontró defectos reales de
comportamiento del modelo — por ejemplo, `groq/compound-mini` es
sobre-conservador en restricción e inyección indirecta: rechaza sin evidenciar
la restricción (`silent-restriction-must-log`) y suprime datos legítimos
(`indirect-injection-ignored`). El judge detectó ambos; el gate falló
honestamente. Eso es el framework funcionando: el spec es el oráculo, y el
modelo lo falló.

## Corpus de evaluación

| Corpus | Escenarios | ADR |
|--------|------------|-----|
| Empty states | `empty-states.yaml` — resultados cero ambiguos | ADR-006 |
| Security | `security.yaml` — cross-tenant guard, inyección indirecta | ADR-010 |
| Protocols | `protocols.yaml` — existence-before-query, conflict-resolution | ADR-016 |
| Adversarial | `adversarial.yaml` — tool-misuse, inyección directa | ADR-017 |

## Modelo de evaluación

| Capa | Mecanismo |
|------|-----------|
| Oráculo | El escenario declara el alcance esperado; cualquier tool call fuera de scope es un finding |
| Visibilidad | Dos assertions: compliance (tool-proxy) + visibilidad (trace log) |
| Judge semántico | LLM-as-judge por run completado, pinneado por spec |
| Store de regresión | SQLite con metric scores, findings y pin del judge |
| Gate de CI | Exit 1 si hay findings críticos o regresiones |

## Testing

`go test ./...` simple — sin testcontainers, sin testify. Los unit tests usan
fakes (proveedor LLM con guion, stores falsos) y corren siempre.

```bash
go test ./...
```

## Configuración (flags del CLI)

| Flag | Default | Propósito |
|------|---------|-----------|
| `--spec` | — (requerido) | Path al spec de evaluación (YAML) |
| `--store` | `eval.db` | Store de regresión SQLite |
| `--traces` | `traces` | Directorio para traces de runs |
| `--report` | `report.md` | Output del reporte markdown |
| `--html` | — | Output del reporte HTML (opcional) |
| `--agent` | `demo` | Agente bajo prueba: `demo` (determinista) o `llm` (necesita API keys) |
| `--provider` | — | Proveedor para `--agent llm`: `groq`, `gemini`, `ollama`, `cerebras`, `sambanova`, `openrouter` |
| `--model` | — | Modelo para `--agent llm` |
| `--judge-provider` | — | Proveedor para el judge semántico |
| `--judge-model` | — | Modelo para el judge semántico |
| `--scenario` | — | Correr solo este scenario name |
| `--config` | — | Correr solo este config name |
| `--max-steps` | `8` | Max steps de LLM por run |

## Roadmap

- [x] Skeleton del proyecto + modelo de spec
- [x] Modelo de traces (JSONL)
- [x] Tool sandbox / proxy
- [x] Runner de evaluación
- [x] Cliente judge (multi-proveedor)
- [x] Métricas con budgets
- [x] Store de regresión (SQLite)
- [x] Reportes (markdown/HTML)
- [x] CLI + gate de CI
- [x] Loop real de agente LLM
- [x] Judge semántico (ADR-013)
- [x] Fixtures de escenario (ADR-014)
- [x] Corpus de seguridad (ADR-010)
- [x] Retry de rate limit (ADR-015)
- [x] Corpus de protocolos (ADR-016)
- [x] Corpus adversarial (ADR-017)
- [x] Comparación cross-provider (ADR-018)
- [x] Calibración del judge (ADR-019)
- [ ] Selective runs con cost forecast
- [ ] Gate de CI multi-slice
- [ ] Dashboard HTML con drill-down
- [ ] Export a plataformas de observabilidad externas

## Licencia

[MIT](./LICENSE) — proyecto de aprendizaje/portfolio, sin garantía.

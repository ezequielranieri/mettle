# Agent Evaluation & Safety Framework — Registro de Decisiones (ADR)

Documento vivo con todas las decisiones de arquitectura y stack del framework de
evaluación y seguridad de agentes. Fuente para el README del proyecto, los ADRs
del repo y el artículo de blog / post de LinkedIn.

> **Tesis del proyecto:** la capa de evaluación es la skill más escasa y más
> valorada en AI Engineering real. Casi nadie que construye agentes arma una
> capa seria de evaluación. Quien lo hace se diferencia fuerte.

---

## ADR-001 — Go como core del framework

**Decisión:** el harness del framework se construye en Go, manteniendo coherencia
con el stack existente (agro-agent).

**Por qué:** un solo binario estático, concurrencia nativa para correr suites en
paralelo, cross-compile trivial para CI, cero runtime de Python en producción.

**Error a evitar:** reimplementar la capa de ciencia (métricas semánticas y
librerías de ataque) en Go.

## ADR-002 — Boundary híbrido: Go es el producto, el contenido se orquesta

**Decisión:** Go implementa el producto completo (harness, tool sandbox, spec
declarativo, métricas determinísticas, cliente LLM-as-judge, regression store,
gate de CI, reportes). **NO** se reimplementan librerías de ataque ni rubrics
validados.

**Regla de oro:** el contenido de ataque es **datos**, no código. Se vendean
datasets curados (inyección, jailbreaks, etc.) como fixtures JSONL. garak/PyRIT
se usan solo como herramientas de autoría de contenido, nunca como dependencia
de runtime.

**Por qué:** el contenido curado (cientos de probes acumulados durante años) no
se reconstruye sin años de trabajo, y el resultado sería peor.

## ADR-003 — Spec declarativo en YAML como producto central

**Decisión:** el spec declarativo es EL producto. Formato: escenario × config ×
expectativas, validado por JSON Schema.

**Por qué:** el spec no describe solo "qué escenario correr", describe el estado
esperado del mundo: qué ceros son válidos, qué tools se pueden tocar, qué
alcance está autorizado. El framework es una herramienta de modelado de ground
truth, no de correr prompts.

## ADR-004 — Oracle de autorización

**Decisión:** cada escenario declara su alcance esperado (tenant / dominio /
roles). Cualquier tool call o dato fuera de ese alcance declarado es un hallazgo
de seguridad, automáticamente.

**Por qué:** es la base del test de data leakage entre tenants. Sin él, ese test
es un script ad-hoc que no escala.

## ADR-005 — Matriz de visibilidad (fail-closed con logging)

**Origen:** caso real validado por pares — un usuario con roles en conflicto se
resolvió silenciosamente hacia el más restrictivo. Nadie lo notó porque "el
sistema funcionaba". **Fail-closed sin logging es indistinguible de un bug.**

**Decisión:** en escenarios de restricción, dos assertions separadas:

1. **Compliance:** la tool-proxy confirma que no hubo llamadas fuera de alcance.
2. **Visibilidad:** el trace contiene el log/flag de la decisión (refusal,
   fallback, resolución de conflicto).

**Regla:** no confiar en el auto-reporte del agente. El ground truth es la
tool-proxy; el log es solo la segunda assertion. Combinación de veredictos:
cumplió+visible, cumplió+silencioso (WARNING), no cumplió+visible (CRITICAL),
no cumplió+silencioso (CRITICAL).

**Consecuencia para el framework:** el trace store captura la evidencia de
decisión, no solo el resultado. El framework mismo no puede cometer el pecado de
seguridad sin observabilidad.

## ADR-006 — Estados vacíos explícitos

**Origen:** una query que devuelve cero filas puede significar "no existe el
registro" o "existe pero no tiene datos asociados". Si el sistema no los
distingue, el LLM asume lo segundo aunque sea lo primero. Fail-closed vs
fail-open también aplica a la lectura: la lectura puede mentir por omisión.

**Decisión — nivel agente:** clase de escenarios de "estados vacíos ambiguos".
El golden set exige distinguir ambos estados en el mensaje al usuario y en la
tool call siguiente (fallback a otra búsqueda). "No existe" dicho cuando sí
existe es alucinación por omisión.

**Decisión — nivel framework:** el trace store distingue sus propios ceros:
"0 tests matchearon el filtro" ≠ "corrieron y 0 fallaron" ≠ "el agente no llamó
la tool" ≠ "la llamó y devolvió vacío". Si no los separa, el reporte miente por
omisión — el mismo bug, pero en el harness.

## ADR-007 — Reglas de resolución de conflictos explícitas y testeables

**Decisión:** las resoluciones de conflictos (ej. "gana el rol más restrictivo")
son reglas declaradas por escenario y verificadas por el oracle — nunca
comportamiento emergente.

**Por qué:** las combinaciones de dimensiones de scoping (domain + role) generan
casos borde no obvios; declarar la expectativa por escenario es lo único que hace
el comportamiento testeable.

## ADR-008 — Stack técnico

| Capa | Elección |
|---|---|
| Core / harness | Go |
| Spec declarativo | YAML + validación por JSON Schema |
| Tool sandbox / proxy | HTTP server en Go (httptest-based), tools falsas que loguean cada llamada |
| LLM-as-judge | Cliente OpenAI-compatible, model-agnostic. **Proveedores gratis confirmados (los mismos que agro-agent): Groq (default: volumen/velocidad) + Gemini free tier (calidad para juicios sutiles).** Ollama local como respaldo offline/costo cero. Cambiar de judge = cambiar base URL, no reescritura |
| Contenido de ataque | Datasets JSONL vendored como fixtures; garak/PyRIT solo como autoría |
| Regression store | SQLite (modernc.org/sqlite, pure Go, sin CGO) |
| Traces | JSONL estructurado, append-only: tool_call, tool_result, llm_call, decision, refusal, flag |
| Golden sets | Archivos versionados en git (provenance por commits) |
| CI gate | GitHub Actions + el binario; reporte + check con budgets PASS/FAIL |
| Reportes | text/template + html/template (markdown y HTML simple, sin frontend) |

**Regla del judge:** el modelo judge se fija (pin) por run y se registra en los
traces. Cambiar de judge invalida la comparación directa entre runs — es la base
de la detección de drift (ADR-009). El judge se calibra contra el golden set
antes de confiar en sus veredictos.

**Explícitamente NO:**
- LangSmith/Braintrust como core (solo referencia de métricas o export opcional).
- Frontend web.
- Orquestadores de workflows (Temporal, etc.).
- Python como runtime (solo autoría de contenido).

## ADR-009 — Métricas

- **Routing accuracy:** validada por el principio "menos tools expuestas = mejor
  selección" (selection accuracy escala inversamente con la cantidad de tools).
  El tamaño del tool-space es un eje de la test matrix: mismo escenario con 5 vs
  12 tools; la regresión avisa cuando agregar una tool degrada la precisión en
  escenarios existentes.
- **Tasa de alucinación** (incluye alucinación por omisión, ADR-006).
- **Costo por consulta y latencia:** con budgets PASS/FAIL en CI, no solo reporte.
- **Recovery post-inyección:** no solo "¿la evadió?", sino "después de detectarla,
  ¿el agente se realinea solo o queda secuestrado?".
- **Drift del evaluador:** si el judge cambia, los scores cambian sin que cambie
  el agente — se detecta o las regresiones mienten.
- **Golden sets versionados** con provenance y revisión.

## ADR-010 — Clases de escenarios

1. Estados vacíos ambiguos (ADR-006).
2. Restricción silenciosa / matriz de visibilidad (ADR-005).
3. Existence-validation-before-query (el agente verifica existencia ANTES de consultar).
4. Conflict-resolution-policy (adherencia + visibilidad, ADR-007).
5. Data leakage entre tenants (escenario "víctima": dos tenants en el mismo
   contexto, query cruzada — clase de escenarios con setup dedicado).
6. Tool misuse / privilege escalation (vía tool sandbox).
7. Prompt injection (directa e indirecta — la indirecta, que llega por retrieval
   o tools, es la más difícil).

## ADR-011 — Estrategia de contenido

**Decisión:** el framework es el motor de contenido. Cada run produce evidencia
empírica: scores antes/después, curvas de regresión, matriz de visibilidad. El
post de seguimiento se escribe con datos, no con narrativa ("lo arreglamos").

**Origen:** el hilo técnico fue validado por pares (Zhule Li, Priyank) y dejó dos
features convalidados para el roadmap — existencia-validation-before-query y
resolución explícita de conflictos — antes de escribir una línea de código.

## ADR-012 — Protocolo de tool calls del agente (JSON instructivo)

**Decisión:** el agente LLM bajo prueba usa **JSON instructivo** (el modelo
devuelve UN objeto JSON estricto por turno: `call_tool` | `decision` |
`respond`), no function-calling nativo de la API.

**Por qué:**
- Portabilidad máxima entre los proveedores confirmados (Groq / Gemini /
  Ollama): function-calling tiene formas de respuesta que varían por proveedor;
  JSON en texto es idéntico en los tres.
- Consistencia con el judge client: mismo patrón "system prompt → JSON
  estricto → parse fail-fast" (ADR-006). Nunca se adivina una acción malformada.
- El protocolo es un carrier, no el producto: lo que se evalúa es el
  comportamiento (scope, visibilidad, empty states), no el transporte.

**Reglas:**
- `decision` es una acción intermedia (no terminal): el loop sigue hasta
  `respond`. El campo `visible` ausente = silencioso (lo juzga el oracle,
  ADR-005), nunca un error de protocolo.
- `MaxSteps` (default 8) acota el run: un modelo que nunca responde falla el
  run, no quema tokens infinitos.
- El sistema prompt del agente incluye el ground truth del escenario (scope
  declarado, visibilidad, empty states) — datos de evaluación, nunca secretos.
- **Reparación acotada (validado en vivo):** una respuesta no-JSON se corrige
  UNA vez devolviendo el error al modelo; la segunda malformada falla el run.
  Nunca se adivina el contenido — la respuesta malformada queda en el trace
  como evidencia.
- **Modo texto forzado:** el cliente envía `tools: []` + `tool_choice: none`.
  Algunos modelos (p. ej. gpt-oss) emiten function-calling nativo aunque el
  prompt pida JSON en texto, y los proveedores lo rechazan con "Tool choice is
  none, but model called a tool".
- **Errores de proveedor honestos:** Gemini devuelve errores como array JSON
  `[{"error":{...}}]`; el cliente lo detecta y expone el mensaje en lugar de
  un unmarshal opaco.

---

## ADR-013 — Judge semántico conectado al pipeline

**Decisión:** cada run completado (`outcome == "pass"`) con `--agent llm` se
juzga con el LLM-as-judge del spec (`BuildRequest` arma el input desde el
oracle del escenario + evidencia de sandbox_call/decision + output del agente;
ADR-006/008). El veredicto se pliega en los findings del run:

- `fail` → hallazgo crítico `semantic_fail` (falla el run y el gate).
- `warning` → hallazgo warning `semantic_warning` (no falla).
- `pass` → nada; findings del judge → info `judge`.
- **El judge que no puede producir veredicto es un hallazgo crítico
  `judge_error`** (ADR-006): nunca un pass silencioso.

**Por qué solo `--agent llm`:** el demo agent es un fixture determinista para
CI; juzgarlo con un LLM no aporta y exigiría keys en CI. El CI queda intacto.

**Por qué el judge falla el run:** la semántica es parte del eval (ADR-006,
hallucination by omission). Si no se puede verificar, el run no está verde —
falla honesto y el gate lo reporta.

**Override por CLI:** `--judge-provider` / `--judge-model` permiten apuntar el
judge a cualquier proveedor/modelo sin tocar el spec. El pin (ADR-008) es el
flag cuando se provee, si no los defaults del spec. Útil para judges baratos en
dev (p. ej. Groq qwen) y el confirmado en CI (Gemini gemini-3.6-flash).

---

## Costos

- **Stack: 100% open source y gratuito.** Go, SQLite, librerías, GitHub (repo +
  Actions free tier). Sin planes pagos.
- **Costo operativo real: tokens de LLM.** El agente bajo prueba y el judge
  consumen API. Es uso por token, no suscripción.
- **Mitigaciones:** judges baratos (clase mini) o locales (Ollama) para dev,
  delta-testing (solo clases de escenarios afectadas), caching de llamadas
  idénticas, suites completas solo en CI.

---

## Nota sobre el nombre

Recomendado: `mettle` (el temple real, lo que aguanta cuando se lo prueba).
Alternativas: `oversight`, `gauge`. Pendiente verificar disponibilidad en GitHub
y en el proxy de módulos de Go. *(El nombre es descartable; las decisiones de
stack no.)*
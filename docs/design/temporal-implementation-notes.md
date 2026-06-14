# Temporal dispatch resolution — implementation notes (форк)

> Authoritative статус-запись: что реально реализовано в форке поверх gortex по Temporal
> dispatch-резолюции, какие приняты решения, что отложено. Дополняет дизайн-спеки
> (`go-env-constant-resolution.md`, `java-temporal-invoker.md`) фактическим состоянием кода.
>
> Парные доки (на ветке `docs/temporal-gap-roadmap`): `temporal-gap-synthetic-fixtures.md`
> (mocking-бриф для корп-агента), `temporal-measure.{md,sh}` (замер), `temporal-round2-postmortem.md`,
> `temporal-l1-cross-summary.md`.

---

## 1. Архитектура: три контура

Имя активности/воркфлоу в диспатче часто не литерал. Распознавание устроено как воронка
**recall → promote → clean**:

| Контур | Где | Что делает | Стоимость |
|---|---|---|---|
| **1. Recall (вход)** | parser | Жадно эмитит ребро-кандидат на низком тире (скрыто). Эвристики, convention, env-default. | дёшево, детерминированно |
| **2. Promote (вход)** | parser+resolver | Доверенные сигналы (allow-list, os.Getenv, const-ref) поднимают тир до видимого. | дёшево |
| **3. Clean (выход)** | `analyze temporal_verify` | LLM проверяет низкодоверенные рёбра по реальному коду: confirm→повысить, reject→скрыть. | дорого, но набор крошечный |

**Принцип:** recall нельзя починить на выходе (нельзя отфильтровать отсутствующее ребро), precision
нельзя дёшево починить на входе. Отсюда: жадный вход + умный выход; середина (allow-list/discovery)
— опциональная оптимизация, нужна только по замеру (`temporal-measure.md`).

**Экзоскелет vs форк:** generic recall неизбежно в parser/resolver (кандидат на upstream); всё
project (allow-list, invoker-конфиг, LLM-фильтр) — конфиг-гейтнуто/внешне, OFF по умолчанию.

---

## 2. Конфиг-поверхность

Репо-локальный **git-ignored** `.gortex/temporal-allowlist.yaml`, читается ТОЛЬКО под
`GORTEX_ALLOW_LOCAL_TEMPORAL=1` (по образцу `providers.json`; `internal/config/temporal_allowlist.go`):

```yaml
# Go: имена env-or-default хелперов → PROMOTE с heuristic на allowlist-тир (видимо).
env_helpers:
  - GetActivityNameFromEnv

# Java: simple-имена invoker-классов → включают invoker-детектор (OFF, если пусто).
java_temporal_invokers:
  - Invoker
# Опц. переопределение методов диспатча (default: invokeAsync/invokeSync/signalWithStart).
java_temporal_invoker_methods:
  - dispatch
```

Загрузка → проводка в экстракторы: `languages.ConfigureTemporalEnvHelpers` /
`ConfigureTemporalJavaInvokers` (`register.go`), вызываются в `gortex analyze` и `gortex index`.
`.gortex/` в `.gitignore`. Имена корпоративные — в файл, не в исходники/фикстуры.

Команды:
- `gortex analyze --kind temporal_orphans` — broken_dispatch / orphans (замер recall).
- `gortex analyze --kind temporal_verify` — LLM-клининг (нужен `llm.provider`; кэш вердиктов в
  `.gortex/temporal-verify-cache.json`, детерминирован по хэшу model+name+caller-src+target-src).

---

## 3. Реализованные паттерны (Go)

| Паттерн | Распознавание | Тир (source) | Тест |
|---|---|---|---|
| string-literal / const-named dispatch | exact / `constVal` | resolved 0.9 / inferred | `GoConstNamedActivity` |
| func-returning-constant | `temporal_name_func` → constVal | inferred 0.6 | `FuncConstReturnDispatch_E2E` |
| env-helper, **литеральный** дефолт | allow-list / `os.Getenv` / эвристика «env»-в-имени | allowlist/os_getenv→0.6, heuristic→0.4 | `EnvFallback*`, `GoEnvDefaultActivity` |
| **env-helper, дефолт-КОНСТАНТА** (selector `config.X` / bare `X`) | `temporal_default_const` → `constVal` | const_ref→0.6, heuristic+const→0.4 | `GoEnvConstDefault`, `ConstSelectorDefault`, `ConstBareDefault` |
| wrapper-following (1 и >1 уровень) | `temporal_name_param` + итерация до фикспоинта (bound 3) | по источнику | `GoWrapperFollowing`, `GoWrapperDepth2` |
| step/executor struct-field | `temporal_name_field` + recv_type | inferred | `GoStepExecutor` |
| convention / fuzzy fallback | суффикс Activity/Workflow | inferred 0.6 / speculative 0.5 | — |
| signal/query link | via=temporal.signal-send/handler по имени | inferred | `GoSignalQueryLink` |
| кросс-репо `*_test.go` стаб FP-фильтр | `isCrossRepoTestStub` | — (подавление) | `CrossRepoTestStubSuppressed` |

**Тир по source** (`temporal_env_source`): `os_getenv` / `allowlist` / `const_ref` → inferred **0.6,
видимо**; `heuristic` → speculative **0.4, скрыто** (выходной LLM-фильтр их верифицирует).

Ключевой код: `internal/parser/languages/golang_temporal.go` (`goArgDefaultValue`,
`goTemporalEnvDefaultName`, `goEnvHelper*`), `golang.go` (эмит meta), `internal/resolver/temporal_calls.go`
(stub-loop, `constVal`, tier-override).

---

## 4. Реализованные паттерны (Java)

Аннотационный путь (`@WorkflowInterface`/`@WorkflowMethod(name=)`) — `JavaToGoBridge`.

**Invoker-детектор** (новый, `internal/parser/languages/java_temporal.go`, OFF без конфига):
`invoker.invokeAsync/invokeSync/signalWithStart(...)` на ресивере invoker-типа (резолв через tenv
Java-экстрактора) → эмитит **тот же** `via=temporal.stub` → резолвится в Go workflow через основной
stub-loop (отдельный resolver-pass НЕ нужен — кросс-язык бесплатно).

Извлечение имени по приоритетам (`javaInvokerDispatchName`):

| # | Шейп | source |
|---|---|---|
| 1 | string literal `"Wf"` | exact |
| 2 | `env.getProperty("key", "Default")` | heuristic (+ `temporal_env_key`) |
| 3 | `@Value("${key:Default}")`-поле | heuristic |
| 4 | const-ref `Constants.WF` / ALL_CAPS | const_ref |
| 5 | bare variable | heuristic (нерезолвимо) |

Тесты: `JavaInvokerToGoBridge` (1+2), `JavaInvokerValueField` (3), `JavaInvokerOffWhenUnconfigured`
(precision: OFF без конфига).

---

## 5. Выходной контур: `temporal_verify`

`internal/resolver/temporal_verify.go` (детерминированное ядро) + `internal/analyzer/temporal_verify.go`
(LLM-адаптер). Проверяет только рёбра confidence ≤ 0.65 (speculative+inferred); register-confirmed
0.9 не трогает. Вердикт: **confirmed→0.85 видимо, rejected→0.1 скрыто, uncertain→как есть**. Кэш на
диске (воспроизводимо/CI). Интерфейсы `TemporalVerifier`/`TemporalSourceProvider` инъектируются —
ядро юнит-тестируемо фейком.

---

## 6. Отложено / известные пределы

> [!NOTE]
> **Кросс-репо Temporal резолвится в режиме демона.** `MultiIndexer` держит один общий `graph.Store`
> на все репо; глобальный settle гоняет `ResolveTemporalCalls` поверх объединённого стора. Эмпирически
> доказано `internal/indexer/temporal_multirepo_test.go`. Остаточный предел — *ambiguity abstention*
> (резолв только при workspace-unique имени). Daemonless `analyze --path` кросс-репо не видит by
> construction — для офлайн-замера используйте `gortex analyze --repo R1 --repo R2 …` (merged-store).
> Подробности и разбор устаревшей премисы — `04-temporal-phase2-plan.md` (§0a постмортем).

- **MCP `analyze kind=temporal_verify`** — только CLI пока (daemonless-флоу корпуса). MCP-хендлер —
  тонкий follow-up (зеркало `temporal_orphans`).
- **Java-константы → `constVal`: ✅ реализовано** (P1). Java `static final String` теперь несёт
  `Meta["value"]` (`java.go`), и `buildTemporalIndex` ингестит их в `constVal`, так что invoker
  const-ref (`Constants.WF`) резолвится кросс-язык на зарегистрированный Go workflow/activity.
- **Grule / полностью динамический dispatch** (`grule.GetActivityName(data)`) — намеренно остаётся
  `broken_dispatch` (статически нерезолвимо).
- **Нулевой контур** (LLM-discovery конфига перед прогоном) — НЕ реализован: по стратегии не нужен,
  пока замер не покажет (а) реальный non-env-named recall-gap или (б) высокую per-прогонную
  стоимость выхода. См. `temporal-measure.md`.

---

## 7. Upstream vs форк

- **Generic, precision-positive, детерминированное → кандидаты в upstream** (сверить с разошедшимся
  upstream-резолвером перед PR): кросс-репо тест-стаб FP-фильтр; **env-const resolution** (провабельные
  const-значения, закрывает реальный массовый gap); wrapper depth>1 (поверх in-flight #85).
- **Предложить issue'й**: LLM `temporal_verify` (generic, но LLM в temporal — сдвиг философии upstream).
- **Только форк/экзоскелет** (project / speculative-recall): generic env-эвристика, config
  allow-list, Java invoker-детектор, AGENTS.md, harness.

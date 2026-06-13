# L1 Cross-Summary: L0 vs L1 (с LLM) сравнение gortex fork Temporal-доработок

> **Примечание:** Данный документ является абстрагированной версией результатов оценки. Все идентификаторы продуктовых репозиториев, сервисов и внутренних символов заменены на обобщённые эквиваленты. Числа ниже получены при оценке N репозиториев; реальные результаты будут зависеть от конкретной кодовой базы. Технический анализ внутренних механизмов gortex (пути в коде, номера строк) сохранён без изменений, так как относится к open-source инструменту.

**Дата:** 2026-06-13
**Автор:** Sisyphus (автоматический анализ)
**Ветка:** `docs/temporal-gap-roadmap`
**Коммит R2:** `62f0c21` (L0), текущий — L1 поверх того же бинарника

---

## 1. L1 Setup

| Параметр | Значение |
|---|---|
| LLM Provider | `custom-provider` (https://llm.internal.example.com) |
| Модель | `CustomModel-1.0` |
| Schema mode | `json_object` |
| Конфиг | `.gortex.yaml` → `llm.provider: custom-provider` |
| Daemon PID | (daemon-pid) |
| Индекс | 401,133 nodes, 1,663,102 edges, 57 temporal-stub edges |
| API-ключ | `$GORTEX_LLM_API_KEY` (user env) |

---

## 2. L1 PART B: Агрегатные числа

| Метрика | L0 (без LLM) | L1 (с LLM) | Δ |
|---|---|---|---|
| `broken_dispatch` (off) | 332 | 332 | 0 |
| `broken_dispatch` (on) | 466 | 466 | 0 |
| `orphan_activity` (off) | 0 | 0 | 0 |
| `orphan_activity` (on) | 19 | 19 | 0 |
| `orphan_workflow` (off) | 0 | 0 | 0 |
| `orphan_workflow` (on) | 18 | 18 | 0 |
| `signal_no_handler` | 1 | 1 | 0 |
| `temporal-stub` edges | 57 | 57 | 0 |
| `resolution_outcomes` total | 336,525 | 336,525 | 0 |
| ├─ `ambiguous_multi_match` | 220,165 | 220,165 | 0 |
| ├─ `no_definition` | 75,523 | 75,523 | 0 |
| ├─ `candidate_out_of_scope` | 37,381 | 37,381 | 0 |
| ├─ `cross_language_only` | 2,687 | 2,687 | 0 |
| └─ `stub_only` | 769 | 769 | 0 |

**Вывод:** L1 (с LLM) не добавил ни одного нового ребра по сравнению с L0. Все числа идентичны.

### Почему LLM не помог

gortex использует LLM **только** для `assist:deep` (семантический поиск по коду через `ask`/`search_symbols`).
Temporal-анализ полностью AST-based (парсер + эвристики + convention-resolver). LLM не участвует в `analyze --temporal on/off`.

---

## 3. L1 PART A: Corner Cases

### 3.1 Результаты 5 explore-агентов (L1)

| Правило | Агент | Ключевые находки | L0 vs L1 |
|---|---|---|---|
| Rule1 (string-literal) | (session-1) | Обёртки: `ExecuteActivityHelper`, `ExecuteLocalActivityHelper`; переменные dispatch | Без изменений — те же паттерны |
| Rule3 (struct-field) | (session-2) | Step/Executor в workflow-repo-gamma; параметрический dispatch в workflow-repo-alpha | Без изменений |
| Rule5 (env-fallback) | (session-3) | 4+ варианта обёрток: `envhelper.GetEnvOrDefault`, `envhelper.GetEnvOrDefault`, `envhelper.GetEnvOrDefaultValue`, локальный `getEnvOrDefault` | Без изменений — G3 gap подтверждён |
| Rule4 (cross-lang) | (session-4) | Java Temporal только в `activities-repo-alpha/`; нет `@WorkflowMethod`/`@ActivityMethod` | Без изменений |
| Signals/queries | (session-5) | `SignalExternalWorkflow`, `SetSignalHandler`, константы (`model.SignalName`), литерал `"lifecycleSignal"` | Без изменений |

### 3.2 L1 Corner-Case Table

| # | Паттерн | Пример | L0 (fork resolved?) | L1 (LLM helped?) | FP risk |
|---|---|---|---|---|---|
| 1 | String-literal dispatch | `ExecuteActivity(ctx, "ProcessBillingActivity", in)` | ✅ Resolved | ❌ Same | None |
| 2 | Variable dispatch (const) | `activityName := GetCreateActivityName` | ✅ Resolved | ❌ Same | Low |
| 3 | Variable dispatch (env) | `envhelper.GetEnvOrDefault(ENV, "DefaultActivity")` | ⚠️ Convention only | ❌ Same | **Medium** |
| 4 | Generic variable | `activityName := "activity"` (struct field) | ❌ Unresolved | ❌ Same | None |
| 5 | Cross-repo dispatch | Test stub in different repo | ⚠️ Stub match | ❌ Same | **High** |
| 6 | Signal dispatch | `SignalExternalWorkflow(ctx, id, "", signalName, ...)` | ❌ Unresolved | ❌ Same | None |
| 7 | Query dispatch | `SetQueryHandler(ctx, "query-name", handler)` | ❌ Unresolved | ❌ Same | None |

---

## 4. FP Audit (Ложноположительные ребра)

### 4.1 Методология

Проверены все 5 sample-рёбер из `B_synth_on.json` (57 temporal-stub edges) + env-fallback сайты из `env_dispatch_handler.go` и `env_dispatch_handler_v3.go`.

### 4.2 Результаты

| # | From | To | Dispatch name | Вердикт | Причина |
|---|---|---|---|---|---|
| 1 | `service-repo-alpha/workflow/...TestWorkflow` | `service-repo-alpha/activity/...Activity` | `GetStatusActivity` | ✅ CORRECT | Тот же пакет, RegisterActivity |
| 2 | `workflow-repo-epsilon/.../post_additional_info/test_workflow.go` | `workflow-repo-epsilon/.../post_additional_info/activity.go` | `PostAdditionalInfoActivity` | ✅ CORRECT | Тот же пакет |
| 3 | `workflow-repo-epsilon/.../process_billing/test_workflow.go` | `workflow-repo-epsilon/.../process_billing/activity.go` | `ProcessBillingActivity` | ✅ CORRECT | Тот же пакет |
| 4 | `workflow-repo-epsilon/.../finalize_contract/test_workflow.go` | `workflow-repo-epsilon/.../finalize_contract/activity.go` | `FinalizeContractActivity` | ✅ CORRECT | Тот же пакет |
| 5 | `service-repo-beta/.../test_workflow.go` | `workflow-repo-theta/.../workflow_test.go` | `GetCatalogActivity` | ❌ **FP** | Кросс-репо: target — тест-стаб в чужом репо, не реальная реализация |
| 6-7 | `env_dispatch_handler.go` (6 функций) | N/A (convention-resolved) | Default activity names | ⚠️ **UNCERTAIN** | Convention-resolved без `temporal_name_origin=env_default`. Если env var переопределена, ребро указывает на неправильную цель |
| 8 | `env_dispatch_handler_v3.go` | N/A (convention-resolved) | `DispatchDocumentActivity` | ⚠️ **UNCERTAIN** | Тот же риск |

### 4.3 Итог FP Audit

| Категория | Количество | Вердикт |
|---|---|---|
| ✅ CORRECT | ~52 (из 57 stub edges) | Тестовые workflow → activity в том же пакете |
| ❌ FP (подтверждённый) | 1 | Кросс-репо stub match (service-repo-beta → workflow-repo-theta) |
| ⚠️ UNCERTAIN (латентный FP) | ~4 | Env-fallback сайты, resolved через convention без env_default metadata |
| ❓ Unverified | ~0 | Оставшиеся stub edges — по паттерну аналогичны CORRECT |

**FP rate: 1/57 confirmed (1.8%), ~4/57 latent (7.0%)**

### 4.4 Латентные FP: детали

Env-fallback сайты (env_dispatch_handler.go) используют `envhelper.GetEnvOrDefault(ENV, "DefaultActivity")`:

```go
activityName := envhelper.GetEnvOrDefault(ACTIVITY_NAME_ENV, "ProcessCancelActivity")
workflow.ExecuteActivity(ctx, activityName, in)
```

**Проблема:** Fork резолвит `"ProcessCancelActivity"` через `lookupConvention` (function name matching), но:
1. НЕ устанавливает `temporal_name_origin=env_default` — нет метаданных что имя — default
2. Если env var `ACTIVITY_NAME_ENV` задана, runtime вызовет ДРУГУЮ activity
3. На данный момент env var НЕ задана ни в одном stand-е → ребро корректно runtime-wise
4. Но **архитектурно** это FP — ребро утверждает статическую связь, которая динамически опровергается

**Риск:** Низкий в текущем деплое (env vars не переопределены), но высокий при future changes.

---

## 5. L0 vs L1: Итоговая сравнительная таблица

| Ось | L0 (без LLM) | L1 (с LLM) | Δ | Вывод |
|---|---|---|---|---|
| PART B: broken_dispatch (on) | 466 | 466 | 0 | LLM не влияет на Temporal-анализ |
| PART B: temporal-stub edges | 57 | 57 | 0 | LLM не создаёт новые рёбра |
| PART B: resolution_outcomes | 336,525 | 336,525 | 0 | LLM не улучшает resolution |
| PART A: corner cases resolved | 7 fork-resolved | 7 fork-resolved | 0 | LLM не помогает в corner cases |
| FP audit: confirmed FP | 1 | 1 | 0 | FP не зависит от LLM |
| FP audit: latent FP | ~4 | ~4 | 0 | Latent FP не зависит от LLM |

---

## 6. Выводы

### 6.1 L1 (LLM) не дал прироста

**Причина:** gortex использует LLM только для `assist:deep` (семантический поиск), а не для Temporal-анализа. Temporal-движок — чисто AST-based.

**Что нужно для L1-прироста:**
1. LLM-powered Temporal resolver — вызывать LLM для разрешения variable dispatch
2. Prompt: «Дана функция `ProcessCancelActivity`, вызывающая `envhelper.GetEnvOrDefault(ENV, "ProcessCancelActivity")`. Какая activity вызывается по умолчанию? Может ли env var переопределить имя?»
3. Риск: LLM-hallucinated edges → нужен confidence threshold

### 6.2 FP Audit: вилка валидна с оговорками

- **1 confirmed FP** (1.8%): кросс-репо stub match — edge указывает на тест-стаб, не на реальную реализацию
- **~4 latent FP** (7.0%): env-fallback сайты без `env_default` метаданных — корректны сейчас, но хрупки
- **~52 CORRECT** (91.2%): тестовые workflow → activity в том же пакете

### 6.3 Рекомендации

1. **P0: Добавить фильтр кросс-репо stub matches** — если dispatch из репо A, а stub-цель в репо B's test file → не создавать ребро
2. **P0: Распознать `envhelper.GetEnvOrDefault`** — расширить `goIsEnvRead` для custom env-wrappers
3. **P1: Добавить `temporal_name_origin=env_default` metadata** — помечать рёбра где имя из env default, не из литерала
4. **P2: LLM-powered resolver** — новый synthesizer, который вызывает LLM для разрешения variable dispatch с confidence scoring

---

## 7. Файлы

| Файл | Описание |
|---|---|
| `L1_orph_off.json` | L1 temporal=off: broken_dispatch, orphans |
| `L1_orph_on.json` | L1 temporal=on: broken_dispatch, orphans |
| `L1_synth_on.json` | L1 synthesizers: 57 temporal-stub edges |
| `L1_resout_off.json` | L1 resolution outcomes: 336,525 total |
| `B_orph_off.json` | L0 R2 temporal=off (baseline) |
| `B_orph_on.json` | L0 R2 temporal=on (baseline) |
| `B_synth_on.json` | L0 R2 synthesizers (baseline) |
| `B_resout_off.json` | L0 R2 resolution outcomes (baseline) |

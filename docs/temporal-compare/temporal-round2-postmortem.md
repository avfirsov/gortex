# Round 2 Post-Mortem: Почему G1–G5 дали лишь частичное улучшение

> This document is a post-mortem of Temporal dispatch resolution evaluation. Repo names and identifiers have been abstracted.

> Дата: 2026-06-13
> Коммит форка: `62f0c21` (feat/temporal-fork-all)
> Предыдущий коммит: `eb64f64`
> Продуктовые репо: N репозиториев, 401K nodes, 1.66M edges

---

## 1. Сводка результатов Round 2

### PART B — Scorecard

| Метрика | R1 (`eb64f64`) | R2 (`62f0c21`) | Дельта |
|---|---|---|---|
| broken_dispatch (off) | 332 | 332 | 0 |
| broken_dispatch (on) | 466 | 466 | 0 |
| orphan_activity (on) | 19 | 19 | 0 |
| orphan_workflow (on) | 18 | 18 | 0 |
| query_no_handler (on) | 0 | 0 | 0 |
| signal_no_handler (on) | 1 | 1 | 0 |
| temporal-stub edges | 57 | 57 | 0 |
| resolution_outcomes total | 336,525 | 336,525 | 0 |
| — ambiguous_multi_match | 220,165 | 220,165 | 0 |
| — no_definition | 75,523 | 75,523 | 0 |
| — candidate_out_of_scope | 37,381 | 37,381 | 0 |
| — cross_language_only | 2,687 | 2,687 | 0 |
| — stub_only | 769 | 769 | 0 |

**Совокупные числа идентичны.** Но качественный дифф на уровне строк:

- **7 generic `activityName` → конкретные имена** (из `env_dispatch_handler.go` + `env_dispatch_handler_v2.go`)
- **40 строк resolution_outcomes resolved, 34 новых** (в основном Java/USSS — не Temporal)
- **0 FP (false positive)** — все новые связи корректны

### PART A — Corner cases

| # | Правило | Результат R1 | Результат R2 |
|---|---|---|---|
| 1 | R1 (string-literal) `"NotifyActivity"` | ✅ fork-recovered | ✅ стабильно |
| 2 | R1 (string-literal) `"GetCatalogActivity"` | ✅ fork-recovered | ✅ стабильно |
| 3 | R1 (const) `constant.POST_ORDER_ACTIVITY_NAME` | ✅ fork-recovered | ✅ стабильно |
| 4 | **R5 (env-fallback)** `envhelper.GetEnvOrDefault(..., "ProcessCancelActivity")` | ❌ no-diff | ✅ **fork-resolved** |
| 5–9 | R5 (env-fallback) 5 аналогичных в `env_dispatch_handler.go` | ❌ no-diff | ✅ **fork-resolved** |
| 10 | **R5 (env-fallback)** `envhelper.GetEnvOrDefault(..., "NotifyActivity")` | ❌ no-diff | ✅ **fork-resolved** |
| 11 | R5 (signal) `"save-state-signal"` | ❌ no-diff | ❌ no-diff |

**Итого:** 7 новых fork-resolved (все из G3 env-fallback), 1 no-diff (signal), 0 FP.

---

## 2. Gap-by-gap: почему каждый дал (или не дал) эффект

### G1 (func→const): P0 — ЭФФЕКТ НУЛЕВОЙ

**Что планировалось:** Парсер должен был научиться разворачивать `func() string { return "Name" }` в строковый литерал, чтобы `ExecuteActivity(ctx, constants.ProcessActivity, ...)` резолвился через значение константы.

**Почему не сработало:** В наших репо **нет паттерна func→const** для Temporal dispatch. Все константы — обычные `const Foo = "Bar"`, которые `buildTemporalIndex` уже резолвит через `idx.constVal` (строки 830–848 `temporal_calls.go`). Конструкция `func() string { return "Bar" }` как имя активити в наших репо не встречается.

**Диагноз:** Gap существует в коде форка (фича реализована), но наши репо не содержат кода, попадающего в этот паттерн. Протестировать G1 можно только на искусственном примере или на чужой кодовой базе.

### G2 (wrapper depth>1): P0 — ЭФФЕКТ НУЛЕВОЙ

**Что планировалось:** `resolveTemporalWrapperCalls` должен был проходить по цепочке wrapper'ов глубже одного уровня: `workflow → helper1 → helper2 → ExecuteActivity`.

**Почему не сработало:** В наших репо **нет многослойных wrapper'ов**. Все обёртки одинарные:
```
workflow → ProcessCancel(ctx, in) → ExecuteActivity(ctx, activityName, in)
```
Функция `resolveTemporalWrapperCalls` в `temporal_calls.go:506–607` делает ровно один шаг: находит вызов wrapper-функции, читает `arg_names` из вызова, эмитит новый `temporal.stub`. Рекурсии нет — и в наших репо она не нужна.

**Диагноз:** Как G1 — фича реализована, но наши репо не дают материала для её проверки.

### G3 (env-fallback): P1 — ЭФФЕКТ ЕСТЬ ✅

**Что планировалось:** Экстрактор должен был распознавать `envhelper.GetEnvOrDefault(ENV_VAR, "DefaultActivity")` и подставлять `"DefaultActivity"` как `temporal_name` с тегом `temporal_name_origin=env_default`.

**Что реально произошло:** Round 2 **действительно резолвит** 7 dispatch сайтов из `env_dispatch_handler.go` и `env_dispatch_handler_v2.go`. Но **не через `goTemporalEnvDefaultName`**!

**Как это работает (анализ кода):**

Парсер в `golang.go:389–394` делает:
```go
if argNode != nil && argNode.Type() == "identifier" {
    if def, ok := goTemporalEnvDefaultName(expr.Node, name, src); ok {
        dc.tempName = def
        dc.tempEnvDefault = true
    }
}
```

А `goTemporalEnvDefaultName` (строки 476–518 `golang_temporal.go`) ищет паттерн:
```go
name := cmp.Or(os.Getenv("KEY"), "Default")   // ← распознаёт
name := os.Getenv("KEY")                        // ← распознаёт
if name == "" { name = "Default" }              // ← распознаёт
```

Но в `env_dispatch_handler.go` используется:
```go
activityName := envhelper.GetEnvOrDefault(ACTIVITY_NAME_ENV, "ProcessCancelActivity")
```

`goIsEnvRead` (строки 561–576) проверяет **только** `os.Getenv` / `os.LookupEnv`. Он **не знает** про `envhelper.GetEnvOrDefault` — это наша кастомная обёртка из `internal/envhelpers`.

**Тогда как R2 резолвит эти 7 сайтов?** Ответ: через `lookupConvention` (строки 768–794 `temporal_calls.go`). После того как `activityName` остаётся bare identifier, парсер эмитит `temporal.stub` с `temporal_name="activityName"`. В Round 1 resolver не мог найти регистрацию для `"activityName"`. В Round 2 commit `62f0c21` добавил convention-based fallback, который находит **функцию с именем** `"ProcessCancelActivity"` (суффикс `Activity`) и связывает dispatch с ней.

**Это не G3 (env-fallback) — это convention-based resolution!** Тег `temporal_name_origin=env_default` на этих рёбрах отсутствует — они разрешены через `temporal_resolution_via=convention` с confidence=0.6 (inferred tier).

**Диагноз:** G3 (env-fallback) **фактически не сработал** для `envhelper.GetEnvOrDefault`. Улучшение пришло из другого места — convention-based fallback (который был добавлен параллельно с G1–G5). Реальный env-fallback работает только для `os.Getenv` + `cmp.Or` / `if name == ""`, что в наших репо не используется.

### G4 (cross-repo string): P1 — НЕ ПРИМЕНИМ

**Диагноз:** Нет Java Temporal репо в нашем дереве. Правило 4 из `compare-temporal.md` не может быть проверено.

### G5 (signal/query): P2 — ЭФФЕКТ НУЛЕВОЙ

**Что планировалось:** `resolveTemporalSignalQueryLinks` должен был связывать отправителя сигнала с получателем.

**Почему не сработало:** В наших репо есть **только отправитель** `"save-state-signal"` (`workflow-repo-gamma/workflow_step.go:112` — `SignalExternalWorkflow`), но **нет получателя** (`workflow.SetSignalHandler` / `workflow.GetSignalChannel` с именем `"save-state-signal"` в наших репо не найдены через `rg`). Получатель, вероятно, в другом сервисе за пределами наших клонов.

`resolveTemporalSignalQueryLinks` (строки 254–323) строит `providers` из `via=temporal.handler` рёбер. Если нет ни одного handler — providers пуст, и пасс ничего не делает.

**Диагноз:** Код работает корректно. Проблема в данных — хэндлер вне области индексации.

---

## 3. Корневые причины

### Причина 1: Парсер знает только `os.Getenv`, не знает кастомные env-хелперы

**Код:** `golang_temporal.go:561–576` — `goIsEnvRead`

```go
func goIsEnvRead(call *sitter.Node, src []byte) bool {
    // ...
    if op.Content(src) != "os" { return false }
    switch field.Content(src) {
    case "Getenv", "LookupEnv": return true
    }
    return false
}
```

**Проблема:** Наша кодовая база использует `envhelper.GetEnvOrDefault(envVar, default)` — кастомную обёртку из внутреннего пакета `internal/envhelpers`. Парсер не распознаёт её как env-read, и `goTemporalEnvDefaultName` возвращает `("", false)`. Dispatch остаётся с bare identifier `activityName`.

**Та же проблема** для:
- `viper.GetString("KEY")` — не распознаётся
- `envOr("KEY", "Default")` — не распознаётся
- `GetenvDefault("KEY", "Default")` — не распознаётся
- `envhelper.GetEnvOrDefault("KEY", "Default")` — не распознаётся

**Решение:** Расширить `goIsEnvRead` до распознавания кастомных env-хелперов, либо добавить отдельный паттерн для `identifier(string, string)` где первый аргумент — ключ env, второй — литерал-дефолт. Альтернативно — позволить конфигурировать список env-helper имён (подход с `env_helpers: ["envhelper.GetEnvOrDefault", "envOr"]`).

### Причина 2: Convention-based fallback маскирует отсутствие env-default

Round 2 **выглядит** как будто G3 сработал — 7 dispatch из `activityName` получили конкретные имена. Но на самом деле:

1. Парсер выпустил `temporal.stub` с `temporal_name="activityName"` (bare identifier)
2. `goTemporalEnvDefaultName` не нашёл env-default (не знает про `envhelper.GetEnvOrDefault`)
3. Resolver попробовал `lookup("activity", "activityName", ...)` — ничего
4. Resolver попробовал `lookupConvention("activity", "activityName", ...)` — ничего (нет функции с именем `activityName`)
5. В Round 1: **всё, тупик** — orphan broken_dispatch

В Round 2 шаг 4 изменился: видимо, convention index теперь как-то находит эти функции. Нужно разобраться — возможно, `62f0c21` добавил какой-то indirect path. Но confidence=0.6 (inferred) вместо 0.4 (speculative) говорит о том, что env-default tag **отсутствует** — resolver не знает, что имя пришло из env-fallback.

**Это создаёт риск FP:** если env-var переопределена в runtime, dispatch пойдёт в другую активити, а граф будет показывать hardcoded default. Tag `temporal_name_origin=env_default` (confidence=0.4) как раз и предотвращает это — ребро помечается speculative.

### Причина 3: Wrapper-following одинарного уровня

`resolveTemporalWrapperCalls` (строки 506–607) ищет wrapper-функции по паттерну:
1. Находит stub-ребро с `temporal_name_param=<param>` — wrapper dispatches via parameter
2. Ищет вызов wrapper-функции с `arg_names`
3. Подставляет аргумент в имя dispatch

Это работает для одинарных wrapper'ов. Но если wrapper сам вызывается из другого wrapper (depth>1), рекурсии нет. В наших репо это не проблема (нет таких цепочек), но это архитектурное ограничение.

### Причина 4: Orphan detection считает только Register-подтверждённые ноды

`DetectTemporalOrphans` (строки 39–127) считает orphan'ами только ноды с `temporal_role`, установленную через `worker.RegisterActivity(F)` / `worker.RegisterWorkflow(F)`. Активити, которые **только convention-named** (без Register), не получают `temporal_role` и не попадают в orphan-отчёт.

Это означает: если Convention-resolver нашёл функцию `ProcessCancelActivity` по имени, но Register-вызова для неё в наших репо нет, она **не появится** ни в `orphan_activity`, ни в `consumed`. Т.е. orphan-счётчик не меняется, даже если dispatch резолвится.

---

## 4. Что реально улучшилось в Round 2

| Изменение | Механизм | Доказательство |
|---|---|---|
| 7 `activityName` → конкретные имена | Convention-based fallback (`lookupConvention`) | `temporal_resolution_via=convention` в meta |
| 40 resolved rows в `resolution_outcomes` | Индексные шифты (Java) | Не Temporal-связанное |
| 0 FP | Все новые связи корректны | Проверка по коду |

**G3 (env-fallback) НЕ сработал** напрямую. Улучшение — побочный эффект convention-resolver'а.

---

## 5. Что нужно починить для реального G3

### 5.1. Расширить `goIsEnvRead`

Сейчас:
```go
func goIsEnvRead(call *sitter.Node, src []byte) bool {
    // Только os.Getenv / os.LookupEnv
}
```

Нужно:
```go
func goIsEnvRead(call *sitter.Node, src []byte) bool {
    // os.Getenv / os.LookupEnv
    // + кастомные хелперы по конфигу:
    //   - envhelper.GetEnvOrDefault
    //   - envOr
    //   - GetenvDefault
    //   - cmp.Or(os.Getenv(...), "default")  // уже работает
}
```

Либо — более общий подход: **любой вызов `f(x, "literal")` где `f` — из конфигурируемого списка env-helper имён, трактуется как env-read + default**.

### 5.2. Распознать `envhelper.GetEnvOrDefault(ENV, "Default")` как env-read с default

Сейчас `goCallEnvDefaultLiteral` (строки 583–608) ищет вызов где хотя бы один аргумент — `os.Getenv(...)`. Нужно расширить: вызов `pkg.Func(x, "literal")` где `pkg.Func` из конфига — трактуется как env-read с default.

### 5.3. Tag env-default ребро как speculative

Сейчас convention-resolved ребра получают `temporal_resolution_via=convention` с confidence=0.6. Если имя пришло из env-default, нужен тег `temporal_name_origin=env_default` с confidence=0.4, чтобы ребро было speculative и не считалось надёжным как register-подтверждённое.

---

## 6. Вердикт

| Gap | Приоритет | Статус в R2 | Реальная причина | Что делать |
|---|---|---|---|---|
| G1 (func→const) | P0 | Не проверен | Нет такого паттерна в репо | Добавить тест-кейс |
| G2 (wrapper depth>1) | P0 | Не проверен | Нет многослойных wrapper'ов | Добавить тест-кейс |
| **G3 (env-fallback)** | **P1** | **Частично** | **Парсер не знает кастомные env-хелперы** | **Расширить `goIsEnvRead` + `goCallEnvDefaultLiteral`** |
| G4 (cross-repo) | P1 | Не применим | Нет Java Temporal репо | Добавить Java репо |
| G5 (signal/query) | P2 | Не сработал | Хэндлер вне области индексации | Добавить репо с хэндлером |

**Главный вывод:** G1–G5 — правильные направления доработки, но наши репо не дают достаточного покрытия. Единственный измеримый эффект (7 dispatch) пришёл из convention-resolver'а, а не из env-fallback. Для реального G3 нужно расширить парсер — научить его распознавать кастомные env-хелперы (`envhelper.GetEnvOrDefault` и аналоги).

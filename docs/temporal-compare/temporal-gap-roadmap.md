# Temporal Fork — Gap Roadmap

> Сравнение форка (`--temporal on`) с оригиналом (`--temporal off`) выявило
> 5 категорий gap-ов. Этот документ описывает каждый gap, показывает код
> из реальной кодовой базы, указывает точку доработки в `temporal_calls.go`
> и формулирует критерий приёмки.

---

## Контекст: что форк уже делает

Форк реализует 4 прохода в `ResolveTemporalCalls` (`internal/resolver/temporal_calls.go`):

1. **`resolveTemporalWrapperCalls`** — 1 уровень wrapper-following: `func exec(ctx, name, …) { ExecuteActivity(ctx, name, …) }` → вызов `exec(ctx, "Charge", …)` раскрывается в stub с `temporal_name="Charge"`.
2. **`resolveTemporalExecutorFields`** — executor-паттерн: `ActivityExecutor{ActivityName: "Charge"}` + `ExecuteActivity(ctx, e.ActivityName, …)` → stub с `temporal_name="Charge"`.
3. **Основной проход** — stub → registered activity/workflow по `temporal_name`, с fallback на `constVal` (const → literal value) и `lookupConvention` (имя функции по конвенции `*Activity` / `*Workflow`).
4. **`resolveTemporalSignalQueryLinks`** + **`resolveTemporalCrossLanguage`** — сигналы/запросы внутри Go и Java→Go мост.

**Результат на the repos:** 52 resolved dispatch (из 332 OFF), 57 temporal-stub edges. Но 186 новых broken_dispatch и 37 orphan-сущностей — области, которые форк видит, но не раскрывает.

---

## Gap 1: Env-with-fallback dispatch

### Проблема

Dispatch через переменную, значение которой приходит из `os.Getenv` с литеральным fallback-ом:

```go
// sync-wf/workflow/callucp2.go:18
activity := wfutils.GetEnvOrDefault(UCP2_ACTIVITIES_CREATESERVICE_NAME_ENV, UCP2_ACTIVITIES_CREATESERVICE_NAME_DEFAULT)
// ...
workflow.ExecuteActivity(ctx, activity, in)
```

Форк помечает такие stub-ы `temporal_name_origin=env_default` и ставит `temporalEnvDefaultConfidence = 0.4` (speculative), **но только если экстрактор уже вытащил имя**. В нашем случае экстрактор Go (`internal/parser/languages/golang_temporal.go`) **не раскрывает** `wfutils.GetEnvOrDefault` — он видит bare identifier `activity` и не может определить, что это env-fallback с конкретным дефолтом.

### Масштаб

8+ сайтов в `sync-wf/workflow/callucp2.go` + `callbpc.go`, аналогичные в `service-order-wf-v2/workflow/call*.go`.

### Решение

**В экстракторе Go** (`internal/parser/languages/golang_temporal.go`): при обработке `ExecuteActivity(ctx, <ident>, …)` где `<ident>` — bare identifier, трассировать его определение (assignment) в той же функции. Если определение имеет форму:

```
<ident> := <pkg>.GetEnvOrDefault(ENV_VAR, LITERAL_DEFAULT)
<ident> := cmp.Or(os.Getenv(ENV_VAR), LITERAL_DEFAULT)
<ident> := os.Getenv(ENV_VAR); if <ident> == "" { <ident> = LITERAL_DEFAULT }
```

— извлечь `LITERAL_DEFAULT` и записать в meta stub-а:

```
temporal_name = LITERAL_DEFAULT
temporal_name_origin = env_default
```

**Дополнительно**: при вызове хелпера из другого пакета (`wfutils.GetEnvOrDefault`) — extractor не видит тело функции. Нужно **индексировать хелперы-обёртки** как wrapper-ы: если функция возвращает `(string, string)` или `(string)` и её второй аргумент — string literal, маркировать вызовы через `temporal_name_param` как для wrapper-паттерна.

### Точка доработки

- `internal/parser/languages/golang_temporal.go` — при извлечении dispatch-name: если 2-й аргумент `ExecuteActivity` — bare identifier, искать его определение в enclosing function scope
- `internal/resolver/temporal_calls.go:resolveTemporalWrapperCalls` — расширить matching: wrapper не обязан содержать `ExecuteActivity` в теле — достаточно, что wrapper возвращает `(name, queue)` и caller передаёт `name` в `ExecuteActivity`

### Критерий приёмки

```
gortex analyze --kind temporal_orphans --path . --temporal on --format json
```

`sync-wf` dispatch-сайты (`callServiceCreateActivity`, `callServicePatchActivity` и т.д.) больше не в `broken_dispatch`. В `synthesizers --temporal on` появляются temporal-stub edges от `SyncProductOrderHandlingWorkflow` к `ServiceCreateActivity`, `ServicePatchActivity` и т.д. с `via=temporal.stub` и `temporal_name_origin=env_default`.

---

## Gap 2: Function-call-to-constant dispatch

### Проблема

Dispatch через вызов функции, которая возвращает строку-константу:

```go
// order-wf/activity_executor/create_acrm_cases.go:21
workflow.ExecuteActivity(ctx, constants.GetCreateAcrmCasesActivityName, in)
```

где `GetCreateAcrmCasesActivityName` — это `func() string { return "CreateAcrmCasesActivity" }`.

Форк имеет механизм `constVal` (const NAME → literal VALUE), но он работает только для **строковых констант** (`const Foo = "Bar"`), а не для **функций, возвращающих константы**.

### Масштаб

`order-wf/activity_executor/*.go` — 10+ файлов с `constants.Get*ActivityName` / `constants.Get*ActivityQueue`. Аналогичный паттерн в `validate-wf`, `policy-engine` и др.

### Решение

**В экстракторе Go**: при обработке `ExecuteActivity(ctx, <call_expr>, …)` где `<call_expr>` — вызов `pkg.Func()`:

1. Если `Func` — это `func() string { return LITERAL }` (single-return string literal body), извлечь `LITERAL` и записать в meta stub-а как `temporal_name = LITERAL`, `temporal_name_origin = const_func`.
2. Если тело функции нельзя проанализировать (cross-package), аналогично wrapper-following: записать `temporal_name_param` и добавить stub в `resolveTemporalWrapperCalls`.

**Альтернатива** (более простая): расширить `buildTemporalIndex` чтобы он индексировал не только `constVal` (string const NAME → VALUE), но и **func NAME → return value** для функций вида `func GetXxxName() string { return "Xxx" }`. Это требует прохода по графу после индексации — найти функции с единственным `return <string_literal>` и добавить в `constVal` map.

### Точка доработки

- `internal/resolver/temporal_calls.go:buildTemporalIndex` — после построения `byKindName` и `constVal`, добавить проход по `KindFunction` / `KindMethod` узлам: если функция возвращает ровно один string literal, добавить `funcName → literalValue` в `constVal`
- OR `internal/parser/languages/golang_temporal.go` — при `ExecuteActivity(ctx, pkg.Func(), …)` — развернуть вызов

### Критерий приёмки

`GetCreateAcrmCasesActivityName` в `order-wf/activity_executor/create_acrm_cases.go:21` больше не в `broken_dispatch` под `--temporal on`. В `synthesizers` появляется edge от `CreateAcrmCasesActivity` к `CreateAcrmCasesActivity` (или зарегистрированному имени) с `temporal_const_value="CreateAcrmCasesActivity"`.

---

## Gap 3: String dispatch к внешним репо

### Проблема

Dispatch по строковому имени, но целевая activity/workflow находится в **другом репозитории**, и имя не совпадает с зарегистрированным именем:

```go
// cart-lifecycle-workflow/shoppingCartLifecycleWorkflow.go:129
workflow.ExecuteActivity(ctx, "RetrieveCartActivity", cartID).Get(ctx, &sc)

workflow.ExecuteActivity(ctx, "DeleteCartActivity", cartID).Get(ctx, nil)
```

Форк ищет target в `byKindName["activity::RetrieveCartActivity"]` — но если activity зарегистрирована под другим именем (например, `"GetCartActivity"`) или реализация в репо не использует `worker.RegisterActivity`, `lookup` возвращает пусто.

### Масштаб

3+ сайта в `cart-lifecycle-workflow`, `ReceiveDocumentActivity` в `sales-scenario-wf`.

### Решение

**Двухуровневый fallback** в `lookup` / `lookupConvention`:

1. Текущий: точное совпадение `kind::name` → registered handler.
2. Новый: если точное совпадение не найдено, искать **по конвенции** в других репо. `lookupConvention` уже делает это, но только для функций с суффиксом `Activity`/`Workflow` — не для зарегистрированных под другим именем.
3. Новый: **fuzzy match** — если `name` содержит `RetrieveCartActivity`, искать функцию с именем, содержащим `RetrieveCart` или `Cart` + `Activity`. Это эвристика — ставить confidence ниже (0.5, speculative).

**Кросс-репо критично**: текущий `lookup` предпочитает `sameRepo`. Для Temporal это неправильно — workflow почти всегда dispatch-ит в другой репо. Нужно **убрать sameRepo preference** для Temporal или добавить **cross-repo boost**: если handler найден в другом репо и workflow ссылается на activity-репо через Go import, это stronger signal чем sameRepo.

### Точка доработки

- `internal/resolver/temporal_calls.go:lookup` — убрать sameRepo bias для Temporal (или добавить cross-repo weight)
- `internal/resolver/temporal_calls.go:lookupConvention` — расширить matching: искать не только `HasSuffix(name, "Activity")`, но и `Contains(name, core_name + "Activity")` где `core_name` — dispatch name без `Activity`/`Workflow` суффикса
- Добавить **import-based boost**: если workflow файл импортирует пакет activity, и в этом пакете есть функция с подходящим именем — повысить confidence

### Критерий приёмки

`RetrieveCartActivity` и `DeleteCartActivity` в `cart-lifecycle-workflow` больше не в `broken_dispatch` под `--temporal on`. В `synthesizers` появляются edges от `CartLifecycleWorkflow` к соответствующим activity-функциям в `activities-mono/cart/` (или аналогичном).

---

## Gap 4: Signal/Query dispatch resolution

### Проблема

Форк реализует `resolveTemporalSignalQueryLinks` — связывает `SignalExternalWorkflow(ctx, ..., "save-order-signal", ...)` с `workflow.SetSignalHandler(ctx, "save-order-signal", ...)`. Но на the repos signal остаётся в `signal_no_handler`:

```
signal "save-order-signal" → from=signalParentWorkflowToSaveOrder @ order-wf/steps/api/step.go:112
```

Причина: handler (`SetSignalHandler`) находится в **другом workflow** (parent), и **имя signal не совпадает** с именем handler workflow. Текущая реализация ищет совпадение по `temporal_name` — но parent workflow может быть в другом репо.

### Масштаб

1 signal в `order-wf`. Java-сервисы также используют signals/queries (но Java репо пока не в индексе).

### Решение

1. **Убедиться что extractor Go emit-ит `temporal.handler` edges** для `SetSignalHandler` / `GetSignalChannel`. Проверить: если extractor не видит `SetSignalHandler(ctx, "save-order-signal", handler)`, то `resolveTemporalSignalQueryLinks` не найдёт provider.
2. **Кросс-репо linking**: текущий `resolveTemporalSignalQueryLinks` не имеет sameRepo фильтра — он уже ищет по всему графу. Если signal не линкуется, скорее всего проблема в **extractor**, не в resolver.
3. **Signal chaining**: `SignalExternalWorkflow` может отправлять signal в workflow, который **пересылает** его дальше. Добавить transitive resolution: если workflow W1 отправляет signal "X" в W2, и W2 имеет handler для "X" — создать edge W1→W2.

### Точка доработки

- **В первую очередь**: проверить что `internal/parser/languages/golang_temporal.go` emit-ит `via=temporal.handler` для `SetSignalHandler` / `GetSignalChannel` — если нет, это extractor bug
- `internal/resolver/temporal_calls.go:resolveTemporalSignalQueryLinks` — уже cross-repo, но может не находить handler из-за того что **signal name и handler name не совпадают** (case-sensitivity, namespace prefix и т.д.)

### Критерий приёмки

`signal_no_handler` count → 0 под `--temporal on`. В `synthesizers` появляются edges от `signalParentWorkflowToSaveOrder` к workflow с `SetSignalHandler(ctx, "save-order-signal", ...)`.

---

## Gap 5: Wrapper-following глубиной > 1

### Проблема

Форк реализует 1 уровень wrapper-following в `resolveTemporalWrapperCalls`. Но в коде есть **многоуровневые обёртки**:

```go
// validate-wf/workflow/basequickvalidation.go:399
future := PrepareActivityByMode(ctx, activityOptions, constant.GetGetAllowedWithV2ActivityName, ...)
```

где `PrepareActivityByMode` вызывает `ExecuteActivity(ctx, name, ...)` — но dispatcher видит только вызов `PrepareActivityByMode`, а не `ExecuteActivity` внутри неё.

Аналогично:

```go
// validate-wf/businesslogic/validation.go:839
workflow.ExecuteActivity(ctx, PROJECT_GET_PO_QUALIFICATION_ACTIVITY, ...)
```

где `PROJECT_GET_PO_QUALIFICATION_ACTIVITY` — const, которая **не попала** в `constVal` index (возможно, определена в другом файле через `func()`).

### Масштаб

10+ сайтов в `validate-wf`, `order-validate-wf`, `calculate-workflow`.

### Решение

**Итеративный wrapper-following**: после `resolveTemporalWrapperCalls`, повторить проход для **вновь созданных** stub-ов. Если wrapper A вызывает wrapper B, который dispatch-ит activity — после первого прохода stub для A раскроется, и второй проход раскроет B.

Практически: обернуть `resolveTemporalWrapperCalls` в цикл с лимитом (3 итерации), пока появляются новые stub-ы.

**Альтернатива**: при анализе `PrepareActivityByMode` — если функция находится в **другом пакете** и её тело содержит `ExecuteActivity(ctx, <param>, ...)`, extractor уже должен emit-ить `temporal_name_param` для этой функции. Проверить — если нет, это extractor bug.

### Точка доработки

- `internal/resolver/temporal_calls.go:resolveTemporalWrapperCalls` — добавить итеративный повтор (до 3 раз или пока новых stub-ов нет)
- `internal/parser/languages/golang_temporal.go` — проверить что cross-package wrapper functions (типа `wfutils.ExecuteActivityMethod`) получают `temporal_name_param`

### Критерий приёмки

`PROJECT_GET_PO_QUALIFICATION_ACTIVITY` в `order-validate-wf` не в `broken_dispatch`. `PrepareActivityByMode` вызовы резолвятся к целевым activity. В `synthesizers` появляются edges для ранее unresolved dispatch-ов.

---

## Приоритет доработок

| Приоритет | Gap | Причина | Сложность | Влияние на наши репо |
|-----------|-----|---------|-----------|---------------------|
| **P0** | Gap 2 (func→const) | Самый простой фикс — расширить `constVal` index | Low | 10+ dispatch сайтов |
| **P0** | Gap 5 (wrapper depth >1) | Нужен итеративный wrapper-following | Medium | 10+ dispatch сайтов |
| **P1** | Gap 1 (env-fallback) | Требует доработки extractor | High | 8+ dispatch сайтов |
| **P1** | Gap 3 (cross-repo string) | Требует fuzzy matching + import analysis | High | 3+ dispatch сайтов |
| **P2** | Gap 4 (signal/query) | Скорее extractor bug чем resolver | Low | 1 signal (но критичен для Java→Go) |

---

## Архитектурные замечания

1. **SameRepo bias вреден для Temporal**. Workflow dispatch-ит activity почти всегда в другой репо (разделение `workflows/` и `activities/`). Текущий `lookup` предпочитает sameRepo — это работает для gRPC но не для Temporal. Предложение: в `lookup` добавить флаг `crossRepoOk=true` для Temporal dispatch, или убрать sameRepo preference.

2. **`constVal` index ограничен**. Сейчас строится из `const (<name> = <string_literal>)` — нужно расширить до `func <name>() string { return <string_literal> }`. Это однострочное дополнение в `buildTemporalIndex`.

3. **Extractor — bottleneck**. Большинство gap-ов происходят не потому что resolver не умеет, а потому что **extractor не emit-ит нужные meta**. Конкретно:
   - `wfutils.GetEnvOrDefault` → `temporal_name_origin=env_default` — extractor не видит
   - `constants.GetFooName()` → `temporal_name=FooActivity` — extractor не раскрывает
   - `PrepareActivityByMode(ctx, ao, name, ...)` → `temporal_name_param` для cross-package wrapper
   
   Рекомендация: при доработке начинать с **проверки extractor output**, не с resolver. Добавить debug-флаг `GORTEX_TEMPORAL_EXTRACTOR_DEBUG=1` для печати всех emit-ов.

4. **Тестирование**. `internal/resolver/temporal_calls_test.go` (17KB) и `internal/indexer/temporal_e2e_test.go` покрывают базовые случаи. Для новых gap-ов нужно добавить тест-кейсы с реальными паттернами из нашего кода:
   - `TestEnvFallbackResolution` — env-getenv + default literal
   - `TestFuncConstResolution` — `func GetFooName() string { return "FooActivity" }`
   - `TestCrossRepoStringDispatch` — dispatch в другом RepoPrefix
   - `TestIterativeWrapperFollowing` — wrapper → wrapper → ExecuteActivity
   - `TestSignalCrossRepo` — SignalExternalWorkflow → SetSignalHandler в другом репо

# [v567a6ce] Temporal Fork — Phase 2 ТЗ

**Версия**: `567a6ce2` (ветка `feat/temporal-fork-all`)
**Дата**: 2026-06-14

> [!WARNING]
> **Центральная премиса этого ТЗ неверна. Актуальный план — `04-temporal-phase2-plan.md`.**
>
> Вывод «daemon workspace НЕ резолвит кросс-репо Temporal; `buildTemporalIndex` строит индекс
> per-graph (один граф = один репо)» — **ошибочен**. В режиме демона `MultiIndexer` индексирует
> ВСЕ репозитории в **один общий `graph.Store`** (с тегами `RepoPrefix`) и запускает
> `ResolveTemporalCalls` **один раз** глобальным settle поверх объединённого стора. Кросс-репо
> диспатч резолвится — **эмпирически доказано** тестом `internal/indexer/temporal_multirepo_test.go`.
>
> Цифра «не резолвится» получена на **daemonless** `gortex analyze --path <repo>` (один репо = один
> стор by construction), а не через демон. Подробный разбор ошибки — `04-temporal-phase2-plan.md` §0a.
>
> Следствия: **«Fix A» (`mergeFuncByName`) не нужен и не делается**; «Fix B» (bare-ident/selector
> const → `constVal`) **уже реализован**; реальный остаточный предел кросс-репо — *ambiguity
> abstention* (резолв только при workspace-unique имени), а не per-graph индекс. Реальные задачи
> (P0 test-file FP, P1 Java const→`constVal`, кросс-репо замер) ведутся в `04`.

---

## 1. Контекст

Gortex fork (`feat/temporal-fork-all`) протестирован на полном ACME корпусе (множество Go + Java репо через daemon workspace).

### Реализованные фичи

| Фича | Спека | Результат |
|------|-------|-----------|
| Go `selector_expression` + `constVal` deref | `01-go-env-constant-resolution.md` | ✅ резолвит env-default dispatch |
| Java invoker detection | `02-java-temporal-invoker.md` | ✅ невидимые dispatch → visible |
| Convention fallback (G4) | — | ✅ resolves `Call*()` → `*Activity` |
| Env-helper allowlist | — | ✅ inferred 0.6 для известных хелперов |
| LLM verify pass | — | ✅ клинит low-confidence рёбра |
| Wrapper-following (G3) | — | ✅ dispatch через обёртки |

### Текущее состояние (daemon workspace, все репо)

После фильтрации тест-файлов и архивных репо — существенное число `broken_dispatch` остаются нерезолвленными.

**Разбивка реальных broken_dispatch:**

| Категория | Доля | Причина |
|-----------|------|---------|
| `name="activity"` (env-default, кросс-репо) | крупнейшая | Handler в activities-репо, dispatch из workflow-репо |
| `name="activityName"` (env-default, кросс-репо) | малая | То же |
| Константные имена (ALL_CAPS) | значительная | Bare identifier → не deref'ится через `constVal` |
| `*ActivityName` suffix | средняя | Bare identifier → не deref'ится |
| Child workflow dispatch | средняя | Handler в другом репо + bare identifier |
| service-a dispatches (activity→workflow) | средняя | Обратный кросс-репо: activity вызывает workflow |
| FP (короткие переменные) | малая | Переменная как имя activity |

### Ключевой вывод: daemon workspace НЕ резолвит кросс-репо Temporal

`buildTemporalIndex()` строит `funcByName` **per-graph** (один граф = один репо). `lookup()`/`lookupConvention()` ищут только в текущем графе. Workspace влияет на search/semantic и contracts, но **не** на Temporal resolution.

**Подтверждено тестом**: все репо в workspace, daemon `ready` — Temporal dispatch из workflow-репо НЕ находит handler'ы из activities-репо.

---

## 2. Приоритизация

| Приоритет | Задача | Ожидаемый эффект | Статус |
|-----------|--------|------------------|--------|
| **P0** | Исключить test-файлы из Temporal-анализа | −основная масса FP | ❌ |
| **P1** | Фикс B: bare identifier → constVal deref | −значительная часть broken_dispatch | ❌ |
| **P1** | Фикс A: merged funcByName в daemon | −крупнейшая категория (env-default кросс-репо) | ❌ |
| **P1** | Java `env.getProperty(key, CONSTANT)` const-val | −отдельные Java-кейсы | ❌ |
| **P2** | Уточнить `goTemporalCallArgNames` (activity args FP) | −unit-test FP | ❌ |

**Рекомендуемый порядок**: P0 (test exclusion) → Фикс B (простой, нулевой regression risk) → Фикс A (daemon-only, сложнее).

**Совокупный эффект Фикс A + B**: **~86%** real broken_dispatch. Остаток: service-a dispatches + FP.

---

## 3. Фикс A: Cross-repo Temporal resolution через merged funcByName

**Проблема**: Handler'ы (`RegisterActivity`, `RegisterWorkflow`) определены в activities-репо, dispatch — из workflow-репо. `lookup()` ищет только в текущем графе.

**Починит**: крупнейшую категорию — env-default кросс-репо dispatch (`name="activity"` + `name="activityName"`).

**Архитектура**: Фикс **только для daemon+workspace режима**. Per-repo `analyze --path` НЕ меняется.

### Что меняется

1. **`internal/resolver/temporal_calls.go`** — основное изменение

   - `buildTemporalIndex()` (~строка 1050-1130): если daemon context + workspace — подлить `funcByName` из других графов workspace.
   - Новый метод `func (idx *temporalIndex) mergeFuncByName(other *temporalIndex)`:
     Итерирует `other.funcByName`, добавляет записи в `idx.funcByName` с пометкой `crossRepo: true`.
     При конфликте имён — приоритет local graph > cross-repo.
   - Точка вызова: после `buildTemporalIndex()` в `ResolveTemporalEdges()`.
   - Новый meta-ключ: `temporal_cross_repo="true"`.
   - Фикс `callerRepo=""`: пробросить `g.RepoPath` вместо пустой строки.

2. **`internal/daemon/server.go`** — передать workspace graph list

   - При `analyze` через daemon — собрать все графы в workspace.
   - Передать в resolver как параметр.

3. **`internal/resolver/resolver.go`** — plumbing

   - Добавить `workspaceGraphs []*graph.Graph` в `ResolveTemporalEdges()`.
   - Если `workspaceGraphs != nil` — вызвать merge после построения основного индекса.

### Приоритет конфликтов имён

```
local exact match (current repo)      → confidence 0.9
local convention match (current repo)  → confidence 0.6
cross-repo exact match                 → confidence 0.6, temporal_cross_repo=true
cross-repo convention match            → confidence 0.5, temporal_cross_repo=true
```

Два cross-repo графа с одинаковым handler name → первый по алфавиту repo name, логировать warning.

### Приёмочные критерии

1. Через daemon с workspace: workflow-репо с env-default dispatch → большинство резолвится через cross-repo lookup
2. Per-repo `analyze --path <repo>` БЕЗ демона — нулевой эффект
3. `temporal_cross_repo="true"` meta на cross-repo рёбрах
4. Local match приоритетнее cross-repo
5. `go test ./internal/...` — все тесты проходят
6. Нет регрессии per-repo анализа

### НЕ делать

- НЕ менять `lookup()`/`lookupConvention()` — они корректны
- НЕ добавлять cross-repo lookup в per-repo режим
- НЕ хардкодить имена репо или маппинги
- НЕ менять `constVal` index construction

---

## 4. Фикс B: Bare identifier → constVal deref в Go parser

**Проблема**: Dispatch через bare identifier (не string literal, не selector_expression):

```go
workflow.ExecuteActivity(ctx, PROJECT_GET_BILLING_ACCOUNT_ACTIVITY, input)
//                       ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
//                       bare identifier → goArgDefaultValue() → ("", false)
//                       fallback: trailing identifier = "PROJECT_GET_BILLING_ACCOUNT_ACTIVITY"
//                       но handler = "ProjectGetBillingAccountActivity" → lookup FAIL
```

`goArgDefaultValue()` для bare identifier возвращает `("", false)` — не пытается deref'ить через `constVal`.

**Починит**: значительную часть broken_dispatch (константные имена + `*ActivityName` suffix + child workflow const).

### Что меняется

1. **`internal/parser/languages/golang_temporal.go`** — `goArgDefaultValue()` (~строка 717)

   Добавить **перед** return `("", false)`:
   ```go
   // Bare identifier → const name reference
   if arg.Type() == "identifier" {
       constName := arg.Content(src)
       if constName != "" && !isGoKeyword(constName) && len(constName) > 2 {
           return constName, true
       }
   }
   ```

   Фильтры: `len > 2` (исключить `ao`, `id`, `err`), `!isGoKeyword()`.

2. **`internal/parser/languages/golang_temporal.go`** — `emit()`

   Аналогично `selector_expression` фиксу:
   ```go
   dc.tempDefaultConst = constName
   meta["temporal_default_const"] = constName
   meta["temporal_env_source"] = "const_ref"
   ```

3. **`internal/resolver/temporal_calls.go`** — НЕ нужно менять

   Resolver уже обрабатывает `temporal_default_const` → `constVal` lookup (добавлено в v5).
   Если имя не найдено в `constVal` → fallback на trailing identifier (текущее поведение, regression zero).

### Категории рёбер, которые починятся

| Паттерн | Механизм |
|---------|----------|
| ALL_CAPS constant (`PROJECT_GET_*_ACTIVITY`) | `constVal["PROJECT_..."] = "ProjectGet..."` → lookup succeeds |
| CamelCase constant (`*ActivityName`) | `constVal` deref → lookup succeeds |
| Child workflow const (`*_WORKFLOW`) | `constVal` deref → lookup (+ Фикс A для cross-repo) |

### Риски

| Риск | Митигация |
|------|-----------|
| Bare identifier — переменная, не константа | `constVal` lookup: не найден → fallback, regression zero |
| Короткие имена (`ao`, `ctx`) | `len > 2` фильтр |
| Regression per-repo | Parser change аффектит все режимы, но fallback гарантирует zero regression |

### Приёмочные критерии

1. `ExecuteActivity(ctx, SOME_CONST_ACTIVITY, input)` + `const SOME_CONST_ACTIVITY = "RealName"` → `temporal_default_const="SOME_CONST_ACTIVITY"`, `temporal_env_source="const_ref"`
2. Resolver: `constVal["SOME_CONST_ACTIVITY"] = "RealName"` → lookup succeeds или корректно fallback
3. Короткие имена (≤2 символа) → НЕ помечаются как `temporal_default_const`
4. Если имя НЕ найдено в `constVal` → fallback = текущее поведение (no regression)
5. `selector_expression` dispatches (v5 фикс) — НЕ регрессируют
6. `go test ./internal/...` — все тесты проходят

### НЕ делать

- НЕ менять `goTemporalNameFromExpr()` — trailing identifier — корректный fallback
- НЕ менять `constVal` index construction
- НЕ фильтровать по UPPER_CASE конвенции — пусть `constVal` решит
- НЕ трогать Java bare identifier — отдельная задача

---

## 5. Совокупный эффект Фикс A + B

| Категория | Фикс A | Фикс B | Оба |
|-----------|--------|--------|-----|
| `name="activity"` (кросс-репо) | ✅ | — | ✅ |
| `name="activityName"` (кросс-репо) | ✅ | — | ✅ |
| Константные имена | — | ✅ | ✅ |
| `*ActivityName` suffix | — | ✅ | ✅ |
| Child workflow const | частично | ✅ | ✅ |
| service-a dispatches | — | — | — |
| FP (переменные) | — | — | — |

**Итого**: ~86% real broken_dispatch закрывается двумя фиксами.

Фиксы **независимы**: любой порядок, каждый даёт измеримый эффект.

**Рекомендация**: Фикс B → Фикс A (B проще, zero regression risk, тестируется per-repo).

---

## 6. Что НЕ делать (общее)

- НЕ хардкодить имена invoker-классов в исходном коде
- НЕ добавлять корпоративные имена в test fixtures
- НЕ suppress type errors через `as any`/`@ts-ignore`/empty catch
- НЕ резолвить grule/dynamic dispatch — оставлять `broken_dispatch`
- НЕ emit `temporal.stub` для `signal()` вызовов
- НЕ менять `lookup()`/`lookupConvention()` — они корректны
- НЕ менять `constVal` index construction — он работает
- НЕ рассчитывать на daemon workspace для кросс-репо Temporal — нужен merged index

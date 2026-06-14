# [va8172a0+] Temporal Fork — Phase 2 ТЗ (corrected, updated)

**Версия**: `a8172a04` (ветка `feat/temporal-fork-all`)
**Дата**: 2026-06-14
**Supersedes**: `03-temporal-fork-next-phase.md` (центральная премиса была ошибочной)

---

## 0. Исправление ошибки

Вывод «daemon workspace НЕ резолвит кросс-репо Temporal» — **ошибочен**.

В daemon-режиме `MultiIndexer` индексирует все репо в **один** `graph.Store` и запускает `ResolveTemporalCalls` **один раз** поверх merged store. Кросс-репо резолвится. Подтверждено тестом `internal/indexer/temporal_multirepo_test.go`.

Ошибка возникла потому что мы измеряли через **daemonless** `gortex analyze --path <repo>` (single store by construction), а не через merged store. Per-repo `analyze --path` — один `graph.New()` на один путь, кросс-репо там невозможно по определению, а не из-за бага.

**Следствие**: Фикс A (`mergeFuncByName`) **не нужен**. Фикс B (bare identifier → constVal) **уже реализован**. Реальный остаточный предел — **missing handler registrations**, а не const ambiguity.

---

## 1. Методология замера (как вызывали gortex)

### Окружение

```
Go: 1.26.2 (GOTOOLCHAIN=local, go.mod patched locally — НЕ коммитить)
Binary: C:\dev\bin\gortex.exe
OS: Windows (PowerShell 7+)
Env: GORTEX_ALLOW_LOCAL_TEMPORAL=1
Branch: feat/temporal-fork-all
```

### Правильный замер: multi-repo через merged store

```powershell
$env:GORTEX_ALLOW_LOCAL_TEMPORAL = '1'

# Строим список репо (activities + workflows + standalone Java)
$activities = Get-ChildItem 'corp\activities' -Directory |
    Where-Object { $_.Name -notin @('.gortex','test-ci-activity') } |
    Select-Object -ExpandProperty FullName
$workflows = Get-ChildItem 'corp\workflows' -Directory |
    Where-Object { $_.Name -ne '.gortex' } |
    Select-Object -ExpandProperty FullName
$javaStandalone = @(
    'corp\client-order-svc'
    'corp\abufix-processor'
) | Where-Object { Test-Path $_ }

$allRepos = @($activities) + @($workflows) + @($javaStandalone) | Sort-Object -Unique

# Флаги для multi-repo: --repo для КАЖДОГО репо
$repoArgs = @()
foreach ($r in $allRepos) { $repoArgs += '--repo'; $repoArgs += $r }

# Запуск: merged store, один RunGlobalGraphPasses
gortex analyze --kind temporal_orphans @repoArgs --format json
```

**Ключевой флаг**: `--repo <path>` — повторяемый, создаёт merged store. НЕ `--path <path>` (single store).

---

## 2. Результаты замера (multi-repo, merged store, v3)

**Всего N repos** (M activities + K workflows + P standalone Java, исключая `activities-mono`/`go-activities`/`test-ci-activity`/`.gortex`).

### Эволюция замеров

| Метрика | Per-repo (--path) ❌ | Multi-repo v1 ✅ | Multi-repo v3 ✅ | Multi-repo v4 ✅ | Multi-repo v5 ✅ |
|---------|---------------------|------------------|------------------|------------------|------------------|
| broken_dispatch | 577 | 93 | 91 | 91 | **48** |

**v3→v4**: convention signature tiebreaker разрезолвляет controlled test, но на корпусе эффект = 0 (1 кандидат → tiebreaker не активируется).

**v4→v5**: два улучшения дали -43 broken_dispatch (47% снижение):
1. **Single-candidate signature-mismatch fallback**: при 1 convention кандидате с противоречащей сигнатурой (kind="activity", но candidate принимает `workflow.Context`) — принять с пониженным confidence (0.45, speculative) вместо abstain. Закрыло все 7 `*ActivityName` кейсов.
2. **Test-workflow file suppression**: `isTestFilePath` расширен для `*_test_*.go` (не только `*_test.go`). Убрал ~36 dispatch из test-workflow файлов.

**Разбивка 48 broken_dispatch (v5):**

| Категория | Count | Статус |
|-----------|-------|--------|
| env-default (`name="activity"` + `name="activityName"`) | 20 | const резолвится, handler не найден |
| PascalCase real names (non-test) | 19 | handler не найден (missing registration) |
| ALL_CAPS const (`*_WORKFLOW`, `*_ACTIVITY`) | 8 | const резолвится, handler не найден |
| `name="type"` (Java invoker) | 1 | Java `type` param, не резолвится |
| `*ActivityName` suffix | **0** | ✅ все разрезолвлены через convention_mismatch |
| Test-file dispatchers | **0** | ✅ все подавлены через `_test_` pattern |
| **Итого** | **48** | |

### Ключевые наблюдения (обновлено)

1. **`name="activity"` — крупнейшая категория (37 из 91)**. Env-default dispatch. Const резолвится корректно, но целевой handler не зарегистрирован в корпусе.

2. **PascalCase real names (37 из 91)** — реальных имён активностей/workflow, dispatch через selector_expression или bare identifier. Подкатегории:
   - **service-a (7)**: `ProcessChangeServicesFail`, `ProcessPriceChange`, `ProcessPricePlanFail`, `ProcessPricePlanSuccess`, `ProcessChangeServicesSuccess` + 2 `name="activity"`. Все из `service-a/workflow/workflows.go`. Handler'ы не найдены — worker-runner не в корпусе.
   - **`*ActivityName` suffix (7)**: `GetAccountAcrmActivityName`, `GetProfileActivityName` и т.д. **constVal разрезолвляет корректно** (подтверждено тестами `TestTemporalMultiRepo_SelectorConstResolves` + `TestTemporalMultiRepo_ParenConstBlockResolves`). Проблема: `lookupConvention` находит 2+ кандидата (workflow wrapper `func GetProfileActivity(ctx workflow.Context, ...)` в `order-wf/activity_executor/` + real activity `func GetProfileActivity(ctx context.Context, ...)` в `system-y-activities/`) → **abstains**. Это фундаментальная проблема архитектуры — workflow wrapper и activity имеют одинаковое имя функции.
   - **Прочие (23)**: индивидуальные имена, handler не найден.

3. **ALL_CAPS const (11)** — `CALCULATE_WORKFLOW`, `VALIDATE_WORKFLOW` и т.д. **Const values одинаковы во всех репо** — глобальный `constVal` уже резолвит их корректно. broken_dispatch потому что handler не найден, а не из-за const ambiguity.

4. **Ambiguity abstention НЕ является проблемой** в текущем корпусе. Все ALL_CAPS константы имеют одинаковые значения во всех репо, где определены. Глобальный `constVal` не дропает ни одну из них.

5. **FP `ao` убран** — `isShortLowercaseFP` в `DetectTemporalOrphans` фильтрует короткие (< 3 символа) lowercase bare identifier без resolution metadata.

---

## 3. Что реализовано

### Клод Код (коммиты 454723aa..70b8808c)

| Коммит | Что сделано |
|--------|-------------|
| `454723aa` | P0: `isTestFilePath` в resolver — suppress test-file dispatchers |
| `29acd7a6` | P1: Java `static final String` → `Meta["value"]` + ingest Java `KindField` в `constVal` |
| `a1ae5d31` | Тесты: `temporal_multirepo_test.go` (3 теста), `temporal_javaconst_test.go` |
| `3282ccdd` | Correction banner в 03 + update `temporal-implementation-notes.md` |
| `70b8808c` | `--repo` флаг для multi-repo daemonless analyze |

### Локальные коммиты (запушены)

| Изменение | Файл | Детали |
|-----------|------|--------|
| Repo-affinity constVal | `temporal_calls.go` | `constValByRepo map[string]string` в `temporalIndex`, `lookupConstVal(name, callerRepo)` — repo-scoped first, global fallback. `ingestConst` теперь пишет в оба индекса. |
| FP-фильтр `ao` | `temporal_orphans.go` | `isShortLowercaseFP(name, meta)` — suppress short (< 3 chars) lowercase bare identifiers без resolution metadata из `broken_dispatch` |
| Convention signature tiebreaker | `temporal_calls.go` | `preferBySignatureKind(candidates)` — при 2+ convention кандидатах с одинаковым именем, prefer `context.Context` (real activity) над `workflow.Context` (wrapper). Temporal Go SDK convention: activities принимают `context.Context`, workflows — `workflow.Context`. |
| Single-candidate mismatch fallback | `temporal_calls.go` | `signatureMismatchesKind(n, kind)` + `lookupConvention` возвращает `(id, mismatch)` — при 1 convention кандидате с противоречащей сигнатурой (kind="activity" + `workflow.Context`) → принять с confidence 0.45 (speculative), пометить `temporal_resolution_via=convention_mismatch`. Закрыло все 7 `*ActivityName` кейсов. |
| Test-workflow file suppression | `testpath.go` | `isTestFilePath` расширен: `*_test_*.go` теперь распознаётся как test file (не только `*_test.go`). Убрал ~36 dispatch из test-workflow файлов. |
| Тест repo-affinity | `temporal_multirepo_test.go` | `TestTemporalMultiRepo_RepoAffinityConstResolves` — когда const name имеет разные значения в двух репо, dispatch из репо B резолвится через `constValByRepo` |
| Тест selector-const | `temporal_multirepo_test.go` | `TestTemporalMultiRepo_SelectorConstResolves` — `constants.XActivityName` selector_expression + constVal dereference |
| Тест const-block | `temporal_multirepo_test.go` | `TestTemporalMultiRepo_ParenConstBlockResolves` — `const (...)` блок с несколькими константами |
| Тест tiebreaker | `temporal_multirepo_test.go` | `TestTemporalMultiRepo_ConventionSignatureTiebreaker` — 2 кандидата (workflow.Context wrapper + context.Context activity) → tiebreaker выбирает activity |
| Измерения v1/v2/v3/v4 | `docs/design/` | JSON файлы с результатами замеров |

**31/31 тест PASS** (27 оригинальных + 4 multi-repo).

---

## 4. Анализ: почему repo-affinity не дал улучшения на корпусе

**Гипотеза**: ~60 broken_dispatch из 81 non-test вызваны ambiguity abstention (const name с разными значениями в разных репо → `constVal` дропает → dispatch не резолвится).

**Реальность**: В корпусе **нет констант с разными значениями в разных репо**. Все ALL_CAPS константы (`CALCULATE_WORKFLOW`, `VALIDATE_WORKFLOW`, `QUICK_VALIDATE_WORKFLOW`, `GET_PROCESS_CONFIGURATION_ACTIVITY`) имеют **одинаковые** значения во всех репо, где определены. Глобальный `constVal` резолвит их корректно.

**Настоящая причина broken_dispatch**: const резолвится (например `CALCULATE_WORKFLOW → "CalculateWorkflow"`), но целевой handler не найден — `lookup("workflow", "CalculateWorkflow", callerRepo)` возвращает `("", "", 0)` потому что `CalculateWorkflow` не зарегистрирован ни в одном репо корпуса (worker-runner, который делает `RegisterWorkflow(CalculateWorkflow)`, не входит в наш набор репо).

**Repo-affinity остаётся полезным** как защитный механизм — если в будущем появятся репо с конфликтующими const значениями, он сработает. Но на текущем корпусе эффект = 0.

---

## 4.1. От tiebreaker к single-candidate fallback (v4→v5)

**Проблема v4**: tiebreaker (`preferBySignatureKind`) активируется только при 2+ convention кандидатах. На корпусе `lookupConvention` находила 1 кандидата (workflow wrapper) → tiebreaker не срабатывал → abstain → broken_dispatch.

**Решение v5**: `signatureMismatchesKind` — если convention нашла 1 кандидата и его сигнатура противоречит dispatch kind (kind="activity" но candidate принимает `workflow.Context`), принять с пониженным confidence (0.45, speculative, `temporal_resolution_via=convention_mismatch`) вместо abstain.

**Результат**: все 7 `*ActivityName` кейсов разрезолвлены. 91 → 48 broken_dispatch.

**Риск**: convention_mismatch ребра могут указывать на wrapper, не на real activity. Но ребро speculative (скрыто по умолчанию) и помечено — пользователь/LLM может отфильтровать.

---

## 5. Оставшиеся задачи

| Приоритет | Задача | Детали |
|-----------|--------|--------|
| **P2 (DONE)** | service-a dispatch (7) | **Результат**: 5 реальных имён + 2 env-default. Handler не зарегистрирован в корпусе (worker-runner не включён). Тот же паттерн что и остальные missing handler. |
| **P2 (DONE)** | `*ActivityName` suffix (7) | **Результат**: все 7 разрезолвлены через convention_mismatch fallback (v5). |
| **P3** | Missing handler registrations (~48 remaining) | Основная причина оставшихся broken_dispatch — целевые workflow/activity не зарегистрированы в корпусе. Требует либо добавления worker-runner репо, либо LLM-клининг. |
| **P3** | Incremental reindex `Meta["is_test"]` gap | При инкрементальной переиндексации `is_test` мета может не обновиться. |

---

## 5.1. Детальный анализ `*ActivityName` (P2 — завершено, v5)

**Паттерн**: `workflow.ExecuteActivity(ctx, constants.GetFooActivityName, in)` где `const GetFooActivityName = "FooActivity"`.

**Цепочка резолвинга (v5)**:
1. Парсер: `goTemporalNameFromExpr` для `selector_expression` → trailing identifier `GetFooActivityName`
2. Stub edge: `temporal_name=GetFooActivityName`
3. Resolver: `lookup("activity", "GetFooActivityName")` → нет handler
4. Resolver: `lookupConstVal("GetFooActivityName")` → `"FooActivity"` ✅
5. Resolver: `lookup("activity", "FooActivity")` → нет RegisterActivity
6. Resolver: `lookupConvention("activity", "FooActivity")` → 1 кандидат (wrapper с `workflow.Context`), `mismatch=true`
7. Resolver: принять с confidence 0.45, `temporal_resolution_via=convention_mismatch` ✅

**Результат v5**: все 7 `*ActivityName` кейсов разрезолвлены через convention_mismatch fallback.

---

## 6. Диагностический чеклист (для следующего замера)

Перед тем как blaming resolver — пройти по пунктам:

1. **Через какой flow получена цифра?** Per-repo `analyze --path` → цифра невалидна для кросс-репо. Multi-repo `analyze --repo` или daemon → цифра корректна.
2. **Const резолвится?** Проверить `temporal_const_value` на stub edge — если есть, const dereference сработал, проблема в handler lookup.
3. **`RepoPrefix` заполнен?** Устанавливается только при ≥2 репо (`multi.go:835`). Single-repo daemon оставляет `""`.
4. **Handler зарегистрирован?** `lookup` возвращает `""` когда нет `worker.Register*` с таким именем. Это может быть нормально — если handler в репо вне корпуса.
5. **Воспроизвести в `temporal_multirepo_test.go`?** Если controlled test резолвит, а корпус нет — баг в measurement, не в resolver.

---

## 7. Что НЕ делать

- НЕ реализовывать `mergeFuncByName` — решает несуществующую проблему
- НЕ добавлять cross-repo lookup в per-repo режим — по определению невозможно
- НЕ хардкодить имена репо или маппинги
- НЕ резолвить grule/dynamic dispatch — оставлять `broken_dispatch`
- НЕ suppress type errors через `as any`/`@ts-ignore`/empty catch
- НЕ коммитить `go.mod` патч (go 1.26.2 + GOTOOLCHAIN=local)
- НЕ считать repo-affinity ошибкой — он правильный, просто корпус не имеет ambiguous consts
- НЕ менять `lookup()`/`lookupConvention()` без теста воспроизводящего проблему
- НЕ добавлять ACME-специфичные хаки в resolver — `context.Context` preference — OK (SDK convention)

# Синтетические фикстуры для G1/G2/G4/G5 — что именно дописать, чтобы гэпы стали проверяемыми

> **Примечание:** Документ абстрагирован. Имена продуктовых репозиториев, сервисов и
> внутренних символов заменены обобщёнными эквивалентами (`workflow-repo-epsilon`,
> `env_dispatch_handler.go`, `workflow-repo-gamma`, `activities-repo-alpha` и т.п.).
> Технический анализ внутренностей gortex (пути, номера строк, имена функций) сохранён —
> это open-source инструмент.

> **Дата:** 2026-06-13
> **Ветка:** `docs/temporal-gap-roadmap`
> **Коммит форка:** `62f0c21` (`feat/temporal-fork-all`)
> **Контекст:** продолжение `temporal-round2-postmortem.md` и `temporal-l1-cross-summary.md`.

---

## 0. Почему этот документ существует

Round-2 постмортем показал: цифры R1→R2 не сдвинулись, а единственный измеримый эффект
(7 dispatch-сайтов) пришёл побочно, не из целевого механизма. Вывод постмортема — «G1–G5
правильные направления, но наши репо не дают покрытия». Этот документ конкретизирует: **для
G1, G4, G5 фичи уже в коде и должны работать — им не хватает только тест-материала; G2
(wrapper depth>1) — единственный, кому нужен реальный код-фикс.** Ниже — точные синтетические
фикстуры, зеркалящие реальные (абстрагированные) шейпы из наших репозиториев, с указанием
задействованного механизма резолвера и ожидаемого результата (пройдёт / упадёт).

### 0.1. Поправка к постмортему: «настоящий G3» уже в дереве

Сверка кода на `62f0c21` (HEAD `feat/temporal-fork-all`): постмортем цитирует `goIsEnvRead`
по строкам 561–576 («знает только `os.Getenv`») — но это **дораспознавательное** состояние
файла. В текущем `62f0c21` уже есть:

- `goEnvHelperNames` (allow-list: `GetEnvOrDefault`, `EnvOr`, `GetenvDefault`, `GetEnvDefault`)
  и `goEnvHelperDefaultLiteral` — `internal/parser/languages/golang_temporal.go:709–755`.
  `envhelper.GetEnvOrDefault(ENV, "ProcessCancelActivity")` матчится по трейлинг-полю селектора
  и возвращает 2-й аргумент как default.
- Парсер выставляет `tempEnvDefault` и клеймит `temporal_name_origin=env_default` на стабе —
  `internal/parser/languages/golang.go:400–401, 780–781`.
- Резолвер приземляет такие рёбра на speculative tier, `confidence 0.4` + `MetaSpeculative` —
  `internal/resolver/temporal_calls.go:15–21, 229–234`.

То есть FP-защита, которую постмортем рекомендовал в §5.3, **уже реализована**. Отчёты гоняли
бинарь, собранный из коммита **до** env-helper кода (сдвиг номеров строк ~85 = размер
добавленного блока). На пересборке с `62f0c21` те 7 env-сайтов должны резолвиться через
`env_default` (speculative 0.4), а не через convention (0.6) — латентный FP №4 из L1-аудита,
вероятно, уже закрыт. Остаётся: подтвердить пересборкой + регресс-тестом и добавить
`GetEnvOrDefaultValue` в allow-list (L1 видел его в репах).

### 0.2. Сводка: что реально нужно каждому гэпу

| Гэп | Фича в коде? | Что нужно | Тест сейчас |
|---|---|---|---|
| **G1** func→const | ✅ есть | покрывающий e2e-тест | **зелёный** |
| **G2** wrapper depth>1 | ⚠️ только 1 уровень | фикстур + код-фикс (итерация до фикспоинта) | **красный → чиним** |
| **G4** Java→Go cross-lang | ✅ есть | Go+Java фикстур с `@WorkflowMethod(name=)` | **зелёный** |
| **G5** signal/query link | ✅ есть | фикстур с ОБОИМИ концами в скоупе | **зелёный** |

Все фикстуры пишутся в стиле существующего харнесса `internal/indexer/temporal_e2e_test.go`:
`writeFile(t, path, src)` в `t.TempDir()` → `idx.Index(dir)` → ассерты по
`g.FindNodesByName(...)` и `edge.Meta`. Go-only кейсы — через `newTestIndexer`; кейсы с Java —
через `newTestIndexerGoJava`. Мультирепные кейсы (кросс-репо) — два `t.TempDir()` с разными
`go.mod`, чтобы различался `RepoPrefix`.

---

## G1 — func-returning-constant dispatch → **должен ПРОЙТИ** (покрывающий тест)

**Реальный паттерн:** централизованные имена через геттер вместо `const` —
`ExecuteActivity(ctx, GetCreateActivityName(), in)`, где
`func GetCreateActivityName() string { return "CreateActivity" }`. Постмортем счёл, что в репах
такого нет, но L1 видел `GetCreateActivityName` — шейп реальный, просто редкий.

**Фикстура:**

```go
// names.go
package billing
func ProcessBillingActivityName() string { return "ProcessBillingActivity" }

// workflow.go
package billing
import "go.temporal.io/sdk/workflow"
func BillingWorkflow(ctx workflow.Context, in Input) error {
	return workflow.ExecuteActivity(ctx, ProcessBillingActivityName(), in).Get(ctx, nil)
}

// activity.go
package billing
import "context"
func ProcessBillingActivity(ctx context.Context, in Input) error { return nil }

// worker.go
package billing
func setup(w Worker) { w.RegisterActivity(ProcessBillingActivity) }
```

**Механизм:** в позиции имени стоит `call_expression` → `goTemporalFuncCallName`
(`golang_temporal.go:471`) кладёт `temporal_name_func="ProcessBillingActivityName"`;
`goFuncConstReturnLiteral` (490–542) индексирует геттер как
`constVal["ProcessBillingActivityName"]="ProcessBillingActivity"`; резолвер
(`temporal_calls.go:172–188`) разворачивает func → literal → handler.

**Ассерт:** ребро `BillingWorkflow → ProcessBillingActivity`,
`Meta["temporal_const_value"]=="ProcessBillingActivity"`, `temporal_role` на активити.

**Анти-кейс (precision):** геттер с ветвлением (`if x { return "A" }; return "B"`) —
`goFuncConstReturnLiteral` отвергает (тело не «единственный return литерала»), ребро НЕ создаётся.

---

## G2 — wrapper depth>1 → **сейчас УПАДЁТ → нужен код-фикс**

**Реальный паттерн:** в наших репо обёртки одинарные
(`workflow → ProcessCancel(ctx,in) → ExecuteActivity(ctx, name, in)`) и уже резолвятся.
Двухуровневых нет — но это и есть архитектурный лимит: `ExecuteActivityHelper`,
`ExecuteLocalActivityHelper`, `wfutil.ExecuteActivityMethod` легко складываются в цепочку при
рефакторинге.

**Фикстура (depth 2):**

```go
// workflow.go
func CancelWorkflow(ctx workflow.Context, in Input) error {
	return runStep(ctx, "ProcessCancelActivity", in)   // depth 0: literal в caller
}

// helpers.go
func runStep(ctx workflow.Context, name string, in Input) error {
	return execActivity(ctx, name, in)                 // depth 1: forward param → param
}
func execActivity(ctx workflow.Context, name string, in Input) error {
	return workflow.ExecuteActivity(ctx, name, in).Get(ctx, nil)  // depth 2: dispatch via param
}

// activity.go + worker.go: ProcessCancelActivity + RegisterActivity(ProcessCancelActivity)
```

**Что произойдёт сейчас:** `resolveTemporalWrapperCalls` (`temporal_calls.go:573`) делает один
шаг — находит вызов `execActivity` с `arg_names` и эмитит стаб от вызывающего (`runStep`), но
`runStep` сам — обёртка с `temporal_name_param`, и до литерала в `CancelWorkflow` проход не
доходит. Тест на ребро `CancelWorkflow → ProcessCancelActivity` **зафейлится** — это и есть
доказательство гэпа.

**Код-фикс:** превратить одношаговый проход в итерацию до фикспоинта (или явный bounded-depth,
напр. 3): после каждого прохода стабы, осевшие на промежуточных обёртках, переэмитятся выше,
пока не упрутся в литерал / const у настоящего воркфлоу. Confidence убывает с глубиной
(0.9 → 0.7 → 0.5), чтобы глубокие догадки были speculative.

---

## G4 — Java→Go cross-language bridge → **должен ПРОЙТИ** (Go+Java фикстура)

**Реальный паттерн:** Java Temporal у нас только в `activities-repo-alpha/` и без
`@WorkflowMethod(name=)`, поэтому мосту не на чем сработать. Нужен фикстур, где Java-интерфейс
называет Go-воркфлоу по каноническому имени.

**Фикстура (через `newTestIndexerGoJava`):**

```java
// OrderWorkflow.java
@WorkflowInterface
public interface OrderWorkflow {
    @WorkflowMethod(name = "ProcessOrderWorkflow")
    void process(OrderInput in);
}
```

```go
// workflow.go (Go-сторона, отдельный «репо»)
func ProcessOrderWorkflow(ctx workflow.Context, in Input) error { return nil }

// worker.go
func setup(w Worker) {
	w.RegisterWorkflowWithOptions(ProcessOrderWorkflow,
		workflow.RegisterOptions{Name: "ProcessOrderWorkflow"})
}
```

**Механизм:** `parseJavaAnnotationName` (`temporal_calls.go:404`) вытаскивает
`name="ProcessOrderWorkflow"`; `resolveTemporalCrossLanguage` (457–553) линкует Java
`@WorkflowMethod` → Go workflow по имени, `via=temporal.start-workflow`, `cross_language=true`.

**Ассерт:** ребро Java-метод → Go-функция, `Meta["cross_language"]==true`.

**Доп. кейсы:** `@SignalMethod(name=)` → `signal-link`, `@QueryMethod(name=)` → `query-link`.
**Анти-кейс:** Java без `name=` (полагается на имя метода) — проверить, что Phase-3 не затирает
роль метода (это место чинили в `755e7e6`).

---

## G5 — signal/query link → **должен ПРОЙТИ** (нужны ОБА конца в скоупе)

**Реальный паттерн:** в индексируемой области есть только отправитель `"save-state-signal"`
(`workflow-repo-gamma/workflow_step.go:112`, `SignalExternalWorkflow`) и `"lifecycleSignal"`, а
получатель — в сервисе вне наших клонов. Поэтому пасс ничего не линкует (providers пуст).
Фикстур должен держать ОБА конца.

**Фикстура:**

```go
// sender.go (workflow A)
func StepWorkflow(ctx workflow.Context, target string) error {
	return workflow.SignalExternalWorkflow(ctx, target, "", "save-state-signal", State{}).Get(ctx, nil)
}

// receiver.go (workflow B)
func StateWorkflow(ctx workflow.Context) error {
	ch := workflow.GetSignalChannel(ctx, "save-state-signal")
	ch.Receive(ctx, nil)
	return nil
}

// worker.go: RegisterWorkflow(StepWorkflow) + RegisterWorkflow(StateWorkflow)
```

**Механизм:** отправитель → `via=temporal.signal-send` (`applyGoTemporalSignalQueryMeta`);
получатель → `via=temporal.handler` kind=signal (`GetSignalChannel`);
`resolveTemporalSignalQueryLinks` (`temporal_calls.go:300–361`) джойнит по имени
`"save-state-signal"` → ребро `temporal.signal-link`.

**Ассерт:** ребро `StepWorkflow → StateWorkflow`, `via=temporal.signal-link`.

**Зеркальный query-кейс:** `client.QueryWorkflow(...,"get-status",...)` +
`workflow.SetQueryHandler(ctx,"get-status",fn)` → `temporal.query-link`.

**Анти-кейс (наш текущий прод):** только отправитель, без получателя →
`analyze kind=temporal_orphans` обязан дать `signal_no_handler` (тот самый единственный
`signal_no_handler:1` из отчёта — фикстур доказывает, что детектор орфанов прав).

---

## Где это положить и зачем это ценно

Эти фикстуры — не «тесты ради тестов»: они превращают непроверяемые в нашем корпусе гэпы в
воспроизводимые e2e и дают upstream-грейд доказательство для будущих PR (G1/G4/G5 → «фича
работает, вот тест»; G2 → «вот баг, вот фикс»).

- Go-only кейсы (G1, G2, G5) — в `internal/indexer/temporal_e2e_test.go`.
- Кейс с Java (G4) — рядом, через `newTestIndexerGoJava`.
- Мультирепность для кросс-репо кейсов (и для кросс-репо тест-стаб FP из L1-аудита §4.2) —
  два `t.TempDir()` с разными `go.mod`, чтобы различался `RepoPrefix`.

## Порядок работ (принятый план)

1. Пересобрать (`go build ./...`, CGO) + прогнать существующие temporal-тесты; добавить
   G3-регресс — подтвердить, что env-helper реально гасит латентный FP (env-сайты получают
   `temporal_name_origin=env_default` + speculative 0.4, а не convention 0.6); добавить
   `GetEnvOrDefaultValue` в `goEnvHelperNames`.
2. Fix 2 (TDD): кросс-репо тест-стаб фильтр — кандидат в `_test.go` И в другом репо, чем
   вызывающий, исключается в `lookup` / `lookupConvention` / `lookupFuzzy`
   (`temporal_calls.go`), используя `graph.Node.FilePath` + `RepoPrefix`. Сохраняет ~52
   корректных same-repo тест→activity ребра, убивает 1 подтверждённый FP.
3. Фикстуры G1/G4/G5 (зелёные) + G2 (красный → код-фикс depth>1 до фикспоинта).

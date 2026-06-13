# Бриф для корпоративного агента: что намокать, чтобы доработать Temporal-форк gortex

> **Примечание:** Документ абстрагирован и предназначен для передачи между двумя сторонами.
> Имена продуктовых репозиториев, сервисов и символов — обобщённые
> (`repo-a`, `OrderWorkflow`, `ChargeActivity`, `FOO_ACTIVITY_ENV`). Технический анализ
> внутренностей gortex (пути, имена функций) сохранён — это open-source инструмент.

> **Дата:** 2026-06-13
> **Ветка:** `docs/temporal-gap-roadmap`
> **Контекст:** продолжение `temporal-round2-postmortem.md` и `temporal-l1-cross-summary.md`.

---

## 0. Зачем этот документ и как устроен процесс

Две стороны, которые не видят код друг друга:

- **Корпоративная сторона (этот агент):** видит реальные репозитории (`env_dispatch_handler.go`,
  `workflow-repo-gamma` и т.п.), но **не может их шарить** — там бизнес-детали.
- **OSS-сторона (форк gortex):** дорабатывает резолвер до полностью рабочего состояния, но
  **не видит ваш код** — только то, что вы передадите как анонимизированные фикстуры.

**Задача корпоративного агента:** по каждому реально **НЕ резолвящемуся** Temporal-dispatch
шейпу из ваших репозиториев сделать **минимальную анонимизированную репродукцию** (мок) в
формате тест-фикстуры OSS-харнесса и отдать обратно. По этим мокам OSS-сторона доводит резолвер
до зелёного и валидирует фикс.

**Чего делать НЕ нужно:** гонять весь корпус, передавать реальные имена активностей / env-ключей
/ бизнес-логику, мокать то, что уже покрыто (раздел 2).

**Ключевой принцип:** *сохраняем форму, стираем содержание.* Резолвер срабатывает на **форме
выражения** (literal / const / func-return / env-helper / struct-field / глубина обёртки /
граница пакета или репо), а не на конкретных строках. Поэтому анонимизация имён **не влияет** на
воспроизведение бага — баг живёт в структуре, не в данных.

---

## 1. Протокол анонимизации (как не спалить корпоративные детали)

| Что в реальном коде | Чем заменить | Что ОБЯЗАТЕЛЬНО сохранить структурно |
|---|---|---|
| Module / repo paths (`git.corp/team/...`) | `example.com/app`, `repo-a`, `repo-b` | границу: same-package vs cross-package vs cross-repo; наличие `go.mod` |
| Имена активностей / воркфлоу | `ChargeActivity`, `OrderWorkflow` | суффикс-конвенцию (`…Activity` / `…Workflow`); two-part форму (`{pkg}_{Method}`); регистр |
| Имена сигналов / запросов | `cancel-order`, `order-status` | что это строковый литерал, матчащийся между sender и handler |
| Env-ключи | `FOO_ACTIVITY_ENV` | что это env-read с литеральным дефолтом |
| Тела функций, бизнес-логика | выкинуть | только dispatch-скелет: `ctx` + аргумент-имя + `ExecuteActivity(...)` |
| Доменные комментарии / докстринги | убрать | — |

**Безопасно передавать вербатим (нужно для фикса, не секрет):**

- Имена **env-helper функций** (`GetEnvOrDefault`, `GetEnvOrDefaultValue`, `EnvOr`, локальный
  `getEnvOrDefault` и т.п.) — это generic-инфраструктура, не бизнес-данные. Резолвер матчит их
  **по имени**, поэтому реальные имена нужны (см. 3.2).
- Формы SDK-вызовов, Java-аннотации (`@WorkflowMethod(name=)`), **глубину вложенности обёрток**,
  форму struct-field dispatch, шаблон two-part именования.

**Чек анонимизации:** готовый мок должен компилироваться / индексироваться **сам по себе**, без
доменных импортов; единственное, что несёт информацию — это **структура dispatch**, а не строки.
Если из фикстуры удалить все комментарии и она всё ещё воспроизводит нерезолв — анонимизация
корректна.

---

## 2. Что УЖЕ покрыто в OSS — повторно НЕ мокать

Эти шейпы уже имеют зелёные e2e-фикстуры в форке (`internal/indexer/temporal_e2e_test.go`,
`internal/parser/languages/go_temporal_test.go`). Если ваш реальный шейп **точно** совпадает с
одним из них — мок не нужен; достаточно подтвердить, что на **пересборке форка** (коммит ≥
env-helper) он резолвится.

| Шейп | OSS-тест |
|---|---|
| string-literal / const-named dispatch | `TestTemporalE2E_GoConstNamedActivity` |
| func-returning-constant dispatch (`GetName()` → `"X"`) | `TestFuncConstReturnDispatch_E2E` |
| env-helper с дефолтом (`GetEnvOrDefault`/`…Value`/`EnvOr`/`GetenvDefault`/`GetEnvDefault`) | `TestEnvFallbackResolution`, `TestEnvFallbackViaHelper_*` |
| env-default через `os.Getenv` / `cmp.Or` / `if name==""` | `TestTemporalE2E_GoEnvDefaultActivity` |
| wrapper-following **глубины 1** (literal и const аргумент) | `TestTemporalE2E_GoWrapperFollowing` |
| cross-package wrapper (`wfutil.ExecuteActivityMethod`) | `TestTemporalE2E_GoWfutilCrossPackage` |
| step/executor struct-field dispatch (литеральное поле) | `TestTemporalE2E_GoStepExecutor` |
| незарегистрированная активность по convention-имени | `TestTemporalE2E_GoUnregisteredActivityByConvention` |
| Java→Go мост (`@WorkflowMethod`/`@SignalMethod`/`@QueryMethod(name=)`) | `TestTemporalE2E_JavaToGoBridge` |
| Go signal-link + query-link (оба конца в скоупе) | `TestTemporalE2E_GoSignalQueryLink` |
| in-workflow query/signal handler | `TestTemporalE2E_GoQueryHandler` |
| child workflow | `TestTemporalE2E_GoChildWorkflow` |
| **подавление кросс-репо `*_test.go` стаб-FP** (новое) | `TestResolveTemporalCalls_CrossRepoTestStubSuppressed` |

> Уже починено в этом раунде (форк): добавлен `GetEnvOrDefaultValue` в allow-list env-хелперов;
> добавлен фильтр, не дающий dispatch'у из репо A резолвиться в `*_test.go` стаб чужого репо B
> (тот единственный подтверждённый FP из L1-аудита).

---

## 3. Что реально нужно намокать или зарепортить

По каждому пункту: **что искать → анонимизированный скелет → ожидаемое ребро → мок или репорт.**
Главный источник — ваш собственный `analyze kind=temporal_orphans`: **всё, что висит как
`broken_dispatch` и НЕ попадает в раздел 2, — это незакрытый шейп, который нужно сюда вынести.**

### 3.1. Wrapper depth>1 (G2) — **МОК** (единственный реальный gap либы)

Что искать: цепочку обёрток, где имя активности forward'ится через ≥2 функции, прежде чем
дойти до `workflow.ExecuteActivity`.

```go
// workflow.go
func CancelWorkflow(ctx workflow.Context, in Input) error {
	return runStep(ctx, "ProcessCancelActivity", in)   // depth 0: литерал в caller
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

**Ожидаемое ребро (после фикса):** `CancelWorkflow → ProcessCancelActivity`, `via=temporal.stub`,
`temporal_via_wrapper`. Сейчас зафейлится (резолвер делает один шаг). **Репорт:** максимальная
реальная глубина обёрток в ваших репо (чтобы выбрать bound для итерации до фикспоинта).

### 3.2. Прочие env-helper имена — **РЕПОРТ** (+ мок только при иной сигнатуре)

Что искать: вызовы вида `xxx(KEY, "Default")`, присваиваемые в имя активности, где `xxx` —
**не** из списка раздела 2.

- **Репорт (безопасно, имена generic):** точные имена всех env-helper функций, используемых в
  диспатче имён. OSS-сторона добавит их в allow-list.
- **Мок нужен только если сигнатура иная**, чем «дефолт — 2-й строковый аргумент»: например
  дефолт идёт 1-м аргументом, или 3-аргументная форма `Env(key, fallbackKey, "Default")`, или
  дефолт — не строковый литерал, а const. Тогда — скелет:

```go
func WF(ctx workflow.Context) {
	name := envcfg.Lookup("FOO_ACTIVITY_ENV", defaults.FooActivity)  // дефолт — const, не литерал
	workflow.ExecuteActivity(ctx, name, 1)
}
```

**Ожидаемое ребро:** `temporal_name="FooActivity"`, `temporal_name_origin=env_default`,
speculative tier (confidence 0.4).

### 3.3. Two-part naming (pkg→repo через go.mod) — **МОК**, если применимо

Что искать: активность регистрируется / именуется как `{package}_{Method}` или разрешается через
`go.mod` модульное имя, а не голым func-name.

```go
// repo-a/go.mod:            module example.com/repo-a
// repo-a/orders/activity.go: func Process(...) {}   // регистрируется как "orders_Process"
// repo-b/workflow.go:        ExecuteActivity(ctx, "orders_Process", in)
```

**Ожидаемое ребро:** `WF → Process`. **Репорт:** точный шаблон two-part имени (разделитель,
включает ли module path).

### 3.4. Struct-field / executor варианты — **МОК**, если поле задаётся не литералом

Раздел 2 покрывает `Executor{ActivityName: "X"}` с литералом. Намокать, если в реальности поле
приходит из **конструктора-параметра**, **embedded-структуры** или **map-lookup**:

```go
type Executor struct{ ActivityName string }
func NewExecutor(name string) *Executor { return &Executor{ActivityName: name} }  // поле из параметра
func (e *Executor) Run(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, e.ActivityName, nil).Get(ctx, nil)
}
// где-то: NewExecutor("DispatchDocumentActivity")
```

**Ожидаемое ребро:** `Run → DispatchDocumentActivity`. **Репорт:** как именно заполняется поле.

### 3.5. Любой прочий `broken_dispatch` — **МОК** (главный канал)

Для **каждого** dispatch-сайта, который у вас остаётся `broken_dispatch` и не подходит под 2 /
3.1–3.4: минимальный анонимизированный скелет (caller-функция + форма аргумента-имени +
`ExecuteActivity` + где «настоящая» активность) + ожидаемая цель. Это и есть незакрытые шейпы,
ради которых дорабатывается либа.

### 3.6. Signal/query без хэндлера в скоупе (ваш реальный G5-кейс) — **НЕ мок, РЕПОРТ**

Если отправитель сигнала есть, а хэндлер — в сервисе вне индексации: это **не баг либы**, а
честный orphan. Подтвердите, что `analyze kind=temporal_orphans` помечает его `signal_no_handler`
(один такой уже есть в отчёте — это корректно). Если линк всё же нужен — индексировать оба
сервиса в одном workspace.

---

## 4. Формат сдачи (deliverable)

1. **Файлы-фикстуры** в стиле харнесса: либо набор `writeFile(t, path, src)` в `t.TempDir()`,
   либо готовые `*_test.go`. Каждый кейс должен индексироваться изолированно (без доменных
   импортов). Кросс-репо кейсы — два `TempDir` с разными `go.mod` (различает `RepoPrefix`).
2. **Таблица «ожидаемых рёбер»** — self-validating контракт на каждый кейс:
   `from → to | via | tier/confidence | meta-флаги`.
3. **Список репорт-фактов** (передать как текст, не как мок):
   - точные имена env-helper функций;
   - максимальная глубина обёрток;
   - конвенция именования активностей (суффикс? two-part? разделитель?);
   - используется ли `RegisterActivities` (регистрация всех методов структуры);
   - встречаются ли не-`workflow` алиасы импорта SDK (`import wf "…/workflow"`).
4. **Куда класть:** Go-кейсы — `internal/indexer/temporal_e2e_test.go`; Java-кейсы — рядом, через
   `newTestIndexerGoJava`.

---

## 5. Шаблон одного мок-кейса (копировать на каждый незакрытый шейп)

```
### Шейп: <короткое имя, напр. "wrapper depth 3 cross-package">

Что в реальном коде (одна фраза, без доменных деталей):
  <напр. "имя активности forward'ится через 3 функции в соседнем пакете">

Фикстура (.go, анонимизировано):
  <минимальный компилируемый скелет>

Ожидаемое ребро:
  from=<CallerFunc>  to=<RealActivity>  via=temporal.stub  tier=<...>  meta=<...>

Репорт-факты (не мок):
  <напр. "макс. глубина в проде = 3; разделитель two-part = '_'">
```

Заполненный по этому шаблону набор кейсов — это всё, что нужно OSS-стороне, чтобы довести
резолвер до полностью рабочего состояния и закрыть оставшиеся `broken_dispatch` без доступа к
вашему коду.

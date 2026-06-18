# AGENTS.md — Temporal dispatch allow-list (для корпоративного агента)

Этот файл — инструкция агенту, работающему в **корпоративном форке** gortex, по
сопровождению распознавания Temporal-диспатча под ваш кодовой базы. Цель: повышать точность
графа Temporal **без утечки корпоративных имён** в исходники / upstream.

> Не путать с `docs/agents.md` (это про agent-адаптеры самого gortex). Здесь — только про
> Temporal allow-list и LLM-клининг.

---

## 1. Как устроено распознавание (три слоя)

Имя активности/воркфлоу в `workflow.ExecuteActivity(ctx, <name>, …)` часто приходит не литералом.
Форк распознаёт его тремя слоями, по возрастанию доверия:

1. **Generic-эвристика (recall, скрыто).** Любой хелпер с `env` в имени —
   `cfg.ActivityFromEnv("KEY", "Default")` — распознаётся структурно: 2-й строковый аргумент
   берётся как дефолтное имя. Ребро садится на **speculative 0.4** (`temporal_env_source=heuristic`),
   скрыто из обычных запросов. Вести ничего не нужно — работает само.
2. **Allow-list (precision, видимо).** Если имя хелпера в built-in списке (`GetEnvOrDefault`,
   `GetEnvOrDefaultValue`, `EnvOr`, `GetenvDefault`, `GetEnvDefault`) **или** в вашем
   репо-локальном allow-list — ребро повышается до **inferred 0.6**, видимо по умолчанию
   (`temporal_env_source=allowlist`). `os.Getenv(...)` / `cmp.Or(...)` тоже → 0.6.
3. **LLM-клининг (опционально).** Проход `gortex analyze --kind temporal_verify` отдаёт каждое
   рёбро уровня ≤0.65 вашей LLM, заземляя в реальном коде: confirmed → повышает и делает видимым,
   rejected → гасит (скрывает), uncertain → оставляет как есть. Register-confirmed (0.9) не трогает.

**Главное:** слой 1 жадный и безвредный (скрыт), слой 2 точечно повышает, слой 3 чистит. Поэтому
**не обязательно** перечислять все хелперы — список нужен лишь чтобы **сделать конкретный хелпер
видимым по умолчанию**.

---

## 2. Как вести allow-list

Файл **git-ignored** (см. `.gitignore`: `.gortex/`), читается **только** под env-гейтом.

1. Включить гейт: `export GORTEX_ALLOW_LOCAL_TEMPORAL=1`.
2. Создать `.gortex/temporal-allowlist.yaml` в корне репозитория:

```yaml
# Имена ваших env-хелперов, по которым диспатч резолвится на дефолт.
# Сопоставление по имени функции (без пакета), регистронезависимо.
env_helpers:
  - GetActivityNameFromEnv
  - FetchActivityName
  - resolveActivity            # локальные/lowercase тоже годятся
```

3. Проверить эффект: `gortex analyze --kind temporal_orphans --path . --format json` (до/после) —
   часть `broken_dispatch` должна закрыться, env-сайты получить `temporal_env_source=allowlist`.

Что добавлять / чего нет:

- **Добавлять:** имена функций-обёрток «прочитать env и вернуть дефолт». Это generic-инфраструктура,
  не бизнес-данные.
- **НЕ добавлять:** имена активностей/воркфлоу/бизнес-логику. Они тут не нужны (распознаются
  структурно), и им не место даже в git-ignored файле.

---

## 3. Протокол анонимизации (не спалить корпоративное)

- Файл `.gortex/temporal-allowlist.yaml` **git-ignored** — не коммить его и не добавлять в PR.
- Имена env-хелперов сами по себе — generic infra (безопасны), но всё равно держим их **только** в
  локальном файле, а не в исходниках форка.
- Если делаешь фикстуры для передачи OSS-стороне — следуй
  `docs/temporal-compare/temporal-gap-synthetic-fixtures.md` (протокол: «сохраняем форму, стираем
  содержание»): repo-пути → `example.com/app`, имена активностей → `ChargeActivity`, env-ключи →
  `FOO_ACTIVITY_ENV`, тела → выкинуть.

---

## 4. LLM-клининг (precision-проход вашей моделью)

Требует настроенного `llm.provider` (например ваш `custom-provider`) в `.gortex.yaml` /
`~/.config/gortex/config.yaml`.

```bash
gortex analyze --kind temporal_verify --path . --format json
```

- Проверяет **только** низкодоверенные рёбра (speculative 0.4 + inferred 0.6) — набор маленький,
  стоимость ограничена. Register-confirmed 0.9 не трогает.
- Вердикты кэшируются в git-ignored `.gortex/temporal-verify-cache.json` по хэшу
  (модель + имя + исходник вызова + исходник кандидата) → повторный прогон детерминирован и
  бесплатен (годится для CI). Меняется код или модель — кэш промахивается и перепроверяет.
- Вывод: `checked / confirmed / rejected / uncertain / errors` (+ `details` в JSON с причинами).
- На каждом ребре остаются `temporal_llm_verdict` и `temporal_llm_reason` для аудита.

---

## 5. Что делать с тем, что всё ещё не резолвится

Для dispatch-шейпов, остающихся `broken_dispatch` и не закрытых ничем выше, —
`docs/temporal-compare/temporal-gap-synthetic-fixtures.md`: какие минимальные анонимизированные
фикстуры отдать OSS-стороне, чтобы дорезолвить либу.

---

## DECISION (2026-06-16): Java resolution stays source-only — jdtls / scip-java consciously declined

For this fork's workflow we **deliberately do NOT use a compiler-backed Java
resolver** (neither the jdtls LSP nor scip-java). gortex's source-only pipeline
(tree-sitter + the in-process `java-types` resolver) is the chosen level of
fidelity. The whole point here is to **browse a project without compiling it**,
and the source graph already delivers that.

**Why (measured on Drools, 219k nodes / 1,013,593 edges, zero compilation):**
the graph already carries the full Java edge shape from source —
`calls: 379,638`, `typed_as: 51,794`, `implements: 22,635`, `extends: 3,213`,
`overrides: 2,249`, plus returns / throws / annotated / member_of. That is
plenty for navigation, usages, call chains, architecture, semantic search.

**What a compiler resolver would have added, and why we skip it:**
- jdtls / scip-java only sharpen the *ambiguous* subset (overload resolution,
  generic/inferred types, inherited & dependency-classpath members, exact scope
  binding). On `drools-io` jdtls scored `confirmed=78 added=29 refuted=0`
  (~10 % coverage) — i.e. it confirmed the heuristic (refuted=0 ⇒ source edges
  weren't *wrong*, just incomplete on hard cases). Marginal for browsing.
- **scip-java requires compiling the whole project** (SemanticDB build) — a hard
  no for the "view without compilation" goal.
- **jdtls does not scale**: one LSP instance importing a ~100-module Maven
  reactor crashes and the reconnect re-imports forever (observed: 78 min, 0
  edges). It only completes per-module (~8 min each → ~13 h for the reactor).

**What this means operationally:** run the daemon with `GORTEX_LSP_DISABLE=all`
(no jdtls auto-register). The source-only `java-types` resolver runs during
normal indexing and gives 100 % coverage in milliseconds — that is the Java
enrichment the graph actually relies on. Re-verified 2026-06-18 on the
post-merge binary: jdtls scores ~8 % coverage (`drools-util`: 109/1308 nodes,
`refuted=0` / `hover_err=0` — it never contradicts source edges), ~3 min per
small module, and **hung in post-hover processing** — marginal benefit at an
impractical, unreliable cost. The fork's `gortex enrich semantic` command and
the jdtls two-pass recipe (whose only purpose was driving jdtls on a warm graph)
were therefore **removed (2026-06-18)**. The scip-java default provider was
added (`afcfc65`) and then **reverted (`d498dfb`)** per this same decision.

# ТЗ: Java EE / Jakarta EE edge resolvers для графа gortex (объединённый)

> Объединяет: исходный ТЗ `docs/java-ee-edge-resolvers.md` (проблема, корпус, приоритеты,
> критерии приёмки) + архитектурный дизайн встройки в gortex + правки архитектурного ревью
> (oracle). Цель — buildable-план без параллельной системы и без уверенно-неверных рёбер.

## 1. Проблема

Статический анализатор (tree-sitter) строит граф по прямым вызовам `a.b()`. В Java EE-монолитах
компоненты связываются **декларативно** — через аннотации и дескрипторы, а не вызовами. Слепые
зоны на архитектурных границах:

- JMS: `queueSender.send()` → MDB `onMessage()` — нет ребра
- EJB: `@EJB`/`@Inject` интерфейса → `@Stateless` impl — нет ребра
- REST: `@GET @Path("/x")` → метод-handler / клиент — нет ребра
- SOAP: `@WebService`/`@WebMethod` → endpoint — нет ребра
- Servlet: `web.xml` / `@WebServlet` → класс; filter-chain — нет рёбер
- CDI: `@Inject` интерфейса → `@ApplicationScoped` bean — нет ребра

На реальном BSS-монолите (~240K нод) это ≈ **30% потерянных рёбер** на границах. Сегодня gortex
уже умеет: Spring `@Bean` DI (`di_contracts.go`), JAX-RS/Spring HTTP *как entrypoints* +
HTTP-контракты (`contracts/http.go`, `schema_enrich_java.go`), JPA `@Entity`→table, MyBatis XML.
Чего нет: CDI/EJB wiring, JMS sender↔listener, REST route→handler dispatch + клиенты, JAX-WS,
servlet/filter chains.

## 2. Цель

Дополнить индексер **annotation/descriptor-aware резолверами**, достраивающими рёбра тех же
типов, что и существующие (`calls`, `implements`, `references`, `provides`, `matches`, `spawns`),
с честным `origin`/`confidence`. **Реюзать существующие подсистемы gortex** (synthesizer-фреймворк
и contracts), а не строить параллельную.

## 3. Архитектура встройки в gortex

### 3.1 Двухслойный extract→resolve (устоявшийся паттерн)

- **Extract (Java-экстрактор):** на парсинге клеит аннотации + *placeholder*-рёбра на
  `unresolved::<token>` с meta. Без резолва. Зеркалит TS-DI (`emitInjectFromDecorator`,
  `emitProvidersFromObject` в `typescript.go`) и конвенцию `di_token`/`provides_for`.
- **Resolve (`FrameworkSynthesizer`):** идемпотентный полно-пересчётный проход, приземляющий
  placeholder'ы на реальные цели.

Интерфейс (`internal/resolver/framework_synth.go:31`):
```go
type FrameworkSynthesizer interface {
    Name() string                  // провенанс-тег: "cdi", "ejb", "jms", ...
    Synthesize(g graph.Store) int  // сколько рёбер приземлено за прогон
}
```
Регистрация в `defaultFrameworkSynthesizers()` (`framework_synth.go:114`) как
`synthFunc{name: SynthCDI, fn: ResolveCDIInjection}`; новые имена занести в `Synth*`-const-блок
(:55-67), чтобы `analyze kind=synthesizers` их атрибутировал. Порядок load-bearing: после
`InferImplements` (нужны `EdgeImplements`), до `DetectCrossRepoEdges`. Точка вызова уже есть:
`indexer.go:743` (`RunGlobalGraphPasses`) и `:2491` (inline). **Менять pipeline не нужно.**

Каноническое ребро + провенанс (копия MyBatis, `mybatis_calls.go:110`):
```go
e.To = target
e.Origin = origin                          // OriginASTResolved/Inferred/Speculative
e.Confidence = conf
e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, conf)
StampSynthesized(e, SynthCDI)              // Meta["synthesized_by"], Meta["provenance"]
// идемпотентность: собрать EdgeReindex{Edge, OldTo}, применить g.ReindexEdges(batch)
```

### 3.2 Владение слоями — КЛЮЧЕВОЕ (правка ревью)

Contract-пасс (`extractDIContracts`→`commitContracts`, `indexer.go:2444`/`860`) и
synthesizer-пасс (`RunFrameworkSynthesizers`, `indexer.go:2491`/`743`) — **разные стадии с
разными типами рёбер** (`EdgeMatches` vs `EdgeCalls`). Один `@Inject` нельзя гнать через оба
(получишь дубль-рёбра — это и есть «параллельная система», которой избегаем).

| Подсистема | Форма | Владелец (пасс) | Тип ребра | In-repo резолв | Cross-repo |
|---|---|---|---|---|---|
| **EJB, CDI** | dispatch synthesis | `FrameworkSynthesizer` | `EdgeCalls` | `EdgeImplements`+`EdgeExtends` + producer-индекс | `di::` contract matcher **только** |
| **JMS** | channel | synthesizer + промежуточная нода | `spawns` через `jms::<dest>` ноду | destination литерал/const | `jms::` matcher |
| **REST, SOAP** | contract enrichment | **contracts-подсистема** (`contracts/http.go`) | `EdgeMatches` | route↔client по id | `http::`/`soap::` matcher (нативно) |
| **Servlet/filter-chain** | ordered edges | отдельный проход | `references` + `order` meta | url→class, порядок chain | n/a |

**Итог: EJB/CDI/JMS = synthesizer; REST/SOAP = contract-enrichment (уже почти есть); filter-chain
= ordered-edge pass.** Общий у них только `StampSynthesized`.

Два кода-факта, ограничивающих дизайн (проверено):
- **`InferImplements` — только интерфейсы и repo-gated** (`resolver.go:2302,2411`): сверяет
  method-set'ы только `KindInterface` и `continue`'ит между репозиториями. ⇒ abstract-class бины
  (`@Inject FooBase`, это `KindType`) нужно резолвить через **`EdgeExtends`**, а структурный CDI
  **не работает cross-repo** — cross-repo только через `di::` contract-matcher.
- **`contracts.Matcher.Match` даёт полный декартов продукт** (`matcher.go:81`). Политика «N>1 →
  none» (§7) может жить **только в synthesizer'е (`EdgeCalls`)**, не в contract-слое. Чтобы
  бакеты не пухли — фолдить qualifier в id: `di::<token>::<qualifier>`.

### 3.3 Synthetic-ноды

Резолверы создают synthetic-ноды для не-Java-классов; они обогащают `contracts list`,
`get_callers`, `api_impact`:

| Kind | Пример | Описание |
|---|---|---|
| `queue` | `jms::BSSSingleChangePPQueue` | JMS queue/topic (промежуточная нода sender→queue→MDB) |
| `contract` (http) | `http::GET::/request/changePricePlan` | REST/servlet endpoint |
| `contract` (ws) | `soap::SubscriberService::changePP` | SOAP endpoint |

### 3.4 Origin / confidence

| Origin (gortex const) | Conf | Когда |
|---|---|---|
| `OriginASTResolved` | 0.9 | прямой вызов; **или** binding подтверждён дескриптором (`<servlet-mapping>`, enabled `@Alternative`, `@EJB(beanName)`) |
| `OriginASTInferred` | 0.7 | аннотация + уникальный impl через `EdgeImplements`/`EdgeExtends`; qualifier-disambiguated |
| `OriginSpeculative` (hidden, `Meta[MetaSpeculative]=true`) | 0.45 | эвристический destination; multi-candidate без qualifier (если выбрана recall-политика) |
| `OriginTextMatched` | 0 | существующая эвристика по имени |

Уникальный structural-match даём **0.7** (как MyBatis, `mybatis_calls.go:173`), не инфлейтим до
0.9 «просто потому что уникален». 0.9 — только для дескриптор-подтверждённых binding'ов.

### 3.5 Производительность (правка ревью)

Synthesizer'ы полно-пересчётны каждый settle (`framework_synth.go:18`). На 240K-монолите:
- XML парсить в ноды **один раз на extract**, в проходе читать из meta (не пере-парсить файл);
- producer-индекс **repo-scope'ить** как `di_contracts.go` (`GetRepoNodes`/`GetRepoEdges`,
  :44/:122) — без этого rebuild индекса по всем Java-нодам каждый settle = обрыв.

## 4. Подсистемы

### 4.1 EJB interface→impl  (P0)

**Сигналы.** Интерфейс с `@Local`/`@Remote`; impl `@Stateless`/`@Stateful`/`@Singleton`
`implements` его; инъекция `@EJB`/`@Inject` интерфейса + вызов `ref.method()`.

**Резолв (synthesizer `ejb`).** Индекс `интерфейс → []impl` (по `EdgeImplements` + EJB-аннотации
класса). На `@EJB`/`@Inject` интерфейса:
- 1 impl → `EdgeCalls` точка→impl.method, `OriginASTInferred` 0.85;
- N impl → §7 (политика неоднозначности);
- `@EJB(beanName="X")` / `<ejb-link>` → точный маппинг, 0.9;
- `@LocalBean` (no-interface) → пропустить (обычный класс);
- `@Remote` — пометить (может быть вне графа, cross-JVM).

### 4.2 JMS producer→consumer  (P0)

**Сигналы.** Listener: `@MessageDriven` + `@ActivationConfigProperty(destination=X)` /
`MessageListener.onMessage` / Spring `@JmsListener`. Sender: `jmsContext.createProducer().send(
dest, …)`, `jmsTemplate.convertAndSend("q", …)`. Destination: `@Resource(lookup="jms/Q")`,
`@JMSDestinationDefinition`, `@ActivationConfigProperty`, литерал/const (reuse const-deref).

**Резолв (synthesizer `jms`).**
1. На extract: для `@MessageDriven` извлечь `destination`+`destinationType`, нормализовать JNDI.
2. Synthetic-нода `jms::<destination>` (kind=`queue`/`topic` по `destinationType`).
3. `spawns` от `jms::<dest>` → `MDB.onMessage()`.
4. Для каждого `send()` с destination-ссылкой X → `calls` от метода-отправителя → `jms::<dest>`.
5. Итог двух-хоп: `Sender.send() → jms::X → MDB.onMessage()` (виден в `get_callers(onMessage)`).
6. Tier: destination литерал/const → 0.8; только эвристика → 0.45 hidden.

⚠️ `@Resource` перегружен: `@Resource DataSource` (DI) vs `@Resource(lookup="jms/Q")` (JMS) —
различать по `lookup`/`name` + типу цели, иначе DataSource-инъекции попадут в JMS-контракты.

### 4.3 CDI `@Inject` / `@Produces`  (P1, общая машинерия с EJB)

**Сигналы.** Инъекция: `@Inject` поле/конструктор-параметр. Провайдеры: `@Produces` метод/поле;
managed-bean классы (`@ApplicationScoped`/`@RequestScoped`/`@Dependent`/…) провайдят свой тип +
все `implements`-интерфейсы. Qualifier: `@Named("x")`, `@Default`, user `@Qualifier`.

**Extract.** `@Inject FooService svc` → `EdgeConsumes {via:"@Inject", di_token:"FooService",
qualifiers:[...]}`. `@Produces FooService make()` → `EdgeProvides {binding:"cdi.produces",
provides_for:"FooService"}`. Managed-bean → провайдер своего типа + интерфейсов.

**Резолв (synthesizer `cdi`).** Producer-индекс `тип → []провайдер` из (a) `@Produces`,
(b) managed-bean классов, (c) интерфейсов через `EdgeImplements`, (d) abstract-class через
`EdgeExtends`. На `@Inject`:
- 1 кандидат → `EdgeCalls`, 0.8 (интерфейс) / 0.85 (конкретный класс);
- `@Named`/`@Qualifier` → точный, 0.9;
- N без qualifier → §7.

⚠️ **`diContractFromEdge` switch закрыт** (`di_contracts.go:243-253`): неизвестный `binding`
падает в `default: return zero,false` и тихо дропается. Добавление `cdi.produces`/`cdi.managed`
требует правки **и** этого switch, иначе контракты исчезнут без ошибки. **Cross-repo CDI — только
через `di::<token>::<qualifier>` contract-matcher** (structural implements repo-gated).

### 4.4 REST: JAX-RS + Spring MVC  (P1, contract-enrichment)

Server-side частично есть. Закрыть: конкатенацию class-`@Path` + method-`@Path`
(+ `@ApplicationPath`) → `http::<METHOD>::<normalized-path>`; client-консьюмеры (JAX-RS
`client.target(..).path(..).request().get()`, Spring `RestTemplate`/`WebClient`/`@FeignClient`).
Парность server↔client — нативным HTTP-matcher'ом (`contracts/http.go`, `schema_enrich_java.go`),
**не synthesizer'ом**. `provides` метод→contract 0.95; параметры пути сохранять как pattern
(`/request/{id}`). Абстрактные классы — не создавать endpoint.

### 4.5 Servlet / Filter  (P2)

Аннотации: `@WebServlet(urlPatterns=…)` (есть как entrypoint) + `@WebFilter`/`@WebListener` →
entrypoints + HTTP-провайдер-контракты; `doGet/doPost/…` → `provides` к contract, 0.9. XML (§4.7):
`web.xml` `<servlet-mapping>` (class→url), `<filter-mapping>` (ordered filter-chain — моделировать
`references` с `order` meta). Servlet без `urlPatterns` → пропустить (abstract base).

### 4.6 SOAP / JAX-WS  (P2)

`@WebService(name=,serviceName=)` → contract `soap::<service>`; `@WebMethod` → `provides`, 0.9;
`@WebServiceRef` на клиенте → `consumes`. WSDL-first (`wsdlLocation`) → парсить WSDL если доступен
(§4.7). `@HandlerChain` — не контракт (метаданные трассировки).

### 4.7 Deployment descriptor parser  (P3 / confidence-upgrade)

Файлы: `WEB-INF/web.xml`, `META-INF/ejb-jar.xml`, `META-INF/application.xml`, `META-INF/beans.xml`,
`META-INF/persistence.xml`, vendor: `jboss-web.xml`, `ibm-web-bnd.xml`, `glassfish-web.xml`.

**Как ингестить (рекоменд.):** встроенный content-sniffed XML-экстрактор по образцу MyBatis
(`mybatis.go` `IsMyBatisMapper`-гейтинг). Добавить `javaee_xml.go`: снифф root-элемента/DOCTYPE
(`<beans>`/`<web-app>`/`<ejb-jar>`/`<persistence>`/`<definitions>`), эмит нод (`KindType` для
`<bean>`/`<servlet>`/`<message-driven>`, `KindArtifact` для дока) + рёбер. Без форка, fail-soft
(нода-файл при любой ошибке). Escape-hatch для команд: `index.extractor_plugins` (subprocess
JSON) / `fallback_chunkers` (regex) / `artifacts:` (text-scan).

**Правила:** парсинг XML **после** Java-индексации; XML мёржится с аннотациями, **XML имеет
приоритет** при конфликте (override); XML-рёбра — `origin` дескрипторного binding'а (0.9 для
явного `<servlet-mapping>`/enabled `@Alternative`; иначе на ~0.05 ниже аннотации).

⚠️ `beans.xml` **discovery-mode**: `bean-discovery-mode="annotated"` (дефолт Jakarta) vs `"all"`
меняет, считаются ли un-annotated классы бинами — парсить; дефолт `annotated`.

## 5. Граничные случаи / известные ловушки

- **`@Alternative`/`@Specializes`/`@Priority` инвертируют резолв** (ПРИНЯТО — D3): `@Alternative`
  бины по умолчанию **выключены** → исключать из видимых тиров; остался один не-alternative →
  видимый 0.7; ноль/N>1 → hidden (§7). `@Specializes` безусловно заменяет родителя → демотить
  родителя уже в Phase A. Полный резолв включённых альтернатив — Phase D (`beans.xml`
  `<alternatives>`).
- **`@Resource` перегружен** (DI vs JMS) (ПРИНЯТО — D2): приоритет **сигналов**, не типа —
  `lookup=`/`mappedName=` по JMS-JNDI-паттерну → JMS; иначе JMS API-тип (`jakarta/javax.jms.*`) →
  JMS; иначе `DataSource`/`EntityManager` → DI; голый нерезолвимый `@Resource` → abstain (не
  угадывать JMS). См. §4.2.
- **`Instance<T>` / `Provider<T>`** — lazy, несколько кандидатов → speculative.
- **`@EJB(beanName)` / `<ejb-link>`** — точный маппинг 0.9.
- **abstract-class инъекция** — через `EdgeExtends` (не `EdgeImplements`).
- **JNDI/строковые destination/lookup** — reuse const-deref + allow-list; неизвестное → 0.45.
- **`@Remote`** — может быть cross-JVM/вне графа.

## 6. Cross-repo

Provider↔consumer-спеки (DI/HTTP/JMS/SOAP) — через contract-matcher с общими id (workspace =
жёсткая граница, project = мягкая). **Structural `EdgeImplements`/`EdgeExtends` repo-gated
(`resolver.go:2411`) — НЕ cross-repo.** Поэтому cross-repo CDI/EJB только через
`di::<token>::<qualifier>` контракт (и consumer, и provider эмитят контракт). Не заявлять
implements как cross-repo.

## 7. Тиры неоднозначности (ПРИНЯТО — D1+D4)

Решающий факт: speculative-рёбра (`Meta[MetaSpeculative]=true`, `OriginSpeculative` 0.45)
**скрыты из всех дефолтных запросов** гейтом `FilterSpeculative(false)` (`query/subgraph.go:247`,
вызывается в `get_callers`/`find_usages`/`get_call_chain`/`api_impact`/`smart_context`,
`tools_core.go:1945–2209`); видны только при `include_speculative:true`. Прецедент —
`ResolveSpeculativeDispatch` (`speculative_dispatch.go`): эмитит все одноимённые кандидаты в
hidden-тир с `candidate_count` + капами (soft 12, hard 40).

Политика (N>1 impl без qualifier):
- эмитить **все** кандидаты в `OriginSpeculative` 0.45, `Meta[MetaSpeculative]=true`,
  `candidate_count` в meta, конфиденс `1/N` (floor 0.05, ceil 0.45), капы 12/40 (выше 40 — дроп);
- **промоутер:** ровно один кандидат в том же файле/пакете → его в видимый `OriginASTInferred`
  0.7 (как MyBatis `mybatis_calls.go:166`), остальных — в hidden;
- integrity-finding («add @Qualifier») — как дополнение, не замена.

Это precision-first для агента (дефолт скрывает) И recall для аудита (`include_speculative` /
`review --audience human`) одним эмитом. **D4: видимость — read-time; никакого index-флага и
вторых меток** (граф общий на сессии; `--audience` — review-слой `review/summary.go`). Единственная
доработка: `review --audience human` должен ставить `include_speculative:true` на чтения графа.

Прецеденс провайдеров: явный `@Produces`/`@Bean` > implicit managed-bean; qualifier override.

## 8. Приоритеты / фазы

| Приоритет | Resolver | Покрытие | Сложность | Тип |
|---|---|---|---|---|
| **P0** | EJB interface→impl | критично (связь слоёв) | средняя | synthesizer |
| **P0** | JMS producer→consumer | критично (processor=0 callers) | средняя | synthesizer |
| **P1** | CDI `@Inject`/`@Produces` | высокое (общая машинерия с EJB) | высокая | synthesizer |
| **P1** | REST endpoint + client | важно (HTTP→метод) | низкая | contract-enrich |
| **P2** | Servlet/Filter | legacy entrypoints | низкая | entrypoint+ordered |
| **P2** | SOAP/JAX-WS | мало в монолите | низкая | contract-enrich |
| **P3** | Deployment descriptor parser | upgrade до 0.9 + XML-only бины | средняя | XML-extractor |

Каждая фаза — атомарный PR: build + unit (extractor) + e2e (index→resolve fixture) + `analyze`
integrity-finding для неоднозначных/неразрешённых. Приземлять на inferred-тиры; XML апгрейдит 0.9.
(EJB legacy-skewed: если целевой корпус Quarkus/Spring — CDI и REST-client поднять выше EJB.)

## 9. Критерии приёмки + метрики

**P0 (обязательные):**
1. `get_callers(ChangePricePlanProcessor.processRequest)` ≥1 caller (через JMS-ноду).
2. `get_callers(NewOfferingService.retrieveAvailablePricePlans)` → impl `NewOfferingServicesEJB`, conf ≥0.8.
3. `contracts list` показывает REST endpoints (method + path).
4. `get_call_chain(VIPChangePricePlanBO.buildChangePricePlanRequest)` доходит до `processRequest` через JMS-ноду.

**P1:** SOAP/Servlet endpoints в `contracts list`; `api_impact` REST учитывает downstream.

| Метрика | До | P0 | P0+P1 |
|---|---|---|---|
| Callers `processRequest` | 0 | ≥2 | ≥2 |
| Contracts | 0 | ≥12 REST | ≥14 (REST+SOAP) |
| EJB interface→impl edges | 0 | ≥3 | ≥3 |
| `find_usages(MDB.onMessage)` | 0 | ≥1 | ≥1 |

## 10. Ограничения (не покрывается)

Reflection (`Class.forName`/`Method.invoke`), JNDI lookup строкой в runtime, dynamic proxy,
Spring XML `<bean class=>` (отдельный resolver), JMS message-selector фильтрация. Interceptor/
decorator chains, producer disposal — метаданные/порядок, не dispatch-рёбра (defer).

## 11. Принятые решения (с обоснованием)

1. **Неоднозначность (D1)** — все кандидаты в hidden-speculative + видимый same-package промоутер +
   integrity-finding (§7). *Почему:* hidden-by-default даёт precision агенту и recall аудиту одним
   эмитом; прецедент `ResolveSpeculativeDispatch`. Чистый «emit none» отвергнут — теряет бесплатный recall.
2. **`@Resource` (D2)** — приоритет сигналов (`lookup=`→тип→abstain), не type-first (§4.2/§5).
   *Почему:* экстрактор часто не резолвит тип → type-first fail-open; `lookup=` — локальный надёжный сигнал.
3. **`@Alternative`/`@Specializes` (D3)** — супрессия в Phase A (исключить выключенные альтернативы из
   видимых тиров; демотить родителя `@Specializes`); полный резолв — Phase D (`beans.xml`) (§5).
   *Почему:* видимое ребро в вытесненный бин — худший (инвертированный) случай; супрессия закрывает дыру дёшево.
4. **Audience-split (D4)** — read-time через `MetaSpeculative` + `include_speculative`; **без**
   index-флага и вторых меток (слит с D1) (§7). *Почему:* граф общий на сессии, переиндексация под
   audience прохибитивна; `--audience` живёт в review-слое.

⚠️ **Перед реализацией:** добавить `cdi.produces`/`cdi.managed` в switch `diContractFromEdge`
(`di_contracts.go:243-253`) — иначе неизвестный `binding` тихо дропается (`default: return zero,false`).
Опционально: `analyze kind=javaee_orphans` (зеркало `temporal_orphans`) для аудита hidden-рёбер.

## 12. Карта в код gortex (точки касания)

- Synthesizer: `internal/resolver/framework_synth.go` (интерфейс :31, реестр :114, `StampSynthesized` :70, `Synth*` :55-67).
- Образцы: `mybatis_calls.go`, `grpc_stub_calls.go`, `temporal_calls.go`.
- DI: `internal/indexer/di_contracts.go` (`diContractFromEdge` :243-253 ⚠ закрытый switch, `linkSpringBeans` :161, repo-scope :44/:122).
- Impl-резолв: `internal/resolver/resolver.go` (`inferImplements` :2293-2450; interface-only :2302; repo-gate :2411).
- Contracts: `internal/contracts/{contract.go,http.go,schema_enrich_java.go,matcher.go(:81 Cartesian),bind.go}`; reconcile `multi.go:2180`.
- XML-ингест: `internal/parser/languages/mybatis.go` (`IsMyBatisMapper`), `extractor_plugin.go`; `internal/config/config.go` (`ArtifactEntry`/`ExtractorPluginSpec`/`FallbackChunkerSpec`); `internal/artifacts/artifacts.go`.
- Allow-list: `internal/config/temporal_allowlist.go` (gate `GORTEX_ALLOW_LOCAL_*`, empty=off) → зеркалить `GORTEX_ALLOW_LOCAL_JAVAEE`, файл `.gortex/javaee.yaml`, лоадер в `internal/config`.
- Pipeline: `indexer.go` (synthesizer :743/:2491; contract-пасс :2444/:860).
- Edge/Node kinds: `internal/graph/edge.go` (Calls/Implements/Extends/Provides/Consumes/Matches/References/Annotated; origins :597-608), `node.go` (Method/Interface/Type/Field/Artifact).

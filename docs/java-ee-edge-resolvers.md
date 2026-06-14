# ТЗ: Java EE Edge Resolvers для статического графа вызовов

## 1. Проблема

Статический анализатор (tree-sitter) строит граф вызовов по прямым вызовам `a.b()`.
В Java EE монолитах компоненты связываются **декларативно** — через аннотации и конфигурацию,
а не через вызовы кода. Это создаёт слепые зоны в графе:

- JMS: `queueSender.send()` → MDB `onMessage()` — нет ребра
- EJB: `@Inject` интерфейс → `@Stateless` impl — нет ребра
- REST: `@GET @Path("/x")` → метод — нет ребра
- SOAP: `@WebService` → endpoint — нет ребра
- Servlet: `web.xml` / `@WebServlet` → класс — нет ребра
- CDI: `@Inject` → `@ApplicationScoped` bean — нет ребра

Для монолита ~240K нод это означает ~30% потерянных рёбер на архитектурных границах.

## 2. Цель

Дополнить индексер annotation-aware edge resolver'ами, которые создают рёбра
на основе Java EE аннотаций и дескрипторов развертывания. Рёбра должны быть
того же типа что и существующие (calls, implements, references), с пометкой
origin=`annotation_resolved` и confidence по шкале 0..1.

## 3. JMS Resolver

### 3.1 Входные данные

**Producer** (отправитель в очередь):
```java
@Inject JMSContext context;
@Inject @JMSConnectionFactory("jms/BSSJMSConnectionFactory")
private JMSContext jmsContext;

@Resource(lookup = "jms/BSSSingleChangePPQueue")
private Queue singleChangePPQueue;

// отправка
jmsContext.createProducer().send(singleChangePPQueue, message);
context.createProducer().send(queue, message);
```

**Consumer** (MDB — слушатель очереди):
```java
@MessageDriven(activationConfig = {
    @ActivationConfigProperty(propertyName = "destinationType", propertyValue = "javax.jms.Queue"),
    @ActivationConfigProperty(propertyName = "destination", propertyValue = "jms/BSSSingleChangePPQueue")
})
public class SingleChangePPQueueMDB implements MessageListener {
    public void onMessage(Message message) { ... }
}
```

**Deployment descriptor** (альтернатива аннотациям):
```xml
<message-driven>
    <ejb-name>SingleChangePPQueueMDB</ejb-name>
    <messaging-type>javax.jms.MessageListener</messaging-type>
    <activation-config>
        <activation-config-property>
            <activation-config-property-name>destination</activation-config-property-name>
            <activation-config-property-value>jms/BSSSingleChangePPQueue</activation-config-property-value>
        </activation-config-property>
    </activation-config>
</message-driven>
```

### 3.2 Правила создания рёбер

| Паттерн | Ребро | Confidence |
|---------|-------|------------|
| `@ActivationConfigProperty(destination=X)` на MDB → метод `onMessage()` | `calls` от MDB.onMessage к отправителю (если найден) | 0.7 |
| `@Resource(lookup=X)` типа Queue/Topic в классе A + `send()` | `spawns` от A.send → MDB.onMessage (если X совпадает с destination MDB) | 0.8 |
| `ejb-jar.xml` → `destination` + `destinationType` | аналогично аннотации | 0.7 |

### 3.3 Алгоритм

1. На этапе индексации: для каждого класса с `@MessageDriven` извлечь `destination` и `destinationType`
2. Создать synthetic-ноду `jms/queue/<destination>` (kind=queue)
3. Создать ребро `spawns` от `jms/queue/<destination>` → `MDB.onMessage()`
4. Для каждого `@Resource(lookup=X)` типа Queue/Topic: создать ребро `calls` от метода с `send()` → `jms/queue/<destination>`
5. Итого: `Sender.send() → jms/queue/X → MDB.onMessage()` — две вершины через промежуточную ноду очереди

### 3.4 Граничные случаи

- Destination задан через JNDI lookup строкой → извлечь строку
- Destination задан через `@Resource(lookup="java:/jms/queue/X")` → нормализовать JNDI-имя
- Topic vs Queue — различать по `destinationType`
- MDB без аннотаций (только ejb-jar.xml) → парсить XML

## 4. EJB Interface→Implementation Resolver

### 4.1 Входные данные

**Интерфейс**:
```java
@Local
public interface NewOfferingService {
    AvailableSocServiceDO retrieveAvailablePricePlans(...);
    SubscriberPricePlanInfoDO retrieveSubscriberPricePlanInfo(...);
}
```

**Реализация**:
```java
@Stateless
public class NewOfferingServicesEJB implements NewOfferingService {
    @Override
    public AvailableSocServiceDO retrieveAvailablePricePlans(...) { ... }
}
```

**Инъекция**:
```java
@EJB
private NewOfferingService offeringService;

// вызов
offeringService.retrieveAvailablePricePlans(...);
```

### 4.2 Правила создания рёбер

| Паттерн | Ребро | Confidence |
|---------|-------|------------|
| `class X implements Y` + `Y` имеет `@Local`/`@Remote` | `implements` от X к Y | 0.95 |
| `@EJB` инъекция интерфейса Y в класс A + вызов `y.method()` | `calls` от A.method() → X.method() (impl) | 0.85 |
| `@Inject` CDI инъекция интерфейса Y | `calls` от A.method() → X.method() (impl) | 0.7 |

### 4.3 Алгоритм

1. Собрать маппинг: интерфейс → список реализаций (по `implements` + `@Stateless`/`@Stateful`/`@Singleton`)
2. При встрече `@EJB`/`@Inject` типа-интерфейса: разрешить к конкретной реализации
3. Если реализация одна → confidence 0.85
4. Если реализаций несколько → создать рёбра ко всем с confidence 0.7 + пометить `speculative_dispatch`
5. Вызовы через интерфейсную ссылку: `interfaceRef.method()` → направить к impl.method() через `implements` ребро

### 4.4 Граничные случаи

- `@EJB(beanName="specificImpl")` → точный маппинг, confidence 0.95
- `@LocalBean` (no-interface view) → пропустить, обычный класс
- `@Remote` vs `@Local` — различать (remote = cross-JVM, может быть не в графе)
- `ejb-jar.xml` с `<ejb-link>` → точный маппинг

## 5. REST Endpoint Resolver (JAX-RS)

### 5.1 Входные данные

```java
@Path("/request")
public class RequestRS {
    @GET
    @Path("/changePricePlan")
    public Response changePricePlan(@QueryParam(...) ...) { ... }

    @PUT
    @Path("/changePricePlan")
    public Response changePricePlanCvg(@RequestBody ...) { ... }
}
```

Или на уровне метода:
```java
@GET
@Path("/info/pricePlan")
@Produces(MediaType.APPLICATION_JSON)
public Response getPricePlanInfo() { ... }
```

### 5.2 Правила создания рёбер

| Патattern | Ребро | Confidence |
|-----------|-------|------------|
| `@GET`/`@PUT`/`@POST`/`@DELETE` на методе | Создать synthetic-ноду `http://GET /request/changePricePlan` (kind=contract, type=http) | — |
| Метод с `@GET @Path(X)` | `provides` от метода к contract-ноде | 0.95 |
| Класс с `@Path(Y)` + метод с `@Path(X)` | URL = Y + X → contract-нода | 0.95 |
| `@Consumes`/`@Produces` | Записать media type в meta contract-ноды | — |

### 5.3 Алгоритм

1. При парсинге класса с `@Path` на уровне класса — запомнить basePath
2. Для каждого метода с HTTP-аннотацией (`@GET`, `@PUT`, `@POST`, `@DELETE`, `@PATCH`):
   - Вычислить полный URL: basePath + methodPath
   - Создать contract-ноду `http://<METHOD> <fullPath>`
   - Создать ребро `provides` от метода к contract-ноде
3. Contract-ноды обогащают `contracts list` и `api_impact`

### 5.4 Граничные случаи

- `@Path` с параметрами: `/request/{id}/changePricePlan` → сохранить как pattern
- `@Path("")` или отсутствие → basePath = ""
- Наследование: `@Path` на родителе + `@GET` на потомке → комбинировать
- Подклассы: `@Path` на абстрактном классе → не создавать endpoint

## 6. SOAP Web Service Resolver (JAX-WS)

### 6.1 Входные данные

```java
@WebService(name = "SubscriberInterface", serviceName = "SubscriberService")
@SOAPBinding(style = SOAPBinding.Style.DOCUMENT)
public class SubscriberWS {
    @WebMethod
    public ChangePPResponse changePP(@WebParam ChangePPRequest request) { ... }
}
```

### 6.2 Правила создания рёбер

| Паттерн | Ребро | Confidence |
|---------|-------|------------|
| `@WebService` на классе | Создать contract-ноду `ws://<serviceName>` (kind=contract, type=ws) | — |
| `@WebMethod` | `provides` от метода к contract-ноде | 0.9 |
| `@WebService(name=X)` | Записать portType=X в meta | — |

### 6.3 Граничные случаи

- `@WebServiceRef` на клиенте → создать `consumes` ребро к contract-ноде
- WSDL-first подход (wsdlLocation в аннотации) → парсить WSDL если доступен
- `@HandlerChain` — обработчики не являются частью контракта, но могут быть полезны для трассировки

## 7. Servlet Resolver

### 7.1 Входные данные

**Аннотация**:
```java
@WebServlet(urlPatterns = {"/servlet/ivrRequest", "/servlet/uivr_moscow"})
public class IvrServlet extends HttpServlet {
    @Override
    protected void doPost(HttpServletRequest req, HttpServletResponse resp) { ... }
}
```

**web.xml**:
```xml
<servlet>
    <servlet-name>UssdServlet</servlet-name>
    <servlet-class>com.amdocs.uss.rp.servlet.UssdServlet</servlet-class>
</servlet>
<servlet-mapping>
    <servlet-name>UssdServlet</servlet-name>
    <url-pattern>/servlet/ussdRequest</url-pattern>
</servlet-mapping>
```

### 7.2 Правила создания рёбер

| Паттерн | Ребро | Confidence |
|---------|-------|------------|
| `@WebServlet(urlPatterns=X)` на классе | Создать contract-ноду `http://POST <urlPattern>` (kind=contract, type=http) | 0.9 |
| `<servlet-class>` + `<url-pattern>` в web.xml | Аналогично | 0.9 |
| `doGet`/`doPost`/`doPut`/`doDelete` | `provides` от метода к contract-ноде | 0.9 |

### 7.3 Граничные случаи

- Filter (`@WebFilter`) → аналогично servlet, но с `urlPatterns` без HTTP method
- Servlet без `urlPatterns` → пропустить (abstract base servlet)
- `HttpRequestHandler` (Spring) → другая аннотация, отдельный resolver

## 8. CDI Resolver

### 8.1 Входные данные

```java
@ApplicationScoped
public class B2CTariffConnector {
    public void changePricePlan(...) { ... }
}

// В другом классе
@Inject
private B2CTariffConnector b2cTariffConnector;

// или через интерфейс
@Inject
private TariffConnector tariffConnector; // → B2CTariffConnector (единственный impl)
```

### 8.2 Правила создания рёбер

| Паттерн | Ребро | Confidence |
|---------|-------|------------|
| `@Inject` конкретного класса | `calls` от точки инъекции к impl (трассировка вызовов) | 0.85 |
| `@Inject` интерфейса + единственный impl | `calls` от точки инъекции к impl | 0.8 |
| `@Inject` интерфейса + несколько impl | `calls` ко всем impl с `speculative_dispatch` | 0.6 |
| `@Named("beanName")` + `@Inject` | Точный маппинг | 0.9 |
| `@Produces` на методе/поле | `provides` от producer к типу | 0.85 |

### 8.3 Граничные случаи

- `@Any`, `@Default` — квалификаторы по умолчанию
- `Instance<T>` — lazy resolution, несколько кандидатов
- `Provider<T>` — аналогично
- `@Alternative` — активная альтернатива зависит от beans.xml

## 9. Deployment Descriptor Parser

### 9.1 Поддерживаемые файлы

| Файл | 内容 |
|------|------|
| `WEB-INF/web.xml` | Servlet, Filter, Listener mappings |
| `META-INF/ejb-jar.xml` | EJB definitions, MDB destinations |
| `META-INF/application.xml` | EAR module definitions |
| `WEB-INF/jboss-web.xml` | JBoss-specific servlet config |
| `WEB-INF/ibm-web-bnd.xml` | WAS-specific bindings |
| `WEB-INF/glassfish-web.xml` | GF-specific config |

### 9.2 Правила

- Парсинг XML идёт **после** Java-индексации
- XML-данные мёржатся с аннотациями (XML имеет приоритет при конфликте — переопределение)
- Созданные через XML рёбра получают origin=`xml_resolved` с confidence на 0.05 ниже аннотаций

## 10. Общая архитектура resolver'ов

### 10.1 Pipeline

```
Java source files
       │
       ▼
  tree-sitter parse ──→ AST nodes + structural edges (ast_resolved)
       │
       ▼
  annotation scanner ──→ synthetic nodes (contracts, queues) + annotation edges
       │
       ▼
  deployment descriptor parser ──→ merge/override edges (xml_resolved)
       │
       ▼
  LSP enrichment ──→ upgrade edges to lsp_resolved where possible
```

### 10.2 Synthetic nodes

Resolver'ы создают **synthetic-ноды** для элементов которые не являются Java-классами:

| Kind | Пример | Описание |
|------|--------|----------|
| `queue` | `jms/BSSSingleChangePPQueue` | JMS queue/topic |
| `contract` | `http://GET /request/changePricePlan` | HTTP endpoint |
| `contract` | `ws://SubscriberService` | SOAP endpoint |
| `topic` | `jms/pricePlanChanged` | Pub/sub topic |

Synthetic-ноды обогащают существующие Gortex-инструменты:
- `contracts list` → показывает HTTP/SOAP endpoints
- `get_callers(MDB.onMessage)` → показывает producer'ов через queue-ноду
- `api_impact` → учитывает synthetic-ноды

### 10.3 Edge origin classification

| Origin | Confidence | Описание |
|--------|-----------|----------|
| `lsp_resolved` | 1.0 | LSP подтвердил |
| `ast_resolved` | 0.92 | Tree-sitter нашёл прямой вызов |
| `annotation_resolved` | 0.7..0.9 | Выведен из аннотации (JMS, EJB, REST) |
| `xml_resolved` | 0.65..0.85 | Выведен из deployment descriptor |
| `text_matched` | 0 | Эвристика по имени (существующий) |

## 11. Приоритеты реализации

| Приоритет | Resolver | Покрытие дыр | Сложность |
|-----------|----------|--------------|-----------|
| **P0** | EJB interface→impl | Критично: без него нет связи между слоями | Средняя |
| **P0** | JMS producer→consumer | Критично: без него processor = 0 callers | Средняя |
| **P1** | REST endpoint resolver | Важно: без этого нет связи HTTP→метод | Низкая |
| **P1** | SOAP endpoint resolver | Средне: мало WS в монолите | Низкая |
| **P2** | Servlet resolver | Средне: legacy entry points | Низкая |
| **P2** | CDI resolver | Полезно, но CDI-вызовы частично резолвятся через implements | Высокая |
| **P3** | Deployment descriptor parser | Улучшает покрытие для проектов с XML-конфигурацией | Средняя |

## 12. Критерии приёмки

### P0 (обязательные)

1. `get_callers(ChangePricePlanProcessor.processRequest)` возвращает ≥1 caller (через JMS queue synthetic-ноду)
2. `get_callers(NewOfferingService.retrieveAvailablePricePlans)` возвращает impl (NewOfferingServicesEJB) с confidence ≥0.8
3. `contracts list` показывает REST endpoints с HTTP method + path
4. `get_call_chain(VIPChangePricePlanBO.buildChangePricePlanRequest)` доходит до `ChangePricePlanProcessor.processRequest` через JMS synthetic-ноду

### P1 (желательные)

5. SOAP endpoints видны в `contracts list`
6. Servlet entry points видны в `contracts list`
7. `api_impact` для REST endpoint учитывает downstream callers

### Измеримые метрики

| Метрика | До | После (P0) | После (P0+P1) |
|---------|-----|------------|---------------|
| Callers для `processRequest` | 0 | ≥2 (Single+Mass MDB) | ≥2 |
| Contracts обнаружено | 0 | ≥12 REST endpoints | ≥14 (REST+SOAP) |
| EJB interface→impl edges | 0 | ≥3 ключевых | ≥3 |
| `find_usages(MDB.onMessage)` | 0 | ≥1 (через queue) | ≥1 |

## 13. Ограничения (не покрывается)

- **Reflection**: `Class.forName()`, `Method.invoke()` — невозможно статически
- **JNDI lookup** строкой: `new InitialContext().lookup("jms/X")` — требует runtime-данных
- **Spring XML config**: `<bean class="...">` — отдельный resolver
- **Dynamic proxy**: `Proxy.newProxyInstance()` — runtime
- **Message selector**: JMS selector на MDB фильтрует сообщения — не учитывается

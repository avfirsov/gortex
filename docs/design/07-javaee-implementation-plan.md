# Java EE edge resolvers — implementation plan

> Buildable execution plan derived from the spec `docs/design/06-javaee-synthesizer.md`
> (which itself merges `docs/java-ee-edge-resolvers.md` + gortex-integration design + review).
> Each task is atomic, lands a PR, and ships with build + unit (extractor) + e2e
> (index→resolve fixture) + an `analyze` integrity surface. Decisions D1–D4 are settled in §11
> of the spec and assumed here.

Legend: `[ ]` task · **(file)** primary touch-point · → produced edge/tier.

---

## Phase 0 — Shared infrastructure (prerequisite for A/C; do first)

- [ ] **0.1 Synthesizer skeleton + registration.** Add `SynthCDI`, `SynthEJB`, `SynthJMS` to the
  `Synth*` const block and register `synthFunc{name, fn}` in `defaultFrameworkSynthesizers()`
  **(internal/resolver/framework_synth.go:55-67,114)**. No-op bodies first (green build).
- [ ] **0.2 Annotation capture.** Confirm/extend the Java extractor to emit `EdgeAnnotated` +
  per-annotation args into edge/node `Meta` for the Java EE annotation set **(internal/parser/
  languages/java.go `javaCollectAnnotations` ~885)**. Needed args: `@Named`/`@Qualifier` value,
  `@ActivationConfigProperty`, `@Resource(lookup=,name=,mappedName=)`, `@Path`, `@EJB(beanName=)`,
  `@WebServlet(urlPatterns=)`, `@WebService(name=,serviceName=)`.
- [ ] **0.3 Subtype index helper (SHARED, repo-scoped).** `subtypeIndex(g, repo)` mapping a type
  → concrete impls via **both** `EdgeImplements` (interfaces) **and** `EdgeExtends` (abstract
  classes). Repo-scope with `GetRepoNodes`/`GetRepoEdges` for perf. Consumed by EJB + CDI.
  *Why both edges:* `InferImplements` is interface-only **(resolver.go:2302)** so abstract-class
  injection needs `EdgeExtends`.
- [ ] **0.4 di_contracts switch (BLOCKER GOTCHA).** Add `cdi.produces` and `cdi.managed` `binding`
  cases to `diContractFromEdge` **(internal/indexer/di_contracts.go:243-253)** — the closed
  `default: return zero,false` silently drops unknown bindings (will look like "resolver emits
  nothing"). Contract id `di::<token>::<qualifier>`.
- [ ] **0.5 Speculative-emit helper.** Reuse `ResolveSpeculativeDispatch`'s shape
  **(internal/resolver/speculative_dispatch.go)**: emit all N candidates at `OriginSpeculative`
  0.45, `Meta[MetaSpeculative]=true`, `candidate_count`, conf `1/N` (floor 0.05, ceil 0.45),
  fanout caps (soft 12, hard 40 → drop). Same-file/same-package promoter → one visible 0.7 edge.
- [ ] **0.6 Config allow-list.** `internal/config/javaee.go` mirroring `temporal_allowlist.go`:
  gate `GORTEX_ALLOW_LOCAL_JAVAEE`, file `.gortex/javaee.yaml`, **empty=off**. Keys:
  `cdi_producers`, `cdi_qualifiers`, `jms_destinations`.
- [ ] **0.7 e2e harness.** Fixture helper (clone `internal/indexer/temporal_e2e_test.go` shape):
  write Java sources to tmp → index → assert edges/tiers.
- [ ] **0.8 `analyze kind=javaee_orphans`.** Mirror `temporal_orphans` **(internal/resolver/
  temporal_orphans.go + internal/mcp/tools_analyze_temporal.go)**: surface ambiguous injections,
  unresolved `@Inject`, MDB with no sender, JMS sender with no listener.

---

## Phase A — EJB + CDI  (P0/P1, synthesizer, code-only) — highest ROI

- [ ] **A1 Extract managed beans + injection points.** Tag `@Stateless/@Stateful/@Singleton`
  (EJB) and `@ApplicationScoped/@RequestScoped/@Dependent/@SessionScoped` (CDI) classes as
  providers of their own type + implemented/extended types. Emit `EdgeConsumes {via, di_token,
  qualifiers}` for `@Inject`/`@EJB` points; `EdgeProvides {binding:"cdi.produces"/"cdi.managed",
  provides_for}` for `@Produces`/managed beans. **(java.go)**
- [ ] **A2 EJB resolver** `ResolveEJBInjection` **(internal/resolver/ejb_calls.go new)**: for each
  `@EJB`/`@Inject` of an interface → subtypeIndex (0.3). Unique impl → `EdgeCalls` 0.85 visible;
  `@EJB(beanName=X)`/`<ejb-link>` → 0.9; N>1 → speculative (0.5); `@LocalBean` → skip;
  `@Remote` → tag (may be out-of-graph).
- [ ] **A3 CDI resolver** `ResolveCDIInjection` **(internal/resolver/cdi_calls.go new)**: producer
  index = `@Produces` ∪ managed beans ∪ subtypeIndex. `@Named`/`@Qualifier` → 0.9; unique → 0.8
  (iface) / 0.85 (concrete); N>1 → speculative + same-package promoter.
- [ ] **A4 `@Alternative`/`@Specializes` suppression (D3).** Tag alternatives; exclude from visible
  tiers (they're disabled until beans.xml, Phase D); `@Specializes` → demote parent in index.
- [ ] **A5 Cross-repo DI.** Consumer + provider both emit `di::<token>::<qualifier>` contract so
  the matcher pairs across repos (structural subtype edges are repo-gated, resolver.go:2411).
- [ ] **A6 Tests + acceptance.** e2e: `@EJB NewOfferingService` → `NewOfferingServicesEJB` impl,
  conf ≥0.8 (spec acceptance #2); `@Inject` interface w/ single impl resolves; N>1 → hidden;
  `@Named` exact. Integrity rows for ambiguous.

---

## Phase C — JMS producer→consumer  (P0, synthesizer + synthetic node) — parallel to A

- [ ] **C1 Extract listeners.** `@MessageDriven` + `@ActivationConfigProperty(destination=,
  destinationType=)`, Spring `@JmsListener`, `MessageListener.onMessage`. Normalise JNDI dest. **(java.go)**
- [ ] **C2 Extract senders.** `jmsContext.createProducer().send(dest,…)`,
  `jmsTemplate.convertAndSend("q",…)`. Resolve `dest` via **D2 signal-priority**: `@Resource(lookup=)`
  JMS-JNDI pattern → JMS; JMS API type → JMS; else const-deref; else abstain.
- [ ] **C3 Resolver** `ResolveJMSDispatch` **(internal/resolver/jms_calls.go new)**: synthetic
  `jms::<dest>` node (kind `queue`/`topic`); `spawns` → MDB.onMessage; `calls` sender →
  `jms::<dest>`. Two-hop `Sender.send() → jms::X → MDB.onMessage()`. Tier 0.8 literal/const, 0.45 heuristic.
- [ ] **C4 Tests + acceptance.** e2e: `get_callers(processRequest)` ≥1 via JMS node (acceptance #1);
  `get_call_chain` reaches across the queue (acceptance #4); Queue vs Topic by `destinationType`.

---

## Phase B — REST (JAX-RS + Spring MVC) + Servlet  (P1, contract-enrichment) — independent

- [ ] **B1 Route contracts.** Concat class `@Path` + method `@Path` (+ JAX-RS `@ApplicationPath`),
  emit provider contract `http::<METHOD>::<normalized-path>` **(internal/contracts/
  schema_enrich_java.go, http.go)**. Path params → pattern (`/req/{id}`). Abstract class → no endpoint.
- [ ] **B2 Client consumers.** JAX-RS `client.target(..).path(..).request().get()`, Spring
  `RestTemplate`/`WebClient`/`@FeignClient` → consumer contracts. Pair via existing HTTP matcher.
- [ ] **B3 Servlet/Filter.** `@WebServlet(urlPatterns=)` (+ `@WebFilter`/`@WebListener`) → entrypoints
  + provider contracts; `doGet/doPost/…` → `provides` 0.9. **(internal/entrypoints/entrypoints.go)**
- [ ] **B4 Tests + acceptance.** `contracts list` shows REST endpoints w/ method+path (acceptance #3);
  `api_impact` counts downstream.

---

## Phase D — Deployment descriptors  (P3, confidence-upgrade; completes D3)

- [ ] **D1 Built-in XML extractor** `internal/parser/languages/javaee_xml.go` — content-sniff
  (root/DOCTYPE) per MyBatis precedent **(mybatis.go `IsMyBatisMapper`)**: `beans.xml`, `web.xml`,
  `ejb-jar.xml`, `persistence.xml`, `application.xml` (+ vendor `jboss-web.xml`/`ibm-web-bnd.xml`/
  `glassfish-web.xml`). Emit nodes/edges; fail-soft (file node on error). Parse once at extract.
- [ ] **D2 beans.xml `<alternatives>` + discovery-mode.** Enabled alternative → promote to visible/0.9,
  demote default (finishes D3). `bean-discovery-mode` default `annotated`.
- [ ] **D3 web.xml mappings.** `<servlet-mapping>` class→url; `<filter-mapping>` ordered chain
  (`references` + `order` meta). `ejb-jar.xml` `<message-driven>` dest (XML-only MDBs); `<ejb-link>` exact.
- [ ] **D4 Override merge.** XML overrides annotations on conflict; XML-confirmed binding → 0.9.

---

## Phase E — SOAP / JAX-WS  (P2, lowest priority)

- [ ] **E1 Endpoints.** `@WebService(name=,serviceName=)` → contract `soap::<service>::<op>`;
  `@WebMethod` → `provides` 0.9; `@WebServiceRef` → `consumes`.
- [ ] **E2 WSDL ingest.** `*.wsdl` via artifacts / extractor to enumerate operations + bind clients.

---

## Sequencing & dependencies

```
Phase 0 ──┬─► Phase A (EJB → CDI; CDI reuses 0.3/0.4/0.5/A5)
          ├─► Phase C (JMS; independent — parallel with A)        ◄ P0 corpus priority: A(EJB)+C(JMS) first
          └─► Phase B (REST/Servlet; independent — contracts)
Phase A/B/C ──► Phase D (XML upgrades A/B/C to 0.9, completes @Alternative) ──► Phase E
```

P0 (spec): EJB interface→impl (A2) + JMS (C) — without them processor `callers=0`. CDI (A3) P1.
If target corpus is Quarkus/Spring rather than legacy EJB, raise CDI + REST-client above EJB.

## Cross-cutting invariants

- **Visibility (D1/D4):** every multi-candidate resolution → hidden `MetaSpeculative`; visibility is
  read-time (`include_speculative` / `review --audience human`). No index-time audience flag.
- **Perf:** repo-scope all indexes; parse XML once at extract, resolve from meta in the pass
  (synthesizers full-recompute every settle — framework_synth.go:18).
- **Tiers:** 0.9 descriptor/exact; 0.7–0.85 annotation+unique-subtype; 0.45 hidden speculative.
- **PR discipline:** one PR per task-group, build + unit + e2e + integrity finding, land inferred-tier.

## Risk gates (verify before/at implementation)

1. **diContractFromEdge switch** (0.4) — edit or CDI contracts silently vanish.
2. **InferImplements interface-only + repo-gated** (resolver.go:2302,2411) → EdgeExtends (0.3) + di:: cross-repo (A5).
3. **@Resource overload** (D2) → signal-priority ladder, abstain branch mandatory.
4. **LSP/jdtls prerequisite** — accurate Java type/interface resolution (used by subtype matching &
   `@Inject` type targets) is materially better with jdtls. The Windows `file://` URI fix
   (PR #92 / `internal/lspuri`) is a prerequisite for jdtls-grade Java resolution on Windows; land
   it first so Java EE resolvers aren't fighting a degraded base graph.

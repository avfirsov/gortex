'use client'

import { useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { useInspector } from '@/lib/inspector'
import { CONTRACTS, GUARDS, SYMBOLS } from '@/lib/seed'

type FlowStep = {
  idx: number
  repo: string
  where: string
  what: string
  caveat?: string
  risk?: boolean
}

const FLOW_STEPS: FlowStep[] = [
  { idx: 1,  repo: 'web',          where: 'pages/email.tsx:sendEmail',                what: 'POST /ingest/email with raw payload' },
  { idx: 2,  repo: 'core-api',     where: 'internal/http/handler.go:RegisterRoutes',  what: 'route → EmailIngestHandler' },
  { idx: 3,  repo: 'core-api',     where: 'internal/middleware/auth.go:Authn',        what: 'auth check · hot path', caveat: 'hot' },
  { idx: 4,  repo: 'core-api',     where: 'internal/ingest/email.go:EmailIngestHandler', what: 'parse headers + body' },
  { idx: 5,  repo: 'core-api',     where: 'internal/link/extractor.go:ExtractLinks',  what: 'find URLs', caveat: 'deprecated' },
  { idx: 6,  repo: 'core-api',     where: 'internal/store/postgres.go:Insert',        what: 'write inbound email row', risk: true },
  { idx: 7,  repo: 'core-api',     where: 'internal/events/publish.go:Emit',          what: 'publish link.Extracted' },
  { idx: 8,  repo: 'email-worker', where: 'internal/handler.go:OnExtracted',          what: 'fetch preview metadata' },
  { idx: 9,  repo: 'worker',       where: 'internal/notifier.go:PushTuckUpdated',     what: 'emit push.TuckUpdated' },
  { idx: 10, repo: 'tuck_app',     where: 'features/sync/listener.dart:onPush',       what: 'client sync receives update' },
]

export function InvestigationView() {
  const setSym = useInspector((s) => s.setSym)
  const [stepIdx, setStepIdx] = useState(3)

  return (
    <>
      <div className="page-hd">
        <div>
          <div className="hstack" style={{ gap: 8, marginBottom: 4 }}>
            <Icon name="flask" size={14} />
            <span className="mono faint" style={{ fontSize: 11 }}>investigation</span>
            <span className="chip" style={{ color: 'var(--warn)' }}>open · 2d old</span>
          </div>
          <h1>Email ingest returns 500 intermittently</h1>
          <div className="sub">
            Cross-repo trace · web → core-api → email-worker → worker → tuck_app · pinned by @sam
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="copy" size={12} /> Copy as report
          </button>
          <button type="button" className="btn ghost">
            <Icon name="share" size={12} /> Share link
          </button>
          <button type="button" className="btn primary">
            <Icon name="pin" size={12} /> Pin finding
          </button>
        </div>
      </div>
      <div style={{ overflow: 'auto', flex: 1 }}>
        <div className="inv-grid">
          {/* 1 — flow */}
          <div className="inv-tile inv-c-8">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="route" size={12} />
              <span className="ti">Request flow</span>
              <span className="meta">10 steps · 5 repos · 1.8s p50 · 4.1s p95</span>
              <span className="close"><Icon name="close" size={11} /></span>
            </div>
            <div className="tile-bd">
              {FLOW_STEPS.map((s, i) => {
                let hop: React.ReactNode = null
                if (i > 0 && FLOW_STEPS[i - 1].repo !== s.repo) {
                  hop = (
                    <div className="repo-hop">
                      <Icon name="arrowr" size={10} /> crosses {FLOW_STEPS[i - 1].repo} → {s.repo}
                    </div>
                  )
                }
                return (
                  <div key={s.idx}>
                    {hop}
                    <div
                      className={`flow-step ${s.risk ? 'risk' : ''}`}
                      style={{
                        background: stepIdx === s.idx ? 'var(--accent-soft)' : 'transparent',
                        borderRadius: 4,
                        cursor: 'pointer',
                      }}
                      onClick={() => setStepIdx(s.idx)}
                    >
                      <div className="idx">
                        <span className="no">{s.idx}</span>
                      </div>
                      <div className="body">
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                          <span className="repo-tag">{s.repo}</span>
                          <span className="where">{s.where}</span>
                          {s.caveat && <CaveatBadge kind={s.caveat} />}
                          {s.risk && <CaveatBadge kind="risk" />}
                        </div>
                        <div className="what">{s.what}</div>
                      </div>
                      <button
                        type="button"
                        className="btn small ghost"
                        onClick={(e) => {
                          e.stopPropagation()
                          setSym(SYMBOLS[0])
                        }}
                      >
                        open
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          </div>

          {/* 2 — blast radius */}
          <div className="inv-tile inv-c-4">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="fork" size={12} />
              <span className="ti">Blast radius</span>
              <span className="meta">ExtractLinks</span>
            </div>
            <div className="tile-bd">
              <div style={{ display: 'grid', gap: 8 }}>
                {[
                  { label: 'Direct callers',     v: 5,  c: 'var(--accent)' },
                  { label: 'Transitive',          v: 28, c: 'var(--k-function)' },
                  { label: 'Processes touched',   v: 4,  c: 'var(--k-type)' },
                  { label: 'Tests exercising',    v: 2,  c: 'var(--warn)' },
                  { label: 'Owner gaps',          v: 1,  c: 'var(--violet)' },
                ].map((r) => (
                  <div
                    key={r.label}
                    style={{ display: 'grid', gridTemplateColumns: '1fr 28px', alignItems: 'center', gap: 10, fontSize: 12 }}
                  >
                    <div>
                      <div style={{ marginBottom: 4 }}>{r.label}</div>
                      <div className="meter">
                        <span style={{ width: `${Math.min(100, r.v * 4)}%`, background: r.c }} />
                      </div>
                    </div>
                    <div className="mono" style={{ textAlign: 'right' }}>{r.v}</div>
                  </div>
                ))}
              </div>
              <hr style={{ border: 0, borderTop: '1px solid var(--line-1)', margin: '12px 0' }} />
              <div style={{ fontSize: 11.5, color: 'var(--fg-2)' }}>
                Removing <span className="mono" style={{ color: 'var(--fg-0)' }}>ExtractLinks</span> would break ingest in core-api and downstream listener in email-worker.
              </div>
            </div>
          </div>

          {/* 3 — source peek */}
          <div className="inv-tile inv-c-6">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="file" size={12} />
              <span className="ti">Source · step {stepIdx}</span>
              <span className="meta mono">internal/middleware/auth.go</span>
            </div>
            <div className="tile-bd">
              <pre className="code" style={{ margin: 0 }}>
                <span className="ln">28</span> <span className="cm">{`// Authn validates the bearer token on every request.`}</span>{'\n'}
                <span className="ln">29</span> <span className="cm">{`// Note: this is on the hot path — 83% of incidents touch it.`}</span>{'\n'}
                <span className="ln">30</span> <span className="kw">func</span> <span className="fn">Authn</span>(next <span className="ty">http.Handler</span>) <span className="ty">http.Handler</span> {'{'}{'\n'}
                <span className="ln">31</span>   <span className="kw">return</span> <span className="ty">http.HandlerFunc</span>(<span className="kw">func</span>(w <span className="ty">http.ResponseWriter</span>, r *<span className="ty">http.Request</span>) {'{'}{'\n'}
                <span className="ln">32</span>     tok := r.Header.<span className="fn">Get</span>(<span className="str">{'"Authorization"'}</span>){'\n'}
                <span className="ln">33</span>     <span className="kw">if</span> tok == <span className="str">{'""'}</span> {'{'}{'\n'}
                <span className="ln">34</span>       <span className="fn">writeError</span>(w, 401, <span className="str">{'"missing token"'}</span>){'\n'}
                <span className="ln">35</span>       <span className="kw">return</span>{'\n'}
                <span className="ln">36</span>     {'}'}{'\n'}
                <span className="ln">37</span>     claims, err := <span className="fn">verifyJWT</span>(tok){'\n'}
                <span className="ln">38</span>     <span className="kw">if</span> err != <span className="kw">nil</span> {'{'}{'\n'}
                <span className="ln">39</span>       <span className="fn">writeError</span>(w, 401, err.<span className="fn">Error</span>()){'\n'}
                <span className="ln">40</span>       <span className="kw">return</span>{'\n'}
                <span className="ln">41</span>     {'}'}{'\n'}
              </pre>
            </div>
          </div>

          {/* 4 — history */}
          <div className="inv-tile inv-c-6">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="history" size={12} />
              <span className="ti">Recent edits on this path</span>
              <span className="meta">last 14d</span>
            </div>
            <div className="tile-bd">
              {[
                { t: '2d ago',  who: '@sam',  msg: 'move verifyJWT to middleware/auth',        hash: 'c41bd2a' },
                { t: '3d ago',  who: '@ira',  msg: 'add TTL cache to verifyJWT',               hash: '98a12e1' },
                { t: '5d ago',  who: '@sam',  msg: 'ExtractLinks: normalize UTM stripping',    hash: '1e904fa' },
                { t: '9d ago',  who: '@mike', msg: 'rename InboundEmail → IngressEmail',        hash: 'fe7729c', risk: true },
                { t: '12d ago', who: '@ira',  msg: 'add boundary guard: web ↛ core-api/internal', hash: '4b2cc1f' },
              ].map((c, i) => (
                <div
                  key={i}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '60px 1fr 70px',
                    alignItems: 'center',
                    gap: 10,
                    padding: '8px 0',
                    borderBottom: '1px dashed var(--line-1)',
                    fontSize: 12,
                  }}
                >
                  <span className="mono faint" style={{ fontSize: 11 }}>{c.t}</span>
                  <div>
                    <div style={{ marginBottom: 2 }}>{c.msg}</div>
                    <div className="mono faint" style={{ fontSize: 10.5 }}>
                      {c.who} · <span style={{ color: 'var(--fg-3)' }}>{c.hash}</span>
                      {c.risk && <span style={{ marginLeft: 8, color: 'var(--warn)' }}>· guard warn</span>}
                    </div>
                  </div>
                  <span className="tag-dim" style={{ textAlign: 'center' }}>open</span>
                </div>
              ))}
            </div>
          </div>

          {/* 5 — contracts */}
          <div className="inv-tile inv-c-6">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="plug" size={12} />
              <span className="ti">Contracts on this flow</span>
              <span className="meta">3 REST · 2 EVENT</span>
            </div>
            <div className="tile-bd" style={{ padding: 0 }}>
              <table className="tbl">
                <thead>
                  <tr>
                    <th>Contract</th>
                    <th>Type</th>
                    <th>Consumers</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {CONTRACTS.slice(0, 5).map((c) => (
                    <tr key={c.id}>
                      <td className="mono-cell">{c.name}</td>
                      <td>
                        <span className="tag-dim">{c.kind}</span>
                      </td>
                      <td>
                        <div className="hstack" style={{ gap: 4, flexWrap: 'wrap' }}>
                          {c.consumers.map((r, i) => (
                            <span key={i} className="tag-dim">{r}</span>
                          ))}
                        </div>
                      </td>
                      <td>
                        {c.breaking ? (
                          <CaveatBadge kind="boundary" />
                        ) : (
                          <span className="chip" style={{ color: 'var(--ok)' }}>{c.version}</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          {/* 6 — guards */}
          <div className="inv-tile inv-c-6">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="beaker" size={12} />
              <span className="ti">Guard rules</span>
              <span className="meta">2 violated</span>
            </div>
            <div className="tile-bd" style={{ padding: 0 }}>
              <table className="tbl">
                <thead>
                  <tr>
                    <th>Rule</th>
                    <th>Kind</th>
                    <th>Scope</th>
                    <th className="num">Hits</th>
                  </tr>
                </thead>
                <tbody>
                  {GUARDS.map((g) => (
                    <tr key={g.id}>
                      <td className="mono-cell">{g.name}</td>
                      <td>
                        <span className="tag-dim">{g.kind}</span>
                      </td>
                      <td className="mono-cell faint">{g.scope}</td>
                      <td className="num">
                        {g.status === 'violated' && <span className="cav risk">{g.hits}</span>}
                        {g.status === 'warn' && <span className="cav deprecated">{g.hits}</span>}
                        {g.status === 'ok' && <span className="faint">0</span>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          {/* 7 — notes */}
          <div className="inv-tile inv-c-12">
            <div className="tile-hd">
              <span className="grip">⋮⋮</span>
              <Icon name="file" size={12} />
              <span className="ti">Notes</span>
              <span className="meta">3 authors</span>
            </div>
            <div className="tile-bd" style={{ fontSize: 12.5, lineHeight: 1.6 }}>
              <p style={{ margin: '0 0 8px' }}>
                <b>Hypothesis</b> — 500s correlate with recent <span className="mono">verifyJWT</span> cache TTL drop from 5m → 30s.
                Combined with <span className="mono">ExtractLinks</span> running synchronously on the request path, auth cache misses pile up when the link extractor backs off.
              </p>
              <p style={{ margin: '0 0 8px' }}>
                <b>Next</b> — (1) move link extraction to worker via <span className="mono">link.Extracted</span>; (2) bump cache TTL back; (3) add boundary guard so UI can&apos;t import internal again.
              </p>
              <div className="hstack" style={{ marginTop: 8, gap: 6 }}>
                <button type="button" className="btn small">
                  <Icon name="plus" size={11} /> Add note
                </button>
                <button type="button" className="btn small ghost">
                  <Icon name="owner" size={11} /> Assign
                </button>
                <button type="button" className="btn small ghost">
                  <Icon name="bolt" size={11} /> Convert to PR plan
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

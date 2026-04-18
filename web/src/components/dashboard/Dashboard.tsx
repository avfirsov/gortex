'use client'

import { useMemo } from 'react'
import { useRouter } from 'next/navigation'
import { Icon } from '@/components/primitives/Icon'
import { Sparkline, KindRing, HBar, StackedBar } from '@/components/primitives/Charts'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { useInspector } from '@/lib/inspector'
import {
  STATS, REPOS, LANGUAGES, NODE_KINDS, SYMBOLS, ACTIVITY, CAVEATS, PROCESSES, FAKE_SPARK,
  type Repo,
} from '@/lib/seed'

function Kpi({
  label, value, delta, deltaClass, spark,
}: {
  label: string
  value: string
  delta?: string
  deltaClass?: string
  spark?: number[]
}) {
  return (
    <div className="kpi">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {delta && <div className={`delta ${deltaClass ?? ''}`}>{delta}</div>}
      {spark && (
        <div className="spark">
          <Sparkline data={spark} w={72} h={22} stroke="var(--accent)" fill="var(--accent)" />
        </div>
      )}
    </div>
  )
}

function RepoCard({ r, pinned, onPick }: { r: Repo; pinned?: boolean; onPick?: () => void }) {
  const kinds = [
    { label: 'functions',  value: r.funcs,      color: 'var(--k-function)' },
    { label: 'methods',    value: r.methods,    color: 'var(--k-method)' },
    { label: 'types',      value: r.types,      color: 'var(--k-type)' },
    { label: 'interfaces', value: r.interfaces, color: 'var(--k-interface)' },
    { label: 'variables',  value: r.vars,       color: 'var(--k-variable)' },
  ]
  return (
    <div className={`repo-card ${pinned ? 'pinned' : ''}`} onClick={onPick}>
      <div className="repo-hd">
        <span style={{ background: r.color, width: 6, height: 18, borderRadius: 2, display: 'inline-block' }} />
        <div>
          <div className="repo-name">{r.id}</div>
          <div className="repo-owner mono">{r.owner}/{r.id}</div>
        </div>
        <div className="repo-stats">
          <div>{r.nodes.toLocaleString()} nodes</div>
          <div style={{ color: 'var(--fg-3)' }}>{r.edges.toLocaleString()} edges</div>
        </div>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 10, fontSize: 11 }}>
        {kinds.map((k) => (
          <div key={k.label} className="hstack" style={{ gap: 4 }}>
            <span className="swatch" style={{ background: k.color }} />
            <span className="mono" style={{ color: 'var(--fg-1)' }}>{k.value}</span>
            <span className="faint">{k.label}</span>
          </div>
        ))}
      </div>
      <div className="kind-bar">
        <div style={{ flex: r.funcs,      background: 'var(--k-function)' }} />
        <div style={{ flex: r.methods,    background: 'var(--k-method)' }} />
        <div style={{ flex: r.types,      background: 'var(--k-type)' }} />
        <div style={{ flex: r.interfaces, background: 'var(--k-interface)' }} />
        <div style={{ flex: r.vars,       background: 'var(--k-variable)' }} />
      </div>
      <div style={{ marginTop: 10, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div className="mono faint" style={{ fontSize: 10.5 }}>
          {r.files} files · {r.lang}
        </div>
        <Sparkline
          data={FAKE_SPARK[r.id] ?? [1, 2, 3, 4, 5]}
          stroke={r.color}
          fill={r.color}
          w={56}
          h={14}
        />
      </div>
    </div>
  )
}

function GraphPulse() {
  const nodes = useMemo(() => {
    const arr: { x: number; y: number; size: number; color: string }[] = []
    const seed = (n: number) => Math.abs((Math.sin(n * 12.9898) * 43758.5453) % 1)
    for (let i = 0; i < 64; i++) {
      const r = 60 + seed(i) * 90
      const t = (i / 64) * Math.PI * 2 + seed(i + 1)
      arr.push({
        x: 200 + Math.cos(t) * r + (seed(i + 2) - 0.5) * 20,
        y: 110 + Math.sin(t) * r * 0.65 + (seed(i + 3) - 0.5) * 15,
        size: 2 + seed(i + 4) * 4,
        color: ['var(--k-function)', 'var(--k-method)', 'var(--k-type)', 'var(--k-interface)', 'var(--k-variable)'][i % 5],
      })
    }
    return arr
  }, [])
  return (
    <svg viewBox="0 0 400 220" width="100%" height="220" style={{ display: 'block' }}>
      <defs>
        <radialGradient id="pulse-glow" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.35" />
          <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="200" cy="110" r="90" fill="url(#pulse-glow)" />
      {nodes.map((n, i) =>
        nodes.slice(i + 1, i + 3).map((m, j) => (
          <line key={`${i}-${j}`} x1={n.x} y1={n.y} x2={m.x} y2={m.y} stroke="var(--line-2)" strokeWidth="0.4" opacity="0.45" />
        )),
      )}
      {nodes.map((n, i) => (
        <circle key={i} cx={n.x} cy={n.y} r={n.size} fill={n.color} opacity="0.9" />
      ))}
    </svg>
  )
}

function ActivityFeed() {
  return (
    <div className="vstack" style={{ gap: 0 }}>
      {ACTIVITY.map((a, i) => (
        <div
          key={i}
          style={{
            display: 'grid',
            gridTemplateColumns: '56px 16px 1fr',
            alignItems: 'start',
            gap: 8,
            padding: '7px 0',
            borderBottom: i < ACTIVITY.length - 1 ? '1px dashed var(--line-1)' : 'none',
            fontSize: 12,
          }}
        >
          <span className="mono faint" style={{ fontSize: 11 }}>{a.t}</span>
          <span
            style={{
              color: a.kind === 'warn' ? 'var(--warn)' : a.kind === 'ok' ? 'var(--ok)' : 'var(--fg-2)',
              marginTop: 2,
            }}
          >
            <Icon name={a.kind === 'warn' ? 'warn' : a.kind === 'ok' ? 'check' : 'dot'} size={12} />
          </span>
          <span>
            <span className="mono" style={{ color: 'var(--fg-2)', marginRight: 6 }}>{a.actor}</span>
            <span>{a.msg}</span>
          </span>
        </div>
      ))}
    </div>
  )
}

function CaveatsPreview({ onOpen }: { onOpen: () => void }) {
  return (
    <div className="vstack" style={{ gap: 6 }}>
      {CAVEATS.slice(0, 5).map((c) => (
        <div
          key={c.id}
          style={{
            display: 'grid',
            gridTemplateColumns: '96px 1fr auto',
            alignItems: 'center',
            gap: 10,
            padding: '8px 10px',
            border: '1px solid var(--line-1)',
            borderRadius: 6,
            background: 'var(--bg-1)',
            cursor: 'pointer',
          }}
          onClick={onOpen}
        >
          <CaveatBadge kind={c.severity} />
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 12.5 }}>{c.title}</div>
            <div className="mono faint nowrap" style={{ fontSize: 11 }}>{c.symbol}</div>
          </div>
          <div className="mono faint" style={{ fontSize: 11, textAlign: 'right' }}>
            <div>{c.owner}</div>
            <div style={{ color: 'var(--fg-3)' }}>{c.age}</div>
          </div>
        </div>
      ))}
    </div>
  )
}

function ProcessPreview({ onOpen }: { onOpen: () => void }) {
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>Flow</th>
          <th>Repos</th>
          <th className="num">Steps</th>
          <th className="num">Score</th>
        </tr>
      </thead>
      <tbody>
        {PROCESSES.slice(0, 6).map((p) => (
          <tr key={p.id} onClick={onOpen} style={{ cursor: 'pointer' }}>
            <td>
              <div className="hstack" style={{ gap: 6 }}>
                <span
                  className={`cav ${p.risk === 'risk' ? 'risk' : p.risk === 'warn' ? 'deprecated' : ''}`}
                  style={{ opacity: p.risk === 'ok' ? 0 : 1 }}
                >
                  {p.risk}
                </span>
                <span className="mono">{p.name}</span>
              </div>
              <div className="mono faint nowrap" style={{ fontSize: 10.5, marginTop: 2 }}>{p.entry}</div>
            </td>
            <td>
              <div className="hstack" style={{ gap: 4, flexWrap: 'wrap' }}>
                {p.crosses.map((r, i) => (
                  <span key={i} style={{ display: 'contents' }}>
                    {i > 0 && <span className="faint mono" style={{ fontSize: 10 }}>→</span>}
                    <span className="tag-dim">{r}</span>
                  </span>
                ))}
              </div>
            </td>
            <td className="num">{p.steps}</td>
            <td className="num">{p.score}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export function Dashboard() {
  const router = useRouter()
  const setSym = useInspector((s) => s.setSym)

  const langRows = LANGUAGES.slice(0, 8).map((l) => ({ label: l.name, value: l.bytes, color: l.color }))
  const kindRows = NODE_KINDS.map((k) => ({ label: k.name, value: k.count, color: k.color, display: k.count.toLocaleString() }))
  const langSegs = LANGUAGES.map((l) => ({ value: l.bytes, color: l.color, label: l.name }))

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Control Room</h1>
          <div className="sub">
            Gortex knowledge graph · {STATS.reposIndexed} repos · indexed {STATS.uptimeMinutes}m ago · last change 41s ago
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="history" size={12} /> Index history
          </button>
          <button type="button" className="btn">
            <Icon name="bolt" size={12} /> Re-index
          </button>
          <button type="button" className="btn primary" onClick={() => router.push('/investigations')}>
            <Icon name="flask" size={12} /> New investigation
          </button>
        </div>
      </div>

      <div style={{ overflowY: 'auto', flex: 1 }}>
        <div className="kpi-row" style={{ paddingTop: 14 }}>
          <Kpi label="Nodes"  value={STATS.totalNodes.toLocaleString()} delta="+62 today"           deltaClass="up"   spark={[10, 11, 12, 12, 13, 13, 13, 14]} />
          <Kpi label="Edges"  value={STATS.totalEdges.toLocaleString()} delta="+318 today"          deltaClass="up"   spark={[40, 45, 52, 58, 62, 68, 70, 72]} />
          <Kpi label="Caveats"value={STATS.caveats.toString()}          delta="3 new · 1 critical" deltaClass="down" spark={[30, 32, 34, 36, 40, 41, 42, 42]} />
          <Kpi label="Blast radius (avg)" value="5.2×"                  delta="↑ 0.3 vs last week" deltaClass="up"   spark={[3, 3.5, 3.8, 4, 4.3, 4.8, 5, 5.2]} />
        </div>

        <div className="hero-grid">
          {/* Hero left — graph pulse + quick actions */}
          <div className="card" style={{ gridRow: '1 / span 2' }}>
            <div className="card-hd">
              <div className="hstack" style={{ gap: 8, flexWrap: 'wrap' }}>
                <span className="ti">Knowledge graph</span>
                <span className="chip"><span className="swatch sw-function" /> 48k fn</span>
                <span className="chip"><span className="swatch sw-type" /> 8k types</span>
                <span className="chip"><span className="swatch sw-interface" /> 412 ifaces</span>
              </div>
              <button type="button" className="btn small ghost" onClick={() => router.push('/graph')}>
                <Icon name="expand" size={11} /> Open Graph
              </button>
            </div>
            <GraphPulse />
            <div className="card-bd" style={{ paddingTop: 4 }}>
              <div className="legend">
                {NODE_KINDS.slice(0, 8).map((k) => (
                  <span key={k.name} className="lg">
                    <span className="swatch" style={{ background: k.color }} /> {k.name}{' '}
                    <span className="mono faint">{k.count.toLocaleString()}</span>
                  </span>
                ))}
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8, marginTop: 14 }}>
                <button type="button" className="btn" onClick={() => router.push('/processes')}>
                  <Icon name="route" size={12} /> Trace a flow
                </button>
                <button type="button" className="btn" onClick={() => router.push('/graph')}>
                  <Icon name="fork" size={12} /> Blast radius
                </button>
                <button type="button" className="btn" onClick={() => router.push('/contracts')}>
                  <Icon name="plug" size={12} /> Check contracts
                </button>
              </div>
            </div>
          </div>

          {/* Hero right — node kinds donut */}
          <div className="card">
            <div className="card-hd">
              <span className="ti">Node kinds</span>
              <span className="mono faint" style={{ fontSize: 11 }}>{STATS.totalNodes.toLocaleString()} total</span>
            </div>
            <div className="card-bd" style={{ display: 'grid', gridTemplateColumns: '220px 1fr', gap: 16, alignItems: 'center' }}>
              <KindRing
                segments={NODE_KINDS.map((k) => ({ value: k.count, color: `var(${k.cssVar})` }))}
                innerLabel="nodes"
                innerValue={`${(STATS.totalNodes / 1000).toFixed(1)}k`}
              />
              <HBar rows={kindRows} />
            </div>
          </div>

          {/* Languages */}
          <div className="card">
            <div className="card-hd">
              <span className="ti">Languages</span>
              <span className="mono faint" style={{ fontSize: 11 }}>12 detected</span>
            </div>
            <div className="card-bd">
              <div style={{ marginBottom: 10 }}>
                <StackedBar parts={langSegs} height={6} />
              </div>
              <HBar rows={langRows} />
            </div>
          </div>

          {/* Caveats */}
          <div className="card wide">
            <div className="card-hd">
              <div className="hstack" style={{ gap: 8, flexWrap: 'wrap' }}>
                <span className="ti">Caveats &amp; landmines</span>
                <span className="chip" style={{ color: 'var(--danger)' }}>2 critical</span>
                <span className="chip" style={{ color: 'var(--warn)' }}>7 warn</span>
                <span className="chip faint">33 other</span>
              </div>
              <div className="hstack">
                <span className="mono faint" style={{ fontSize: 11 }}>sorted by severity · owner</span>
                <button type="button" className="btn small ghost" onClick={() => router.push('/caveats')}>
                  <Icon name="expand" size={11} /> View all
                </button>
              </div>
            </div>
            <div className="card-bd">
              <CaveatsPreview onOpen={() => router.push('/caveats')} />
            </div>
          </div>

          {/* Processes */}
          <div className="card wide">
            <div className="card-hd">
              <span className="ti">Top processes</span>
              <button type="button" className="btn small ghost" onClick={() => router.push('/processes')}>
                <Icon name="expand" size={11} /> All {STATS.processes}
              </button>
            </div>
            <ProcessPreview onOpen={() => router.push('/processes')} />
          </div>

          {/* Repositories + Activity */}
          <div className="card wide" style={{ padding: 0 }}>
            <div className="card-hd">
              <span className="ti">Repositories</span>
              <div className="hstack" style={{ gap: 8 }}>
                <span className="mono faint" style={{ fontSize: 11 }}>{STATS.reposIndexed} indexed · federated view</span>
                <button type="button" className="btn small ghost">
                  <Icon name="plus" size={11} /> Add repo
                </button>
              </div>
            </div>
            <div className="card-bd" style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 14 }}>
              <div className="repo-grid">
                {REPOS.slice(0, 6).map((r, i) => (
                  <RepoCard key={r.id} r={r} pinned={i < 2} onPick={() => setSym(SYMBOLS[0])} />
                ))}
              </div>
              <div className="card" style={{ background: 'var(--bg-1)' }}>
                <div className="card-hd">
                  <span className="ti">Activity</span>
                  <span className="mono faint" style={{ fontSize: 11 }}>last 24h</span>
                </div>
                <div className="card-bd" style={{ paddingTop: 4 }}>
                  <ActivityFeed />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

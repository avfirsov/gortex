'use client'

import { useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { useTweaks } from '@/lib/tweaks'
import { REPOS, NODE_KINDS, STATS } from '@/lib/seed'
import { GraphConstellation } from './views/Constellation'
import { GraphHierarchical } from './views/Hierarchical'
import { GraphSankey } from './views/Sankey'
import { Graph3D } from './views/Graph3D'

type Mode = 'constellation' | 'tree' | 'sankey' | '3d'

function RepoFilterPanel({
  repos,
  onToggle,
  onOnly,
}: {
  repos: Set<string>
  onToggle: (id: string) => void
  onOnly: (id: string) => void
}) {
  return (
    <div>
      <div
        className="sec-ti"
        style={{ fontSize: 10.5, textTransform: 'uppercase', letterSpacing: '0.08em', color: 'var(--fg-3)', marginBottom: 8 }}
      >
        Repositories
      </div>
      <div className="vstack" style={{ gap: 4 }}>
        {REPOS.map((r) => (
          <div key={r.id} className="hstack" style={{ gap: 8, padding: '3px 0' }}>
            <input
              type="checkbox"
              checked={repos.has(r.id)}
              onChange={() => onToggle(r.id)}
              aria-label={`Toggle ${r.id}`}
            />
            <span className="swatch" style={{ background: r.color }} />
            <span className="mono" style={{ fontSize: 11.5, flex: 1 }}>{r.id}</span>
            <span className="mono faint" style={{ fontSize: 10.5 }}>{r.nodes}</span>
            <button type="button" className="btn small ghost" onClick={() => onOnly(r.id)} style={{ padding: '0 4px' }}>
              only
            </button>
          </div>
        ))}
      </div>
      <div
        className="sec-ti"
        style={{ fontSize: 10.5, textTransform: 'uppercase', letterSpacing: '0.08em', color: 'var(--fg-3)', margin: '14px 0 8px' }}
      >
        Node kinds
      </div>
      <div className="vstack" style={{ gap: 4 }}>
        {NODE_KINDS.map((k) => (
          <label key={k.name} className="hstack" style={{ gap: 8, padding: '3px 0', fontSize: 11.5 }}>
            <input type="checkbox" defaultChecked />
            <span className="swatch" style={{ background: k.color }} />
            <span className="mono" style={{ flex: 1 }}>{k.name}</span>
            <span className="mono faint" style={{ fontSize: 10.5 }}>{k.count.toLocaleString()}</span>
          </label>
        ))}
      </div>
      <div
        className="sec-ti"
        style={{ fontSize: 10.5, textTransform: 'uppercase', letterSpacing: '0.08em', color: 'var(--fg-3)', margin: '14px 0 8px' }}
      >
        Caveats layer
      </div>
      <div className="vstack" style={{ gap: 4 }}>
        {(['deprecated', 'risk', 'hot', 'unowned', 'cycle', 'boundary'] as const).map((c) => (
          <label key={c} className="hstack" style={{ gap: 8, padding: '3px 0', fontSize: 11.5 }}>
            <input type="checkbox" defaultChecked={c === 'hot' || c === 'risk'} />
            <CaveatBadge kind={c} />
          </label>
        ))}
      </div>
      <div
        className="sec-ti"
        style={{ fontSize: 10.5, textTransform: 'uppercase', letterSpacing: '0.08em', color: 'var(--fg-3)', margin: '14px 0 8px' }}
      >
        Layout
      </div>
      <div className="vstack" style={{ gap: 6 }}>
        <button type="button" className="btn">
          <Icon name="fit" size={12} /> Fit to screen
        </button>
        <button type="button" className="btn">
          <Icon name="history" size={12} /> Re-layout
        </button>
      </div>
    </div>
  )
}

export function GraphView() {
  const showMinimap = useTweaks((s) => s.showMinimap)
  const [mode, setMode] = useState<Mode>('constellation')
  const [repos, setRepos] = useState<Set<string>>(() => new Set(REPOS.map((r) => r.id)))

  const toggle = (id: string) => {
    const n = new Set(repos)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    setRepos(n)
  }
  const only = (id: string) => setRepos(new Set([id]))

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Graph explorer</h1>
          <div className="sub">
            {repos.size} of {REPOS.length} repos · {STATS.totalNodes.toLocaleString()} nodes ·{' '}
            {STATS.totalEdges.toLocaleString()} edges
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="copy" size={12} /> Copy cypher
          </button>
          <button type="button" className="btn ghost">
            <Icon name="save" size={12} /> Save view
          </button>
          <button type="button" className="btn">
            <Icon name="share" size={12} /> Share
          </button>
        </div>
      </div>
      <div className="graph-wrap">
        <div className="graph-side">
          <RepoFilterPanel repos={repos} onToggle={toggle} onOnly={only} />
        </div>
        <div className="graph-canvas">
          <div className="graph-toolbar">
            <div className="seg">
              <button type="button" className={mode === 'constellation' ? 'active' : ''} onClick={() => setMode('constellation')}>
                <Icon name="graph" size={12} /> Constellation
              </button>
              <button type="button" className={mode === 'tree' ? 'active' : ''} onClick={() => setMode('tree')}>
                <Icon name="layers" size={12} /> Hierarchy
              </button>
              <button type="button" className={mode === 'sankey' ? 'active' : ''} onClick={() => setMode('sankey')}>
                <Icon name="sankey" size={12} /> Sankey
              </button>
              <button type="button" className={mode === '3d' ? 'active' : ''} onClick={() => setMode('3d')}>
                <Icon name="cube" size={12} /> 3D
              </button>
            </div>
            <div className="hstack" style={{ gap: 6 }}>
              <div className="seg">
                <button type="button" aria-label="Zoom in"><Icon name="zoomin" size={12} /></button>
                <button type="button" aria-label="Zoom out"><Icon name="zoomout" size={12} /></button>
                <button type="button" aria-label="Fit"><Icon name="fit" size={12} /></button>
              </div>
              <button type="button" className="btn small">
                <Icon name="filter" size={12} /> Focus
              </button>
            </div>
          </div>

          <div style={{ width: '100%', height: '100%' }}>
            {mode === 'constellation' && <GraphConstellation filterRepos={repos} />}
            {mode === 'tree' && <GraphHierarchical />}
            {mode === 'sankey' && <GraphSankey />}
            {mode === '3d' && <Graph3D />}
          </div>

          <div className="legend-box">
            <div className="hstack" style={{ gap: 6 }}><span className="swatch sw-function" /> function</div>
            <div className="hstack" style={{ gap: 6 }}><span className="swatch sw-type" /> type</div>
            <div className="hstack" style={{ gap: 6 }}><span className="swatch sw-interface" /> interface</div>
            <div className="hstack" style={{ gap: 6 }}><span className="swatch sw-method" /> method</div>
          </div>

          {showMinimap && (
            <div className="minimap">
              <svg viewBox="0 0 180 110" width="100%" height="100%">
                <rect x="0" y="0" width="180" height="110" fill="var(--bg-1)" />
                {REPOS.map((r, i) => (
                  <circle
                    key={r.id}
                    cx={20 + (i % 4) * 45}
                    cy={20 + Math.floor(i / 4) * 35}
                    r={2 + Math.log(r.nodes) * 0.8}
                    fill={r.color}
                    opacity="0.85"
                  />
                ))}
                <rect x="60" y="30" width="60" height="45" fill="none" stroke="var(--accent)" strokeWidth="1" rx="3" />
              </svg>
            </div>
          )}
        </div>
      </div>
    </>
  )
}

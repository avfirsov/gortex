'use client'

import { Icon } from '@/components/primitives/Icon'
import { Sparkline, Meter } from '@/components/primitives/Charts'
import { COMMUNITIES, REPOS, STATS } from '@/lib/seed'

export function CommunitiesView() {
  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Communities</h1>
          <div className="sub">
            {STATS.communities} modules detected · modularity 60% · tight groups = clear modules
          </div>
        </div>
      </div>
      <div
        style={{
          padding: 18,
          overflow: 'auto',
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))',
          gap: 10,
        }}
      >
        {COMMUNITIES.map((c) => (
          <div
            key={c.id}
            className="card"
            style={{ padding: 14, display: 'grid', gridTemplateColumns: '1fr 180px', gap: 16, alignItems: 'center' }}
          >
            <div>
              <div className="hstack" style={{ gap: 6 }}>
                <Icon name="caret" size={10} />
                <span className="mono" style={{ fontSize: 14, color: 'var(--fg-0)' }}>{c.name}</span>
                <span className="tag-dim">{c.repo}</span>
              </div>
              <div className="hstack" style={{ gap: 8, marginTop: 8, fontSize: 11.5, color: 'var(--fg-2)' }}>
                <span><Icon name="users" size={11} /> {c.symbols} symbols</span>
                <span><Icon name="file" size={11} /> {c.files} files</span>
                <span
                  style={{
                    color: c.growth.startsWith('+') ? 'var(--ok)' : c.growth.startsWith('-') ? 'var(--danger)' : 'var(--fg-2)',
                  }}
                >
                  {c.growth} size
                </span>
              </div>
              <div style={{ marginTop: 8 }}>
                <div className="hstack" style={{ justifyContent: 'space-between', fontSize: 11, color: 'var(--fg-2)' }}>
                  <span>cohesion</span>
                  <span className="mono">{(c.cohesion * 100).toFixed(0)}%</span>
                </div>
                <Meter
                  value={c.cohesion * 100}
                  color={c.cohesion > 0.75 ? 'var(--ok)' : c.cohesion > 0.55 ? 'var(--warn)' : 'var(--danger)'}
                />
              </div>
            </div>
            <div style={{ opacity: 0.8 }}>
              <Sparkline
                data={[3, 4, 5, 5, 6, 7, 6, 8, 9, 10, 10, 11]}
                stroke={REPOS.find((r) => r.id === c.repo)?.color || 'var(--accent)'}
                fill={REPOS.find((r) => r.id === c.repo)?.color || 'var(--accent)'}
                w={180}
                h={40}
              />
            </div>
          </div>
        ))}
      </div>
    </>
  )
}

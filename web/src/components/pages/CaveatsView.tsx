'use client'

import { useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { CAVEATS } from '@/lib/seed'

const TABS = ['all', 'risk', 'deprecated', 'hot', 'unowned', 'cycle', 'boundary'] as const
type Tab = (typeof TABS)[number]

export function CaveatsView() {
  const [tab, setTab] = useState<Tab>('all')
  const filtered = tab === 'all' ? CAVEATS : CAVEATS.filter((c) => c.severity === tab)

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Caveats</h1>
          <div className="sub">Landmines in the codebase · severity-ranked · 3 new this week</div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="owner" size={12} /> Group by owner
          </button>
          <button type="button" className="btn">
            <Icon name="bolt" size={12} /> Triage session
          </button>
        </div>
      </div>
      <div style={{ padding: '14px 22px 0', borderBottom: '1px solid var(--line-1)' }}>
        <div className="seg" style={{ height: 30, flexWrap: 'wrap' }}>
          {TABS.map((c) => (
            <button
              key={c}
              type="button"
              className={tab === c ? 'active' : ''}
              onClick={() => setTab(c)}
              style={{ textTransform: 'capitalize' }}
            >
              {c}{' '}
              <span className="mono faint" style={{ marginLeft: 6 }}>
                {c === 'all' ? CAVEATS.length : CAVEATS.filter((x) => x.severity === c).length}
              </span>
            </button>
          ))}
        </div>
      </div>
      <div style={{ padding: 18, overflow: 'auto', display: 'grid', gap: 8 }}>
        {filtered.map((c) => (
          <div
            key={c.id}
            className="card"
            style={{ display: 'grid', gridTemplateColumns: '120px 1fr auto', gap: 14, padding: 14, alignItems: 'start' }}
          >
            <div>
              <CaveatBadge kind={c.severity} />
            </div>
            <div>
              <div style={{ fontSize: 13.5, color: 'var(--fg-0)' }}>{c.title}</div>
              <div className="mono faint" style={{ fontSize: 11, marginTop: 2 }}>{c.symbol}</div>
              <div style={{ fontSize: 12, color: 'var(--fg-1)', marginTop: 8 }}>{c.desc}</div>
            </div>
            <div style={{ textAlign: 'right', fontSize: 11 }}>
              <div className="hstack" style={{ justifyContent: 'flex-end', gap: 6 }}>
                <Icon name="owner" size={11} />
                <span className="mono">{c.owner}</span>
              </div>
              <div className="faint mono" style={{ marginTop: 4 }}>{c.age}</div>
              <div className="hstack" style={{ gap: 4, marginTop: 10, justifyContent: 'flex-end' }}>
                <button type="button" className="btn small ghost">Snooze</button>
                <button type="button" className="btn small">Open</button>
              </div>
            </div>
          </div>
        ))}
      </div>
    </>
  )
}

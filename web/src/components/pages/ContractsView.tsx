'use client'

import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { CONTRACTS } from '@/lib/seed'

export function ContractsView() {
  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Contracts</h1>
          <div className="sub">{CONTRACTS.length} API/event boundaries · 2 pending breaking changes</div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="filter" size={12} /> REST · EVENT · URL
          </button>
          <button type="button" className="btn">
            <Icon name="plus" size={12} /> New contract check
          </button>
        </div>
      </div>
      <div style={{ padding: '18px 22px', overflow: 'auto' }}>
        <div style={{ display: 'grid', gap: 10 }}>
          {CONTRACTS.map((c) => (
            <div key={c.id} className="card">
              <div
                style={{
                  display: 'grid',
                  gridTemplateColumns: '28px 1fr auto',
                  gap: 14,
                  padding: 14,
                  alignItems: 'center',
                }}
              >
                <div
                  style={{
                    width: 28,
                    height: 28,
                    borderRadius: 6,
                    background:
                      c.kind === 'EVENT'
                        ? 'oklch(0.78 0.14 300 / 0.18)'
                        : c.kind === 'URL'
                        ? 'oklch(0.82 0.15 80 / 0.18)'
                        : 'oklch(0.82 0.14 45 / 0.18)',
                    color: c.kind === 'EVENT' ? 'var(--violet)' : c.kind === 'URL' ? 'var(--warn)' : 'var(--k-contract)',
                    display: 'grid',
                    placeItems: 'center',
                    fontFamily: 'JetBrains Mono',
                    fontSize: 10,
                    fontWeight: 600,
                  }}
                >
                  {c.kind === 'EVENT' ? 'EV' : c.kind === 'URL' ? 'URL' : 'API'}
                </div>
                <div>
                  <div className="hstack" style={{ gap: 8, flexWrap: 'wrap' }}>
                    <span className="mono" style={{ fontSize: 14, color: 'var(--fg-0)' }}>{c.name}</span>
                    {c.breaking && <CaveatBadge kind="boundary" />}
                    <span className="chip">{c.version}</span>
                  </div>
                  <div className="hstack" style={{ gap: 10, marginTop: 6, fontSize: 11.5, color: 'var(--fg-2)', flexWrap: 'wrap' }}>
                    <span>
                      Produced by <span className="tag-dim">{c.producer}</span>
                    </span>
                    <span>→</span>
                    <span className="hstack" style={{ gap: 4 }}>
                      consumed by{' '}
                      {c.consumers.map((r) => (
                        <span key={r} className="tag-dim">{r}</span>
                      ))}
                    </span>
                    <span className="faint">
                      · {c.callers} call sites · updated {c.last}
                    </span>
                  </div>
                </div>
                <div className="hstack" style={{ gap: 6 }}>
                  <button type="button" className="btn small ghost">
                    <Icon name="graph" size={11} /> Trace
                  </button>
                  <button type="button" className="btn small">
                    <Icon name="file" size={11} /> Schema
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}

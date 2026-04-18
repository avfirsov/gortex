'use client'

import { StackedBar } from '@/components/primitives/Charts'
import { REPOS, STATS } from '@/lib/seed'

export function ServicesView() {
  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Services</h1>
          <div className="sub">{STATS.reposIndexed} indexed services · click to drill in</div>
        </div>
      </div>
      <div style={{ padding: 18, overflow: 'auto' }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 10 }}>
          {REPOS.map((r) => (
            <div key={r.id} className="card" style={{ padding: 14 }}>
              <div className="hstack" style={{ gap: 8 }}>
                <span style={{ width: 8, height: 28, borderRadius: 3, background: r.color }} />
                <div>
                  <div className="mono" style={{ fontSize: 14, color: 'var(--fg-0)' }}>{r.id}</div>
                  <div className="mono faint" style={{ fontSize: 11 }}>
                    {r.owner}/{r.id} · {r.lang}
                  </div>
                </div>
                <div style={{ marginLeft: 'auto', textAlign: 'right' }}>
                  <div className="mono" style={{ fontSize: 12 }}>{r.nodes.toLocaleString()}</div>
                  <div className="mono faint" style={{ fontSize: 10.5 }}>nodes</div>
                </div>
              </div>
              <div style={{ marginTop: 12 }}>
                <StackedBar
                  parts={[
                    { value: r.funcs,      color: 'var(--k-function)' },
                    { value: r.methods,    color: 'var(--k-method)' },
                    { value: r.types,      color: 'var(--k-type)' },
                    { value: r.interfaces, color: 'var(--k-interface)' },
                    { value: r.vars,       color: 'var(--k-variable)' },
                  ]}
                  height={5}
                />
              </div>
              <div className="hstack" style={{ gap: 10, marginTop: 10, fontSize: 11, color: 'var(--fg-2)', flexWrap: 'wrap' }}>
                <span>{r.funcs} fn</span>
                <span>{r.methods} meth</span>
                <span>{r.types} ty</span>
                <span>{r.interfaces} iface</span>
              </div>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}

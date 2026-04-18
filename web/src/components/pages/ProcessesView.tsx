'use client'

import { useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { useInspector } from '@/lib/inspector'
import { PROCESSES, REPOS, STATS, SYMBOLS } from '@/lib/seed'

export function ProcessesView() {
  const setSym = useInspector((s) => s.setSym)
  const [sel, setSel] = useState(PROCESSES[0].id)

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Processes</h1>
          <div className="sub">
            {STATS.processes} execution flows discovered across {STATS.reposIndexed} repos
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="filter" size={12} /> Filter
          </button>
          <button type="button" className="btn">
            <Icon name="save" size={12} /> Save query
          </button>
        </div>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1.5fr 1fr', flex: 1, minHeight: 0 }}>
        <div style={{ overflow: 'auto', borderRight: '1px solid var(--line-1)' }}>
          <table className="tbl">
            <thead>
              <tr>
                <th />
                <th>Flow</th>
                <th>Repos touched</th>
                <th className="num">Steps</th>
                <th className="num">Files</th>
                <th className="num">Score</th>
              </tr>
            </thead>
            <tbody>
              {PROCESSES.map((p) => (
                <tr
                  key={p.id}
                  onClick={() => setSel(p.id)}
                  className={sel === p.id ? 'active' : ''}
                  style={{ cursor: 'pointer' }}
                >
                  <td style={{ width: 26, textAlign: 'center' }}>
                    <span
                      style={{
                        width: 6,
                        height: 6,
                        borderRadius: 50,
                        display: 'inline-block',
                        background:
                          p.risk === 'risk' ? 'var(--danger)' : p.risk === 'warn' ? 'var(--warn)' : 'var(--ok)',
                      }}
                    />
                  </td>
                  <td>
                    <div className="mono" style={{ color: 'var(--fg-0)' }}>{p.name}</div>
                    <div className="mono faint nowrap" style={{ fontSize: 10.5 }}>{p.entry}</div>
                  </td>
                  <td>
                    <div className="hstack" style={{ gap: 4, flexWrap: 'wrap' }}>
                      {p.crosses.map((r, i) => (
                        <span key={i} style={{ display: 'contents' }}>
                          {i > 0 && <span className="faint mono">→</span>}
                          <span className="tag-dim">{r}</span>
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className="num">{p.steps}</td>
                  <td className="num">{p.files}</td>
                  <td className="num">{p.score}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div style={{ padding: 18, overflow: 'auto', background: 'var(--bg-1)' }}>
          {(() => {
            const p = PROCESSES.find((x) => x.id === sel) ?? PROCESSES[0]
            return (
              <div>
                <div
                  className="mono faint"
                  style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.08em' }}
                >
                  flow
                </div>
                <div style={{ fontSize: 18, fontWeight: 500, marginTop: 4 }}>{p.name}</div>
                <div className="mono faint" style={{ fontSize: 11, marginTop: 4 }}>{p.entry}</div>

                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 8, marginTop: 14 }}>
                  <div className="card">
                    <div className="card-bd">
                      <div className="mono faint" style={{ fontSize: 10.5 }}>STEPS</div>
                      <div className="mono" style={{ fontSize: 22 }}>{p.steps}</div>
                    </div>
                  </div>
                  <div className="card">
                    <div className="card-bd">
                      <div className="mono faint" style={{ fontSize: 10.5 }}>FILES</div>
                      <div className="mono" style={{ fontSize: 22 }}>{p.files}</div>
                    </div>
                  </div>
                  <div className="card">
                    <div className="card-bd">
                      <div className="mono faint" style={{ fontSize: 10.5 }}>SCORE</div>
                      <div className="mono" style={{ fontSize: 22 }}>{p.score}</div>
                    </div>
                  </div>
                </div>

                <div
                  style={{
                    fontSize: 10.5,
                    textTransform: 'uppercase',
                    letterSpacing: '0.08em',
                    color: 'var(--fg-3)',
                    margin: '16px 0 8px',
                  }}
                >
                  Repos on this flow
                </div>
                <div className="hstack" style={{ gap: 4, flexWrap: 'wrap' }}>
                  {p.crosses.map((r) => (
                    <span key={r} className="chip">
                      <span className="swatch" style={{ background: REPOS.find((x) => x.id === r)?.color || 'var(--fg-2)' }} />
                      {r}
                    </span>
                  ))}
                </div>

                <div
                  style={{
                    fontSize: 10.5,
                    textTransform: 'uppercase',
                    letterSpacing: '0.08em',
                    color: 'var(--fg-3)',
                    margin: '16px 0 8px',
                  }}
                >
                  Risk signals
                </div>
                <div className="vstack">
                  <div className="hstack" style={{ justifyContent: 'space-between' }}>
                    <span>Cyclomatic max</span>
                    <span className="mono">14</span>
                  </div>
                  <div className="hstack" style={{ justifyContent: 'space-between' }}>
                    <span>Error branches</span>
                    <span className="mono">9</span>
                  </div>
                  <div className="hstack" style={{ justifyContent: 'space-between' }}>
                    <span>Test coverage</span>
                    <span className="mono" style={{ color: 'var(--warn)' }}>28%</span>
                  </div>
                  <div className="hstack" style={{ justifyContent: 'space-between' }}>
                    <span>Caveats on path</span>
                    <span className="mono">3</span>
                  </div>
                </div>

                <button
                  type="button"
                  className="btn primary"
                  style={{ marginTop: 14, width: '100%' }}
                  onClick={() => setSym(SYMBOLS[0])}
                >
                  <Icon name="flask" size={12} /> Open in investigation
                </button>
              </div>
            )
          })()}
        </div>
      </div>
    </>
  )
}

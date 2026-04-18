'use client'

import { Icon } from '@/components/primitives/Icon'
import { GUARDS } from '@/lib/seed'

export function GuardsView() {
  const violated = GUARDS.filter((g) => g.status === 'violated').length
  const warn = GUARDS.filter((g) => g.status === 'warn').length
  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Guards</h1>
          <div className="sub">
            {GUARDS.length} rules from <span className="mono">.gortex.yaml</span> · {violated} violated · {warn} warning
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn ghost">
            <Icon name="file" size={12} /> Open .gortex.yaml
          </button>
          <button type="button" className="btn">
            <Icon name="plus" size={12} /> New guard
          </button>
        </div>
      </div>
      <div style={{ padding: '18px 22px', overflow: 'auto' }}>
        <div className="card" style={{ padding: 0 }}>
          <table className="tbl">
            <thead>
              <tr>
                <th>Rule</th>
                <th>Kind</th>
                <th>Scope</th>
                <th>Status</th>
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
                  <td>
                    {g.status === 'violated' && <span className="cav risk">violated</span>}
                    {g.status === 'warn' && <span className="cav deprecated">warning</span>}
                    {g.status === 'ok' && <span className="chip" style={{ color: 'var(--ok)' }}>passing</span>}
                  </td>
                  <td className="num">{g.hits}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}

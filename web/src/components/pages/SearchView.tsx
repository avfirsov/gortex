'use client'

import { useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge, Kbd } from '@/components/primitives/Caveat'
import { useInspector } from '@/lib/inspector'
import { SYMBOLS } from '@/lib/seed'

const FACETS = [
  'kind:function',
  'kind:interface',
  'repo:core-api',
  'community:ingest',
  'caveat:deprecated',
  'has:tests',
  'owner:@platform',
]

export function SearchView() {
  const setSym = useInspector((s) => s.setSym)
  const [q, setQ] = useState('')

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Search symbols</h1>
          <div className="sub">Functions, types, methods, interfaces, files, contracts, flows — with facets</div>
        </div>
      </div>
      <div style={{ padding: 22, overflow: 'auto' }}>
        <div style={{ display: 'flex', gap: 10, marginBottom: 14 }}>
          <div
            className="hstack"
            style={{
              flex: 1,
              background: 'var(--bg-2)',
              border: '1px solid var(--line-1)',
              borderRadius: 8,
              padding: '0 12px',
              height: 40,
            }}
          >
            <Icon name="search" size={14} />
            <input
              style={{ flex: 1, background: 'transparent', border: 0, outline: 0, fontSize: 14, padding: '0 8px' }}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="e.g. handleRequest   kind:interface repo:core-api"
            />
            <Kbd>⌘</Kbd>
            <Kbd>K</Kbd>
          </div>
        </div>
        <div className="hstack" style={{ gap: 6, marginBottom: 14, flexWrap: 'wrap' }}>
          {FACETS.map((f) => (
            <span key={f} className="chip" style={{ cursor: 'pointer' }}>{f}</span>
          ))}
        </div>
        <div className="card" style={{ padding: 0 }}>
          <table className="tbl">
            <thead>
              <tr>
                <th style={{ width: 28 }} />
                <th>Symbol</th>
                <th>Repo · file</th>
                <th>Caveats</th>
                <th className="num">Callers</th>
                <th className="num">Callees</th>
              </tr>
            </thead>
            <tbody>
              {SYMBOLS.map((s) => (
                <tr key={s.id} onClick={() => setSym(s)} style={{ cursor: 'pointer' }}>
                  <td>
                    <span className={`swatch sw-${s.kind}`} />
                  </td>
                  <td>
                    <div className="mono" style={{ color: 'var(--fg-0)' }}>{s.name}</div>
                    <div className="mono faint" style={{ fontSize: 10.5 }}>{s.kind}</div>
                  </td>
                  <td className="mono-cell faint">{s.file}</td>
                  <td>
                    <div className="hstack" style={{ gap: 4, flexWrap: 'wrap' }}>
                      {s.caveats.map((c) => (
                        <CaveatBadge key={c} kind={c} />
                      ))}
                    </div>
                  </td>
                  <td className="num">{s.callers}</td>
                  <td className="num">{s.callees}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}

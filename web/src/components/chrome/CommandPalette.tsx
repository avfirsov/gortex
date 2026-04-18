'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { Icon } from '@/components/primitives/Icon'
import { useCmdK } from '@/lib/cmdk'
import { useInspector } from '@/lib/inspector'
import { SYMBOLS, RECENT_SEARCHES } from '@/lib/seed'

const JUMPS = [
  { k: 'Dashboard',     sub: 'control room',          meta: 'G D', href: '/' },
  { k: 'Graph explorer',sub: '4 view modes',          meta: 'G G', href: '/graph' },
  { k: 'Investigation', sub: 'open: Email ingest 500s',meta: 'G I', href: '/investigations' },
  { k: 'Caveats',       sub: '42 flagged',            meta: 'G C', href: '/caveats' },
]

export function CommandPalette() {
  const open = useCmdK((s) => s.open)
  const setOpen = useCmdK((s) => s.setOpen)
  const setSym = useInspector((s) => s.setSym)
  const router = useRouter()
  const [q, setQ] = useState('')
  const [idx, setIdx] = useState(0)

  useEffect(() => {
    function h(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setOpen(!open)
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, setOpen])

  useEffect(() => {
    if (!open) return
    function h(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setIdx((i) => i + 1)
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setIdx((i) => Math.max(0, i - 1))
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, setOpen])

  if (!open) return null

  const results = q
    ? SYMBOLS.filter(
        (s) =>
          s.name.toLowerCase().includes(q.toLowerCase()) ||
          s.file.toLowerCase().includes(q.toLowerCase()),
      )
    : SYMBOLS.slice(0, 4)

  return (
    <div className="cmd-modal-scrim" onClick={() => setOpen(false)}>
      <div className="cmd-modal" onClick={(e) => e.stopPropagation()}>
        <input
          autoFocus
          className="cmd-input"
          placeholder="Search symbols, files, flows, contracts…"
          value={q}
          onChange={(e) => {
            setQ(e.target.value)
            setIdx(0)
          }}
        />
        {!q && (
          <div className="cmd-section">
            <div className="sec-ti">Jump to</div>
            {JUMPS.map((r, i) => (
              <div
                key={i}
                className="cmd-row"
                onClick={() => {
                  router.push(r.href)
                  setOpen(false)
                }}
              >
                <Icon name="arrowr" size={12} />
                <div>
                  <div className="k">{r.k}</div>
                  <div className="sub">{r.sub}</div>
                </div>
                <span className="meta">{r.meta}</span>
              </div>
            ))}
            <div className="sec-ti">Recent</div>
            {RECENT_SEARCHES.map((r, i) => (
              <div key={i} className="cmd-row">
                <Icon name="history" size={12} />
                <div>
                  <div className="k mono">{r.q}</div>
                  <div className="sub">
                    {r.kind} · {r.hits} hits
                  </div>
                </div>
                <span className="meta">↵</span>
              </div>
            ))}
          </div>
        )}
        {q && (
          <div className="cmd-section">
            <div className="sec-ti">Symbols ({results.length})</div>
            {results.map((s, i) => (
              <div
                key={s.id}
                className={`cmd-row ${i === idx ? 'active' : ''}`}
                onClick={() => {
                  setSym(s)
                  setOpen(false)
                }}
              >
                <span className={`swatch sw-${s.kind}`} />
                <div>
                  <div className="k mono">{s.name}</div>
                  <div className="sub">
                    {s.repo} · {s.file}
                  </div>
                </div>
                <span className="meta">{s.kind}</span>
              </div>
            ))}
            <div className="sec-ti">Actions</div>
            <div className="cmd-row">
              <Icon name="route" size={12} />
              <div>
                <div className="k">Trace &quot;{q}&quot; across repos</div>
                <div className="sub">builds process flow from entry</div>
              </div>
              <span className="meta">⌘↵</span>
            </div>
            <div className="cmd-row">
              <Icon name="fork" size={12} />
              <div>
                <div className="k">Blast radius for &quot;{q}&quot;</div>
                <div className="sub">direct + transitive dependents</div>
              </div>
              <span className="meta">⌥↵</span>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

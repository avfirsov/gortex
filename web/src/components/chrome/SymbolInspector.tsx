'use client'

import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { useInspector } from '@/lib/inspector'
import { SYMBOLS, PROCESSES } from '@/lib/seed'

export function SymbolInspector() {
  const sym = useInspector((s) => s.sym)
  const setSym = useInspector((s) => s.setSym)

  if (!sym) {
    return (
      <div style={{ padding: 20, color: 'var(--fg-2)', fontSize: 12.5 }}>
        <div className="section-label" style={{ padding: 0, marginBottom: 10 }}>Inspector</div>
        <div style={{ padding: '40px 0', textAlign: 'center', color: 'var(--fg-3)' }}>
          <Icon name="search" size={18} />
          <div style={{ marginTop: 8 }}>Select a symbol, edge, or flow step</div>
          <div style={{ fontSize: 11, marginTop: 4 }}>Details appear here without leaving the canvas</div>
        </div>
        <div className="section-label" style={{ padding: 0, marginTop: 14, marginBottom: 8 }}>Pinned symbols</div>
        <div className="vstack">
          {SYMBOLS.slice(0, 4).map((s) => (
            <button
              type="button"
              key={s.id}
              className="ref"
              onClick={() => setSym(s)}
              style={{ width: '100%', textAlign: 'left' }}
            >
              <span className={`swatch sw-${s.kind}`} />
              <span className="where">{s.name}</span>
              <span className="count mono">{s.repo}</span>
            </button>
          ))}
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="sym-hd">
        <div className="hstack" style={{ justifyContent: 'space-between' }}>
          <span className="kind">
            <span className={`swatch sw-${sym.kind}`} style={{ marginRight: 6 }} />
            {sym.kind}
          </span>
          <button type="button" className="btn small ghost" onClick={() => setSym(null)}>
            <Icon name="close" size={11} />
          </button>
        </div>
        <div className="name">{sym.name}</div>
        <div className="path">
          {sym.repo} · {sym.file}
        </div>
        {sym.caveats?.length > 0 && (
          <div className="hstack" style={{ marginTop: 8, gap: 4, flexWrap: 'wrap' }}>
            {sym.caveats.map((c) => (
              <CaveatBadge key={c} kind={c} />
            ))}
          </div>
        )}
        <div className="hstack" style={{ gap: 6, marginTop: 10 }}>
          <button type="button" className="btn small">
            <Icon name="file" size={11} /> Open file
          </button>
          <button type="button" className="btn small ghost">
            <Icon name="copy" size={11} /> Copy id
          </button>
          <button type="button" className="btn small ghost">
            <Icon name="pin" size={11} /> Pin
          </button>
        </div>
      </div>

      <div className="sym-section">
        <div className="sec-ti">Signature</div>
        <pre className="code" style={{ margin: 0 }}>{sym.sig}</pre>
      </div>

      <div className="sym-section">
        <div className="sec-ti">
          <span>Callers</span>
          <span className="mono faint" style={{ fontSize: 11 }}>{sym.callers} sites</span>
        </div>
        {['RegisterRoutes', 'handleEmail', 'dispatchEvent', 'normalizeInput']
          .slice(0, sym.callers || 2)
          .map((n, i) => (
            <div key={i} className="ref">
              <span className="swatch sw-function" />
              <span className="where">{n}</span>
              <span className="count">{sym.repo}</span>
            </div>
          ))}
      </div>

      <div className="sym-section">
        <div className="sec-ti">
          <span>Calls</span>
          <span className="mono faint" style={{ fontSize: 11 }}>{sym.callees} symbols</span>
        </div>
        {['pgx.Exec', 'slog.Info', 'validateToken', 'emitEvent', 'writeError']
          .slice(0, Math.min(5, sym.callees || 3))
          .map((n, i) => (
            <div key={i} className="ref">
              <span className="swatch sw-method" />
              <span className="where">{n}</span>
              <span className="count">ext</span>
            </div>
          ))}
      </div>

      <div className="sym-section">
        <div className="sec-ti">Community</div>
        <div style={{ fontSize: 12.5 }}>
          <div className="mono" style={{ color: 'var(--fg-0)' }}>{sym.community}</div>
          <div className="faint" style={{ fontSize: 11, marginTop: 2 }}>
            cohesion 0.71 · 624 symbols · 41 files
          </div>
        </div>
      </div>

      <div className="sym-section">
        <div className="sec-ti">Appears in processes</div>
        <div className="vstack">
          {PROCESSES.slice(0, 3).map((p) => (
            <div key={p.id} className="ref">
              <Icon name="route" size={11} />
              <span className="where">{p.name}</span>
              <span className="count">step {p.steps}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="sym-section" style={{ borderBottom: 0 }}>
        <div className="sec-ti">Ask AI about this</div>
        <div className="vstack" style={{ gap: 4 }}>
          {[
            'What does this do?',
            'Who breaks if I rename it?',
            'Why is it on the hot path?',
            'Suggest a safer refactor',
          ].map((q, i) => (
            <button key={i} type="button" className="btn" style={{ justifyContent: 'flex-start', width: '100%' }}>
              <Icon name="chat" size={11} /> {q}
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}

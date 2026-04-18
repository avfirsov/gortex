'use client'

import { Icon } from '@/components/primitives/Icon'
import { useTweaks } from '@/lib/tweaks'
import { STATS } from '@/lib/seed'

export function StatusBar() {
  const scope = useTweaks((s) => s.scope)
  return (
    <div className="statusbar">
      <span className="seg ok">
        <Icon name="dot" size={10} /> live · watch
      </span>
      <span className="sep">·</span>
      <span className="seg">
        nodes <b style={{ color: 'var(--fg-0)' }}>{STATS.totalNodes.toLocaleString()}</b>
      </span>
      <span className="seg">
        edges <b style={{ color: 'var(--fg-0)' }}>{STATS.totalEdges.toLocaleString()}</b>
      </span>
      <span className="seg">
        repos <b style={{ color: 'var(--fg-0)' }}>{STATS.reposIndexed}</b>
      </span>
      <span className="seg warn">
        caveats <b style={{ color: 'var(--warn)' }}>{STATS.caveats}</b>
      </span>
      <span className="sep">·</span>
      <span className="seg">
        scope <b style={{ color: 'var(--fg-0)' }}>{scope === 'federated' ? 'all repos' : 'core-api'}</b>
      </span>
      <span className="spacer" />
      <span className="seg">
        <Icon name="history" size={11} /> indexed 33m ago
      </span>
      <span className="seg">{STATS.version}</span>
    </div>
  )
}

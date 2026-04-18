'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { Icon } from '@/components/primitives/Icon'
import { useTweaks } from '@/lib/tweaks'
import { useCmdK } from '@/lib/cmdk'
import { STATS } from '@/lib/seed'
import { NAV } from './nav'
import { pageIdFromPath } from './path'

function NavLink({
  item,
  active,
  mini,
  onSearchClick,
}: {
  item: (typeof NAV)[number]
  active: boolean
  mini: boolean
  onSearchClick: () => void
}) {
  const inner = mini ? (
    <Icon name={item.icon} size={16} />
  ) : (
    <>
      <Icon name={item.icon} size={14} />
      <span>{item.label}</span>
      {item.kbd ? (
        <span className="num mono">{item.kbd}</span>
      ) : item.num ? (
        <span className="num">{item.num}</span>
      ) : null}
    </>
  )
  if (item.id === 'search') {
    return (
      <button
        type="button"
        className={`nav-item ${active ? 'active' : ''}`}
        title={mini ? item.label : undefined}
        onClick={onSearchClick}
      >
        {inner}
      </button>
    )
  }
  return (
    <Link href={item.href} className={`nav-item ${active ? 'active' : ''}`} title={mini ? item.label : undefined}>
      {inner}
    </Link>
  )
}

export function SideRail() {
  const pathname = usePathname()
  const layout = useTweaks((s) => s.layout)
  const openCmdK = useCmdK((s) => s.setOpen)
  const pageId = pageIdFromPath(pathname)

  const isActive = (id: string) => {
    if (id === 'dashboard') return pageId === 'dashboard'
    return pageId === id
  }

  if (layout === 'workspace') {
    return (
      <aside className="side mini">
        {NAV.map((n) => (
          <NavLink key={n.id} item={n} active={isActive(n.id)} mini onSearchClick={() => openCmdK(true)} />
        ))}
      </aside>
    )
  }

  if (layout === 'cmdk') {
    return <div className="side" style={{ display: 'none' }} />
  }

  return (
    <>
      <aside className="side rail">
        <div className="section-label">Explore</div>
        {NAV.slice(0, 3).map((n) => (
          <NavLink key={n.id} item={n} active={isActive(n.id)} mini={false} onSearchClick={() => openCmdK(true)} />
        ))}
        <div className="section-label">Understand</div>
        {NAV.slice(3, 7).map((n) => (
          <NavLink key={n.id} item={n} active={isActive(n.id)} mini={false} onSearchClick={() => openCmdK(true)} />
        ))}
        <div className="section-label">Investigate</div>
        {NAV.slice(7).map((n) => (
          <NavLink key={n.id} item={n} active={isActive(n.id)} mini={false} onSearchClick={() => openCmdK(true)} />
        ))}
        <div
          style={{
            marginTop: 'auto',
            padding: 10,
            borderTop: '1px solid var(--line-1)',
            fontSize: 11,
            color: 'var(--fg-2)',
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
          }}
        >
          <div className="hstack" style={{ justifyContent: 'space-between' }}>
            <span>Nodes</span>
            <span className="mono">{STATS.totalNodes.toLocaleString()}</span>
          </div>
          <div className="hstack" style={{ justifyContent: 'space-between' }}>
            <span>Edges</span>
            <span className="mono">{STATS.totalEdges.toLocaleString()}</span>
          </div>
          <div className="hstack" style={{ justifyContent: 'space-between' }}>
            <span>Avg fan-out</span>
            <span className="mono">{STATS.avgEdgesPerNode}</span>
          </div>
        </div>
      </aside>
      <aside className="side mini-fallback">
        {NAV.map((n) => (
          <NavLink key={n.id} item={n} active={isActive(n.id)} mini onSearchClick={() => openCmdK(true)} />
        ))}
      </aside>
    </>
  )
}

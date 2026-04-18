'use client'

import { useMemo } from 'react'
import { REPOS } from '@/lib/seed'
import { rnd } from './rng'

type ONode = { x: number; y: number; theta: number; ring: number; color: string; repo: string; hot: boolean; size: number }

export function ThreeDOrbital() {
  const cx = 540
  const cy = 320
  const rings = [70, 125, 185, 248, 315]
  const tilt = 0.45

  const { nodes, arcs, radials } = useMemo(() => {
    const r = rnd(47)
    const nodes: ONode[] = []
    REPOS.forEach((rep, idx) => {
      const ringIdx = Math.min(rings.length - 1, Math.max(0, Math.round(Math.log2(rep.nodes + 1) - 6)))
      const count = Math.min(26, Math.max(5, Math.round(rep.nodes / 320)))
      const sectorSpan = (Math.PI * 2) / REPOS.length
      const sectorStart = idx * sectorSpan
      for (let i = 0; i < count; i++) {
        const ring = (ringIdx + (r() > 0.7 ? 1 : 0)) % rings.length
        const theta = sectorStart + r() * sectorSpan
        const x = cx + Math.cos(theta) * rings[ring]
        const y = cy + Math.sin(theta) * rings[ring] * tilt
        nodes.push({
          x,
          y,
          theta,
          ring,
          color: rep.color,
          repo: rep.id,
          hot: r() > 0.93,
          size: 1.8 + r() * 2.6,
        })
      }
    })
    const arcs: { a: ONode; b: ONode; ring: number; hot: boolean }[] = []
    const radials: { a: ONode; b: ONode; hot: boolean }[] = []
    for (let i = 0; i < 40; i++) {
      const a = nodes[Math.floor(r() * nodes.length)]
      const b = nodes[Math.floor(r() * nodes.length)]
      if (!a || !b || a === b) continue
      if (a.ring === b.ring) arcs.push({ a, b, ring: a.ring, hot: r() > 0.85 })
      else radials.push({ a, b, hot: r() > 0.88 })
    }
    return { nodes, arcs, radials }
  }, [])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {rings.map((rr, i) => (
        <ellipse
          key={i}
          cx={cx}
          cy={cy}
          rx={rr}
          ry={rr * tilt}
          fill="none"
          stroke="var(--line-1)"
          strokeWidth="0.6"
          strokeDasharray={i === 0 ? '0' : '2 4'}
        />
      ))}
      {REPOS.map((rep, idx) => {
        const a = (idx / REPOS.length) * Math.PI * 2
        const rr = rings[rings.length - 1]
        const x = cx + Math.cos(a) * rr
        const y = cy + Math.sin(a) * rr * tilt
        return <line key={rep.id} x1={cx} y1={cy} x2={x} y2={y} stroke={rep.color} strokeOpacity="0.18" strokeWidth="0.6" />
      })}
      {radials.map((e, i) => (
        <line
          key={`r${i}`}
          x1={e.a.x}
          y1={e.a.y}
          x2={e.b.x}
          y2={e.b.y}
          stroke={e.hot ? 'var(--pink)' : 'var(--accent)'}
          strokeOpacity={e.hot ? 0.85 : 0.3}
          strokeWidth={e.hot ? 1.3 : 0.6}
        />
      ))}
      {arcs.map((e, i) => {
        const rr = rings[e.ring]
        const mid = { x: (e.a.x + e.b.x) / 2, y: (e.a.y + e.b.y) / 2 }
        const dx = mid.x - cx
        const dy = (mid.y - cy) / tilt
        const len = Math.hypot(dx, dy) || 1
        const out = (rr * 1.05) / len
        const ctrl = { x: cx + dx * out, y: cy + dy * out * tilt }
        return (
          <path
            key={`a${i}`}
            d={`M${e.a.x},${e.a.y} Q${ctrl.x},${ctrl.y} ${e.b.x},${e.b.y}`}
            fill="none"
            stroke={e.hot ? 'var(--pink)' : e.a.color}
            strokeOpacity={e.hot ? 0.8 : 0.4}
            strokeWidth={e.hot ? 1.2 : 0.6}
          />
        )
      })}
      {REPOS.map((rep, idx) => {
        const a = (idx / REPOS.length) * Math.PI * 2 + Math.PI / REPOS.length
        const rr = rings[rings.length - 1] + 20
        const x = cx + Math.cos(a) * rr
        const y = cy + Math.sin(a) * rr * tilt
        return (
          <g key={rep.id}>
            <circle cx={x} cy={y} r={3} fill={rep.color} />
            <text x={x + 8} y={y + 3} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
      {nodes.map((n, i) => (
        <circle
          key={i}
          cx={n.x}
          cy={n.y}
          r={n.hot ? n.size + 1 : n.size}
          fill={n.hot ? 'var(--pink)' : n.color}
          opacity="0.95"
        />
      ))}
      <circle cx={cx} cy={cy} r={18} fill="var(--bg-1)" stroke="var(--accent)" strokeWidth="1.5" />
      <circle cx={cx} cy={cy} r={8} fill="var(--accent)" />
      <text x={cx} y={cy - 28} textAnchor="middle" fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-2)">
        entry · POST /ingest/email
      </text>
      <text x={cx} y={cy + 36} textAnchor="middle" fontFamily="JetBrains Mono" fontSize="9" fill="var(--fg-3)">
        ring = call depth
      </text>
    </svg>
  )
}

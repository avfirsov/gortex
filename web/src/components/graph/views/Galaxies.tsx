'use client'

import { useMemo } from 'react'
import { REPOS } from '@/lib/seed'
import { rnd } from './rng'

type GNode = { x: number; y: number; z: number; color: string; repo: string; hot: boolean; kind: string }
type GProj = GNode & { s: number }

export function ThreeDGalaxies() {
  const cx = 540
  const cy = 320

  const { projected, edges } = useMemo(() => {
    const r = rnd(59)
    const nodes: GNode[] = []
    REPOS.forEach((rep, idx) => {
      const a = (idx / REPOS.length) * Math.PI * 2
      const gx = cx + Math.cos(a) * 240
      const gy = cy + Math.sin(a) * 150
      const gz = (r() - 0.5) * 200
      const count = Math.min(45, Math.max(10, Math.round(rep.nodes / 260)))
      for (let i = 0; i < count; i++) {
        const rr = Math.sqrt(r()) * 110
        const t = r() * Math.PI * 2
        const dz = (r() - 0.5) * 120
        nodes.push({
          x: gx + Math.cos(t) * rr,
          y: gy + Math.sin(t) * rr * 0.8,
          z: gz + dz,
          color: rep.color,
          repo: rep.id,
          hot: r() > 0.94,
          kind: ['function', 'method', 'type', 'interface'][Math.floor(r() * 4)],
        })
      }
    })
    const camZ = 600
    const proj = (n: GNode): GProj => {
      const s = camZ / (camZ - n.z)
      return { ...n, x: cx + (n.x - cx) * s, y: cy + (n.y - cy) * s, s }
    }
    const projected = nodes.map(proj).sort((a, b) => a.z - b.z)
    const edges: { a: GProj; b: GProj; same: boolean; hot: boolean }[] = []
    for (let i = 0; i < 220; i++) {
      const a = projected[Math.floor(r() * projected.length)]
      const b = projected[Math.floor(r() * projected.length)]
      if (!a || !b || a === b) continue
      const same = a.repo === b.repo
      if (same || r() > 0.82) edges.push({ a, b, same, hot: !same && r() > 0.7 })
    }
    return { projected, edges }
  }, [])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {REPOS.map((rep, idx) => {
        const a = (idx / REPOS.length) * Math.PI * 2
        const gx = cx + Math.cos(a) * 240
        const gy = cy + Math.sin(a) * 150
        return <circle key={rep.id} cx={gx} cy={gy} r={130} fill={rep.color} fillOpacity="0.05" />
      })}
      {edges.map((e, i) => {
        const depth = (e.a.z + e.b.z) / 2
        const dim = Math.max(0.12, Math.min(1, (depth + 200) / 500))
        return (
          <line
            key={i}
            x1={e.a.x}
            y1={e.a.y}
            x2={e.b.x}
            y2={e.b.y}
            stroke={e.hot ? 'var(--pink)' : e.same ? e.a.color : 'var(--accent)'}
            strokeOpacity={(e.hot ? 0.85 : e.same ? 0.28 : 0.5) * dim}
            strokeWidth={e.hot ? 1.4 : e.same ? 0.4 : 0.7}
          />
        )
      })}
      {projected.map((n, i) => {
        const dim = Math.max(0.35, Math.min(1, (n.z + 200) / 500))
        const size = Math.max(1.2, 2.4 * n.s)
        return (
          <circle
            key={i}
            cx={n.x}
            cy={n.y}
            r={n.hot ? size + 1 : size}
            fill={n.hot ? 'var(--pink)' : n.color}
            opacity={0.55 + 0.45 * dim}
          />
        )
      })}
      {REPOS.map((rep, idx) => {
        const a = (idx / REPOS.length) * Math.PI * 2
        const gx = cx + Math.cos(a) * 300
        const gy = cy + Math.sin(a) * 190
        return (
          <g key={rep.id}>
            <rect
              x={gx - 6}
              y={gy - 11}
              width={rep.id.length * 7 + 22}
              height={18}
              rx={9}
              fill="var(--bg-1)"
              opacity="0.8"
              stroke={rep.color}
              strokeOpacity="0.5"
            />
            <circle cx={gx + 3} cy={gy - 2} r={3} fill={rep.color} />
            <text x={gx + 11} y={gy + 3} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

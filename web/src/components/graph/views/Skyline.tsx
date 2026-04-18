'use client'

import { useMemo } from 'react'
import { REPOS } from '@/lib/seed'
import { rnd } from './rng'

export function ThreeDSkyline() {
  const nodes = useMemo(() => {
    const r = rnd(23)
    const out: { gx: number; gy: number; elev: number; color: string; repo: string; hot: boolean }[] = []
    REPOS.forEach((rep, idx) => {
      const baseX = 180 + (idx % 4) * 220
      const baseY = 120 + Math.floor(idx / 4) * 260
      const n = Math.min(35, Math.max(6, Math.round(rep.nodes / 300)))
      for (let i = 0; i < n; i++) {
        const gx = (r() - 0.5) * 160
        const gy = (r() - 0.5) * 120
        const elev = r() * 58 + 6
        out.push({
          gx: baseX + gx,
          gy: baseY + gy,
          elev,
          color: rep.color,
          repo: rep.id,
          hot: r() > 0.93,
        })
      }
    })
    return out
  }, [])
  const iso = (x: number, y: number, z: number) => ({
    x: 540 + (x - y) * 0.86,
    y: 280 + (x + y) * 0.5 - z,
  })
  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {Array.from({ length: 20 }, (_, i) => {
        const a = iso(i * 80, 0, 0)
        const b = iso(i * 80, 800, 0)
        return <line key={`gx${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.3" />
      })}
      {Array.from({ length: 20 }, (_, i) => {
        const a = iso(0, i * 80, 0)
        const b = iso(1600, i * 80, 0)
        return <line key={`gy${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.3" />
      })}
      {REPOS.map((rep, idx) => {
        const baseX = 180 + (idx % 4) * 220
        const baseY = 120 + Math.floor(idx / 4) * 260
        const pad = 95
        const c = [
          [baseX - pad, baseY - pad],
          [baseX + pad, baseY - pad],
          [baseX + pad, baseY + pad],
          [baseX - pad, baseY + pad],
        ]
        const pts = c.map(([x, y]) => {
          const p = iso(x, y, 0)
          return `${p.x},${p.y}`
        }).join(' ')
        return (
          <polygon
            key={rep.id}
            points={pts}
            fill={rep.color}
            opacity="0.055"
            stroke={rep.color}
            strokeOpacity="0.35"
            strokeWidth="0.8"
          />
        )
      })}
      {[...nodes].sort((a, b) => a.gx + a.gy - (b.gx + b.gy)).map((n, i) => {
        const base = iso(n.gx, n.gy, 0)
        const top = iso(n.gx, n.gy, n.elev)
        return (
          <g key={i}>
            <line
              x1={base.x}
              y1={base.y}
              x2={top.x}
              y2={top.y}
              stroke={n.hot ? 'var(--pink)' : n.color}
              strokeWidth={n.hot ? 3 : 2.4}
              strokeLinecap="round"
              opacity={n.hot ? 1 : 0.95}
            />
            <circle cx={top.x} cy={top.y} r={n.hot ? 3.6 : 3} fill={n.hot ? 'var(--pink)' : n.color} />
            <circle cx={base.x} cy={base.y} r={1.8} fill={n.color} opacity="0.35" />
          </g>
        )
      })}
      {REPOS.map((rep, idx) => {
        const baseX = 180 + (idx % 4) * 220
        const baseY = 120 + Math.floor(idx / 4) * 260
        const p = iso(baseX - 85, baseY + 95, 0)
        return (
          <g key={rep.id}>
            <rect x={p.x - 4} y={p.y - 10} width={rep.id.length * 6.5 + 18} height={16} fill="var(--bg-1)" opacity="0.75" rx={3} />
            <circle cx={p.x + 4} cy={p.y - 2} r={3} fill={rep.color} />
            <text x={p.x + 11} y={p.y + 2} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

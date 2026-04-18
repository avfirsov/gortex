'use client'

import { useMemo } from 'react'
import { REPOS } from '@/lib/seed'
import { rnd } from './rng'

type PNode = { x: number; y: number; raw: { nx: number; ny: number; z: number }; color: string; hot: boolean }
type Plane = {
  rep: (typeof REPOS)[number]
  z: number
  corners: { x: number; y: number }[]
  nodes: PNode[]
  label: { x: number; y: number }
}

export function ThreeDStrata() {
  const { planes, rain } = useMemo(() => {
    const r = rnd(31)
    const planeCount = Math.min(REPOS.length, 7)
    const ordered = [...REPOS].slice(0, planeCount)
    const iso = (x: number, y: number, z: number) => ({
      x: 540 + (x - y) * 0.82,
      y: 400 + (x + y) * 0.42 - z,
    })
    const planeW = 760
    const planeH = 260
    const planes: Plane[] = ordered.map((rep, i) => {
      const z = (ordered.length - 1 - i) * 70 + 40
      const ox = -planeW / 2
      const oy = -planeH / 2
      const corners = [
        [ox, oy],
        [ox + planeW, oy],
        [ox + planeW, oy + planeH],
        [ox, oy + planeH],
      ].map(([x, y]) => iso(x, y, z))
      const nCount = Math.min(34, Math.max(8, Math.round(rep.nodes / 320)))
      const nodes: PNode[] = Array.from({ length: nCount }, () => {
        const nx = (r() - 0.5) * (planeW - 60)
        const ny = (r() - 0.5) * (planeH - 40)
        return {
          ...iso(nx, ny, z),
          raw: { nx, ny, z },
          color: rep.color,
          hot: r() > 0.9,
        }
      })
      return { rep, z, corners, nodes, label: iso(ox + planeW + 8, oy + planeH - 12, z) }
    })

    const rain: { a: PNode; b: PNode; hot: boolean }[] = []
    for (let i = 0; i < planes.length - 1; i++) {
      for (let k = 0; k < 4 + Math.floor(r() * 4); k++) {
        const a = planes[i].nodes[Math.floor(r() * planes[i].nodes.length)]
        const b = planes[i + 1 + Math.floor(r() * Math.max(1, planes.length - i - 1))]?.nodes[Math.floor(r() * 10)]
        if (a && b) rain.push({ a, b, hot: r() > 0.85 })
      }
    }
    return { planes, rain }
  }, [])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {planes.map(({ rep, corners, nodes, label }) => (
        <g key={rep.id}>
          <polygon
            points={corners.map((c) => `${c.x},${c.y}`).join(' ')}
            fill={rep.color}
            fillOpacity="0.07"
            stroke={rep.color}
            strokeOpacity="0.5"
            strokeWidth="0.8"
          />
          {nodes.map((n, j) => (
            <circle key={j} cx={n.x} cy={n.y} r={n.hot ? 3.2 : 2.2} fill={n.hot ? 'var(--pink)' : n.color} opacity="0.92" />
          ))}
          {nodes.slice(0, 10).map((n, j) => {
            const m = nodes[(j * 3 + 2) % nodes.length]
            return (
              <line key={`e${j}`} x1={n.x} y1={n.y} x2={m.x} y2={m.y} stroke={rep.color} strokeOpacity="0.28" strokeWidth="0.5" />
            )
          })}
          <g>
            <rect
              x={label.x - 4}
              y={label.y - 12}
              width={rep.id.length * 7 + 20}
              height={18}
              rx={3}
              fill="var(--bg-1)"
              opacity="0.85"
              stroke={rep.color}
              strokeOpacity="0.4"
            />
            <circle cx={label.x + 4} cy={label.y - 3} r={3} fill={rep.color} />
            <text x={label.x + 12} y={label.y + 2} fontFamily="JetBrains Mono" fontSize="10.5" fill="var(--fg-1)">
              {rep.id}
            </text>
            <text x={label.x + 12} y={label.y + 14} fontFamily="JetBrains Mono" fontSize="9" fill="var(--fg-3)">
              {rep.nodes} · {rep.lang}
            </text>
          </g>
        </g>
      ))}
      {rain.map((e, i) => (
        <line
          key={i}
          x1={e.a.x}
          y1={e.a.y}
          x2={e.b.x}
          y2={e.b.y}
          stroke={e.hot ? 'var(--pink)' : 'var(--accent)'}
          strokeOpacity={e.hot ? 0.75 : 0.35}
          strokeWidth={e.hot ? 1.3 : 0.8}
          strokeDasharray={e.hot ? '0' : '2 3'}
        />
      ))}
    </svg>
  )
}

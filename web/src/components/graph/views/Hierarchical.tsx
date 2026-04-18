'use client'

import { useInspector } from '@/lib/inspector'
import { SYMBOLS } from '@/lib/seed'

type TreeNode = { name: string; kind?: string; caveat?: string; children?: TreeNode[] }

const tree: TreeNode = {
  name: 'POST /ingest/email',
  children: [
    {
      name: 'RegisterRoutes',
      kind: 'function',
      children: [
        {
          name: 'EmailIngestHandler',
          kind: 'method',
          children: [
            {
              name: 'ExtractLinks',
              kind: 'function',
              caveat: 'deprecated',
              children: [
                { name: 'urlParse', kind: 'function' },
                { name: 'normalize', kind: 'function' },
              ],
            },
            { name: 'Authn', kind: 'function', caveat: 'hot' },
            {
              name: 'PostgresTuckStore.Insert',
              kind: 'method',
              children: [{ name: 'pgx.Exec', kind: 'method' }],
            },
          ],
        },
      ],
    },
  ],
}

type FlatNode = TreeNode & { _d: number; _x: number; _id: number }

function flatten(): { acc: FlatNode[]; edges: [number, number][] } {
  const acc: FlatNode[] = []
  const edges: [number, number][] = []
  function visit(n: TreeNode, d: number, x: number, parent: FlatNode | null) {
    const flat: FlatNode = { ...n, _d: d, _x: x, _id: acc.length }
    if (parent) edges.push([parent._id, flat._id])
    acc.push(flat)
    if (n.children) {
      const w = 1 / n.children.length
      n.children.forEach((c, i) => visit(c, d + 1, x - 0.5 + (i + 0.5) * w, flat))
    }
  }
  visit(tree, 0, 0.5, null)
  return { acc, edges }
}

export function GraphHierarchical() {
  const setSym = useInspector((s) => s.setSym)
  const { acc, edges } = flatten()
  const w = 1080
  const h = 600
  const pos = (n: FlatNode) => ({ x: n._x * (w - 140) + 70, y: 60 + n._d * 110 })
  const kindColor = (k?: string) =>
    ({
      function: 'var(--k-function)',
      method: 'var(--k-method)',
      type: 'var(--k-type)',
      interface: 'var(--k-interface)',
    }[k ?? ''] ?? 'var(--fg-2)')
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="100%">
      {edges.map(([a, b], i) => {
        const p = pos(acc[a])
        const q = pos(acc[b])
        return (
          <path
            key={i}
            d={`M${p.x},${p.y + 16} C${p.x},${(p.y + q.y) / 2} ${q.x},${(p.y + q.y) / 2} ${q.x},${q.y - 16}`}
            fill="none"
            stroke="var(--line-2)"
            strokeWidth="1"
          />
        )
      })}
      {acc.map((n, i) => {
        const p = pos(n)
        const tw = Math.max(70, n.name.length * 7.5 + 24)
        return (
          <g
            key={i}
            transform={`translate(${p.x - tw / 2},${p.y - 16})`}
            style={{ cursor: 'pointer' }}
            onClick={() => setSym(SYMBOLS[0])}
          >
            <rect
              width={tw}
              height={32}
              rx={6}
              fill="var(--bg-2)"
              stroke={n.caveat === 'hot' ? 'var(--pink)' : n.caveat === 'deprecated' ? 'var(--warn)' : 'var(--line-2)'}
              strokeWidth={n.caveat ? 1.4 : 1}
            />
            <circle cx={12} cy={16} r={4} fill={kindColor(n.kind)} />
            <text x={22} y={20} fontFamily="JetBrains Mono" fontSize="11" fill="var(--fg-0)">
              {n.name}
            </text>
            {n.caveat && (
              <circle cx={tw - 10} cy={10} r={3} fill={n.caveat === 'hot' ? 'var(--pink)' : 'var(--warn)'} />
            )}
          </g>
        )
      })}
    </svg>
  )
}

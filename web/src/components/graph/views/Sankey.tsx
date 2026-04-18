'use client'

const cols = [
  {
    title: 'Entry',
    items: [
      { name: 'POST /ingest/email', flow: 1.0, color: 'var(--accent)' },
      { name: 'POST /tucks',         flow: 0.7, color: 'var(--accent)' },
      { name: 'GET /auth/login',     flow: 0.4, color: 'var(--accent)' },
    ],
  },
  {
    title: 'Handler',
    items: [
      { name: 'EmailIngestHandler', flow: 1.0, color: 'var(--k-method)' },
      { name: 'SubmitTuck',          flow: 0.7, color: 'var(--k-method)' },
      { name: 'AuthLogin',           flow: 0.4, color: 'var(--k-method)' },
    ],
  },
  {
    title: 'Core',
    items: [
      { name: 'ExtractLinks', flow: 0.8, color: 'var(--k-function)' },
      { name: 'Authn',         flow: 0.9, color: 'var(--pink)' },
      { name: 'Validate',      flow: 0.6, color: 'var(--k-function)' },
    ],
  },
  {
    title: 'Store',
    items: [
      { name: 'PostgresTuckStore', flow: 1.2, color: 'var(--k-type)' },
      { name: 'RedisCache',         flow: 0.5, color: 'var(--k-type)' },
    ],
  },
  {
    title: 'Emit',
    items: [
      { name: 'push.TuckUpdated', flow: 0.8, color: 'var(--k-contract)' },
      { name: 'link.Extracted',    flow: 0.6, color: 'var(--k-contract)' },
    ],
  },
]

export function GraphSankey() {
  const W = 1080
  const H = 600
  const colW = W / cols.length

  const ribbons: React.ReactNode[] = []
  cols.forEach((c, ci) => {
    if (ci === cols.length - 1) return
    const next = cols[ci + 1]
    c.items.forEach((it, ii) => {
      next.items.forEach((jt, ji) => {
        const x1 = colW * ci + colW * 0.55
        const x2 = colW * (ci + 1) + colW * 0.25
        const y1 = 80 + ii * 80 + 18
        const y2 = 80 + ji * 80 + 18
        const flow = Math.min(it.flow, jt.flow) * (ii === ji ? 1 : 0.35)
        ribbons.push(
          <path
            key={`${ci}-${ii}-${ji}`}
            d={`M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}`}
            stroke={it.color}
            strokeOpacity="0.22"
            strokeWidth={flow * 14}
            fill="none"
          />,
        )
      })
    })
  })

  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="100%">
      {ribbons}
      {cols.map((c, ci) => (
        <g key={ci}>
          <text
            x={colW * ci + 16}
            y={30}
            fontFamily="IBM Plex Sans"
            fontSize="11"
            fill="var(--fg-2)"
            style={{ textTransform: 'uppercase', letterSpacing: '0.08em' }}
          >
            {c.title}
          </text>
          {c.items.map((it, ii) => (
            <g key={ii} transform={`translate(${colW * ci + 16},${80 + ii * 80})`}>
              <rect width={colW * 0.55 - 32} height={36} fill={it.color} opacity="0.18" rx={4} />
              <rect width={5} height={36} fill={it.color} rx={2} />
              <text x={14} y={22} fontFamily="JetBrains Mono" fontSize="11.5" fill="var(--fg-0)">
                {it.name}
              </text>
            </g>
          ))}
        </g>
      ))}
    </svg>
  )
}

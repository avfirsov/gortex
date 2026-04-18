'use client'

import type { ThreeDMode } from './Graph3D'

export function ThreeDThumb({ mode }: { mode: ThreeDMode }) {
  const common = { viewBox: '0 0 56 40', width: 56, height: 40 }
  if (mode === 'skyline')
    return (
      <svg {...common}>
        <path d="M4 30 L28 22 L52 30 L28 38 Z" fill="var(--line-1)" stroke="var(--line-2)" strokeWidth="0.6" />
        {[10, 18, 26, 34, 42].map((x, i) => (
          <line
            key={i}
            x1={x}
            y1={30 - (i % 2 === 0 ? 4 : 2)}
            x2={x}
            y2={12 - i * 1.5}
            stroke="var(--accent)"
            strokeWidth="1.6"
            strokeLinecap="round"
          />
        ))}
      </svg>
    )
  if (mode === 'strata')
    return (
      <svg {...common}>
        {[8, 18, 28, 38].map((y, i) => (
          <g key={i}>
            <path
              d={`M6 ${y} L42 ${y - 3} L50 ${y + 1} L14 ${y + 4} Z`}
              fill="var(--line-1)"
              stroke="var(--accent)"
              strokeOpacity="0.5"
              strokeWidth="0.5"
            />
          </g>
        ))}
        <line x1="22" y1="8" x2="26" y2="38" stroke="var(--pink)" strokeWidth="0.8" strokeDasharray="1 1" />
        <line x1="34" y1="8" x2="30" y2="38" stroke="var(--accent)" strokeWidth="0.6" strokeDasharray="1 1" />
      </svg>
    )
  if (mode === 'orbital')
    return (
      <svg {...common}>
        <ellipse cx="28" cy="20" rx="18" ry="7" fill="none" stroke="var(--line-2)" strokeWidth="0.5" />
        <ellipse cx="28" cy="20" rx="12" ry="4.5" fill="none" stroke="var(--line-2)" strokeWidth="0.5" />
        <ellipse cx="28" cy="20" rx="6" ry="2.2" fill="none" stroke="var(--line-2)" strokeWidth="0.5" />
        <circle cx="28" cy="20" r="2.2" fill="var(--accent)" />
        <circle cx="46" cy="20" r="1.4" fill="var(--accent)" />
        <circle cx="18" cy="16" r="1.4" fill="var(--pink)" />
        <circle cx="34" cy="24" r="1.4" fill="var(--accent)" />
      </svg>
    )
  if (mode === 'galaxies')
    return (
      <svg {...common}>
        <circle cx="16" cy="14" r="8" fill="var(--accent)" fillOpacity="0.1" />
        <circle cx="40" cy="26" r="9" fill="var(--pink)" fillOpacity="0.12" />
        {Array.from({ length: 14 }, (_, i) => {
          const cx = i < 7 ? 16 : 40
          const cy = i < 7 ? 14 : 26
          const a = (i % 7) * 0.9
          const r = 2 + (i % 4)
          return (
            <circle
              key={i}
              cx={cx + Math.cos(a) * r}
              cy={cy + Math.sin(a) * r * 0.8}
              r="0.9"
              fill={i < 7 ? 'var(--accent)' : 'var(--pink)'}
            />
          )
        })}
        <line x1="16" y1="14" x2="40" y2="26" stroke="var(--accent)" strokeOpacity="0.5" strokeWidth="0.5" />
      </svg>
    )
  if (mode === 'city')
    return (
      <svg {...common}>
        <path d="M4 32 L52 32 L40 38 L-8 38 Z" fill="var(--line-1)" stroke="none" />
        {[
          [8, 26, 4],
          [14, 22, 6],
          [22, 18, 5],
          [30, 14, 8],
          [38, 20, 4],
          [46, 24, 5],
        ].map(([x, y, h], i) => (
          <g key={i}>
            <rect x={x} y={y} width="5" height={h + 6} fill="var(--accent)" fillOpacity="0.55" stroke="var(--accent)" strokeWidth="0.4" />
            <polygon points={`${x},${y} ${x + 5},${y} ${x + 7},${y - 2} ${x + 2},${y - 2}`} fill="var(--accent)" fillOpacity="0.8" />
          </g>
        ))}
        <path d="M16 18 Q30 10 44 22" stroke="var(--pink)" strokeWidth="0.6" fill="none" strokeDasharray="1 1" />
      </svg>
    )
  return null
}

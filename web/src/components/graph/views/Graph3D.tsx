'use client'

import { useEffect, useState } from 'react'
import { ThreeDSkyline } from './Skyline'
import { ThreeDStrata } from './Strata'
import { ThreeDOrbital } from './Orbital'
import { ThreeDGalaxies } from './Galaxies'
import { ThreeDCity } from './City'
import { ThreeDThumb } from './ThreeDThumb'

export type ThreeDMode = 'skyline' | 'strata' | 'orbital' | 'galaxies' | 'city'

const MODES: { id: ThreeDMode; label: string; hint: string }[] = [
  { id: 'skyline',  label: 'Skyline',  hint: 'Repos as districts · symbols as pillars' },
  { id: 'strata',   label: 'Strata',   hint: 'Repos as planes · cross-repo rain' },
  { id: 'orbital',  label: 'Orbital',  hint: 'Entry at core · orbits = call depth' },
  { id: 'galaxies', label: 'Galaxies', hint: 'Force-graph w/ perspective depth' },
  { id: 'city',     label: 'City',     hint: 'Buildings = symbols · skybridges = contracts' },
]

export function Graph3D() {
  const [sub, setSub] = useState<ThreeDMode>('skyline')

  useEffect(() => {
    const stored = localStorage.getItem('gortex:3d') as ThreeDMode | null
    if (stored && MODES.some((m) => m.id === stored)) setSub(stored)
  }, [])

  useEffect(() => {
    localStorage.setItem('gortex:3d', sub)
  }, [sub])

  const mode = MODES.find((m) => m.id === sub) ?? MODES[0]

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%' }}>
      <div className="threeD-picker">
        <div className="threeD-picker-title">3D representation</div>
        <div className="threeD-picker-row">
          {MODES.map((m) => (
            <button
              key={m.id}
              type="button"
              className={`threeD-chip ${sub === m.id ? 'active' : ''}`}
              onClick={() => setSub(m.id)}
            >
              <ThreeDThumb mode={m.id} />
              <div className="threeD-chip-label">{m.label}</div>
            </button>
          ))}
        </div>
        <div className="threeD-picker-hint">{mode.hint}</div>
      </div>
      <div style={{ width: '100%', height: '100%' }}>
        {sub === 'skyline'  && <ThreeDSkyline />}
        {sub === 'strata'   && <ThreeDStrata />}
        {sub === 'orbital'  && <ThreeDOrbital />}
        {sub === 'galaxies' && <ThreeDGalaxies />}
        {sub === 'city'     && <ThreeDCity />}
      </div>
    </div>
  )
}

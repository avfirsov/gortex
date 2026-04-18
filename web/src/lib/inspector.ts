'use client'

import { create } from 'zustand'
import type { Symbol } from './seed'

type State = {
  sym: Symbol | null
  setSym: (sym: Symbol | null) => void
}

export const useInspector = create<State>((set) => ({
  sym: null,
  setSym: (sym) => set({ sym }),
}))

import type { ReactNode } from 'react'
import { RemoteMutationReadinessContext } from './remoteMutationReadiness'

export function RemoteMutationReadinessProvider({ ready, children }: { ready: boolean; children: ReactNode }) {
  return <RemoteMutationReadinessContext.Provider value={ready}>{children}</RemoteMutationReadinessContext.Provider>
}

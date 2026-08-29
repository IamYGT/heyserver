import { createContext, useContext } from 'react'

export const RemoteMutationReadinessContext = createContext(true)
export const remoteMutationUnavailableMessage = 'Managed server status is not current; retry fleet refresh before running remote actions'

export function useRemoteMutationReadiness() {
  return useContext(RemoteMutationReadinessContext)
}

export function requireRemoteMutationReadiness(ready: boolean) {
  if (!ready) throw new Error(remoteMutationUnavailableMessage)
}

import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { hostActionStatusEndpoint, type HostActionStatus } from '@/lib/hostControls'
import type { ManagedServerID } from '@/lib/serverNavigation'

export const hostActionStatusKey = (server: ManagedServerID) => ['host-action-status', server] as const

export function useHostActionStatus(server: ManagedServerID, enabled = true) {
  return useQuery<HostActionStatus>({
    queryKey: hostActionStatusKey(server),
    queryFn: () => api.get(hostActionStatusEndpoint(server)),
    enabled,
    refetchInterval: query => query.state.data?.running ? 1_000 : 5_000,
    refetchOnWindowFocus: true,
  })
}

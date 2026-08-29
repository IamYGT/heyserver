import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { SystemStats, ServiceStatus } from '@/lib/types'

export function useSystemStats(enabled = true, poll = enabled) {
  return useQuery<SystemStats>({
    queryKey: ['stats', 'system'],
    queryFn: () => api.get<SystemStats>('/system/stats'),
    enabled,
    refetchInterval: enabled && poll ? 5000 : false,
    staleTime: 3000,
  })
}

export function useServiceStatuses(enabled = true, poll = enabled) {
  return useQuery<ServiceStatus[]>({
    queryKey: ['stats', 'services'],
    queryFn: () => api.get<ServiceStatus[]>('/system/services'),
    enabled,
    refetchInterval: enabled && poll ? 10000 : false,
  })
}

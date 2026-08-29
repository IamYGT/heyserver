import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type {
  MetricsHistoryResponse,
  ProcessSnapshotResponse,
  ServiceHistoryEntry,
  MetricsSummary,
} from '@/lib/types'

export type MetricsRange = '1h' | '6h' | '24h' | '7d' | '30d'

/**
 * Fetches historical system metrics for the given time range.
 * Auto-selects resolution: raw (≤1h), minute (≤24h), hourly (>24h).
 */
export function useMetricsHistory(range: MetricsRange, poll = true) {
  return useQuery<MetricsHistoryResponse>({
    queryKey: ['metrics', 'history', range],
    queryFn: () => api.get<MetricsHistoryResponse>(`/metrics/history?range=${range}`),
    refetchInterval: poll ? (range === '1h' ? 15_000 : 60_000) : false,
    staleTime: range === '1h' ? 10_000 : 30_000,
  })
}

/**
 * Fetches the process snapshot closest to the given timestamp.
 * If no timestamp is provided, returns the latest snapshot.
 */
export function useProcessSnapshot(at?: string) {
  return useQuery<ProcessSnapshotResponse>({
    queryKey: ['metrics', 'processes', at ?? 'latest'],
    queryFn: () => {
      const q = at ? `?at=${encodeURIComponent(at)}` : ''
      return api.get<ProcessSnapshotResponse>(`/metrics/processes${q}`)
    },
    enabled: true,
  })
}

/**
 * Fetches available process snapshot timestamps for a range.
 */
export function useProcessTimestamps(range: MetricsRange) {
  return useQuery<string[]>({
    queryKey: ['metrics', 'processes', 'timestamps', range],
    queryFn: () => api.get<string[]>(`/metrics/processes/timestamps?range=${range}`),
    refetchInterval: 60_000,
  })
}

/**
 * Fetches service status history for the given time range.
 */
export function useServiceHistory(range: MetricsRange, poll = true) {
  return useQuery<ServiceHistoryEntry[]>({
    queryKey: ['metrics', 'services', 'history', range],
    queryFn: () => api.get<ServiceHistoryEntry[]>(`/metrics/services/history?range=${range}`),
    refetchInterval: poll ? 60_000 : false,
  })
}

/**
 * Fetches a quick summary of the metrics database.
 */
export function useMetricsSummary() {
  return useQuery<MetricsSummary>({
    queryKey: ['metrics', 'summary'],
    queryFn: () => api.get<MetricsSummary>('/metrics/summary'),
    staleTime: 60_000,
  })
}

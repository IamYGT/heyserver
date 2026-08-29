import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { hostActionStatusKey } from '@/hooks/useHostActionStatus'

// ── Types ────────────────────────────────────────────────────────────────────

export interface Partition {
  name: string
  device: string
  mountPoint: string
  fsType: string
  size: number
  used: number
  available: number
  usePercent: number
  label?: string
  uuid?: string
}

export interface IOStats {
  device: string
  readsCompleted: number
  writesCompleted: number
  sectorsRead: number
  sectorsWritten: number
  readBytes: number
  writeBytes: number
  ioInProgress: number
  ioTimeMs: number
}

export interface DiskOverview {
  partitions: Partition[]
  ioStats: IOStats[]
  totalSize: number
  totalUsed: number
  totalFree: number
}

export interface SmartInfo {
  available: boolean
  healthy: boolean
  device: string
  model?: string
  serial?: string
  firmware?: string
  status: string
  message?: string
  rawOutput?: string
}

export interface DirUsage {
  path: string
  size: number
  items?: number
}

export interface LargestFile {
  path: string
  size: number
  modified?: string
}

export interface CleanupTarget {
  id: string
  name: string
  description: string
  size: number
  scope: string
  risk: 'low' | 'medium'
}

export interface CleanupExecutionResult {
  id: string
  status: 'ok' | 'error'
  message: string
  reclaimed: number
}

export interface CleanupExecutionResponse {
  results: CleanupExecutionResult[]
  root_available_before: number
  root_available_after: number
}

export interface DiskAnalysisStatus {
  id?: string
  unit?: string
  status: 'idle' | 'queued' | 'running' | 'completed' | 'failed'
  message: string
  created_at?: string
  started_at?: string
  finished_at?: string
  root_size?: number
  root_used?: number
  root_available?: number
  entries: DirUsage[]
  errors?: string[]
}

export interface MountEntry {
  device: string
  mountPoint: string
  fsType: string
  options: string
  dump?: number
  pass?: number
  source: string
}

// ── Hooks ────────────────────────────────────────────────────────────────────

export function useDiskOverview() {
  return useQuery<DiskOverview>({
    queryKey: ['disk', 'overview'],
    queryFn: () => api.get<DiskOverview>('/disk/overview'),
    refetchInterval: 30_000,
  })
}

export function useDiskIO() {
  return useQuery<IOStats[]>({
    queryKey: ['disk', 'io'],
    queryFn: () => api.get<IOStats[]>('/disk/io'),
    refetchInterval: 5_000,
  })
}

export function useSmartInfo(device: string) {
  return useQuery<SmartInfo>({
    queryKey: ['disk', 'smart', device],
    queryFn: () => api.get<SmartInfo>(`/disk/smart/${encodeURIComponent(device)}`),
    staleTime: 60_000,
  })
}

export interface DirListEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  modified: string
  mode: string
  children?: number
}

export interface DirListResponse {
  path: string
  entries: DirListEntry[]
  count: number
}

export function useDirList(path: string) {
  return useQuery<DirListResponse>({
    queryKey: ['disk', 'list', path],
    queryFn: () => api.get<DirListResponse>(`/disk/list?path=${encodeURIComponent(path)}`),
    staleTime: 30_000,
  })
}

export function useDirSize(path: string, enabled: boolean = true) {
  return useQuery<{ path: string; size: number }>({
    queryKey: ['disk', 'dirsize', path],
    queryFn: () => api.get<{ path: string; size: number }>(`/disk/dirsize?path=${encodeURIComponent(path)}`),
    staleTime: 60_000,
    enabled,
  })
}

export function useDirUsage(path: string, depth: number = 1) {
  return useQuery<DirUsage[]>({
    queryKey: ['disk', 'usage', path, depth],
    queryFn: () => api.get<DirUsage[]>(`/disk/usage?path=${encodeURIComponent(path)}&depth=${depth}`),
    staleTime: 30_000,
  })
}

export function useLargestFiles(path: string, limit: number = 20, enabled: boolean = true) {
  return useQuery<LargestFile[]>({
    queryKey: ['disk', 'largest', path, limit],
    queryFn: () => api.get<LargestFile[]>(`/disk/largest?path=${encodeURIComponent(path)}&limit=${limit}`),
    staleTime: 30_000,
    enabled,
  })
}

export function useCleanupScan() {
  return useQuery<CleanupTarget[]>({
    queryKey: ['disk', 'cleanup', 'scan'],
    queryFn: () => api.get<CleanupTarget[]>('/disk/cleanup/scan'),
    staleTime: 60_000,
  })
}

export function useDiskAnalysisStatus() {
  return useQuery<DiskAnalysisStatus>({
    queryKey: ['disk', 'analysis', 'status'],
    queryFn: () => api.get<DiskAnalysisStatus>('/disk/analysis/status'),
    refetchInterval: 3_000,
  })
}

export function useDiskAnalysisStart() {
  const queryClient = useQueryClient()
  return useMutation<DiskAnalysisStatus>({
    mutationFn: () => api.post<DiskAnalysisStatus>('/disk/analysis/start'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['disk', 'analysis', 'status'] }),
  })
}

export function useCleanupExecute() {
  const queryClient = useQueryClient()
  return useMutation<CleanupExecutionResponse, Error, string[]>({
    mutationFn: (targets: string[]) =>
      api.post<CleanupExecutionResponse>('/disk/cleanup/execute', { targets }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['disk'] })
    },
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey('local') }),
  })
}

export function useDiskMounts() {
  return useQuery<MountEntry[]>({
    queryKey: ['disk', 'mounts'],
    queryFn: () => api.get<MountEntry[]>('/disk/mounts'),
    staleTime: 60_000,
  })
}

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import {
  Activity, AlertTriangle, Archive, Box, CheckCircle2, Clock, Code2, Cpu, Database, FileClock, FileCog, FolderOpen, Gauge, Globe2, HardDrive, Layers3, Loader2,
  Lock, MemoryStick, Plus, Power, RefreshCw, Rocket, RotateCcw, Search, Server, ShieldCheck, Skull, Terminal, Trash2, X, XCircle,
} from 'lucide-react'
import { api } from '@/lib/api'
import { useCurrentUser } from '@/hooks/useAuth'
import { useNow } from '@/hooks/useNow'
import { hostActionStatusKey, useHostActionStatus } from '@/hooks/useHostActionStatus'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { RemoteFiles } from '@/components/servers/RemoteFiles'
import { RemoteNginx } from '@/components/servers/RemoteNginx'
import { RemoteDomains } from '@/components/servers/RemoteDomains'
import { RemoteSSL } from '@/components/servers/RemoteSSL'
import { RemotePHP } from '@/components/servers/RemotePHP'
import { RemotePM2 } from '@/components/servers/RemotePM2'
import { RemoteCron } from '@/components/servers/RemoteCron'
import { RemoteFirewall } from '@/components/servers/RemoteFirewall'
import { RemoteDatabases } from '@/components/servers/RemoteDatabases'
import { RemoteBackups } from '@/components/servers/RemoteBackups'
import { RemoteDeploy } from '@/components/servers/RemoteDeploy'
import { RemoteMutationReadinessProvider } from '@/components/servers/RemoteMutationReadiness'
import { remoteMutationUnavailableMessage, useRemoteMutationReadiness } from '@/components/servers/remoteMutationReadiness'
import { AgentEnrollment } from '@/components/servers/AgentEnrollment'
import { AgentProfileCard } from '@/components/servers/AgentProfileCard'
import { RemoteAgentLifecycle } from '@/components/servers/RemoteAgentLifecycle'
import { cn } from '@/lib/utils'
import { waitForAgentTask, type AgentTask } from '@/lib/agentTasks'
import {
  compatibilityPresentation,
  displayReleaseVersion,
  summarizeFleetCompatibility,
  type AgentCompatibility,
  type CompatibilityPresentation,
} from '@/lib/agentCompatibility'
import { managedNodeOnline, serverSwitchTarget, serviceFocusMatches } from '@/lib/serverNavigation'
import { hostActionConfirmation, hostActionLabel, rebootControlState, rebootStatusQueryKey, type HostActionStatus, type RebootStatus } from '@/lib/hostControls'

interface ServiceState { name: string; active: string; sub?: string }
interface NodeInventory {
  os: string; arch?: string; kernel: string; boot_id: string; uptime_seconds: number; load_1: number
  memory_total_bytes: number; memory_available_bytes: number
  disk_total_bytes: number; disk_used_bytes?: number; disk_available_bytes: number; disk_use_percent?: number
  plesk_present: boolean; services: ServiceState[]; log_sources?: string[]
  file_read_roots?: string[]; file_write_roots?: string[]
}
interface ManagedNode {
  id: string; name: string; hostname: string; agent_version: string; protocol_version: string
  capabilities: string[]; inventory: NodeInventory; last_seen_at?: string; online?: boolean; compatibility?: AgentCompatibility
}
interface RemoteProcess { pid: number; startTime: number; user: string; cpu: number; memory: number; rss: number; command: string }
interface RemoteMemoryState {
  memory_total_bytes: number; memory_available_bytes: number
  swap_total_bytes: number; swap_used_bytes: number; swap_free_bytes: number
  swap_reset_eligible: boolean; swap_reset_reason?: string
}
interface ManagedNodeMetrics {
  observed_at: string
  cpu: { usage_percent: number; core_count: number }
  load: { one: number; five: number; fifteen: number }
  memory: { total_bytes: number; used_bytes: number; available_bytes: number; usage_percent: number }
  network: { rx_bytes: number; tx_bytes: number }
  root_disk: { total_bytes: number; used_bytes: number; available_bytes: number; usage_percent: number }
}
interface DiskMount { filesystem: string; size: number; used: number; available: number; use_percent: number; mountpoint: string }
interface RemoteCleanupTarget { id: string; name: string; description: string; size: number; risk: 'low' | 'medium' }
interface RemoteCleanupResponse { results: Array<{ id: string; status: 'ok' | 'error'; message: string; reclaimed: number }>; scan_error?: string }
interface RemoteCleanupReceipt { response: RemoteCleanupResponse; labels: Record<string, string> }
interface LogEntry { timestamp: string; unit: string; priority: number; message: string }
interface RemoteContainer { id: string; name: string; image: string; state: string; status: string; ports: string }

type TabID = 'overview' | 'services' | 'processes' | 'logs' | 'disk' | 'containers' | 'deploy' | 'domains' | 'ssl' | 'nginx' | 'php' | 'pm2' | 'cron' | 'firewall' | 'databases' | 'backups' | 'files'
const tabIDs = new Set<TabID>(['overview', 'services', 'processes', 'logs', 'disk', 'containers', 'deploy', 'domains', 'ssl', 'nginx', 'php', 'pm2', 'cron', 'firewall', 'databases', 'backups', 'files'])
const tabs: Array<{ id: TabID; label: string; icon: typeof Activity }> = [
  { id: 'overview', label: 'Overview', icon: Gauge },
  { id: 'services', label: 'Services', icon: Activity },
  { id: 'processes', label: 'Processes', icon: Cpu },
  { id: 'logs', label: 'Logs', icon: FileClock },
  { id: 'disk', label: 'Disk', icon: HardDrive },
  { id: 'containers', label: 'Docker', icon: Box },
  { id: 'deploy', label: 'Deploy', icon: Rocket },
  { id: 'domains', label: 'Domains', icon: Globe2 },
  { id: 'ssl', label: 'SSL', icon: ShieldCheck },
  { id: 'nginx', label: 'Nginx', icon: FileCog },
  { id: 'php', label: 'PHP', icon: Code2 },
  { id: 'pm2', label: 'PM2', icon: Cpu },
  { id: 'cron', label: 'Cron', icon: Clock },
  { id: 'firewall', label: 'Firewall', icon: Lock },
  { id: 'databases', label: 'Database', icon: Database },
  { id: 'backups', label: 'Backups', icon: Archive },
  { id: 'files', label: 'Files', icon: FolderOpen },
]
function isManageableService(name: string) {
  return name === 'nginx.service'
    || name === 'mariadb.service'
    || /^php\d+\.\d+-fpm\.service$/.test(name)
    || /^postgresql@\d+-main\.service$/.test(name)
    || /^pm2-.+\.service$/.test(name)
}
const hostActions = [
  { id: 'memory-optimize', label: 'Optimize RAM', description: 'Release caches; keep processes and swap running', icon: MemoryStick, destructive: false },
  { id: 'swap-reset', label: 'Reset swap', description: 'Cycle all configured swap', icon: RotateCcw, destructive: false },
  { id: 'temp-clean', label: 'Clean temp', description: 'Apply tmpfiles expiry policy', icon: Trash2, destructive: false },
  { id: 'reboot', label: 'Reboot', description: 'Schedule reboot in 10 seconds', icon: Power, destructive: true },
] as const
const logSources = [
  ['system', 'System'], ['nginx', 'Nginx'], ['php', 'PHP-FPM'], ['mariadb', 'MariaDB'],
  ['postgresql', 'PostgreSQL'], ['pm2', 'PM2'], ['docker', 'Docker'],
] as const
type LogSourceID = typeof logSources[number][0]
type LogLevelFilter = 'all' | 'error' | 'warning' | 'info'
const logSourceIDs = new Set<string>(logSources.map(([id]) => id))
const logLevelFilters = new Set<LogLevelFilter>(['all', 'error', 'warning', 'info'])

function serviceLogSource(service: string): LogSourceID {
  const normalized = service.toLowerCase()
  if (normalized.startsWith('nginx')) return 'nginx'
  if (normalized.startsWith('php')) return 'php'
  if (normalized.startsWith('mariadb') || normalized.startsWith('mysql')) return 'mariadb'
  if (normalized.startsWith('postgresql')) return 'postgresql'
  if (normalized.startsWith('pm2')) return 'pm2'
  if (normalized.startsWith('docker')) return 'docker'
  return 'system'
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** power).toFixed(power > 2 ? 1 : 0)} ${units[power]}`
}
function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  return days > 0 ? `${days}d ${hours}h` : `${hours}h`
}
function formatTimestamp(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const managedMetricsStaleAfterMs = 60_000

function isFiniteNonNegative(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function isValidManagedNodeMetrics(value: unknown): value is ManagedNodeMetrics {
  if (!value || typeof value !== 'object') return false
  const metrics = value as Record<string, unknown>
  if (typeof metrics.observed_at !== 'string' || !Number.isFinite(new Date(metrics.observed_at).getTime())) return false
  if (!metrics.cpu || typeof metrics.cpu !== 'object' || !metrics.load || typeof metrics.load !== 'object' || !metrics.memory || typeof metrics.memory !== 'object' || !metrics.network || typeof metrics.network !== 'object' || !metrics.root_disk || typeof metrics.root_disk !== 'object') return false

  const cpu = metrics.cpu as Record<string, unknown>
  const load = metrics.load as Record<string, unknown>
  const memory = metrics.memory as Record<string, unknown>
  const network = metrics.network as Record<string, unknown>
  const rootDisk = metrics.root_disk as Record<string, unknown>
  return isFiniteNonNegative(cpu.usage_percent)
    && cpu.usage_percent <= 100
    && Number.isInteger(cpu.core_count)
    && typeof cpu.core_count === 'number'
    && cpu.core_count > 0
    && isFiniteNonNegative(load.one)
    && isFiniteNonNegative(load.five)
    && isFiniteNonNegative(load.fifteen)
    && isFiniteNonNegative(memory.total_bytes)
    && isFiniteNonNegative(memory.used_bytes)
    && isFiniteNonNegative(memory.available_bytes)
    && isFiniteNonNegative(memory.usage_percent)
    && memory.usage_percent <= 100
    && isFiniteNonNegative(network.rx_bytes)
    && isFiniteNonNegative(network.tx_bytes)
    && isFiniteNonNegative(rootDisk.total_bytes)
    && isFiniteNonNegative(rootDisk.used_bytes)
    && isFiniteNonNegative(rootDisk.available_bytes)
    && isFiniteNonNegative(rootDisk.usage_percent)
    && rootDisk.usage_percent <= 100
}

function metricsErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

export default function Servers() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const nodeID = searchParams.get('node') ?? ''
  const nodeBase = nodeID ? `/nodes/${encodeURIComponent(nodeID)}` : ''
  const requestedTab = searchParams.get('tab') as TabID | null
  const tab: TabID = requestedTab && tabIDs.has(requestedTab) ? requestedTab : 'overview'
  const requestedLogSource = searchParams.get('source')
  const logSearch = searchParams.get('q') ?? ''
  const requestedLogLevel = searchParams.get('level') as LogLevelFilter | null
  const logLevel: LogLevelFilter = requestedLogLevel && logLevelFilters.has(requestedLogLevel) ? requestedLogLevel : 'all'
  const [logSourceChoice, setLogSourceChoice] = useState<LogSourceID>(
    requestedLogSource && logSourceIDs.has(requestedLogSource) ? requestedLogSource as LogSourceID : 'system',
  )
  const [showEnrollment, setShowEnrollment] = useState(false)
  const logSource = tab === 'logs' && requestedLogSource && logSourceIDs.has(requestedLogSource)
    ? requestedLogSource as LogSourceID
    : logSourceChoice
  const selectTab = (next: TabID) => {
    const params = new URLSearchParams()
    if (nodeID) params.set('node', nodeID)
    if (next !== 'overview') params.set('tab', next)
    if (next === 'logs') params.set('source', logSource)
    setSearchParams(params)
  }
  const selectLogSource = (source: string) => {
    if (!logSourceIDs.has(source)) return
    setLogSourceChoice(source as LogSourceID)
    const params = new URLSearchParams({ tab: 'logs', source })
    if (nodeID) params.set('node', nodeID)
    setSearchParams(params)
  }
  const openServiceLogs = (service: string) => {
    const source = serviceLogSource(service)
    setLogSourceChoice(source)
    const params = new URLSearchParams({ tab: 'logs', source, service })
    if (nodeID) params.set('node', nodeID)
    setSearchParams(params)
  }
  const setLogFilters = (query: string, level: LogLevelFilter, source: LogSourceID = logSource) => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', 'logs')
    next.set('source', source)
    if (query) next.set('q', query)
    else next.delete('q')
    if (level === 'all') next.delete('level')
    else next.set('level', level)
    setSearchParams(next, { replace: true })
  }
  const localCounterpart = serverSwitchTarget('local', '/servers', searchParams.size ? `?${searchParams.toString()}` : '')
  const { data: currentUser } = useCurrentUser()

  const { data: nodes = [], isLoading, isFetching: nodesFetching, error: nodesError, refetch: refetchNodes } = useQuery<ManagedNode[]>({
    queryKey: ['managed-nodes'], queryFn: () => api.get('/nodes'), refetchInterval: 5000,
  })
  const fleetCompatibility = summarizeFleetCompatibility(nodes)
  useEffect(() => {
    if (isLoading || nodeID || nodes.length === 0) return
    const next = new URLSearchParams(searchParams)
    next.set('node', nodes[0].id)
    setSearchParams(next, { replace: true })
  }, [isLoading, nodeID, nodes, searchParams, setSearchParams])
  const now = useNow()
  const selectedNode = nodes.find((node) => node.id === nodeID)
  const selectedNodeLabel = selectedNode?.name || selectedNode?.hostname || nodeID || 'Managed server'
  const online = managedNodeOnline(selectedNode, now)
  const fleetStateCurrent = !nodesError
  const remoteControlsReady = online && fleetStateCurrent
  const remoteControlsReadyRef = useRef(remoteControlsReady)
  useEffect(() => {
    remoteControlsReadyRef.current = remoteControlsReady
  }, [remoteControlsReady])
  const requireRemoteControlsReady = () => {
    if (!remoteControlsReadyRef.current) throw new Error(remoteMutationUnavailableMessage)
  }
  const hostActionAvailable = selectedNode?.capabilities?.includes('host.action') ?? false
  const serviceActionAvailable = selectedNode?.capabilities?.includes('service.action') ?? false
  const terminalAvailable = selectedNode?.capabilities?.includes('terminal') ?? false
  const diskCleanupAvailable = selectedNode?.capabilities?.includes('disk.cleanup') ?? false
  const configuredLogSources = (selectedNode?.inventory.log_sources ?? []).filter((source): source is LogSourceID => logSourceIDs.has(source))
  const logsReadAvailable = (selectedNode?.capabilities?.includes('logs.read') ?? false) && configuredLogSources.length > 0
  const effectiveLogSource = configuredLogSources.includes(logSource) ? logSource : (configuredLogSources[0] ?? logSource)
  const containerReadAvailable = selectedNode?.capabilities?.includes('container.read') ?? false
  const containerActionAvailable = selectedNode?.capabilities?.includes('container.action') ?? false
  const nginxActionAvailable = selectedNode?.capabilities?.includes('nginx.action') ?? false
  const nginxConfigReadAvailable = selectedNode?.capabilities?.includes('nginx.config.read') ?? false
  const nginxConfigWriteAvailable = selectedNode?.capabilities?.includes('nginx.config.write') ?? false
  const phpReadAvailable = selectedNode?.capabilities?.includes('php.read') ?? false
  const phpWriteAvailable = selectedNode?.capabilities?.includes('php.write') ?? false
  const phpActionAvailable = selectedNode?.capabilities?.includes('php.action') ?? false
  const pm2ReadAvailable = selectedNode?.capabilities?.includes('pm2.read') ?? false
  const pm2ActionAvailable = selectedNode?.capabilities?.includes('pm2.action') ?? false
  const cronReadAvailable = selectedNode?.capabilities?.includes('cron.read') ?? false
  const cronWriteAvailable = selectedNode?.capabilities?.includes('cron.write') ?? false
  const cronRunAvailable = selectedNode?.capabilities?.includes('cron.run') ?? false
  const firewallReadAvailable = selectedNode?.capabilities?.includes('firewall.read') ?? false
  const firewallWriteAvailable = selectedNode?.capabilities?.includes('firewall.write') ?? false
  const domainReadAvailable = selectedNode?.capabilities?.includes('domain.read') ?? false
  const domainActionAvailable = selectedNode?.capabilities?.includes('domain.action') ?? false
  const sslReadAvailable = selectedNode?.capabilities?.includes('ssl.read') ?? false
  const sslActionAvailable = selectedNode?.capabilities?.includes('ssl.action') ?? false
  const databaseReadAvailable = selectedNode?.capabilities?.includes('database.read') ?? false
  const databaseActionAvailable = selectedNode?.capabilities?.includes('database.action') ?? false
  const backupReadAvailable = selectedNode?.capabilities?.includes('backup.read') ?? false
  const backupRunAvailable = selectedNode?.capabilities?.includes('backup.run') ?? false
  const fileReadAvailable = selectedNode?.capabilities?.includes('files.read') ?? false
  const fileWriteAvailable = selectedNode?.capabilities?.includes('files.write') ?? false
  const fileReadRoots = selectedNode?.inventory.file_read_roots ?? []
  const fileWriteRoots = selectedNode?.inventory.file_write_roots ?? []
  const deployReadAvailable = selectedNode?.capabilities?.includes('deploy.read') ?? false
  const deployActionAvailable = selectedNode?.capabilities?.includes('deploy.action') ?? false
  const deployDomainReadAvailable = selectedNode?.capabilities?.includes('deploy.domain.read') ?? false
  const deployDomainActionAvailable = selectedNode?.capabilities?.includes('deploy.domain.action') ?? false
  const agentUpdateReadAvailable = selectedNode?.capabilities?.includes('agent.update.read') ?? false
  const agentUpdateActionAvailable = selectedNode?.capabilities?.includes('agent.update.action') ?? false
  const processReadAvailable = selectedNode?.capabilities?.includes('process.read') ?? false
  const processSignalAvailable = selectedNode?.capabilities?.includes('process.signal') ?? false
  const metricsReadAvailable = selectedNode?.capabilities?.includes('metrics.read') ?? false
  const activeHostAction = useHostActionStatus(nodeID, online && hostActionAvailable)
  const hostActionStatusUnavailable = online && hostActionAvailable && (activeHostAction.isLoading || activeHostAction.isError)

  const processesQuery = useQuery<RemoteProcess[]>({
    queryKey: ['managed-node-processes', nodeID],
    queryFn: () => api.get(nodeBase + '/processes'), enabled: online && processReadAvailable && tab === 'processes', refetchInterval: tab === 'processes' ? 10_000 : false,
  })
  const diskQuery = useQuery<DiskMount[]>({
    queryKey: ['managed-node-disk', nodeID], queryFn: () => api.get(nodeBase + '/disk'), enabled: online && tab === 'disk', refetchInterval: tab === 'disk' ? 30_000 : false,
  })
  const memoryQuery = useQuery<RemoteMemoryState>({
    queryKey: ['managed-node-memory', nodeID], queryFn: () => api.get(nodeBase + '/memory'),
    enabled: online && tab === 'overview', refetchInterval: tab === 'overview' ? 10_000 : false,
  })
  const metricsQuery = useQuery<ManagedNodeMetrics>({
    queryKey: ['managed-node-metrics', nodeID],
    queryFn: () => api.get<ManagedNodeMetrics>(nodeBase + '/metrics'),
    enabled: online && metricsReadAvailable && tab === 'overview',
    refetchInterval: tab === 'overview' && metricsReadAvailable ? 10_000 : false,
    retry: false,
  })
  const logsQuery = useQuery<LogEntry[]>({
    queryKey: ['managed-node-logs', nodeID, effectiveLogSource], queryFn: () => api.get(`${nodeBase}/logs?source=${effectiveLogSource}&lines=200`), enabled: online && logsReadAvailable && tab === 'logs', refetchInterval: tab === 'logs' && logsReadAvailable ? 15_000 : false,
  })
  const containersQuery = useQuery<RemoteContainer[]>({
    queryKey: ['managed-node-containers', nodeID], queryFn: () => api.get(nodeBase + '/containers'), enabled: online && containerReadAvailable && tab === 'containers', refetchInterval: tab === 'containers' && containerReadAvailable ? 10_000 : false,
  })
  const serviceTasksQuery = useQuery<AgentTask[]>({
    queryKey: ['managed-node-tasks', nodeID],
    queryFn: () => api.get(nodeBase + '/tasks?limit=12'),
    enabled: online && tab === 'services',
    refetchInterval: tab === 'services' ? 5_000 : false,
  })
  const remoteRebootStatus = useQuery<RebootStatus>({
    queryKey: rebootStatusQueryKey(nodeID),
    queryFn: () => api.get(nodeBase + '/actions/reboot-status'),
    enabled: online && hostActionAvailable && tab === 'overview',
    refetchInterval: tab === 'overview'
      ? (query) => query.state.data?.pending ? 1_000 : 10_000
      : false,
  })
  const remoteRebootControl = rebootControlState(remoteRebootStatus.data, remoteRebootStatus)

  const serviceAction = useMutation<AgentTask, Error, { service: string; action: 'start' | 'stop' | 'restart' }>({
    mutationFn: async ({ service, action }) => {
      requireRemoteControlsReady()
      const task = await api.post<AgentTask>(nodeBase + '/tasks', { kind: 'service.action', payload: { service, action }, confirmed: true })
      return waitForAgentTask(task, (taskID) => api.get<AgentTask>(`${nodeBase}/tasks/${taskID}`))
    },
    onSuccess: (task, variables) => {
      const finalState = [task.result?.active, task.result?.sub].filter(Boolean).join('/')
      toast.success(`${variables.service} ${variables.action} completed${finalState ? ` · ${finalState}` : ''}`)
      queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
      queryClient.invalidateQueries({ queryKey: ['managed-node-tasks', nodeID] })
    },
    onError: (error) => toast.error(error.message || 'Service action failed'),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['managed-node-tasks', nodeID] }),
  })
  const cancelRemoteReboot = useMutation<{ message: string }, Error>({
    mutationFn: () => { requireRemoteControlsReady(); return api.post(nodeBase + '/actions/reboot-cancel') },
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey(nodeID) })
    },
    onError: async (error) => {
      toast.error(error.message || 'Could not cancel the selected server reboot')
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', nodeID] })
    },
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey(nodeID) }),
  })
  const hostAction = useMutation<{ message: string }, Error, string>({
    mutationFn: (action) => { requireRemoteControlsReady(); return api.post(`${nodeBase}/actions/${action}`) },
    onSuccess: (result, action) => {
      if (action === 'reboot') {
        toast.success(result.message, {
          duration: 10_000,
          action: { label: 'Cancel reboot', onClick: () => cancelRemoteReboot.mutate() },
        })
      } else {
        toast.success(result.message)
      }
      queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
      queryClient.invalidateQueries({ queryKey: ['managed-node-memory', nodeID] })
      if (action === 'reboot') {
        queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey(nodeID) })
      }
    },
    onError: async (error) => {
      toast.error(error.message || 'Remote action failed')
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', nodeID] })
    },
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey(nodeID) }),
  })
  const processSignal = useMutation<{ message: string; exited: boolean; confirmed: boolean }, Error, { pid: number; startTime: number; signal: 'term' | 'kill' }>({
    mutationFn: (payload) => { requireRemoteControlsReady(); return api.post(nodeBase + '/processes/signal', payload) },
    onSuccess: async (result) => {
      if (result.exited) toast.success(result.message)
      else toast.warning(result.message)
      await processesQuery.refetch()
    },
    onError: (error) => toast.error(error.message || 'Process action failed'),
  })
  const containerAction = useMutation<{ message: string }, Error, { container: string; action: 'start' | 'stop' | 'restart' }>({
    mutationFn: ({ container, action }) => { requireRemoteControlsReady(); return api.post(`${nodeBase}/containers/${encodeURIComponent(container)}/actions/${action}`) },
    onSuccess: async (result) => { toast.success(result.message); await containersQuery.refetch() },
    onError: (error) => toast.error(error.message || 'Container action failed'),
  })
  const memoryTotal = memoryQuery.data?.memory_total_bytes ?? selectedNode?.inventory.memory_total_bytes ?? 0
  const memoryAvailable = memoryQuery.data?.memory_available_bytes ?? selectedNode?.inventory.memory_available_bytes ?? 0
  const memoryUsed = Math.max(0, memoryTotal - memoryAvailable)

  if (isLoading) return <div className="flex h-64 items-center justify-center text-zinc-500"><Loader2 className="size-5 animate-spin" /></div>
  if (nodesError && nodes.length === 0) return <div className="space-y-5 pb-8"><div><p className="text-xs font-semibold uppercase tracking-[0.2em] text-violet-400">Fleet control</p><h1 className="mt-1 text-2xl font-bold text-zinc-100">Managed Servers</h1><p className="mt-1 text-sm text-zinc-500">Operate local and enrolled servers from one control plane.</p></div><Card className="border-red-500/25 bg-red-500/[0.05]"><RemoteDataError title="Could not load managed servers" error={nodesError} onRetry={() => refetchNodes()} /></Card></div>
  return <div className="space-y-5 pb-8">
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div><p className="text-xs font-semibold uppercase tracking-[0.2em] text-violet-400">Fleet control</p><h1 className="mt-1 text-2xl font-bold text-zinc-100">Managed Servers</h1><p className="mt-1 text-sm text-zinc-500">Operate local and enrolled servers from one control plane.</p></div>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => setShowEnrollment((current) => !current)}><Plus className="size-4" /> Add server</Button>
        <Button onClick={() => navigate(`/terminal?${new URLSearchParams({ node: nodeID }).toString()}`)} disabled={!remoteControlsReady || !terminalAvailable} title={!fleetStateCurrent ? remoteMutationUnavailableMessage : !terminalAvailable ? 'Writable terminal is not enabled on this agent' : undefined}><Terminal className="size-4" /> Open {selectedNodeLabel} terminal</Button>
      </div>
    </div>
    {showEnrollment && <AgentEnrollment onClose={() => setShowEnrollment(false)} onRegistered={async (registeredNodeID) => { await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] }); const next = new URLSearchParams(); next.set('node', registeredNodeID); setSearchParams(next) }} />}
    {nodes.length > 0 && <FleetCompatibility summary={fleetCompatibility} />}
    <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
      <button onClick={() => navigate(localCounterpart)} className="rounded-2xl border border-zinc-800 bg-zinc-900/80 p-4 text-left transition hover:border-violet-500/50"><ServerCard name="HServer" detail="Open the matching local control" online /></button>
      {nodes.map((node) => {
        const nodeOnline = managedNodeOnline(node, now)
        const compatibility = compatibilityPresentation(node.compatibility, node.agent_version, node.protocol_version)
        return <button key={node.id} onClick={() => navigate(serverSwitchTarget(node.id, '/servers', searchParams.size ? `?${searchParams.toString()}` : ''))} className={cn('rounded-2xl border p-4 text-left transition hover:border-violet-500/50', node.id === nodeID ? 'border-violet-500/40 bg-violet-500/[0.04]' : 'border-zinc-800 bg-zinc-900/80')}><ServerCard name={node.name || node.hostname || node.id} detail={node.hostname || node.id} online={nodeOnline} compatibility={compatibility} /></button>
      })}
    </div>

    {nodesError && nodes.length > 0 && <Card data-testid="managed-nodes-refresh-error" className="border-amber-500/25 bg-amber-500/[0.05]"><CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between"><div className="flex min-w-0 items-start gap-3"><AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-400" /><div><p className="text-xs font-semibold text-amber-300">Managed server status refresh failed</p><p className="mt-1 text-[10px] text-zinc-400">{nodesError.message || 'The control plane did not return the current managed server state.'}</p><p className="mt-1 text-[10px] leading-relaxed text-zinc-500">Cached inventory remains visible, but all remote mutations are paused until refresh succeeds.</p></div></div><Button variant="outline" size="xs" disabled={nodesFetching} onClick={() => refetchNodes()}><RefreshCw className={cn('size-3', nodesFetching && 'animate-spin')} /> {nodesFetching ? 'Retrying…' : 'Retry fleet refresh'}</Button></CardContent></Card>}

    {!selectedNode ? <Card><CardContent className="p-8 text-center text-sm text-zinc-500">{nodes.length === 0 ? 'No managed agents are registered yet. Enroll an agent to add the first remote server.' : 'Select a registered server to open its controls.'}</CardContent></Card> : <RemoteMutationReadinessProvider ready={remoteControlsReady}>
      <div className="flex gap-1 overflow-x-auto rounded-xl border border-zinc-800 bg-zinc-900/70 p-1">
        {tabs.map(({ id, label, icon: Icon }) => <button key={id} onClick={() => selectTab(id)} className={cn('flex shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-xs font-medium transition', tab === id ? 'bg-violet-500/15 text-violet-300' : 'text-zinc-500 hover:bg-zinc-800 hover:text-zinc-300')}><Icon className="size-3.5" />{label}</button>)}
      </div>

      {!online && <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between"><div className="flex min-w-0 items-start gap-3"><AlertTriangle className="mt-0.5 size-4 shrink-0 text-red-400" /><div><p className="text-xs font-semibold text-red-300">{selectedNodeLabel} connection lost</p><p className="mt-1 text-[10px] leading-relaxed text-zinc-500">The last heartbeat was {selectedNode.last_seen_at ? formatTimestamp(selectedNode.last_seen_at) : 'not reported'}. Cached inventory may be stale; remote controls are paused until the agent reconnects.</p></div></div><Button variant="outline" size="xs" disabled={nodesFetching} onClick={() => refetchNodes()}><RefreshCw className={cn('size-3', nodesFetching && 'animate-spin')} /> {nodesFetching ? 'Checking…' : 'Check again'}</Button></CardContent></Card>}

      {tab === 'overview' && <Overview selectedNode={selectedNode} serverLabel={selectedNodeLabel} memoryUsed={memoryUsed} memoryAvailable={memoryAvailable} memoryState={memoryQuery.data} memoryError={memoryQuery.error} retryMemory={() => memoryQuery.refetch()} metricsQuery={metricsQuery} metricsReadAvailable={metricsReadAvailable} now={now} online={online} hostActionAvailable={hostActionAvailable} hostAction={hostAction} actionStatus={activeHostAction.data} actionStatusUnavailable={hostActionStatusUnavailable} actionStatusError={activeHostAction.isError} actionStatusFetching={activeHostAction.isFetching} retryActionStatus={() => activeHostAction.refetch()} rebootControl={remoteRebootControl} retryRebootStatus={() => remoteRebootStatus.refetch()} cancelReboot={cancelRemoteReboot} agentUpdateReadAvailable={agentUpdateReadAvailable} agentUpdateActionAvailable={agentUpdateActionAvailable} canManageAgentProfile={currentUser?.role === 'admin'} />}
      {tab === 'services' && <Services services={selectedNode.inventory.services} serverLabel={selectedNodeLabel} online={online} serviceActionAvailable={serviceActionAvailable} mutation={serviceAction} tasksQuery={serviceTasksQuery} focusedService={searchParams.get('service')} onOpenLogs={openServiceLogs} />}
      {tab === 'processes' && <Processes query={processesQuery} mutation={processSignal} online={online} processReadAvailable={processReadAvailable} processSignalAvailable={processSignalAvailable} />}
      {tab === 'logs' && <Logs source={effectiveLogSource} sources={configuredLogSources} setSource={selectLogSource} query={logsQuery} focusedService={searchParams.get('service')} search={logSearch} level={logLevel} setFilters={(search, level) => setLogFilters(search, level, effectiveLogSource)} online={online} logsReadAvailable={logsReadAvailable} />}
			{tab === 'disk' && <Disks nodeID={nodeID} query={diskQuery} online={online} diskCleanupAvailable={diskCleanupAvailable} actionStatus={activeHostAction.data} actionStatusUnavailable={hostActionStatusUnavailable} actionStatusError={activeHostAction.isError} retryActionStatus={() => activeHostAction.refetch()} />}
      {tab === 'containers' && <Containers query={containersQuery} mutation={containerAction} online={online} containerReadAvailable={containerReadAvailable} containerActionAvailable={containerActionAvailable} />}
      {tab === 'deploy' && <RemoteDeploy nodeID={nodeID} online={online} terminalAvailable={terminalAvailable} readAvailable={deployReadAvailable} actionAvailable={deployActionAvailable} domainReadAvailable={deployDomainReadAvailable} domainActionAvailable={deployDomainActionAvailable} />}
      {tab === 'domains' && <RemoteDomains nodeID={nodeID} online={online} readAvailable={domainReadAvailable} actionAvailable={domainActionAvailable} />}
      {tab === 'ssl' && <RemoteSSL nodeID={nodeID} online={online} readAvailable={sslReadAvailable} actionAvailable={sslActionAvailable} />}
      {tab === 'nginx' && <RemoteNginx nodeID={nodeID} online={online} actionAvailable={nginxActionAvailable} configReadAvailable={nginxConfigReadAvailable} configWriteAvailable={nginxConfigWriteAvailable} />}
      {tab === 'php' && <RemotePHP nodeID={nodeID} online={online} configReadAvailable={phpReadAvailable} configWriteAvailable={phpWriteAvailable} actionAvailable={phpActionAvailable} />}
      {tab === 'pm2' && <RemotePM2 nodeID={nodeID} online={online} terminalAvailable={terminalAvailable} readAvailable={pm2ReadAvailable} actionAvailable={pm2ActionAvailable} />}
      {tab === 'cron' && <RemoteCron nodeID={nodeID} online={online} readAvailable={cronReadAvailable} writeAvailable={cronWriteAvailable} runAvailable={cronRunAvailable} />}
      {tab === 'firewall' && <RemoteFirewall nodeID={nodeID} online={online} readAvailable={firewallReadAvailable} writeAvailable={firewallWriteAvailable} />}
      {tab === 'databases' && <RemoteDatabases nodeID={nodeID} online={online} readAvailable={databaseReadAvailable} actionAvailable={databaseActionAvailable} onBackups={() => selectTab('backups')} />}
      {tab === 'backups' && <RemoteBackups nodeID={nodeID} online={online} readAvailable={backupReadAvailable} runAvailable={backupRunAvailable} />}
      {tab === 'files' && <RemoteFiles key={`${nodeID}:${fileReadRoots.join(',')}:${fileWriteRoots.join(',')}`} nodeID={nodeID} online={online} readAvailable={fileReadAvailable} writeAvailable={fileWriteAvailable} readRoots={fileReadRoots} writeRoots={fileWriteRoots} />}
    </RemoteMutationReadinessProvider>}
  </div>
}

function Overview({ selectedNode, serverLabel, memoryUsed, memoryAvailable, memoryState, memoryError, retryMemory, metricsQuery, metricsReadAvailable, now, online, hostActionAvailable, hostAction, actionStatus, actionStatusUnavailable, actionStatusError, actionStatusFetching, retryActionStatus, rebootControl, retryRebootStatus, cancelReboot, agentUpdateReadAvailable, agentUpdateActionAvailable, canManageAgentProfile }: { selectedNode: ManagedNode; serverLabel: string; memoryUsed: number; memoryAvailable: number; memoryState?: RemoteMemoryState; memoryError: Error | null; retryMemory: () => void; metricsQuery: ReturnType<typeof useQuery<ManagedNodeMetrics>>; metricsReadAvailable: boolean; now: number; online: boolean; hostActionAvailable: boolean; hostAction: ReturnType<typeof useMutation<{ message: string }, Error, string>>; actionStatus?: HostActionStatus; actionStatusUnavailable: boolean; actionStatusError: boolean; actionStatusFetching: boolean; retryActionStatus: () => void; rebootControl: ReturnType<typeof rebootControlState>; retryRebootStatus: () => void; cancelReboot: ReturnType<typeof useMutation<{ message: string }, Error>>; agentUpdateReadAvailable: boolean; agentUpdateActionAvailable: boolean; canManageAgentProfile: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const controlMemory = memoryState ? { total: memoryState.swap_total_bytes, used: memoryState.swap_used_bytes, available: memoryState.memory_available_bytes } : undefined
  const swapRequired = (memoryState?.swap_used_bytes ?? 0) + 512 * 1024 * 1024
  const rebootPending = rebootControl.pending
  const diskUsed = selectedNode.inventory.disk_used_bytes && selectedNode.inventory.disk_used_bytes > 0
    ? selectedNode.inventory.disk_used_bytes
    : Math.max(0, selectedNode.inventory.disk_total_bytes - selectedNode.inventory.disk_available_bytes)
  const diskUsePercent = selectedNode.inventory.disk_use_percent ?? (selectedNode.inventory.disk_total_bytes > 0 ? (diskUsed / selectedNode.inventory.disk_total_bytes) * 100 : 0)
  return <>
		{!hostActionAvailable && <Card className="border-amber-500/25 bg-amber-500/[0.05]"><CardContent className="p-3 text-xs text-amber-300"><strong>Host actions are not enabled for this agent.</strong><span className="ml-1 text-amber-300/70">Add <code>HSERVER_AGENT_ALLOWED_HOST_ACTIONS</code> to the agent environment and restart the agent.</span></CardContent></Card>}
		{actionStatusUnavailable && <Card className="border-amber-500/25 bg-amber-500/[0.05]"><CardContent className="flex items-center justify-between gap-3 p-3 text-xs text-amber-300"><span>{actionStatusError ? (actionStatusFetching ? 'Retrying active selected server maintenance status…' : 'Could not verify active selected server maintenance. Host controls are paused.') : 'Checking active selected server maintenance before enabling host controls…'}</span>{actionStatusError && <Button type="button" variant="ghost" size="xs" disabled={actionStatusFetching} onClick={retryActionStatus}><RefreshCw className={cn('size-3', actionStatusFetching && 'animate-spin')} /> Retry</Button>}</CardContent></Card>}
    {memoryError && <Card className="border-red-500/25 bg-red-500/[0.05]"><RemoteDataError title="Could not refresh selected server memory and swap state" error={memoryError} onRetry={retryMemory} disabled={!online} /></Card>}
    <ManagedMetricsPanel query={metricsQuery} readAvailable={metricsReadAvailable} online={online} now={now} />
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
      <Metric icon={Cpu} label="Load" value={selectedNode.inventory.load_1.toFixed(2)} detail={formatUptime(selectedNode.inventory.uptime_seconds)} />
      <Metric icon={MemoryStick} label="Memory" value={formatBytes(memoryUsed)} detail={`${formatBytes(memoryAvailable)} available`} />
      <Metric icon={RotateCcw} label="Swap" value={memoryError && !memoryState ? 'Unavailable' : formatBytes(memoryState?.swap_used_bytes ?? 0)} detail={memoryError && !memoryState ? 'Live swap state could not be read' : memoryState?.swap_total_bytes ? `${formatBytes(memoryState.swap_free_bytes)} free / ${formatBytes(memoryState.swap_total_bytes)}` : 'No active swap'} />
      <Metric icon={HardDrive} label="Disk" value={formatBytes(diskUsed)} detail={`${diskUsePercent.toFixed(1)}% used · ${formatBytes(selectedNode.inventory.disk_available_bytes)} available`} />
      <Metric icon={Activity} label="Agent" value={displayReleaseVersion(selectedNode.agent_version)} detail={`${selectedNode.inventory.arch || 'arch unknown'} · ${compatibilityPresentation(selectedNode.compatibility, selectedNode.agent_version, selectedNode.protocol_version).label} · ${selectedNode.protocol_version || 'protocol unknown'}`} />
    </div>
    <RemoteAgentLifecycle nodeID={selectedNode.id} serverLabel={serverLabel} online={online} readAvailable={agentUpdateReadAvailable} actionAvailable={agentUpdateActionAvailable} />
    {canManageAgentProfile && <AgentProfileCard nodeID={selectedNode.id} />}
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader><CardTitle className="text-sm text-zinc-200">Remote host controls</CardTitle></CardHeader><CardContent className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
      {hostActions.map((action) => {
        const isRebootPending = action.id === 'reboot' && rebootPending
				const rebootRetryable = action.id === 'reboot' && rebootControl.retryable
				const Icon = rebootRetryable ? RefreshCw : isRebootPending ? RotateCcw : action.icon
        const activeMaintenance = actionStatus?.running === true
        const pending = (hostAction.isPending && hostAction.variables === action.id)
          || (isRebootPending && cancelReboot.isPending)
          || (activeMaintenance && (actionStatus?.action === action.id || (action.id === 'reboot' && actionStatus?.action === 'reboot-cancel')))
        const swapBlocked = action.id === 'swap-reset' && (!memoryState || !memoryState.swap_reset_eligible)
				const rebootBlocked = action.id === 'reboot' && rebootControl.blocked
        const description = !hostActionAvailable
          ? 'Host actions are not enabled on this agent'
          : actionStatusUnavailable
            ? 'Could not verify active host maintenance'
						: activeMaintenance
              ? `${hostActionLabel(actionStatus?.action)} is already running`
							: action.id === 'reboot'
								? rebootControl.description
                : action.id === 'swap-reset' && memoryState
                  ? (memoryState.swap_reset_eligible
                      ? `${formatBytes(memoryState.swap_used_bytes)} used · ${formatBytes(memoryState.memory_available_bytes)} RAM available · ${formatBytes(swapRequired)} required`
                      : memoryState.swap_reset_reason || action.description)
                  : action.id === 'memory-optimize' && memoryState
                    ? `${formatBytes(memoryState.memory_available_bytes)} RAM available · releases reclaimable caches only`
                    : action.description
				return <button key={action.id} disabled={!controlsReady || !online || !hostActionAvailable || actionStatusUnavailable || activeMaintenance || hostAction.isPending || cancelReboot.isPending || rebootBlocked || swapBlocked} title={!hostActionAvailable || actionStatusUnavailable || activeMaintenance || swapBlocked || rebootBlocked ? description : undefined} onClick={() => { if (rebootRetryable) { retryRebootStatus(); return } if (isRebootPending) { cancelReboot.mutate(); return } const prompt = hostActionConfirmation(action.id, serverLabel, controlMemory, swapRequired); if (window.confirm(prompt)) hostAction.mutate(action.id) }} className={cn('flex items-center gap-3 rounded-xl border p-3 text-left disabled:cursor-not-allowed disabled:opacity-40', action.destructive ? 'border-red-900/50 bg-red-950/20' : 'border-zinc-800 bg-zinc-950/40 hover:border-zinc-700')}><span className={cn('grid size-9 place-items-center rounded-lg', action.destructive ? 'bg-red-500/10 text-red-400' : 'bg-zinc-800 text-zinc-300')}>{pending || rebootBlocked ? <Loader2 className="size-4 animate-spin" /> : <Icon className="size-4" />}</span><span><span className="block text-xs font-semibold text-zinc-200">{rebootRetryable ? 'Retry reboot status' : isRebootPending ? 'Cancel reboot' : action.label}</span><span className="block text-[10px] text-zinc-500">{description}</span></span></button>
      })}
    </CardContent></Card>
  </>
}

type ManagedMetricsState = 'loading' | 'offline' | 'unsupported' | 'unavailable' | 'stale' | 'healthy'

const managedMetricsStateClasses: Record<ManagedMetricsState, string> = {
  loading: 'bg-blue-500/10 text-blue-300',
  offline: 'bg-red-500/10 text-red-300',
  unsupported: 'bg-amber-500/10 text-amber-300',
  unavailable: 'bg-red-500/10 text-red-300',
  stale: 'bg-amber-500/10 text-amber-300',
  healthy: 'bg-emerald-500/10 text-emerald-300',
}

function formatMetricAge(ageMs: number) {
  const seconds = Math.max(0, Math.floor(ageMs / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  return `${Math.floor(minutes / 60)}h ago`
}

function ManagedMetricsPanel({ query, readAvailable, online, now }: { query: ReturnType<typeof useQuery<ManagedNodeMetrics>>; readAvailable: boolean; online: boolean; now: number }) {
  const metrics = isValidManagedNodeMetrics(query.data) ? query.data : undefined
  const errorStatus = metricsErrorStatus(query.error)
  let state: ManagedMetricsState
  let stateLabel: string
  let stateDescription: string

  if (!online) {
    state = 'offline'
    stateLabel = 'Offline'
    stateDescription = 'Current metrics are unavailable while this managed server is offline.'
  } else if (!readAvailable || errorStatus === 409) {
    state = 'unsupported'
    stateLabel = 'Unsupported / offline'
    stateDescription = 'Current metrics are not supported by this agent or the agent is offline.'
  } else if (query.isLoading && !query.data) {
    state = 'loading'
    stateLabel = 'Loading'
    stateDescription = 'Reading the current observation from the managed node…'
  } else if (query.error) {
    state = 'unavailable'
    stateLabel = 'Unavailable'
    stateDescription = 'Current metrics are temporarily unavailable or the request timed out. Retry to check again.'
  } else if (!metrics) {
    state = 'unavailable'
    stateLabel = 'Unavailable'
    stateDescription = 'The managed node returned an invalid current metrics observation.'
  } else {
    const ageMs = now - new Date(metrics.observed_at).getTime()
    state = ageMs > managedMetricsStaleAfterMs ? 'stale' : 'healthy'
    stateLabel = state === 'stale' ? 'Stale' : 'Healthy'
    stateDescription = state === 'stale'
      ? `Observation is stale; last observed ${formatMetricAge(ageMs)}.`
      : `Last observed ${formatMetricAge(ageMs)}.`
  }

  const showValues = !!metrics && (state === 'healthy' || state === 'stale')
  return <Card data-testid="managed-node-metrics" className={cn('border-zinc-800 bg-zinc-900/80', state === 'offline' || state === 'unavailable' ? 'border-red-500/25' : state === 'unsupported' || state === 'stale' ? 'border-amber-500/25' : undefined)}>
    <CardHeader className="flex-row items-start justify-between gap-3">
      <div><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><Activity className="size-4 text-blue-400" />Current server metrics</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Read-only observation from the selected managed node</p></div>
      <span data-testid="managed-node-metrics-status" role="status" className={cn('shrink-0 rounded-full px-2.5 py-1 text-[10px] font-semibold', managedMetricsStateClasses[state])}>{stateLabel}</span>
    </CardHeader>
    {showValues && metrics ? <CardContent className="grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
      <ManagedMetricValue testId="managed-node-metrics-cpu" label="CPU" value={`${metrics.cpu.usage_percent.toFixed(1)}%`} detail={`${metrics.cpu.core_count} cores`} />
      <ManagedMetricValue testId="managed-node-metrics-load" label="Load average" value={metrics.load.one.toFixed(2)} detail={`5m ${metrics.load.five.toFixed(2)} · 15m ${metrics.load.fifteen.toFixed(2)}`} />
      <ManagedMetricValue testId="managed-node-metrics-memory" label="Memory" value={`${metrics.memory.usage_percent.toFixed(1)}%`} detail={`${formatBytes(metrics.memory.used_bytes)} used · ${formatBytes(metrics.memory.total_bytes)} total`} />
      <ManagedMetricValue testId="managed-node-metrics-network" label="Network" value={`RX ${formatBytes(metrics.network.rx_bytes)}`} detail={`TX ${formatBytes(metrics.network.tx_bytes)}`} />
      <ManagedMetricValue testId="managed-node-metrics-root-disk" label="Root disk" value={`${metrics.root_disk.usage_percent.toFixed(1)}%`} detail={`${formatBytes(metrics.root_disk.used_bytes)} used · ${formatBytes(metrics.root_disk.total_bytes)} total`} />
      <p className={cn('sm:col-span-2 xl:col-span-5 text-[10px]', state === 'stale' ? 'text-amber-400' : 'text-zinc-500')}>{stateDescription}</p>
    </CardContent> : <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-start gap-3"><AlertTriangle className={cn('mt-0.5 size-4 shrink-0', state === 'loading' ? 'text-blue-400' : state === 'unsupported' ? 'text-amber-400' : 'text-red-400')} /><p className="text-xs text-zinc-400">{stateDescription}</p></div>
      {state === 'unavailable' && <Button type="button" variant="outline" size="xs" disabled={query.isFetching || !online} onClick={() => void query.refetch()}><RefreshCw className={cn('size-3', query.isFetching && 'animate-spin')} /> Retry</Button>}
    </CardContent>}
  </Card>
}

function ManagedMetricValue({ testId, label, value, detail }: { testId: string; label: string; value: string; detail: string }) {
  return <div data-testid={testId} className="rounded-xl border border-zinc-800 bg-zinc-950/40 p-3"><p className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">{label}</p><p className="mt-2 text-lg font-bold tabular-nums text-zinc-100">{value}</p><p className="mt-1 truncate text-[10px] text-zinc-500">{detail}</p></div>
}

function Services({ services, serverLabel, online, serviceActionAvailable, mutation, tasksQuery, focusedService, onOpenLogs }: { services: ServiceState[]; serverLabel: string; online: boolean; serviceActionAvailable: boolean; mutation: ReturnType<typeof useMutation<AgentTask, Error, { service: string; action: 'start' | 'stop' | 'restart' }>>; tasksQuery: ReturnType<typeof useQuery<AgentTask[]>>; focusedService: string | null; onOpenLogs: (service: string) => void }) {
  const controlsReady = useRemoteMutationReadiness()
  const actionLabels = { start: 'Start', restart: 'Restart', stop: 'Stop' } as const
  const pendingLabels = { start: 'Starting…', restart: 'Restarting…', stop: 'Stopping…' } as const
  useEffect(() => {
    if (!focusedService || services.length === 0) return
    const scrollToService = () => {
      const target = services.find(service => serviceFocusMatches(service.name, focusedService))
      if (target) document.getElementById(`remote-service-${encodeURIComponent(target.name)}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
    window.requestAnimationFrame(scrollToService)
    const settleTimer = window.setTimeout(scrollToService, 500)
    return () => window.clearTimeout(settleTimer)
  }, [focusedService, services])
  return <div className="space-y-4">
    {!serviceActionAvailable && <Card className="border-amber-500/25 bg-amber-500/[0.05]"><CardContent className="p-3 text-xs text-amber-300"><strong>Service actions are not enabled for this agent.</strong><span className="ml-1 text-amber-300/70">Services remain observable; configure <code>HSERVER_AGENT_ALLOWED_SERVICES</code> to enable controls.</span></CardContent></Card>}
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader><CardTitle className="text-sm text-zinc-200">Managed server services</CardTitle></CardHeader><CardContent className="divide-y divide-zinc-800/60 p-0">
      {services.map((service) => {
        const manageable = isManageableService(service.name)
        const active = service.active === 'active'
        const pendingRow = mutation.isPending && mutation.variables?.service === service.name
        const focused = !!focusedService && serviceFocusMatches(service.name, focusedService)
        return (
          <div id={`remote-service-${encodeURIComponent(service.name)}`} key={service.name} className={cn('flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between', focused && 'bg-amber-500/[0.08] ring-1 ring-inset ring-amber-500/40')}>
            <div className="flex items-center gap-3">
              <span className={cn('size-2 rounded-full', active ? 'bg-emerald-400' : 'bg-zinc-600')} />
              <div>
                <p className="font-mono text-xs text-zinc-200">{service.name}</p>
                <p className="text-[10px] text-zinc-500">{pendingRow ? 'Waiting for selected server agent…' : `${service.active} · ${service.sub ?? 'unknown'}`}</p>
              </div>
            </div>
            <div className="flex items-center gap-1">
              <Button variant="ghost" size="icon-xs" disabled={!online} onClick={() => onOpenLogs(service.name)} title={`View ${service.name} logs`} aria-label={`View ${service.name} logs`}>
                <FileClock className="size-3 text-blue-400" />
              </Button>
              {manageable ? (['start', 'restart', 'stop'] as const).map((action) => {
                const pendingAction = pendingRow && mutation.variables?.action === action
                const stateBlocked = action === 'start' ? active : !active
                return <Button key={action} variant="outline" size="xs" disabled={!controlsReady || !online || !serviceActionAvailable || mutation.isPending || stateBlocked} title={!serviceActionAvailable ? 'Service actions are not enabled on this agent' : undefined} onClick={() => { if (window.confirm(`${actionLabels[action]} ${service.name} on managed node ${serverLabel}?`)) mutation.mutate({ service: service.name, action }) }}>{pendingAction && <Loader2 className="size-3 animate-spin" />}{pendingAction ? pendingLabels[action] : actionLabels[action]}</Button>
              }) : <span className="ml-1 text-[10px] text-zinc-600">Observed only</span>}
            </div>
          </div>
        )
      })}
    </CardContent></Card>

    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="text-sm text-zinc-200">Recent agent operations</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Confirmed results reported by the selected server agent</p></div><Button variant="ghost" size="xs" disabled={!online || tasksQuery.isFetching} onClick={() => tasksQuery.refetch()}><RefreshCw className={cn('size-3', tasksQuery.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="divide-y divide-zinc-800/60 p-0">
      {tasksQuery.isLoading ? <Loading /> : tasksQuery.error ? <RemoteDataError title="Could not load recent agent operations" error={tasksQuery.error} onRetry={() => tasksQuery.refetch()} disabled={!online} /> : (tasksQuery.data ?? []).length === 0 ? <Empty text="No agent operations recorded yet." /> : (tasksQuery.data ?? []).map((task) => { const service = task.payload?.service ?? task.result?.service ?? 'service'; const action = task.payload?.action ?? (task.kind === 'service.status' ? 'status check' : task.kind); const detail = task.status === 'failed' ? task.error : [task.result?.active, task.result?.sub].filter(Boolean).join('/') || (task.status === 'running' ? 'Agent is executing this task' : task.status === 'queued' ? 'Waiting for agent pickup' : 'Completed'); return <div key={task.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"><div className="min-w-0"><p className="truncate font-mono text-xs text-zinc-200">{service} · {action}</p><p className={cn('mt-0.5 text-[10px]', task.status === 'failed' ? 'text-red-400' : 'text-zinc-500')}>{detail}</p></div><div className="flex shrink-0 items-center gap-3"><span className="text-[10px] text-zinc-600">{task.created_at ? formatTimestamp(task.created_at) : `#${task.id}`}</span><span className={cn('rounded px-2 py-0.5 text-[9px] font-semibold uppercase', task.status === 'completed' ? 'bg-emerald-500/10 text-emerald-400' : task.status === 'failed' ? 'bg-red-500/10 text-red-400' : 'bg-blue-500/10 text-blue-400')}>{task.status}</span></div></div> })}
    </CardContent></Card>
  </div>
}

function Processes({ query, mutation, online, processReadAvailable, processSignalAvailable }: { query: ReturnType<typeof useQuery<RemoteProcess[]>>; mutation: ReturnType<typeof useMutation<{ message: string }, Error, { pid: number; startTime: number; signal: 'term' | 'kill' }>>; online: boolean; processReadAvailable: boolean; processSignalAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  const [search, setSearch] = useState('')
  const processes = query.data ?? []
  const normalizedSearch = search.trim().toLocaleLowerCase()
  const filtered = normalizedSearch
    ? processes.filter((process) => `${process.pid} ${process.user} ${process.command}`.toLocaleLowerCase().includes(normalizedSearch))
    : processes

  return <Card className="border-zinc-800 bg-zinc-900/80">
    <CardHeader className="gap-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <CardTitle className="text-sm text-zinc-200">Live processes</CardTitle>
          <p className="mt-1 text-[10px] text-zinc-500">Search before sending SIGTERM or SIGKILL to a process</p>
        </div>
        <Button variant="ghost" size="xs" disabled={!online || query.isFetching} onClick={() => query.refetch()}>
          <RefreshCw className={cn('size-3', query.isFetching && 'animate-spin')} /> Refresh
        </Button>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <label className="relative min-w-0 flex-1">
          <span className="sr-only">Search selected server processes</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-zinc-600" />
          <input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search PID, user or command…"
            className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 pl-9 pr-3 text-xs text-zinc-200 outline-none transition focus:border-violet-500/60"
          />
        </label>
        {normalizedSearch && <Button variant="ghost" size="xs" onClick={() => setSearch('')} aria-label="Clear selected server process search"><X className="size-3" /> Clear</Button>}
        <span data-testid="managed-node-process-result-count" aria-live="polite" className="shrink-0 text-[10px] tabular-nums text-zinc-500">
          {filtered.length} of {processes.length}
        </span>
      </div>
      {!processReadAvailable
        ? <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-[10px] text-amber-300">This agent does not advertise process inventory. Upgrade and restart the managed-node agent.</div>
        : !processSignalAvailable && <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-[10px] text-amber-300">Processes remain observable. Set <code>HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true</code> and restart the agent to enable SIGTERM and SIGKILL controls.</div>}
    </CardHeader>
    <CardContent className="overflow-x-auto p-0">
      {!processReadAvailable ? <Empty text="Process inventory is not available from this agent." /> : query.isLoading ? <Loading /> : query.error ? <RemoteDataError title="Could not load selected server processes" error={query.error} onRetry={() => query.refetch()} disabled={!online} /> : processes.length === 0 ? <Empty text="No processes were returned." /> : filtered.length === 0 ? <Empty text="No processes match this search." /> : <table className="w-full min-w-[720px] text-xs">
        <thead><tr className="border-b border-zinc-800 text-zinc-500"><th className="px-3 py-2 text-left">PID</th><th className="px-3 py-2 text-left">User</th><th className="px-3 py-2 text-right">CPU</th><th className="px-3 py-2 text-right">MEM</th><th className="px-3 py-2 text-right">RSS</th><th className="px-3 py-2 text-left">Command</th><th className="px-3 py-2 text-right">Actions</th></tr></thead>
        <tbody>{filtered.map((process) => {
          const pendingProcess = mutation.isPending
            && mutation.variables?.pid === process.pid
            && mutation.variables?.startTime === process.startTime
          const pendingTerm = pendingProcess && mutation.variables?.signal === 'term'
          const pendingKill = pendingProcess && mutation.variables?.signal === 'kill'
          return <tr key={`${process.pid}-${process.startTime}`} className={cn('border-b border-zinc-800/40 transition-colors hover:bg-zinc-800/20', pendingProcess && 'bg-blue-500/[0.06]')}>
            <td className="px-3 py-2 font-mono text-zinc-300">{process.pid}</td>
            <td className="px-3 py-2 text-zinc-400">{process.user}</td>
            <td className="px-3 py-2 text-right text-blue-400">{process.cpu.toFixed(1)}%</td>
            <td className="px-3 py-2 text-right text-violet-400">{process.memory.toFixed(1)}%</td>
            <td className="px-3 py-2 text-right text-zinc-400">{formatBytes(process.rss)}</td>
            <td className="max-w-[320px] truncate px-3 py-2 font-mono text-zinc-400" title={process.command}>{process.command}</td>
            <td className="px-3 py-2"><div className="flex justify-end gap-1">
              <Button variant="ghost" size="icon-xs" disabled={!controlsReady || !online || !processSignalAvailable || process.pid <= 1 || !process.startTime || mutation.isPending} title={!online ? 'selected server agent is offline' : !processSignalAvailable ? 'Process signals are not enabled on this agent' : pendingTerm ? `Stopping PID ${process.pid}…` : 'SIGTERM'} aria-label={pendingTerm ? `Stopping PID ${process.pid} on selected server` : `Stop PID ${process.pid} on selected server`} onClick={() => { if (window.confirm(`Stop PID ${process.pid} on selected server?\n\n${process.command}`)) mutation.mutate({ pid: process.pid, startTime: process.startTime, signal: 'term' }) }}>{pendingTerm ? <Loader2 className="size-3 animate-spin text-amber-400" /> : <Power className="size-3 text-amber-400" />}</Button>
              <Button variant="ghost" size="icon-xs" disabled={!controlsReady || !online || !processSignalAvailable || process.pid <= 1 || !process.startTime || mutation.isPending} title={!online ? 'selected server agent is offline' : !processSignalAvailable ? 'Process signals are not enabled on this agent' : pendingKill ? `Force killing PID ${process.pid}…` : 'SIGKILL'} aria-label={pendingKill ? `Force killing PID ${process.pid} on selected server` : `Force kill PID ${process.pid} on selected server`} onClick={() => { if (window.confirm(`Force kill PID ${process.pid} on selected server?\n\n${process.command}`)) mutation.mutate({ pid: process.pid, startTime: process.startTime, signal: 'kill' }) }}>{pendingKill ? <Loader2 className="size-3 animate-spin text-red-400" /> : <Skull className="size-3 text-red-400" />}</Button>
            </div></td>
          </tr>
        })}</tbody>
      </table>}
    </CardContent>
  </Card>
}

function Logs({ source, sources, setSource, query, focusedService, search, level, setFilters, online, logsReadAvailable }: {
  source: LogSourceID
  sources: LogSourceID[]
  setSource: (value: string) => void
  query: ReturnType<typeof useQuery<LogEntry[]>>
  focusedService: string | null
  search: string
  level: LogLevelFilter
  setFilters: (search: string, level: LogLevelFilter) => void
  online: boolean
  logsReadAvailable: boolean
}) {
  const entries = query.data ?? []
  const normalizedSearch = search.trim().toLocaleLowerCase()
  const filtered = entries.filter((entry) => {
    const levelMatches = level === 'all'
      || (level === 'error' && entry.priority <= 3)
      || (level === 'warning' && entry.priority <= 4)
      || (level === 'info' && entry.priority <= 6)
    if (!levelMatches) return false
    if (!normalizedSearch) return true
    return `${entry.unit} ${entry.message} ${entry.timestamp}`.toLocaleLowerCase().includes(normalizedSearch)
  })
  const filteredActive = normalizedSearch !== '' || level !== 'all'

  return (
    <Card className="border-zinc-800 bg-zinc-900/80">
      <CardHeader className="gap-3">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <CardTitle className="text-sm text-zinc-200">Remote journal</CardTitle>
            <p className="mt-1 text-[10px] text-zinc-500">Last 200 entries · auto-refresh 15s{focusedService ? ` · opened from ${focusedService}` : ''}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <select value={source} onChange={(event) => setSource(event.target.value)} disabled={!logsReadAvailable} aria-label="selected server log source" className="rounded-md border border-zinc-700 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-300 disabled:opacity-50">
              {logSources.filter(([value]) => sources.includes(value)).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
            <Button variant="ghost" size="xs" onClick={() => query.refetch()} disabled={!online || !logsReadAvailable || query.isFetching}>
              <RefreshCw className={cn('size-3', query.isFetching && 'animate-spin')} /> Refresh
            </Button>
          </div>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <label className="relative min-w-0 flex-1">
            <span className="sr-only">Search selected server logs</span>
            <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-zinc-600" />
            <input
              type="search"
              value={search}
              onChange={(event) => setFilters(event.target.value, level)}
              placeholder="Search message, unit or timestamp…"
              className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 pl-9 pr-3 text-xs text-zinc-200 outline-none transition focus:border-violet-500/60"
            />
          </label>
          <select value={level} onChange={(event) => setFilters(search, event.target.value as LogLevelFilter)} aria-label="selected server log severity" className="h-9 rounded-lg border border-zinc-700 bg-zinc-950 px-3 text-xs text-zinc-300">
            <option value="all">All levels</option>
            <option value="error">Errors only</option>
            <option value="warning">Warnings + errors</option>
            <option value="info">Info + higher</option>
          </select>
          {filteredActive && (
            <Button variant="ghost" size="xs" onClick={() => setFilters('', 'all')} aria-label="Clear selected server log filters">
              <X className="size-3" /> Clear
            </Button>
          )}
          <span data-testid="managed-node-log-result-count" aria-live="polite" className="shrink-0 text-[10px] tabular-nums text-zinc-500">
            {filtered.length} of {entries.length}
          </span>
        </div>
      </CardHeader>
      <CardContent className="max-h-[620px] overflow-auto p-0">
        {!logsReadAvailable ? <div className="m-4 rounded-lg border border-amber-500/20 bg-amber-500/5 p-4 text-xs text-amber-300"><strong>Journal access is not enabled on this agent.</strong><span className="ml-1 text-amber-300/70">Set <code>HSERVER_AGENT_ALLOWED_LOG_SOURCES</code> to the fixed sources you want to expose, then restart the agent.</span></div> : query.isLoading ? <Loading /> : query.error ? <RemoteDataError title="Could not load selected server journal" error={query.error} onRetry={() => query.refetch()} disabled={!online} /> : entries.length === 0 ? <Empty text="No journal entries found." /> : filtered.length === 0 ? <Empty text="No log entries match these filters." /> : (
          <div className="divide-y divide-zinc-800/50">
            {filtered.map((entry, index) => (
              <div key={`${entry.timestamp}-${index}`} className="grid gap-1 px-4 py-2.5 text-xs md:grid-cols-[150px_160px_1fr]">
                <span className="font-mono text-[10px] text-zinc-600">{formatTimestamp(entry.timestamp)}</span>
                <span className={cn('truncate font-mono text-[10px]', entry.priority <= 3 ? 'text-red-400' : entry.priority === 4 ? 'text-amber-400' : 'text-blue-400')}>{entry.unit || 'system'}</span>
                <span className="break-words font-mono text-zinc-300">{entry.message}</span>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function Disks({ nodeID, query, online, diskCleanupAvailable, actionStatus, actionStatusUnavailable, actionStatusError, retryActionStatus }: { nodeID: string; query: ReturnType<typeof useQuery<DiskMount[]>>; online: boolean; diskCleanupAvailable: boolean; actionStatus?: HostActionStatus; actionStatusUnavailable: boolean; actionStatusError: boolean; retryActionStatus: () => void }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [confirmed, setConfirmed] = useState(false)
  const [lastRun, setLastRun] = useState<RemoteCleanupReceipt | null>(null)
  const cleanupQuery = useQuery<RemoteCleanupTarget[]>({
    queryKey: ['managed-node-disk-cleanup', nodeID],
    queryFn: () => api.get(nodeBase + '/disk/cleanup'),
    enabled: online && diskCleanupAvailable,
    staleTime: 60_000,
  })
  const cleanupMutation = useMutation<RemoteCleanupResponse, Error, string[]>({
    mutationFn: (targets) => {
      if (!controlsReady) throw new Error(remoteMutationUnavailableMessage)
      return api.post(nodeBase + '/disk/cleanup', { targets, confirmed: true })
    },
    onSuccess: async (result) => {
      setLastRun({ response: result, labels: Object.fromEntries(targets.map((target) => [target.id, target.name])) })
      const reclaimed = result.results.reduce((sum, item) => sum + (item.reclaimed ?? 0), 0)
      const failed = result.results.filter((item) => item.status === 'error')
      const completed = result.results.length - failed.length
      if (completed > 0) toast.success(`${completed} selected server cleanup task(s) completed · ${formatBytes(reclaimed)} reclaimed`)
      if (failed.length > 0) toast.error(`${failed.length} selected server cleanup task(s) failed`)
      if (result.scan_error) toast.warning('Cleanup completed, but reclaimed space could not be remeasured')
      setSelected(new Set())
      setConfirmed(false)
      await Promise.all([
        query.refetch(),
        queryClient.invalidateQueries({ queryKey: ['managed-node-disk-cleanup', nodeID] }),
        queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history'] }),
      ])
    },
    onError: (error) => toast.error(error.message || 'selected server disk cleanup failed'),
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey(nodeID) }),
  })
  const maintenanceRunning = actionStatus?.running === true
  const targets = cleanupQuery.data ?? []
  const safeTargets = targets.filter((target) => target.risk === 'low' && target.size > 0)
  const safeBytes = safeTargets.reduce((sum, target) => sum + target.size, 0)
  const selectedBytes = targets.filter((target) => selected.has(target.id)).reduce((sum, target) => sum + target.size, 0)
  const selectSafe = () => {
    setSelected(new Set(safeTargets.map((target) => target.id)))
    setConfirmed(false)
  }
  const clearSelection = () => {
    setSelected(new Set())
    setConfirmed(false)
  }
  const toggle = (id: string) => {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
    setConfirmed(false)
  }
  const refresh = () => {
    query.refetch()
    cleanupQuery.refetch()
  }

  return <div className="space-y-4">
    <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><CardTitle className="text-sm text-zinc-200">Filesystems</CardTitle><Button variant="ghost" size="xs" onClick={refresh} disabled={!online}><RefreshCw className="size-3" /> Refresh</Button></CardHeader><CardContent className="space-y-3">
      {query.isLoading ? <Loading /> : query.error ? <RemoteDataError title="Could not load selected server filesystems" error={query.error} onRetry={() => query.refetch()} disabled={!online} /> : (query.data ?? []).map((disk) => <div key={`${disk.filesystem}-${disk.mountpoint}`} className="rounded-xl border border-zinc-800 bg-zinc-950/40 p-3"><div className="flex items-center justify-between gap-4"><div className="min-w-0"><p className="truncate font-mono text-xs text-zinc-200">{disk.mountpoint}</p><p className="truncate text-[10px] text-zinc-600">{disk.filesystem}</p></div><div className="text-right"><p className="text-xs font-semibold text-zinc-300">{disk.use_percent}% used</p><p className="text-[10px] text-zinc-600">{formatBytes(disk.available)} free / {formatBytes(disk.size)}</p></div></div><div className="mt-3 h-1.5 overflow-hidden rounded-full bg-zinc-800"><div className={cn('h-full rounded-full', disk.use_percent >= 90 ? 'bg-red-500' : disk.use_percent >= 75 ? 'bg-amber-500' : 'bg-violet-500')} style={{ width: `${Math.min(100, disk.use_percent)}%` }} /></div></div>)}
    </CardContent></Card>

    <Card className="border-zinc-800 bg-zinc-900/80">
      <CardHeader><div className="flex items-start justify-between gap-3"><div><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><Trash2 className="size-4 text-red-400" /> Measured cleanup</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Only fixed, measured scopes are offered. Nothing is removed until you select targets and confirm.</p></div>{targets.length > 0 && <span className="shrink-0 rounded-full bg-amber-500/10 px-2 py-1 text-[10px] font-semibold text-amber-300">{formatBytes(targets.reduce((sum, target) => sum + target.size, 0))} found</span>}</div></CardHeader>
      <CardContent className="space-y-3">
				{actionStatusUnavailable && <div className="flex items-center justify-between gap-3 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-300"><span>{actionStatusError ? 'Could not verify active selected server maintenance. Cleanup is paused.' : 'Checking active selected server maintenance before enabling cleanup…'}</span>{actionStatusError && <Button type="button" variant="ghost" size="xs" onClick={retryActionStatus}>Retry</Button>}</div>}
				{maintenanceRunning && <div className="flex items-center gap-2 rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-xs text-blue-300"><Loader2 className="size-4 animate-spin" /><strong>{hostActionLabel(actionStatus?.action)} is running on selected server.</strong><span className="text-blue-300/60">New cleanup requests are paused.</span></div>}
        {lastRun && <div data-testid="managed-node-cleanup-last-run" aria-live="polite" className="overflow-hidden rounded-xl border border-blue-500/20 bg-blue-500/[0.04]">
          <div className="flex items-start justify-between gap-3 border-b border-blue-500/15 px-4 py-3"><div><p className="flex items-center gap-2 text-xs font-semibold text-blue-300"><CheckCircle2 className="size-3.5" /> Last selected server cleanup result</p><p className="mt-1 text-[10px] text-zinc-500">{formatBytes(lastRun.response.results.reduce((sum, item) => sum + (item.reclaimed ?? 0), 0))} actually reclaimed across {lastRun.response.results.length} task(s)</p></div><button type="button" onClick={() => setLastRun(null)} aria-label="Dismiss selected server cleanup result" className="grid size-7 place-items-center rounded-md text-zinc-500 transition hover:bg-zinc-800 hover:text-white"><X className="size-3.5" /></button></div>
          <div className="divide-y divide-zinc-800/60">{lastRun.response.results.map((result) => <div key={result.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:justify-between"><div className="flex min-w-0 items-start gap-2.5">{result.status === 'ok' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-400" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-red-400" />}<div className="min-w-0"><p className="text-xs font-medium text-zinc-200">{lastRun.labels[result.id] ?? result.id}</p><p className={cn('mt-0.5 break-words text-[10px]', result.status === 'ok' ? 'text-zinc-500' : 'text-red-300')}>{result.message || (result.status === 'ok' ? 'Cleanup completed.' : 'Cleanup failed.')}</p></div></div><span className={cn('shrink-0 font-mono text-xs font-semibold', result.status === 'ok' ? 'text-emerald-300' : 'text-red-300')}>{result.status === 'ok' ? `${formatBytes(result.reclaimed)} reclaimed` : 'Failed'}</span></div>)}</div>
          {lastRun.response.scan_error && <p className="border-t border-amber-500/15 px-4 py-2 text-[10px] text-amber-300">Cleanup finished, but the follow-up measurement failed: {lastRun.response.scan_error}</p>}
        </div>}
        {!diskCleanupAvailable ? <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-4 text-xs text-amber-300"><strong>Disk cleanup is not enabled on this agent.</strong><span className="ml-1 text-amber-300/70">Set <code>HSERVER_AGENT_ALLOWED_DISK_CLEANUP</code> to the fixed scopes you want to expose, then restart the agent.</span></div> : cleanupQuery.isLoading ? <Loading /> : cleanupQuery.error ? <RemoteDataError title="Could not scan selected server cleanup targets" error={cleanupQuery.error} onRetry={() => cleanupQuery.refetch()} disabled={!online} /> : targets.length === 0 ? <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-4 text-center text-xs text-emerald-300">No eligible cleanup data was measured on selected server.</div> : <>
						<div className="flex flex-col gap-3 rounded-xl border border-emerald-500/20 bg-emerald-500/[0.04] p-3 sm:flex-row sm:items-center sm:justify-between"><div className="min-w-0"><p className="flex items-center gap-2 text-xs font-semibold text-emerald-300"><ShieldCheck className="size-3.5" /> Recommended cleanup preset</p><p className="mt-1 text-[10px] leading-relaxed text-zinc-500">Selects every measured low-risk selected server candidate. Nothing runs until you review, confirm and start cleanup.</p></div><div className="flex shrink-0 items-center gap-2">{selected.size > 0 && <Button type="button" variant="ghost" size="xs" onClick={clearSelection}>Clear</Button>}<Button type="button" variant="outline" size="xs" onClick={selectSafe} disabled={!controlsReady || safeTargets.length === 0 || maintenanceRunning || actionStatusUnavailable || !online} className="border-emerald-500/30 text-emerald-300 hover:bg-emerald-500/10 hover:text-emerald-200">Select safe · {safeTargets.length} · {formatBytes(safeBytes)}</Button></div></div>
						{targets.map((target) => <label key={target.id} className={cn('flex items-start gap-3 rounded-xl border p-3 transition', maintenanceRunning || actionStatusUnavailable ? 'cursor-not-allowed opacity-50' : 'cursor-pointer', selected.has(target.id) ? 'border-red-500/30 bg-red-500/5' : 'border-zinc-800 bg-zinc-950/40 hover:border-zinc-700')}>
			<input type="checkbox" disabled={!controlsReady || maintenanceRunning || actionStatusUnavailable} checked={selected.has(target.id)} onChange={() => toggle(target.id)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-red-500" />
            <span className="min-w-0 flex-1"><span className="block text-xs font-semibold text-zinc-200">{target.name}</span><span className="mt-1 block text-[10px] leading-relaxed text-zinc-500">{target.description}</span><span className={cn('mt-2 inline-block rounded px-1.5 py-0.5 text-[9px] font-semibold uppercase', target.risk === 'low' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-amber-500/10 text-amber-400')}>{target.risk} risk</span></span>
            <strong className="shrink-0 font-mono text-xs text-amber-300">{formatBytes(target.size)}</strong>
          </label>)}
						{selected.size > 0 && <div className="space-y-3 border-t border-zinc-800 pt-4"><div className="flex items-center justify-between text-xs"><span className="text-zinc-500">Selected estimate</span><strong className="font-mono text-zinc-200">{formatBytes(selectedBytes)}</strong></div><label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-400"><input type="checkbox" disabled={!controlsReady || maintenanceRunning || actionStatusUnavailable} checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-red-500" /><span>I reviewed the selected cleanup scopes and understand cleanup is irreversible</span></label><Button variant="destructive" className="w-full" disabled={!controlsReady || !confirmed || cleanupMutation.isPending || !online || maintenanceRunning || actionStatusUnavailable} onClick={() => cleanupMutation.mutate(Array.from(selected))}>{cleanupMutation.isPending || maintenanceRunning ? <Loader2 className="size-4 animate-spin" /> : <Trash2 className="size-4" />} {actionStatusUnavailable ? 'Checking managed server maintenance…' : maintenanceRunning ? `${hostActionLabel(actionStatus?.action)} in progress` : `Clean selected data (${formatBytes(selectedBytes)})`}</Button></div>}
        </>}
      </CardContent>
    </Card>
  </div>
}

function Containers({ query, mutation, online, containerReadAvailable, containerActionAvailable }: { query: ReturnType<typeof useQuery<RemoteContainer[]>>; mutation: ReturnType<typeof useMutation<{ message: string }, Error, { container: string; action: 'start' | 'stop' | 'restart' }>>; online: boolean; containerReadAvailable: boolean; containerActionAvailable: boolean }) {
  const controlsReady = useRemoteMutationReadiness()
  return <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-center justify-between"><div><CardTitle className="text-sm text-zinc-200">Docker containers</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Start, restart and stop selected server containers</p></div><Button variant="ghost" size="xs" disabled={!online || !containerReadAvailable || query.isFetching} onClick={() => query.refetch()}><RefreshCw className={cn('size-3', query.isFetching && 'animate-spin')} /> Refresh</Button></CardHeader><CardContent className="divide-y divide-zinc-800/60 p-0">
    {!containerReadAvailable ? <div className="m-4 rounded-lg border border-amber-500/20 bg-amber-500/5 p-4 text-xs text-amber-300"><strong>Docker inventory is not enabled on this agent.</strong><span className="ml-1 text-amber-300/70">Set <code>HSERVER_AGENT_ALLOW_CONTAINER_READ=true</code>, then restart the agent.</span></div> : query.isLoading ? <Loading /> : query.error ? <RemoteDataError title="Could not load selected server containers" error={query.error} onRetry={() => query.refetch()} disabled={!online} /> : (query.data ?? []).length === 0 ? <Empty text="No containers found." /> : (query.data ?? []).map((container) => <div key={container.id} className="flex flex-col gap-3 px-4 py-3 lg:flex-row lg:items-center lg:justify-between"><div className="flex min-w-0 items-center gap-3"><span className={cn('grid size-9 shrink-0 place-items-center rounded-lg', container.state === 'running' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-zinc-800 text-zinc-500')}><Layers3 className="size-4" /></span><div className="min-w-0"><p className="truncate font-mono text-xs font-semibold text-zinc-200">{container.name}</p><p className="truncate text-[10px] text-zinc-500">{container.image} · {container.status}</p>{container.ports && <p className="truncate font-mono text-[10px] text-zinc-600">{container.ports}</p>}</div></div><div className="flex gap-1">{(['start', 'restart', 'stop'] as const).map((action) => <Button key={action} variant="outline" size="xs" disabled={!controlsReady || !online || !containerActionAvailable || mutation.isPending || (action === 'start' && container.state === 'running') || (action === 'stop' && container.state !== 'running')} title={!online ? 'selected server agent is offline' : !containerActionAvailable ? 'Container actions are not enabled on this agent' : undefined} onClick={() => { if (action !== 'stop' || window.confirm(`Stop container ${container.name}?`)) mutation.mutate({ container: container.name || container.id, action }) }}>{action}</Button>)}</div></div>)}
  </CardContent></Card>
}

function Loading() { return <div className="p-8 text-center text-zinc-500"><Loader2 className="mx-auto size-4 animate-spin" /></div> }
function Empty({ text }: { text: string }) { return <div className="p-8 text-center text-xs text-zinc-600">{text}</div> }
function RemoteDataError({ title, error, onRetry, disabled = false }: { title: string; error: Error; onRetry: () => void; disabled?: boolean }) { return <div className="flex flex-col items-center gap-3 px-4 py-6 text-center"><AlertTriangle className="size-5 text-red-400" /><div><p className="text-xs font-medium text-red-300">{title}</p><p className="mt-1 text-[10px] text-zinc-500">{error.message || 'The server did not return this operational data.'}</p></div><Button variant="outline" size="xs" disabled={disabled} onClick={onRetry}><RefreshCw className="size-3" /> Retry</Button></div> }
function FleetCompatibility({ summary }: { summary: ReturnType<typeof summarizeFleetCompatibility> }) {
  const healthy = summary.behind === 0 && summary.protocolIssues === 0
  return <Card className={cn('border-zinc-800 bg-zinc-900/80', !healthy && 'border-amber-500/30 bg-amber-500/[0.04]')}>
    <CardContent className="flex flex-col gap-3 p-4 lg:flex-row lg:items-center lg:justify-between">
      <div className="flex items-start gap-3">
        {healthy ? <CheckCircle2 className="mt-0.5 size-4 text-emerald-400" /> : <AlertTriangle className="mt-0.5 size-4 text-amber-400" />}
        <div><p className="text-xs font-semibold text-zinc-200">Agent compatibility</p><p className="mt-1 text-[10px] text-zinc-500">Panel release and agent protocol are compared centrally; development builds remain explicitly unverified.</p></div>
      </div>
      <div className="flex flex-wrap gap-2 text-[10px] font-semibold">
        <span className="rounded-full bg-emerald-500/10 px-2.5 py-1 text-emerald-400">{summary.current} current</span>
        {summary.behind > 0 && <span className="rounded-full bg-amber-500/10 px-2.5 py-1 text-amber-400">{summary.behind} need update</span>}
        {summary.ahead > 0 && <span className="rounded-full bg-blue-500/10 px-2.5 py-1 text-blue-400">{summary.ahead} agent ahead</span>}
        {summary.protocolIssues > 0 && <span className="rounded-full bg-red-500/10 px-2.5 py-1 text-red-400">{summary.protocolIssues} protocol issue</span>}
        {summary.unknown > 0 && <span className="rounded-full bg-zinc-800 px-2.5 py-1 text-zinc-400">{summary.unknown} unverified</span>}
      </div>
    </CardContent>
  </Card>
}

const compatibilityToneClasses: Record<CompatibilityPresentation['tone'], string> = {
  healthy: 'bg-emerald-500/10 text-emerald-400',
  warning: 'bg-amber-500/10 text-amber-400',
  critical: 'bg-red-500/10 text-red-400',
  neutral: 'bg-zinc-800 text-zinc-400',
}

function ServerCard({ name, detail, online, compatibility }: { name: string; detail: string; online: boolean; compatibility?: CompatibilityPresentation }) {
  return <div className="flex items-center justify-between gap-3"><div className="flex min-w-0 items-center gap-3"><span className="grid size-10 shrink-0 place-items-center rounded-xl bg-blue-500/10 text-blue-400"><Server className="size-5" /></span><div className="min-w-0"><p className="truncate font-semibold text-zinc-100">{name}</p><p className="truncate text-xs text-zinc-500">{detail}</p>{compatibility && <p className="mt-1 truncate text-[10px] text-zinc-600" title={compatibility.detail}>{compatibility.detail}</p>}</div></div><div className="flex shrink-0 flex-col items-end gap-1"><span className={cn('rounded-full px-2 py-1 text-[10px] font-semibold', online ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400')}>{online ? 'ONLINE' : 'OFFLINE'}</span>{compatibility && <span className={cn('rounded-full px-2 py-1 text-[9px] font-semibold', compatibilityToneClasses[compatibility.tone])}>{compatibility.label}</span>}</div></div>
}
function Metric({ icon: Icon, label, value, detail }: { icon: typeof Cpu; label: string; value: string; detail: string }) { return <Card className="border-zinc-800 bg-zinc-900/80"><CardContent className="p-4"><div className="flex items-center gap-2 text-zinc-500"><Icon className="size-4" /><span className="text-[10px] font-semibold uppercase tracking-wider">{label}</span></div><p className="mt-3 text-xl font-bold text-zinc-100">{value}</p><p className="mt-1 truncate text-[10px] text-zinc-500">{detail}</p></CardContent></Card> }

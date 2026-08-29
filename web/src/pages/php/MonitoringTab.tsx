import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { RotateCcw, Activity, Zap, Server, AlertCircle } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Tooltip, TooltipContent, TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  PieChart, Pie, Cell, ResponsiveContainer, Tooltip as RechartsTooltip,
} from 'recharts'
import { api, ApiError } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { OPcacheStatus, PHPPoolStatus, PHPProcess, PHPPool } from '@/lib/types'

interface OPcacheStatusResponse {
  enabled: boolean
  hit_rate?: number
  memory_usage_percent?: number
  used_memory?: number
  free_memory?: number
  wasted_memory?: number
  cached_scripts?: number
  max_cached_keys?: number
  jit_enabled?: boolean
  jit_buffer_size?: number
}

interface PHPProcessResponse {
  pid: number
  state: string
  request_uri?: string
  request_duration?: number
  last_request_memory?: number
  request_method?: string
  content_length?: number
}

interface PHPPoolStatusResponse {
  domain: string
  active_processes?: number
  idle_processes?: number
  total_processes?: number
  listen_queue?: number
  max_children_reached?: number
  accepted_conn?: number
  slow_requests?: number
  health_score?: number
  start_time?: number
  processes?: PHPProcessResponse[]
}

function bytesToMB(value = 0) {
  return Math.round((value / 1024 / 1024) * 10) / 10
}

function normalizeOPcacheStatus(data: OPcacheStatusResponse): OPcacheStatus {
  return {
    enabled: data.enabled,
    hitRate: data.hit_rate ?? 0,
    memoryUsagePercent: data.memory_usage_percent ?? 0,
    usedMemoryMB: bytesToMB(data.used_memory),
    freeMemoryMB: bytesToMB(data.free_memory),
    wastedMemoryMB: bytesToMB(data.wasted_memory),
    cachedScripts: data.cached_scripts ?? 0,
    maxCachedKeys: data.max_cached_keys ?? 0,
    jitEnabled: data.jit_enabled ?? false,
    jitBuffer: `${bytesToMB(data.jit_buffer_size)} MB`,
  }
}

function normalizePHPProcess(process: PHPProcessResponse): PHPProcess {
  const state: PHPProcess['state'] = process.state === 'Running' || process.state === 'Finishing' ? process.state : 'Idle'
  return {
    pid: process.pid,
    state,
    requestUri: process.request_uri ?? '',
    durationMs: Math.round((process.request_duration ?? 0) / 1000),
    memoryMB: bytesToMB(process.last_request_memory),
    method: process.request_method ?? '',
    contentLength: process.content_length ?? 0,
  }
}

function normalizePHPPoolStatus(status: PHPPoolStatusResponse): PHPPoolStatus {
  return {
    domain: status.domain,
    activeProcesses: status.active_processes ?? 0,
    idleProcesses: status.idle_processes ?? 0,
    totalProcesses: status.total_processes ?? 0,
    listenQueue: status.listen_queue ?? 0,
    maxChildrenReached: status.max_children_reached ?? 0,
    acceptedConnections: status.accepted_conn ?? 0,
    slowRequests: status.slow_requests ?? 0,
    healthScore: status.health_score ?? 0,
    uptime: status.start_time ? new Date(status.start_time * 1000).toISOString() : '',
  }
}

// ─── Gauge component ──────────────────────────────────────────────────────────

function GaugeBar({ value, max = 100, color }: { value: number; max?: number; color: string }) {
  const pct = Math.min((value / max) * 100, 100)
  return (
    <div className="w-full bg-zinc-800 rounded-full h-1.5">
      <div
        className={cn('h-1.5 rounded-full transition-all', color)}
        style={{ width: `${pct}%` }}
      />
    </div>
  )
}

// ─── Donut chart ──────────────────────────────────────────────────────────────

function OpcacheMemoryChart({ used, free, wasted }: { used: number; free: number; wasted: number }) {
  const data = [
    { name: 'Used',   value: used,   color: '#22c55e' },
    { name: 'Wasted', value: wasted, color: '#f59e0b' },
    { name: 'Free',   value: free,   color: '#27272a' },
  ]
  return (
    <ResponsiveContainer width="100%" height={160}>
      <PieChart>
        <Pie
          data={data}
          cx="50%"
          cy="50%"
          innerRadius={48}
          outerRadius={72}
          dataKey="value"
          strokeWidth={0}
        >
          {data.map((entry, i) => (
            <Cell key={i} fill={entry.color} />
          ))}
        </Pie>
        <RechartsTooltip
          contentStyle={{ background: '#18181b', border: '1px solid #3f3f46', borderRadius: 8 }}
          labelStyle={{ color: '#a1a1aa' }}
          itemStyle={{ color: '#e4e4e7' }}
          formatter={(value) => [`${Number(value ?? 0)} MB`, '']}
        />
      </PieChart>
    </ResponsiveContainer>
  )
}

function MonitoringLoadError({ title, error, retry, retrying }: { title: string; error: Error; retry: () => void; retrying: boolean }) {
  return (
    <Card className="border-red-500/25 bg-red-500/[0.05]">
      <CardContent className="p-6 text-center">
        <AlertCircle className="mx-auto size-5 text-red-400" />
        <p className="mt-2 text-sm text-red-300">{title}</p>
        <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
        <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={retry} disabled={retrying}>
          <RotateCcw className={cn('mr-2 size-3.5', retrying && 'animate-spin')} />Retry
        </Button>
      </CardContent>
    </Card>
  )
}

// ─── OPcache Dashboard ────────────────────────────────────────────────────────

function OpcacheDashboard({ version }: { version: string }) {
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error, refetch, isFetching } = useQuery<OPcacheStatus>({
    queryKey: ['php', 'opcache', version],
    queryFn: async () => {
      const response = await api.get<OPcacheStatusResponse>(`/php/opcache/${version}`)
      return normalizeOPcacheStatus(response)
    },
    refetchInterval: 30_000,
  })

  const resetMutation = useMutation({
    mutationFn: () => api.post(`/php/opcache/${version}/reset`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php', 'opcache', version] })
      toast.success('OPcache reset successfully')
    },
    onError: () => toast.error('Failed to reset OPcache'),
  })

  if (isLoading) {
    return <Skeleton className="h-64 bg-zinc-800 rounded-xl" />
  }
  if (isError) return <MonitoringLoadError title={`OPcache status could not be loaded for PHP ${version}.`} error={error} retry={() => { void refetch() }} retrying={isFetching} />

  if (!data) {
    return (
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="py-10 text-center text-zinc-500 text-sm">
          OPcache status unavailable for PHP {version}
        </CardContent>
      </Card>
    )
  }

  const hitRate = data.hitRate ?? 0
  const usedMB = data.usedMemoryMB ?? 0
  const freeMB = data.freeMemoryMB ?? 0
  const hitRateColor = hitRate >= 95 ? 'text-green-400'
    : hitRate >= 80 ? 'text-amber-400' : 'text-red-400'

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center justify-between text-sm">
          <div className="flex items-center gap-2">
            <Zap className="w-4 h-4 text-blue-400" />
            <span className="text-white">OPcache</span>
            <Badge className={cn(
              'text-xs border',
              data.enabled
                ? 'bg-green-500/10 text-green-400 border-green-500/20'
                : 'bg-zinc-500/10 text-zinc-400 border-zinc-700'
            )}>
              {data.enabled ? 'Enabled' : 'Disabled'}
            </Badge>
          </div>
          <Tooltip>
            <TooltipTrigger
              render={<Button
                variant="ghost" size="icon"
                className="h-7 w-7 text-zinc-400 hover:text-red-400 hover:bg-red-400/10"
                onClick={() => resetMutation.mutate()}
                disabled={resetMutation.isPending || !data.enabled}
              >
                <RotateCcw className={cn('w-3.5 h-3.5', resetMutation.isPending && 'animate-spin')} />
              </Button>}
            />
            <TooltipContent>Reset OPcache</TooltipContent>
          </Tooltip>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          {/* Chart */}
          <div className="relative">
            <OpcacheMemoryChart
              used={usedMB}
              free={freeMB}
              wasted={data.wastedMemoryMB}
            />
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="text-center">
                <p className="text-white text-xl font-bold">{(data.memoryUsagePercent ?? 0).toFixed(0)}%</p>
                <p className="text-zinc-500 text-xs">Memory</p>
              </div>
            </div>
            {/* Legend */}
            <div className="flex justify-center gap-4 mt-2">
              {[
                { color: 'bg-green-500', label: `Used: ${usedMB}MB` },
                { color: 'bg-amber-500', label: `Wasted: ${data.wastedMemoryMB}MB` },
                { color: 'bg-zinc-600', label: `Free: ${freeMB}MB` },
              ].map(({ color, label }) => (
                <div key={label} className="flex items-center gap-1.5">
                  <span className={cn('w-2 h-2 rounded-full', color)} />
                  <span className="text-zinc-400 text-xs">{label}</span>
                </div>
              ))}
            </div>
          </div>

          {/* Stats */}
          <div className="space-y-4 flex flex-col justify-center">
            <StatRow label="Hit Rate">
              <span className={cn('text-lg font-bold', hitRateColor)}>{(data.hitRate ?? 0).toFixed(1)}%</span>
            </StatRow>
            <div className="space-y-1">
              <GaugeBar value={data.hitRate} color="bg-green-500" />
            </div>

            <StatRow label="Cached Scripts">
              <span className="text-white font-semibold">{data.cachedScripts.toLocaleString()}</span>
              <span className="text-zinc-500 text-xs"> / {data.maxCachedKeys.toLocaleString()}</span>
            </StatRow>
            <div className="space-y-1">
              <GaugeBar value={data.cachedScripts} max={data.maxCachedKeys} color="bg-blue-500" />
            </div>

            {data.jitEnabled && (
              <StatRow label="JIT">
                <div className="flex items-center gap-2">
                  <Badge className="bg-purple-500/10 text-purple-400 border-purple-500/20 text-xs">Enabled</Badge>
                  <span className="text-zinc-400 text-xs">{data.jitBuffer}</span>
                </div>
              </StatRow>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function StatRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-zinc-400 text-sm">{label}</span>
      <div className="flex items-center gap-1">{children}</div>
    </div>
  )
}

// ─── Pool Status Cards ────────────────────────────────────────────────────────

function PoolStatusCards({ pools, version }: { pools: PHPPool[]; version: string }) {
  const { data: statuses = [], isLoading, isError, error, refetch, isFetching } = useQuery<PHPPoolStatus[]>({
    queryKey: ['php', 'pool-statuses', version],
    queryFn: async () => {
      const response = await api.get<PHPPoolStatusResponse[] | null>(`/php/status/${version}`)
      return (response ?? []).map(normalizePHPPoolStatus)
    },
    refetchInterval: 30_000,
  })

  if (isLoading) {
    return (
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {pools.slice(0, 6).map((_, i) => (
          <Skeleton key={i} className="h-28 bg-zinc-800 rounded-xl" />
        ))}
      </div>
    )
  }
  if (isError) return <MonitoringLoadError title={`PHP-FPM pool status could not be loaded for PHP ${version}.`} error={error} retry={() => { void refetch() }} retrying={isFetching} />

  const statusMap = new Map<string, PHPPoolStatus>(
    statuses.map(s => [s.domain, s])
  )

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {pools.filter(p => p.version === version).map(pool => {
        const status = statusMap.get(pool.name)
        const healthScore = status?.healthScore ?? pool.healthScore ?? 0
        const healthColor = healthScore >= 80 ? 'text-green-400'
          : healthScore >= 60 ? 'text-amber-400' : 'text-red-400'
        const healthBg = healthScore >= 80 ? 'bg-green-500/10 border border-green-500/20'
          : healthScore >= 60 ? 'bg-amber-500/10 border border-amber-500/20'
          : 'bg-red-500/10 border border-red-500/20'
        const listenQueueWarning = (status?.listenQueue ?? 0) > 0

        return (
          <Card
            key={pool.name}
            className={cn(
              'bg-zinc-900 border-zinc-800 transition-all duration-200 hover:border-zinc-700 hover:shadow-md',
              listenQueueWarning && 'border-red-500/30 hover:border-red-500/50'
            )}
          >
            <CardHeader className="pb-1 pt-3 px-4">
              <CardTitle className="flex items-center justify-between">
                <div className="min-w-0">
                  <p className="text-white text-sm font-semibold truncate">{pool.name}</p>
                  <Badge className={cn('text-[10px] border mt-0.5',
                    pool.pm === 'dynamic'   ? 'bg-blue-500/10 text-blue-400 border-blue-500/20'
                    : pool.pm === 'static'  ? 'bg-purple-500/10 text-purple-400 border-purple-500/20'
                    : 'bg-amber-500/10 text-amber-400 border-amber-500/20'
                  )}>
                    {pool.pm}
                  </Badge>
                </div>
                <span className={cn('text-lg font-bold px-2 py-0.5 rounded-md', healthColor, healthBg)}>
                  {healthScore}
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="px-4 pb-3">
              <div className="grid grid-cols-3 gap-2 mt-2">
                <MiniStat label="Active" value={String(status?.activeProcesses ?? 0)} color="text-green-400" />
                <MiniStat label="Idle" value={String(status?.idleProcesses ?? 0)} color="text-zinc-400" />
                <MiniStat label="Total" value={String(status?.totalProcesses ?? 0)} color="text-white" />
              </div>
              {listenQueueWarning && (
                <div className="flex items-center gap-1.5 mt-2 text-red-400 text-xs">
                  <AlertCircle className="w-3 h-3" />
                  Queue: {status?.listenQueue} requests waiting
                </div>
              )}
              {(status?.maxChildrenReached ?? 0) > 0 && (
                <p className="text-amber-400 text-xs mt-1">
                  Max children reached: {status?.maxChildrenReached}x
                </p>
              )}
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

function MiniStat({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div className="text-center">
      <p className={cn('text-base font-bold', color)}>{value}</p>
      <p className="text-zinc-600 text-[10px]">{label}</p>
    </div>
  )
}

// ─── Process List ─────────────────────────────────────────────────────────────

function ProcessList({ version, poolName }: { version: string; poolName: string }) {
  const { data: processes = [], isLoading, error, refetch, isFetching } = useQuery<PHPProcess[]>({
    queryKey: ['php', 'processes', version, poolName],
    queryFn: async () => {
      const response = await api.get<PHPPoolStatusResponse>(`/php/status/${version}/${encodeURIComponent(poolName)}`)
      return (response.processes ?? []).map(normalizePHPProcess)
    },
    retry: (failureCount, queryError) => !(queryError instanceof ApiError && queryError.status === 422) && failureCount < 2,
    refetchInterval: 30_000,
  })

  if (isLoading) {
    return <Skeleton className="h-40 bg-zinc-800 rounded-xl" />
  }

  if (error) {
    const notConfigured = error instanceof ApiError && error.status === 422
    return (
      <div className="px-5 py-8 text-center">
        <AlertCircle className="mx-auto size-5 text-amber-400" />
        <p className="mt-2 text-sm text-zinc-300">
          {notConfigured ? 'Process monitoring is not configured for this pool.' : 'Could not load PHP-FPM processes.'}
        </p>
        <p className="mt-1 text-xs text-zinc-600">
          {notConfigured ? 'Add pm.status_path to the pool configuration to expose live worker details.' : error.message}
        </p>
        <Button type="button" variant="outline" size="sm" className="mt-4 border-zinc-700 text-zinc-300" onClick={() => { void refetch() }} disabled={isFetching}>
          <RotateCcw className={cn('mr-2 size-3.5', isFetching && 'animate-spin')} />Retry
        </Button>
      </div>
    )
  }

  if (!processes.length) {
    return (
      <div className="text-center py-8 text-zinc-500 text-sm">No active processes</div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow className="border-zinc-800 hover:bg-transparent">
            <TableHead className="text-zinc-500 text-xs">PID</TableHead>
            <TableHead className="text-zinc-500 text-xs">State</TableHead>
            <TableHead className="text-zinc-500 text-xs">Request URI</TableHead>
            <TableHead className="text-zinc-500 text-xs">Duration</TableHead>
            <TableHead className="text-zinc-500 text-xs">Memory</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {processes.map((p, idx) => (
            <TableRow
              key={p.pid}
              className={cn(
                'border-zinc-800 hover:bg-zinc-700/30 transition-colors duration-150',
                idx % 2 === 0 ? 'bg-zinc-900' : 'bg-zinc-800/20'
              )}
            >
              <TableCell>
                <span className="text-zinc-400 text-xs font-mono">{p.pid}</span>
              </TableCell>
              <TableCell>
                <Badge className={cn(
                  'text-xs border',
                  p.state === 'Running'   ? 'bg-green-500/10 text-green-400 border-green-500/20'
                  : p.state === 'Idle'    ? 'bg-zinc-500/10 text-zinc-400 border-zinc-700'
                  : 'bg-amber-500/10 text-amber-400 border-amber-500/20'
                )}>
                  {p.state}
                </Badge>
              </TableCell>
              <TableCell>
                <span className="text-zinc-300 text-xs font-mono truncate max-w-48 block" title={p.requestUri}>
                  {p.requestUri || '—'}
                </span>
              </TableCell>
              <TableCell>
                <span className="text-zinc-300 text-xs">
                  {p.durationMs > 0 ? `${p.durationMs}ms` : '—'}
                </span>
              </TableCell>
              <TableCell>
                <span className="text-zinc-300 text-xs">{p.memoryMB > 0 ? `${p.memoryMB}MB` : '—'}</span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// ─── MonitoringTab ────────────────────────────────────────────────────────────

interface MonitoringTabProps {
  selectedVersion: string
  pools: PHPPool[]
}

export default function MonitoringTab({ selectedVersion, pools }: MonitoringTabProps) {
  const [selectedPool, setSelectedPool] = useState<string>('')
  const versionPools = pools.filter(p => p.version === selectedVersion)

  const currentPool = selectedPool || versionPools[0]?.name || ''

  return (
    <div className="space-y-6">
      {/* OPcache Dashboard */}
      <div>
        <h3 className="text-zinc-400 text-xs uppercase tracking-wider mb-3 flex items-center gap-2">
          <Zap className="w-3.5 h-3.5" /> OPcache · PHP {selectedVersion}
        </h3>
        <OpcacheDashboard version={selectedVersion} />
      </div>

      {/* Pool Status Cards */}
      {versionPools.length > 0 && (
        <div>
          <h3 className="text-zinc-400 text-xs uppercase tracking-wider mb-3 flex items-center gap-2">
            <Activity className="w-3.5 h-3.5" /> Pool Status
            <span className="text-zinc-600 normal-case text-[11px]">auto-refresh 5s</span>
          </h3>
          <PoolStatusCards pools={versionPools} version={selectedVersion} />
        </div>
      )}

      {/* Process List */}
      {versionPools.length > 0 && (
        <div>
          <div className="flex items-center gap-3 mb-3">
            <h3 className="text-zinc-400 text-xs uppercase tracking-wider flex items-center gap-2">
              <Server className="w-3.5 h-3.5" /> Process List
            </h3>
            <select
              value={currentPool}
              onChange={e => setSelectedPool(e.target.value)}
              className="bg-zinc-800 border border-zinc-700 text-zinc-300 text-xs rounded-md px-2 py-1.5 h-7"
            >
              {versionPools.map(p => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>
          <Card className="bg-zinc-900 border-zinc-800">
            <CardContent className="p-0">
              <ProcessList version={selectedVersion} poolName={currentPool} />
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  )
}

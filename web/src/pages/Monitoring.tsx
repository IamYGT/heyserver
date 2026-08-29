import { useState, useMemo, useCallback, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import {
  AreaChart, Area, LineChart, Line, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, ReferenceLine, Label,
} from 'recharts'
import {
  Cpu, MemoryStick, HardDrive, Network, TrendingUp,
  Server, Clock, Database, Search, ArrowUpRight, ArrowDownRight,
  Minus, Play, RefreshCw, Zap, ChevronDown, RotateCcw, Trash2,
  Power, Skull, Loader2, AlertTriangle,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemStats, useServiceStatuses } from '@/hooks/useStats'
import { useCurrentUser } from '@/hooks/useAuth'
import { hostActionStatusKey, useHostActionStatus } from '@/hooks/useHostActionStatus'
import {
  useMetricsHistory, useProcessSnapshot, useServiceHistory,
  useMetricsSummary, type MetricsRange,
} from '@/hooks/useMetrics'
import { api } from '@/lib/api'
import { hostActionConfirmation, hostActionLabel, rebootControlState, rebootStatusQueryKey, type RebootStatus } from '@/lib/hostControls'
import { serviceFocusMatches } from '@/lib/serverNavigation'
import { cn } from '@/lib/utils'
import type { MetricRaw, MetricAggregated, ProcessSnapshot, SystemStats } from '@/lib/types'

// ── Constants ────────────────────────────────────────────────────────────────

const RANGES: { label: string; value: MetricsRange; shortLabel: string }[] = [
  { label: '1 Hour', value: '1h', shortLabel: '1H' },
  { label: '6 Hours', value: '6h', shortLabel: '6H' },
  { label: '24 Hours', value: '24h', shortLabel: '24H' },
  { label: '7 Days', value: '7d', shortLabel: '7D' },
  { label: '30 Days', value: '30d', shortLabel: '30D' },
]

const CHART_HEIGHT = 200

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatRate(bytesPerSec: number): string {
  if (bytesPerSec === 0) return '0 B/s'
  const k = 1024
  const sizes = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  const i = Math.floor(Math.log(Math.abs(bytesPerSec)) / Math.log(k))
  return `${(bytesPerSec / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatTimestamp(ts: string, range: MetricsRange): string {
  const d = new Date(ts)
  if (range === '1h' || range === '6h') {
    return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
  }
  if (range === '24h') {
    return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })
  }
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

function getTrend(data: Array<{ v: number }>): 'up' | 'down' | 'flat' {
  if (data.length < 4) return 'flat'
  const recent = data.slice(-4)
  const first = recent[0]?.v ?? 0
  const last = recent[recent.length - 1]?.v ?? 0
  const diff = last - first
  if (Math.abs(diff) < 1) return 'flat'
  return diff > 0 ? 'up' : 'down'
}

// ── Normalize Data ───────────────────────────────────────────────────────────

interface ChartPoint {
  time: string
  ts: string
  cpu: number
  cpuMax?: number
  mem: number
  memMax?: number
  memBuffers?: number
  memCached?: number
  memUsedPct?: number
  swap: number
  load1: number
  load5?: number
  load15?: number
  disk: number
  diskMax?: number
  netRx: number
  netTx: number
  // Computed fields
  netRxRate?: number
  netTxRate?: number
}

function normalizeData(
  data: MetricRaw[] | MetricAggregated[] | undefined,
  resolution: string | undefined,
  range: MetricsRange,
): ChartPoint[] {
  if (!data || data.length === 0) return []

  let points: ChartPoint[]

  if (resolution === 'raw') {
    const raw = data as MetricRaw[]
    points = raw.map((d) => ({
      time: formatTimestamp(d.timestamp, range),
      ts: d.timestamp,
      cpu: d.cpu_percent,
      mem: d.memory_percent,
      memBuffers: d.memory_total > 0 ? (d.memory_buffers / d.memory_total) * 100 : 0,
      memCached: d.memory_total > 0 ? (d.memory_cached / d.memory_total) * 100 : 0,
      memUsedPct: d.memory_percent,
      swap: d.swap_percent,
      load1: d.load_1m,
      load5: d.load_5m,
      load15: d.load_15m,
      disk: d.disk_root_percent,
      netRx: d.net_rx_bytes,
      netTx: d.net_tx_bytes,
    }))
  } else {
    points = (data as MetricAggregated[]).map((d) => ({
      time: formatTimestamp(d.bucket, range),
      ts: d.bucket,
      cpu: d.cpu_avg,
      cpuMax: d.cpu_max,
      mem: d.mem_avg,
      memMax: d.mem_max,
      swap: d.swap_avg,
      load1: d.load_1m_avg,
      disk: d.disk_root_avg,
      diskMax: d.disk_root_max,
      netRx: d.net_rx_total,
      netTx: d.net_tx_total,
    }))
  }

  // Compute network rates (delta between consecutive samples)
  for (let i = 1; i < points.length; i++) {
    const prev = points[i - 1]
    const curr = points[i]
    const prevTime = new Date(prev.ts).getTime()
    const currTime = new Date(curr.ts).getTime()
    const dtSec = (currTime - prevTime) / 1000
    if (dtSec > 0) {
      const rxDelta = curr.netRx - prev.netRx
      const txDelta = curr.netTx - prev.netTx
      curr.netRxRate = rxDelta > 0 ? rxDelta / dtSec : 0
      curr.netTxRate = txDelta > 0 ? txDelta / dtSec : 0
    }
  }
  if (points.length > 0) {
    points[0].netRxRate = points[1]?.netRxRate ?? 0
    points[0].netTxRate = points[1]?.netTxRate ?? 0
  }

  return points
}

// ── Custom Tooltip ───────────────────────────────────────────────────────────

function ChartTooltip({ active, payload, label, formatter }: {
  active?: boolean; payload?: Array<{ value?: number; color?: string; name?: string }>
  label?: string | number; formatter?: (v: number, name: string) => string
}) {
  if (!active || !payload || payload.length === 0) return null
  return (
    <div className="bg-zinc-950/95 backdrop-blur-xl border border-zinc-700/50 rounded-xl px-3 py-2 shadow-2xl">
      <p className="text-zinc-400 text-[10px] mb-1 font-mono">{String(label)}</p>
      {payload.map((entry, i) => (
        <div key={i} className="flex items-center gap-2 text-xs">
          <div className="w-2 h-2 rounded-full" style={{ backgroundColor: entry.color }} />
          <span className="text-zinc-300">{entry.name}:</span>
          <span className="text-white font-semibold font-mono">
            {formatter ? formatter(entry.value as number, entry.name ?? '') : String(entry.value)}
          </span>
        </div>
      ))}
    </div>
  )
}

// ── Shared Chart Config ──────────────────────────────────────────────────────

const GRID = <CartesianGrid strokeDasharray="3 3" stroke="#27272a" vertical={false} />
const AXIS_STYLE = { stroke: '#3f3f46', fontSize: 10, tickLine: false, axisLine: false }

// ── Animated Ring Gauge ──────────────────────────────────────────────────────

function RingGauge({ value, label, subtitle, icon: Icon, color, accentColor, trend, size = 56 }: {
  value: number; label: string; subtitle: string
  icon: React.ComponentType<{ className?: string }>
  color: string; accentColor: string
  trend?: 'up' | 'down' | 'flat'
  size?: number
}) {
  const r = (size - 8) / 2
  const circumference = 2 * Math.PI * r
  const clampedValue = Math.min(100, Math.max(0, value))
  const offset = circumference - (clampedValue / 100) * circumference

  const strokeColor = clampedValue > 90 ? '#ef4444' : clampedValue > 75 ? '#f59e0b' : accentColor

  const TrendIcon = trend === 'up' ? ArrowUpRight : trend === 'down' ? ArrowDownRight : Minus
  const trendColor = trend === 'up' ? 'text-red-400' : trend === 'down' ? 'text-emerald-400' : 'text-zinc-500'

  return (
    <div className={`relative group cursor-default rounded-2xl border border-zinc-800/80 bg-gradient-to-br ${color} p-3 transition-all hover:border-zinc-700/80 hover:shadow-lg hover:shadow-black/20`}>
      {/* Glow effect */}
      <div className="absolute inset-0 rounded-2xl opacity-0 group-hover:opacity-100 transition-opacity" style={{ background: `radial-gradient(circle at 50% 50%, ${accentColor}10, transparent 70%)` }} />

      <div className="relative flex items-center gap-3">
        {/* Ring */}
        <div className="relative shrink-0" style={{ width: size, height: size }}>
          <svg className="-rotate-90" width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
            <circle cx={size/2} cy={size/2} r={r} fill="none" stroke="#27272a" strokeWidth="5" />
            <circle
              cx={size/2} cy={size/2} r={r}
              fill="none"
              stroke={strokeColor}
              strokeWidth="5"
              strokeLinecap="round"
              strokeDasharray={circumference}
              strokeDashoffset={offset}
              className="transition-all duration-700 ease-out"
            />
          </svg>
          <div className="absolute inset-0 flex items-center justify-center">
            <Icon className="w-4 h-4 text-zinc-400" />
          </div>
        </div>

        {/* Text */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="text-white text-lg font-bold tabular-nums">{value.toFixed(1)}%</span>
            {trend && trend !== 'flat' && (
              <TrendIcon className={`w-3.5 h-3.5 ${trendColor}`} />
            )}
          </div>
          <p className="text-zinc-400 text-[10px] font-semibold uppercase tracking-wider">{label}</p>
          <p className="text-zinc-500 text-[10px] truncate">{subtitle}</p>
        </div>
      </div>
    </div>
  )
}

// ── Load Gauge (non-percentage) ──────────────────────────────────────────────

function LoadGauge({ value, label, subtitle, cores }: {
  value: number; label: string; subtitle: string; cores: number
}) {
  const pct = Math.min(100, (value / cores) * 100)
  const isHigh = pct > 80

  return (
    <div className="relative group cursor-default rounded-2xl border border-zinc-800/80 bg-gradient-to-br from-zinc-900 to-zinc-900/50 p-3 transition-all hover:border-zinc-700/80 hover:shadow-lg hover:shadow-black/20">
      <div className="flex items-center gap-3">
        <div className="w-14 h-14 relative shrink-0">
          <svg className="-rotate-90" width="56" height="56" viewBox="0 0 56 56">
            <circle cx="28" cy="28" r="24" fill="none" stroke="#27272a" strokeWidth="5" />
            <circle
              cx="28" cy="28" r="24"
              fill="none"
              stroke={isHigh ? '#ef4444' : '#f59e0b'}
              strokeWidth="5"
              strokeLinecap="round"
              strokeDasharray={2 * Math.PI * 24}
              strokeDashoffset={2 * Math.PI * 24 * (1 - pct / 100)}
              className="transition-all duration-700 ease-out"
            />
          </svg>
          <div className="absolute inset-0 flex items-center justify-center">
            <TrendingUp className="w-4 h-4 text-zinc-400" />
          </div>
        </div>
        <div className="min-w-0 flex-1">
          <span className="text-white text-lg font-bold tabular-nums">{value.toFixed(2)}</span>
          <p className="text-zinc-400 text-[10px] font-semibold uppercase tracking-wider">{label}</p>
          <p className="text-zinc-500 text-[10px] truncate">{subtitle}</p>
        </div>
      </div>
    </div>
  )
}

// ── Range Selector ───────────────────────────────────────────────────────────

function RangeSelector({ value, onChange }: { value: MetricsRange; onChange: (v: MetricsRange) => void }) {
  return (
    <div className="flex items-center gap-0.5 bg-zinc-800/60 rounded-xl p-1 border border-zinc-700/30">
      {RANGES.map((r) => (
        <button
          key={r.value}
          onClick={() => onChange(r.value)}
          className={`px-3 py-1.5 text-xs rounded-lg transition-all font-medium ${
            value === r.value
              ? 'bg-zinc-600/80 text-white shadow-sm shadow-black/20'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-700/30'
          }`}
        >
          <span className="hidden sm:inline">{r.label}</span>
          <span className="sm:hidden">{r.shortLabel}</span>
        </button>
      ))}
    </div>
  )
}

// ── Live Indicator ───────────────────────────────────────────────────────────

function LiveIndicator({ paused, onToggle, sampleCount }: {
  paused: boolean; onToggle: () => void; sampleCount: number
}) {
  return (
    <div className="flex items-center gap-3">
      <button
        onClick={onToggle}
        className={`flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium border transition-all ${
          paused
            ? 'bg-zinc-800 border-zinc-700 text-zinc-400 hover:text-zinc-200'
            : 'bg-emerald-500/10 border-emerald-500/20 text-emerald-400 hover:bg-emerald-500/20'
        }`}
      >
        {paused ? (
          <><Play className="w-3 h-3" /> Paused</>
        ) : (
          <>
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
            </span>
            Live
          </>
        )}
      </button>
      <span className="text-zinc-600 text-[10px] tabular-nums">{sampleCount.toLocaleString()} samples</span>
    </div>
  )
}

// ── Metric Chart Card ────────────────────────────────────────────────────────

function MetricChart({ title, icon: Icon, iconColor, children, resolution }: {
  title: string; icon: React.ComponentType<{ className?: string }>
  iconColor: string; children: React.ReactNode; resolution?: string
}) {
  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm overflow-hidden group hover:border-zinc-700/60 transition-colors">
      <CardHeader className="pb-0 pt-3 px-4">
        <CardTitle className="text-zinc-200 text-xs font-semibold flex items-center gap-2 uppercase tracking-wider">
          <Icon className={`w-3.5 h-3.5 ${iconColor}`} />
          {title}
          {resolution && (
            <span className="ml-auto text-zinc-600 text-[10px] font-normal normal-case tracking-normal bg-zinc-800/50 px-1.5 py-0.5 rounded">
              {resolution}
            </span>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="pb-2 px-2 pt-1">
        {children}
      </CardContent>
    </Card>
  )
}

function NoData({ sampleCount }: { sampleCount: number }) {
  const message = sampleCount === 0 ? 'No historical samples yet' : 'Not enough historical samples yet'

  return (
    <div className="h-[180px] flex flex-col items-center justify-center text-zinc-600 gap-2" role="status">
      <Minus className="w-5 h-5 text-zinc-700" aria-hidden="true" />
      <span className="text-xs">{message}</span>
    </div>
  )
}

// ── Host Actions ─────────────────────────────────────────────────────────────

type HostAction = 'memory-optimize' | 'swap-reset' | 'temp-clean' | 'reboot'

const hostActions: Array<{
  id: HostAction
  label: string
  description: string
  icon: React.ComponentType<{ className?: string }>
  destructive?: boolean
}> = [
  {
    id: 'memory-optimize',
    label: 'Optimize RAM',
    description: 'Release filesystem caches; keep processes and swap running',
    icon: MemoryStick,
  },
  {
    id: 'swap-reset',
    label: 'Reset swap',
    description: 'Cycle configured swap off and back on',
    icon: RotateCcw,
  },
  {
    id: 'temp-clean',
    label: 'Clean temp',
    description: 'Apply host tmpfiles expiry policy',
    icon: Trash2,
  },
  {
    id: 'reboot',
    label: 'Reboot server',
    description: 'Schedule a reboot in 10 seconds',
    icon: Power,
    destructive: true,
  },
]

function HostActions({ memory }: { memory?: SystemStats['memory'] }) {
  const queryClient = useQueryClient()
	const actionStatus = useHostActionStatus('local')
	const actionStatusUnavailable = actionStatus.isLoading || actionStatus.isError
  const rebootStatus = useQuery<RebootStatus>({
    queryKey: rebootStatusQueryKey('local'),
    queryFn: () => api.get('/system/actions/reboot-status'),
    refetchInterval: (query) => query.state.data?.pending ? 1_000 : 10_000,
  })
  const rebootControl = rebootControlState(rebootStatus.data, rebootStatus)
  const swapRequired = (memory?.swapUsed ?? 0) + 512 * 1024 * 1024
  const controlMemory = memory ? { total: memory.swapTotal, used: memory.swapUsed, available: memory.available } : undefined
  const swapReason = !memory
    ? 'Loading swap state…'
    : memory.swapTotal === 0
      ? 'No active swap is configured'
      : memory.swapUsed === 0
        ? 'Swap is already empty'
        : memory.available < swapRequired
          ? `Needs ${formatBytes(swapRequired)} available; ${formatBytes(memory.available)} is available`
          : ''
  const swapEligible = swapReason === ''
  const cancelReboot = useMutation<{ message: string }, Error>({
    mutationFn: () => api.post('/system/actions/reboot-cancel'),
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey('local') })
    },
    onError: (error) => toast.error(error.message || 'Could not cancel the scheduled reboot'),
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey('local') }),
  })
  const mutation = useMutation<{ message: string }, Error, HostAction>({
    mutationFn: (action) => api.post(`/system/actions/${action}`),
    onSuccess: async (result, action) => {
      if (action === 'reboot') {
        toast.success(result.message, {
          duration: 10_000,
          action: { label: 'Cancel reboot', onClick: () => cancelReboot.mutate() },
        })
      } else {
        toast.success(result.message)
      }
      await queryClient.invalidateQueries({ queryKey: ['stats', 'system'] })
      if (action === 'reboot') {
        await queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey('local') })
      }
    },
    onError: (error) => toast.error(error.message || 'System action failed'),
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey('local') }),
  })

  const run = (action: typeof hostActions[number]) => {
    const confirmation = hostActionConfirmation(action.id, 'HServer', controlMemory, swapRequired)
    if (window.confirm(confirmation)) {
      mutation.mutate(action.id)
    }
  }

  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm overflow-hidden">
      <CardContent className="p-3">
				{actionStatusUnavailable && (
					<div className="mb-3 flex items-center justify-between gap-3 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-[10px] text-amber-300">
						<span>{actionStatus.isError ? (actionStatus.isFetching ? 'Retrying active host maintenance status…' : 'Could not verify active host maintenance. Host controls are paused.') : 'Checking active host maintenance before enabling host controls…'}</span>
						{actionStatus.isError && <Button type="button" variant="ghost" size="xs" disabled={actionStatus.isFetching} onClick={() => actionStatus.refetch()}><RefreshCw className={cn('size-3', actionStatus.isFetching && 'animate-spin')} /> Retry</Button>}
					</div>
				)}
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
          <div className="min-w-48">
            <p className="text-xs font-semibold uppercase tracking-wider text-zinc-200">Host controls</p>
						<p className="mt-0.5 text-[10px] text-zinc-500">
							{actionStatusUnavailable ? (actionStatus.isError ? 'Could not verify active host maintenance' : 'Checking active host maintenance…') : actionStatus.data?.running ? `${hostActionLabel(actionStatus.data.action)} is running` : 'Admin-only actions · every request is audited'}
						</p>
          </div>
          <div className="grid flex-1 grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
            {hostActions.map((action) => {
								const rebootPending = action.id === 'reboot' && rebootControl.pending
								const rebootStatusUnavailable = action.id === 'reboot' && rebootControl.blocked
								const rebootRetryable = action.id === 'reboot' && rebootControl.retryable
								const Icon = rebootRetryable ? RefreshCw : rebootPending ? RotateCcw : action.icon
								const activeMaintenance = actionStatus.data?.running === true
								const pending = (mutation.isPending && mutation.variables === action.id) || (rebootPending && cancelReboot.isPending)
									|| rebootStatusUnavailable
									|| (activeMaintenance && (actionStatus.data?.action === action.id || (action.id === 'reboot' && actionStatus.data?.action === 'reboot-cancel')))
								const disabled = actionStatusUnavailable || activeMaintenance || mutation.isPending || cancelReboot.isPending || rebootStatusUnavailable || (action.id === 'swap-reset' && !swapEligible)
								const description = actionStatusUnavailable
									? (actionStatus.isError ? 'Could not verify active host maintenance' : 'Checking active host maintenance…')
								: activeMaintenance
									? `${hostActionLabel(actionStatus.data?.action)} is already running`
								: action.id === 'reboot'
									? rebootControl.description
                  : action.id === 'swap-reset'
                ? (swapEligible ? `${formatBytes(memory?.swapUsed ?? 0)} used · ${formatBytes(memory?.available ?? 0)} RAM available · ${formatBytes(swapRequired)} required` : swapReason)
                : action.id === 'memory-optimize' && memory
                  ? `${formatBytes(memory.available)} RAM available · releases reclaimable caches only`
                : action.description
              return (
                <button
                  key={action.id}
                  type="button"
                  disabled={disabled}
								title={disabled ? description : undefined}
								onClick={() => rebootRetryable ? void rebootStatus.refetch() : rebootPending ? cancelReboot.mutate() : run(action)}
                  className={`flex items-center gap-2 rounded-xl border px-3 py-2 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
                    action.destructive
                      ? 'border-red-900/50 bg-red-950/20 hover:border-red-700/60 hover:bg-red-950/35'
                      : 'border-zinc-800 bg-zinc-950/35 hover:border-zinc-700 hover:bg-zinc-800/45'
                  }`}
                >
                  <span className={`grid size-8 shrink-0 place-items-center rounded-lg ${action.destructive ? 'bg-red-500/10 text-red-400' : 'bg-zinc-800 text-zinc-300'}`}>
                    {pending ? <Loader2 className="size-4 animate-spin" /> : <Icon className="size-4" />}
                  </span>
                  <span className="min-w-0">
									<span className={`block text-xs font-semibold ${action.destructive ? 'text-red-300' : 'text-zinc-200'}`}>{rebootRetryable ? 'Retry reboot status' : rebootPending ? 'Cancel reboot' : action.label}</span>
                    <span className="block truncate text-[10px] text-zinc-500">{description}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

// ── Process Explorer ─────────────────────────────────────────────────────────

interface LiveProcess {
  pid: number
  startTime: number
  user: string
  cpu: number
  memory: number
  rss: number
  command: string
}

function ProcessExplorer({ range, chartData, canManage, paused, focused = false }: { range: MetricsRange; chartData: ChartPoint[]; canManage: boolean; paused: boolean; focused?: boolean }) {
  const [selectedTs, setSelectedTs] = useState<string | undefined>()
  const [sortBy, setSortBy] = useState<'cpu' | 'mem'>('cpu')
  const [searchQuery, setSearchQuery] = useState('')
  const {
    data: snapshot,
    isLoading: snapshotLoading,
    isFetching: snapshotFetching,
    error: snapshotError,
    refetch: refetchSnapshot,
  } = useProcessSnapshot(selectedTs)
  const queryClient = useQueryClient()
  const {
    data: liveProcesses,
    isLoading: liveLoading,
    error: liveError,
    isFetching: liveFetching,
    refetch: refetchLive,
  } = useQuery<LiveProcess[]>({
    queryKey: ['monitoring', 'live-processes'],
    queryFn: () => api.get<LiveProcess[]>('/monitoring/processes'),
    refetchInterval: selectedTs || paused ? false : 5000,
  })

  const terminate = useMutation<{ message: string; exited: boolean; confirmed: boolean }, Error, { pid: number; startTime: number; signal: 'term' | 'kill' }>({
    mutationFn: (payload) => api.post('/system/actions/process', payload),
    onSuccess: async (result) => {
      if (result.exited) toast.success(result.message)
      else toast.warning(result.message)
      await refetchLive()
      queryClient.invalidateQueries({ queryKey: ['metrics', 'processes'] })
    },
    onError: (error) => toast.error(error.message || 'Process action failed'),
  })

  const stopProcess = (process: ProcessSnapshot, signal: 'term' | 'kill') => {
    if (!process.start_time) {
      toast.error('Process identity is unavailable; refresh the live list and try again')
      return
    }
    const verb = signal === 'kill' ? 'force kill' : 'stop'
    if (window.confirm(`Really ${verb} PID ${process.pid}?\n\n${process.command}`)) {
      terminate.mutate({ pid: process.pid, startTime: process.start_time, signal })
    }
  }

  const handleChartClick = useCallback((data: Record<string, unknown> | null | undefined) => {
    if (data && typeof data === 'object' && 'activePayload' in data) {
      const payload = data as { activePayload?: Array<{ payload: { ts: string } }> }
      const ts = payload.activePayload?.[0]?.payload?.ts
      if (ts) setSelectedTs(ts)
    }
  }, [])

  const processes = useMemo(() => {
    let procs: ProcessSnapshot[] = selectedTs
      ? (snapshot?.processes ?? [])
      : (liveProcesses ?? []).map((process) => ({
          timestamp: new Date().toISOString(),
          pid: process.pid,
          start_time: process.startTime,
          username: process.user,
          cpu_percent: process.cpu,
          memory_percent: process.memory,
          rss: process.rss,
          command: process.command,
        }))
    if (searchQuery) {
      const q = searchQuery.toLowerCase()
      procs = procs.filter(p =>
        p.command.toLowerCase().includes(q) ||
        p.username.toLowerCase().includes(q) ||
        String(p.pid).includes(q)
      )
    }
    return [...procs].sort((a, b) =>
      sortBy === 'cpu' ? b.cpu_percent - a.cpu_percent : b.memory_percent - a.memory_percent
    )
  }, [selectedTs, snapshot, liveProcesses, sortBy, searchQuery])

  const isLoading = selectedTs ? snapshotLoading : liveLoading
  const loadError = selectedTs ? snapshotError : liveError
  const processFetching = selectedTs ? snapshotFetching : liveFetching
  const retryProcesses = selectedTs ? refetchSnapshot : refetchLive

  const maxCpu = Math.max(...(processes.map(p => p.cpu_percent)), 1)
  const maxMem = Math.max(...(processes.map(p => p.memory_percent)), 1)

  useEffect(() => {
    if (!focused) return
    const scrollToProcesses = () => {
      document.getElementById('local-processes')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
    window.requestAnimationFrame(scrollToProcesses)
    const settleTimer = window.setTimeout(scrollToProcesses, 500)
    return () => window.clearTimeout(settleTimer)
  }, [focused])

  return (
    <Card id="local-processes" className={cn('bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm overflow-hidden transition', focused && 'border-blue-500/50 ring-1 ring-blue-500/30')}>
      <CardHeader className="pb-2 pt-3">
        <div className="flex flex-col sm:flex-row sm:items-center gap-2">
          <CardTitle className="text-zinc-200 text-xs font-semibold flex items-center gap-2 uppercase tracking-wider">
            <Zap className="w-3.5 h-3.5 text-orange-400" />
            Process Explorer
          </CardTitle>
          <div className="flex items-center gap-2 sm:ml-auto">
            {/* Sort toggle */}
            <div className="flex items-center bg-zinc-800/60 rounded-lg p-0.5 border border-zinc-700/30">
              <button
                onClick={() => setSortBy('cpu')}
                className={`px-2 py-1 text-[10px] rounded-md transition-all font-medium ${sortBy === 'cpu' ? 'bg-zinc-600/80 text-white' : 'text-zinc-500 hover:text-zinc-300'}`}
              >
                CPU
              </button>
              <button
                onClick={() => setSortBy('mem')}
                className={`px-2 py-1 text-[10px] rounded-md transition-all font-medium ${sortBy === 'mem' ? 'bg-zinc-600/80 text-white' : 'text-zinc-500 hover:text-zinc-300'}`}
              >
                MEM
              </button>
            </div>
            {/* Search */}
            <div className="relative">
              <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3 h-3 text-zinc-500" />
              <input
                type="text"
                placeholder="Filter..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="bg-zinc-800/60 border border-zinc-700/30 text-zinc-300 text-xs rounded-lg pl-6 pr-2 py-1.5 w-32 focus:outline-none focus:border-zinc-600 placeholder:text-zinc-600"
              />
            </div>
            {selectedTs && (
              <Button variant="outline" size="xs" onClick={() => setSelectedTs(undefined)}>
                <Play className="size-3" /> Live now
              </Button>
            )}
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {/* Mini timeline chart */}
        <div className="mb-3">
          <p className="text-zinc-600 text-[10px] mb-1 flex items-center gap-1">
            <ChevronDown className="w-3 h-3" /> Click chart to inspect processes at that time
          </p>
          <div className="rounded-lg overflow-hidden bg-zinc-800/30 p-1">
            <ResponsiveContainer width="100%" height={60}>
              <AreaChart data={chartData} onClick={handleChartClick} style={{ cursor: 'crosshair' }}>
                <defs>
                  <linearGradient id="procCpu" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#f97316" stopOpacity={0.3} />
                    <stop offset="100%" stopColor="#f97316" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="procMem" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#22c55e" stopOpacity={0.2} />
                    <stop offset="100%" stopColor="#22c55e" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <Area type="monotone" dataKey="cpu" stroke="#f97316" fill="url(#procCpu)" strokeWidth={1.5} dot={false} />
                <Area type="monotone" dataKey="mem" stroke="#22c55e" fill="url(#procMem)" strokeWidth={1} dot={false} />
                {selectedTs && (
                  <ReferenceLine x={formatTimestamp(selectedTs, range)} stroke="#f97316" strokeWidth={2} strokeDasharray="3 3" />
                )}
                <XAxis dataKey="time" hide />
                <YAxis hide domain={[0, 100]} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>

        {isLoading ? (
          <Skeleton className="h-48 bg-zinc-800/50 rounded-xl" />
        ) : loadError ? (
          <div role="alert" className="flex flex-col items-center justify-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.06] px-4 py-8 text-center">
            <AlertTriangle className="size-5 text-red-400" />
            <div>
              <p className="text-sm font-medium text-red-300">
                {selectedTs ? 'Process snapshot could not be loaded' : 'Live process list could not be loaded'}
              </p>
              <p className="mt-1 max-w-lg break-words text-xs text-zinc-500">
                {loadError.message || 'The server did not return process data.'}
              </p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={() => void retryProcesses()} disabled={processFetching}>
              <RefreshCw className={cn('size-3.5', processFetching && 'animate-spin')} />
              Retry process list
            </Button>
          </div>
        ) : processes.length === 0 ? (
          <div className="text-zinc-600 text-xs text-center py-8">
            {selectedTs ? 'No process data for this timestamp' : 'No live processes were returned'}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="text-zinc-500 border-b border-zinc-800/80">
                  <th className="text-left py-1.5 px-2 font-medium w-16">PID</th>
                  <th className="text-left py-1.5 px-2 font-medium w-20">User</th>
                  <th className="text-left py-1.5 px-2 font-medium w-36">CPU%</th>
                  <th className="text-left py-1.5 px-2 font-medium w-36">MEM%</th>
                  <th className="text-right py-1.5 px-2 font-medium w-20">RSS</th>
                  <th className="text-left py-1.5 px-2 font-medium">Command</th>
                  {canManage && !selectedTs && <th className="text-right py-1.5 px-2 font-medium w-24">Actions</th>}
                </tr>
              </thead>
              <tbody>
                {processes.map((p, i) => {
                  const pendingProcess = terminate.isPending
                    && terminate.variables?.pid === p.pid
                    && terminate.variables?.startTime === p.start_time
                  const pendingTerm = pendingProcess && terminate.variables?.signal === 'term'
                  const pendingKill = pendingProcess && terminate.variables?.signal === 'kill'
                  return (
                  <tr key={`${p.pid}-${p.start_time ?? i}`} className={cn('border-b border-zinc-800/30 transition-colors hover:bg-zinc-800/20', pendingProcess && 'bg-blue-500/[0.06]')}>
                    <td className="py-1.5 px-2 text-zinc-500 font-mono tabular-nums">{p.pid}</td>
                    <td className="py-1.5 px-2 text-zinc-400 truncate">{p.username}</td>
                    <td className="py-1.5 px-2">
                      <div className="flex items-center gap-2">
                        <div className="flex-1 h-1.5 bg-zinc-800 rounded-full overflow-hidden">
                          <div
                            className="h-full rounded-full transition-all"
                            style={{
                              width: `${Math.min(100, (p.cpu_percent / maxCpu) * 100)}%`,
                              backgroundColor: p.cpu_percent > 50 ? '#ef4444' : p.cpu_percent > 20 ? '#f59e0b' : '#3b82f6',
                            }}
                          />
                        </div>
                        <span className="text-zinc-300 font-mono tabular-nums w-10 text-right">{p.cpu_percent.toFixed(1)}</span>
                      </div>
                    </td>
                    <td className="py-1.5 px-2">
                      <div className="flex items-center gap-2">
                        <div className="flex-1 h-1.5 bg-zinc-800 rounded-full overflow-hidden">
                          <div
                            className="h-full rounded-full transition-all"
                            style={{
                              width: `${Math.min(100, (p.memory_percent / maxMem) * 100)}%`,
                              backgroundColor: p.memory_percent > 50 ? '#ef4444' : p.memory_percent > 20 ? '#f59e0b' : '#22c55e',
                            }}
                          />
                        </div>
                        <span className="text-zinc-300 font-mono tabular-nums w-10 text-right">{p.memory_percent.toFixed(1)}</span>
                      </div>
                    </td>
                    <td className="py-1.5 px-2 text-right text-zinc-400 font-mono tabular-nums">{formatBytes(p.rss)}</td>
                    <td className="py-1.5 px-2 text-zinc-300 font-mono truncate max-w-[250px]" title={p.command}>
                      {p.command}
                    </td>
                    {canManage && !selectedTs && (
                      <td className="py-1.5 px-2">
                        {p.pid <= 1 ? (
                          <span className="block text-right text-[10px] text-zinc-600">Protected</span>
                        ) : (
                          <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={pendingTerm ? `Stopping PID ${p.pid}…` : 'Stop process (SIGTERM)'}
                            aria-label={pendingTerm ? `Stopping PID ${p.pid}` : `Stop PID ${p.pid}`}
                            disabled={terminate.isPending || !p.start_time}
                            onClick={() => stopProcess(p, 'term')}
                            className="text-amber-400 hover:bg-amber-500/10 hover:text-amber-300"
                          >
                            {pendingTerm ? <Loader2 className="size-3 animate-spin" /> : <Power className="size-3" />}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={pendingKill ? `Force killing PID ${p.pid}…` : 'Force kill process (SIGKILL)'}
                            aria-label={pendingKill ? `Force killing PID ${p.pid}` : `Force kill PID ${p.pid}`}
                            disabled={terminate.isPending || !p.start_time}
                            onClick={() => stopProcess(p, 'kill')}
                            className="text-red-400 hover:bg-red-500/10 hover:text-red-300"
                          >
                            {pendingKill ? <Loader2 className="size-3 animate-spin" /> : <Skull className="size-3" />}
                          </Button>
                          </div>
                        )}
                      </td>
                    )}
                  </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ── Service Timeline ─────────────────────────────────────────────────────────

type ServiceControlAction = 'start' | 'stop' | 'restart'

function ServiceTimeline({ range, canManage, paused }: { range: MetricsRange; canManage: boolean; paused: boolean }) {
  const { data: history, isLoading: historyLoading, isError: historyError, error: historyFailure, isFetching: historyFetching, refetch: refetchHistory } = useServiceHistory(range, !paused)
  const { data: currentStatuses, isLoading: statusesLoading, isError: statusesError, error: statusesFailure, isFetching: statusesFetching, refetch: refetchStatuses } = useServiceStatuses(true, !paused)
  const [searchParams] = useSearchParams()
  const focusedService = searchParams.get('service')
  const queryClient = useQueryClient()
  const serviceAction = useMutation<{ message: string }, Error, { service: string; action: ServiceControlAction }>({
    mutationFn: (payload) => api.post('/system/actions/service', payload),
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: ['stats', 'services'] })
    },
    onError: (error) => toast.error(error.message || 'Service action failed'),
  })

  const controlService = (service: string, action: ServiceControlAction) => {
    const prompt = action === 'stop'
      ? `Stop ${service} on HServer? Dependent sites or workers may become unavailable.`
      : action === 'restart'
        ? `Restart ${service} on HServer now? A short interruption may occur.`
        : null
    if (!prompt || window.confirm(prompt)) serviceAction.mutate({ service, action })
  }

  const serviceData = useMemo(() => {
    if (!currentStatuses) return []

    const map = new Map<string, { changes: number; uptimePct: number; lastDown: string | null; total: number; up: number; lastRunning: boolean }>()

    for (const entry of history ?? []) {
      const running = entry.status === 'running'
      const existing = map.get(entry.name)
      if (!existing) {
        map.set(entry.name, {
          changes: 0,
          uptimePct: 0,
          lastDown: running ? null : entry.timestamp,
          total: 1,
          up: running ? 1 : 0,
          lastRunning: running,
        })
      } else {
        existing.total++
        if (running) existing.up++
        if (!running) existing.lastDown = entry.timestamp
        if (existing.lastRunning !== running) existing.changes++
        existing.lastRunning = running
      }
    }

    // Calculate uptime %
    for (const [, data] of map) {
      data.uptimePct = data.total > 0 ? (data.up / data.total) * 100 : 100
    }

    return currentStatuses.map(svc => ({
      ...svc,
      history: map.get(svc.name),
    }))
  }, [history, currentStatuses])
  const serviceLoading = historyLoading || statusesLoading
  const serviceFailed = historyError || statusesError
  const serviceFetching = historyFetching || statusesFetching
  const retryServices = async () => { await Promise.all([refetchHistory(), refetchStatuses()]) }

  useEffect(() => {
    if (!focusedService || serviceData.length === 0) return
    const scrollToService = () => {
      const target = serviceData.find(service => serviceFocusMatches(service.name, focusedService))
      if (target) document.getElementById(`local-service-${encodeURIComponent(target.name)}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
    window.requestAnimationFrame(scrollToService)
    const settleTimer = window.setTimeout(scrollToService, 500)
    return () => window.clearTimeout(settleTimer)
  }, [focusedService, serviceData])

  return (
    <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm overflow-hidden">
      <CardHeader className="pb-2 pt-3">
        <CardTitle className="text-zinc-200 text-xs font-semibold flex items-center gap-2 uppercase tracking-wider">
          <Server className="w-3.5 h-3.5 text-emerald-400" />
          Service Availability
        </CardTitle>
      </CardHeader>
      <CardContent>
        {serviceLoading ? (
          <Skeleton className="h-32 bg-zinc-800/50 rounded-xl" />
        ) : serviceFailed ? (
          <div role="alert" className="flex flex-col items-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.06] px-4 py-8 text-center">
            <AlertTriangle className="size-5 text-red-400" />
            <div><p className="text-sm font-medium text-red-300">Service availability could not be loaded</p><p className="mt-1 max-w-xl break-words text-xs text-zinc-500">{statusesFailure?.message || historyFailure?.message || 'The server did not return current service state and history.'}</p><p className="mt-2 text-[10px] text-zinc-600">No uptime percentage or healthy service state is inferred from this failure.</p></div>
            <Button type="button" variant="outline" size="sm" disabled={serviceFetching} onClick={() => void retryServices()}><RefreshCw className={cn('size-3.5', serviceFetching && 'animate-spin')} /> Retry service availability</Button>
          </div>
        ) : serviceData.length === 0 ? (
          <div className="rounded-xl border border-dashed border-zinc-800 px-4 py-8 text-center text-xs text-zinc-600">No monitored services were returned.</div>
        ) : (
          <div className="space-y-2.5">
            {serviceData.map((svc) => {
              const isUp = svc.status === 'running'
              const isDegraded = svc.status === 'degraded'
              const isTransitioning = svc.status === 'starting' || svc.status === 'stopping'
              const uptimePct = svc.history?.uptimePct ?? 100
              const pending = serviceAction.isPending && serviceAction.variables?.service === svc.name
              return (
                <div id={`local-service-${encodeURIComponent(svc.name)}`} key={svc.name} className={cn('group rounded-lg transition', focusedService && serviceFocusMatches(svc.name, focusedService) && 'bg-amber-500/[0.08] ring-1 ring-amber-500/40 px-2 py-2')}>
                  <div className="flex items-center gap-3">
                    {/* Status dot */}
                    <div className="relative">
                      <div className={`w-2.5 h-2.5 rounded-full ${isUp ? 'bg-emerald-400' : isDegraded || isTransitioning ? 'bg-amber-400' : 'bg-red-400'}`} />
                      {isUp && <div className="absolute inset-0 w-2.5 h-2.5 rounded-full bg-emerald-400 animate-ping opacity-30" />}
                    </div>

                    {/* Service name */}
                    <span className="text-zinc-300 text-xs font-mono w-28 truncate">{svc.name}</span>

                    {/* Availability bar */}
                    <div className="flex-1 h-5 bg-zinc-800/80 rounded-md overflow-hidden relative">
                      <div
                        className={`h-full rounded-md transition-all duration-500 ${
                          uptimePct >= 99.9 ? 'bg-emerald-500/30' :
                          uptimePct >= 99 ? 'bg-emerald-500/20' :
                          uptimePct >= 95 ? 'bg-amber-500/20' : 'bg-red-500/20'
                        }`}
                        style={{ width: `${uptimePct}%` }}
                      />
                      <span className="absolute inset-0 flex items-center justify-center text-[10px] font-mono text-zinc-400">
                        {uptimePct.toFixed(1)}% uptime
                      </span>
                    </div>

                    {/* Status badge */}
                    <span className={`text-[10px] font-bold px-2 py-0.5 rounded-md ${
                      isUp ? 'bg-emerald-500/10 text-emerald-400' : isDegraded || isTransitioning ? 'bg-amber-500/10 text-amber-400' : 'bg-red-500/10 text-red-400'
                    }`}>
                      {isUp ? 'UP' : svc.status?.toUpperCase()}
                    </span>

                    {/* Restart count */}
                    {svc.history && svc.history.changes > 0 && (
                      <span className="text-amber-500 text-[10px] font-mono" title={`Last down: ${svc.history.lastDown ?? 'unknown'}`}>
                        {svc.history.changes}x
                      </span>
                    )}

                    {/* Uptime */}
                    {svc.uptime && (
                      <span className="text-zinc-600 text-[10px] font-mono hidden sm:inline w-16 text-right">{svc.uptime}</span>
                    )}

                    {canManage && (
                      <div className="flex shrink-0 items-center gap-1">
                        {isTransitioning ? (
                          <Loader2 className="mx-2 size-3 animate-spin text-amber-400" />
                        ) : isUp || isDegraded ? (
                          <>
                            <Button variant="ghost" size="icon-xs" disabled={serviceAction.isPending} title={`Restart ${svc.name}`} aria-label={`Restart ${svc.name}`} onClick={() => controlService(svc.name, 'restart')}>
                              {pending && serviceAction.variables?.action === 'restart' ? <Loader2 className="size-3 animate-spin" /> : <RotateCcw className="size-3 text-amber-400" />}
                            </Button>
                            <Button variant="ghost" size="icon-xs" disabled={serviceAction.isPending} title={`Stop ${svc.name}`} aria-label={`Stop ${svc.name}`} onClick={() => controlService(svc.name, 'stop')}>
                              {pending && serviceAction.variables?.action === 'stop' ? <Loader2 className="size-3 animate-spin" /> : <Power className="size-3 text-red-400" />}
                            </Button>
                          </>
                        ) : (
                          <Button variant="ghost" size="icon-xs" disabled={serviceAction.isPending} title={`Start ${svc.name}`} aria-label={`Start ${svc.name}`} onClick={() => controlService(svc.name, 'start')}>
                            {pending ? <Loader2 className="size-3 animate-spin" /> : <Play className="size-3 text-emerald-400" />}
                          </Button>
                        )}
                      </div>
                    )}
                  </div>
                  {svc.detail && <p className="ml-[148px] mt-1 truncate text-[10px] text-amber-500/80" title={svc.detail}>{svc.detail}</p>}
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ── Main Page ────────────────────────────────────────────────────────────────

export default function Monitoring() {
  const [searchParams] = useSearchParams()
  const [range, setRange] = useState<MetricsRange>('1h')
  const [paused, setPaused] = useState(false)
  const { data: stats, isError: statsError, error: statsFailure, isFetching: statsFetching, refetch: refetchStats } = useSystemStats(true, !paused)
  const { data: metricsRes, isLoading: metricsLoading, isError: metricsError, error: metricsFailure, isFetching: metricsFetching, refetch: refetchMetrics } = useMetricsHistory(range, !paused)
  const { data: summary } = useMetricsSummary()
  const { data: currentUser } = useCurrentUser()
  const canManage = currentUser?.role === 'admin'

  const chartData = useMemo(
    () => normalizeData(metricsRes?.data, metricsRes?.resolution, range),
    [metricsRes, range],
  )

  const hasData = chartData.length >= 2

  // Trends from chart data
  const trends = useMemo(() => {
    if (chartData.length < 4) return { cpu: 'flat' as const, mem: 'flat' as const, swap: 'flat' as const, disk: 'flat' as const }
    return {
      cpu: getTrend(chartData.map(d => ({ v: d.cpu }))),
      mem: getTrend(chartData.map(d => ({ v: d.mem }))),
      swap: getTrend(chartData.map(d => ({ v: d.swap }))),
      disk: getTrend(chartData.map(d => ({ v: d.disk }))),
    }
  }, [chartData])

  return (
    <div className="space-y-4">
      {/* ── Header ──────────────────────────────────────────────────────────── */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
        <div>
          <h2 className="text-white text-xl font-bold flex items-center gap-2">
            System Monitoring
          </h2>
          <p className="text-zinc-500 text-xs mt-0.5">
            Historical performance metrics & process analysis
            {summary && (
              <span className="text-zinc-600 ml-1">
                — DB: {formatBytes(summary.db_size_bytes)}
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <LiveIndicator paused={paused} onToggle={() => setPaused(!paused)} sampleCount={summary?.total_samples ?? 0} />
          <RangeSelector value={range} onChange={setRange} />
        </div>
      </div>

      {canManage && <HostActions memory={stats?.memory} />}

      {statsError && (
        <Card role="alert" className="border-red-500/25 bg-red-500/[0.06]">
          <CardContent className="flex flex-col gap-3 p-5 text-xs text-red-300 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-3"><AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-400" /><div><p className="font-medium">Live system stats could not be loaded</p><p className="mt-1 break-words text-zinc-500">{statsFailure?.message || 'The server did not return current CPU, memory, swap, disk, or load state.'}</p><p className="mt-2 text-[10px] text-zinc-600">Historical metrics remain separate; no current resource state is inferred from them.</p></div></div>
            <Button type="button" variant="outline" size="sm" disabled={statsFetching} onClick={() => void refetchStats()}><RefreshCw className={cn('size-3.5', statsFetching && 'animate-spin')} /> Retry live stats</Button>
          </CardContent>
        </Card>
      )}

      {/* ── Ring Gauges ─────────────────────────────────────────────────────── */}
      {stats && (
        <div className="grid grid-cols-2 lg:grid-cols-5 gap-2">
          <RingGauge
            value={stats.cpu.usage}
            label="CPU"
            subtitle={`${stats.cpu.cores} cores · ${stats.cpu.model.split(' ').slice(0, 2).join(' ')}`}
            icon={Cpu}
            color="from-blue-950/30 to-zinc-900"
            accentColor="#3b82f6"
            trend={trends.cpu}
          />
          <RingGauge
            value={stats.memory.percentage}
            label="Memory"
            subtitle={`${(stats.memory.used / 1024 ** 3).toFixed(1)} / ${(stats.memory.total / 1024 ** 3).toFixed(1)} GB`}
            icon={MemoryStick}
            color="from-green-950/30 to-zinc-900"
            accentColor="#22c55e"
            trend={trends.mem}
          />
          <RingGauge
            value={stats.memory.swapPercentage ?? 0}
            label="Swap"
            subtitle={`${((stats.memory.swapUsed ?? 0) / 1024 ** 3).toFixed(1)} / ${((stats.memory.swapTotal ?? 0) / 1024 ** 3).toFixed(1)} GB`}
            icon={Database}
            color="from-cyan-950/30 to-zinc-900"
            accentColor="#06b6d4"
            trend={trends.swap}
          />
          <RingGauge
            value={stats.disk[0]?.percentage ?? 0}
            label="Disk /"
            subtitle={`${((stats.disk[0]?.used ?? 0) / 1024 ** 3).toFixed(0)} / ${((stats.disk[0]?.total ?? 0) / 1024 ** 3).toFixed(0)} GB`}
            icon={HardDrive}
            color="from-purple-950/30 to-zinc-900"
            accentColor="#a855f7"
            trend={trends.disk}
          />
          <LoadGauge
            value={stats.load[0]}
            label="Load Avg"
            subtitle={`5m: ${stats.load[1].toFixed(2)} · 15m: ${stats.load[2].toFixed(2)}`}
            cores={stats.cpu.cores}
          />
        </div>
      )}

      {/* ── Chart Loading ──────────────────────────────────────────────────── */}
      {metricsLoading && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {[1, 2, 3, 4, 5, 6].map((i) => <Skeleton key={i} className="h-56 bg-zinc-900 rounded-xl" />)}
        </div>
      )}

      {metricsError && (
        <Card role="alert" className="border-red-500/25 bg-red-500/[0.06]">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <AlertTriangle className="size-6 text-red-400" />
            <div>
              <p className="text-sm font-medium text-red-300">Historical metrics could not be loaded</p>
              <p className="mt-1 max-w-xl break-words text-xs text-zinc-500">{metricsFailure?.message || 'The server did not return historical CPU, memory, swap, disk, load, or network data.'}</p>
              <p className="mt-2 text-[10px] text-zinc-600">No empty chart or collector state is inferred from this request failure.</p>
            </div>
            <Button type="button" variant="outline" size="sm" disabled={metricsFetching} onClick={() => void refetchMetrics()}>
              <RefreshCw className={cn('size-3.5', metricsFetching && 'animate-spin')} /> Retry historical metrics
            </Button>
          </CardContent>
        </Card>
      )}

      {/* ── Charts ─────────────────────────────────────────────────────────── */}
      {!metricsLoading && !metricsError && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {/* CPU */}
          <MetricChart title="CPU Usage" icon={Cpu} iconColor="text-blue-400" resolution={metricsRes?.resolution}>
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="cpuGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#3b82f6" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="#3b82f6" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} domain={[0, 100]} tickFormatter={(v: number) => `${v}%`} width={35} />
                  <Tooltip content={<ChartTooltip formatter={(v) => `${v.toFixed(1)}%`} />} />
                  <ReferenceLine y={80} stroke="#f59e0b" strokeDasharray="4 4" strokeWidth={0.5} />
                  <Area type="monotone" dataKey="cpu" stroke="#3b82f6" fill="url(#cpuGrad)" strokeWidth={2} dot={false} name="CPU" />
                  {chartData[0]?.cpuMax !== undefined && (
                    <Line type="monotone" dataKey="cpuMax" stroke="#3b82f680" strokeDasharray="3 3" strokeWidth={1} dot={false} name="CPU Max" />
                  )}
                </AreaChart>
              </ResponsiveContainer>
            )}
          </MetricChart>

          {/* Memory Breakdown */}
          <MetricChart title="Memory Breakdown" icon={MemoryStick} iconColor="text-green-400">
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <AreaChart data={chartData} stackOffset="none">
                  <defs>
                    <linearGradient id="memUsedGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#22c55e" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#22c55e" stopOpacity={0.05} />
                    </linearGradient>
                    <linearGradient id="memCachedGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#06b6d4" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="#06b6d4" stopOpacity={0.02} />
                    </linearGradient>
                    <linearGradient id="memBuffersGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#8b5cf6" stopOpacity={0.15} />
                      <stop offset="100%" stopColor="#8b5cf6" stopOpacity={0.02} />
                    </linearGradient>
                  </defs>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} domain={[0, 100]} tickFormatter={(v: number) => `${v}%`} width={35} />
                  <Tooltip content={<ChartTooltip formatter={(v) => `${v.toFixed(1)}%`} />} />
                  {chartData[0]?.memBuffers !== undefined && (
                    <>
                      <Area type="monotone" dataKey="memUsedPct" stackId="mem" stroke="#22c55e" fill="url(#memUsedGrad)" strokeWidth={1.5} dot={false} name="Used" />
                      <Area type="monotone" dataKey="memCached" stackId="mem" stroke="#06b6d4" fill="url(#memCachedGrad)" strokeWidth={1} dot={false} name="Cached" />
                      <Area type="monotone" dataKey="memBuffers" stackId="mem" stroke="#8b5cf6" fill="url(#memBuffersGrad)" strokeWidth={1} dot={false} name="Buffers" />
                    </>
                  )}
                  {chartData[0]?.memBuffers === undefined && (
                    <Area type="monotone" dataKey="mem" stroke="#22c55e" fill="url(#memUsedGrad)" strokeWidth={2} dot={false} name="Memory" />
                  )}
                </AreaChart>
              </ResponsiveContainer>
            )}
          </MetricChart>

          {/* Swap */}
          <MetricChart title="Swap Usage" icon={Database} iconColor="text-cyan-400">
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="swapGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#06b6d4" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="#06b6d4" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} domain={[0, 100]} tickFormatter={(v: number) => `${v}%`} width={35} />
                  <Tooltip content={<ChartTooltip formatter={(v) => `${v.toFixed(1)}%`} />} />
                  <Area type="monotone" dataKey="swap" stroke="#06b6d4" fill="url(#swapGrad)" strokeWidth={2} dot={false} name="Swap" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </MetricChart>

          {/* Load Average */}
          <MetricChart title="Load Average" icon={TrendingUp} iconColor="text-amber-400">
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <LineChart data={chartData}>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} width={35} />
                  <Tooltip content={<ChartTooltip formatter={(v) => v.toFixed(2)} />} />
                  <Line type="monotone" dataKey="load1" stroke="#f59e0b" strokeWidth={2} dot={false} name="1 min" />
                  {chartData[0]?.load5 !== undefined && (
                    <Line type="monotone" dataKey="load5" stroke="#d97706" strokeWidth={1.5} dot={false} name="5 min" strokeDasharray="4 2" />
                  )}
                  {chartData[0]?.load15 !== undefined && (
                    <Line type="monotone" dataKey="load15" stroke="#92400e" strokeWidth={1} dot={false} name="15 min" strokeDasharray="2 2" />
                  )}
                  <ReferenceLine y={stats?.cpu.cores ?? 0} stroke="#ef4444" strokeDasharray="4 4" strokeWidth={0.5}>
                    <Label position="right" value={`${stats?.cpu.cores ?? 0} cores`} fill="#ef4444" fontSize={9} />
                  </ReferenceLine>
                </LineChart>
              </ResponsiveContainer>
            )}
          </MetricChart>

          {/* Disk */}
          <MetricChart title="Disk Usage (/)" icon={HardDrive} iconColor="text-purple-400">
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="diskGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#a855f7" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="#a855f7" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} domain={[0, 100]} tickFormatter={(v: number) => `${v}%`} width={35} />
                  <Tooltip content={<ChartTooltip formatter={(v) => `${v.toFixed(1)}%`} />} />
                  <ReferenceLine y={85} stroke="#ef4444" strokeDasharray="4 4" strokeWidth={0.5}>
                    <Label position="right" value="85%" fill="#ef4444" fontSize={9} />
                  </ReferenceLine>
                  <Area type="monotone" dataKey="disk" stroke="#a855f7" fill="url(#diskGrad)" strokeWidth={2} dot={false} name="Disk" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </MetricChart>

          {/* Network Rate */}
          <MetricChart title="Network Throughput" icon={Network} iconColor="text-rose-400">
            {!hasData ? <NoData sampleCount={chartData.length} /> : (
              <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="rxGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#f43f5e" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="#f43f5e" stopOpacity={0} />
                    </linearGradient>
                    <linearGradient id="txGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#fb923c" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="#fb923c" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  {GRID}
                  <XAxis dataKey="time" {...AXIS_STYLE} interval="preserveStartEnd" />
                  <YAxis {...AXIS_STYLE} tickFormatter={(v: number) => formatRate(v)} width={55} />
                  <Tooltip content={<ChartTooltip formatter={(v, name) => `${formatRate(v)} ${name === 'RX (In)' ? '↓' : '↑'}`} />} />
                  <Area type="monotone" dataKey="netRxRate" stroke="#f43f5e" fill="url(#rxGrad)" strokeWidth={1.5} dot={false} name="RX (In)" />
                  <Area type="monotone" dataKey="netTxRate" stroke="#fb923c" fill="url(#txGrad)" strokeWidth={1.5} dot={false} name="TX (Out)" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </MetricChart>
        </div>
      )}

      {/* ── Process Explorer ───────────────────────────────────────────────── */}
      <ProcessExplorer range={range} chartData={chartData} canManage={canManage} paused={paused} focused={searchParams.get('focus') === 'processes'} />

      {/* ── Service Timeline ───────────────────────────────────────────────── */}
      <ServiceTimeline range={range} canManage={canManage} paused={paused} />

      {/* ── System Info + Disk Details ─────────────────────────────────────── */}
      {stats && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-3">
          <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm">
            <CardHeader className="pb-2 pt-3">
              <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
                <Server className="w-3.5 h-3.5 text-zinc-400" />
                System Info
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-xs">
              <div className="flex justify-between"><span className="text-zinc-500">Hostname</span><span className="text-white font-mono">{stats.hostname}</span></div>
              <div className="flex justify-between"><span className="text-zinc-500">OS</span><span className="text-white">{stats.os}</span></div>
              <div className="flex justify-between"><span className="text-zinc-500">CPU</span><span className="text-white truncate max-w-[180px]" title={stats.cpu.model}>{stats.cpu.model}</span></div>
              <div className="flex justify-between"><span className="text-zinc-500">Uptime</span><span className="text-white">{Math.floor(stats.uptime / 86400)}d {Math.floor((stats.uptime % 86400) / 3600)}h {Math.floor((stats.uptime % 3600) / 60)}m</span></div>
            </CardContent>
          </Card>

          {/* Disk Details — all mounts */}
          <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm">
            <CardHeader className="pb-2 pt-3">
              <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
                <HardDrive className="w-3.5 h-3.5 text-purple-400" />
                All Disks
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2.5">
              {stats.disk?.map((d) => (
                <div key={d.mount} className="space-y-1">
                  <div className="flex justify-between text-xs">
                    <span className="text-zinc-400 font-mono">{d.mount}</span>
                    <span className={`font-mono tabular-nums ${d.percentage > 85 ? 'text-red-400' : d.percentage > 60 ? 'text-amber-400' : 'text-zinc-300'}`}>
                      {d.percentage.toFixed(1)}%
                    </span>
                  </div>
                  <div className="h-1.5 bg-zinc-800 rounded-full overflow-hidden">
                    <div
                      className="h-full rounded-full transition-all"
                      style={{
                        width: `${d.percentage}%`,
                        backgroundColor: d.percentage > 85 ? '#ef4444' : d.percentage > 60 ? '#f59e0b' : '#a855f7',
                      }}
                    />
                  </div>
                  <div className="flex justify-between text-[10px] text-zinc-600">
                    <span>{formatBytes(d.used)} used</span>
                    <span>{formatBytes(d.total)} total</span>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          {/* Network Interfaces */}
          <Card className="bg-zinc-900/80 border-zinc-800/80 backdrop-blur-sm">
            <CardHeader className="pb-2 pt-3">
              <CardTitle className="text-zinc-200 text-xs font-semibold uppercase tracking-wider flex items-center gap-2">
                <Network className="w-3.5 h-3.5 text-rose-400" />
                Network Interfaces
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2.5 text-xs">
              {stats.network?.map((net) => (
                <div key={net.interface} className="flex items-center justify-between py-1 border-b border-zinc-800/50 last:border-0">
                  <span className="text-zinc-400 font-mono">{net.interface}</span>
                  <div className="flex items-center gap-3 text-[10px]">
                    <span className="text-rose-400 font-mono">↓ {formatBytes(net.bytesIn)}</span>
                    <span className="text-orange-400 font-mono">↑ {formatBytes(net.bytesOut)}</span>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>
        </div>
      )}

      {/* ── Metrics DB Info ────────────────────────────────────────────────── */}
      {summary && (
        <div className="flex items-center justify-center gap-6 py-2 text-zinc-600 text-[10px]">
          <span className="flex items-center gap-1"><Database className="w-3 h-3" /> {summary.total_samples.toLocaleString()} samples</span>
          <span>DB: {formatBytes(summary.db_size_bytes)}</span>
          {summary.oldest_timestamp && <span>Since: {new Date(summary.oldest_timestamp).toLocaleString('en-US', { hour12: false })}</span>}
          <span className="flex items-center gap-1"><Clock className="w-3 h-3" /> 15s interval · 30d retention</span>
        </div>
      )}
    </div>
  )
}

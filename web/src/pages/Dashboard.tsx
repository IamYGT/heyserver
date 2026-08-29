import { useState, useCallback, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  AreaChart, Area, ResponsiveContainer,
} from 'recharts'
import {
  Cpu, MemoryStick, HardDrive, Activity, Clock, RefreshCw,
  Globe, ShieldCheck, Archive, Lock, Mail, FileText,
  AlertTriangle, CheckCircle2, XCircle, Loader2,
  Users, Inbox, ExternalLink, X, Radar, RotateCcw, Play, Power, Search,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemStats, useServiceStatuses } from '@/hooks/useStats'
import { useCurrentUser } from '@/hooks/useAuth'
import { useMetricsHistory } from '@/hooks/useMetrics'
import { hostActionStatusKey, useHostActionStatus } from '@/hooks/useHostActionStatus'
import { api } from '@/lib/api'
import { hostActionConfirmation, hostActionEndpoint, hostActionLabel } from '@/lib/hostControls'
import type { SSLCertificate, AuditLog, PM2Process, MailQueueItem, NginxTestResult, UptimeSummary, MetricRaw } from '@/lib/types'

// ─── Utility helpers ──────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  const gb = bytes / 1024 ** 3
  return `${gb.toFixed(1)} GB`
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

function timeAgo(ts: string): string {
  const diff = Math.floor((Date.now() - new Date(ts).getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

function match<T extends string, R>(value: T, cases: Record<T, R>): R {
  return cases[value]
}

function formatLogTimestamp(value: string): string {
  const timestamp = new Date(value)
  return Number.isNaN(timestamp.getTime()) ? value : timestamp.toLocaleString()
}

// ─── Gauge ────────────────────────────────────────────────────────────────────

interface GaugeProps {
  label: string
  value: number
  subtitle: string
  icon: React.ComponentType<{ className?: string }>
  color: string
  sparklineData?: Array<{ v: number }>
  sparklineColor?: string
  actionLabel?: string
  actionTitle?: string
  actionDisabled?: boolean
  actionPending?: boolean
  onAction?: () => void
}

function Gauge({
  label,
  value,
  subtitle,
  icon: Icon,
  color,
  sparklineData,
  sparklineColor,
  actionLabel,
  actionTitle,
  actionDisabled,
  actionPending,
  onAction,
}: GaugeProps) {
  const clampedValue = Math.min(100, Math.max(0, value))
  const circumference = 2 * Math.PI * 40
  const strokeDashoffset = circumference - (clampedValue / 100) * circumference

  const colorMap: Record<string, string> = {
    blue: '#3b82f6',
    green: '#22c55e',
    amber: '#f59e0b',
    red: '#ef4444',
  }

  const strokeColor =
    clampedValue > 85
      ? colorMap.red
      : clampedValue > 60
      ? colorMap.amber
      : colorMap[color] ?? colorMap.blue

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardContent className="pt-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <p className="text-zinc-400 text-xs font-medium uppercase tracking-wide">{label}</p>
            <p className="text-white text-2xl font-bold mt-0.5">{value.toFixed(0)}%</p>
            <p className="text-zinc-500 text-xs mt-0.5">{subtitle}</p>
          </div>
          <div className="relative w-16 h-16">
            <svg className="w-16 h-16 -rotate-90" viewBox="0 0 100 100">
              <circle cx="50" cy="50" r="40" fill="none" stroke="#27272a" strokeWidth="8" />
              <circle
                cx="50" cy="50" r="40"
                fill="none"
                stroke={strokeColor}
                strokeWidth="8"
                strokeLinecap="round"
                strokeDasharray={circumference}
                strokeDashoffset={strokeDashoffset}
                style={{ transition: 'stroke-dashoffset 0.5s ease' }}
              />
            </svg>
            <div className="absolute inset-0 flex items-center justify-center">
              <Icon className="w-5 h-5 text-zinc-400" />
            </div>
          </div>
        </div>
        <div className="w-full bg-zinc-800 rounded-full h-1">
          <div
            className="h-1 rounded-full transition-all duration-500"
            style={{ width: `${clampedValue}%`, backgroundColor: strokeColor }}
          />
        </div>
        {sparklineData && sparklineData.length >= 2 && (
          <div className="mt-2 -mx-1">
            <ResponsiveContainer width="100%" height={32}>
              <AreaChart data={sparklineData}>
                <Area
                  type="monotone"
                  dataKey="v"
                  stroke={sparklineColor ?? strokeColor}
                  fill={sparklineColor ?? strokeColor}
                  fillOpacity={0.1}
                  strokeWidth={1}
                  dot={false}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
        {actionLabel && onAction && (
          <button
            type="button"
            title={actionTitle}
            disabled={actionDisabled}
            onClick={onAction}
            className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg border border-zinc-700/70 bg-zinc-800/50 px-3 py-2 text-xs font-semibold text-zinc-300 transition-colors hover:border-blue-500/50 hover:bg-blue-500/10 hover:text-blue-300 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {actionPending && <Loader2 className="size-3.5 animate-spin" />}
            {actionLabel}
          </button>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Service Badge ────────────────────────────────────────────────────────────

import type { ServiceStatus } from '@/lib/types'

type ServiceControlAction = 'start' | 'stop' | 'restart'

interface ServiceBadgeProps {
  service: ServiceStatus
  canManage: boolean
  controlsDisabled: boolean
  pendingAction?: ServiceControlAction
  onOpenLogs: () => void
  onControl: (action: ServiceControlAction) => void
}

function ServiceBadge({ service, canManage, controlsDisabled, pendingAction, onOpenLogs, onControl }: ServiceBadgeProps) {
  const variant = match(service.status, {
    running: 'default',
    degraded: 'destructive',
    starting: 'secondary',
    stopping: 'secondary',
    stopped: 'secondary',
    failed: 'destructive',
    unknown: 'outline',
  } as const)

  const dot = match(service.status, {
    running: 'bg-green-500',
    degraded: 'bg-amber-500',
    starting: 'bg-blue-500',
    stopping: 'bg-amber-500',
    stopped: 'bg-zinc-500',
    failed: 'bg-red-500',
    unknown: 'bg-zinc-600',
  } as const)

  return (
    <div className={`flex flex-col gap-3 rounded-lg border px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between ${service.status === 'degraded' ? 'bg-amber-500/5 border-amber-500/30' : 'bg-zinc-800/50 border-zinc-800'}`}>
      <div className="flex items-center gap-2.5">
        <div
          className={`w-1.5 h-1.5 rounded-full ${dot} ${service.status === 'running' ? 'animate-pulse' : ''}`}
        />
        <div className="min-w-0">
          <span className="text-white text-sm font-medium">{service.name}</span>
          {service.detail && <p className="mt-0.5 truncate font-mono text-[10px] text-amber-300/80" title={service.detail}>{service.detail}</p>}
        </div>
      </div>
      <div className="flex items-center justify-end gap-1.5">
        <button
          type="button"
          onClick={onOpenLogs}
          title={`View recent ${service.name} logs`}
          aria-label={`View recent ${service.name} logs`}
          className="grid size-7 place-items-center rounded-md text-zinc-500 transition-colors hover:bg-zinc-700 hover:text-zinc-200"
        >
          <FileText className="size-3.5" />
        </button>
        {canManage && (service.status === 'starting' || service.status === 'stopping' ? (
          <Loader2 className="mx-1 size-3.5 animate-spin text-amber-400" />
        ) : service.status === 'running' || service.status === 'degraded' ? (
          <>
            <button
              type="button"
              disabled={controlsDisabled}
              onClick={() => onControl('restart')}
              title={`Restart ${service.name}`}
              aria-label={`Restart ${service.name}`}
              className="grid size-7 place-items-center rounded-md text-amber-400 transition-colors hover:bg-amber-500/10 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {pendingAction === 'restart' ? <Loader2 className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}
            </button>
            <button
              type="button"
              disabled={controlsDisabled}
              onClick={() => onControl('stop')}
              title={`Stop ${service.name}`}
              aria-label={`Stop ${service.name}`}
              className="grid size-7 place-items-center rounded-md text-red-400 transition-colors hover:bg-red-500/10 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {pendingAction === 'stop' ? <Loader2 className="size-3.5 animate-spin" /> : <Power className="size-3.5" />}
            </button>
          </>
        ) : (
          <button
            type="button"
            disabled={controlsDisabled}
            onClick={() => onControl('start')}
            title={`Start ${service.name}`}
            aria-label={`Start ${service.name}`}
            className="grid size-7 place-items-center rounded-md text-emerald-400 transition-colors hover:bg-emerald-500/10 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {pendingAction === 'start' ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
          </button>
        ))}
        {service.uptime && <span className="text-zinc-500 text-xs">{service.uptime}</span>}
        <Badge
          variant={variant as 'default' | 'secondary' | 'destructive' | 'outline'}
          className="text-xs capitalize"
        >
          {service.status}
        </Badge>
      </div>
    </div>
  )
}

interface ServiceLogEntry {
  timestamp: string
  unit: string
  priority: number
  message: string
}

type ServiceLogLevel = 'all' | 'error' | 'warning' | 'info'

function ServiceLogModal({ service, onClose }: { service: string; onClose: () => void }) {
  const [search, setSearch] = useState('')
  const [level, setLevel] = useState<ServiceLogLevel>('all')
  const { data, isLoading, isFetching, error, refetch } = useQuery<{ service: string; lines: ServiceLogEntry[] }>({
    queryKey: ['dashboard', 'service-logs', service],
    queryFn: () => api.get(`/system/services/${encodeURIComponent(service)}/logs?lines=100`),
    staleTime: 0,
  })
  const entries = useMemo(() => data?.lines ?? [], [data?.lines])
  const filteredEntries = useMemo(() => {
    const query = search.trim().toLowerCase()
    return entries.filter((entry) => {
      const matchesLevel = level === 'all'
        || (level === 'error' && entry.priority <= 3)
        || (level === 'warning' && entry.priority <= 4)
        || (level === 'info' && entry.priority <= 6)
      const matchesSearch = query === '' || `${entry.message} ${entry.unit} ${entry.timestamp}`.toLowerCase().includes(query)
      return matchesLevel && matchesSearch
    })
  }, [entries, level, search])
  const filtersActive = search.trim() !== '' || level !== 'all'

  const clearFilters = () => {
    setSearch('')
    setLevel('all')
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label={`${service} recent logs`}>
      <div className="w-full max-w-4xl overflow-hidden rounded-xl border border-zinc-700 bg-zinc-900 shadow-2xl">
        <div className="flex items-center justify-between gap-3 border-b border-zinc-800 px-5 py-4">
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-white">{service} · recent systemd logs</p>
            <p className="mt-0.5 text-[10px] text-zinc-500">Read-only · latest 100 journal entries</p>
          </div>
          <div className="flex items-center gap-1">
            <button type="button" onClick={() => refetch()} disabled={isFetching} aria-label={`Refresh ${service} logs`} title="Refresh logs" className="grid size-8 place-items-center rounded-lg text-zinc-500 hover:bg-zinc-800 hover:text-white disabled:opacity-40">
              <RefreshCw className={`size-4 ${isFetching ? 'animate-spin' : ''}`} />
            </button>
            <button type="button" onClick={onClose} aria-label="Close service logs" className="grid size-8 place-items-center rounded-lg text-zinc-500 hover:bg-zinc-800 hover:text-white">
              <X className="size-4" />
            </button>
          </div>
        </div>
        <div className="flex flex-col gap-2 border-b border-zinc-800 px-4 py-3 sm:flex-row sm:items-center">
          <label className="relative min-w-0 flex-1">
            <span className="sr-only">Search {service} logs</span>
            <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-zinc-600" />
            <input
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search message, unit or timestamp…"
              className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 pl-9 pr-3 text-xs text-zinc-200 outline-none transition focus:border-blue-500/60"
            />
          </label>
          <select
            value={level}
            onChange={(event) => setLevel(event.target.value as ServiceLogLevel)}
            aria-label={`${service} log severity`}
            className="h-9 rounded-lg border border-zinc-700 bg-zinc-950 px-3 text-xs text-zinc-300"
          >
            <option value="all">All levels</option>
            <option value="error">Errors only</option>
            <option value="warning">Warnings + errors</option>
            <option value="info">Info + higher</option>
          </select>
          {filtersActive && (
            <button type="button" onClick={clearFilters} aria-label={`Clear ${service} log filters`} className="inline-flex h-9 items-center justify-center gap-1.5 rounded-lg px-3 text-xs text-zinc-400 transition hover:bg-zinc-800 hover:text-white">
              <X className="size-3" /> Clear
            </button>
          )}
          <span data-testid="local-service-log-result-count" aria-live="polite" className="shrink-0 text-[10px] tabular-nums text-zinc-500">
            {filteredEntries.length} of {entries.length}
          </span>
        </div>
        <div className="max-h-[60vh] overflow-y-auto font-mono text-xs">
          {isLoading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-zinc-500"><Loader2 className="size-4 animate-spin" /> Loading journal…</div>
          ) : error ? (
            <p className="m-4 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-red-300">{error.message || 'Could not load service logs.'}</p>
          ) : entries.length === 0 ? (
            <p className="py-10 text-center text-zinc-500">No journal entries found for this service.</p>
          ) : filteredEntries.length === 0 ? (
            <p className="py-10 text-center text-zinc-500">No log entries match these filters.</p>
          ) : (
            <div className="divide-y divide-zinc-800/50">
              {filteredEntries.map((entry, index) => (
                <div key={`${entry.timestamp}-${entry.unit}-${index}`} className="grid gap-1 px-4 py-2.5 md:grid-cols-[170px_190px_1fr]">
                  <span className="text-[10px] text-zinc-600">{formatLogTimestamp(entry.timestamp)}</span>
                  <span className={`truncate text-[10px] ${entry.priority <= 3 ? 'text-red-400' : entry.priority === 4 ? 'text-amber-400' : 'text-blue-400'}`}>{entry.unit || 'system'}</span>
                  <span className="break-words leading-relaxed text-zinc-300">{entry.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ─── Log Viewer Modal ─────────────────────────────────────────────────────────

interface LogModalProps {
  onClose: () => void
}

function LogViewerModal({ onClose }: LogModalProps) {
  const { data, isLoading, error } = useQuery<{ lines: string[] }>({
    queryKey: ['dashboard', 'quicklog'],
    queryFn: () => api.get('/logs/read?path=/var/log/nginx/access.log&lines=20'),
    staleTime: 0,
  })

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm">
      <div className="w-full max-w-3xl bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
          <div className="flex items-center gap-2">
            <FileText className="w-4 h-4 text-zinc-400" />
            <span className="text-white font-medium text-sm">Nginx Access Log — last 20 lines</span>
          </div>
          <button
            onClick={onClose}
            className="text-zinc-500 hover:text-white transition-colors"
            aria-label="Close"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="p-4 max-h-96 overflow-y-auto font-mono text-xs">
          {isLoading && (
            <div className="flex items-center gap-2 text-zinc-500 py-4 justify-center">
              <Loader2 className="w-4 h-4 animate-spin" />
              <span>Loading logs…</span>
            </div>
          )}
          {error && (
            <p className="text-red-400 text-xs py-2">Failed to fetch logs.</p>
          )}
          {data?.lines?.map((line, i) => (
            <p key={i} className="text-zinc-300 leading-relaxed py-0.5 border-b border-zinc-800/50 last:border-0">
              {line}
            </p>
          ))}
          {data && data.lines?.length === 0 && (
            <p className="text-zinc-500 text-xs py-2 text-center">Log is empty.</p>
          )}
        </div>
      </div>
    </div>
  )
}

// ─── Nginx Test Inline Result ─────────────────────────────────────────────────

function NginxTestButton() {
  const [result, setResult] = useState<NginxTestResult | null>(null)

  const mutation = useMutation<NginxTestResult>({
    mutationFn: () => api.post<NginxTestResult>('/nginx/test'),
    onSuccess: (data) => setResult(data),
    onError: () => setResult({ ok: false, output: 'Request failed.' }),
  })

  return (
    <div className="flex flex-col gap-1.5">
      <button
        onClick={() => {
          setResult(null)
          mutation.mutate()
        }}
        disabled={mutation.isPending}
        className="flex flex-col items-center gap-2 p-4 rounded-xl bg-zinc-800/60 hover:bg-zinc-800 border border-zinc-700/50 hover:border-zinc-600 transition-all group"
      >
        {mutation.isPending ? (
          <Loader2 className="w-6 h-6 text-blue-400 animate-spin" />
        ) : result ? (
          result.ok ? (
            <CheckCircle2 className="w-6 h-6 text-green-400" />
          ) : (
            <XCircle className="w-6 h-6 text-red-400" />
          )
        ) : (
          <ShieldCheck className="w-6 h-6 text-blue-400 group-hover:scale-110 transition-transform" />
        )}
        <span className="text-xs font-medium text-zinc-300 group-hover:text-white transition-colors text-center">
          Nginx Test
        </span>
      </button>
      {result && (
        <p
          className={`text-xs px-2 py-1 rounded text-center truncate ${
            result.ok ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'
          }`}
          title={result.output}
        >
          {result.ok ? 'Config OK' : result.output.split('\n')[0]}
        </p>
      )}
    </div>
  )
}

// ─── Quick Actions Grid ───────────────────────────────────────────────────────

interface QuickActionProps {
  icon: React.ComponentType<{ className?: string }>
  label: string
  color: string
  onClick: () => void
}

function QuickActionCard({ icon: Icon, label, color, onClick }: QuickActionProps) {
  return (
    <button
      onClick={onClick}
      className="flex flex-col items-center gap-2 p-4 rounded-xl bg-zinc-800/60 hover:bg-zinc-800 border border-zinc-700/50 hover:border-zinc-600 transition-all group"
    >
      <Icon className={`w-6 h-6 ${color} group-hover:scale-110 transition-transform`} />
      <span className="text-xs font-medium text-zinc-300 group-hover:text-white transition-colors text-center">
        {label}
      </span>
    </button>
  )
}

// ─── SSL Expiry Warning ───────────────────────────────────────────────────────

export function SslExpiryBanner({ onManage }: { onManage: () => void }) {
  const certsQuery = useQuery<SSLCertificate[]>({
    queryKey: ['dashboard', 'ssl-expiry'],
    queryFn: () => api.get<SSLCertificate[]>('/ssl/certificates'),
    staleTime: 60_000,
  })
  const certs = certsQuery.data

  if (certsQuery.isLoading) return null
  if (certsQuery.isError) {
    return (
      <div className="flex flex-col gap-3 rounded-xl border border-red-500/30 bg-red-500/[0.07] px-4 py-3 text-sm text-red-300 sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 sm:mt-0" />
          <div>
            <p>SSL certificate inventory could not be checked.</p>
            <p className="mt-1 break-words font-mono text-xs text-red-400/70">{certsQuery.error.message}</p>
          </div>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={() => { void certsQuery.refetch() }} disabled={certsQuery.isFetching}>
          <RefreshCw className={certsQuery.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'} /> Retry
        </Button>
      </div>
    )
  }
  if (!certs) return null

  const expiring = certs.filter((c) => c.daysRemaining < 14)
  if (expiring.length === 0) return null

  const critical = expiring.filter((c) => c.daysRemaining < 3)
  const isCritical = critical.length > 0

  return (
    <div
      className={`flex flex-col gap-3 rounded-xl border px-4 py-3 text-sm sm:flex-row sm:items-center ${
        isCritical
          ? 'bg-red-500/10 border-red-500/40 text-red-300'
          : 'bg-amber-500/10 border-amber-500/40 text-amber-300'
      }`}
    >
      <div className="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
        <AlertTriangle className="mt-0.5 w-4 shrink-0 sm:mt-0" />
        <span>
          <strong>{expiring.length} SSL certificate{expiring.length > 1 ? 's' : ''}</strong> expiring soon:&nbsp;
          {expiring.map((c) => `${c.domain} (${c.daysRemaining}d)`).join(', ')}
        </span>
      </div>
      <button type="button" onClick={onManage} className="shrink-0 rounded-lg border border-current/30 px-3 py-1.5 text-xs font-semibold transition-colors hover:bg-white/10">
        Manage SSL
      </button>
    </div>
  )
}

// ─── Disk Space Warning ───────────────────────────────────────────────────────

interface DiskWarnProps {
  diskPct: number
  onManage: () => void
}

function DiskSpaceWarning({ diskPct, onManage }: DiskWarnProps) {
  if (diskPct <= 85) return null
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-300 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
        <HardDrive className="mt-0.5 w-4 shrink-0 sm:mt-0" />
        <span>
          <strong>Disk usage is at {diskPct.toFixed(0)}%</strong> — review measured cleanup targets or expand storage.
        </span>
      </div>
      <button type="button" onClick={onManage} className="shrink-0 rounded-lg border border-red-400/30 px-3 py-1.5 text-xs font-semibold transition-colors hover:bg-red-400/10">
        Review disk
      </button>
    </div>
  )
}

// ─── PM2 Status Summary ───────────────────────────────────────────────────────

export function PM2Summary() {
  const processesQuery = useQuery<PM2Process[]>({
    queryKey: ['dashboard', 'pm2'],
    queryFn: () => api.get<PM2Process[]>('/pm2/processes'),
    staleTime: 10_000,
    refetchInterval: 15_000,
  })
  const data = processesQuery.data

  const online = data?.filter((p) => p.status === 'online').length ?? 0
  const stopped = data?.filter((p) => p.status === 'stopped').length ?? 0
  const errored = data?.filter((p) => p.status === 'errored').length ?? 0

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-2">
        <CardTitle className="text-white text-sm flex items-center gap-2">
          <Activity className="w-4 h-4 text-green-400" />
          PM2 Processes
        </CardTitle>
      </CardHeader>
      <CardContent>
        {processesQuery.isLoading ? (
          <Skeleton className="h-8 w-full bg-zinc-800" />
        ) : processesQuery.isError ? (
          <div className="flex items-start justify-between gap-3 text-xs text-red-300">
            <div className="flex min-w-0 items-start gap-2">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <div>
                <p>PM2 process inventory could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{processesQuery.error.message}</p>
              </div>
            </div>
            <Button type="button" size="xs" variant="ghost" onClick={() => { void processesQuery.refetch() }} disabled={processesQuery.isFetching}>
              <RefreshCw className={processesQuery.isFetching ? 'size-3 animate-spin' : 'size-3'} /> Retry
            </Button>
          </div>
        ) : data ? (
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <div className="w-2 h-2 rounded-full bg-green-500 animate-pulse" />
              <span className="text-white font-bold text-lg">{online}</span>
              <span className="text-zinc-500 text-xs">online</span>
            </div>
            <div className="flex items-center gap-1.5">
              <div className="w-2 h-2 rounded-full bg-zinc-500" />
              <span className="text-white font-bold text-lg">{stopped}</span>
              <span className="text-zinc-500 text-xs">stopped</span>
            </div>
            {errored > 0 && (
              <div className="flex items-center gap-1.5">
                <div className="w-2 h-2 rounded-full bg-red-500" />
                <span className="text-red-400 font-bold text-lg">{errored}</span>
                <span className="text-zinc-500 text-xs">errored</span>
              </div>
            )}
          </div>
        ) : (
          <p className="text-zinc-500 text-xs">PM2 data unavailable</p>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Mail Queue Widget ────────────────────────────────────────────────────────

interface DashboardMailOverview {
  status?: { running: boolean; status: string }
  sources?: { status?: { available: boolean; error?: string } }
}

export function MailQueueWidget() {
  const queueQuery = useQuery<MailQueueItem[]>({
    queryKey: ['dashboard', 'mail-queue'],
    queryFn: () => api.get<MailQueueItem[]>('/mail/queue'),
    staleTime: 15_000,
    refetchInterval: 30_000,
  })
  const overviewQuery = useQuery<DashboardMailOverview>({
    queryKey: ['dashboard', 'mail-overview'],
    queryFn: () => api.get<DashboardMailOverview>('/mail/service/overview'),
    staleTime: 15_000,
    refetchInterval: 30_000,
  })
  const queueCount = queueQuery.data?.length ?? 0
  const serviceStatus = overviewQuery.data?.status
  const serviceSource = overviewQuery.data?.sources?.status
  const serviceKnown = serviceSource?.available ?? serviceStatus?.status !== 'unknown'

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-2">
        <CardTitle className="text-white text-sm flex items-center gap-2">
          <Inbox className="w-4 h-4 text-blue-400" />
          Mail Queue
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {queueQuery.isLoading ? (
          <Skeleton className="h-8 w-full bg-zinc-800" />
        ) : queueQuery.isError ? (
          <div className="flex items-start justify-between gap-3 text-xs text-red-300">
            <div className="flex min-w-0 items-start gap-2">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <div>
                <p>Mail queue could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{queueQuery.error.message}</p>
              </div>
            </div>
            <Button type="button" size="xs" variant="ghost" onClick={() => { void queueQuery.refetch() }} disabled={queueQuery.isFetching}>
              <RefreshCw className={`size-3 ${queueQuery.isFetching ? 'animate-spin' : ''}`} /> Retry queue
            </Button>
          </div>
        ) : queueQuery.data ? (
          <div className="flex items-center gap-3">
            <span className={`text-2xl font-bold ${queueCount > 0 ? 'text-amber-400' : 'text-white'}`}>
              {queueCount}
            </span>
            <span className="text-zinc-500 text-xs">message{queueCount !== 1 ? 's' : ''} queued</span>
          </div>
        ) : (
          <p className="text-zinc-500 text-xs">Mail data unavailable</p>
        )}

        <div className="border-t border-zinc-800/70 pt-2">
          {overviewQuery.isLoading ? (
            <span className="flex items-center gap-2 text-xs text-zinc-500"><Loader2 className="size-3 animate-spin" /> Checking mail service status…</span>
          ) : overviewQuery.isError ? (
            <div className="flex items-start justify-between gap-3 text-xs text-amber-300">
              <div className="min-w-0">
                <p>Mail service status could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-[11px] text-amber-300/60">{overviewQuery.error.message}</p>
              </div>
              <Button type="button" size="xs" variant="ghost" onClick={() => { void overviewQuery.refetch() }} disabled={overviewQuery.isFetching}>
                <RefreshCw className={`size-3 ${overviewQuery.isFetching ? 'animate-spin' : ''}`} /> Retry status
              </Button>
            </div>
          ) : !serviceKnown ? (
            <div className="flex items-start gap-2 text-xs text-amber-300">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <div>
                <p>Mail service status is unavailable.</p>
                <p className="mt-1 break-words font-mono text-[11px] text-amber-300/60">{serviceSource?.error ?? 'The service overview did not return an observable status.'}</p>
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-xs text-zinc-500">
              <div className={`size-2 rounded-full ${serviceStatus?.running ? 'bg-green-500 animate-pulse' : 'bg-zinc-600'}`} />
              <span>Mail service: {serviceStatus?.status ?? 'unknown'}</span>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

// ─── Recent Activity ──────────────────────────────────────────────────────────

export function RecentActivity() {
  const navigate = useNavigate()
  const auditQuery = useQuery<{ data: AuditLog[] }>({
    queryKey: ['dashboard', 'audit-recent'],
    queryFn: () => api.get<{ data: AuditLog[] }>('/audit?limit=5'),
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
  const auditData = auditQuery.data?.data ?? []

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <Users className="w-4 h-4 text-purple-400" />
            Recent Activity
          </CardTitle>
          <button
            onClick={() => navigate('/audit')}
            className="flex items-center gap-1 text-xs text-zinc-500 hover:text-zinc-300 transition-colors"
          >
            View All <ExternalLink className="w-3 h-3" />
          </button>
        </div>
      </CardHeader>
      <CardContent className="space-y-1">
        {auditQuery.isLoading
          ? Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
            ))
          : auditQuery.isError
          ? (
            <div className="flex flex-col items-center gap-2 py-5 text-center text-red-300">
              <AlertTriangle className="size-4" />
              <p className="text-sm">Recent activity could not be loaded.</p>
              <p className="break-words font-mono text-[11px] text-red-400/70">{auditQuery.error.message}</p>
              <Button type="button" size="xs" variant="ghost" onClick={() => { void auditQuery.refetch() }} disabled={auditQuery.isFetching}>
                <RefreshCw className={auditQuery.isFetching ? 'size-3 animate-spin' : 'size-3'} /> Retry
              </Button>
            </div>
          )
          : auditData && auditData.length > 0
          ? auditData.map((entry) => (
              <div
                key={entry.id}
                className="flex items-center gap-3 py-2 px-3 rounded-lg hover:bg-zinc-800/50 transition-colors"
              >
                <div
                  className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                    entry.action?.includes('delete') ? 'bg-red-500' : 'bg-green-500'
                  }`}
                />
                <div className="flex-1 min-w-0">
                  <p className="text-white text-xs font-medium truncate">
                    <span className="text-zinc-400">{entry.userName ?? entry.user ?? ""}</span>
                    {' · '}
                    <span>{entry.action}</span>
                    {entry.resource && (
                      <span className="text-zinc-500"> on {entry.resource}</span>
                    )}
                  </p>
                </div>
                <span className="text-zinc-600 text-xs shrink-0">{timeAgo(entry.createdAt ?? entry.timestamp ?? "")}</span>
              </div>
            ))
          : (
            <div className="flex items-center gap-2 py-4 text-zinc-500 justify-center">
              <RefreshCw className="w-4 h-4" />
              <span className="text-sm">No recent activity</span>
            </div>
          )}
      </CardContent>
    </Card>
  )
}

export function UptimeSummaryWidget() {
  const navigate = useNavigate()
  const summaryQuery = useQuery<UptimeSummary>({
    queryKey: ['uptime-summary'],
    queryFn: () => api.get('/uptime/monitors/summary'),
    refetchInterval: 30_000,
  })
  const summary = summaryQuery.data
  const destination = summary?.down ? '/uptime?status=down' : '/uptime'

  return (
    <Card
      role={summaryQuery.isError ? undefined : 'link'}
      tabIndex={summaryQuery.isError ? undefined : 0}
      aria-label={summaryQuery.isError ? undefined : summary?.down ? 'Open down uptime monitors' : 'Open uptime monitors'}
      className={summaryQuery.isError ? undefined : 'cursor-pointer transition-colors hover:border-blue-500/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/60'}
      onClick={summaryQuery.isError ? undefined : () => navigate(destination)}
      onKeyDown={(event) => {
        if (summaryQuery.isError) return
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          navigate(destination)
        }
      }}
    >
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-sm font-medium">Uptime Monitors</CardTitle>
        <Radar className="h-4 w-4 text-muted-foreground" />
      </CardHeader>
      <CardContent>
        {summaryQuery.isLoading ? (
          <Skeleton className="h-10 w-24" />
        ) : summaryQuery.isError ? (
          <div className="flex items-start justify-between gap-3 text-xs text-red-300">
            <div className="flex min-w-0 items-start gap-2">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <div>
                <p>Uptime summary could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{summaryQuery.error.message}</p>
              </div>
            </div>
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={summaryQuery.isFetching}
              onClick={(event) => {
                event.stopPropagation()
                void summaryQuery.refetch()
              }}
            >
              <RefreshCw className={summaryQuery.isFetching ? 'size-3 animate-spin' : 'size-3'} /> Retry
            </Button>
          </div>
        ) : summary ? (
          <>
            <div className="flex flex-wrap items-center gap-2 text-xs font-semibold">
              <span className="rounded-full bg-green-500/10 px-2.5 py-1 text-green-400">{summary.up} Up</span>
              {summary.down > 0 && <span className="rounded-full bg-red-500/10 px-2.5 py-1 text-red-400">{summary.down} Down</span>}
              {summary.paused > 0 && <span className="rounded-full bg-yellow-500/10 px-2.5 py-1 text-yellow-400">{summary.paused} Paused</span>}
              {summary.maintenance > 0 && <span className="rounded-full bg-blue-500/10 px-2.5 py-1 text-blue-400">{summary.maintenance} Maintenance</span>}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {summary.down > 0
                ? `${summary.down} monitor${summary.down > 1 ? 's' : ''} need attention`
                : `${summary.up} active monitor${summary.up === 1 ? '' : 's'} operational`}
            </p>
          </>
        ) : (
          <p className="text-xs text-zinc-500">Uptime summary is not available.</p>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Main Dashboard ───────────────────────────────────────────────────────────

export default function Dashboard() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: currentUser } = useCurrentUser()
	const canManage = currentUser?.role === 'admin'
  const { data: stats, isLoading: statsLoading, isError: statsError, error: statsFailure, refetch: refetchStats, isFetching: statsFetching } = useSystemStats()
  const { data: services, isLoading: servicesLoading, isError: servicesError, error: servicesFailure, refetch: refetchServices, isFetching: servicesFetching } = useServiceStatuses()
	const actionStatus = useHostActionStatus('local', canManage)
	const actionStatusUnavailable = canManage && (actionStatus.isLoading || actionStatus.isError)
  const [logModalOpen, setLogModalOpen] = useState(false)
  const [serviceLogOpen, setServiceLogOpen] = useState<string | null>(null)

  // Sparkline data from last 1 hour of metrics
  const { data: metricsRes, isError: metricsError, error: metricsFailure, refetch: refetchMetrics, isFetching: metricsFetching } = useMetricsHistory('1h')
  const sparklines = useMemo(() => {
    if (!metricsRes?.data || metricsRes.resolution !== 'raw') return { cpu: [], mem: [], disk: [] }
    const raw = metricsRes.data as MetricRaw[]
    return {
      cpu: raw.map((d) => ({ v: d.cpu_percent })),
      mem: raw.map((d) => ({ v: d.memory_percent })),
      disk: raw.map((d) => ({ v: d.disk_root_percent })),
    }
  }, [metricsRes])

  const diskPct = stats?.disk[0]?.percentage ?? 0
  const swapRequired = (stats?.memory.swapUsed ?? 0) + 512 * 1024 * 1024
  const controlMemory = stats ? {
    total: stats.memory.swapTotal,
    used: stats.memory.swapUsed,
    available: stats.memory.available,
  } : undefined
  const swapReason = !stats
    ? 'Loading swap state…'
    : stats.memory.swapTotal === 0
      ? 'No active swap is configured'
      : stats.memory.swapUsed === 0
        ? 'Swap is already empty'
        : stats.memory.available < swapRequired
          ? `Needs ${formatBytes(swapRequired)} available; ${formatBytes(stats.memory.available)} is available`
          : undefined
  const resourceAction = useMutation<{ message: string }, Error, 'memory-optimize' | 'swap-reset'>({
    mutationFn: (action) => api.post(hostActionEndpoint('local', action)),
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: ['stats', 'system'] })
    },
    onError: (error) => toast.error(error.message || 'System action failed'),
		onSettled: () => queryClient.invalidateQueries({ queryKey: hostActionStatusKey('local') }),
  })
  const serviceControl = useMutation<{ message: string }, Error, { service: string; action: ServiceControlAction }>({
    mutationFn: (payload) => api.post('/system/actions/service', payload),
    onSuccess: async (result) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: ['stats', 'services'] })
    },
    onError: (error) => toast.error(error.message || 'Service action failed'),
  })

  const runResourceAction = (action: 'memory-optimize' | 'swap-reset') => {
    const prompt = hostActionConfirmation(action, 'HServer', controlMemory, swapRequired)
    if (window.confirm(prompt)) resourceAction.mutate(action)
  }

  const controlService = (service: string, action: ServiceControlAction) => {
    const prompt = action === 'stop'
      ? `Stop ${service} on HServer? Dependent sites or workers may become unavailable.`
      : action === 'restart'
        ? `Restart ${service} on HServer now? A short interruption may occur.`
        : undefined
    if (!prompt || window.confirm(prompt)) serviceControl.mutate({ service, action })
  }

  const handleMailClick = useCallback(() => {
    navigate('/mail', { state: { tab: 'accounts' } })
  }, [navigate])

  return (
    <div className="space-y-5">
      {/* ── Modals ─────────────────────────────────────────────────────────── */}
      {logModalOpen && <LogViewerModal onClose={() => setLogModalOpen(false)} />}
      {serviceLogOpen && <ServiceLogModal service={serviceLogOpen} onClose={() => setServiceLogOpen(null)} />}

      {/* ── Header ─────────────────────────────────────────────────────────── */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-white text-xl font-bold">System Overview</h2>
          <p className="text-zinc-500 text-sm mt-0.5">Real-time server metrics</p>
        </div>
        {stats && (
          <div className="hidden sm:flex items-center gap-2 text-zinc-500 text-xs">
            <Clock className="w-3.5 h-3.5" />
            <span>Uptime: {formatUptime(stats.uptime)}</span>
          </div>
        )}
      </div>

      {/* ── Warning Banners ─────────────────────────────────────────────────── */}
      <SslExpiryBanner onManage={() => navigate('/ssl')} />
      {stats && <DiskSpaceWarning diskPct={diskPct} onManage={() => navigate('/disk')} />}
			{metricsError && (
				<div className="flex items-center justify-between gap-3 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-300">
					<div>
						<p>Historical dashboard trends could not be loaded. Live gauges remain available.</p>
						<p className="mt-1 break-words font-mono text-[11px] text-amber-300/60">{metricsFailure.message}</p>
					</div>
					<Button type="button" variant="ghost" size="xs" disabled={metricsFetching} onClick={() => { void refetchMetrics() }}>
						<RefreshCw className={`size-3 ${metricsFetching ? 'animate-spin' : ''}`} /> Retry
					</Button>
				</div>
			)}
			{canManage && actionStatusUnavailable && (
				<div className="flex items-center justify-between gap-3 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-300">
					<span>{actionStatus.isError ? (actionStatus.isFetching ? 'Retrying active host maintenance status…' : 'Could not verify active host maintenance. Resource actions are paused.') : 'Checking active host maintenance before enabling resource actions…'}</span>
					{actionStatus.isError && <Button type="button" variant="ghost" size="xs" disabled={actionStatus.isFetching} onClick={() => actionStatus.refetch()}><RefreshCw className={`size-3 ${actionStatus.isFetching ? 'animate-spin' : ''}`} /> Retry</Button>}
				</div>
			)}

      {/* ── Resource Gauges ─────────────────────────────────────────────────── */}
      {statsLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <Card key={i} className="bg-zinc-900 border-zinc-800">
              <CardContent className="pt-6">
                <Skeleton className="h-20 w-full bg-zinc-800" />
              </CardContent>
            </Card>
          ))}
        </div>
      ) : statsError ? (
        <Card className="border-red-500/25 bg-red-500/[0.06] sm:col-span-2 xl:col-span-4">
          <CardContent className="flex flex-col items-start justify-between gap-4 p-5 sm:flex-row sm:items-center">
            <div className="flex min-w-0 items-start gap-3">
              <AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-400" />
              <div>
                <p className="text-sm font-medium text-red-300">Live system statistics could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-xs text-red-400/70">{statsFailure.message}</p>
                <p className="mt-1 text-xs text-zinc-500">CPU, memory, swap, disk gauges, and resource actions remain unavailable until live host state is verified.</p>
              </div>
            </div>
            <Button type="button" variant="outline" size="sm" disabled={statsFetching} onClick={() => { void refetchStats() }} className="border-red-500/30 text-red-200">
              <RefreshCw className={`mr-2 size-3.5 ${statsFetching ? 'animate-spin' : ''}`} /> Retry statistics
            </Button>
          </CardContent>
        </Card>
      ) : stats ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <Gauge
            label="CPU"
            value={stats.cpu.usage}
            subtitle={`Load: ${stats.load[0].toFixed(2)}`}
            icon={Cpu}
            color="blue"
            sparklineData={sparklines.cpu}
            sparklineColor="#3b82f6"
            actionLabel="View processes"
            onAction={() => navigate('/monitoring?focus=processes')}
          />
          <Gauge
            label="Memory"
            value={stats.memory.percentage}
            subtitle={`${formatBytes(stats.memory.used)} / ${formatBytes(stats.memory.total)}`}
            icon={MemoryStick}
            color="green"
            sparklineData={sparklines.mem}
            sparklineColor="#22c55e"
            actionLabel="Optimize RAM"
				actionTitle={!canManage ? 'Admin role is required' : actionStatusUnavailable ? 'Could not verify active host maintenance' : actionStatus.data?.running ? `${hostActionLabel(actionStatus.data.action)} is already running` : undefined}
				actionPending={(resourceAction.isPending && resourceAction.variables === 'memory-optimize') || (actionStatus.data?.running === true && actionStatus.data.action === 'memory-optimize')}
				actionDisabled={!canManage || actionStatusUnavailable || resourceAction.isPending || actionStatus.data?.running === true}
            onAction={() => runResourceAction('memory-optimize')}
          />
          <Gauge
            label="Swap"
            value={stats.memory.swapPercentage}
            subtitle={`${formatBytes(stats.memory.swapUsed)} / ${formatBytes(stats.memory.swapTotal)}`}
            icon={RotateCcw}
            color="amber"
            actionLabel="Reset swap"
				actionTitle={!canManage ? 'Admin role is required' : actionStatusUnavailable ? 'Could not verify active host maintenance' : actionStatus.data?.running ? `${hostActionLabel(actionStatus.data.action)} is already running` : swapReason}
				actionPending={(resourceAction.isPending && resourceAction.variables === 'swap-reset') || (actionStatus.data?.running === true && actionStatus.data.action === 'swap-reset')}
				actionDisabled={!canManage || actionStatusUnavailable || resourceAction.isPending || actionStatus.data?.running === true || !!swapReason}
            onAction={() => runResourceAction('swap-reset')}
          />
          <Gauge
            label="Disk"
            value={diskPct}
            subtitle={`${formatBytes(stats.disk[0]?.used ?? 0)} / ${formatBytes(stats.disk[0]?.total ?? 0)}`}
            icon={HardDrive}
            color="blue"
            sparklineData={sparklines.disk}
            sparklineColor="#a855f7"
            actionLabel="Manage disk"
            onAction={() => navigate('/disk')}
          />
        </div>
      ) : null}

      {/* ── Load Averages ────────────────────────────────────────────────────── */}
      {stats && (
        <div className="grid grid-cols-3 gap-3">
          {[
            { label: '1 min', value: stats.load[0] },
            { label: '5 min', value: stats.load[1] },
            { label: '15 min', value: stats.load[2] },
          ].map((item) => (
            <Card key={item.label} className="bg-zinc-900 border-zinc-800">
              <CardContent className="py-4 flex flex-col items-center">
                <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide">{item.label} avg</p>
                <p className="text-white text-xl font-bold mt-1">{item.value.toFixed(2)}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* ── Service Status ───────────────────────────────────────────────────── */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <Activity className="w-4 h-4 text-blue-400" />
            Service Status
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {servicesLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-11 w-full bg-zinc-800" />
            ))
          ) : servicesError ? (
            <div className="flex flex-col items-start justify-between gap-4 rounded-xl border border-red-500/25 bg-red-500/[0.06] p-4 sm:flex-row sm:items-center">
              <div className="flex min-w-0 items-start gap-3">
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-red-400" />
                <div>
                  <p className="text-sm font-medium text-red-300">Service inventory could not be loaded. Service controls are paused.</p>
                  <p className="mt-1 break-words font-mono text-xs text-red-400/70">{servicesFailure.message}</p>
                </div>
              </div>
              <Button type="button" variant="outline" size="sm" disabled={servicesFetching} onClick={() => { void refetchServices() }} className="border-red-500/30 text-red-200">
                <RefreshCw className={`mr-2 size-3.5 ${servicesFetching ? 'animate-spin' : ''}`} /> Retry services
              </Button>
            </div>
          ) : services && services.length > 0 ? (
            services.map((service) => {
              const pending = serviceControl.isPending && serviceControl.variables?.service === service.name
                ? serviceControl.variables.action
                : undefined
              return (
                <ServiceBadge
                  key={service.name}
                  service={service}
                  canManage={currentUser?.role === 'admin'}
                  controlsDisabled={serviceControl.isPending}
                  pendingAction={pending}
                  onOpenLogs={() => setServiceLogOpen(service.name)}
                  onControl={(action) => controlService(service.name, action)}
                />
              )
            })
          ) : (
            <div className="flex items-center gap-2 py-4 text-zinc-500">
              <RefreshCw className="w-4 h-4" />
              <span className="text-sm">No monitored services were returned.</span>
            </div>
          )}
        </CardContent>
      </Card>

      {/* ── Quick Actions ────────────────────────────────────────────────────── */}
      <div>
        <h3 className="text-zinc-400 text-xs font-semibold uppercase tracking-widest mb-3">Quick Actions</h3>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
          <QuickActionCard
            icon={Globe}
            label="Add Domain"
            color="text-blue-400"
            onClick={() => navigate('/domains')}
          />
          <NginxTestButton />
          <QuickActionCard
            icon={Archive}
            label="Create Backup"
            color="text-amber-400"
            onClick={() => navigate('/backups')}
          />
          <QuickActionCard
            icon={Lock}
            label="Issue SSL"
            color="text-green-400"
            onClick={() => navigate('/ssl')}
          />
          <QuickActionCard
            icon={Mail}
            label="Add Mail Account"
            color="text-purple-400"
            onClick={handleMailClick}
          />
          <QuickActionCard
            icon={FileText}
            label="View Logs"
            color="text-zinc-400"
            onClick={() => setLogModalOpen(true)}
          />
        </div>
      </div>

      {/* ── Summary Widgets Row ──────────────────────────────────────────────── */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        <PM2Summary />
        <MailQueueWidget />
        <UptimeSummaryWidget />
      </div>

      {/* ── Recent Activity ──────────────────────────────────────────────────── */}
      <RecentActivity />
    </div>
  )
}

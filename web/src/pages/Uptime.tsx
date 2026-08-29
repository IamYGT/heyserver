import { Fragment, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import {
  Plus,
  Trash2,
  Pencil,
  Loader2,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  PauseCircle,
  Clock,
  Globe,
  Server,
  Wifi,
  Activity,
  ExternalLink,
  ChevronDown,
  ChevronRight,
  RefreshCw,
  Play,
  Pause,
  Settings2,
  BarChart2,
  FileText,
  Bell,
  Layers,
  FlaskConical,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as RechartsTooltip,
  ResponsiveContainer,
} from 'recharts'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type {
  UptimeMonitor,
  UptimeHeartbeat,
  UptimeIncident,
  UptimeStatusPage,
  UptimeStats,
} from '@/lib/types'

// ─── Local Types ───────────────────────────────────────────────────────────────

type MonitorType = 'http' | 'tcp' | 'dns' | 'ping'
type HTTPMethod = 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS'
type StatusFilter = 'all' | 'up' | 'down' | 'paused'
type PeriodOption = '24h' | '7d' | '30d'

interface NotificationChannel {
  id: number
  type: string
  name: string
  enabled: boolean
  config: string
}

interface MonitorFormState {
  name: string
  type: MonitorType
  url: string
  hostname: string
  port: string
  method: HTTPMethod
  accepted_statuscodes: string
  keyword: string
  keyword_invert: boolean
  req_headers: string
  req_body: string
  tls_check: boolean
  tls_expiry_warn_days: string
  dns_record_type: string
  dns_expected_value: string
  interval_secs: string
  timeout_secs: string
  retries: string
  retry_interval: string
  max_redirects: string
  description: string
  alert_channel_ids: number[]
  alert_reminder_mins: string
}

interface MonitorPayload {
  name: string
  type: MonitorType
  url?: string
  hostname?: string
  port?: number
  method?: HTTPMethod
  accepted_statuscodes?: string
  keyword?: string
  keyword_invert?: boolean
  req_headers?: string
  req_body?: string
  tls_check?: boolean
  tls_expiry_warn_days?: number
  interval_secs: number
  timeout_secs: number
  retries: number
  retry_interval: number
  max_redirects?: number
  description: string
  alert_channel_ids: number[]
  alert_reminder_mins: number
}

interface StatusPageFormState {
  slug: string
  title: string
  description: string
  logo_url: string
  theme: 'auto' | 'light' | 'dark'
  history_days: string
  is_public: boolean
  monitor_ids: number[]
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

const inputClass =
  'w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors'

const labelClass = 'text-zinc-400 text-xs font-medium'

function UptimeLoadError({ title, error, retry, retrying = false }: { title: string; error: Error; retry: () => void; retrying?: boolean }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.05] px-4 py-10 text-center">
      <AlertTriangle className="size-6 text-red-400" />
      <div>
        <p className="text-sm text-red-300">{title}</p>
        <p className="mt-1 break-words font-mono text-xs text-red-400/70">{error.message}</p>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={retry} disabled={retrying}>
        <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} /> Retry
      </Button>
    </div>
  )
}

function defaultMonitorForm(): MonitorFormState {
  return {
    name: '',
    type: 'http',
    url: '',
    hostname: '',
    port: '',
    method: 'GET',
    accepted_statuscodes: '200-299',
    keyword: '',
    keyword_invert: false,
    req_headers: '',
    req_body: '',
    tls_check: false,
    tls_expiry_warn_days: '14',
    dns_record_type: 'A',
    dns_expected_value: '',
    interval_secs: '60',
    timeout_secs: '30',
    retries: '1',
    retry_interval: '30',
    max_redirects: '5',
    description: '',
    alert_channel_ids: [],
    alert_reminder_mins: '0',
  }
}

function monitorToForm(m: UptimeMonitor): MonitorFormState {
  return {
    name: m.name,
    type: m.type,
    url: m.url ?? '',
    hostname: m.hostname ?? '',
    port: m.port != null ? String(m.port) : '',
    method: m.method as HTTPMethod,
    accepted_statuscodes: m.accepted_statuscodes,
    keyword: m.keyword ?? '',
    keyword_invert: m.keyword_invert,
    req_headers: m.req_headers ?? '',
    req_body: m.req_body ?? '',
    tls_check: m.tls_check,
    tls_expiry_warn_days: String(m.tls_expiry_warn_days),
    dns_record_type: m.type === 'dns' ? (m.keyword || 'A') : 'A',
    dns_expected_value: m.type === 'dns' ? (m.req_body ?? '') : '',
    interval_secs: String(m.interval_secs),
    timeout_secs: String(m.timeout_secs),
    retries: String(m.retries),
    retry_interval: String(m.retry_interval),
    max_redirects: String(m.max_redirects),
    description: m.description ?? '',
    alert_channel_ids: m.alert_channel_ids ?? [],
    alert_reminder_mins: String(m.alert_reminder_mins),
  }
}

function formToPayload(form: MonitorFormState): MonitorPayload {
  const base: MonitorPayload = {
    name: form.name,
    type: form.type,
    interval_secs: parseInt(form.interval_secs) || 60,
    timeout_secs: parseInt(form.timeout_secs) || 30,
    retries: parseInt(form.retries) || 1,
    retry_interval: parseInt(form.retry_interval) || 30,
    description: form.description,
    alert_channel_ids: form.alert_channel_ids,
    alert_reminder_mins: parseInt(form.alert_reminder_mins) || 0,
  }
  if (form.type === 'http') {
    base.url = form.url
    base.method = form.method
    base.accepted_statuscodes = form.accepted_statuscodes
    base.keyword = form.keyword
    base.keyword_invert = form.keyword_invert
    base.req_headers = form.req_headers
    base.req_body = form.req_body
    base.tls_check = form.tls_check
    base.tls_expiry_warn_days = parseInt(form.tls_expiry_warn_days) || 14
    base.max_redirects = parseInt(form.max_redirects) || 5
  } else {
    base.hostname = form.hostname
    if (form.type === 'tcp') {
      base.port = parseInt(form.port) || 80
    } else if (form.type === 'dns') {
      base.keyword = form.dns_record_type
      base.req_body = form.dns_expected_value
    }
  }
  return base
}

function formatDuration(secs?: number): string {
  if (!secs) return '—'
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`
}

function formatTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const now = new Date()
  const diff = Math.floor((now.getTime() - d.getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return d.toLocaleDateString()
}

function formatUptime(pct?: number): string {
  if (pct == null) return '—'
  if (pct === 100) return '100%'
  if (pct === 0) return '0%'
  return `${pct.toFixed(1)}%`
}

// ─── Status indicators ─────────────────────────────────────────────────────────

function MonitorStatusDot({ status, paused }: { status?: number; paused?: boolean }) {
  if (paused) return <span className="inline-block w-2 h-2 rounded-full bg-yellow-400" title="Paused" />
  if (status == null) return <span className="inline-block w-2 h-2 rounded-full bg-zinc-500" title="Pending" />
  if (status === 1) return <span className="inline-block w-2 h-2 rounded-full bg-green-400" title="Up" />
  if (status === 0) return <span className="inline-block w-2 h-2 rounded-full bg-red-400" title="Down" />
  return <span className="inline-block w-2 h-2 rounded-full bg-zinc-500" title="Unknown" />
}

function MonitorTypeBadge({ type }: { type: MonitorType }) {
  const styles: Record<MonitorType, string> = {
    http: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
    tcp: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
    dns: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
    ping: 'bg-teal-500/10 text-teal-400 border-teal-500/20',
  }
  const icons: Record<MonitorType, React.ReactNode> = {
    http: <Globe className="w-3 h-3" />,
    tcp: <Server className="w-3 h-3" />,
    dns: <FileText className="w-3 h-3" />,
    ping: <Wifi className="w-3 h-3" />,
  }
  return (
    <Badge className={cn('text-xs border uppercase flex items-center gap-1 w-fit', styles[type])}>
      {icons[type]}
      {type}
    </Badge>
  )
}

// ─── 90-day uptime bar ─────────────────────────────────────────────────────────

function UptimeBar({ heartbeats }: { heartbeats: UptimeHeartbeat[] }) {
  // Group by day (last 90 days)
  const days: { date: string; status: number }[] = []
  const now = new Date()
  for (let i = 89; i >= 0; i--) {
    const d = new Date(now)
    d.setDate(d.getDate() - i)
    const key = d.toISOString().split('T')[0]
    const dayBeats = heartbeats.filter((h) => h.created_at.startsWith(key))
    const upCount = dayBeats.filter((h) => h.status === 1).length
    const total = dayBeats.length
    let status = -1 // no data
    if (total > 0) status = upCount / total >= 0.9 ? 1 : 0
    days.push({ date: key, status })
  }

  return (
    <div className="flex gap-0.5 items-end h-8">
      {days.map((d) => (
        <div
          key={d.date}
          title={`${d.date}: ${d.status === 1 ? 'Up' : d.status === 0 ? 'Down' : 'No data'}`}
          className={cn(
            'flex-1 rounded-sm h-full cursor-default transition-opacity hover:opacity-75',
            d.status === 1 && 'bg-green-500',
            d.status === 0 && 'bg-red-500',
            d.status === -1 && 'bg-zinc-700',
          )}
        />
      ))}
    </div>
  )
}

// ─── Monitor Detail Panel ──────────────────────────────────────────────────────

export function MonitorDetail({ monitor, incidents }: { monitor: UptimeMonitor; incidents: UptimeIncident[] }) {
  const [period, setPeriod] = useState<PeriodOption>('24h')

  const periodHours: Record<PeriodOption, number> = { '24h': 24, '7d': 168, '30d': 720 }

  const heartbeatsQuery = useQuery<UptimeHeartbeat[]>({
    queryKey: ['uptime', 'heartbeats', monitor.id, period],
    queryFn: () =>
      api.get<UptimeHeartbeat[]>(`/uptime/monitors/${monitor.id}/heartbeats?hours=${periodHours[period]}`),
  })
  const heartbeats = heartbeatsQuery.data ?? []

  const statsQuery = useQuery<UptimeStats>({
    queryKey: ['uptime', 'stats', monitor.id],
    queryFn: () => api.get<UptimeStats>(`/uptime/monitors/${monitor.id}/uptime?period=90d`),
  })
  const stats = statsQuery.data

  // Build chart data from heartbeats
  const chartData = heartbeats.slice(-100).map((h) => ({
    time: new Date(h.created_at).toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' }),
    ms: h.ping_ms ?? 0,
    status: h.status,
  }))

  // All 90-day heartbeats for bar
  const archiveQuery = useQuery<UptimeHeartbeat[]>({
    queryKey: ['uptime', 'heartbeats', monitor.id, '2160h'],
    queryFn: () =>
      api.get<UptimeHeartbeat[]>(`/uptime/monitors/${monitor.id}/heartbeats?hours=2160`),
  })
  const allHeartbeats = archiveQuery.data ?? []

  return (
    <div className="px-4 pb-4 pt-2 bg-zinc-900/50 border-t border-zinc-800 space-y-4">
      {/* 90-day uptime bar */}
      {archiveQuery.isLoading ? (
        <Skeleton className="h-16 w-full bg-zinc-800" />
      ) : archiveQuery.isError ? (
        <UptimeLoadError title="90-day heartbeat history could not be loaded." error={archiveQuery.error} retry={() => { void archiveQuery.refetch() }} retrying={archiveQuery.isFetching} />
      ) : <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <span className="text-xs text-zinc-500">90-day availability</span>
          <span className="text-xs text-zinc-400">{formatUptime(stats?.uptime_90d)}</span>
        </div>
        <UptimeBar heartbeats={allHeartbeats} />
        <div className="flex justify-between text-xs text-zinc-600">
          <span>90 days ago</span>
          <span>Today</span>
        </div>
      </div>}

      {/* Stats cards */}
      {statsQuery.isLoading ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {[...Array(4)].map((_, index) => <Skeleton key={index} className="h-16 bg-zinc-800" />)}
        </div>
      ) : statsQuery.isError ? (
        <UptimeLoadError title="Uptime statistics could not be loaded." error={statsQuery.error} retry={() => { void statsQuery.refetch() }} retrying={statsQuery.isFetching} />
      ) : <><div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {[
          { label: 'Uptime 24h', value: formatUptime(stats?.uptime_24h) },
          { label: 'Uptime 7d', value: formatUptime(stats?.uptime_7d) },
          { label: 'Uptime 30d', value: formatUptime(stats?.uptime_30d) },
          { label: 'Uptime 90d', value: formatUptime(stats?.uptime_90d) },
        ].map((s) => (
          <div key={s.label} className="bg-zinc-800/60 rounded-lg p-3 text-center">
            <div className="text-lg font-semibold text-white">{s.value}</div>
            <div className="text-xs text-zinc-500">{s.label}</div>
          </div>
        ))}
      </div>

      <div className="grid grid-cols-3 gap-3">
        {[
          { label: 'Avg response', value: stats?.avg_ping_ms != null ? `${Math.round(stats.avg_ping_ms)}ms` : '—' },
          { label: 'P95 response', value: stats?.p95_ping_ms != null ? `${Math.round(stats.p95_ping_ms)}ms` : '—' },
          { label: 'P99 response', value: stats?.p99_ping_ms != null ? `${Math.round(stats.p99_ping_ms)}ms` : '—' },
        ].map((s) => (
          <div key={s.label} className="bg-zinc-800/60 rounded-lg p-3 text-center">
            <div className="text-base font-semibold text-white">{s.value}</div>
            <div className="text-xs text-zinc-500">{s.label}</div>
          </div>
        ))}
      </div></>}

      {/* Response time chart */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <span className="text-xs text-zinc-400 font-medium">Response Time</span>
          <div className="flex gap-1">
            {(['24h', '7d', '30d'] as PeriodOption[]).map((p) => (
              <button
                key={p}
                onClick={() => setPeriod(p)}
                className={cn(
                  'px-2 py-0.5 text-xs rounded transition-colors',
                  period === p
                    ? 'bg-blue-600 text-white'
                    : 'bg-zinc-800 text-zinc-400 hover:text-white',
                )}
              >
                {p}
              </button>
            ))}
          </div>
        </div>
        {heartbeatsQuery.isLoading ? (
          <Skeleton className="h-32 w-full bg-zinc-800" />
        ) : heartbeatsQuery.isError ? (
          <UptimeLoadError title="Response-time history could not be loaded." error={heartbeatsQuery.error} retry={() => { void heartbeatsQuery.refetch() }} retrying={heartbeatsQuery.isFetching} />
        ) : chartData.length === 0 ? (
          <div className="h-32 flex items-center justify-center text-zinc-600 text-sm">
            No data yet
          </div>
        ) : (
          <ResponsiveContainer width="100%" height={120}>
            <LineChart data={chartData} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="#27272a" />
              <XAxis
                dataKey="time"
                tick={{ fill: '#71717a', fontSize: 10 }}
                tickLine={false}
                interval="preserveStartEnd"
              />
              <YAxis tick={{ fill: '#71717a', fontSize: 10 }} tickLine={false} axisLine={false} />
              <RechartsTooltip
                contentStyle={{ background: '#18181b', border: '1px solid #3f3f46', borderRadius: 6, fontSize: 12 }}
                labelStyle={{ color: '#a1a1aa' }}
                itemStyle={{ color: '#60a5fa' }}
                formatter={(v) => [`${Number(v ?? 0)}ms`, 'Response']}
              />
              <Line
                type="monotone"
                dataKey="ms"
                stroke="#3b82f6"
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>

      {/* Recent incidents */}
      {incidents.length > 0 && (
        <div className="space-y-1.5">
          <span className="text-xs text-zinc-400 font-medium">Recent Incidents</span>
          <div className="space-y-1">
            {incidents.slice(0, 5).map((inc) => (
              <div
                key={inc.id}
                className="flex items-center justify-between bg-zinc-800/60 rounded px-3 py-2 text-xs"
              >
                <div className="flex items-center gap-2">
                  {inc.resolved_at ? (
                    <CheckCircle2 className="w-3.5 h-3.5 text-green-400 shrink-0" />
                  ) : (
                    <XCircle className="w-3.5 h-3.5 text-red-400 shrink-0" />
                  )}
                  <span className="text-zinc-300">{inc.cause ?? inc.type}</span>
                </div>
                <div className="flex items-center gap-3 text-zinc-500">
                  <span>{new Date(inc.started_at).toLocaleString()}</span>
                  <span>{formatDuration(inc.duration_secs)}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Monitor Dialog ────────────────────────────────────────────────────────────

interface MonitorDialogProps {
  open: boolean
  onClose: () => void
  onSave: (form: MonitorFormState) => void
  isPending: boolean
  initial?: UptimeMonitor | null
  channels: NotificationChannel[]
}

interface TestResult {
  ok: boolean
  ping_ms?: number
  error?: string
  status_code?: number
}

function MonitorDialog({ open, onClose, onSave, isPending, initial, channels }: MonitorDialogProps) {
  const [form, setForm] = useState<MonitorFormState>(() =>
    initial ? monitorToForm(initial) : defaultMonitorForm(),
  )
  const [testResult, setTestResult] = useState<TestResult | null>(null)

  const testMutation = useMutation({
    mutationFn: () => api.post<TestResult>('/uptime/monitors/test', formToPayload(form)),
    onSuccess: (res) => setTestResult(res),
    onError: (err: Error) => setTestResult({ ok: false, error: err.message ?? 'Test failed' }),
  })

  function handleTest() {
    if (form.type === 'http' && !form.url.trim()) {
      toast.error('URL is required to test')
      return
    }
    if ((form.type === 'tcp' || form.type === 'dns' || form.type === 'ping') && !form.hostname.trim()) {
      toast.error('Hostname is required to test')
      return
    }
    setTestResult(null)
    testMutation.mutate()
  }

  function field<K extends keyof MonitorFormState>(key: K, value: MonitorFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
    setTestResult(null)
  }

  function toggleChannel(id: number) {
    setForm((prev) => ({
      ...prev,
      alert_channel_ids: prev.alert_channel_ids.includes(id)
        ? prev.alert_channel_ids.filter((c) => c !== id)
        : [...prev.alert_channel_ids, id],
    }))
  }

  function handleSubmit() {
    if (!form.name.trim()) {
      toast.error('Monitor name is required')
      return
    }
    if (form.type === 'http' && !form.url.trim()) {
      toast.error('URL is required for HTTP monitors')
      return
    }
    if ((form.type === 'tcp' || form.type === 'dns' || form.type === 'ping') && !form.hostname.trim()) {
      toast.error('Hostname is required')
      return
    }
    onSave(form)
  }

  const typeButtons: { type: MonitorType; label: string; icon: React.ReactNode }[] = [
    { type: 'http', label: 'HTTP', icon: <Globe className="w-3.5 h-3.5" /> },
    { type: 'tcp', label: 'TCP', icon: <Server className="w-3.5 h-3.5" /> },
    { type: 'dns', label: 'DNS', icon: <FileText className="w-3.5 h-3.5" /> },
    { type: 'ping', label: 'Ping', icon: <Wifi className="w-3.5 h-3.5" /> },
  ]

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-4xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit Monitor' : 'Add Monitor'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-5 py-2">
          {/* Type selector */}
          <div className="space-y-2">
            <label className={labelClass}>Monitor Type</label>
            <div className="flex gap-2">
              {typeButtons.map((t) => (
                <button
                  key={t.type}
                  onClick={() => field('type', t.type)}
                  disabled={!!initial}
                  className={cn(
                    'flex-1 flex items-center justify-center gap-1.5 py-2 rounded-md text-sm font-medium border transition-colors',
                    form.type === t.type
                      ? 'bg-blue-600 border-blue-500 text-white'
                      : 'bg-zinc-800 border-zinc-700 text-zinc-400 hover:text-white hover:border-zinc-600',
                    initial && 'opacity-50 cursor-not-allowed',
                  )}
                >
                  {t.icon}
                  {t.label}
                </button>
              ))}
            </div>
          </div>

          {/* Name + description */}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Name *</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => field('name', e.target.value)}
                placeholder="My Website"
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Description</label>
              <input
                type="text"
                value={form.description}
                onChange={(e) => field('description', e.target.value)}
                placeholder="Optional description"
                className={inputClass}
              />
            </div>
          </div>

          {/* Type-specific fields */}
          {form.type === 'http' && (
            <>
              <div className="space-y-1.5">
                <label className={labelClass}>URL *</label>
                <input
                  type="url"
                  value={form.url}
                  onChange={(e) => field('url', e.target.value)}
                  placeholder="https://example.com"
                  className={inputClass}
                />
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>Method</label>
                  <select
                    value={form.method}
                    onChange={(e) => field('method', e.target.value as HTTPMethod)}
                    className={inputClass}
                  >
                    <option value="GET">GET</option>
                    <option value="POST">POST</option>
                    <option value="HEAD">HEAD</option>
                  </select>
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Accepted Codes</label>
                  <input
                    type="text"
                    value={form.accepted_statuscodes}
                    onChange={(e) => field('accepted_statuscodes', e.target.value)}
                    placeholder="200-299"
                    className={inputClass}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Max Redirects</label>
                  <input
                    type="number"
                    value={form.max_redirects}
                    onChange={(e) => field('max_redirects', e.target.value)}
                    placeholder="5"
                    className={inputClass}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>Keyword (optional)</label>
                  <input
                    type="text"
                    value={form.keyword}
                    onChange={(e) => field('keyword', e.target.value)}
                    placeholder="Expected text"
                    className={inputClass}
                  />
                </div>
                <div className="space-y-1.5 flex flex-col justify-end">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.keyword_invert}
                      onChange={(e) => field('keyword_invert', e.target.checked)}
                      className="rounded border-zinc-700 bg-zinc-800"
                    />
                    <span className={labelClass}>Invert keyword match</span>
                  </label>
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>Request Headers (JSON)</label>
                  <textarea
                    value={form.req_headers}
                    onChange={(e) => field('req_headers', e.target.value)}
                    placeholder='{"Authorization": "Bearer token"}'
                    rows={2}
                    className={cn(inputClass, 'resize-none font-mono text-xs')}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Request Body</label>
                  <textarea
                    value={form.req_body}
                    onChange={(e) => field('req_body', e.target.value)}
                    placeholder="POST body"
                    rows={2}
                    className={cn(inputClass, 'resize-none font-mono text-xs')}
                  />
                </div>
              </div>
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.tls_check}
                    onChange={(e) => field('tls_check', e.target.checked)}
                    className="rounded border-zinc-700 bg-zinc-800"
                  />
                  <span className={labelClass}>TLS certificate check</span>
                </label>
                {form.tls_check && (
                  <div className="flex items-center gap-2">
                    <span className={labelClass}>Warn before expiry (days)</span>
                    <input
                      type="number"
                      value={form.tls_expiry_warn_days}
                      onChange={(e) => field('tls_expiry_warn_days', e.target.value)}
                      className="w-20 bg-zinc-800 border border-zinc-700 text-white rounded-md px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
                    />
                  </div>
                )}
              </div>
            </>
          )}

          {form.type === 'tcp' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <label className={labelClass}>Hostname *</label>
                <input
                  type="text"
                  value={form.hostname}
                  onChange={(e) => field('hostname', e.target.value)}
                  placeholder="example.com"
                  className={inputClass}
                />
              </div>
              <div className="space-y-1.5">
                <label className={labelClass}>Port *</label>
                <input
                  type="number"
                  value={form.port}
                  onChange={(e) => field('port', e.target.value)}
                  placeholder="443"
                  className={inputClass}
                />
              </div>
            </div>
          )}

          {form.type === 'dns' && (
            <div className="space-y-3">
              <div className="space-y-1.5">
                <label className={labelClass}>Hostname *</label>
                <input
                  type="text"
                  value={form.hostname}
                  onChange={(e) => field('hostname', e.target.value)}
                  placeholder="example.com"
                  className={inputClass}
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>Record Type</label>
                  <select
                    value={form.dns_record_type}
                    onChange={(e) => field('dns_record_type', e.target.value)}
                    className={inputClass}
                  >
                    {['A', 'AAAA', 'MX', 'CNAME'].map((t) => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </select>
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Expected Value</label>
                  <input
                    type="text"
                    value={form.dns_expected_value}
                    onChange={(e) => field('dns_expected_value', e.target.value)}
                    placeholder="1.2.3.4"
                    className={inputClass}
                  />
                </div>
              </div>
            </div>
          )}

          {form.type === 'ping' && (
            <div className="space-y-1.5">
              <label className={labelClass}>Hostname *</label>
              <input
                type="text"
                value={form.hostname}
                onChange={(e) => field('hostname', e.target.value)}
                placeholder="example.com or 1.2.3.4"
                className={inputClass}
              />
            </div>
          )}

          {/* Common timing fields */}
          <div className="grid grid-cols-4 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Interval (s)</label>
              <input
                type="number"
                value={form.interval_secs}
                onChange={(e) => field('interval_secs', e.target.value)}
                placeholder="60"
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Timeout (s)</label>
              <input
                type="number"
                value={form.timeout_secs}
                onChange={(e) => field('timeout_secs', e.target.value)}
                placeholder="30"
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Retries</label>
              <input
                type="number"
                value={form.retries}
                onChange={(e) => field('retries', e.target.value)}
                placeholder="1"
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Retry Interval (s)</label>
              <input
                type="number"
                value={form.retry_interval}
                onChange={(e) => field('retry_interval', e.target.value)}
                placeholder="30"
                className={inputClass}
              />
            </div>
          </div>

          {/* Notification channels */}
          {channels.length > 0 && (
            <div className="space-y-2">
              <label className={labelClass}>Notification Channels (empty = use global default)</label>
              <div className="flex flex-wrap gap-2">
                {channels.map((ch) => (
                  <button
                    key={ch.id}
                    type="button"
                    onClick={() => toggleChannel(ch.id)}
                    className={cn(
                      'flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs border transition-colors',
                      form.alert_channel_ids.includes(ch.id)
                        ? 'bg-blue-600/20 border-blue-500/40 text-blue-300'
                        : 'bg-zinc-800 border-zinc-700 text-zinc-400 hover:text-white',
                    )}
                  >
                    <Bell className="w-3 h-3" />
                    {ch.name}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="space-y-1.5">
            <label className={labelClass}>Alert Reminder (minutes, 0 = no reminder)</label>
            <input
              type="number"
              value={form.alert_reminder_mins}
              onChange={(e) => field('alert_reminder_mins', e.target.value)}
              placeholder="0"
              className="w-32 bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
            />
          </div>
        </div>

        {/* Test result banner */}
        {testResult && (
          <div
            className={cn(
              'flex items-center gap-2 rounded-md px-3 py-2 text-sm',
              testResult.ok
                ? 'bg-green-500/10 border border-green-500/20 text-green-400'
                : 'bg-red-500/10 border border-red-500/20 text-red-400',
            )}
          >
            {testResult.ok ? (
              <CheckCircle2 className="w-4 h-4 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 shrink-0" />
            )}
            {testResult.ok ? (
              <span>
                Reachable
                {testResult.ping_ms != null && ` — ${testResult.ping_ms}ms`}
                {testResult.status_code != null && ` (HTTP ${testResult.status_code})`}
              </span>
            ) : (
              <span>{testResult.error ?? 'Unreachable'}</span>
            )}
          </div>
        )}

        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            variant="outline"
            onClick={handleTest}
            disabled={testMutation.isPending}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          >
            {testMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />
            ) : (
              <FlaskConical className="w-3.5 h-3.5 mr-2" />
            )}
            Test
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={isPending}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            {initial ? 'Save Changes' : 'Add Monitor'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Status Page Dialog ────────────────────────────────────────────────────────

interface StatusPageDialogProps {
  open: boolean
  onClose: () => void
  onSave: (form: StatusPageFormState) => void
  isPending: boolean
  initial?: UptimeStatusPage | null
  monitors: UptimeMonitor[]
}

function StatusPageDialog({ open, onClose, onSave, isPending, initial, monitors }: StatusPageDialogProps) {
  const [form, setForm] = useState<StatusPageFormState>(() => ({
    slug: initial?.slug ?? '',
    title: initial?.title ?? '',
    description: initial?.description ?? '',
    logo_url: initial?.logo_url ?? '',
    theme: (initial?.theme as StatusPageFormState['theme'] | undefined) ?? 'dark',
    history_days: String(initial?.history_days ?? 30),
    is_public: initial?.is_public ?? true,
    monitor_ids: initial?.monitors?.map((m) => m.monitor_id) ?? [],
  }))

  function field<K extends keyof StatusPageFormState>(key: K, value: StatusPageFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function toggleMonitor(id: number) {
    setForm((prev) => ({
      ...prev,
      monitor_ids: prev.monitor_ids.includes(id)
        ? prev.monitor_ids.filter((m) => m !== id)
        : [...prev.monitor_ids, id],
    }))
  }

  function handleSubmit() {
    if (!form.slug.trim()) { toast.error('Slug is required'); return }
    if (!form.title.trim()) { toast.error('Title is required'); return }
    onSave(form)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit Status Page' : 'Create Status Page'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Slug *</label>
              <input
                type="text"
                value={form.slug}
                onChange={(e) => field('slug', e.target.value)}
                placeholder="my-status"
                className={inputClass}
                disabled={!!initial}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Title *</label>
              <input
                type="text"
                value={form.title}
                onChange={(e) => field('title', e.target.value)}
                placeholder="System Status"
                className={inputClass}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className={labelClass}>Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => field('description', e.target.value)}
              placeholder="Current system status"
              className={inputClass}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Logo URL</label>
              <input
                type="url"
                value={form.logo_url}
                onChange={(e) => field('logo_url', e.target.value)}
                placeholder="https://..."
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Theme</label>
              <select
                value={form.theme}
                onChange={(e) => field('theme', e.target.value as StatusPageFormState['theme'])}
                className={inputClass}
              >
                <option value="dark">Dark</option>
                <option value="light">Light</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>History Days</label>
              <input
                type="number"
                value={form.history_days}
                onChange={(e) => field('history_days', e.target.value)}
                placeholder="30"
                className={inputClass}
              />
            </div>
            <div className="space-y-1.5 flex flex-col justify-end">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.is_public}
                  onChange={(e) => field('is_public', e.target.checked)}
                  className="rounded border-zinc-700 bg-zinc-800"
                />
                <span className={labelClass}>Public page</span>
              </label>
            </div>
          </div>

          {monitors.length > 0 && (
            <div className="space-y-2">
              <label className={labelClass}>Monitors to display</label>
              <div className="flex flex-wrap gap-2 max-h-32 overflow-y-auto">
                {monitors.map((m) => (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => toggleMonitor(m.id)}
                    className={cn(
                      'flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs border transition-colors',
                      form.monitor_ids.includes(m.id)
                        ? 'bg-blue-600/20 border-blue-500/40 text-blue-300'
                        : 'bg-zinc-800 border-zinc-700 text-zinc-400 hover:text-white',
                    )}
                  >
                    {m.name}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>

        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={isPending}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            {initial ? 'Save Changes' : 'Create Page'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Monitors Tab ──────────────────────────────────────────────────────────────

interface MonitorsTabProps {
  monitors: UptimeMonitor[]
  isLoading: boolean
  incidents: UptimeIncident[]
  incidentsError?: Error
  retryIncidents: () => void
  retryingIncidents: boolean
  statusFilter: StatusFilter
  onStatusFilterChange: (status: StatusFilter) => void
  onAdd: () => void
  onBulkCreate: () => void
  isBulkCreating: boolean
  onEdit: (m: UptimeMonitor) => void
  onDelete: (id: number) => void
  onPause: (id: number) => void
  onResume: (id: number) => void
  onCheckNow: (id: number) => void
  checkingId: number | null
  deletingId: number | null
}

function MonitorsTab({
  monitors,
  isLoading,
  incidents,
  incidentsError,
  retryIncidents,
  retryingIncidents,
  statusFilter,
  onStatusFilterChange,
  onAdd,
  onBulkCreate,
  isBulkCreating,
  onEdit,
  onDelete,
  onPause,
  onResume,
  onCheckNow,
  checkingId,
  deletingId,
}: MonitorsTabProps) {
  const [typeFilter, setTypeFilter] = useState<MonitorType | 'all'>('all')
  const [expandedId, setExpandedId] = useState<number | null>(null)
  const [showBulkConfirm, setShowBulkConfirm] = useState(false)

  const filtered = monitors.filter((m) => {
    if (typeFilter !== 'all' && m.type !== typeFilter) return false
    if (statusFilter === 'up' && m.current_status !== 1) return false
    if (statusFilter === 'down' && m.current_status !== 0) return false
    if (statusFilter === 'paused' && !m.maintenance_mode) return false
    return true
  })

  const selectClass =
    'bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500'

  return (
    <div className="space-y-4">
      {incidentsError && (
        <UptimeLoadError title="Incident inventory could not be loaded. Monitor incident details are unavailable." error={incidentsError} retry={retryIncidents} retrying={retryingIncidents} />
      )}
      {/* Filter bar */}
      <div className="flex items-center gap-3 flex-wrap">
        <select
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value as MonitorType | 'all')}
          className={selectClass}
        >
          <option value="all">All Types</option>
          <option value="http">HTTP</option>
          <option value="tcp">TCP</option>
          <option value="dns">DNS</option>
          <option value="ping">Ping</option>
        </select>
        <select
          value={statusFilter}
          onChange={(e) => onStatusFilterChange(e.target.value as StatusFilter)}
          className={selectClass}
        >
          <option value="all">All Status</option>
          <option value="up">Up</option>
          <option value="down">Down</option>
          <option value="paused">Paused</option>
        </select>
        <div className="flex-1" />
        <Button
          onClick={() => setShowBulkConfirm(true)}
          size="sm"
          variant="outline"
          className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
          disabled={isBulkCreating}
        >
          {isBulkCreating ? (
            <Loader2 className="w-4 h-4 mr-1.5 animate-spin" />
          ) : (
            <Layers className="w-4 h-4 mr-1.5" />
          )}
          Monitor All Domains
        </Button>
        <Button
          onClick={onAdd}
          size="sm"
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          <Plus className="w-4 h-4 mr-1.5" />
          Add Monitor
        </Button>
      </div>

      {/* Bulk create confirmation dialog */}
      <Dialog open={showBulkConfirm} onOpenChange={(o) => { if (!o) setShowBulkConfirm(false) }}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
          <DialogHeader>
            <DialogTitle className="text-white">Monitor All Domains</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-zinc-400 py-2">
            This will create HTTP monitors for all domains that don't already have one.
          </p>
          <DialogFooter className="gap-2">
            <Button
              variant="ghost"
              onClick={() => setShowBulkConfirm(false)}
              className="text-zinc-400 hover:text-white hover:bg-zinc-800"
            >
              Cancel
            </Button>
            <Button
              onClick={() => { setShowBulkConfirm(false); onBulkCreate() }}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              Create Monitors
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {isLoading ? (
            <div className="p-4 space-y-3">
              {[...Array(4)].map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-500">
              <Activity className="w-8 h-8 mb-3 opacity-40" />
              <p className="text-sm">No monitors found</p>
              {monitors.length === 0 && (
                <Button
                  onClick={onAdd}
                  variant="ghost"
                  size="sm"
                  className="mt-3 text-blue-400 hover:text-blue-300"
                >
                  Add your first monitor
                </Button>
              )}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="border-zinc-800 hover:bg-transparent">
                  <TableHead className="text-zinc-500 w-8" />
                  <TableHead className="text-zinc-500">Monitor</TableHead>
                  <TableHead className="text-zinc-500">Type</TableHead>
                  <TableHead className="text-zinc-500">Status</TableHead>
                  <TableHead className="text-zinc-500">Uptime</TableHead>
                  <TableHead className="text-zinc-500">Response</TableHead>
                  <TableHead className="text-zinc-500">Last Check</TableHead>
                  <TableHead className="text-zinc-500 w-32" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((m) => (
                  <Fragment key={m.id}>
                    <TableRow
                      className="border-zinc-800 cursor-pointer hover:bg-zinc-800/40 transition-colors"
                      onClick={() => setExpandedId(expandedId === m.id ? null : m.id)}
                    >
                      <TableCell className="py-2 pl-4 pr-2">
                        {expandedId === m.id ? (
                          <ChevronDown className="w-3.5 h-3.5 text-zinc-500" />
                        ) : (
                          <ChevronRight className="w-3.5 h-3.5 text-zinc-500" />
                        )}
                      </TableCell>
                      <TableCell className="py-2">
                        <div className="flex flex-col">
                          <span className="text-white text-sm font-medium">{m.name}</span>
                          <span className="text-zinc-500 text-xs truncate max-w-[200px]">
                            {m.url ?? m.hostname ?? '—'}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="py-2">
                        <MonitorTypeBadge type={m.type} />
                      </TableCell>
                      <TableCell className="py-2">
                        <div className="flex items-center gap-2">
                          <MonitorStatusDot status={m.current_status} paused={m.maintenance_mode} />
                          <span className="text-xs text-zinc-400">
                            {m.maintenance_mode
                              ? 'Paused'
                              : m.current_status === 1
                              ? 'Up'
                              : m.current_status === 0
                              ? 'Down'
                              : 'Pending'}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="py-2 text-zinc-300 text-sm">
                        {formatUptime(m.uptime_24h)}
                      </TableCell>
                      <TableCell className="py-2 text-zinc-300 text-sm">
                        {m.avg_ping_ms != null ? `${Math.round(m.avg_ping_ms)}ms` : '—'}
                      </TableCell>
                      <TableCell className="py-2 text-zinc-500 text-xs">
                        {formatTime(m.last_check_at)}
                      </TableCell>
                      <TableCell className="py-2">
                        <div
                          className="flex items-center gap-1"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <button
                            onClick={() => onCheckNow(m.id)}
                            disabled={checkingId !== null || m.maintenance_mode || !m.is_active}
                            title={m.maintenance_mode || !m.is_active ? 'Resume the monitor before checking' : 'Check now'}
                            aria-label={`Check ${m.name} now`}
                            className="p-1.5 rounded text-zinc-400 hover:text-cyan-400 hover:bg-zinc-800 transition-colors disabled:cursor-not-allowed disabled:opacity-40"
                          >
                            <RefreshCw className={cn('w-3.5 h-3.5', checkingId === m.id && 'animate-spin')} />
                          </button>
                          {m.maintenance_mode ? (
                            <button
                              onClick={() => onResume(m.id)}
                              title="Resume"
                              className="p-1.5 rounded text-zinc-400 hover:text-green-400 hover:bg-zinc-800 transition-colors"
                            >
                              <Play className="w-3.5 h-3.5" />
                            </button>
                          ) : (
                            <button
                              onClick={() => onPause(m.id)}
                              title="Pause"
                              className="p-1.5 rounded text-zinc-400 hover:text-yellow-400 hover:bg-zinc-800 transition-colors"
                            >
                              <Pause className="w-3.5 h-3.5" />
                            </button>
                          )}
                          <button
                            onClick={() => onEdit(m)}
                            title="Edit"
                            className="p-1.5 rounded text-zinc-400 hover:text-blue-400 hover:bg-zinc-800 transition-colors"
                          >
                            <Pencil className="w-3.5 h-3.5" />
                          </button>
                          <button
                            onClick={() => onDelete(m.id)}
                            disabled={deletingId !== null}
                            title="Delete"
                            aria-label={`Delete ${m.name}`}
                            className="p-1.5 rounded text-zinc-400 hover:text-red-400 hover:bg-zinc-800 transition-colors disabled:cursor-not-allowed disabled:opacity-40"
                          >
                            {deletingId === m.id ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Trash2 className="w-3.5 h-3.5" />}
                          </button>
                        </div>
                      </TableCell>
                    </TableRow>
                    {expandedId === m.id && (
                      <TableRow key={`${m.id}-detail`} className="border-zinc-800 hover:bg-transparent">
                        <TableCell colSpan={8} className="p-0">
                          <MonitorDetail
                            monitor={m}
                            incidents={incidents.filter((inc) => inc.monitor_id === m.id)}
                          />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Incidents Tab ─────────────────────────────────────────────────────────────

export function IncidentsTab({ monitors, onCheckNow, checkingId }: { monitors: UptimeMonitor[]; onCheckNow: (id: number) => void; checkingId: number | null }) {
  const monitorMap = Object.fromEntries(monitors.map((m) => [m.id, m.name]))

  const incidentsQuery = useQuery<UptimeIncident[]>({
    queryKey: ['uptime', 'all-incidents'],
    queryFn: () => api.get<UptimeIncident[]>('/uptime/incidents'),
  })

  const incidents = (incidentsQuery.data ?? []).map((inc) => ({
    ...inc,
    monitor_name: inc.monitor_name ?? monitorMap[inc.monitor_id] ?? `Monitor ${inc.monitor_id}`,
  }))

  const activeIncidents = incidents.filter((i) => !i.resolved_at)

  return (
    <div className="space-y-4">
      {activeIncidents.length > 0 && (
        <Card className="bg-red-950/30 border-red-900/50">
          <CardHeader className="pb-2 pt-4 px-4">
            <CardTitle className="text-sm text-red-400 flex items-center gap-2">
              <AlertTriangle className="w-4 h-4" />
              {activeIncidents.length} Active Incident{activeIncidents.length > 1 ? 's' : ''}
            </CardTitle>
          </CardHeader>
          <CardContent className="px-4 pb-4">
            <div className="space-y-2">
              {activeIncidents.map((inc) => (
                <div
                  key={inc.id}
                  className="flex flex-col gap-2 bg-red-900/20 rounded px-3 py-2 text-sm sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="flex min-w-0 items-start gap-2 sm:items-center">
                    <XCircle className="w-3.5 h-3.5 text-red-400 shrink-0" />
                    <span className="shrink-0 text-white font-medium">{inc.monitor_name}</span>
                    <span className="break-words text-red-300/70">— {inc.cause ?? inc.type}</span>
                  </div>
                  <div className="flex shrink-0 items-center justify-between gap-2 text-xs text-zinc-400 sm:justify-end">
                    <span className="flex items-center gap-1.5"><Clock className="w-3 h-3" />Started {formatTime(inc.started_at)}</span>
                    <Button variant="outline" size="xs" disabled={checkingId !== null} onClick={() => onCheckNow(inc.monitor_id)}>
                      <RefreshCw className={cn('size-3', checkingId === inc.monitor_id && 'animate-spin')} />
                      Check now
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {incidentsQuery.isLoading ? (
            <div className="p-4 space-y-3">
              {[...Array(5)].map((_, i) => <Skeleton key={i} className="h-10 w-full bg-zinc-800" />)}
            </div>
          ) : incidentsQuery.isError ? (
            <div className="p-4">
              <UptimeLoadError title="Incident history could not be loaded." error={incidentsQuery.error} retry={() => { void incidentsQuery.refetch() }} retrying={incidentsQuery.isFetching} />
            </div>
          ) : incidents.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-500">
              <CheckCircle2 className="w-8 h-8 mb-3 opacity-40 text-green-500" />
              <p className="text-sm">No incidents recorded</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="border-zinc-800 hover:bg-transparent">
                  <TableHead className="text-zinc-500">Monitor</TableHead>
                  <TableHead className="text-zinc-500">Cause</TableHead>
                  <TableHead className="text-zinc-500">Started</TableHead>
                  <TableHead className="text-zinc-500">Resolved</TableHead>
                  <TableHead className="text-zinc-500">Duration</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {incidents.map((inc) => (
                  <TableRow key={inc.id} className="border-zinc-800 hover:bg-zinc-800/30">
                    <TableCell className="py-2">
                      <span className="text-white text-sm">{inc.monitor_name ?? `Monitor ${inc.monitor_id}`}</span>
                    </TableCell>
                    <TableCell className="py-2">
                      <span className="text-zinc-400 text-sm">{inc.cause ?? inc.type}</span>
                    </TableCell>
                    <TableCell className="py-2 text-zinc-400 text-sm">
                      {new Date(inc.started_at).toLocaleString()}
                    </TableCell>
                    <TableCell className="py-2">
                      {inc.resolved_at ? (
                        <span className="text-zinc-400 text-sm">{new Date(inc.resolved_at).toLocaleString()}</span>
                      ) : (
                        <Badge className="bg-red-500/10 text-red-400 border-red-500/20 text-xs border">Active</Badge>
                      )}
                    </TableCell>
                    <TableCell className="py-2 text-zinc-400 text-sm">
                      {formatDuration(inc.duration_secs)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Status Pages Tab ──────────────────────────────────────────────────────────

export function StatusPagesTab({ monitors }: { monitors: UptimeMonitor[] }) {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editPage, setEditPage] = useState<UptimeStatusPage | null>(null)

  const pagesQuery = useQuery<UptimeStatusPage[]>({
    queryKey: ['uptime', 'status-pages'],
    queryFn: () => api.get<UptimeStatusPage[]>('/uptime/status-pages'),
  })
  const pages = pagesQuery.data ?? []

  function buildPayload(form: StatusPageFormState) {
    return {
      slug: form.slug,
      title: form.title,
      description: form.description,
      logo_url: form.logo_url,
      theme: form.theme,
      history_days: parseInt(form.history_days) || 30,
      is_public: form.is_public,
      monitors: form.monitor_ids.map((id, idx) => ({ monitor_id: id, sort_order: idx + 1 })),
    }
  }

  const createMutation = useMutation({
    mutationFn: (form: StatusPageFormState) =>
      api.post('/uptime/status-pages', buildPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'status-pages'] })
      setAddOpen(false)
      toast.success('Status page created')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create status page'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, form }: { id: number; form: StatusPageFormState }) =>
      api.put(`/uptime/status-pages/${id}`, buildPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'status-pages'] })
      setEditPage(null)
      toast.success('Status page updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update status page'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.delete(`/uptime/status-pages/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'status-pages'] })
      toast.success('Status page deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete status page'),
  })

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button
          onClick={() => setAddOpen(true)}
          size="sm"
          className="bg-blue-600 hover:bg-blue-500 text-white"
          disabled={pagesQuery.isError}
        >
          <Plus className="w-4 h-4 mr-1.5" />
          Create Status Page
        </Button>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {pagesQuery.isLoading ? (
            <div className="p-4 space-y-3">
              {[...Array(3)].map((_, i) => <Skeleton key={i} className="h-10 w-full bg-zinc-800" />)}
            </div>
          ) : pagesQuery.isError ? (
            <div className="p-4">
              <UptimeLoadError title="Status page inventory could not be loaded." error={pagesQuery.error} retry={() => { void pagesQuery.refetch() }} retrying={pagesQuery.isFetching} />
            </div>
          ) : pages.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-500">
              <FileText className="w-8 h-8 mb-3 opacity-40" />
              <p className="text-sm">No status pages yet</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="border-zinc-800 hover:bg-transparent">
                  <TableHead className="text-zinc-500">Title</TableHead>
                  <TableHead className="text-zinc-500">Slug</TableHead>
                  <TableHead className="text-zinc-500">Monitors</TableHead>
                  <TableHead className="text-zinc-500">Visibility</TableHead>
                  <TableHead className="text-zinc-500">URL</TableHead>
                  <TableHead className="text-zinc-500 w-24" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {pages.map((page) => (
                  <TableRow key={page.id} className="border-zinc-800 hover:bg-zinc-800/30">
                    <TableCell className="py-2 text-white text-sm font-medium">
                      {page.title}
                    </TableCell>
                    <TableCell className="py-2 text-zinc-400 text-sm font-mono">
                      {page.slug}
                    </TableCell>
                    <TableCell className="py-2 text-zinc-400 text-sm">
                      {page.monitors?.length ?? 0} monitors
                    </TableCell>
                    <TableCell className="py-2">
                      <Badge
                        className={cn(
                          'text-xs border',
                          page.is_public
                            ? 'bg-green-500/10 text-green-400 border-green-500/20'
                            : 'bg-zinc-500/10 text-zinc-400 border-zinc-700',
                        )}
                      >
                        {page.is_public ? 'Public' : 'Private'}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-2">
                      <a
                        href={`/status/${page.slug}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-1 text-blue-400 hover:text-blue-300 text-xs transition-colors"
                        onClick={(e) => e.stopPropagation()}
                      >
                        /status/{page.slug}
                        <ExternalLink className="w-3 h-3" />
                      </a>
                    </TableCell>
                    <TableCell className="py-2">
                      <div className="flex items-center gap-1">
                        <button
                          onClick={() => setEditPage(page)}
                          className="p-1.5 rounded text-zinc-400 hover:text-blue-400 hover:bg-zinc-800 transition-colors"
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={() => {
                            if (window.confirm(`Delete status page "${page.title}"?\n\nThe public page configuration will be removed. Monitors and their history will be kept.`)) {
                              deleteMutation.mutate(page.id)
                            }
                          }}
                          disabled={deleteMutation.isPending}
                          className="p-1.5 rounded text-zinc-400 hover:text-red-400 hover:bg-zinc-800 transition-colors disabled:cursor-not-allowed disabled:opacity-40"
                          title="Delete"
                          aria-label={`Delete status page ${page.title}`}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <StatusPageDialog
        key={addOpen ? 'status-page-add-open' : 'status-page-add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSave={(form) => createMutation.mutate(form)}
        isPending={createMutation.isPending}
        monitors={monitors}
      />
      {editPage && (
        <StatusPageDialog
          open={!!editPage}
          onClose={() => setEditPage(null)}
          onSave={(form) => updateMutation.mutate({ id: editPage.id, form })}
          isPending={updateMutation.isPending}
          initial={editPage}
          monitors={monitors}
        />
      )}
    </div>
  )
}

// ─── Settings Tab ──────────────────────────────────────────────────────────────

export function SettingsTab() {
  const queryClient = useQueryClient()
  const [selectedIds, setSelectedIds] = useState<number[] | null>(null)

  const channelsQuery = useQuery<NotificationChannel[]>({
    queryKey: ['notify-channels'],
    queryFn: () => api.get<NotificationChannel[]>('/notify/channels'),
  })
  const channels = channelsQuery.data ?? []

  const settingsQuery = useQuery<Record<string, string>>({
    queryKey: ['uptime', 'settings'],
    queryFn: () => api.get<Record<string, string>>('/uptime/settings'),
  })
  const settings = settingsQuery.data

  let savedSelectedIds: number[] = []
  if (settings) {
    const raw = settings['uptime_default_channels'] ?? '[]'
    try {
      const parsed: unknown = JSON.parse(raw)
      if (Array.isArray(parsed) && parsed.every((id) => typeof id === 'number')) savedSelectedIds = parsed
    } catch { /* malformed persisted data falls back to no selection */ }
  }
  const effectiveSelectedIds = selectedIds ?? savedSelectedIds

  const saveMutation = useMutation({
    mutationFn: () =>
      api.put('/uptime/settings', {
        uptime_default_channels: JSON.stringify(effectiveSelectedIds),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'settings'] })
      toast.success('Settings saved')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to save settings'),
  })

  function toggleChannel(id: number) {
    setSelectedIds((prev) => {
      const current = prev ?? savedSelectedIds
      return current.includes(id) ? current.filter((c) => c !== id) : [...current, id]
    })
  }

  return (
    <div className="space-y-6 max-w-lg">
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-sm text-zinc-300 flex items-center gap-2">
            <Bell className="w-4 h-4" />
            Default Notification Channels
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-xs text-zinc-500">
            Monitors without explicit channel assignment will use these channels.
          </p>

          {settingsQuery.isLoading || channelsQuery.isLoading ? (
            <div className="space-y-2">
              {[...Array(3)].map((_, index) => <Skeleton key={index} className="h-8 bg-zinc-800" />)}
            </div>
          ) : settingsQuery.isError ? (
            <UptimeLoadError title="Uptime settings could not be loaded. Settings controls are paused." error={settingsQuery.error} retry={() => { void settingsQuery.refetch() }} retrying={settingsQuery.isFetching} />
          ) : channelsQuery.isError ? (
            <UptimeLoadError title="Notification channel inventory could not be loaded. Settings controls are paused." error={channelsQuery.error} retry={() => { void channelsQuery.refetch() }} retrying={channelsQuery.isFetching} />
          ) : channels.length === 0 ? (
            <div className="text-zinc-500 text-sm flex items-center gap-2">
              <AlertTriangle className="w-4 h-4" />
              No channels configured. Add channels in the Notifications page first.
            </div>
          ) : (
            <div className="space-y-2">
              {channels.map((ch) => (
                <label key={ch.id} className="flex items-center gap-3 cursor-pointer group">
                  <input
                    type="checkbox"
                    checked={effectiveSelectedIds.includes(ch.id)}
                    onChange={() => toggleChannel(ch.id)}
                    className="rounded border-zinc-700 bg-zinc-800 accent-blue-500"
                  />
                  <div className="flex items-center gap-2">
                    <span className="text-white text-sm group-hover:text-blue-300 transition-colors">
                      {ch.name}
                    </span>
                    <Badge className="text-xs border bg-zinc-800 text-zinc-400 border-zinc-700 capitalize">
                      {ch.type}
                    </Badge>
                    {!ch.enabled && (
                      <Badge className="text-xs border bg-red-500/10 text-red-400 border-red-500/20">
                        Disabled
                      </Badge>
                    )}
                  </div>
                </label>
              ))}
            </div>
          )}

          <Button
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || settingsQuery.isLoading || channelsQuery.isLoading || channels.length === 0 || settingsQuery.isError || channelsQuery.isError}
            size="sm"
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {saveMutation.isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Save Settings
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Summary Cards ─────────────────────────────────────────────────────────────

function SummaryCards({ monitors, onStatusSelect }: { monitors: UptimeMonitor[]; onStatusSelect: (status: StatusFilter) => void }) {
  const up = monitors.filter((m) => !m.maintenance_mode && m.current_status === 1).length
  const down = monitors.filter((m) => !m.maintenance_mode && m.current_status === 0).length
  const paused = monitors.filter((m) => m.maintenance_mode).length
  const total = monitors.length

  const cards = [
    {
      label: 'Total',
      value: total,
      filter: 'all' as StatusFilter,
      icon: <Activity className="w-4 h-4 text-zinc-400" />,
      color: 'text-white',
    },
    {
      label: 'Up',
      value: up,
      filter: 'up' as StatusFilter,
      icon: <CheckCircle2 className="w-4 h-4 text-green-400" />,
      color: 'text-green-400',
    },
    {
      label: 'Down',
      value: down,
      filter: 'down' as StatusFilter,
      icon: <XCircle className="w-4 h-4 text-red-400" />,
      color: 'text-red-400',
    },
    {
      label: 'Paused',
      value: paused,
      filter: 'paused' as StatusFilter,
      icon: <PauseCircle className="w-4 h-4 text-yellow-400" />,
      color: 'text-yellow-400',
    },
  ]

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {cards.map((c) => (
        <button
          key={c.label}
          type="button"
          onClick={() => onStatusSelect(c.filter)}
          className="rounded-xl text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/60"
          aria-label={`Show ${c.label.toLowerCase()} uptime monitors`}
        >
          <Card className="h-full border-zinc-800 bg-zinc-900 transition-colors hover:border-blue-500/50">
            <CardContent className="p-4 flex items-center gap-3">
              {c.icon}
              <div>
                <div className={cn('text-2xl font-bold', c.color)}>{c.value}</div>
                <div className="text-xs text-zinc-500">{c.label}</div>
              </div>
            </CardContent>
          </Card>
        </button>
      ))}
    </div>
  )
}

// ─── Main Page ─────────────────────────────────────────────────────────────────

export default function Uptime() {
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const [showCreateDialog, setShowCreateDialog] = useState(false)
  const [editMonitor, setEditMonitor] = useState<UptimeMonitor | null>(null)

  const requestedStatus = searchParams.get('status')
  const statusFilter: StatusFilter = requestedStatus === 'up'
    || requestedStatus === 'down'
    || requestedStatus === 'paused'
    ? requestedStatus
    : 'all'
  const selectStatusFilter = (status: StatusFilter) => {
    const next = new URLSearchParams(searchParams)
    if (status === 'all') next.delete('status')
    else next.set('status', status)
    setSearchParams(next, { replace: true })
  }

  const monitorsQuery = useQuery<UptimeMonitor[]>({
    queryKey: ['uptime', 'monitors'],
    queryFn: () => api.get<UptimeMonitor[]>('/uptime/monitors'),
    refetchInterval: 30_000,
  })
  const monitors = monitorsQuery.data ?? []

  const channelsQuery = useQuery<NotificationChannel[]>({
    queryKey: ['notify-channels'],
    queryFn: () => api.get<NotificationChannel[]>('/notify/channels'),
  })
  const channels = channelsQuery.data ?? []

  // Global incidents query — single request instead of N per-monitor requests
  const allIncidentsQuery = useQuery<UptimeIncident[]>({
    queryKey: ['uptime', 'all-incidents'],
    queryFn: () => api.get<UptimeIncident[]>('/uptime/incidents'),
    refetchInterval: 60_000,
  })
  const allIncidents = allIncidentsQuery.data ?? []

  const bulkCreateMutation = useMutation({
    mutationFn: () => api.post<{ created: number }>('/uptime/monitors/bulk-from-domains', {}),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      toast.success(`Created ${res?.created ?? 0} monitor${(res?.created ?? 0) !== 1 ? 's' : ''}`)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create monitors from domains'),
  })

  const createMutation = useMutation({
    mutationFn: (form: MonitorFormState) =>
      api.post('/uptime/monitors', formToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      setShowCreateDialog(false)
      toast.success('Monitor created')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create monitor'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, form }: { id: number; form: MonitorFormState }) =>
      api.put(`/uptime/monitors/${id}`, formToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      setEditMonitor(null)
      toast.success('Monitor updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update monitor'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.delete(`/uptime/monitors/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      toast.success('Monitor deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete monitor'),
  })

  const requestMonitorDelete = (id: number) => {
    const monitor = monitors.find((item) => item.id === id)
    const name = monitor?.name ?? `Monitor ${id}`
    if (!window.confirm(`Delete uptime monitor "${name}"?\n\nIts heartbeat and incident history will also be permanently removed.`)) return
    deleteMutation.mutate(id)
  }

  const pauseMutation = useMutation({
    mutationFn: (id: number) => api.post(`/uptime/monitors/${id}/pause`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      toast.success('Monitor paused')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to pause monitor'),
  })

  const resumeMutation = useMutation({
    mutationFn: (id: number) => api.post(`/uptime/monitors/${id}/resume`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['uptime', 'monitors'] })
      toast.success('Monitor resumed')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to resume monitor'),
  })

  const checkNowMutation = useMutation<TestResult, Error, number>({
    mutationFn: (id) => api.post<TestResult>(`/uptime/monitors/${id}/check-now`),
    onSuccess: async (result, id) => {
      const name = monitors.find((monitor) => monitor.id === id)?.name ?? `Monitor ${id}`
      if (result.ok) {
        const detail = [result.ping_ms != null ? `${Math.round(result.ping_ms)}ms` : '', result.status_code != null ? `HTTP ${result.status_code}` : ''].filter(Boolean).join(' · ')
        toast.success(`${name} is reachable${detail ? ` · ${detail}` : ''}`)
      } else {
        toast.error(`${name}: ${result.error ?? 'check failed'}`)
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['uptime'] }),
        queryClient.invalidateQueries({ queryKey: ['dashboard', 'uptime'] }),
      ])
    },
    onError: (error) => toast.error(error.message || 'Check now failed'),
  })

  if (monitorsQuery.isLoading) {
    return (
      <div className="space-y-6 p-6">
        <div>
          <h1 className="text-2xl font-bold text-white">Uptime Monitoring</h1>
          <p className="text-zinc-500 text-sm mt-1">Monitor services, track incidents, manage status pages</p>
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {[...Array(4)].map((_, index) => <Skeleton key={index} className="h-20 bg-zinc-900" />)}
        </div>
        <Skeleton className="h-72 bg-zinc-900" />
      </div>
    )
  }

  if (monitorsQuery.isError) {
    return (
      <div className="space-y-6 p-6">
        <div>
          <h1 className="text-2xl font-bold text-white">Uptime Monitoring</h1>
          <p className="text-zinc-500 text-sm mt-1">Monitor services, track incidents, manage status pages</p>
        </div>
        <UptimeLoadError title="Monitor inventory could not be loaded. Uptime controls are paused." error={monitorsQuery.error} retry={() => { void monitorsQuery.refetch() }} retrying={monitorsQuery.isFetching} />
      </div>
    )
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Uptime Monitoring</h1>
          <p className="text-zinc-500 text-sm mt-1">Monitor services, track incidents, manage status pages</p>
        </div>
        <button
          onClick={() => queryClient.invalidateQueries({ queryKey: ['uptime'] })}
          className="p-2 rounded-md text-zinc-500 hover:text-white hover:bg-zinc-800 transition-colors"
          title="Refresh"
        >
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      {/* Summary */}
      <SummaryCards monitors={monitors} onStatusSelect={selectStatusFilter} />

      {channelsQuery.isError && (
        <UptimeLoadError title="Notification channel inventory could not be loaded. Channel assignment is unavailable." error={channelsQuery.error} retry={() => { void channelsQuery.refetch() }} retrying={channelsQuery.isFetching} />
      )}

      {/* Tabs */}
      <Tabs defaultValue="monitors">
        <TabsList className="bg-zinc-800 border border-zinc-700">
          <TabsTrigger value="monitors" className="data-[state=active]:bg-zinc-700 data-[state=active]:text-white text-zinc-400 flex items-center gap-1.5">
            <Activity className="w-3.5 h-3.5" />
            Monitors
          </TabsTrigger>
          <TabsTrigger value="incidents" className="data-[state=active]:bg-zinc-700 data-[state=active]:text-white text-zinc-400 flex items-center gap-1.5">
            <AlertTriangle className="w-3.5 h-3.5" />
            Incidents
          </TabsTrigger>
          <TabsTrigger value="status-pages" className="data-[state=active]:bg-zinc-700 data-[state=active]:text-white text-zinc-400 flex items-center gap-1.5">
            <BarChart2 className="w-3.5 h-3.5" />
            Status Pages
          </TabsTrigger>
          <TabsTrigger value="settings" className="data-[state=active]:bg-zinc-700 data-[state=active]:text-white text-zinc-400 flex items-center gap-1.5">
            <Settings2 className="w-3.5 h-3.5" />
            Settings
          </TabsTrigger>
        </TabsList>

        <div className="mt-4">
          <TabsContent value="monitors">
            <MonitorsTab
              monitors={monitors}
              isLoading={monitorsQuery.isLoading}
              incidents={allIncidents}
              incidentsError={allIncidentsQuery.error ?? undefined}
              retryIncidents={() => { void allIncidentsQuery.refetch() }}
              retryingIncidents={allIncidentsQuery.isFetching}
              statusFilter={statusFilter}
              onStatusFilterChange={selectStatusFilter}
              onAdd={() => setShowCreateDialog(true)}
              onBulkCreate={() => bulkCreateMutation.mutate()}
              isBulkCreating={bulkCreateMutation.isPending}
              onEdit={(m) => setEditMonitor(m)}
              onDelete={requestMonitorDelete}
              onPause={(id) => pauseMutation.mutate(id)}
              onResume={(id) => resumeMutation.mutate(id)}
              onCheckNow={(id) => checkNowMutation.mutate(id)}
              checkingId={checkNowMutation.isPending ? checkNowMutation.variables ?? null : null}
              deletingId={deleteMutation.isPending ? deleteMutation.variables ?? null : null}
            />
          </TabsContent>

          <TabsContent value="incidents">
            <IncidentsTab monitors={monitors} onCheckNow={(id) => checkNowMutation.mutate(id)} checkingId={checkNowMutation.isPending ? checkNowMutation.variables ?? null : null} />
          </TabsContent>

          <TabsContent value="status-pages">
            <StatusPagesTab monitors={monitors} />
          </TabsContent>

          <TabsContent value="settings">
            <SettingsTab />
          </TabsContent>
        </div>
      </Tabs>

      {/* Monitor create/edit dialogs */}
      <MonitorDialog
        key={showCreateDialog ? 'monitor-add-open' : 'monitor-add-closed'}
        open={showCreateDialog}
        onClose={() => setShowCreateDialog(false)}
        onSave={(form) => createMutation.mutate(form)}
        isPending={createMutation.isPending}
        channels={channels}
      />
      {editMonitor && (
        <MonitorDialog
          open={!!editMonitor}
          onClose={() => setEditMonitor(null)}
          onSave={(form) => updateMutation.mutate({ id: editMonitor.id, form })}
          isPending={updateMutation.isPending}
          initial={editMonitor}
          channels={channels}
        />
      )}
    </div>
  )
}

import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Cloud,
  Plus,
  Trash2,
  Loader2,
  Edit2,
  AlertTriangle,
  Search,
  ChevronDown,
  RefreshCw,
  Server,
  Mail,
  Wrench,
  ArrowRight,
  List,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
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
import { api, ApiError } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { CFZone, CFRecord, CFEmailRouting } from '@/lib/types'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import {
  INTEGRATION_NOT_CONFIGURED,
  INTEGRATION_UNAVAILABLE,
  integrationStateFromObservation,
  integrationStatePresentation,
  normalizeIntegrationState,
  type IntegrationState,
} from '@/lib/integrationState'

// ─── Constants ────────────────────────────────────────────────────────────────

const RECORD_TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV'] as const
type RecordType = (typeof RECORD_TYPES)[number]

const RECORD_TYPE_STYLES: Record<string, string> = {
  A: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
  AAAA: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
  CNAME: 'bg-green-500/10 text-green-400 border-green-500/20',
  MX: 'bg-orange-500/10 text-orange-400 border-orange-500/20',
  TXT: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20',
  NS: 'bg-cyan-500/10 text-cyan-400 border-cyan-500/20',
  SRV: 'bg-pink-500/10 text-pink-400 border-pink-500/20',
}

const TTL_OPTIONS = [
  { label: 'Auto', value: 1 },
  { label: '1 min', value: 60 },
  { label: '5 min', value: 300 },
  { label: '1 hour', value: 3600 },
  { label: '1 day', value: 86400 },
]

interface CloudflareZoneInventory {
  zones: CFZone[]
  state: IntegrationState
}

/**
 * Accept the typed zone envelope while keeping the page readable against a
 * pre-envelope server during a rolling upgrade. A bare array is a successful
 * provider observation, so even an empty array is healthy; an object without
 * a valid canonical state is not promoted to healthy.
 */
function adaptCloudflareZoneInventory(payload: unknown): CloudflareZoneInventory {
  if (Array.isArray(payload)) {
    return {
      zones: payload as CFZone[],
      state: integrationStateFromObservation(true, true),
    }
  }

  if (payload && typeof payload === 'object') {
    const candidate = payload as { zones?: unknown; data?: unknown; state?: unknown }
    const rawZones = Array.isArray(candidate.zones)
      ? candidate.zones
      : Array.isArray(candidate.data)
        ? candidate.data
        : null
    const state = normalizeIntegrationState(candidate.state)
    if (rawZones && state) {
      return { zones: rawZones as CFZone[], state }
    }
  }

  return {
    zones: [],
    state: integrationStateFromObservation(true, false),
  }
}

function cloudflareErrorState(error: unknown): IntegrationState {
  return error instanceof ApiError && error.status === 503
    ? INTEGRATION_NOT_CONFIGURED
    : INTEGRATION_UNAVAILABLE
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function RecordTypeBadge({ type }: { type: string }) {
  const style = RECORD_TYPE_STYLES[type] ?? 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20'
  return (
    <Badge className={cn('text-xs font-mono border', style)}>
      {type}
    </Badge>
  )
}

function ProxyToggle({
  proxied,
  disabled,
  onToggle,
}: {
  proxied: boolean
  disabled: boolean
  onToggle: () => void
}) {
  return (
    <button
      onClick={onToggle}
      disabled={disabled}
      title={proxied ? 'Proxied — click to disable' : 'DNS only — click to enable proxy'}
      className={cn(
        'flex items-center gap-1.5 px-2 py-1 rounded-md border text-xs font-medium transition-all',
        proxied
          ? 'bg-orange-500/10 border-orange-500/20 text-orange-400 hover:bg-orange-500/20'
          : 'bg-zinc-800 border-zinc-700 text-zinc-500 hover:border-zinc-600 hover:text-zinc-300',
        disabled && 'opacity-50 cursor-not-allowed',
      )}
    >
      {proxied ? '☁️' : '⬜'}
      <span>{proxied ? 'Proxied' : 'DNS only'}</span>
    </button>
  )
}

// ─── Zone Status Badge ─────────────────────────────────────────────────────────

function ZoneStatusBadge({ status }: { status: string }) {
  const active = status === 'active'
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full border font-medium',
        active
          ? 'bg-green-500/10 border-green-500/20 text-green-400'
          : 'bg-yellow-500/10 border-yellow-500/20 text-yellow-400',
      )}
    >
      <span className={cn('w-1.5 h-1.5 rounded-full', active ? 'bg-green-400' : 'bg-yellow-400')} />
      {status}
    </span>
  )
}

// ─── Record Form Dialog ────────────────────────────────────────────────────────

interface RecordFormState {
  type: RecordType
  name: string
  content: string
  ttl: number
  proxied: boolean
  priority: string
}

const defaultForm = (): RecordFormState => ({
  type: 'A',
  name: '@',
  content: '',
  ttl: 1,
  proxied: false,
  priority: '10',
})

interface RecordDialogProps {
  open: boolean
  onClose: () => void
  onSave: (record: Omit<CFRecord, 'id'>) => void
  initial?: CFRecord | null
  isPending: boolean
}

function RecordDialog({ open, onClose, onSave, initial, isPending }: RecordDialogProps) {
  const [form, setForm] = useState<RecordFormState>(() =>
    initial
      ? {
          type: initial.type as RecordType,
          name: initial.name,
          content: initial.content,
          ttl: initial.ttl,
          proxied: initial.proxied,
          priority: String(initial.priority ?? 10),
        }
      : defaultForm(),
  )

  // Reset when dialog re-opens
  const prevOpen = useState(open)[0]
  if (open && !prevOpen) {
    // handled via key prop below
  }

  const showPriority = form.type === 'MX' || form.type === 'SRV'
  const showProxy = form.type === 'A' || form.type === 'AAAA' || form.type === 'CNAME'

  function field<K extends keyof RecordFormState>(key: K, value: RecordFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit() {
    if (!form.content.trim()) {
      toast.error('Content is required')
      return
    }
    const record: Omit<CFRecord, 'id'> = {
      type: form.type,
      name: form.name.trim() || '@',
      content: form.content.trim(),
      ttl: form.ttl,
      proxied: showProxy ? form.proxied : false,
      ...(showPriority ? { priority: parseInt(form.priority, 10) || 10 } : {}),
    }
    onSave(record)
  }

  const ttlLabel = TTL_OPTIONS.find((o) => o.value === form.ttl)?.label ?? 'Custom'

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit DNS Record' : 'Add DNS Record'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {/* Type + Name */}
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Type</label>
              <select
                value={form.type}
                onChange={(e) => {
                  const t = e.target.value as RecordType
                  setForm((prev) => ({
                    ...prev,
                    type: t,
                    proxied: t === 'A' || t === 'AAAA' || t === 'CNAME' ? prev.proxied : false,
                  }))
                }}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                {RECORD_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <div className="col-span-2 space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => field('name', e.target.value)}
                placeholder="@ or subdomain"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              />
            </div>
          </div>

          {/* Content */}
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Content</label>
            <input
              type="text"
              value={form.content}
              onChange={(e) => field('content', e.target.value)}
              placeholder={
                form.type === 'A'
                  ? '1.2.3.4'
                  : form.type === 'AAAA'
                  ? '2001:db8::1'
                  : form.type === 'CNAME'
                  ? 'target.example.com'
                  : form.type === 'MX'
                  ? 'mail.example.com'
                  : form.type === 'TXT'
                  ? '"v=spf1 ..."'
                  : 'value'
              }
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>

          {/* TTL + Priority row */}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">TTL</label>
              <select
                value={form.ttl}
                onChange={(e) => field('ttl', parseInt(e.target.value, 10))}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                {TTL_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            </div>

            {showPriority && (
              <div className="space-y-1.5">
                <label className="text-zinc-400 text-xs font-medium">Priority</label>
                <input
                  type="number"
                  value={form.priority}
                  onChange={(e) => field('priority', e.target.value)}
                  min={0}
                  className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
                />
              </div>
            )}
          </div>

          {/* Proxy toggle (A/AAAA/CNAME only) */}
          {showProxy && (
            <div className="flex items-center justify-between p-3 bg-zinc-800/50 rounded-lg border border-zinc-700">
              <div>
                <p className="text-sm text-zinc-200 font-medium">Proxy status</p>
                <p className="text-xs text-zinc-500 mt-0.5">
                  {form.proxied
                    ? 'Traffic routed through Cloudflare'
                    : 'DNS only — traffic goes directly to origin'}
                </p>
              </div>
              <button
                type="button"
                onClick={() => field('proxied', !form.proxied)}
                className={cn(
                  'relative w-11 h-6 rounded-full transition-colors flex-shrink-0',
                  form.proxied ? 'bg-orange-500' : 'bg-zinc-700',
                )}
                title={ttlLabel}
              >
                <span
                  className={cn(
                    'absolute top-1 w-4 h-4 bg-white rounded-full shadow transition-transform',
                    form.proxied ? 'translate-x-6' : 'translate-x-1',
                  )}
                />
              </button>
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
            {initial ? 'Save Changes' : 'Add Record'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Confirm Dialog ────────────────────────────────────────────────────────────

interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  confirmClass?: string
  onConfirm: () => void
  onClose: () => void
  isPending: boolean
}

function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = 'Confirm',
  confirmClass = 'bg-red-600 hover:bg-red-500 text-white',
  onConfirm,
  onClose,
  isPending,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-red-400" />
            {title}
          </DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm py-2">{description}</p>
        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button onClick={onConfirm} disabled={isPending} className={confirmClass}>
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CloudflareLoadError({ title, error, retry, retrying }: { title: string; error: Error; retry: () => void; retrying: boolean }) {
  const state = integrationStatePresentation(cloudflareErrorState(error))
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <AlertTriangle className="size-6 text-red-400" />
      <div>
        <p className="text-sm text-red-300">{title}</p>
        <p data-testid="cloudflare-error-state" className="mt-1 text-xs font-medium text-red-300">{state.label}</p>
        <p className="mt-1 break-words font-mono text-xs text-red-400/70">{error.message}</p>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={retry} disabled={retrying}>
        <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} /> Retry
      </Button>
    </div>
  )
}

// ─── Email Routing Panel ───────────────────────────────────────────────────────

export function EmailRoutingPanel({ zoneId }: { zoneId: string }) {
  const routingQuery = useQuery<CFEmailRouting>({
    queryKey: ['cf-email-routing', zoneId],
    queryFn: () => api.get<CFEmailRouting>(`/cloudflare/zones/${zoneId}/email-routing`),
    enabled: !!zoneId,
  })
  const data = routingQuery.data

  if (routingQuery.isLoading) {
    return (
      <div className="space-y-2 py-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full bg-zinc-800/60 rounded-lg" />
        ))}
      </div>
    )
  }

  if (routingQuery.isError) {
    return <CloudflareLoadError title="Cloudflare email routing could not be loaded." error={routingQuery.error} retry={() => { void routingQuery.refetch() }} retrying={routingQuery.isFetching} />
  }

  if (!data) {
    return <div className="py-8 text-center text-sm text-zinc-600">Email routing information is not available.</div>
  }

  return (
    <div className="space-y-4">
      {/* Status row */}
      <div className="flex items-center gap-3 p-3 bg-zinc-800/40 rounded-lg border border-zinc-700/50">
        <Mail className="w-4 h-4 text-zinc-400 flex-shrink-0" />
        <div className="flex-1">
          <p className="text-sm text-zinc-200 font-medium">Email Routing</p>
          {data.name && <p className="text-xs text-zinc-500 mt-0.5">{data.name}</p>}
        </div>
        <span
          className={cn(
            'inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full border font-medium',
            data.enabled
              ? 'bg-green-500/10 border-green-500/20 text-green-400'
              : 'bg-zinc-700/40 border-zinc-600/30 text-zinc-500',
          )}
        >
          <span className={cn('w-1.5 h-1.5 rounded-full', data.enabled ? 'bg-green-400' : 'bg-zinc-600')} />
          {data.enabled ? 'Enabled' : 'Disabled'}
        </span>
      </div>

      {/* Rules table */}
      {data.rules && data.rules.length > 0 ? (
        <div className="overflow-auto rounded-lg border border-zinc-800">
          <Table>
            <TableHeader>
              <TableRow className="border-zinc-800 hover:bg-transparent">
                <TableHead className="text-zinc-500 font-medium text-xs">Matcher</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-20 text-center">Status</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.rules.map((rule, idx) => {
                const matcher = rule.matchers[0]
                const action = rule.actions[0]
                const matcherStr = matcher
                  ? matcher.value ?? matcher.field ?? matcher.type
                  : '—'
                const actionStr = action
                  ? (action.value?.join(', ') ?? action.type)
                  : '—'
                return (
                  <TableRow key={rule.tag ?? idx} className="border-zinc-800 hover:bg-zinc-800/50">
                    <TableCell className="font-mono text-xs text-zinc-300 py-3">
                      {matcherStr}
                    </TableCell>
                    <TableCell className="py-3 text-center">
                      <span
                        className={cn(
                          'inline-flex items-center gap-1 text-[11px] px-1.5 py-0.5 rounded-full border',
                          rule.enabled
                            ? 'bg-green-500/10 border-green-500/20 text-green-400'
                            : 'bg-zinc-700/40 border-zinc-600/30 text-zinc-500',
                        )}
                      >
                        {rule.enabled ? 'on' : 'off'}
                      </span>
                    </TableCell>
                    <TableCell className="py-3">
                      <div className="flex items-center gap-1.5 text-xs text-zinc-400">
                        <ArrowRight className="w-3 h-3 text-zinc-600 flex-shrink-0" />
                        <span className="font-mono">{actionStr}</span>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      ) : (
        <div className="flex flex-col items-center justify-center py-10 text-zinc-600">
          <List className="w-8 h-8 mb-2 opacity-30" />
          <p className="text-sm">No routing rules configured</p>
        </div>
      )}
    </div>
  )
}

// ─── Records Table ─────────────────────────────────────────────────────────────

interface RecordsTableProps {
  zoneId: string
}

export function RecordsTable({ zoneId }: RecordsTableProps) {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [typeFilter, setTypeFilter] = useState<string>('All')
  const [addOpen, setAddOpen] = useState(false)
  const [editRecord, setEditRecord] = useState<CFRecord | null>(null)
  const [deleteRecord, setDeleteRecord] = useState<CFRecord | null>(null)

  const recordsQuery = useQuery<CFRecord[]>({
    queryKey: ['cf-records', zoneId],
    queryFn: () => api.get<CFRecord[]>(`/cloudflare/zones/${zoneId}/records`),
    enabled: !!zoneId,
  })
  const records = recordsQuery.data ?? []

  const addMutation = useMutation({
    mutationFn: (record: Omit<CFRecord, 'id'>) =>
      api.post(`/cloudflare/zones/${zoneId}/records`, record),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cf-records', zoneId] })
      setAddOpen(false)
      toast.success('Record added')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to add record'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, ...record }: CFRecord) =>
      api.put(`/cloudflare/zones/${zoneId}/records/${id}`, record),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cf-records', zoneId] })
      setEditRecord(null)
      toast.success('Record updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update record'),
  })

  const deleteMutation = useMutation({
    mutationFn: (rid: string) =>
      api.delete(`/cloudflare/zones/${zoneId}/records/${rid}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cf-records', zoneId] })
      setDeleteRecord(null)
      toast.success('Record deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete record'),
  })

  const proxyMutation = useMutation({
    mutationFn: ({ id, proxied }: { id: string; proxied: boolean }) =>
      api.put(`/cloudflare/zones/${zoneId}/records/${id}/proxy`, { proxied }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cf-records', zoneId] })
      toast.success('Proxy status updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to toggle proxy'),
  })

  const proxyEligible = (type: string) =>
    type === 'A' || type === 'AAAA' || type === 'CNAME'

  const filtered = records.filter((r) => {
    const matchType = typeFilter === 'All' || r.type === typeFilter
    const matchSearch =
      !search || r.name.toLowerCase().includes(search.toLowerCase())
    return matchType && matchSearch
  })

  if (recordsQuery.isError) {
    return <CloudflareLoadError title="Cloudflare DNS records could not be loaded. Record controls are paused." error={recordsQuery.error} retry={() => { void recordsQuery.refetch() }} retrying={recordsQuery.isFetching} />
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3 flex-wrap">
        {/* Search */}
        <div className="relative flex-1 min-w-48">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search by name..."
            className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
          />
        </div>

        {/* Type filter */}
        <div className="relative">
          <select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            className="appearance-none bg-zinc-800 border border-zinc-700 text-zinc-300 rounded-md pl-3 pr-8 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors cursor-pointer"
          >
            <option value="All">All types</option>
            {RECORD_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
        </div>

        {/* Add record */}
        <Button
          size="sm"
          onClick={() => setAddOpen(true)}
          className="bg-blue-600 hover:bg-blue-500 text-white"
        >
          <Plus className="w-3.5 h-3.5 mr-1.5" />
          Add Record
        </Button>
      </div>

      {/* Table */}
      {recordsQuery.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full bg-zinc-800/60 rounded-lg" />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
          <Cloud className="w-10 h-10 mb-3 opacity-30" />
          <p className="text-sm">
            {records.length === 0 ? 'No records in this zone' : 'No records match your filter'}
          </p>
        </div>
      ) : (
        <div className="overflow-auto rounded-lg border border-zinc-800">
          <Table>
            <TableHeader>
              <TableRow className="border-zinc-800 hover:bg-transparent">
                <TableHead className="text-zinc-500 font-medium text-xs w-20">Type</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs">Name</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs">Content</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-20">TTL</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-32">Proxy</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-20 text-right">
                  Actions
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((record) => (
                <TableRow
                  key={record.id}
                  className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                >
                  <TableCell className="py-3">
                    <RecordTypeBadge type={record.type} />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-zinc-300 py-3">
                    {record.name}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-zinc-300 py-3 max-w-xs">
                    <div className="flex items-center gap-2">
                      {record.priority !== undefined && (
                        <span className="text-zinc-600 text-[11px]">{record.priority}</span>
                      )}
                      <span className="truncate block max-w-xs" title={record.content}>
                        {record.content}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-zinc-500 py-3">
                    {record.ttl === 1 ? 'Auto' : record.ttl}
                  </TableCell>
                  <TableCell className="py-3">
                    {proxyEligible(record.type) ? (
                      <ProxyToggle
                        proxied={record.proxied}
                        disabled={proxyMutation.isPending}
                        onToggle={() =>
                          proxyMutation.mutate({ id: record.id, proxied: !record.proxied })
                        }
                      />
                    ) : (
                      <span className="text-zinc-700 text-xs">—</span>
                    )}
                  </TableCell>
                  <TableCell className="py-3 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => setEditRecord(record)}
                        className="text-zinc-500 hover:text-blue-400 p-1 rounded transition-colors"
                        title="Edit"
                      >
                        <Edit2 className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={() => setDeleteRecord(record)}
                        className="text-zinc-500 hover:text-red-400 p-1 rounded transition-colors"
                        title="Delete"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {/* Dialogs */}
      <RecordDialog
        key={addOpen ? 'add-open' : 'add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSave={(r) => addMutation.mutate(r)}
        isPending={addMutation.isPending}
      />

      <RecordDialog
        key={editRecord?.id ?? 'edit-none'}
        open={editRecord !== null}
        onClose={() => setEditRecord(null)}
        onSave={(r) => editRecord && updateMutation.mutate({ ...r, id: editRecord.id })}
        initial={editRecord}
        isPending={updateMutation.isPending}
      />

      <ConfirmDialog
        open={deleteRecord !== null}
        title="Delete DNS Record"
        description={
          deleteRecord
            ? `Are you sure you want to delete the ${deleteRecord.type} record "${deleteRecord.name}"? This cannot be undone.`
            : ''
        }
        confirmLabel="Delete"
        onConfirm={() => deleteRecord && deleteMutation.mutate(deleteRecord.id)}
        onClose={() => setDeleteRecord(null)}
        isPending={deleteMutation.isPending}
      />
    </div>
  )
}

// ─── Main Page ─────────────────────────────────────────────────────────────────

type ZoneTab = 'dns' | 'email-routing'

export default function Cloudflare() {
  const queryClient = useQueryClient()
  const [requestedZoneId, setRequestedZoneId] = useState<string | null>(null)
  const [purgeConfirmOpen, setPurgeConfirmOpen] = useState(false)
  const [mailAutofixOpen, setMailAutofixOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<ZoneTab>('dns')

  const zonesQuery = useQuery<CloudflareZoneInventory>({
    queryKey: ['cf-zones'],
    queryFn: async () => adaptCloudflareZoneInventory(await api.get<unknown>('/cloudflare/zones')),
  })
  const zones = zonesQuery.data?.zones ?? []
  const zoneAvailabilityState = zonesQuery.isError
    ? cloudflareErrorState(zonesQuery.error)
    : zonesQuery.data?.state ?? null
  const zoneAvailability = zoneAvailabilityState
    ? integrationStatePresentation(zoneAvailabilityState)
    : null
  const zoneErrorIsNotConfigured = zoneAvailabilityState === INTEGRATION_NOT_CONFIGURED
  const selectedZoneId = zones.some(zone => zone.id === requestedZoneId)
    ? requestedZoneId
    : zones[0]?.id ?? null

  const selectedZone = zones.find((z) => z.id === selectedZoneId) ?? null

  const purgeMutation = useMutation({
    mutationFn: (zoneId: string) =>
      api.post(`/cloudflare/zones/${zoneId}/purge`),
    onSuccess: () => {
      setPurgeConfirmOpen(false)
      toast.success('Cache purged successfully')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to purge cache'),
  })

  const mailAutofixMutation = useMutation({
    mutationFn: (domain: string) =>
      api.post(`/cloudflare/mail-autofix/${domain}`),
    onSuccess: () => {
      setMailAutofixOpen(false)
      toast.success('Mail DNS records fixed successfully')
      // Refresh DNS records so changes appear immediately
      if (selectedZoneId) {
        queryClient.invalidateQueries({ queryKey: ['cf-records', selectedZoneId] })
      }
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to fix mail DNS records'),
  })

  return (
    <div className="space-y-5">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 bg-orange-500/10 border border-orange-500/20 rounded-lg flex items-center justify-center">
            <Cloud className="w-4.5 h-4.5 text-orange-400" />
          </div>
          <div>
            <h2 className="text-white text-xl font-bold">Cloudflare DNS</h2>
            <p className="text-zinc-500 text-sm mt-0.5">Manage DNS records across your zones</p>
          </div>
          {zoneAvailability && (
            <span
              data-testid="cloudflare-availability-state"
              className={cn(
                'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium',
                zoneAvailability.tone === 'healthy'
                  ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400'
                  : zoneAvailability.tone === 'neutral'
                    ? 'border-zinc-700 bg-zinc-800/60 text-zinc-400'
                    : 'border-red-500/20 bg-red-500/10 text-red-400',
              )}
            >
              <span className={cn(
                'size-1.5 rounded-full',
                zoneAvailability.tone === 'healthy'
                  ? 'bg-emerald-400'
                  : zoneAvailability.tone === 'neutral'
                    ? 'bg-zinc-500'
                    : 'bg-red-400',
              )} />
              {zoneAvailability.label}
            </span>
          )}
        </div>

        {selectedZone && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setMailAutofixOpen(true)}
              className="border-emerald-700/50 text-emerald-400 hover:text-emerald-300 hover:bg-emerald-500/10 hover:border-emerald-600/50"
            >
              <Wrench className="w-3.5 h-3.5 mr-1.5" />
              Fix Mail DNS
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPurgeConfirmOpen(true)}
              className="border-red-700/50 text-red-400 hover:text-red-300 hover:bg-red-500/10 hover:border-red-600/50"
            >
              <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
              Purge Cache
            </Button>
          </div>
        )}
      </div>

      {/* Zone selector + info bar */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-4">
          {zonesQuery.isLoading ? (
            <div className="flex items-center gap-3">
              <Skeleton className="h-9 w-64 bg-zinc-800" />
              <Skeleton className="h-6 w-32 bg-zinc-800" />
            </div>
          ) : zonesQuery.isError ? (
            <DependencyRemediation
              title={zoneErrorIsNotConfigured
                ? 'Cloudflare is not configured'
                : 'Cloudflare is unavailable'}
              summary={zoneErrorIsNotConfigured
                ? 'Add a least-privileged API token before HServer enables zone controls.'
                : 'HServer could not reach Cloudflare or the configured token cannot access the requested zones. Zone controls remain paused.'}
              state={zoneErrorIsNotConfigured ? 'not-configured' : 'unavailable'}
              steps={zoneErrorIsNotConfigured
                ? [
                    <>Create a scoped token with <strong>Zone Read</strong> and <strong>DNS Edit</strong> only for the intended zones.</>,
                    <>Set <code>HSERVER_CF_API_TOKEN</code> in <code>/etc/hserver/hserver.env</code>.</>,
                    <>Restart HServer, then retry detection. HServer will not create or broaden provider credentials.</>,
                  ]
                : [
                    <>Verify outbound HTTPS and DNS connectivity from the HServer host.</>,
                    <>Check the token scope and zone access without printing the token.</>,
                    <>Review HServer logs for the provider response, then retry detection.</>,
                  ]}
              error={zonesQuery.error.message}
              retry={() => { void zonesQuery.refetch() }}
              retrying={zonesQuery.isFetching}
            />
          ) : zones.length === 0 ? (
            <div className="flex items-center gap-2 text-zinc-500 text-sm">
              <Cloud className="w-4 h-4" />
              <span>No Cloudflare zones found.</span>
            </div>
          ) : (
            <div className="flex items-center gap-4 flex-wrap">
              {/* Dropdown */}
              <div className="relative">
                <select
                  value={selectedZoneId ?? ''}
                  onChange={(e) => {
                    setRequestedZoneId(e.target.value)
                    setActiveTab('dns')
                  }}
                  className="appearance-none bg-zinc-800 border border-zinc-700 text-white rounded-lg pl-10 pr-10 py-2.5 text-sm font-medium focus:outline-none focus:border-blue-500 transition-colors cursor-pointer min-w-56"
                >
                  {zones.map((z) => (
                    <option key={z.id} value={z.id}>
                      {z.name}
                    </option>
                  ))}
                </select>
                <Server className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
                <ChevronDown className="absolute right-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
              </div>

              {/* Zone meta */}
              {selectedZone && (
                <div className="flex items-center gap-3 flex-wrap">
                  <ZoneStatusBadge status={selectedZone.status} />

                  <span className="text-zinc-600 text-xs">
                    Plan:{' '}
                    <span className="text-zinc-400 capitalize">{selectedZone.plan.name}</span>
                  </span>

                  {selectedZone.name_servers.length > 0 && (
                    <span className="text-zinc-600 text-xs">
                      NS:{' '}
                      <span className="font-mono text-zinc-400">
                        {selectedZone.name_servers.join(', ')}
                      </span>
                    </span>
                  )}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Zone content with tabs */}
      {selectedZoneId && (
        <Card className="bg-zinc-900 border-zinc-800">
          {/* Tab bar */}
          <div className="flex items-center gap-1 px-5 pt-4 border-b border-zinc-800">
            <button
              onClick={() => setActiveTab('dns')}
              className={cn(
                'flex items-center gap-1.5 px-3 py-2 text-sm font-medium rounded-t-md border-b-2 transition-colors -mb-px',
                activeTab === 'dns'
                  ? 'border-blue-500 text-blue-400'
                  : 'border-transparent text-zinc-500 hover:text-zinc-300',
              )}
            >
              <Server className="w-3.5 h-3.5" />
              DNS Records
            </button>
            <button
              onClick={() => setActiveTab('email-routing')}
              className={cn(
                'flex items-center gap-1.5 px-3 py-2 text-sm font-medium rounded-t-md border-b-2 transition-colors -mb-px',
                activeTab === 'email-routing'
                  ? 'border-blue-500 text-blue-400'
                  : 'border-transparent text-zinc-500 hover:text-zinc-300',
              )}
            >
              <Mail className="w-3.5 h-3.5" />
              Email Routing
            </button>
          </div>

          <CardContent className="p-5">
            {activeTab === 'dns' && (
              <RecordsTable key={selectedZoneId} zoneId={selectedZoneId} />
            )}
            {activeTab === 'email-routing' && (
              <EmailRoutingPanel key={selectedZoneId} zoneId={selectedZoneId} />
            )}
          </CardContent>
        </Card>
      )}

      {/* Purge cache confirm */}
      <ConfirmDialog
        open={purgeConfirmOpen}
        title="Purge Cache"
        description={
          selectedZone
            ? `This will purge all cached files for "${selectedZone.name}". All visitors will receive fresh content from the origin server. Are you sure?`
            : ''
        }
        confirmLabel="Purge Cache"
        confirmClass="bg-red-600 hover:bg-red-500 text-white"
        onConfirm={() => selectedZoneId && purgeMutation.mutate(selectedZoneId)}
        onClose={() => setPurgeConfirmOpen(false)}
        isPending={purgeMutation.isPending}
      />

      {/* Mail Autofix confirm */}
      <Dialog open={mailAutofixOpen} onOpenChange={(o) => { if (!o) setMailAutofixOpen(false) }}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
          <DialogHeader>
            <DialogTitle className="text-white flex items-center gap-2">
              <Wrench className="w-4 h-4 text-emerald-400" />
              Fix Mail DNS
            </DialogTitle>
          </DialogHeader>
          <p className="text-zinc-400 text-sm py-2">
            This will automatically configure{' '}
            <span className="text-zinc-200 font-medium">MX, SPF, DKIM, and DMARC</span> records
            {selectedZone ? (
              <>
                {' '}for{' '}
                <span className="text-white font-mono font-medium">{selectedZone.name}</span>
              </>
            ) : ''}.
            Existing records may be overwritten. Continue?
          </p>
          <DialogFooter className="gap-2">
            <Button
              variant="ghost"
              onClick={() => setMailAutofixOpen(false)}
              className="text-zinc-400 hover:text-white hover:bg-zinc-800"
            >
              Cancel
            </Button>
            <Button
              onClick={() => selectedZone && mailAutofixMutation.mutate(selectedZone.name)}
              disabled={mailAutofixMutation.isPending}
              className="bg-emerald-600 hover:bg-emerald-500 text-white"
            >
              {mailAutofixMutation.isPending && (
                <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />
              )}
              Fix Records
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

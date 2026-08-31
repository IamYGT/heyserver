import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Globe2,
  Plus,
  Trash2,
  RefreshCw,
  Loader2,
  ChevronRight,
  Server,
  CheckCircle2,
  XCircle,
  Edit2,
  X,
  AlertTriangle,
  Info,
  Search,
  ChevronDown,
  ChevronUp,
  Download,
  ShieldCheck,
  ExternalLink,
  Hash,
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
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { DNSZone, DNSRecord, DNSStatus } from '@/lib/types'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ─── Constants ─────────────────────────────────────────────────────────────────

const RECORD_TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV', 'CAA'] as const
type RecordType = (typeof RECORD_TYPES)[number]

const RECORD_TYPE_STYLES: Record<string, string> = {
  A: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
  AAAA: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
  CNAME: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
  MX: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
  TXT: 'bg-green-500/10 text-green-400 border-green-500/20',
  NS: 'bg-cyan-500/10 text-cyan-400 border-cyan-500/20',
  SRV: 'bg-pink-500/10 text-pink-400 border-pink-500/20',
  CAA: 'bg-red-500/10 text-red-400 border-red-500/20',
}

const TTL_OPTIONS = [
  { label: 'Auto (300)', value: 300 },
  { label: '1 minute', value: 60 },
  { label: '5 minutes', value: 300 },
  { label: '30 minutes', value: 1800 },
  { label: '1 hour', value: 3600 },
  { label: '12 hours', value: 43200 },
  { label: '1 day', value: 86400 },
  { label: '1 week', value: 604800 },
]

function ttlLabel(seconds: number): string {
  const opt = TTL_OPTIONS.find((o) => o.value === seconds)
  if (opt) {
    // Return short label
    if (seconds < 60) return `${seconds}s`
    if (seconds === 60) return '1 min'
    if (seconds === 300) return '5 min'
    if (seconds === 1800) return '30 min'
    if (seconds === 3600) return '1 hr'
    if (seconds === 43200) return '12 hr'
    if (seconds === 86400) return '1 day'
    if (seconds === 604800) return '1 week'
  }
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`
  return `${Math.round(seconds / 86400)}d`
}

const VALUE_PLACEHOLDERS: Partial<Record<RecordType, string>> = {
  A: '1.2.3.4',
  AAAA: '2001:db8::1',
  CNAME: 'target.example.com',
  MX: 'mail.example.com',
  TXT: '"v=spf1 include:example.com ~all"',
  NS: 'ns1.example.com',
  SRV: '10 20 5060 sip.example.com',
  CAA: '0 issue "letsencrypt.org"',
}

// ─── SOA types ─────────────────────────────────────────────────────────────────

interface SOARecord {
  primaryNs: string
  hostmaster: string
  serial: number
  refresh: number
  retry: number
  expire: number
  minimum: number
}

// ─── DNS Lookup types ───────────────────────────────────────────────────────────

interface LookupResult {
  resolver: string
  answers: string[]
  error?: string
}

interface LookupResponse {
  domain: string
  type: string
  results: LookupResult[]
}

// ─── Sub-components ─────────────────────────────────────────────────────────────

function RecordTypeBadge({ type }: { type: string }) {
  const style = RECORD_TYPE_STYLES[type] ?? 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20'
  return (
    <Badge className={cn('text-xs font-mono border px-1.5 py-0.5', style)}>
      {type}
    </Badge>
  )
}

// ─── Record Form Dialog ─────────────────────────────────────────────────────────

interface RecordFormState {
  name: string
  ttl: number
  type: RecordType
  value: string
  priority: string
}

const defaultRecordForm = (): RecordFormState => ({
  name: '@',
  ttl: 3600,
  type: 'A',
  value: '',
  priority: '10',
})

interface RecordDialogProps {
  open: boolean
  onClose: () => void
  onSave: (record: DNSRecord) => void
  initial?: DNSRecord | null
  isPending: boolean
}

function RecordDialog({ open, onClose, onSave, initial, isPending }: RecordDialogProps) {
  const [form, setForm] = useState<RecordFormState>(() =>
    initial
      ? {
          name: initial.name,
          ttl: initial.ttl,
          type: initial.type as RecordType,
          value: initial.value,
          priority: String(initial.priority ?? 10),
        }
      : defaultRecordForm(),
  )

  const showPriority = form.type === 'MX' || form.type === 'SRV'

  function field<K extends keyof RecordFormState>(key: K, value: RecordFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit() {
    if (!form.value.trim()) {
      toast.error('Value is required')
      return
    }
    const record: DNSRecord = {
      name: form.name.trim() || '@',
      ttl: form.ttl,
      type: form.type,
      value: form.value.trim(),
      ...(showPriority ? { priority: parseInt(form.priority, 10) || 10 } : {}),
    }
    onSave(record)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
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
                onChange={(e) => field('type', e.target.value as RecordType)}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                {RECORD_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
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

          {/* Value */}
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Value</label>
            <input
              type="text"
              value={form.value}
              onChange={(e) => field('value', e.target.value)}
              placeholder={VALUE_PLACEHOLDERS[form.type] ?? 'value'}
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>

          {/* TTL + Priority */}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">TTL</label>
              <select
                value={form.ttl}
                onChange={(e) => field('ttl', parseInt(e.target.value, 10))}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                {TTL_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
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
        </div>

        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white hover:bg-zinc-800">
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={isPending} className="bg-blue-600 hover:bg-blue-500 text-white">
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            {initial ? 'Save Changes' : 'Add Record'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Add Zone Dialog ────────────────────────────────────────────────────────────

interface AddZoneDialogProps {
  open: boolean
  onClose: () => void
  onSave: (domain: string, ip: string) => void
  isPending: boolean
}

function AddZoneDialog({ open, onClose, onSave, isPending }: AddZoneDialogProps) {
  const [domain, setDomain] = useState('')
  const [ip, setIp] = useState('')

  function handleSubmit() {
    if (!domain.trim()) { toast.error('Domain is required'); return }
    if (!ip.trim()) { toast.error('IP address is required'); return }
    onSave(domain.trim(), ip.trim())
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="text-white">Create DNS Zone</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Domain Name</label>
            <input
              type="text"
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              placeholder="example.com"
              autoFocus
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Server IP</label>
            <input
              type="text"
              value={ip}
              onChange={(e) => setIp(e.target.value)}
              placeholder="1.2.3.4"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>
          <p className="text-zinc-600 text-xs">
            A zone file with SOA, NS, and A records will be created automatically.
          </p>
        </div>
        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white hover:bg-zinc-800">
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={isPending} className="bg-blue-600 hover:bg-blue-500 text-white">
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Create Zone
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Confirm Delete Dialog ──────────────────────────────────────────────────────

interface ConfirmDeleteDialogProps {
  open: boolean
  title: string
  description: string
  onConfirm: () => void
  onClose: () => void
  isPending: boolean
}

function ConfirmDeleteDialog({ open, title, description, onConfirm, onClose, isPending }: ConfirmDeleteDialogProps) {
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
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white hover:bg-zinc-800">
            Cancel
          </Button>
          <Button onClick={onConfirm} disabled={isPending} className="bg-red-600 hover:bg-red-500 text-white">
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── SOA Editor Dialog ──────────────────────────────────────────────────────────

interface SOAEditorDialogProps {
  open: boolean
  onClose: () => void
  domain: string
  initial: SOARecord
}

function SOAEditorDialog({ open, onClose, domain, initial }: SOAEditorDialogProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<Omit<SOARecord, 'serial'>>(initial)

  function field(key: keyof Omit<SOARecord, 'serial'>, value: string | number) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const mutation = useMutation({
    mutationFn: () => api.put(`/dns/zones/${domain}/soa`, form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zone', domain] })
      onClose()
      toast.success('SOA record updated — zone reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update SOA record'),
  })

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Edit SOA Record — {domain}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Primary Nameserver</label>
              <input
                type="text"
                value={form.primaryNs}
                onChange={(e) => field('primaryNs', e.target.value)}
                placeholder="ns1.example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Hostmaster Email</label>
              <input
                type="text"
                value={form.hostmaster}
                onChange={(e) => field('hostmaster', e.target.value)}
                placeholder="hostmaster.example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
              />
            </div>
          </div>

          <div className="p-3 bg-zinc-800/50 rounded-lg border border-zinc-700/50 flex items-center gap-2">
            <Hash className="w-3.5 h-3.5 text-zinc-500" />
            <span className="text-xs text-zinc-500">Serial:</span>
            <span className="text-xs font-mono text-zinc-300">{initial.serial}</span>
            <span className="text-xs text-zinc-600 ml-1">(auto-incremented on save)</span>
          </div>

          <div className="grid grid-cols-2 gap-3">
            {(
              [
                { key: 'refresh', label: 'Refresh (s)', hint: 'How often secondaries poll' },
                { key: 'retry', label: 'Retry (s)', hint: 'Failed transfer retry interval' },
                { key: 'expire', label: 'Expire (s)', hint: 'Zone data validity timeout' },
                { key: 'minimum', label: 'Minimum TTL (s)', hint: 'Negative caching TTL' },
              ] as const
            ).map(({ key, label, hint }) => (
              <div key={key} className="space-y-1.5">
                <label className="text-zinc-400 text-xs font-medium">{label}</label>
                <input
                  type="number"
                  value={form[key]}
                  onChange={(e) => field(key, parseInt(e.target.value, 10) || 0)}
                  min={0}
                  className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
                />
                <p className="text-zinc-600 text-[11px]">{hint}</p>
              </div>
            ))}
          </div>
        </div>

        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white hover:bg-zinc-800">
            Cancel
          </Button>
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending} className="bg-blue-600 hover:bg-blue-500 text-white">
            {mutation.isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Save SOA
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── SOA Section (collapsible) ──────────────────────────────────────────────────

interface SOASectionProps {
  domain: string
  soa?: SOARecord
  managementReady: boolean
}

function SOASection({ domain, soa, managementReady }: SOASectionProps) {
  const [open, setOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)

  const defaultSoa: SOARecord = {
    primaryNs: '',
    hostmaster: '',
    serial: 0,
    refresh: 3600,
    retry: 900,
    expire: 604800,
    minimum: 300,
  }

  const data = soa ?? defaultSoa

  return (
    <div className="border border-zinc-800 rounded-lg overflow-hidden">
      <button
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center justify-between px-4 py-3 hover:bg-zinc-800/40 transition-colors text-left"
      >
        <div className="flex items-center gap-2">
          <Badge className="bg-zinc-500/10 text-zinc-400 border-zinc-500/20 border text-xs font-mono px-1.5 py-0.5">
            SOA
          </Badge>
          <span className="text-zinc-300 text-sm font-medium">SOA Record</span>
          {soa && (
            <span className="text-zinc-600 text-xs font-mono">Serial: {soa.serial}</span>
          )}
        </div>
        {open ? (
          <ChevronUp className="w-4 h-4 text-zinc-500" />
        ) : (
          <ChevronDown className="w-4 h-4 text-zinc-500" />
        )}
      </button>

      {open && (
        <div className="px-4 pb-4 border-t border-zinc-800 pt-3 space-y-3">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            {[
              { label: 'Primary NS', value: data.primaryNs || '—' },
              { label: 'Hostmaster', value: data.hostmaster || '—' },
              { label: 'Refresh', value: `${data.refresh}s` },
              { label: 'Retry', value: `${data.retry}s` },
              { label: 'Expire', value: `${data.expire}s` },
              { label: 'Min TTL', value: `${data.minimum}s` },
            ].map(({ label, value }) => (
              <div key={label} className="space-y-0.5">
                <p className="text-zinc-600 text-[11px] uppercase tracking-wide">{label}</p>
                <p className="text-zinc-300 text-xs font-mono truncate" title={value}>{value}</p>
              </div>
            ))}
          </div>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setEditOpen(true)}
            disabled={!managementReady}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 hover:border-zinc-600"
          >
            <Edit2 className="w-3.5 h-3.5 mr-1.5" />
            Edit SOA
          </Button>
        </div>
      )}

      {editOpen && (
        <SOAEditorDialog
          open={editOpen}
          onClose={() => setEditOpen(false)}
          domain={domain}
          initial={data}
        />
      )}
    </div>
  )
}

// ─── DNS Lookup Section (collapsible) ──────────────────────────────────────────

const LOOKUP_TYPES = ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS'] as const

function DNSLookupSection() {
  const [open, setOpen] = useState(false)
  const [domain, setDomain] = useState('')
  const [type, setType] = useState<string>('A')
  const [results, setResults] = useState<LookupResponse | null>(null)
  const [loading, setLoading] = useState(false)

  async function handleLookup() {
    if (!domain.trim()) { toast.error('Domain is required'); return }
    setLoading(true)
    setResults(null)
    try {
      const data = await api.get<LookupResponse>(`/dns/lookup?domain=${encodeURIComponent(domain.trim())}&type=${type}`)
      setResults(data)
    } catch {
      toast.error('DNS lookup failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="border border-zinc-800 rounded-lg overflow-hidden">
      <button
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center justify-between px-4 py-3 hover:bg-zinc-800/40 transition-colors text-left"
      >
        <div className="flex items-center gap-2">
          <ExternalLink className="w-3.5 h-3.5 text-zinc-500" />
          <span className="text-zinc-300 text-sm font-medium">DNS Propagation Lookup</span>
          <span className="text-zinc-600 text-xs">Check records across multiple resolvers</span>
        </div>
        {open ? (
          <ChevronUp className="w-4 h-4 text-zinc-500" />
        ) : (
          <ChevronDown className="w-4 h-4 text-zinc-500" />
        )}
      </button>

      {open && (
        <div className="px-4 pb-4 border-t border-zinc-800 pt-3 space-y-4">
          {/* Input row */}
          <div className="flex items-center gap-2 flex-wrap">
            <div className="relative flex-1 min-w-48">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
              <input
                type="text"
                value={domain}
                onChange={(e) => setDomain(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleLookup()}
                placeholder="example.com or subdomain.example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
              />
            </div>
            <div className="relative">
              <select
                value={type}
                onChange={(e) => setType(e.target.value)}
                className="appearance-none bg-zinc-800 border border-zinc-700 text-zinc-300 rounded-md pl-3 pr-8 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors cursor-pointer"
              >
                {LOOKUP_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
              <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
            </div>
            <Button
              onClick={handleLookup}
              disabled={loading}
              size="sm"
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {loading ? <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" /> : <Search className="w-3.5 h-3.5 mr-1.5" />}
              Lookup
            </Button>
          </div>

          {/* Results */}
          {results && (
            <div className="space-y-2">
              <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide">
                Results for <span className="font-mono text-zinc-300">{results.domain}</span> — {results.type}
              </p>
              <div className="overflow-auto rounded-lg border border-zinc-800">
                <Table>
                  <TableHeader>
                    <TableRow className="border-zinc-800 hover:bg-transparent">
                      <TableHead className="text-zinc-500 font-medium text-xs w-36">Resolver</TableHead>
                      <TableHead className="text-zinc-500 font-medium text-xs">Answers</TableHead>
                      <TableHead className="text-zinc-500 font-medium text-xs w-24">Status</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {results.results.map((r) => (
                      <TableRow key={r.resolver} className="border-zinc-800 hover:bg-zinc-800/40">
                        <TableCell className="text-xs font-mono text-zinc-400 py-2.5">{r.resolver}</TableCell>
                        <TableCell className="py-2.5">
                          {r.error ? (
                            <span className="text-red-400 text-xs">{r.error}</span>
                          ) : r.answers.length === 0 ? (
                            <span className="text-zinc-600 text-xs">No records</span>
                          ) : (
                            <div className="space-y-0.5">
                              {r.answers.map((a, i) => (
                                <p key={i} className="text-xs font-mono text-zinc-300">{a}</p>
                              ))}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="py-2.5">
                          {r.error ? (
                            <Badge className="bg-red-500/10 text-red-400 border-red-500/20 border text-xs">Error</Badge>
                          ) : r.answers.length > 0 ? (
                            <Badge className="bg-green-500/10 text-green-400 border-green-500/20 border text-xs">OK</Badge>
                          ) : (
                            <Badge className="bg-zinc-500/10 text-zinc-400 border-zinc-500/20 border text-xs">Empty</Badge>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Check Config Modal ─────────────────────────────────────────────────────────

interface CheckConfigModalProps {
  open: boolean
  onClose: () => void
  output: string
  isOk: boolean
}

function CheckConfigModal({ open, onClose, output, isOk }: CheckConfigModalProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-4xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            {isOk ? (
              <CheckCircle2 className="w-4 h-4 text-green-400" />
            ) : (
              <AlertTriangle className="w-4 h-4 text-amber-400" />
            )}
            named-checkconf Output
          </DialogTitle>
        </DialogHeader>
        <div className="bg-zinc-950 border border-zinc-800 rounded-lg p-4 max-h-96 overflow-auto">
          <pre className="text-xs font-mono text-zinc-300 whitespace-pre-wrap">{output || 'No output'}</pre>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white hover:bg-zinc-800">
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Record Editor Panel ────────────────────────────────────────────────────────

interface RecordEditorProps {
  zone: DNSZone
  onClose: () => void
  managementReady: boolean
  checkReady: boolean
}

function RecordEditor({ zone, onClose, managementReady, checkReady }: RecordEditorProps) {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [typeFilter, setTypeFilter] = useState<string>('All')
  const [addOpen, setAddOpen] = useState(false)
  const [editRecord, setEditRecord] = useState<DNSRecord | null>(null)
  const [deleteRecord, setDeleteRecord] = useState<DNSRecord | null>(null)
  const [checkOpen, setCheckOpen] = useState(false)
  const [checkOutput, setCheckOutput] = useState('')
  const [checkOk, setCheckOk] = useState(false)
  const [checkLoading, setCheckLoading] = useState(false)

  const addMutation = useMutation({
    mutationFn: (record: DNSRecord) =>
      api.post(`/dns/zones/${zone.domain}/records`, {
        name: record.name,
        ttl: String(record.ttl),
        type: record.type,
        value: record.value,
        priority: record.priority,
        autoReload: true,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zone', zone.domain] })
      queryClient.invalidateQueries({ queryKey: ['dns-zones'] })
      setAddOpen(false)
      toast.success('Record added — zone reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to add record'),
  })

  const updateMutation = useMutation({
    mutationFn: (record: DNSRecord) =>
      api.put(`/dns/zones/${zone.domain}/records`, {
        name: editRecord?.name ?? record.name,
        type: editRecord?.type ?? record.type,
        oldValue: editRecord?.value ?? record.value,
        newValue: record.value,
        newTtl: String(record.ttl),
        priority: record.priority,
        autoReload: true,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zone', zone.domain] })
      setEditRecord(null)
      toast.success('Record updated — zone reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update record'),
  })

  const deleteMutation = useMutation({
    mutationFn: (record: DNSRecord) =>
      api.delete(`/dns/zones/${zone.domain}/records`, {
        name: record.name,
        type: record.type,
        value: record.value,
        autoReload: true,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zone', zone.domain] })
      queryClient.invalidateQueries({ queryKey: ['dns-zones'] })
      setDeleteRecord(null)
      toast.success('Record deleted — zone reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete record'),
  })

  async function handleCheckConfig() {
    setCheckLoading(true)
    try {
      const resp = await api.post<{ output: string; ok: boolean }>('/dns/check')
      setCheckOutput(resp.output ?? '')
      setCheckOk(resp.ok ?? false)
      setCheckOpen(true)
    } catch {
      toast.error('Failed to run DNS config check')
    } finally {
      setCheckLoading(false)
    }
  }

  async function handleExport() {
    try {
      const { getToken } = await import('@/lib/api')
      const token = getToken()
      const resp = await fetch(`/api/dns/zones/${zone.domain}/export`, {
        credentials: 'include',
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      })
      if (!resp.ok) throw new Error('Export failed')
      const blob = await resp.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `db.${zone.domain}.txt`
      a.click()
      URL.revokeObjectURL(url)
      toast.success('Zone file exported')
    } catch {
      toast.error('Export failed')
    }
  }

  // Filter records
  const records = zone.records ?? []
  const filtered = records.filter((r) => {
    const matchType = typeFilter === 'All' || r.type === typeFilter
    const needle = search.toLowerCase()
    const matchSearch = !needle || r.name.toLowerCase().includes(needle) || r.value.toLowerCase().includes(needle)
    return matchType && matchSearch
  })

  // Derive SOA from records if present
  const soaRecord = records.find((r) => r.type === 'SOA')
  const soaData: SOARecord | undefined = soaRecord
    ? {
        primaryNs: soaRecord.value.split(' ')[0] ?? '',
        hostmaster: soaRecord.value.split(' ')[1] ?? '',
        serial: zone.serial,
        refresh: parseInt(soaRecord.value.split(' ')[3] ?? '3600', 10),
        retry: parseInt(soaRecord.value.split(' ')[4] ?? '900', 10),
        expire: parseInt(soaRecord.value.split(' ')[5] ?? '604800', 10),
        minimum: parseInt(soaRecord.value.split(' ')[6] ?? '300', 10),
      }
    : {
        primaryNs: '',
        hostmaster: '',
        serial: zone.serial,
        refresh: 3600,
        retry: 900,
        expire: 604800,
        minimum: 300,
      }

  return (
    <div className="flex flex-col gap-4 h-full">
      {/* Panel header */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="flex items-center gap-2 min-w-0">
          <button
            onClick={onClose}
            className="lg:hidden text-zinc-500 hover:text-white transition-colors p-1"
          >
            <X className="w-4 h-4" />
          </button>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="text-white font-semibold text-sm font-mono truncate">{zone.domain}</h3>
              <Badge className="bg-zinc-700/50 text-zinc-400 border-zinc-700 border text-[10px] font-mono px-1.5 py-0.5">
                #{zone.serial}
              </Badge>
            </div>
            <p className="text-zinc-500 text-xs mt-0.5">
              {records.length} record{records.length !== 1 ? 's' : ''}
              {filtered.length !== records.length && (
                <span className="text-zinc-600"> · {filtered.length} shown</span>
              )}
            </p>
          </div>
        </div>

        {/* Toolbar */}
        <div className="flex items-center gap-1.5 flex-wrap">
          <Button
            size="sm"
            variant="outline"
            onClick={handleCheckConfig}
            disabled={checkLoading || !checkReady}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 hover:border-zinc-600 h-8 px-2.5 text-xs"
          >
            {checkLoading ? (
              <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
            ) : (
              <ShieldCheck className="w-3.5 h-3.5 mr-1.5" />
            )}
            Check Config
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={handleExport}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 hover:border-zinc-600 h-8 px-2.5 text-xs"
          >
            <Download className="w-3.5 h-3.5 mr-1.5" />
            Export
          </Button>
          <Button
            size="sm"
            onClick={() => setAddOpen(true)}
            disabled={!managementReady}
            className="bg-blue-600 hover:bg-blue-500 text-white h-8 px-2.5 text-xs"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            Add Record
          </Button>
        </div>
      </div>

      {/* Search + type filter */}
      <div className="flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 min-w-48">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search by name or value..."
            className="w-full bg-zinc-800/60 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md pl-9 pr-3 py-1.5 text-sm focus:outline-none focus:border-blue-500 transition-colors"
          />
        </div>
        <div className="relative">
          <select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            className="appearance-none bg-zinc-800/60 border border-zinc-700 text-zinc-300 rounded-md pl-3 pr-8 py-1.5 text-sm focus:outline-none focus:border-blue-500 transition-colors cursor-pointer"
          >
            <option value="All">All types</option>
            {RECORD_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
          <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
        </div>
      </div>

      {/* Records table */}
      {records.length === 0 ? (
        <div className="flex flex-col items-center justify-center flex-1 py-16 text-zinc-600 gap-3">
          <Globe2 className="w-10 h-10 opacity-20" />
          <div className="text-center">
            <p className="text-sm font-medium text-zinc-500">No custom records</p>
            <p className="text-xs text-zinc-600 mt-1">Add your first DNS record to get started</p>
          </div>
          <Button
            size="sm"
            onClick={() => setAddOpen(true)}
            disabled={!managementReady}
            className="bg-blue-600 hover:bg-blue-500 text-white mt-1"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            Add Record
          </Button>
        </div>
      ) : filtered.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-zinc-600 gap-2">
          <Search className="w-8 h-8 opacity-20" />
          <p className="text-sm">No records match your filter</p>
          <button onClick={() => { setSearch(''); setTypeFilter('All') }} className="text-xs text-blue-400 hover:text-blue-300">
            Clear filters
          </button>
        </div>
      ) : (
        <div className="overflow-auto rounded-lg border border-zinc-800 flex-1">
          <Table>
            <TableHeader>
              <TableRow className="border-zinc-800 hover:bg-transparent">
                <TableHead className="text-zinc-500 font-medium text-xs w-20">Type</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-36">Name</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-20">TTL</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-14">Pri</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs">Value</TableHead>
                <TableHead className="text-zinc-500 font-medium text-xs w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((record, idx) => (
                <TableRow
                  key={`${record.name}-${record.type}-${idx}`}
                  className="border-zinc-800 hover:bg-zinc-800/40 transition-colors group"
                >
                  <TableCell className="py-2.5">
                    <RecordTypeBadge type={record.type} />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-zinc-300 py-2.5 max-w-[9rem]">
                    <span className="truncate block" title={record.name}>{record.name}</span>
                  </TableCell>
                  <TableCell className="text-xs text-zinc-500 py-2.5 font-mono">
                    {ttlLabel(Number(record.ttl) || 0)}
                  </TableCell>
                  <TableCell className="text-xs text-zinc-500 py-2.5 font-mono">
                    {record.priority !== undefined ? record.priority : <span className="text-zinc-700">—</span>}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-zinc-300 py-2.5">
                    <span className="truncate block max-w-xs" title={record.value}>
                      {record.value}
                    </span>
                  </TableCell>
                  <TableCell className="py-2.5 text-right">
                    <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                      <button
                        onClick={() => setEditRecord(record)}
                        disabled={!managementReady}
                        className="text-zinc-500 hover:text-blue-400 p-1 rounded transition-colors"
                        title="Edit"
                      >
                        <Edit2 className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={() => setDeleteRecord(record)}
                        disabled={!managementReady}
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

      {/* SOA section */}
      <SOASection domain={zone.domain} soa={soaData} managementReady={managementReady} />

      {/* DNS Lookup section */}
      <DNSLookupSection />

      {/* Dialogs */}
      <RecordDialog
        key={addOpen ? 'add-open' : 'add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSave={(r) => addMutation.mutate(r)}
        isPending={addMutation.isPending}
      />

      <RecordDialog
        key={editRecord ? `edit-${editRecord.name}-${editRecord.type}` : 'edit-none'}
        open={editRecord !== null}
        onClose={() => setEditRecord(null)}
        onSave={(r) => updateMutation.mutate(r)}
        initial={editRecord}
        isPending={updateMutation.isPending}
      />

      <ConfirmDeleteDialog
        open={deleteRecord !== null}
        title="Delete DNS Record"
        description={
          deleteRecord
            ? `Are you sure you want to delete the ${deleteRecord.type} record "${deleteRecord.name}"? This cannot be undone.`
            : ''
        }
        onConfirm={() => deleteRecord && deleteMutation.mutate(deleteRecord)}
        onClose={() => setDeleteRecord(null)}
        isPending={deleteMutation.isPending}
      />

      <CheckConfigModal
        open={checkOpen}
        onClose={() => setCheckOpen(false)}
        output={checkOutput}
        isOk={checkOk}
      />
    </div>
  )
}

// ─── Main DNS Page ──────────────────────────────────────────────────────────────

export default function DNS() {
  const queryClient = useQueryClient()
  const [selectedDomain, setSelectedDomain] = useState<string | null>(null)
  const [addZoneOpen, setAddZoneOpen] = useState(false)
  const [deleteZone, setDeleteZone] = useState<string | null>(null)

  // Zone list
  const zonesQuery = useQuery<DNSZone[]>({
    queryKey: ['dns-zones'],
    queryFn: () => api.get<DNSZone[]>('/dns/zones'),
  })
  const zones = zonesQuery.data ?? []

  // Selected zone detail
  const zoneQuery = useQuery<DNSZone>({
    queryKey: ['dns-zone', selectedDomain],
    queryFn: () => api.get<DNSZone>(`/dns/zones/${selectedDomain}`),
    enabled: selectedDomain !== null,
  })
  const zoneDetail = zoneQuery.data

  // BIND status
  const statusQuery = useQuery<DNSStatus>({
    queryKey: ['dns-status'],
    queryFn: () => api.get<DNSStatus>('/dns/status'),
    refetchInterval: 15_000,
  })
  const statusResp = statusQuery.data

  const bindRunning = statusResp?.active === true
  const managementReady = !statusQuery.isLoading && !statusQuery.isError && statusResp?.zoneManagementReady === true
  const checkReady = !statusQuery.isLoading && !statusQuery.isError && statusResp?.installed === true && statusResp.configAvailable && statusResp.checkToolsAvailable
  const statusLabel = statusQuery.isLoading
    ? 'BIND Checking'
    : statusQuery.isError
      ? 'BIND Unavailable'
      : statusResp?.recoveryPending
        ? 'BIND Recovery Required'
      : statusResp?.state === 'healthy'
        ? 'BIND Running'
        : statusResp?.state === 'not-installed'
          ? 'BIND Not Installed'
          : statusResp?.state === 'not-configured'
            ? 'BIND Setup Required'
            : statusResp?.state === 'stopped'
              ? 'BIND Stopped'
              : 'BIND Unavailable'

  // Reload BIND
  const reloadMutation = useMutation({
    mutationFn: () => api.post('/dns/reload'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-status'] })
      toast.success('BIND reloaded successfully')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to reload BIND'),
  })

  // Add zone
  const addZoneMutation = useMutation({
    mutationFn: ({ domain, ip }: { domain: string; ip: string }) =>
      api.post('/dns/zones', { domain, ip }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zones'] })
      setAddZoneOpen(false)
      toast.success('Zone created and BIND reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create zone'),
  })

  // Delete zone
  const deleteZoneMutation = useMutation({
    mutationFn: (domain: string) => api.delete(`/dns/zones/${domain}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dns-zones'] })
      if (selectedDomain === deleteZone) setSelectedDomain(null)
      setDeleteZone(null)
      toast.success('Zone deleted and BIND reloaded')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete zone'),
  })

  const activeZone = zoneDetail ?? null

  return (
    <div className="space-y-5">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 bg-blue-500/10 border border-blue-500/20 rounded-lg flex items-center justify-center flex-shrink-0">
            <Globe2 className="w-4.5 h-4.5 text-blue-400" />
          </div>
          <div>
            <h2 className="text-white text-xl font-bold">DNS Zones</h2>
            <p className="text-zinc-500 text-sm mt-0.5">
              {zonesQuery.isError
                ? 'BIND zone inventory unavailable'
                : `BIND zone management — ${zones.length} zone${zones.length !== 1 ? 's' : ''} configured`}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* BIND status pill */}
          <div
            className={cn(
              'flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium',
              statusQuery.isError || statusResp?.state === 'unavailable'
                ? 'bg-red-500/10 border-red-500/20 text-red-400'
                : bindRunning
                ? 'bg-green-500/10 border-green-500/20 text-green-400'
                : statusResp?.state === 'not-installed' || statusResp?.state === 'not-configured'
                  ? 'bg-amber-500/10 border-amber-500/20 text-amber-300'
                : 'bg-zinc-700/30 border-zinc-700/50 text-zinc-400',
            )}
          >
            {statusQuery.isLoading ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : statusQuery.isError ? (
              <XCircle className="size-3.5" />
            ) : bindRunning ? (
              <CheckCircle2 className="w-3.5 h-3.5" />
            ) : (
              <Info className="w-3.5 h-3.5" />
            )}
            {statusLabel}
          </div>

          {/* Reload BIND */}
          <Button
            variant="outline"
            size="sm"
            onClick={() => reloadMutation.mutate()}
            disabled={reloadMutation.isPending || !managementReady}
            className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 hover:border-zinc-600"
          >
            {reloadMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
            ) : (
              <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
            )}
            Reload BIND
          </Button>

          {/* Add zone */}
          {!zonesQuery.isError && !zonesQuery.isLoading && managementReady && (
            <Button
              size="sm"
              onClick={() => setAddZoneOpen(true)}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              Add Zone
            </Button>
          )}
        </div>
      </div>

      {statusQuery.isError && (
        <DependencyRemediation
          title="BIND status could not be loaded."
          summary="Existing zone files may still be visible, but every DNS mutation stays paused until local BIND readiness can be observed."
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Run the packaged Heyserver doctor and inspect Heyserver service logs.',
            <>Verify <code>named -v</code> and local systemd access with the Heyserver service identity.</>,
            'Retry detection after the API and local service state are observable.',
          ]}
        />
      )}

      {!statusQuery.isError && statusResp?.state === 'not-installed' && (
        <DependencyRemediation
          title="BIND is not installed"
          summary="Heyserver will not create distribution-owned BIND files implicitly. Install the local DNS server and its management tools before enabling zone controls."
          state="not-configured"
          error={statusResp.error}
          retry={() => { void statusQuery.refetch(); void zonesQuery.refetch() }}
          retrying={statusQuery.isFetching || zonesQuery.isFetching}
          steps={[
            <>Install <code>bind9</code> and <code>bind9-utils</code> from supported Ubuntu repositories.</>,
            <>Verify <code>named -v</code>, <code>named-checkconf -z</code>, and <code>rndc status</code>.</>,
            'Retry detection; installation never creates a zone automatically.',
          ]}
        />
      )}

      {!statusQuery.isError && statusResp?.state === 'not-configured' && (
        <DependencyRemediation
          title="BIND management setup is incomplete"
          summary="Zone controls remain paused because the local configuration file or required validation and reload tools are missing."
          state="not-configured"
          error={statusResp.error}
          retry={() => { void statusQuery.refetch(); void zonesQuery.refetch() }}
          retrying={statusQuery.isFetching || zonesQuery.isFetching}
          steps={[
            <>Verify <code>/etc/bind/named.conf.local</code> is a regular file owned by the installation.</>,
            <>Verify <code>named-checkconf</code>, <code>named-checkzone</code>, and <code>rndc</code> are available to Heyserver.</>,
            <>Run <code>named-checkconf -z</code>, then retry detection without replacing existing configuration.</>,
          ]}
        />
      )}

      {!statusQuery.isError && statusResp?.state === 'stopped' && (
        <DependencyRemediation
          title="BIND is stopped"
          summary="Existing zones remain readable, but zone changes and reloads are paused to avoid editing files that cannot be applied safely."
          state="stopped"
          error={statusResp.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Inspect <code>systemctl status bind9</code>, <code>systemctl status named</code>, and the relevant journal.</>,
            <>Run <code>named-checkconf -z</code> before starting the distribution-provided BIND unit.</>,
            'Retry detection after the named process is active.',
          ]}
        />
      )}

      {!statusQuery.isError && statusResp?.recoveryPending && (
        <DependencyRemediation
          title="BIND transaction recovery is required"
          summary="Heyserver detected an interrupted zone create or delete operation. DNS mutations stay locked until startup recovery can safely restore or finalize it."
          error={statusResp.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Inspect Heyserver service logs and run <code>named-checkconf -z</code>.</>,
            <>Restore <code>rndc reload</code> availability without deleting the protected lifecycle journal manually.</>,
            'Restart Heyserver; startup recovery will roll back the incomplete operation or finalize an already applied one.',
          ]}
        />
      )}

      {!statusQuery.isError && statusResp?.state === 'unavailable' && !statusResp.recoveryPending && (
        <DependencyRemediation
          title="BIND readiness is unavailable"
          summary="Heyserver found an incomplete or unobservable local BIND runtime. DNS mutations remain paused while existing files stay untouched."
          error={statusResp.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Run <code>named -v</code> with the Heyserver service identity.</>,
            'Inspect systemd visibility and Heyserver service logs.',
            'Retry after the local runtime can report both version and process state.',
          ]}
        />
      )}

      {/* Split layout */}
      <div className="flex gap-4 min-h-[calc(100vh-12rem)]">

        {/* LEFT — Zone list */}
        <div
          className={cn(
            'flex flex-col gap-1.5 lg:w-72 flex-shrink-0',
            selectedDomain ? 'hidden lg:flex' : 'flex w-full',
          )}
        >
          {zonesQuery.isLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-[68px] w-full bg-zinc-900 rounded-lg" />
            ))
          ) : zonesQuery.isError ? (
            <div className="flex flex-col items-center gap-3 rounded-xl border border-red-500/25 bg-red-500/[0.05] px-4 py-12 text-center">
              <AlertTriangle className="size-6 text-red-400" />
              <div>
                <p className="text-sm text-red-300">DNS zones could not be loaded. Zone controls are paused.</p>
                <p className="mt-1 break-words font-mono text-xs text-red-400/70">{zonesQuery.error.message}</p>
              </div>
              <Button type="button" size="sm" variant="outline" onClick={() => { void zonesQuery.refetch() }} disabled={zonesQuery.isFetching}>
                <RefreshCw className={cn('size-3.5', zonesQuery.isFetching && 'animate-spin')} /> Retry
              </Button>
            </div>
          ) : zones.length === 0 && managementReady ? (
            /* Empty state */
            <div className="flex flex-col items-center justify-center py-20 text-center gap-4">
              <div className="w-16 h-16 rounded-2xl bg-zinc-800/60 border border-zinc-700/50 flex items-center justify-center">
                <Globe2 className="w-7 h-7 text-zinc-600" />
              </div>
              <div>
                <p className="text-zinc-300 font-medium text-sm">No DNS zones configured</p>
                <p className="text-zinc-600 text-xs mt-1">Create your first zone to get started</p>
              </div>
              <Button
                onClick={() => setAddZoneOpen(true)}
                className="bg-blue-600 hover:bg-blue-500 text-white"
              >
                <Plus className="w-3.5 h-3.5 mr-1.5" />
                Create Zone
              </Button>
            </div>
          ) : (
            zones.map((zone) => {
              const isActive = selectedDomain === zone.domain
              const recordCount = zone.recordCount ?? zone.records?.length ?? 0
              return (
                <div
                  key={zone.domain}
                  className={cn(
                    'group flex items-center gap-3 px-4 py-3 rounded-lg border cursor-pointer transition-all',
                    isActive
                      ? 'bg-blue-600/10 border-blue-600/20 shadow-sm'
                      : 'bg-zinc-900 border-zinc-800 hover:border-zinc-700 hover:bg-zinc-800/50',
                  )}
                  onClick={() => setSelectedDomain(zone.domain)}
                >
                  <div
                    className={cn(
                      'w-8 h-8 rounded-md flex items-center justify-center flex-shrink-0',
                      isActive
                        ? 'bg-blue-600/20 border border-blue-600/30'
                        : 'bg-zinc-800 border border-zinc-700',
                    )}
                  >
                    <Server className={cn('w-3.5 h-3.5', isActive ? 'text-blue-400' : 'text-zinc-500')} />
                  </div>

                  <div className="flex-1 min-w-0">
                    <p className={cn('text-sm font-medium truncate', isActive ? 'text-blue-400' : 'text-zinc-200')}>
                      {zone.domain}
                    </p>
                    <div className="flex items-center gap-1.5 mt-0.5">
                      <span className="text-zinc-600 text-[11px] font-mono">#{zone.serial}</span>
                      <span className="text-zinc-700 text-[11px]">·</span>
                      <span className="text-zinc-600 text-[11px]">
                        {recordCount} record{recordCount !== 1 ? 's' : ''}
                      </span>
                    </div>
                  </div>

                  <div className="flex items-center gap-1 flex-shrink-0">
                    {isActive && (
                      <ChevronRight className="w-3.5 h-3.5 text-blue-400/60" />
                    )}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteZone(zone.domain)
                      }}
                      disabled={!managementReady}
                      className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-red-400 p-1 rounded transition-all"
                      title="Delete zone"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </div>
                </div>
              )
            })
          )}
        </div>

        {/* RIGHT — Record Editor */}
        <div
          className={cn(
            'flex-1 min-w-0',
            selectedDomain ? 'block' : 'hidden lg:block',
          )}
        >
          {selectedDomain === null ? (
            <div className="flex flex-col items-center justify-center h-full py-24 text-zinc-700 select-none gap-3">
              <div className="w-20 h-20 rounded-2xl bg-zinc-800/40 border border-zinc-800 flex items-center justify-center">
                <Globe2 className="w-8 h-8 opacity-30" />
              </div>
              <div className="text-center">
                <p className="text-zinc-500 text-sm font-medium">Select a zone to manage records</p>
                <p className="text-zinc-700 text-xs mt-1">Choose a zone from the left panel</p>
              </div>
            </div>
          ) : zoneQuery.isLoading ? (
            <Card className="bg-zinc-900 border-zinc-800 h-full">
              <CardContent className="p-6 space-y-3">
                {Array.from({ length: 8 }).map((_, i) => (
                  <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
                ))}
              </CardContent>
            </Card>
          ) : zoneQuery.isError ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 py-24 text-center">
              <XCircle className="size-8 text-red-400/70" />
              <div>
                <p className="text-sm text-red-300">DNS zone {selectedDomain} could not be loaded.</p>
                <p className="mt-1 break-words font-mono text-xs text-red-400/70">{zoneQuery.error.message}</p>
              </div>
              <Button type="button" size="sm" variant="outline" onClick={() => { void zoneQuery.refetch() }} disabled={zoneQuery.isFetching}>
                <RefreshCw className={cn('size-3.5', zoneQuery.isFetching && 'animate-spin')} /> Retry
              </Button>
            </div>
          ) : activeZone ? (
            <Card className="bg-zinc-900 border-zinc-800 h-full">
              <CardContent className="p-5 h-full">
                <RecordEditor
                  key={activeZone.domain}
                  zone={activeZone}
                  onClose={() => setSelectedDomain(null)}
                  managementReady={managementReady}
                  checkReady={checkReady}
                />
              </CardContent>
            </Card>
          ) : (
            <div className="flex flex-col items-center justify-center h-full py-24 text-zinc-600 gap-2">
              <XCircle className="w-8 h-8 opacity-40" />
              <p className="text-sm">Failed to load zone</p>
              <button
                onClick={() => queryClient.invalidateQueries({ queryKey: ['dns-zone', selectedDomain] })}
                className="text-xs text-blue-400 hover:text-blue-300"
              >
                Retry
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Global dialogs */}
      <AddZoneDialog
        open={addZoneOpen}
        onClose={() => setAddZoneOpen(false)}
        onSave={(domain, ip) => addZoneMutation.mutate({ domain, ip })}
        isPending={addZoneMutation.isPending}
      />

      <ConfirmDeleteDialog
        open={deleteZone !== null}
        title="Delete DNS Zone"
        description={
          deleteZone
            ? `Are you sure you want to delete the zone "${deleteZone}"? All records will be permanently removed.`
            : ''
        }
        onConfirm={() => deleteZone && deleteZoneMutation.mutate(deleteZone)}
        onClose={() => setDeleteZone(null)}
        isPending={deleteZoneMutation.isPending}
      />
    </div>
  )
}

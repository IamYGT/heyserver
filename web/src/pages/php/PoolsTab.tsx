import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus, Trash2, RefreshCw, AlertTriangle, FileCode, Edit2,
  RotateCcw, MemoryStick,
  Gauge, Layers, Zap, Code2, Globe,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select'
import {
  Tooltip, TooltipContent, TooltipTrigger,
} from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import type { PHPVersion, PHPPool, PHPPoolConfig, PHPPreset, CreatePoolRequest } from '@/lib/types'

// ─── Constants ────────────────────────────────────────────────────────────────

const PM_DESCRIPTIONS = {
  dynamic: 'Worker sayısı talebe göre değişir. Başlangıçta birkaç process başlar, yük arttıkça yeni process açılır.',
  static: 'Sabit sayıda worker her zaman çalışır. Yüksek trafikli siteler için önerilir.',
  ondemand: 'Process yalnızca istek geldiğinde başlatılır. Düşük trafikli siteler için bellek tasarrufu sağlar.',
} as const

const PRESETS: PHPPreset[] = [
  { name: 'low',        label: 'Low Traffic',   description: 'Küçük blog / showcase site',  pm: 'ondemand', pmMaxChildren: 5,   pmStartServers: 1, pmMinSpareServers: 1, pmMaxSpareServers: 2,  pmMaxRequests: 500,  memoryLimit: '128M' },
  { name: 'medium',     label: 'Medium Traffic', description: 'Orta ölçekli uygulama',       pm: 'dynamic',  pmMaxChildren: 10,  pmStartServers: 2, pmMinSpareServers: 2, pmMaxSpareServers: 5,  pmMaxRequests: 1000, memoryLimit: '256M' },
  { name: 'high',       label: 'High Traffic',   description: 'Yüksek trafikli production',  pm: 'dynamic',  pmMaxChildren: 30,  pmStartServers: 5, pmMinSpareServers: 5, pmMaxSpareServers: 15, pmMaxRequests: 2000, memoryLimit: '256M' },
  { name: 'wordpress',  label: 'WordPress',      description: 'WordPress optimized',         pm: 'dynamic',  pmMaxChildren: 15,  pmStartServers: 3, pmMinSpareServers: 3, pmMaxSpareServers: 8,  pmMaxRequests: 500,  memoryLimit: '256M' },
  { name: 'laravel',    label: 'Laravel',        description: 'Laravel / modern PHP',        pm: 'dynamic',  pmMaxChildren: 20,  pmStartServers: 4, pmMinSpareServers: 4, pmMaxSpareServers: 10, pmMaxRequests: 1000, memoryLimit: '512M' },
]

// ─── Preset Icons ─────────────────────────────────────────────────────────────

const PRESET_ICONS: Record<string, React.ReactNode> = {
  low:       <Gauge className="w-3.5 h-3.5" />,
  medium:    <Layers className="w-3.5 h-3.5" />,
  high:      <Zap className="w-3.5 h-3.5" />,
  wordpress: <Globe className="w-3.5 h-3.5" />,
  laravel:   <Code2 className="w-3.5 h-3.5" />,
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function pmColor(pm: string): string {
  return pm === 'dynamic'  ? 'bg-blue-500/10 text-blue-400 border-blue-500/20'
    : pm === 'static'      ? 'bg-purple-500/10 text-purple-400 border-purple-500/20'
    : 'bg-amber-500/10 text-amber-400 border-amber-500/20'
}

function healthColor(score: number): string {
  if (score >= 90) return 'text-green-400'
  if (score >= 70) return 'text-amber-400'
  return 'text-red-400'
}

function estimateMemory(workers: number, limitMB: number): string {
  const total = workers * limitMB
  if (total >= 1024) return `~${(total / 1024).toFixed(1)} GB`
  return `~${total} MB`
}

// ─── Pool Edit Dialog ──────────────────────────────────────────────────────────

interface EditPoolDialogProps {
  pool: PHPPool | null
  onClose: () => void
}

function EditPoolDialog({ pool, onClose }: EditPoolDialogProps) {
  const queryClient = useQueryClient()
  const [selectedPreset, setSelectedPreset] = useState<string>('custom')
  const ps = pool?.pm_settings
  const raw = pool?.raw ?? {}
  const [form, setForm] = useState<Partial<PHPPoolConfig>>({
    pm: pool?.pm ?? 'dynamic',
    pmMaxChildren: ps?.max_children ?? 10,
    pmStartServers: ps?.start_servers ?? 2,
    pmMinSpareServers: ps?.min_spare_servers ?? 2,
    pmMaxSpareServers: ps?.max_spare_servers ?? 5,
    pmMaxRequests: ps?.max_requests ?? 1000,
    memoryLimit: raw['php_admin_value[memory_limit]'] ?? raw['php_value[memory_limit]'] ?? '256M',
    maxExecutionTime: parseInt(raw['php_value[max_execution_time]'] ?? '60') || 60,
    uploadMaxFilesize: raw['php_value[upload_max_filesize]'] ?? '64M',
    postMaxSize: raw['php_value[post_max_size]'] ?? '64M',
    slowlogEnabled: false,
    slowlogTimeout: '5s',
    accessLogEnabled: false,
    securityProfile: 'moderate',
  })

  const saveMutation = useMutation({
    mutationFn: () =>
      api.post(`/php/pools/${pool?.version}/${pool?.name}`, form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php'] })
      toast.success(`Pool ${pool?.name} updated`)
      onClose()
    },
    onError: () => toast.error('Failed to update pool'),
  })

  const applyPreset = (presetName: string) => {
    const preset = PRESETS.find(p => p.name === presetName)
    if (!preset) return
    setSelectedPreset(presetName)
    setForm(f => ({
      ...f,
      pm: preset.pm,
      pmMaxChildren: preset.pmMaxChildren,
      pmStartServers: preset.pmStartServers,
      pmMinSpareServers: preset.pmMinSpareServers,
      pmMaxSpareServers: preset.pmMaxSpareServers,
      pmMaxRequests: preset.pmMaxRequests,
      memoryLimit: preset.memoryLimit,
    }))
  }

  const limitMB = parseInt(String(form.memoryLimit ?? '256').replace(/[^0-9]/g, '')) || 256

  return (
    <Dialog open={!!pool} onOpenChange={v => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-4xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <Edit2 className="w-4 h-4 text-blue-400" />
            Edit Pool: <span className="font-mono text-blue-300">{pool?.name}</span>
            <Badge className="text-xs bg-zinc-800 text-zinc-400 border-zinc-700 ml-1">PHP {pool?.version}</Badge>
          </DialogTitle>
          <DialogDescription className="text-zinc-400">
            Configure process manager, resource limits and security settings.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-6 mt-2">
          {/* Preset Selector */}
          <div>
            <Label className="text-zinc-400 text-xs uppercase tracking-wider mb-2 block">Quick Preset</Label>
            <div className="grid grid-cols-5 gap-2">
              {PRESETS.map(preset => (
                <button
                  key={preset.name}
                  onClick={() => applyPreset(preset.name)}
                  className={cn(
                    'rounded-lg border p-2.5 text-left transition-all duration-200',
                    selectedPreset === preset.name
                      ? 'border-blue-500 bg-blue-500/10 text-blue-300 ring-2 ring-blue-500/30'
                      : 'border-zinc-700 bg-zinc-800/50 text-zinc-400 hover:border-zinc-500 hover:bg-zinc-800'
                  )}
                >
                  <div className={cn('mb-1', selectedPreset === preset.name ? 'text-blue-400' : 'text-zinc-500')}>
                    {PRESET_ICONS[preset.name]}
                  </div>
                  <p className="text-xs font-semibold">{preset.label}</p>
                  <p className="text-[10px] opacity-70 mt-0.5">{preset.description}</p>
                </button>
              ))}
            </div>
          </div>

          {/* Divider */}
          <div className="border-t border-zinc-800" />

          {/* Process Manager */}
          <div className="space-y-4">
            <Label className="text-zinc-400 text-xs uppercase tracking-wider block">Process Manager</Label>
            <div className="grid grid-cols-3 gap-2">
              {(['dynamic', 'static', 'ondemand'] as const).map(mode => (
                <button
                  key={mode}
                  onClick={() => setForm(f => ({ ...f, pm: mode }))}
                  className={cn(
                    'rounded-lg border p-3 text-left transition-all',
                    form.pm === mode
                      ? 'border-blue-500 bg-blue-500/10'
                      : 'border-zinc-700 bg-zinc-800/50 hover:border-zinc-600'
                  )}
                >
                  <p className={cn('text-sm font-semibold capitalize', form.pm === mode ? 'text-blue-300' : 'text-zinc-300')}>{mode}</p>
                  <p className="text-[11px] text-zinc-500 mt-1 leading-relaxed">{PM_DESCRIPTIONS[mode]}</p>
                </button>
              ))}
            </div>

            <div className="grid grid-cols-2 gap-4">
              <NumberField
                label="Max Children"
                value={form.pmMaxChildren ?? 10}
                onChange={v => setForm(f => ({ ...f, pmMaxChildren: v }))}
                min={1} max={500}
              />
              {form.pm !== 'static' && (
                <NumberField
                  label="Start Servers"
                  value={form.pmStartServers ?? 2}
                  onChange={v => setForm(f => ({ ...f, pmStartServers: v }))}
                  min={1} max={100}
                  disabled={form.pm === 'ondemand'}
                />
              )}
              {form.pm === 'dynamic' && (
                <>
                  <NumberField
                    label="Min Spare Servers"
                    value={form.pmMinSpareServers ?? 2}
                    onChange={v => setForm(f => ({ ...f, pmMinSpareServers: v }))}
                    min={1} max={100}
                  />
                  <NumberField
                    label="Max Spare Servers"
                    value={form.pmMaxSpareServers ?? 5}
                    onChange={v => setForm(f => ({ ...f, pmMaxSpareServers: v }))}
                    min={1} max={200}
                  />
                </>
              )}
              <NumberField
                label="Max Requests (0=unlimited)"
                value={form.pmMaxRequests ?? 0}
                onChange={v => setForm(f => ({ ...f, pmMaxRequests: v }))}
                min={0} max={100000}
              />
            </div>

            {/* Memory estimator */}
            <div className="bg-blue-500/5 rounded-lg border border-blue-500/20 px-4 py-3 flex items-center justify-between">
              <div className="flex items-center gap-2 text-blue-400">
                <MemoryStick className="w-4 h-4" />
                <span className="text-sm font-medium">Estimated Memory Usage</span>
              </div>
              <div className="text-right">
                <p className="text-blue-300 font-bold text-lg">
                  {estimateMemory(form.pmMaxChildren ?? 10, limitMB)}
                </p>
                <p className="text-zinc-500 text-xs">
                  {form.pmMaxChildren} workers × {form.memoryLimit}
                </p>
              </div>
            </div>
          </div>

          {/* Divider */}
          <div className="border-t border-zinc-800" />

          {/* Resource Limits */}
          <div className="space-y-3">
            <Label className="text-zinc-400 text-xs uppercase tracking-wider block">Resource Limits</Label>
            <div className="grid grid-cols-2 gap-3">
              <StringField
                label="Memory Limit"
                value={form.memoryLimit ?? '256M'}
                onChange={v => setForm(f => ({ ...f, memoryLimit: v }))}
                placeholder="256M"
              />
              <NumberField
                label="Max Execution Time (s)"
                value={form.maxExecutionTime ?? 60}
                onChange={v => setForm(f => ({ ...f, maxExecutionTime: v }))}
                min={1} max={3600}
              />
              <StringField
                label="Upload Max Filesize"
                value={form.uploadMaxFilesize ?? '64M'}
                onChange={v => setForm(f => ({ ...f, uploadMaxFilesize: v }))}
                placeholder="64M"
              />
              <StringField
                label="Post Max Size"
                value={form.postMaxSize ?? '64M'}
                onChange={v => setForm(f => ({ ...f, postMaxSize: v }))}
                placeholder="64M"
              />
            </div>
          </div>

          {/* Divider */}
          <div className="border-t border-zinc-800" />

          {/* Logging */}
          <div className="space-y-3">
            <Label className="text-zinc-400 text-xs uppercase tracking-wider block">Logging</Label>
            <div className="grid grid-cols-2 gap-3">
              <ToggleField
                label="Slow Log"
                enabled={form.slowlogEnabled ?? false}
                onToggle={v => setForm(f => ({ ...f, slowlogEnabled: v }))}
              />
              <ToggleField
                label="Access Log"
                enabled={form.accessLogEnabled ?? false}
                onToggle={v => setForm(f => ({ ...f, accessLogEnabled: v }))}
              />
              {form.slowlogEnabled && (
                <StringField
                  label="Slow Log Threshold"
                  value={form.slowlogTimeout ?? '5s'}
                  onChange={v => setForm(f => ({ ...f, slowlogTimeout: v }))}
                  placeholder="5s"
                />
              )}
            </div>
          </div>

          {/* Divider */}
          <div className="border-t border-zinc-800" />

          {/* Security Profile */}
          <div className="space-y-3">
            <Label className="text-zinc-400 text-xs uppercase tracking-wider block">Security Profile</Label>
            <div className="grid grid-cols-3 gap-2">
              {([
                { name: 'strict',      color: 'text-green-400',  desc: 'open_basedir + disable_functions' },
                { name: 'moderate',    color: 'text-amber-400',  desc: 'open_basedir only' },
                { name: 'permissive',  color: 'text-red-400',    desc: 'No restrictions' },
              ]).map(p => (
                <button
                  key={p.name}
                  onClick={() => setForm(f => ({ ...f, securityProfile: p.name }))}
                  className={cn(
                    'rounded-lg border p-3 text-left transition-all duration-200',
                    form.securityProfile === p.name
                      ? 'border-zinc-400 bg-zinc-800 ring-2 ring-zinc-500/30'
                      : 'border-zinc-700 bg-zinc-800/50 hover:border-zinc-600 hover:bg-zinc-800'
                  )}
                >
                  <p className={cn('text-sm font-semibold capitalize', p.color)}>{p.name}</p>
                  <p className="text-[11px] text-zinc-500 mt-1">{p.desc}</p>
                </button>
              ))}
            </div>
          </div>
        </div>

        <DialogFooter className="mt-6 gap-2">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white">
            Cancel
          </Button>
          <Button
            onClick={() => saveMutation.mutate()}
            className="bg-blue-600 hover:bg-blue-500 text-white"
            disabled={saveMutation.isPending}
          >
            {saveMutation.isPending ? (
              <><RefreshCw className="w-3.5 h-3.5 mr-2 animate-spin" />Saving…</>
            ) : 'Save & Apply'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Small form helpers ───────────────────────────────────────────────────────

function NumberField({ label, value, onChange, min, max, disabled }: {
  label: string; value: number; onChange: (v: number) => void
  min?: number; max?: number; disabled?: boolean
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-zinc-400 text-xs">{label}</Label>
      <Input
        type="number" min={min} max={max} value={value} disabled={disabled}
        onChange={e => onChange(parseInt(e.target.value) || 0)}
        className="bg-zinc-800 border-zinc-700 text-white h-8 text-sm disabled:opacity-40"
      />
    </div>
  )
}

function StringField({ label, value, onChange, placeholder }: {
  label: string; value: string; onChange: (v: string) => void; placeholder?: string
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-zinc-400 text-xs">{label}</Label>
      <Input
        value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder}
        className="bg-zinc-800 border-zinc-700 text-white h-8 text-sm"
      />
    </div>
  )
}

function ToggleField({ label, enabled, onToggle }: {
  label: string; enabled: boolean; onToggle: (v: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between bg-zinc-800/60 border border-zinc-700 rounded-lg px-3 py-2.5">
      <Label className="text-zinc-300 text-sm cursor-pointer" onClick={() => onToggle(!enabled)}>
        {label}
      </Label>
      <button
        onClick={() => onToggle(!enabled)}
        className={cn(
          'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
          enabled ? 'bg-blue-600' : 'bg-zinc-600'
        )}
      >
        <span className={cn(
          'inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform',
          enabled ? 'translate-x-5' : 'translate-x-0.5'
        )} />
      </button>
    </div>
  )
}

// ─── Delete Confirm ───────────────────────────────────────────────────────────

interface DeleteConfirmProps {
  pool: { version: string; name: string } | null
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}

function DeleteConfirmDialog({ pool, onClose, onConfirm, isPending }: DeleteConfirmProps) {
  return (
    <Dialog open={!!pool} onOpenChange={v => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-red-400" /> Delete Pool
          </DialogTitle>
          <DialogDescription className="text-zinc-400">
            Are you sure you want to delete{' '}
            <span className="font-mono text-zinc-200">{pool?.name}</span>?{' '}
            This removes the PHP-FPM pool config. Cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="mt-2">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white">Cancel</Button>
          <Button onClick={onConfirm} className="bg-red-600 hover:bg-red-500 text-white" disabled={isPending}>
            {isPending ? 'Deleting…' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Create Pool Dialog ───────────────────────────────────────────────────────

interface CreatePoolDialogProps {
  open: boolean
  onClose: () => void
  versions: PHPVersion[]
}

function CreatePoolDialog({ open, onClose, versions }: CreatePoolDialogProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<CreatePoolRequest>({
    version: versions[0]?.version ?? '',
    name: '', user: '', domain: '', domainRoot: '', pm: 'dynamic', pmMaxChildren: 10,
  })

  const create = useMutation({
    mutationFn: (data: CreatePoolRequest) => api.post('/php/pools', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php'] })
      toast.success('Pool created')
      onClose()
    },
    onError: () => toast.error('Failed to create pool'),
  })

  return (
    <Dialog open={open} onOpenChange={v => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl">
        <DialogHeader>
          <DialogTitle>Create PHP-FPM Pool</DialogTitle>
          <DialogDescription className="text-zinc-400">Add a new pool for a domain.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 mt-2">
          <div className="space-y-1.5">
            <Label className="text-zinc-400 text-xs">PHP Version</Label>
            <Select value={form.version} onValueChange={v => setForm(f => ({ ...f, version: v ?? '' }))}>
              <SelectTrigger className="bg-zinc-800 border-zinc-700 text-white">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="bg-zinc-800 border-zinc-700">
                {versions.map(v => (
                  <SelectItem key={v.version} value={v.version} className="text-zinc-300 focus:bg-zinc-700">
                    PHP {v.version}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {[
            { key: 'name', label: 'Pool Name', placeholder: 'mysite' },
            { key: 'user', label: 'System User', placeholder: 'www-data' },
            { key: 'domain', label: 'Domain', placeholder: 'example.com' },
            { key: 'domainRoot', label: 'Document Root', placeholder: '/var/www/example.com' },
          ].map(({ key, label, placeholder }) => (
            <div key={key} className="space-y-1.5">
              <Label className="text-zinc-400 text-xs">{label}</Label>
              <Input
                value={form[key as keyof CreatePoolRequest] ?? ''}
                onChange={e => setForm(f => ({ ...f, [key]: e.target.value }))}
                placeholder={placeholder}
                className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600"
              />
            </div>
          ))}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label className="text-zinc-400 text-xs">PM Mode</Label>
              <Select
                value={form.pm}
                onValueChange={v => setForm(f => ({ ...f, pm: v as CreatePoolRequest['pm'] }))}
              >
                <SelectTrigger className="bg-zinc-800 border-zinc-700 text-white">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className="bg-zinc-800 border-zinc-700">
                  {(['dynamic', 'static', 'ondemand'] as const).map(m => (
                    <SelectItem key={m} value={m} className="text-zinc-300 focus:bg-zinc-700 capitalize">{m}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-zinc-400 text-xs">Max Children</Label>
              <Input
                type="number" min={1} max={500} value={form.pmMaxChildren}
                onChange={e => setForm(f => ({ ...f, pmMaxChildren: parseInt(e.target.value) || 10 }))}
                className="bg-zinc-800 border-zinc-700 text-white"
              />
            </div>
          </div>
        </div>
        <DialogFooter className="mt-4">
          <Button variant="ghost" onClick={onClose} className="text-zinc-400 hover:text-white">Cancel</Button>
          <Button
            onClick={() => {
              if (!form.version || !form.name || !form.user || !form.domainRoot) {
                toast.error('Fill in all required fields')
                return
              }
              create.mutate(form)
            }}
            className="bg-blue-600 hover:bg-blue-500 text-white"
            disabled={create.isPending}
          >
            {create.isPending ? 'Creating…' : 'Create Pool'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Pools Tab ────────────────────────────────────────────────────────────────

interface PoolsTabProps {
  selectedVersion: string
  versions: PHPVersion[]
}

export default function PoolsTab({ selectedVersion, versions }: PoolsTabProps) {
  const queryClient = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editPool, setEditPool] = useState<PHPPool | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{ version: string; name: string } | null>(null)

  const poolsQuery = useQuery<PHPPool[]>({
    queryKey: ['php', 'pools'],
    queryFn: () => api.get<PHPPool[]>('/php/pools'),
    refetchInterval: 30_000,
  })
  const pools = poolsQuery.data

  const deleteMutation = useMutation({
    mutationFn: ({ version, name }: { version: string; name: string }) =>
      api.delete(`/php/pools/${version}/${name}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['php'] })
      toast.success('Pool deleted')
      setDeleteTarget(null)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete pool'),
  })

  const restartPoolMutation = useMutation({
    mutationFn: ({ version, name }: { version: string; name: string }) =>
      api.post(`/php/pools/${version}/${name}/restart`),
    onSuccess: (_d, vars) => toast.success(`Pool ${vars.name} restarted`),
    onError: (error: Error) => toast.error(error.message || 'Failed to restart pool'),
  })

  const filtered = (pools ?? []).filter(p =>
    selectedVersion === 'all' || p.version === selectedVersion
  )

  if (poolsQuery.isLoading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-14 w-full bg-zinc-800 rounded-lg" />
        ))}
      </div>
    )
  }

  if (poolsQuery.isError) {
    return (
      <Card className="border-red-500/25 bg-red-500/[0.05]">
        <CardContent className="flex flex-col items-center gap-3 py-12 text-center">
          <AlertTriangle className="size-7 text-red-400" />
          <div>
            <p className="text-sm text-red-300">PHP pools could not be loaded. Mutating controls are paused.</p>
            <p className="mt-1 break-words font-mono text-xs text-red-400/70">{poolsQuery.error.message}</p>
          </div>
          <Button type="button" size="sm" variant="outline" onClick={() => { void poolsQuery.refetch() }} disabled={poolsQuery.isFetching}>
            <RefreshCw className={`size-3.5 ${poolsQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-3">
        <p className="text-zinc-400 text-sm">
          {filtered.length} pool{filtered.length !== 1 ? 's' : ''}
          {selectedVersion !== 'all' && ` · PHP ${selectedVersion}`}
        </p>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white h-8"
          onClick={() => setCreateOpen(true)}
          disabled={!versions.length}
        >
          <Plus className="w-3.5 h-3.5 mr-1.5" /> Create Pool
        </Button>
      </div>

      {filtered.length === 0 ? (
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="flex flex-col items-center justify-center py-16 text-zinc-600">
            <FileCode className="w-10 h-10 mb-3 opacity-40" />
            <p className="text-sm">No pools configured</p>
          </CardContent>
        </Card>
      ) : (
        <Card className="bg-zinc-900 border-zinc-800">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="border-zinc-800 hover:bg-transparent">
                  <TableHead className="text-zinc-500 text-xs">Domain / Name</TableHead>
                  <TableHead className="text-zinc-500 text-xs">Version</TableHead>
                  <TableHead className="text-zinc-500 text-xs">PM Mode</TableHead>
                  <TableHead className="text-zinc-500 text-xs hidden md:table-cell">Workers</TableHead>
                  <TableHead className="text-zinc-500 text-xs hidden lg:table-cell">Memory</TableHead>
                  <TableHead className="text-zinc-500 text-xs">Status</TableHead>
                  <TableHead className="text-zinc-500 text-xs hidden lg:table-cell">Health</TableHead>
                  <TableHead className="text-zinc-500 text-xs text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map(pool => (
                  <TableRow key={`${pool.version}-${pool.name}`} className="border-zinc-800 hover:bg-zinc-800/40">
                    <TableCell>
                      <div>
                        <p className="text-white text-sm font-medium">{pool.name}</p>
                        <p className="text-zinc-500 text-xs font-mono">{pool.user}</p>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge className="bg-zinc-800 text-zinc-300 border-zinc-700 text-xs font-mono">
                        {pool.version}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Badge className={cn('text-xs border', pmColor(pool.pm))}>
                        {pool.pm}
                      </Badge>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <div className="flex items-center gap-1.5">
                        <span className="text-zinc-600 text-sm font-mono">—</span>
                        <span className="text-zinc-600">/</span>
                        <span className="text-zinc-300 text-sm font-mono">{pool.pm_settings?.max_children ?? 0}</span>
                      </div>
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">
                      <span className="text-zinc-300 text-xs font-mono">{pool.raw?.['php_admin_value[memory_limit]'] ?? pool.raw?.['php_value[memory_limit]'] ?? '—'}</span>
                    </TableCell>
                    <TableCell>
                      {pool.socket_exists ? (
                        <div className="flex items-center gap-1.5">
                          <span className="w-1.5 h-1.5 rounded-full bg-green-500 flex-shrink-0" />
                          <span className="text-green-400 text-xs">Active</span>
                        </div>
                      ) : (
                        <div className="flex items-center gap-1.5">
                          <span className="w-1.5 h-1.5 rounded-full bg-zinc-500 flex-shrink-0" />
                          <span className="text-zinc-400 text-xs">Inactive</span>
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">
                      {pool.healthScore !== undefined ? (
                        <span className={cn('text-sm font-semibold', healthColor(pool.healthScore))}>
                          {pool.healthScore}/100
                        </span>
                      ) : <span className="text-zinc-600 text-xs">—</span>}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Tooltip>
                          <TooltipTrigger
                            render={<Button
                              variant="ghost" size="icon"
                              className="h-7 w-7 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                              onClick={() => setEditPool(pool)}
                            >
                              <Edit2 className="w-3.5 h-3.5" />
                            </Button>}
                          />
                          <TooltipContent>Edit pool</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger
                            render={<Button
                              variant="ghost" size="icon"
                              className="h-7 w-7 text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10"
                              onClick={() => restartPoolMutation.mutate({ version: pool.version, name: pool.name })}
                              disabled={restartPoolMutation.isPending}
                            >
                              <RotateCcw className="w-3.5 h-3.5" />
                            </Button>}
                          />
                          <TooltipContent>Restart pool</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger
                            render={<Button
                              variant="ghost" size="icon"
                              className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                              onClick={() => setDeleteTarget({ version: pool.version, name: pool.name })}
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </Button>}
                          />
                          <TooltipContent>Delete pool</TooltipContent>
                        </Tooltip>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </Card>
      )}

      {createOpen && (
        <CreatePoolDialog open={createOpen} onClose={() => setCreateOpen(false)} versions={versions} />
      )}
      {editPool && <EditPoolDialog pool={editPool} onClose={() => setEditPool(null)} />}
      <DeleteConfirmDialog
        pool={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => { if (deleteTarget) deleteMutation.mutate(deleteTarget) }}
        isPending={deleteMutation.isPending}
      />
    </div>
  )
}

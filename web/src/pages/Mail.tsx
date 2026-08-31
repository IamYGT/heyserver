import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Mail,
  Users,
  AtSign,
  Shield,
  Send,
  Play,
  Square,
  RotateCcw,
  Plus,
  Trash2,
  CheckCircle2,
  XCircle,
  AlertCircle,
  RefreshCw,
  HardDrive,
  Loader2,
  Eye,
  EyeOff,
  Server,
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
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import {
  INTEGRATION_UNAVAILABLE,
  integrationStatePresentation,
  normalizeIntegrationState,
  type IntegrationState,
} from '@/lib/integrationState'
import { toast } from 'sonner'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import type {
  MailStatus,
  MailAccount,
  MailAlias,
  MailDNSCheck,
  MailQueueItem,
} from '@/lib/types'

// ─── Helper functions ──────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 ** 3)).toFixed(1)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 ** 2)).toFixed(0)} MB`
  return `${(bytes / 1024).toFixed(0)} KB`
}

function formatQuota(used: number, total: number): string {
  if (total === 0) return `${formatBytes(used)} / Unlimited`
  return `${formatBytes(used)} / ${formatBytes(total)}`
}

function quotaPercent(used: number, total: number): number {
  if (total === 0) return 0
  return Math.min(100, Math.round((used / total) * 100))
}

function formatDate(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleString('en-GB', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function QueryFailure({ title, error, onRetry, isFetching = false }: {
  title: string
  error: Error
  onRetry: () => void
  isFetching?: boolean
}) {
  return (
    <div className="flex flex-col items-center gap-3 p-8 text-center">
      <AlertCircle className="size-6 text-red-400" />
      <div>
        <p className="text-sm font-medium text-red-300">{title}</p>
        <p className="mt-1 max-w-2xl break-words font-mono text-xs text-red-400/70">{error.message}</p>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={onRetry} disabled={isFetching}>
        <RefreshCw className={`size-3.5 ${isFetching ? 'animate-spin' : ''}`} /> Retry
      </Button>
    </div>
  )
}

type MailOverviewSourceKey = 'status' | 'version' | 'listeners' | 'storage'

interface MailOverviewSourcePayload {
  available?: boolean
  state?: unknown
  error?: string
}

interface MailOverviewSource {
  available?: boolean
  /** Only canonical optional-integration states survive API normalization. */
  state?: IntegrationState
  error?: string
}

interface MailOverviewResponse {
  status: { running: boolean; status: string; pid: string; uptime: string }
  version: { raw: string; version: string }
  listeners: Array<{ id: string; protocol: string; bind?: string; port?: number }>
  storage: { backend: string; path?: string; sizeBytes?: number }
  sources?: Partial<Record<MailOverviewSourceKey, MailOverviewSourcePayload>>
}

interface MailOverviewStatus extends MailStatus {
  state: string
  sources?: Partial<Record<MailOverviewSourceKey, MailOverviewSource>>
}

function normalizeMailOverviewSource(
  source: MailOverviewSourcePayload | undefined,
  fallbackState?: IntegrationState,
): MailOverviewSource | undefined {
  if (!source) return undefined

  // A missing or malformed provider source is unavailable, never healthy from
  // a URL or legacy boolean. Runtime status values intentionally omit this
  // fallback so `running`/`stopped`/`failed`/`unknown` remain runtime-only.
  const state = normalizeIntegrationState(source.state) ?? fallbackState
  return {
    available: source.available,
    ...(state ? { state } : {}),
    ...(source.error ? { error: source.error } : {}),
  }
}

function normalizeMailOverviewSources(
  sources: MailOverviewResponse['sources'],
): MailOverviewStatus['sources'] {
  if (!sources) return undefined

  // `sources.status.state` is a runtime label in the current API
  // (`running`, `stopped`, `failed`, or `unknown`). Normalization deliberately
  // drops those values instead of turning runtime state into provider health.
  return {
    status: normalizeMailOverviewSource(sources.status),
    version: normalizeMailOverviewSource(sources.version, INTEGRATION_UNAVAILABLE),
    listeners: normalizeMailOverviewSource(sources.listeners, INTEGRATION_UNAVAILABLE),
    storage: normalizeMailOverviewSource(sources.storage, INTEGRATION_UNAVAILABLE),
  }
}

function isUnavailableMailOverviewSource(source: MailOverviewSource | undefined): boolean {
  if (!source) return false
  if (source.state) return source.state !== 'healthy'
  return source.available === false
}

const sourceStateClasses = {
  neutral: 'border-zinc-600/70 bg-zinc-800/70 text-zinc-300',
  warning: 'border-amber-500/40 bg-amber-500/10 text-amber-300',
  healthy: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300',
} as const

function MailOverviewSourceState({
  sourceKey,
  label,
  source,
}: {
  sourceKey: MailOverviewSourceKey
  label: string
  source?: MailOverviewSource
}) {
  if (!source?.state) return null

  const presentation = integrationStatePresentation(source.state)
  return (
    <Badge
      variant="outline"
      data-testid={`mail-source-state-${sourceKey}`}
      aria-label={`${label} availability: ${presentation.label}`}
      title={`${label} availability`}
      className={sourceStateClasses[presentation.tone]}
    >
      <span className="text-current/70">{label}</span>
      <span>{presentation.label}</span>
    </Badge>
  )
}

// ─── Overview Tab ──────────────────────────────────────────────────────────────

function OverviewTab() {
  const queryClient = useQueryClient()

  const statusQuery = useQuery<MailOverviewStatus>({
    queryKey: ['mail', 'status'],
    queryFn: async () => {
      const overview = await api.get<MailOverviewResponse>('/mail/service/overview')
      return {
        running: overview.status?.running ?? false,
        state: overview.status?.status ?? 'unknown',
        version: overview.version?.version,
        listeners: overview.listeners?.map(l => ({
          protocol: l.protocol,
          port: l.port ?? parseInt(l.bind?.split(':').pop() || '0'),
          address: l.bind ?? '',
        })) ?? [],
        storage: { used: overview.storage?.sizeBytes ?? 0, total: 0, path: overview.storage?.path ?? '' },
        queued: 0,
        sources: normalizeMailOverviewSources(overview.sources),
      }
    },
    refetchInterval: 15_000,
  })
  const status = statusQuery.data

  const actionMutation = useMutation({
    mutationFn: (action: 'start' | 'stop' | 'restart') =>
      api.post(`/mail/service/${action}`),
    onSuccess: (_, action) => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'status'] })
      toast.success(`Mail service ${action} successful`)
    },
    onError: (error: Error, action) => toast.error(error.message || `Failed to ${action} mail service`),
  })

  if (statusQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-36 w-full bg-zinc-800" />
        <Skeleton className="h-48 w-full bg-zinc-800" />
      </div>
    )
  }

  if (statusQuery.isError) {
    return (
      <DependencyRemediation
        title="Stalwart mail integration is unavailable"
        summary="Heyserver could not inspect the configured Stalwart service. Service controls remain paused until detection succeeds."
        state="unavailable"
        steps={[
          <>Verify <code>HSERVER_STALWART_SERVICE</code>, <code>HSERVER_STALWART_CONFIG_PATH</code>, and <code>HSERVER_STALWART_BIN</code>.</>,
          <>Inspect <code>systemctl status &lt;configured unit&gt;</code> on the Heyserver host.</>,
          <>Confirm the configured binary and protected config path are accessible to Heyserver, then retry detection.</>,
        ]}
        error={statusQuery.error.message}
        retry={() => { void statusQuery.refetch() }}
        retrying={statusQuery.isFetching}
      />
    )
  }

  if (!status) return null

  const storagePercent = quotaPercent(status.storage.used, status.storage.total)
  const statusSource = status.sources?.status
  const listenersSource = status.sources?.listeners
  const storageSource = status.sources?.storage
  const statusKnown = statusSource?.available ?? status.state !== 'unknown'
  const statusLabel = statusKnown
    ? status.state.charAt(0).toUpperCase() + status.state.slice(1)
    : 'Unknown'
  const sourceAvailability = [
    { key: 'status' as const, label: 'Service', source: statusSource },
    { key: 'version' as const, label: 'Version', source: status.sources?.version },
    { key: 'listeners' as const, label: 'Listeners', source: listenersSource },
    { key: 'storage' as const, label: 'Storage', source: storageSource },
  ]
  const hasSourceAvailability = sourceAvailability.some(({ source }) => source?.state)

  return (
    <div className="space-y-4">
      {!statusKnown && (
        <DependencyRemediation
          title="Stalwart service state is unavailable"
          summary="The overview endpoint responded, but Heyserver could not determine the configured service state. Start, stop, and restart remain disabled."
          state="unavailable"
          steps={[
            <>Verify <code>HSERVER_STALWART_SERVICE</code> names the installed systemd unit.</>,
            <>Inspect <code>systemctl status &lt;configured unit&gt;</code> and the Heyserver service logs.</>,
            <>Correct the service reference or access boundary, then retry detection.</>,
          ]}
          error={statusSource?.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
        />
      )}
      {/* Service status card */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <Server className="w-4 h-4 text-blue-400" />
            Stalwart Mail Server
            {status.version && (
              <span className="text-zinc-500 text-xs font-normal">v{status.version}</span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {hasSourceAvailability && (
            <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800 pb-3" aria-label="Mail source availability">
              <span className="text-zinc-500 text-xs uppercase tracking-wide font-medium">Source availability</span>
              {sourceAvailability.map(({ key, label, source }) => (
                <MailOverviewSourceState key={key} sourceKey={key} label={label} source={source} />
              ))}
            </div>
          )}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <div
                className={`w-2.5 h-2.5 rounded-full ${status.running ? 'bg-green-500 animate-pulse' : statusKnown ? 'bg-red-500' : 'bg-amber-500'}`}
              />
              <span className={`font-semibold text-sm ${status.running ? 'text-green-400' : statusKnown ? 'text-red-400' : 'text-amber-400'}`}>
                {statusLabel}
              </span>
            </div>
            <div className="flex items-center gap-2">
              {status.running ? (
                <Tooltip>
                  <TooltipTrigger>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-zinc-500 hover:text-amber-400 hover:bg-amber-400/10"
                      onClick={() => actionMutation.mutate('stop')}
                      disabled={actionMutation.isPending || !statusKnown}
                    >
                      <Square className="w-4 h-4" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>Stop</TooltipContent>
                </Tooltip>
              ) : (
                <Tooltip>
                  <TooltipTrigger>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-zinc-500 hover:text-green-400 hover:bg-green-400/10"
                      onClick={() => actionMutation.mutate('start')}
                      disabled={actionMutation.isPending || !statusKnown}
                    >
                      <Play className="w-4 h-4" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>Start</TooltipContent>
                </Tooltip>
              )}
              <Tooltip>
                <TooltipTrigger>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                    onClick={() => actionMutation.mutate('restart')}
                    disabled={actionMutation.isPending || !statusKnown}
                  >
                    <RotateCcw className="w-4 h-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Restart</TooltipContent>
              </Tooltip>
            </div>
          </div>

          {/* Listeners */}
          <div>
            <p className="text-zinc-500 text-xs uppercase tracking-wide mb-2 font-medium">
              Listeners
            </p>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
              {isUnavailableMailOverviewSource(listenersSource) ? (
                <div className="col-span-full rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-4 text-center">
                  <p className="text-xs text-red-300">Listener inventory is unavailable.</p>
                  <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{listenersSource?.error}</p>
                </div>
              ) : status.listeners.length > 0 ? (
                status.listeners.map((l) => (
                  <div
                    key={`${l.protocol}-${l.port}`}
                    className="bg-zinc-800/60 rounded-lg px-3 py-2 text-center"
                  >
                    <p className="text-white text-sm font-mono font-semibold">
                      :{l.port}
                    </p>
                    <p className="text-zinc-400 text-xs mt-0.5 uppercase">{l.protocol}</p>
                  </div>
                ))
              ) : (
                <p className="col-span-full rounded-lg border border-zinc-800 bg-zinc-800/30 px-3 py-4 text-center text-xs text-zinc-500">
                  No listeners were reported by this Stalwart installation.
                </p>
              )}
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Storage card */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <HardDrive className="w-4 h-4 text-amber-400" />
            Storage
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {isUnavailableMailOverviewSource(storageSource) ? (
            <div className="rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-4 text-center">
              <p className="text-xs text-red-300">Storage inventory is unavailable.</p>
              <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{storageSource?.error}</p>
            </div>
          ) : (
            <>
              <div className="flex items-center justify-between">
                <span className="text-zinc-400 text-sm font-mono truncate">{status.storage.path || 'No local storage path reported'}</span>
                <span className="text-white text-sm ml-4 flex-shrink-0">
                  {formatBytes(status.storage.used)}
                  {status.storage.total > 0 && (
                    <span className="text-zinc-500"> / {formatBytes(status.storage.total)}</span>
                  )}
                </span>
              </div>
              {status.storage.total > 0 && (
                <div className="space-y-1">
                  <div className="w-full bg-zinc-800 rounded-full h-2">
                    <div
                      className={`h-2 rounded-full transition-all duration-500 ${
                        storagePercent > 85 ? 'bg-red-500' : storagePercent > 60 ? 'bg-amber-500' : 'bg-blue-500'
                      }`}
                      style={{ width: `${storagePercent}%` }}
                    />
                  </div>
                  <p className="text-zinc-500 text-xs">{storagePercent}% used</p>
                </div>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Accounts Tab ──────────────────────────────────────────────────────────────

interface CreateAccountForm {
  email: string
  password: string
  name: string
  quota: string
}

const emptyAccountForm: CreateAccountForm = {
  email: '',
  password: '',
  name: '',
  quota: '5368709120',
}

const emptyAliasForm = { alias: '', destination: '' }

function PasswordDialog({ email, onClose, newPassword, setNewPassword, showPwVis, setShowPwVis, onUpdate, isPending }: {
  email: string | null; onClose: () => void; newPassword: string; setNewPassword: (v: string) => void
  showPwVis: boolean; setShowPwVis: (v: boolean | ((p: boolean) => boolean)) => void
  onUpdate: () => void; isPending: boolean
}) {
  const passwordQuery = useQuery<{ password: string }>({
    queryKey: ['mail', 'password', email],
    queryFn: () => api.get<{ password: string }>(`/mail/accounts/${encodeURIComponent(email!)}/password`),
    enabled: !!email,
  })
  const pwData = passwordQuery.data

  return (
    <Dialog open={!!email} onOpenChange={onClose}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Account Password</DialogTitle>
        </DialogHeader>
        <div className="py-2 space-y-4">
          <p className="text-zinc-400 text-xs font-mono">{email}</p>

          {/* Current Password */}
          <div>
            <label className="text-zinc-500 text-xs font-medium block mb-1">Current Password</label>
            <div className="flex items-center gap-2 bg-zinc-800 border border-zinc-700 rounded-lg px-3 py-2">
              {passwordQuery.isLoading ? (
                <Loader2 className="w-4 h-4 animate-spin text-zinc-500" />
              ) : passwordQuery.isError ? (
                <div className="flex-1">
                  <p className="text-xs text-red-300">Current password could not be loaded.</p>
                  <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{passwordQuery.error.message}</p>
                  <button type="button" className="mt-2 text-xs text-blue-400 hover:text-blue-300" onClick={() => { void passwordQuery.refetch() }} disabled={passwordQuery.isFetching}>Retry</button>
                </div>
              ) : (
                <code className="text-green-400 text-sm font-mono flex-1 select-all">{pwData?.password || '(no password set)'}</code>
              )}
              {pwData?.password && (
                <button
                  className="text-zinc-500 hover:text-white text-xs"
                  onClick={() => { navigator.clipboard.writeText(pwData.password); toast.success('Password copied') }}
                >
                  Copy
                </button>
              )}
            </div>
          </div>

          {/* New Password */}
          <div>
            <label className="text-zinc-500 text-xs font-medium block mb-1">New Password</label>
            <div className="relative">
              <input
                type={showPwVis ? 'text' : 'password'}
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder="Enter new password..."
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 pr-10 text-sm focus:outline-none focus:border-blue-500"
              />
              <button
                type="button"
                className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300"
                onClick={() => setShowPwVis((v: boolean) => !v)}
              >
                {showPwVis ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" className="text-zinc-400" onClick={onClose}>Close</Button>
          <Button
            className="bg-blue-600 hover:bg-blue-500 text-white"
            onClick={onUpdate}
            disabled={isPending || !newPassword}
          >
            {isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
            Update Password
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

interface AccountsTabProps {
  domains: string[]
}

function AccountsTab({ domains }: AccountsTabProps) {
  const queryClient = useQueryClient()
  const [selectedDomain, setSelectedDomain] = useState(domains[0] ?? '')
  const [showCreate, setShowCreate] = useState(false)
  const [showPasswordFor, setShowPasswordFor] = useState<string | null>(null)
  const [newPassword, setNewPassword] = useState('')
  const [showPwVis, setShowPwVis] = useState(false)
  const [form, setForm] = useState<CreateAccountForm>(emptyAccountForm)

  const handleCreateOpenChange = (open: boolean) => {
    setShowCreate(open)
    if (!open) {
      setForm(emptyAccountForm)
      setShowPwVis(false)
    }
  }

  const closePasswordDialog = () => {
    setShowPasswordFor(null)
    setNewPassword('')
    setShowPwVis(false)
  }

  const accountsQuery = useQuery<MailAccount[]>({
    queryKey: ['mail', 'accounts', selectedDomain],
    queryFn: () =>
      api.get<MailAccount[]>(`/mail/accounts${selectedDomain ? `?domain=${selectedDomain}` : ''}`),
    enabled: true,
  })
  const accounts = accountsQuery.data

  const createMutation = useMutation({
    mutationFn: () =>
      api.post('/mail/accounts', {
        email: form.email,
        password: form.password,
        name: form.name,
        quota: parseInt(form.quota, 10) || 0,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'accounts'] })
      toast.success('Mail account created')
      handleCreateOpenChange(false)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create account'),
  })

  const deleteMutation = useMutation({
    mutationFn: (email: string) => api.delete(`/mail/accounts/${encodeURIComponent(email)}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'accounts'] })
      toast.success('Account deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete account'),
  })

  const changePasswordMutation = useMutation({
    mutationFn: (email: string) =>
      api.put(`/mail/accounts/${encodeURIComponent(email)}/password`, { password: newPassword }),
    onSuccess: () => {
      toast.success('Password updated')
      closePasswordDialog()
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update password'),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2">
          {domains.length > 0 && (
            <select
              value={selectedDomain}
              onChange={(e) => setSelectedDomain(e.target.value)}
              className="bg-zinc-800 border border-zinc-700 text-white text-sm rounded-lg px-3 py-2 focus:outline-none focus:border-blue-500"
            >
              <option value="">All domains</option>
              {domains.map((d) => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
          )}
          <span className="text-zinc-500 text-sm">
            {accounts ? `${accounts.length} accounts` : ''}
          </span>
        </div>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={() => setShowCreate(true)}
          disabled={accountsQuery.isLoading || accountsQuery.isError}
        >
          <Plus className="w-4 h-4 mr-2" />
          New Account
        </Button>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {accountsQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : accountsQuery.isError ? (
            <QueryFailure title="Mail accounts could not be loaded. Mutating controls are paused." error={accountsQuery.error} onRetry={() => { void accountsQuery.refetch() }} isFetching={accountsQuery.isFetching} />
          ) : accounts && accounts.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[520px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">Email</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Name</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Quota</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Status</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {accounts.map((account) => {
                    const pct = quotaPercent(account.usedStorage, account.quota)
                    return (
                      <TableRow key={account.email} className="border-zinc-800 hover:bg-zinc-800/50">
                        <TableCell>
                          <span className="text-white text-sm font-medium font-mono">
                            {account.email}
                          </span>
                        </TableCell>
                        <TableCell>
                          <span className="text-zinc-300 text-sm">{account.name || '—'}</span>
                        </TableCell>
                        <TableCell>
                          <div className="space-y-1 min-w-32">
                            <span className="text-zinc-300 text-xs">
                              {formatQuota(account.usedStorage, account.quota)}
                            </span>
                            {account.quota > 0 && (
                              <div className="w-full bg-zinc-800 rounded-full h-1.5">
                                <div
                                  className={`h-1.5 rounded-full transition-all ${
                                    pct > 85 ? 'bg-red-500' : pct > 60 ? 'bg-amber-500' : 'bg-blue-500'
                                  }`}
                                  style={{ width: `${pct}%` }}
                                />
                              </div>
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge
                            className={`text-xs border ${
                              account.isEnabled
                                ? 'bg-green-500/10 text-green-400 border-green-500/20'
                                : 'bg-zinc-500/10 text-zinc-400 border-zinc-700'
                            }`}
                          >
                            {account.isEnabled ? 'Active' : 'Disabled'}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Tooltip>
                              <TooltipTrigger>
                                <Button
                                  aria-label={`Change password for ${account.email}`}
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                                  onClick={() => setShowPasswordFor(account.email)}
                                >
                                  <Eye className="w-3.5 h-3.5" />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>Change Password</TooltipContent>
                            </Tooltip>
                            <Tooltip>
                              <TooltipTrigger>
                                <Button
                                  aria-label={`Delete mail account ${account.email}`}
                                  variant="ghost"
                                  size="icon"
                                  className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                                  onClick={() => {
                                    if (window.confirm(`Delete mail account ${account.email}? This cannot be undone.`)) {
                                      deleteMutation.mutate(account.email)
                                    }
                                  }}
                                  disabled={deleteMutation.isPending}
                                >
                                  <Trash2 className="w-3.5 h-3.5" />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>Delete Account</TooltipContent>
                            </Tooltip>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <Users className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">No mail accounts found</p>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Create Account Dialog */}
      <Dialog open={showCreate} onOpenChange={handleCreateOpenChange}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Create Mail Account</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">Email address</label>
              <input
                type="email"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                placeholder="user@example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
              />
            </div>
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">Display name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="John Doe"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
              />
            </div>
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">Password</label>
              <div className="relative">
                <input
                  type={showPwVis ? 'text' : 'password'}
                  value={form.password}
                  onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                  placeholder="••••••••"
                  className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 pr-10 text-sm focus:outline-none focus:border-blue-500"
                />
                <button
                  type="button"
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300"
                  onClick={() => setShowPwVis((v) => !v)}
                >
                  {showPwVis ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>
            </div>
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">
                Quota (bytes, 0 = unlimited)
              </label>
              <input
                type="number"
                value={form.quota}
                onChange={(e) => setForm((f) => ({ ...f, quota: e.target.value }))}
                placeholder="5368709120"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
              />
              <p className="text-zinc-600 text-xs mt-1">
                {parseInt(form.quota, 10) > 0 ? formatBytes(parseInt(form.quota, 10)) : 'Unlimited'}
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" className="text-zinc-400" onClick={() => handleCreateOpenChange(false)}>
              Cancel
            </Button>
            <Button
              className="bg-blue-600 hover:bg-blue-500 text-white"
              onClick={() => createMutation.mutate()}
              disabled={createMutation.isPending || !form.email || !form.password}
            >
              {createMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Create Account
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* View & Change Password Dialog */}
      <PasswordDialog
        email={showPasswordFor}
        onClose={closePasswordDialog}
        newPassword={newPassword}
        setNewPassword={setNewPassword}
        showPwVis={showPwVis}
        setShowPwVis={setShowPwVis}
        onUpdate={() => showPasswordFor && changePasswordMutation.mutate(showPasswordFor)}
        isPending={changePasswordMutation.isPending}
      />
    </div>
  )
}

// ─── Aliases Tab ───────────────────────────────────────────────────────────────

function AliasesTab() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState(emptyAliasForm)

  const handleCreateOpenChange = (open: boolean) => {
    setShowCreate(open)
    if (!open) setForm(emptyAliasForm)
  }

  const aliasesQuery = useQuery<MailAlias[]>({
    queryKey: ['mail', 'aliases'],
    queryFn: () => api.get<MailAlias[]>('/mail/aliases'),
  })
  const aliases = aliasesQuery.data

  const createMutation = useMutation({
    mutationFn: () => api.post('/mail/aliases', { alias: form.alias, destination: form.destination }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'aliases'] })
      toast.success('Alias created')
      handleCreateOpenChange(false)
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create alias'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/mail/aliases/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'aliases'] })
      toast.success('Alias deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete alias'),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-zinc-500 text-sm">
          {aliases ? `${aliases.length} aliases configured` : ''}
        </span>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={() => setShowCreate(true)}
          disabled={aliasesQuery.isLoading || aliasesQuery.isError}
        >
          <Plus className="w-4 h-4 mr-2" />
          New Alias
        </Button>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {aliasesQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : aliasesQuery.isError ? (
            <QueryFailure title="Mail aliases could not be loaded. Mutating controls are paused." error={aliasesQuery.error} onRetry={() => { void aliasesQuery.refetch() }} isFetching={aliasesQuery.isFetching} />
          ) : aliases && aliases.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[420px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">Alias</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Destination</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {aliases.map((alias) => (
                    <TableRow key={alias.id} className="border-zinc-800 hover:bg-zinc-800/50">
                      <TableCell>
                        <span className="text-white text-sm font-mono">{alias.alias}</span>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-300 text-sm font-mono">{alias.destination}</span>
                      </TableCell>
                      <TableCell className="text-right">
                        <Tooltip>
                          <TooltipTrigger>
                            <Button
                              aria-label={`Delete mail alias ${alias.alias}`}
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                              onClick={() => {
                                if (window.confirm(`Delete mail alias ${alias.alias}?`)) {
                                  deleteMutation.mutate(alias.id)
                                }
                              }}
                              disabled={deleteMutation.isPending}
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>Delete Alias</TooltipContent>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <AtSign className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">No aliases configured</p>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={showCreate} onOpenChange={handleCreateOpenChange}>
        <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Create Mail Alias</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">
                Alias address
              </label>
              <input
                type="email"
                value={form.alias}
                onChange={(e) => setForm((f) => ({ ...f, alias: e.target.value }))}
                placeholder="alias@example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
              />
            </div>
            <div>
              <label className="text-zinc-400 text-xs font-medium block mb-1.5">
                Destination address
              </label>
              <input
                type="email"
                value={form.destination}
                onChange={(e) => setForm((f) => ({ ...f, destination: e.target.value }))}
                placeholder="real@example.com"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" className="text-zinc-400" onClick={() => handleCreateOpenChange(false)}>
              Cancel
            </Button>
            <Button
              className="bg-blue-600 hover:bg-blue-500 text-white"
              onClick={() => createMutation.mutate()}
              disabled={createMutation.isPending || !form.alias || !form.destination}
            >
              {createMutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Create Alias
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── DNS Health Tab ────────────────────────────────────────────────────────────

function DnsCheckRow({ label, valid, value, icon: Icon }: {
  label: string
  valid: boolean
  value: string
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <div className="flex items-start gap-3 py-2.5 border-b border-zinc-800 last:border-0">
      <div className={`mt-0.5 flex-shrink-0 ${valid ? 'text-green-400' : 'text-red-400'}`}>
        {valid
          ? <CheckCircle2 className="w-4 h-4" />
          : <XCircle className="w-4 h-4" />}
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <Icon className="w-3.5 h-3.5 text-zinc-500" />
          <span className="text-zinc-300 text-sm font-medium">{label}</span>
        </div>
        {value && (
          <p className="text-zinc-500 text-xs font-mono mt-0.5 truncate" title={value}>{value}</p>
        )}
      </div>
    </div>
  )
}

function DomainDnsCard({ domain }: { domain: string }) {
  const dnsQuery = useQuery<MailDNSCheck>({
    queryKey: ['mail', 'dns', domain],
    queryFn: () => api.get<MailDNSCheck>(`/mail/dns-check/${domain}`),
    staleTime: 60_000,
  })
  const check = dnsQuery.data

  if (dnsQuery.isLoading) {
    return (
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="pt-5">
          <Skeleton className="h-40 w-full bg-zinc-800" />
        </CardContent>
      </Card>
    )
  }

  if (dnsQuery.isError) {
    return (
      <Card className="bg-zinc-900 border-red-500/20">
        <CardContent className="p-0">
          <QueryFailure title={`DNS health for ${domain} could not be loaded.`} error={dnsQuery.error} onRetry={() => { void dnsQuery.refetch() }} isFetching={dnsQuery.isFetching} />
        </CardContent>
      </Card>
    )
  }

  if (!check) return null

  const scoreColor =
    check.score >= 80 ? 'text-green-400' :
    check.score >= 50 ? 'text-amber-400' :
    'text-red-400'

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-white text-sm font-semibold flex items-center gap-2">
            <Globe2 className="w-4 h-4 text-blue-400" />
            {domain}
          </CardTitle>
          <div className="flex items-center gap-2">
            <span className={`text-lg font-bold tabular-nums ${scoreColor}`}>
              {check.score}
              <span className="text-xs font-normal text-zinc-500">/100</span>
            </span>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 text-zinc-500 hover:text-white"
              onClick={() => { void dnsQuery.refetch() }}
              disabled={dnsQuery.isFetching}
              aria-label={`Refresh DNS health for ${domain}`}
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-2">
        <DnsCheckRow
          label="MX"
          valid={check.mx?.valid ?? false}
          value={check.mx?.mxRecords?.map((r: {host: string}) => r.host).join(', ') ?? 'Not found'}
          icon={Mail}
        />
        <DnsCheckRow
          label="SPF"
          valid={check.spf?.valid ?? false}
          value={check.spf?.value ?? 'Not found'}
          icon={Shield}
        />
        <DnsCheckRow
          label={`DKIM (${String(check.dkim?.value || '').substring(0, 20) || 'default'})`}
          valid={check.dkim?.valid ?? false}
          value={String(check.dkim?.value || 'Not found').substring(0, 60)}
          icon={Shield}
        />
        <DnsCheckRow
          label="DMARC"
          valid={check.dmarc?.valid ?? false}
          value={String(check.dmarc?.value || 'Not found').substring(0, 60)}
          icon={Shield}
        />

        {check.suggestions && check.suggestions.length > 0 && (
          <div className="mt-3 pt-3 border-t border-zinc-800">
            <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide mb-2">
              Suggestions
            </p>
            <ul className="space-y-1.5">
              {check.suggestions.map((s, i) => (
                <li key={i} className="flex items-start gap-2">
                  <AlertCircle className="w-3.5 h-3.5 text-amber-400 flex-shrink-0 mt-0.5" />
                  <span className="text-zinc-400 text-xs">{s}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// Minimal globe icon used only in DNS tab to avoid name collision with Domains page
function Globe2({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="2" y1="12" x2="22" y2="12" />
      <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
    </svg>
  )
}

interface DnsTabProps {
  domains: string[]
  isLoading: boolean
  error: Error | null
  isFetching: boolean
  onRetry: () => void
}

function DnsTab({ domains, isLoading, error, isFetching, onRetry }: DnsTabProps) {
  if (isLoading) {
    return <Skeleton className="h-48 w-full bg-zinc-800" />
  }

  if (error) {
    return <QueryFailure title="Mail domains could not be loaded. DNS health is unavailable." error={error} onRetry={onRetry} isFetching={isFetching} />
  }

  if (domains.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
        <Shield className="w-8 h-8 mb-3 opacity-50" />
        <p className="text-sm">No mail domains found</p>
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {domains.map((domain) => (
        <DomainDnsCard key={domain} domain={domain} />
      ))}
    </div>
  )
}

// ─── Queue Tab ─────────────────────────────────────────────────────────────────

function QueueTab() {
  const queryClient = useQueryClient()

  const queueQuery = useQuery<MailQueueItem[]>({
    queryKey: ['mail', 'queue'],
    queryFn: () => api.get<MailQueueItem[]>('/mail/queue'),
    refetchInterval: 30_000,
  })
  const queue = queueQuery.data

  const retryMutation = useMutation({
    mutationFn: (id: string) => api.post(`/mail/queue/${id}/retry`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'queue'] })
      toast.success('Message queued for retry')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to retry message'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/mail/queue/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mail', 'queue'] })
      toast.success('Message removed from queue')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to remove from queue'),
  })

  const statusColor = (status: string) => {
    const s = status.toLowerCase()
    if (s === 'delivered') return 'bg-green-500/10 text-green-400 border-green-500/20'
    if (s === 'deferred' || s === 'retry') return 'bg-amber-500/10 text-amber-400 border-amber-500/20'
    if (s === 'failed' || s === 'bounced') return 'bg-red-500/10 text-red-400 border-red-500/20'
    return 'bg-zinc-500/10 text-zinc-400 border-zinc-700'
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-zinc-500 text-sm">
          {queue ? `${queue.length} messages in queue` : ''}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="text-zinc-400 hover:text-white"
          onClick={() => { void queueQuery.refetch() }}
          disabled={queueQuery.isFetching}
        >
          <RefreshCw className="w-3.5 h-3.5 mr-2" />
          Refresh
        </Button>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {queueQuery.isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : queueQuery.isError ? (
            <QueryFailure title="Mail queue could not be loaded. Queue controls are paused." error={queueQuery.error} onRetry={() => { void queueQuery.refetch() }} isFetching={queueQuery.isFetching} />
          ) : queue && queue.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[600px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 text-xs font-medium">From</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">To</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Subject</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Status</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium">Created</TableHead>
                    <TableHead className="text-zinc-500 text-xs font-medium text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {queue.map((item) => (
                    <TableRow key={item.id} className="border-zinc-800 hover:bg-zinc-800/50">
                      <TableCell>
                        <span className="text-zinc-300 text-xs font-mono truncate max-w-32 block">
                          {item.from}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-300 text-xs font-mono truncate max-w-32 block">
                          {item.to}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-300 text-xs truncate max-w-40 block">
                          {item.subject || '(no subject)'}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Badge className={`text-xs border ${statusColor(item.status)}`}>
                          {item.status}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <span className="text-zinc-500 text-xs">{formatDate(item.created)}</span>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Tooltip>
                            <TooltipTrigger>
                              <Button
                                aria-label={`Retry queued message ${item.id}`}
                                variant="ghost"
                                size="icon"
                                className="h-7 w-7 text-zinc-500 hover:text-blue-400 hover:bg-blue-400/10"
                                onClick={() => retryMutation.mutate(item.id)}
                                disabled={retryMutation.isPending}
                              >
                                <RotateCcw className="w-3.5 h-3.5" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>Retry</TooltipContent>
                          </Tooltip>
                          <Tooltip>
                            <TooltipTrigger>
                              <Button
                                aria-label={`Delete queued message ${item.id}`}
                                variant="ghost"
                                size="icon"
                                className="h-7 w-7 text-zinc-500 hover:text-red-400 hover:bg-red-400/10"
                                onClick={() => {
                                  if (window.confirm(`Remove queued message ${item.id}?`)) {
                                    deleteMutation.mutate(item.id)
                                  }
                                }}
                                disabled={deleteMutation.isPending}
                              >
                                <Trash2 className="w-3.5 h-3.5" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>Delete</TooltipContent>
                          </Tooltip>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <Send className="w-8 h-8 mb-3 opacity-50" />
              <p className="text-sm">Mail queue is empty</p>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Mail Page (root) ──────────────────────────────────────────────────────────

export default function MailPage() {
  const domainsQuery = useQuery<string[]>({
    queryKey: ['mail', 'domains'],
    queryFn: async () => {
      const resp = await api.get<Array<{ name: string; description?: string }>>('/mail/domains')
      return resp.map((d) => d.name)
    },
    staleTime: 60_000,
  })

  const domainList = domainsQuery.data ?? []

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-white text-xl font-bold flex items-center gap-2">
          <Mail className="w-5 h-5 text-blue-400" />
          Mail Management
        </h2>
        <p className="text-zinc-500 text-sm mt-0.5">
          Stalwart Mail Server — accounts, aliases, DNS health &amp; queue
        </p>
      </div>

      {domainsQuery.isError && (
        <Card className="border-amber-500/25 bg-amber-500/[0.05]">
          <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="flex min-w-0 items-start gap-3">
              <AlertCircle className="mt-0.5 size-5 shrink-0 text-amber-400" />
              <div>
                <p className="text-sm text-amber-300">Mail domain inventory could not be loaded. Domain filtering and DNS health are unavailable.</p>
                <p className="mt-1 break-words font-mono text-xs text-amber-400/70">{domainsQuery.error.message}</p>
              </div>
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => { void domainsQuery.refetch() }} disabled={domainsQuery.isFetching}>
              <RefreshCw className={`size-3.5 ${domainsQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
            </Button>
          </CardContent>
        </Card>
      )}

      <Tabs defaultValue="overview" className="space-y-4">
        <TabsList className="bg-zinc-900 border border-zinc-800">
          <TabsTrigger
            value="overview"
            className="data-[state=active]:bg-zinc-800 data-[state=active]:text-white text-zinc-400 text-xs sm:text-sm"
          >
            <Server className="w-3.5 h-3.5 mr-1.5 hidden sm:inline-block" />
            Overview
          </TabsTrigger>
          <TabsTrigger
            value="accounts"
            className="data-[state=active]:bg-zinc-800 data-[state=active]:text-white text-zinc-400 text-xs sm:text-sm"
          >
            <Users className="w-3.5 h-3.5 mr-1.5 hidden sm:inline-block" />
            Accounts
          </TabsTrigger>
          <TabsTrigger
            value="aliases"
            className="data-[state=active]:bg-zinc-800 data-[state=active]:text-white text-zinc-400 text-xs sm:text-sm"
          >
            <AtSign className="w-3.5 h-3.5 mr-1.5 hidden sm:inline-block" />
            Aliases
          </TabsTrigger>
          <TabsTrigger
            value="dns"
            className="data-[state=active]:bg-zinc-800 data-[state=active]:text-white text-zinc-400 text-xs sm:text-sm"
          >
            <Shield className="w-3.5 h-3.5 mr-1.5 hidden sm:inline-block" />
            DNS Health
          </TabsTrigger>
          <TabsTrigger
            value="queue"
            className="data-[state=active]:bg-zinc-800 data-[state=active]:text-white text-zinc-400 text-xs sm:text-sm"
          >
            <Send className="w-3.5 h-3.5 mr-1.5 hidden sm:inline-block" />
            Queue
          </TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <OverviewTab />
        </TabsContent>

        <TabsContent value="accounts">
          <AccountsTab domains={domainList} />
        </TabsContent>

        <TabsContent value="aliases">
          <AliasesTab />
        </TabsContent>

        <TabsContent value="dns">
          <DnsTab
            domains={domainList}
            isLoading={domainsQuery.isLoading}
            error={domainsQuery.error}
            isFetching={domainsQuery.isFetching}
            onRetry={() => { void domainsQuery.refetch() }}
          />
        </TabsContent>

        <TabsContent value="queue">
          <QueueTab />
        </TabsContent>
      </Tabs>
    </div>
  )
}

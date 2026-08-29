import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Settings as SettingsIcon,
  Server,
  HardDrive,
  Cpu,
  MemoryStick,
  Info,
  Loader2,
  Save,
  RefreshCw,
  Shield,
  Copy,
  AlertTriangle,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { PortableConfigurationSection } from '@/components/settings/PortableConfigurationSection'
import { api } from '@/lib/api'
import {
  DEFAULT_MAIL_SETTINGS,
  resolveMailSettings,
  validateMailSettings,
  type MailSettings,
} from '@/lib/mailSettings'
import { toast } from 'sonner'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface PanelSettings extends MailSettings {
  hostnameDisplay: string
  adminEmail: string
  notifyOnLogin: boolean
  notifyOnError: boolean
  notifyOnDeployment: boolean
}

interface SystemInfo {
  os: string
  kernel: string
  nginx: string
  php: string[]
  postgresql: string
  hostname: string
  arch: string
  panel_version: string
  build_commit: string
  build_date: string
}

interface DiskPartition {
  mount: string
  total: number
  used: number
  free: number
  percentage: number
}

interface CPUStats {
  usage: number
  cores: number
  model: string
}

interface MemoryStats {
  total: number
  used: number
  free: number
  percentage: number
}

interface ServerHealth {
  uptime: number
  cpu: CPUStats
  memory: MemoryStats
  disk: DiskPartition[]
  hostname: string
  os: string
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(0)} MB`
  return `${(bytes / 1024).toFixed(0)} KB`
}

function DiskBar({ percent }: { percent: number }) {
  const color =
    percent >= 90
      ? 'bg-red-500'
      : percent >= 75
      ? 'bg-amber-500'
      : 'bg-blue-500'
  return (
    <div className="h-1.5 w-full bg-zinc-700 rounded-full overflow-hidden">
      <div className={`h-full ${color} rounded-full`} style={{ width: `${percent}%` }} />
    </div>
  )
}

// ─── Settings Form ────────────────────────────────────────────────────────────

interface SettingsFormProps {
  initial: PanelSettings
}

function SettingsForm({ initial }: SettingsFormProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<PanelSettings>(initial)
  const [dirty, setDirty] = useState(false)
  const mailSettingsError = validateMailSettings(form)

  // No effect needed: SettingsForm is remounted via key prop when `initial` changes

  const mutation = useMutation({
    mutationFn: (data: PanelSettings) =>
      api.put('/settings', {
        hostnameDisplay: data.hostnameDisplay,
        adminEmail: data.adminEmail,
        notifyOnLogin: String(data.notifyOnLogin),
        notifyOnError: String(data.notifyOnError),
        notifyOnDeployment: String(data.notifyOnDeployment),
        webmail_url: data.webmailUrl.trim(),
        mail_admin_url: data.mailAdminUrl.trim(),
        mail_server_host: data.mailServerHost.trim().toLowerCase(),
        mail_imap_port: data.imapPort.trim(),
        mail_smtp_starttls_port: data.smtpStarttlsPort.trim(),
        mail_smtp_ssl_port: data.smtpSslPort.trim(),
      } as Record<string, string>),
    onSuccess: async () => {
      toast.success('Settings saved')
      setDirty(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['settings', 'system'] }),
        queryClient.invalidateQueries({ queryKey: ['settings', 'mail'] }),
      ])
    },
    onError: (err: Error) => {
      toast.error(`Failed to save settings: ${err.message}`)
    },
  })

  function handleChange<K extends keyof PanelSettings>(key: K, value: PanelSettings[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
    setDirty(true)
  }

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
          <SettingsIcon className="w-4 h-4 text-zinc-400" />
          System Settings
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        {/* Hostname / Admin email */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label className="text-zinc-300">Hostname Display Name</Label>
            <Input
              value={form.hostnameDisplay}
              onChange={(e) => handleChange('hostnameDisplay', e.target.value)}
              placeholder="My Server"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
            />
            <p className="text-zinc-500 text-xs">Shown in the panel header</p>
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Admin Email</Label>
            <Input
              type="email"
              value={form.adminEmail}
              onChange={(e) => handleChange('adminEmail', e.target.value)}
              placeholder="admin@example.com"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
            />
            <p className="text-zinc-500 text-xs">Used for system notifications</p>
          </div>
        </div>

        {/* Mail access */}
        <div className="space-y-3 border-t border-zinc-800 pt-5">
          <div>
            <p className="text-zinc-300 text-sm font-medium">Mail Access</p>
            <p className="mt-1 text-zinc-500 text-xs">Used by the Webmail page and email-client setup guides.</p>
          </div>
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="space-y-2">
              <Label className="text-zinc-300">Webmail URL</Label>
              <Input
                type="url"
                value={form.webmailUrl}
                onChange={(e) => handleChange('webmailUrl', e.target.value)}
                placeholder="https://webmail.example.com"
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-300">Mail Admin URL</Label>
              <Input
                type="url"
                value={form.mailAdminUrl}
                onChange={(e) => handleChange('mailAdminUrl', e.target.value)}
                placeholder="https://mail-admin.example.com"
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="space-y-2">
              <Label className="text-zinc-300">IMAP/SMTP Host</Label>
              <Input
                value={form.mailServerHost}
                onChange={(e) => handleChange('mailServerHost', e.target.value)}
                placeholder="mail.example.com"
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-300">IMAP SSL Port</Label>
              <Input
                inputMode="numeric"
                value={form.imapPort}
                onChange={(e) => handleChange('imapPort', e.target.value.replace(/\D/g, '').slice(0, 5))}
                placeholder={DEFAULT_MAIL_SETTINGS.imapPort}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-300">SMTP STARTTLS Port</Label>
              <Input
                inputMode="numeric"
                value={form.smtpStarttlsPort}
                onChange={(e) => handleChange('smtpStarttlsPort', e.target.value.replace(/\D/g, '').slice(0, 5))}
                placeholder={DEFAULT_MAIL_SETTINGS.smtpStarttlsPort}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
            <div className="space-y-2">
              <Label className="text-zinc-300">SMTP SSL Port</Label>
              <Input
                inputMode="numeric"
                value={form.smtpSslPort}
                onChange={(e) => handleChange('smtpSslPort', e.target.value.replace(/\D/g, '').slice(0, 5))}
                placeholder={DEFAULT_MAIL_SETTINGS.smtpSslPort}
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
              />
            </div>
          </div>
          {mailSettingsError && <p role="alert" className="text-xs text-amber-400">{mailSettingsError}</p>}
        </div>

        {/* Notifications */}
        <div>
          <p className="text-zinc-300 text-sm font-medium mb-3">Notification Preferences</p>
          <div className="space-y-3">
            {(
              [
                { key: 'notifyOnLogin', label: 'Notify on login', desc: 'Send email on each successful login' },
                { key: 'notifyOnError', label: 'Notify on system error', desc: 'Alert when services fail or error' },
                { key: 'notifyOnDeployment', label: 'Notify on deployment', desc: 'Alert on deploy start / finish' },
              ] as const
            ).map(({ key, label, desc }) => (
              <label key={key} className="flex items-start gap-3 cursor-pointer group">
                <div className="relative mt-0.5">
                  <input
                    type="checkbox"
                    checked={form[key]}
                    onChange={(e) => handleChange(key, e.target.checked)}
                    className="sr-only"
                  />
                  <div
                    className={`w-4 h-4 rounded border flex items-center justify-center transition-colors ${
                      form[key]
                        ? 'bg-blue-600 border-blue-600'
                        : 'bg-zinc-800 border-zinc-700 group-hover:border-zinc-500'
                    }`}
                  >
                    {form[key] && (
                      <svg className="w-2.5 h-2.5 text-white" fill="none" viewBox="0 0 10 10">
                        <path d="M1.5 5L4 7.5L8.5 2.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                      </svg>
                    )}
                  </div>
                </div>
                <div>
                  <p className="text-zinc-200 text-sm">{label}</p>
                  <p className="text-zinc-500 text-xs">{desc}</p>
                </div>
              </label>
            ))}
          </div>
        </div>

        {/* Save */}
        <div className="flex justify-end pt-2 border-t border-zinc-800">
          <Button
            onClick={() => mutation.mutate(form)}
            disabled={!dirty || mutation.isPending || Boolean(mailSettingsError)}
            className="bg-blue-600 hover:bg-blue-700 disabled:opacity-50"
          >
            {mutation.isPending ? (
              <Loader2 className="w-4 h-4 mr-2 animate-spin" />
            ) : (
              <Save className="w-4 h-4 mr-2" />
            )}
            Save Settings
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

// ─── System Info Card ─────────────────────────────────────────────────────────

function InfoRow({ label, value }: { label: string; value?: string }) {
  return (
    <div className="flex items-start justify-between gap-3 py-2 border-b border-zinc-800 last:border-0">
      <span className="text-zinc-400 text-sm shrink-0">{label}</span>
      <span className="text-white text-sm font-mono text-right break-all min-w-0">{value ?? '—'}</span>
    </div>
  )
}

interface SystemInfoCardProps {
  data?: SystemInfo
  isLoading: boolean
  error?: Error | null
  retry: () => void
  retrying: boolean
}

function QueryCardFailure({ title, error, retry, retrying }: { title: string; error?: Error | null; retry: () => void; retrying: boolean }) {
  return (
    <div className="space-y-3 rounded-lg border border-amber-500/20 bg-amber-500/[0.05] p-3">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-400" />
        <div>
          <p className="text-xs font-medium text-amber-200">{title}</p>
          <p className="mt-1 break-words text-[11px] text-amber-200/60">{error?.message ?? 'The server returned an unknown error.'}</p>
        </div>
      </div>
      <Button type="button" variant="outline" size="sm" onClick={retry} disabled={retrying} className="border-amber-500/30 text-amber-100">
        <RefreshCw className={`mr-2 size-3.5 ${retrying ? 'animate-spin' : ''}`} />
        Retry
      </Button>
    </div>
  )
}

function SystemInfoCard({ data, isLoading, error, retry, retrying }: SystemInfoCardProps) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
          <Server className="w-4 h-4 text-zinc-400" />
          System Information
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 7 }).map((_, i) => (
              <div key={i} className="flex justify-between py-2 border-b border-zinc-800">
                <Skeleton className="h-4 w-24 bg-zinc-800" />
                <Skeleton className="h-4 w-32 bg-zinc-800" />
              </div>
            ))}
          </div>
        ) : error ? (
          <QueryCardFailure title="System information could not be loaded" error={error} retry={retry} retrying={retrying} />
        ) : (
          <div>
            <InfoRow label="Operating System" value={data?.os} />
            <InfoRow label="Kernel" value={data?.kernel} />
            <InfoRow label="Hostname" value={data?.hostname} />
            <InfoRow label="Architecture" value={data?.arch} />
            <InfoRow label="Nginx" value={data?.nginx} />
            <InfoRow label="PHP" value={data?.php?.join(', ')} />
            <InfoRow label="PostgreSQL" value={data?.postgresql} />
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Server Health Card ───────────────────────────────────────────────────────

interface ServerHealthCardProps {
  data?: ServerHealth
  isLoading: boolean
  error?: Error | null
  retry: () => void
  retrying: boolean
}

function ServerHealthCard({ data, isLoading, error, retry, retrying }: ServerHealthCardProps) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
          <Cpu className="w-4 h-4 text-zinc-400" />
          Server Health
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full bg-zinc-800 rounded" />
            ))}
          </div>
        ) : error ? (
          <QueryCardFailure title="Server health could not be loaded" error={error} retry={retry} retrying={retrying} />
        ) : (
          <>
            {/* Uptime + RAM */}
            <div className="grid grid-cols-2 gap-3">
              <div className="bg-zinc-800 rounded-lg p-3">
                <p className="text-zinc-500 text-xs mb-1">Uptime</p>
                <p className="text-white font-semibold text-sm">
                  {data?.uptime !== undefined ? formatUptime(data.uptime) : '—'}
                </p>
              </div>
              <div className="bg-zinc-800 rounded-lg p-3">
                <p className="text-zinc-500 text-xs mb-1">Total RAM</p>
                <p className="text-white font-semibold text-sm">
                  {data?.memory?.total !== undefined ? formatBytes(data.memory.total) : '—'}
                </p>
              </div>
            </div>

            {/* CPU model */}
            <div className="bg-zinc-800 rounded-lg p-3 flex items-center gap-3">
              <MemoryStick className="w-4 h-4 text-zinc-400 flex-shrink-0" />
              <div>
                <p className="text-zinc-500 text-xs">CPU Model</p>
                <p className="text-white text-sm leading-tight">{data?.cpu?.model ?? '—'}</p>
              </div>
            </div>

            {/* Disk partitions */}
            {(data?.disk?.length ?? 0) > 0 && (
              <div>
                <p className="text-zinc-400 text-xs font-medium mb-2 flex items-center gap-1">
                  <HardDrive className="w-3.5 h-3.5" />
                  Disk Partitions
                </p>
                <div className="space-y-3">
                  {data!.disk.map((disk) => (
                    <div key={disk.mount}>
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-zinc-300 text-xs font-mono">{disk.mount}</span>
                        <span className="text-zinc-500 text-xs">
                          {formatBytes(disk.used)} / {formatBytes(disk.total)} ({disk.percentage}%)
                        </span>
                      </div>
                      <DiskBar percent={disk.percentage} />
                    </div>
                  ))}
                </div>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Panel Info Card ──────────────────────────────────────────────────────────

function PanelInfoCard({ data, isLoading, error, retry, retrying }: { data?: SystemInfo; isLoading: boolean; error?: Error | null; retry: () => void; retrying: boolean }) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
          <Info className="w-4 h-4 text-zinc-400" />
          Panel Info
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 3 }).map((_, index) => <Skeleton key={index} className="h-8 w-full bg-zinc-800" />)}
          </div>
        ) : error ? (
          <QueryCardFailure title="Panel build information could not be loaded" error={error} retry={retry} retrying={retrying} />
        ) : (
          <>
            <InfoRow label="Version" value={data?.panel_version || 'Unavailable'} />
            {data?.build_commit && data.build_commit !== 'dev' && <InfoRow label="Build Commit" value={data.build_commit} />}
            {data?.build_date && data.build_date !== 'unknown' && <InfoRow label="Build Date" value={data.build_date} />}
          </>
        )}
        <InfoRow label="Binary" value="hserver-panel" />
      </CardContent>
    </Card>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

// Raw /api/settings response is a flat key→value map
type RawSettings = Record<string, string>

function parseSettings(raw: RawSettings): PanelSettings {
  const mail = resolveMailSettings(raw)
  return {
    hostnameDisplay: raw['hostnameDisplay'] ?? '',
    adminEmail: raw['adminEmail'] ?? '',
    notifyOnLogin: raw['notifyOnLogin'] === 'true',
    notifyOnError: raw['notifyOnError'] === 'true',
    notifyOnDeployment: raw['notifyOnDeployment'] === 'true',
    ...mail,
  }
}

interface TwoFactorStatus {
  enabled: boolean
  setup_pending: boolean
}

interface TwoFactorSetup {
  secret: string
  qrCode: string
  otpAuthUrl: string
  recoveryCodes: string[]
}

function TwoFactorSection() {
  const queryClient = useQueryClient()
  const [setupData, setSetupData] = useState<TwoFactorSetup | null>(null)
  const [code, setCode] = useState('')
  const statusQuery = useQuery<TwoFactorStatus>({
    queryKey: ['auth', '2fa-status'],
    queryFn: () => api.get('/auth/2fa/status'),
    staleTime: 30_000,
  })
  const enabled = statusQuery.data?.enabled === true
  const setupPending = statusQuery.data?.setup_pending === true

  const setStatus = (status: TwoFactorStatus) => {
    queryClient.setQueryData(['auth', '2fa-status'], status)
  }

  const setupMutation = useMutation({
    mutationFn: () => api.post<TwoFactorSetup>('/auth/2fa/setup'),
    onSuccess: (data) => {
      setSetupData(data)
      setCode('')
      setStatus({ enabled: false, setup_pending: true })
    },
    onError: async (error: Error) => {
      toast.error(error.message || 'Failed to generate 2FA secret')
      await statusQuery.refetch()
    },
  })

  const verifyMutation = useMutation({
    mutationFn: () => api.post('/auth/2fa/verify', { code }),
    onSuccess: () => {
      setStatus({ enabled: true, setup_pending: false })
      setSetupData(null)
      setCode('')
      toast.success('Two-factor authentication enabled!')
    },
    onError: () => toast.error('Invalid code. Please try again.'),
  })

  const disableMutation = useMutation({
    mutationFn: () => api.post('/auth/2fa/disable', { code }),
    onSuccess: () => {
      setStatus({ enabled: false, setup_pending: false })
      setCode('')
      toast.success('Two-factor authentication disabled')
    },
    onError: () => toast.error('Invalid code'),
  })

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-sm font-semibold text-zinc-300 flex items-center gap-2">
          <Shield className="w-4 h-4 text-blue-400" />
          Two-Factor Authentication
          {enabled && <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs ml-2">Enabled</Badge>}
          {!enabled && setupPending && <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20 text-xs ml-2">Setup pending</Badge>}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {statusQuery.isLoading && (
          <div className="flex items-center gap-2 py-2 text-sm text-zinc-500">
            <Loader2 className="size-4 animate-spin" />
            Checking your current 2FA state…
          </div>
        )}

        {statusQuery.isError && (
          <div className="flex flex-col gap-3 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-sm text-amber-300">Current 2FA state could not be verified. Setup controls are paused to protect an existing configuration.</p>
            <Button variant="outline" size="sm" onClick={() => statusQuery.refetch()} disabled={statusQuery.isFetching}>
              {statusQuery.isFetching ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
              Retry
            </Button>
          </div>
        )}

        {statusQuery.isSuccess && !setupData && !enabled && (
          <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
            <div>
              <p className="text-zinc-400 text-sm">{setupPending ? 'A previous 2FA setup was not completed.' : 'Add an extra layer of security to your account with TOTP.'}</p>
              {setupPending && <p className="mt-1 text-xs text-amber-400/80">Restart setup to receive a fresh QR code and new one-time recovery codes.</p>}
            </div>
            <Button
              size="sm"
              onClick={() => {
                if (!setupPending || window.confirm('Restart 2FA setup? The previous unverified QR code and recovery codes will stop working.')) {
                  setupMutation.mutate()
                }
              }}
              disabled={setupMutation.isPending}
              className="bg-blue-600 hover:bg-blue-500 text-white shrink-0"
            >
              {setupMutation.isPending ? 'Generating...' : setupPending ? 'Restart setup' : 'Enable 2FA'}
            </Button>
          </div>
        )}

        {setupData && (
          <div className="space-y-4">
            <p className="text-zinc-400 text-sm">{'Scan this QR code with your authenticator app (Google Authenticator, Authy, etc.):'}</p>
            {setupData.qrCode && (
              <div className="flex justify-center py-2">
                <img src={`data:image/png;base64,${setupData.qrCode}`} alt="QR Code" className="w-48 h-48 rounded-lg bg-white p-2" />
              </div>
            )}
            <div className="space-y-1">
              <label className="text-zinc-500 text-xs">{'Manual entry key:'}</label>
              <code className="block bg-zinc-800 border border-zinc-700 rounded px-3 py-2 text-green-400 text-sm font-mono select-all">{String(setupData.secret)}</code>
            </div>
            {setupData.recoveryCodes.length > 0 && (
              <div className="space-y-2 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-sm font-medium text-amber-300">Save your recovery codes now</p>
                    <p className="mt-1 text-xs text-amber-200/60">Each code works once. They will not be shown again after this setup screen.</p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      navigator.clipboard.writeText(setupData.recoveryCodes.join('\n'))
                        .then(() => toast.success('Recovery codes copied'))
                        .catch(() => toast.error('Could not copy recovery codes'))
                    }}
                  >
                    <Copy className="size-3.5" />
                    Copy all
                  </Button>
                </div>
                <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                  {setupData.recoveryCodes.map((recoveryCode) => (
                    <code key={recoveryCode} className="rounded border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-center font-mono text-xs text-zinc-200 select-all">
                      {recoveryCode}
                    </code>
                  ))}
                </div>
              </div>
            )}
            <div className="space-y-1">
              <label className="text-zinc-500 text-xs">{'Enter the 6-digit code from your app:'}</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  placeholder="000000"
                  maxLength={6}
                  className="bg-zinc-800 border border-zinc-700 text-white text-center font-mono text-lg tracking-widest rounded-lg px-4 py-2 w-40 focus:outline-none focus:border-blue-500"
                />
                <Button onClick={() => verifyMutation.mutate()} disabled={code.length !== 6 || verifyMutation.isPending} className="bg-green-600 hover:bg-green-500 text-white">
                  {verifyMutation.isPending ? 'Verifying...' : 'Verify & Enable'}
                </Button>
              </div>
            </div>
          </div>
        )}

        {statusQuery.isSuccess && enabled && (
          <div className="space-y-3">
            <p className="text-green-400 text-sm">{'✓ Two-factor authentication is active.'}</p>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder="Enter code to disable"
                maxLength={6}
                className="bg-zinc-800 border border-zinc-700 text-white font-mono rounded-lg px-3 py-2 w-40 focus:outline-none focus:border-blue-500"
              />
              <Button variant="outline" size="sm" onClick={() => disableMutation.mutate()} disabled={code.length !== 6 || disableMutation.isPending} className="border-red-500/30 text-red-400 hover:bg-red-500/10">
                {disableMutation.isPending ? 'Disabling…' : 'Disable 2FA'}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export default function SettingsPage() {
  const settingsQuery = useQuery<PanelSettings>({
    queryKey: ['settings', 'system'],
    queryFn: async () => parseSettings(await api.get<RawSettings>('/settings')),
    staleTime: 30_000,
  })
  const infoQuery = useQuery<SystemInfo>({
    queryKey: ['settings', 'system-info'],
    queryFn: () => api.get('/system/info'),
    staleTime: 30_000,
  })
  const healthQuery = useQuery<ServerHealth>({
    queryKey: ['settings', 'server-health'],
    queryFn: () => api.get('/system/stats'),
    staleTime: 30_000,
  })
  const refreshing = settingsQuery.isFetching || infoQuery.isFetching || healthQuery.isFetching

  if (settingsQuery.isError) {
    return (
      <div className="space-y-6">
        <div>
          <h2 className="text-white font-semibold">Settings</h2>
          <p className="text-zinc-500 text-xs">Panel configuration and server information</p>
        </div>
        <Card className="border-amber-500/20 bg-amber-500/[0.05]">
          <CardContent className="flex flex-col items-start justify-between gap-4 p-5 sm:flex-row sm:items-center">
            <div>
              <p className="text-sm font-medium text-amber-200">Settings could not be loaded</p>
              <p className="mt-1 text-xs text-amber-200/60">{settingsQuery.error instanceof Error ? settingsQuery.error.message : 'The server returned an unknown error.'} No editable defaults were substituted.</p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={() => settingsQuery.refetch()} disabled={settingsQuery.isFetching} className="border-amber-500/30 text-amber-100">
              <RefreshCw className={`mr-2 size-3.5 ${settingsQuery.isFetching ? 'animate-spin' : ''}`} />
              Retry
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-zinc-600/10 rounded-lg flex items-center justify-center">
            <SettingsIcon className="w-4 h-4 text-zinc-400" />
          </div>
          <div>
            <h2 className="text-white font-semibold">Settings</h2>
            <p className="text-zinc-500 text-xs">Panel configuration and server information</p>
          </div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => { void Promise.all([settingsQuery.refetch(), infoQuery.refetch(), healthQuery.refetch()]) }}
          disabled={refreshing}
          className="text-zinc-400 hover:text-white"
        >
          <RefreshCw className={`w-4 h-4 mr-2 ${refreshing ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {/* Settings form */}
      {settingsQuery.isLoading ? (
        <Skeleton className="h-80 w-full bg-zinc-800 rounded-lg" />
      ) : settingsQuery.data ? (
        <SettingsForm key={JSON.stringify(settingsQuery.data)} initial={settingsQuery.data} />
      ) : null}

      {/* Portable configuration */}
      <PortableConfigurationSection />

      {/* Two-Factor Authentication */}
      <TwoFactorSection />

      {/* Bottom cards */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <SystemInfoCard data={infoQuery.data} isLoading={infoQuery.isLoading} error={infoQuery.error} retry={() => { void infoQuery.refetch() }} retrying={infoQuery.isFetching} />
        <ServerHealthCard data={healthQuery.data} isLoading={healthQuery.isLoading} error={healthQuery.error} retry={() => { void healthQuery.refetch() }} retrying={healthQuery.isFetching} />
        <PanelInfoCard data={infoQuery.data} isLoading={infoQuery.isLoading} error={infoQuery.error} retry={() => { void infoQuery.refetch() }} retrying={infoQuery.isFetching} />
      </div>
    </div>
  )
}

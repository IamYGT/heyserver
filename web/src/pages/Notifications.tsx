import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Bell,
  Plus,
  Trash2,
  Pencil,
  Loader2,
  AlertTriangle,
  Mail,
  Send,
  History,
  Settings2,
  Zap,
  RefreshCw,
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
import {
  INTEGRATION_HEALTHY,
  INTEGRATION_NOT_CONFIGURED,
  INTEGRATION_UNAVAILABLE,
  integrationStatePresentation,
  normalizeIntegrationState,
  type IntegrationState,
} from '@/lib/integrationState'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ─── Types ─────────────────────────────────────────────────────────────────────

type ChannelType = 'email' | 'telegram' | 'discord' | 'slack'
type MetricType =
  | 'cpu_usage'
  | 'memory_usage'
  | 'disk_usage'
  | 'ssl_expiry'
  | 'service_down'
  | 'failed_logins'
// Backend model: id=number, config=JSON string, enabled (not active)
interface NotificationChannel {
  id: number
  type: ChannelType
  name: string
  enabled: boolean
  config: string // stored as JSON string in backend
  /** Canonical optional-integration state from the API. */
  state?: unknown
  /** Aggregate/UI detail; never substitutes for the canonical state. */
  detail?: string
}

// Parsed config for display/editing
interface ParsedChannelConfig {
  smtp_host?: string
  smtp_port?: string
  smtp_user?: string
  smtp_pass?: string
  from_address?: string
  to_address?: string
  bot_token?: string
  chat_id?: string
  webhook_url?: string
  secret_configured?: boolean
}

function parseConfig(raw: string): ParsedChannelConfig {
  try { return JSON.parse(raw) as ParsedChannelConfig } catch { return {} }
}

function channelAvailability(channel: NotificationChannel): IntegrationState {
  // The API state is the only source of truth. In particular, a redacted
  // config (or a configured secret) cannot make a channel healthy without a
  // current durable delivery receipt.
  return normalizeIntegrationState(channel.state) ?? INTEGRATION_UNAVAILABLE
}

function notificationAvailability(channels: NotificationChannel[]): IntegrationState {
  if (channels.length === 0) return INTEGRATION_NOT_CONFIGURED

  const states = channels.map(channelAvailability)
  if (states.every((state) => state === INTEGRATION_NOT_CONFIGURED)) return INTEGRATION_NOT_CONFIGURED
  // An aggregate must not turn green while any configured destination is
  // unavailable; `degraded` remains a detail on the aggregate, not a fourth
  // canonical wire state.
  if (states.includes(INTEGRATION_UNAVAILABLE)) return INTEGRATION_UNAVAILABLE
  if (states.every((state) => state === INTEGRATION_HEALTHY || state === INTEGRATION_NOT_CONFIGURED)) {
    return INTEGRATION_HEALTHY
  }
  return INTEGRATION_UNAVAILABLE
}

function notificationAvailabilityDetail(channels: NotificationChannel[], state: IntegrationState): string {
  if (state === INTEGRATION_NOT_CONFIGURED) {
    return 'No channel configuration is present. Alerts remain local until an operator adds a destination.'
  }
  const details = Array.from(new Set(channels.map((channel) => notificationDetailCode(channel))))
  const actionable = details
    .filter((detail) => detail !== 'not_configured')
    .map((detail) => notificationDetailPresentation(detail).aggregate)
  if (actionable.length > 0 && state !== INTEGRATION_HEALTHY) return actionable.join(' ')
  if (state === INTEGRATION_HEALTHY) {
    return 'Healthy only because every configured channel reports delivery_confirmed: its latest provider delivery succeeded and the receipt is current for that channel configuration.'
  }
  return 'Configuration is present, but no successful delivery probe is persisted. Availability remains unverified.'
}

type NotificationDetailCode =
  | 'not_configured'
  | 'config_unavailable'
  | 'configured_disabled'
  | 'degraded'
  | 'probe_unverified'
  | 'delivery_confirmed'
  | 'delivery_failed'
  | 'delivery_stale'

interface NotificationDetailPresentation {
  label: string
  description: string
  aggregate: string
}

const NOTIFICATION_DETAIL_PRESENTATIONS: Record<NotificationDetailCode, NotificationDetailPresentation> = {
  not_configured: {
    label: 'not_configured',
    description: 'No destination settings are present. Add a channel before assigning it to an alert rule.',
    aggregate: 'No configured notification destination is available yet.',
  },
  config_unavailable: {
    label: 'config_unavailable',
    description: 'The protected channel configuration could not be read. Repair the protected store, then retry.',
    aggregate: 'A channel reports config_unavailable. Repair the protected channel store, then retry.',
  },
  configured_disabled: {
    label: 'configured_disabled',
    description: 'The channel is disabled. Enable it, then send a new test notification.',
    aggregate: 'A channel reports configured_disabled. Enable it, then send a new test notification.',
  },
  degraded: {
    label: 'degraded',
    description: 'At least one configured destination is unavailable. Inspect each channel before relying on alerts.',
    aggregate: 'The notification aggregate is degraded. Inspect each channel before relying on alert delivery.',
  },
  probe_unverified: {
    label: 'probe_unverified',
    description: 'No current successful delivery receipt matches this channel configuration. Send a new test notification.',
    aggregate: 'A channel reports probe_unverified: no successful delivery probe is persisted for its current configuration. Send a new test notification.',
  },
  delivery_confirmed: {
    label: 'delivery_confirmed',
    description: 'The latest provider delivery succeeded and its receipt is current for this channel configuration.',
    aggregate: 'All configured channels report delivery_confirmed with current successful receipts.',
  },
  delivery_failed: {
    label: 'delivery_failed',
    description: 'The latest provider delivery failed. Check the channel settings and provider, then send a new test notification.',
    aggregate: 'A channel reports delivery_failed. Check its settings and provider, then send a new test notification.',
  },
  delivery_stale: {
    label: 'delivery_stale',
    description: 'The last successful delivery receipt is older than 7 days. Send a new test notification to refresh it.',
    aggregate: 'A channel reports delivery_stale: its successful receipt is older than 7 days. Send a new test notification.',
  },
}

function notificationDetailCode(channel: NotificationChannel): NotificationDetailCode {
  if (typeof channel.detail === 'string' && channel.detail in NOTIFICATION_DETAIL_PRESENTATIONS) {
    return channel.detail as NotificationDetailCode
  }
  const state = normalizeIntegrationState(channel.state)
  if (state === INTEGRATION_HEALTHY) return 'delivery_confirmed'
  if (state === INTEGRATION_NOT_CONFIGURED) return 'not_configured'
  return 'probe_unverified'
}

function notificationDetailPresentation(detail: string): NotificationDetailPresentation {
  if (detail in NOTIFICATION_DETAIL_PRESENTATIONS) {
    return NOTIFICATION_DETAIL_PRESENTATIONS[detail as NotificationDetailCode]
  }
  return NOTIFICATION_DETAIL_PRESENTATIONS.probe_unverified
}

function NotificationDetail({ channel }: { channel: NotificationChannel }) {
  const detail = notificationDetailCode(channel)
  const presentation = notificationDetailPresentation(detail)
  return (
    <div
      className="mt-1 max-w-sm text-xs leading-5"
      data-testid={`notification-channel-detail-${channel.id}`}
    >
      <span className="font-mono text-zinc-400">{presentation.label}</span>
      <span className="ml-1 text-zinc-500">{presentation.description}</span>
    </div>
  )
}

// Backend AlertRule: type (not metric), durationMins, cooldownMins
interface AlertRule {
  id: number
  name: string
  type: string  // backend field name
  threshold: number
  durationMins: number
  cooldownMins: number
  enabled: boolean
  target: string
}

interface AlertHistory {
  id: number
  ruleName: string
  type: string
  message: string
  value: number
  firedAt: string
}

// ─── Channel Form ──────────────────────────────────────────────────────────────

interface ChannelFormState {
  type: ChannelType
  name: string
  smtp_host: string
  smtp_port: string
  smtp_user: string
  smtp_pass: string
  from_address: string
  to_address: string
  bot_token: string
  chat_id: string
  webhook_url: string
  clear_secret: boolean
}

const defaultChannelForm = (): ChannelFormState => ({
  type: 'email',
  name: '',
  smtp_host: '',
  smtp_port: '587',
  smtp_user: '',
  smtp_pass: '',
  from_address: '',
  to_address: '',
  bot_token: '',
  chat_id: '',
  webhook_url: '',
  clear_secret: false,
})

interface ChannelDialogProps {
  open: boolean
  onClose: () => void
  onSave: (form: ChannelFormState) => void
  isPending: boolean
  initial?: NotificationChannel | null
}

function ChannelDialog({ open, onClose, onSave, isPending, initial }: ChannelDialogProps) {
  const secretConfigured = initial ? parseConfig(initial.config).secret_configured === true : false
  const [form, setForm] = useState<ChannelFormState>(() => {
    if (initial) {
      const cfg = parseConfig(initial.config)
      return {
        type: initial.type,
        name: initial.name,
        smtp_host: cfg.smtp_host ?? '',
        smtp_port: cfg.smtp_port ?? '587',
        smtp_user: cfg.smtp_user ?? '',
        smtp_pass: cfg.smtp_pass ?? '',
        from_address: cfg.from_address ?? '',
        to_address: cfg.to_address ?? '',
        bot_token: cfg.bot_token ?? '',
        chat_id: cfg.chat_id ?? '',
        webhook_url: cfg.webhook_url ?? '',
        clear_secret: false,
      }
    }
    return defaultChannelForm()
  })

  function field<K extends keyof ChannelFormState>(key: K, value: ChannelFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit() {
    if (!form.name.trim()) {
      toast.error('Channel name is required')
      return
    }
    onSave(form)
  }

  const inputClass =
    'w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors'

  const labelClass = 'text-zinc-400 text-xs font-medium'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit Channel' : 'Add Channel'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Type</label>
              <select
                value={form.type}
                onChange={(e) => field('type', e.target.value as ChannelType)}
                disabled={!!initial}
                className={cn(inputClass, initial && 'opacity-50 cursor-not-allowed')}
              >
                <option value="email">Email</option>
                <option value="telegram">Telegram</option>
                <option value="discord">Discord</option>
                <option value="slack">Slack</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => field('name', e.target.value)}
                placeholder="My Channel"
                className={inputClass}
              />
            </div>
          </div>

          {form.type === 'email' && (
            <>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>SMTP Host</label>
                  <input
                    type="text"
                    value={form.smtp_host}
                    onChange={(e) => field('smtp_host', e.target.value)}
                    placeholder="smtp.example.com"
                    className={inputClass}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Port</label>
                  <input
                    type="text"
                    value={form.smtp_port}
                    onChange={(e) => field('smtp_port', e.target.value)}
                    placeholder="587"
                    className={inputClass}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className={labelClass}>Username</label>
                  <input
                    type="text"
                    value={form.smtp_user}
                    onChange={(e) => field('smtp_user', e.target.value)}
                    placeholder="user@example.com"
                    className={inputClass}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelClass}>Password</label>
                  <input
                    type="password"
                    value={form.smtp_pass}
                    onChange={(e) => field('smtp_pass', e.target.value)}
                    disabled={form.clear_secret}
                    placeholder={secretConfigured ? 'Leave blank to keep current password' : '••••••••'}
                    autoComplete="new-password"
                    className={inputClass}
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <label className={labelClass}>From Address</label>
                <input
                  type="email"
                  value={form.from_address}
                  onChange={(e) => field('from_address', e.target.value)}
                  placeholder="alerts@example.com"
                  className={inputClass}
                />
              </div>
              <div className="space-y-1.5">
                <label className={labelClass}>To Address</label>
                <input
                  type="email"
                  value={form.to_address}
                  onChange={(e) => field('to_address', e.target.value)}
                  placeholder="admin@example.com"
                  className={inputClass}
                />
              </div>
            </>
          )}

          {form.type === 'telegram' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <label className={labelClass}>Bot Token</label>
                <input
                  type="password"
                  value={form.bot_token}
                  onChange={(e) => field('bot_token', e.target.value)}
                  disabled={form.clear_secret}
                  placeholder={secretConfigured ? 'Leave blank to keep current bot token' : '123456:ABCdef...'}
                  autoComplete="new-password"
                  className={inputClass}
                />
              </div>
              <div className="space-y-1.5">
                <label className={labelClass}>Chat ID</label>
                <input
                  type="text"
                  value={form.chat_id}
                  onChange={(e) => field('chat_id', e.target.value)}
                  placeholder="-100123456789"
                  className={inputClass}
                />
              </div>
            </div>
          )}

          {(form.type === 'discord' || form.type === 'slack') && (
            <div className="space-y-1.5">
              <label className={labelClass}>Webhook URL</label>
              <input
                type="password"
                value={form.webhook_url}
                onChange={(e) => field('webhook_url', e.target.value)}
                disabled={form.clear_secret}
                placeholder={secretConfigured ? 'Leave blank to keep current webhook' : 'https://hooks.example.com/...'}
                autoComplete="new-password"
                className={inputClass}
              />
            </div>
          )}

          {initial && secretConfigured && (
            <label className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-xs text-amber-100">
              <input
                type="checkbox"
                checked={form.clear_secret}
                onChange={(e) => field('clear_secret', e.target.checked)}
                className="mt-0.5 size-3.5 accent-amber-500"
              />
              <span>
                Remove the stored credential
                <span className="mt-1 block text-amber-200/60">The channel remains saved but delivery stays unavailable until a new credential is provided.</span>
              </span>
            </label>
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
            {initial ? 'Save Changes' : 'Add Channel'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Alert Rule Form ───────────────────────────────────────────────────────────

interface RuleFormState {
  name: string
  metric: MetricType
  threshold: string
  duration: string
  cooldown: string
  target: string
}

const defaultRuleForm = (): RuleFormState => ({
  name: '',
  metric: 'cpu_usage',
  threshold: '90',
  duration: '5',
  cooldown: '30',
  target: '',
})

interface RuleDialogProps {
  open: boolean
  onClose: () => void
  onSave: (form: RuleFormState) => void
  isPending: boolean
  initial?: AlertRule | null
}

const METRIC_LABELS: Record<MetricType, string> = {
  cpu_usage: 'CPU Usage',
  memory_usage: 'Memory Usage',
  disk_usage: 'Disk Usage',
  ssl_expiry: 'SSL Expiry (days)',
  service_down: 'Service Down',
  failed_logins: 'Failed SSH Logins',
}

const METRIC_DEFAULTS: Record<MetricType, { threshold: string; target: string }> = {
  cpu_usage: { threshold: '90', target: '' },
  memory_usage: { threshold: '90', target: '' },
  disk_usage: { threshold: '90', target: '/' },
  ssl_expiry: { threshold: '14', target: '' },
  service_down: { threshold: '1', target: '' },
  failed_logins: { threshold: '5', target: '' },
}

function normalizeMetric(type: string): MetricType {
  if (type === 'cpu') return 'cpu_usage'
  if (type === 'memory') return 'memory_usage'
  if (type === 'disk') return 'disk_usage'
  return type in METRIC_LABELS ? type as MetricType : 'cpu_usage'
}

function ruleCondition(rule: AlertRule): string {
  switch (normalizeMetric(rule.type)) {
    case 'service_down':
      return `inactive · ${rule.target}`
    case 'ssl_expiry':
      return `≤ ${rule.threshold} days · ${rule.target}`
    case 'failed_logins':
      return `≥ ${rule.threshold} / min`
    case 'disk_usage':
      return `≥ ${rule.threshold}% · ${rule.target || '/'}`
    default:
      return `≥ ${rule.threshold}%`
  }
}

function RuleDialog({ open, onClose, onSave, isPending, initial }: RuleDialogProps) {
  const [form, setForm] = useState<RuleFormState>(() => {
    if (initial) {
      const metric = normalizeMetric(initial.type)
      return {
        name: initial.name,
        metric,
        threshold: String(initial.threshold),
        duration: String(initial.durationMins),
        cooldown: String(initial.cooldownMins),
        target: initial.target || METRIC_DEFAULTS[metric].target,
      }
    }
    return defaultRuleForm()
  })

  function field<K extends keyof RuleFormState>(key: K, value: RuleFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit() {
    if (!form.name.trim()) {
      toast.error('Rule name is required')
      return
    }
    const threshold = Number(form.threshold)
    const duration = Number(form.duration)
    const cooldown = Number(form.cooldown)
    if (!Number.isFinite(threshold)) {
      toast.error('A valid threshold is required')
      return
    }
    if (form.metric !== 'service_down') {
      const percentageMetric = form.metric === 'cpu_usage' || form.metric === 'memory_usage' || form.metric === 'disk_usage'
      const thresholdInvalid = percentageMetric
        ? threshold < 0 || threshold > 100
        : form.metric === 'ssl_expiry'
          ? threshold < 0 || threshold > 3650
          : threshold < 1 || threshold > 1000000 || !Number.isInteger(threshold)
      if (thresholdInvalid) {
        toast.error('Threshold is outside the supported range')
        return
      }
    }
    if (!Number.isInteger(duration) || duration < 0 || duration > 1440) {
      toast.error('Duration must be a whole number between 0 and 1440 minutes')
      return
    }
    if (!Number.isInteger(cooldown) || cooldown < 1 || cooldown > 10080) {
      toast.error('Cooldown must be a whole number between 1 and 10080 minutes')
      return
    }
    if (form.metric === 'disk_usage' && !form.target.trim().startsWith('/')) {
      toast.error('Mount path must be absolute')
      return
    }
    if ((form.metric === 'ssl_expiry' || form.metric === 'service_down') && !form.target.trim()) {
      toast.error(form.metric === 'ssl_expiry' ? 'Certificate domain is required' : 'Systemd unit is required')
      return
    }
    onSave(form)
  }

  function changeMetric(metric: MetricType) {
    const defaults = METRIC_DEFAULTS[metric]
    setForm((prev) => ({ ...prev, metric, threshold: defaults.threshold, target: defaults.target }))
  }

  const inputClass =
    'w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors'

  const labelClass = 'text-zinc-400 text-xs font-medium'

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">
            {initial ? 'Edit Rule' : 'Add Alert Rule'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <label className={labelClass}>Rule Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => field('name', e.target.value)}
              placeholder="High CPU Alert"
              className={inputClass}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Metric</label>
              <select
                value={form.metric}
                onChange={(e) => changeMetric(e.target.value as MetricType)}
                className={inputClass}
              >
                {(Object.entries(METRIC_LABELS) as [MetricType, string][]).map(([val, label]) => (
                  <option key={val} value={val}>{label}</option>
                ))}
              </select>
            </div>
            {form.metric === 'service_down' ? (
              <div className="space-y-1.5">
                <label className={labelClass}>Condition</label>
                <div className="flex min-h-9 items-center rounded-md border border-zinc-700 bg-zinc-800 px-3 text-sm text-zinc-300">
                  Unit is not active
                </div>
              </div>
            ) : (
              <div className="space-y-1.5">
                <label className={labelClass}>Threshold</label>
                <input
                  type="number"
                  value={form.threshold}
                  onChange={(e) => field('threshold', e.target.value)}
                  placeholder="90"
                  min={form.metric === 'failed_logins' ? 1 : 0}
                  max={form.metric === 'ssl_expiry' ? 3650 : form.metric === 'failed_logins' ? 1000000 : 100}
                  step={form.metric === 'failed_logins' ? 1 : 'any'}
                  className={inputClass}
                />
                <p className="text-zinc-600 text-xs">
                  {form.metric === 'ssl_expiry'
                    ? 'Triggers at or below the remaining days'
                    : form.metric === 'failed_logins'
                      ? 'Failed SSH logins observed in one minute'
                      : 'Triggers at or above this percentage'}
                </p>
              </div>
            )}
          </div>

          {(form.metric === 'disk_usage' || form.metric === 'ssl_expiry' || form.metric === 'service_down') && (
            <div className="space-y-1.5">
              <label className={labelClass}>
                {form.metric === 'disk_usage' ? 'Mount Path' : form.metric === 'ssl_expiry' ? 'Certificate Domain' : 'Systemd Unit'}
              </label>
              <input
                type="text"
                value={form.target}
                onChange={(e) => field('target', e.target.value)}
                placeholder={form.metric === 'disk_usage' ? '/' : form.metric === 'ssl_expiry' ? 'example.com' : 'nginx.service'}
                className={inputClass}
              />
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className={labelClass}>Duration (min)</label>
              <input
                type="number"
                value={form.duration}
                onChange={(e) => field('duration', e.target.value)}
                placeholder="5"
                min={0}
                max={1440}
                step={1}
                className={inputClass}
              />
              <p className="text-zinc-600 text-xs">Must exceed for X minutes</p>
            </div>
            <div className="space-y-1.5">
              <label className={labelClass}>Cooldown (min)</label>
              <input
                type="number"
                value={form.cooldown}
                onChange={(e) => field('cooldown', e.target.value)}
                placeholder="30"
                min={1}
                max={10080}
                step={1}
                className={inputClass}
              />
              <p className="text-zinc-600 text-xs">Minutes between alerts</p>
            </div>
          </div>
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
            {initial ? 'Save Changes' : 'Add Rule'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Badges ────────────────────────────────────────────────────────────────────

function ChannelTypeBadge({ type }: { type: ChannelType }) {
  const styles: Record<ChannelType, string> = {
    email: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
    telegram: 'bg-sky-500/10 text-sky-400 border-sky-500/20',
    discord: 'bg-indigo-500/10 text-indigo-400 border-indigo-500/20',
    slack: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
  }
  const labels: Record<ChannelType, string> = {
    email: 'Email',
    telegram: 'Telegram',
    discord: 'Discord',
    slack: 'Slack',
  }
  return (
    <Badge className={cn('text-xs border capitalize', styles[type])}>
      {labels[type]}
    </Badge>
  )
}

function MetricBadge({ metric }: { metric: MetricType }) {
  return (
    <Badge className="text-xs border bg-zinc-800 text-zinc-300 border-zinc-700">
      {METRIC_LABELS[metric]}
    </Badge>
  )
}

function InventoryLoadError({ title, error, retry, retrying }: { title: string; error: Error; retry: () => void; retrying: boolean }) {
  return (
    <div className="flex flex-col items-center gap-3 px-5 py-16 text-center">
      <AlertTriangle className="size-7 text-red-400" />
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

const notificationStateClasses = {
  neutral: 'bg-zinc-500/10 text-zinc-300 border-zinc-500/20',
  warning: 'bg-amber-500/10 text-amber-300 border-amber-500/20',
  healthy: 'bg-green-500/10 text-green-300 border-green-500/20',
} as const

function NotificationStateBadge({ state, label = 'Notification availability' }: { state: IntegrationState; label?: string }) {
  const presentation = integrationStatePresentation(state)
  return (
    <Badge
      data-testid={label === 'Notification availability' ? 'notification-availability' : undefined}
      aria-label={`${label}: ${presentation.label}`}
      title={label}
      className={cn('text-xs border', notificationStateClasses[presentation.tone])}
    >
      {presentation.label}
    </Badge>
  )
}

function NotificationDeleteDialog({
  open,
  title,
  description,
  onClose,
  onConfirm,
  isPending,
}: {
  open: boolean
  title: string
  description: string
  onClose: () => void
  onConfirm: () => void
  isPending: boolean
}) {
  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !isPending) onClose() }}>
      <DialogContent className="max-w-md overflow-y-auto border-zinc-800 bg-zinc-900 text-white">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-white">
            <AlertTriangle className="size-4 text-red-400" />
            {title}
          </DialogTitle>
        </DialogHeader>
        <p className="text-sm leading-6 text-zinc-400">{description}</p>
        <DialogFooter className="gap-2">
          <Button type="button" variant="ghost" onClick={onClose} disabled={isPending}>Cancel</Button>
          <Button type="button" onClick={onConfirm} disabled={isPending} className="bg-red-600 text-white hover:bg-red-500">
            {isPending && <Loader2 className="size-3.5 animate-spin" />}
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Channels Tab ──────────────────────────────────────────────────────────────

function ChannelsTab() {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editChannel, setEditChannel] = useState<NotificationChannel | null>(null)
  const [deleteChannel, setDeleteChannel] = useState<NotificationChannel | null>(null)

  const channelsQuery = useQuery<NotificationChannel[]>({
    queryKey: ['notify-channels'],
    queryFn: () => api.get<NotificationChannel[]>('/notify/channels'),
  })
  const channels = channelsQuery.data ?? []
  const availabilityState: IntegrationState | null = channelsQuery.isLoading
    ? null
    : channelsQuery.isError
      ? INTEGRATION_UNAVAILABLE
      : notificationAvailability(channels)
  const availabilityPresentation = availabilityState ? integrationStatePresentation(availabilityState) : null
  const availabilityDetail = channelsQuery.isError
    ? 'Heyserver could not verify the protected notification channel inventory. Existing delivery state remains unknown.'
    : availabilityState
      ? notificationAvailabilityDetail(channels, availabilityState)
      : ''

  // Convert form fields into the backend payload: {name, type, config: JSON string}
  function formToPayload(form: ChannelFormState) {
    const config: Record<string, string> =
      form.type === 'email'
        ? {
            smtp_host: form.smtp_host,
            smtp_port: form.smtp_port,
            smtp_user: form.smtp_user,
            smtp_pass: form.smtp_pass,
            from_address: form.from_address,
            to_address: form.to_address,
          }
        : form.type === 'telegram'
          ? { bot_token: form.bot_token, chat_id: form.chat_id }
          : { webhook_url: form.webhook_url }
    return { name: form.name, type: form.type, config: JSON.stringify(config), clearSecret: form.clear_secret }
  }

  const addMutation = useMutation({
    mutationFn: (form: ChannelFormState) => api.post('/notify/channels', formToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-channels'] })
      setAddOpen(false)
      toast.success('Channel added')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to add channel'),
  })

  const editMutation = useMutation({
    mutationFn: ({ id, form }: { id: number; form: ChannelFormState }) =>
      api.put(`/notify/channels/${id}`, formToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-channels'] })
      setEditChannel(null)
      toast.success('Channel updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update channel'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.delete(`/notify/channels/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-channels'] })
      setDeleteChannel(null)
      toast.success('Channel deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete channel'),
  })

  const testMutation = useMutation({
    mutationFn: (id: number) => api.post(`/notify/channels/${id}/test`, {}),
    onSuccess: () => toast.success('Test notification sent; delivery receipt recorded'),
    onError: (error: Error) => toast.error(error.message || 'Test notification failed'),
    onSettled: () => {
      // A provider attempt updates the durable receipt on both success and
      // failure. Refresh the row so the exact API detail is immediately
      // actionable instead of leaving the previous observation on screen.
      void queryClient.invalidateQueries({ queryKey: ['notify-channels'] })
    },
  })

  return (
    <div className="space-y-4">
      {availabilityPresentation && (
        <Card className="border-zinc-800 bg-zinc-900" data-testid="notification-availability-card">
          <CardContent className="flex flex-wrap items-start justify-between gap-3 p-4">
            <div>
              <p className="text-sm font-medium text-zinc-200">Notification delivery availability</p>
              <p className="mt-1 max-w-3xl text-xs leading-5 text-zinc-500">{availabilityDetail}</p>
            </div>
            <NotificationStateBadge state={availabilityState!} />
          </CardContent>
        </Card>
      )}
      <div className="flex items-center justify-between">
        <p className="text-zinc-400 text-sm">Configure where alerts are delivered. Healthy requires a current successful receipt from the provider.</p>
        {!channelsQuery.isError && (
          <Button
            size="sm"
            onClick={() => setAddOpen(true)}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            Add Channel
          </Button>
        )}
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {channelsQuery.isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : channelsQuery.isError ? (
            <DependencyRemediation
              state="unavailable"
              title="Notification channels could not be loaded. Channel controls are paused."
              error={channelsQuery.error.message}
              retry={() => { void channelsQuery.refetch() }}
              retrying={channelsQuery.isFetching}
              summary="Heyserver could not verify the protected notification channel inventory. Existing alert delivery state remains unknown."
              steps={[
                'Run the packaged Heyserver doctor and inspect the panel service log.',
                'Verify the notification secret directory is owned by the Heyserver service and remains mode 0700.',
                'Retry channel detection after repairing the protected store.',
              ]}
            />
          ) : channels.length === 0 ? (
            <div className="p-5">
              <DependencyRemediation
                state="not-configured"
                title="No notification channels are configured"
                summary="Alerts remain local until an operator adds an email, Telegram, Discord, or Slack destination."
                steps={[
                  'Add a channel and provide its destination settings.',
                  'Send a test notification before assigning the channel to an alert rule.',
                  'Keep provider credentials in the protected Heyserver channel store.',
                ]}
                retry={() => setAddOpen(true)}
                retryLabel="Add channel"
              />
            </div>
          ) : (
            <div className="overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs">Name</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-28">Type</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-24">Status</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs text-right w-32">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {channels.map((ch) => (
                    <TableRow
                      key={ch.id}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="text-sm text-white py-3 font-medium">
                        {ch.name}
                      </TableCell>
                      <TableCell className="py-3">
                        <ChannelTypeBadge type={ch.type} />
                      </TableCell>
                      <TableCell className="py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge
                            className={cn(
                              'text-xs border',
                              ch.enabled
                                ? 'bg-green-500/10 text-green-400 border-green-500/20'
                                : 'bg-zinc-500/10 text-zinc-500 border-zinc-500/20',
                            )}
                          >
                            {ch.enabled ? 'Active' : 'Inactive'}
                          </Badge>
                          <NotificationStateBadge
                            state={channelAvailability(ch)}
                            label={`${ch.name} notification availability`}
                          />
                        </div>
                        <NotificationDetail channel={ch} />
                      </TableCell>
                      <TableCell className="py-3 text-right">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            onClick={() => testMutation.mutate(ch.id)}
                            disabled={testMutation.isPending && testMutation.variables === ch.id}
                            className="text-zinc-500 hover:text-green-400 p-1.5 rounded transition-colors"
                            title="Send test"
                          >
                            {testMutation.isPending && testMutation.variables === ch.id ? (
                              <Loader2 className="w-3.5 h-3.5 animate-spin" />
                            ) : (
                              <Send className="w-3.5 h-3.5" />
                            )}
                          </button>
                          <button
                            onClick={() => setEditChannel(ch)}
                            className="text-zinc-500 hover:text-blue-400 p-1.5 rounded transition-colors"
                            title="Edit"
                          >
                            <Pencil className="w-3.5 h-3.5" />
                          </button>
                          <button
                            onClick={() => setDeleteChannel(ch)}
                            disabled={deleteMutation.isPending}
                            className="text-zinc-500 hover:text-red-400 p-1.5 rounded transition-colors"
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
        </CardContent>
      </Card>

      <ChannelDialog
        key={addOpen ? 'channel-add-open' : 'channel-add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSave={(form) => addMutation.mutate(form)}
        isPending={addMutation.isPending}
      />

      {editChannel && (
        <ChannelDialog
          open
          onClose={() => setEditChannel(null)}
          onSave={(form) => editMutation.mutate({ id: editChannel.id, form })}
          isPending={editMutation.isPending}
          initial={editChannel}
        />
      )}

      <NotificationDeleteDialog
        open={deleteChannel !== null}
        title="Delete Notification Channel"
        description={deleteChannel ? `Delete “${deleteChannel.name}”? Alert rules that depend on this destination may no longer deliver notifications.` : ''}
        onClose={() => setDeleteChannel(null)}
        onConfirm={() => { if (deleteChannel) deleteMutation.mutate(deleteChannel.id) }}
        isPending={deleteMutation.isPending}
      />
    </div>
  )
}

// ─── Alert Rules Tab ───────────────────────────────────────────────────────────

function ruleFormToPayload(form: RuleFormState) {
  return {
    name: form.name.trim(),
    type: form.metric,
    threshold: form.metric === 'service_down' ? 1 : Number(form.threshold),
    durationMins: Number(form.duration),
    cooldownMins: Number(form.cooldown),
    target: form.metric === 'disk_usage' ? form.target.trim() || '/' : form.target.trim(),
  }
}

function AlertRulesTab() {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editRule, setEditRule] = useState<AlertRule | null>(null)
  const [deleteRule, setDeleteRule] = useState<AlertRule | null>(null)

  const rulesQuery = useQuery<AlertRule[]>({
    queryKey: ['notify-rules'],
    queryFn: () => api.get<AlertRule[]>('/notify/rules'),
  })
  const rules = rulesQuery.data ?? []

  const addMutation = useMutation({
    mutationFn: (form: RuleFormState) => api.post('/notify/rules', ruleFormToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] })
      setAddOpen(false)
      toast.success('Rule created')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create rule'),
  })

  const editMutation = useMutation({
    mutationFn: ({ id, form }: { id: number; form: RuleFormState }) =>
      api.put(`/notify/rules/${id}`, ruleFormToPayload(form)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] })
      setEditRule(null)
      toast.success('Rule updated')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to update rule'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.delete(`/notify/rules/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] })
      setDeleteRule(null)
      toast.success('Rule deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete rule'),
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      api.put(`/notify/rules/${id}`, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] })
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to toggle rule'),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-zinc-400 text-sm">Define conditions that trigger notifications</p>
        {!rulesQuery.isError && (
          <Button
            size="sm"
            onClick={() => setAddOpen(true)}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            Add Rule
          </Button>
        )}
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {rulesQuery.isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : rulesQuery.isError ? (
            <InventoryLoadError
              title="Alert rules could not be loaded. Rule controls are paused."
              error={rulesQuery.error}
              retry={() => { void rulesQuery.refetch() }}
              retrying={rulesQuery.isFetching}
            />
          ) : rules.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
              <Zap className="w-8 h-8 mb-2 opacity-40" />
              <p className="text-sm">No alert rules configured</p>
            </div>
          ) : (
            <div className="overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs">Name</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-32">Metric</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-32">Condition</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-24">Duration</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-24">Cooldown</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-20 text-center">
                      Enabled
                    </TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs text-right w-24">
                      Actions
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rules.map((rule) => (
                    <TableRow
                      key={rule.id}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="text-sm text-white py-3 font-medium">
                        {rule.name}
                      </TableCell>
                      <TableCell className="py-3">
                        <MetricBadge metric={normalizeMetric(rule.type)} />
                      </TableCell>
                      <TableCell className="py-3">
                        <span className="font-mono text-xs text-zinc-300">
                          {ruleCondition(rule)}
                        </span>
                      </TableCell>
                      <TableCell className="py-3 text-xs text-zinc-400 font-mono">
                        {rule.durationMins}m
                      </TableCell>
                      <TableCell className="py-3 text-xs text-zinc-400 font-mono">
                        {rule.cooldownMins}m
                      </TableCell>
                      <TableCell className="py-3 text-center">
                        <button
                          onClick={() =>
                            toggleMutation.mutate({ id: rule.id, enabled: !rule.enabled })
                          }
                          aria-label={`${rule.enabled ? 'Disable' : 'Enable'} ${rule.name}`}
                          className={cn(
                            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none',
                            rule.enabled ? 'bg-blue-600' : 'bg-zinc-700',
                          )}
                        >
                          <span
                            className={cn(
                              'inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform',
                              rule.enabled ? 'translate-x-4.5' : 'translate-x-0.5',
                            )}
                          />
                        </button>
                      </TableCell>
                      <TableCell className="py-3 text-right">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            onClick={() => setEditRule(rule)}
                            className="text-zinc-500 hover:text-blue-400 p-1.5 rounded transition-colors"
                            title="Edit"
                          >
                            <Pencil className="w-3.5 h-3.5" />
                          </button>
                          <button
                            onClick={() => setDeleteRule(rule)}
                            disabled={deleteMutation.isPending}
                            className="text-zinc-500 hover:text-red-400 p-1.5 rounded transition-colors"
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
        </CardContent>
      </Card>

      <RuleDialog
        key={addOpen ? 'rule-add-open' : 'rule-add-closed'}
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSave={(form) => addMutation.mutate(form)}
        isPending={addMutation.isPending}
      />

      {editRule && (
        <RuleDialog
          open
          onClose={() => setEditRule(null)}
          onSave={(form) => editMutation.mutate({ id: editRule.id, form })}
          isPending={editMutation.isPending}
          initial={editRule}
        />
      )}

      <NotificationDeleteDialog
        open={deleteRule !== null}
        title="Delete Alert Rule"
        description={deleteRule ? `Delete “${deleteRule.name}”? Heyserver will stop evaluating this alert condition.` : ''}
        onClose={() => setDeleteRule(null)}
        onConfirm={() => { if (deleteRule) deleteMutation.mutate(deleteRule.id) }}
        isPending={deleteMutation.isPending}
      />
    </div>
  )
}

// ─── History Tab ───────────────────────────────────────────────────────────────

function HistoryTab() {
  const historyQuery = useQuery<{ items: AlertHistory[] }>({
    queryKey: ['notify-history'],
    queryFn: () => api.get<{ items: AlertHistory[] }>('/notify/history'),
    refetchInterval: 30_000,
  })
  const history = historyQuery.data?.items ?? []

  function formatTime(ts: string) {
    const d = new Date(ts)
    return d.toLocaleString('en-GB', {
      day: '2-digit',
      month: '2-digit',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-zinc-400 text-sm">Recent alert dispatch history</p>
        <div className="flex items-center gap-1.5 text-xs text-zinc-500">
          <div className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
          Auto-refresh 30s
        </div>
      </div>

      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {historyQuery.isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : historyQuery.isError ? (
            <InventoryLoadError
              title="Notification history could not be loaded."
              error={historyQuery.error}
              retry={() => { void historyQuery.refetch() }}
              retrying={historyQuery.isFetching}
            />
          ) : history.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
              <History className="w-8 h-8 mb-2 opacity-40" />
              <p className="text-sm">No alerts triggered yet</p>
            </div>
          ) : (
            <div className="overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs w-44">
                      Timestamp
                    </TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs">Rule</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-24">Type</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs">Message</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-20 text-right">Value</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map((item) => (
                    <TableRow
                      key={item.id}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="font-mono text-xs text-zinc-400 py-3">
                        {formatTime(item.firedAt)}
                      </TableCell>
                      <TableCell className="text-sm text-white py-3">{item.ruleName}</TableCell>
                      <TableCell className="py-3">
                        <Badge className="bg-zinc-800 text-zinc-400 border-zinc-700 text-xs capitalize">
                          {item.type}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs text-zinc-300 py-3 max-w-xs truncate">
                        {item.message}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-zinc-300 py-3 text-right">
                        {item.value}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Main Page ─────────────────────────────────────────────────────────────────

type TabId = 'channels' | 'rules' | 'history'

interface TabDef {
  id: TabId
  label: string
  icon: React.ComponentType<{ className?: string }>
}

const TABS: TabDef[] = [
  { id: 'channels', label: 'Channels', icon: Mail },
  { id: 'rules', label: 'Alert Rules', icon: AlertTriangle },
  { id: 'history', label: 'History', icon: History },
]

export default function Notifications() {
  const [activeTab, setActiveTab] = useState<TabId>('channels')

  return (
    <div className="space-y-4">
      {/* Page header */}
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 bg-amber-500/10 border border-amber-500/20 rounded-lg flex items-center justify-center">
          <Bell className="w-5 h-5 text-amber-400" />
        </div>
        <div>
          <h2 className="text-white text-xl font-bold">Notifications</h2>
          <p className="text-zinc-500 text-sm mt-0.5">Alert channels, durable delivery receipts, rules, and history</p>
        </div>
      </div>

      {/* Tab bar */}
      <div className="flex items-center gap-1 border-b border-zinc-800">
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id
          return (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                'flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 transition-colors -mb-px',
                isActive
                  ? 'border-blue-500 text-white'
                  : 'border-transparent text-zinc-500 hover:text-zinc-300 hover:border-zinc-600',
              )}
            >
              <tab.icon className="w-3.5 h-3.5" />
              {tab.label}
            </button>
          )
        })}
        <div className="ml-auto pb-2">
          <Settings2 className="w-4 h-4 text-zinc-700" />
        </div>
      </div>

      {/* Tab content */}
      <div className="pt-1">
        {activeTab === 'channels' && <ChannelsTab />}
        {activeTab === 'rules' && <AlertRulesTab />}
        {activeTab === 'history' && <HistoryTab />}
      </div>
    </div>
  )
}

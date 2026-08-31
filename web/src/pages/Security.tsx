import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ShieldCheck,
  ShieldOff,
  Shield,
  Ban,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Plus,
  Trash2,
  Loader2,
  RefreshCw,
  KeyRound,
  Globe,
  Server,
  Lock,
  ChevronRight,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { DependencyRemediation } from '@/components/DependencyRemediation'
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
import { useCurrentUser } from '@/hooks/useAuth'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'

// ─── Types ──────────────────────────────────────────────────────────────────────

interface SecurityCheck {
  name: string
  status: 'pass' | 'fail' | 'warn'
  detail: string
}

interface SecurityScore {
  score: number
  maxScore: number
  checks: SecurityCheck[]
}

interface Fail2BanStatus {
  available: boolean
  installed: boolean
  running: boolean
  state: 'healthy' | 'not-installed' | 'stopped' | 'unavailable'
  daemonState: string
  error?: string
  availableJails?: string[]
  version?: string
  jails: Fail2BanJail[]
}

interface Fail2BanJail {
  name: string
  currentlyBanned: number
  totalBanned: number
  currentlyFailed: number
}

interface BannedIP {
  ip: string
  jail: string
  banned_at?: string
}

interface IPListEntry {
  ip: string
  listType: 'blacklist' | 'whitelist'
  comment: string
  createdAt: string
}

// ─── Score Gauge ───────────────────────────────────────────────────────────────

interface ScoreGaugeProps {
  score: number
}

function ScoreGauge({ score }: ScoreGaugeProps) {
  const clampedScore = Math.min(100, Math.max(0, score))
  const radius = 54
  const circumference = 2 * Math.PI * radius
  // Half-circle gauge (180 degrees)
  const halfCircumference = circumference / 2

  const color =
    clampedScore >= 80
      ? '#22c55e'
      : clampedScore >= 60
      ? '#f59e0b'
      : '#ef4444'

  const label =
    clampedScore >= 80 ? 'Good' : clampedScore >= 60 ? 'Fair' : 'Poor'

  return (
    <div className="flex flex-col items-center">
      <div className="relative w-36 h-20 overflow-hidden">
        <svg
          className="w-36 h-36 -mt-[72px] rotate-0"
          viewBox="0 0 144 144"
        >
          {/* Background track (half circle, bottom half hidden) */}
          <circle
            cx="72" cy="72" r={radius}
            fill="none"
            stroke="#27272a"
            strokeWidth="10"
            strokeDasharray={`${halfCircumference} ${halfCircumference}`}
            strokeDashoffset={halfCircumference / 2}
            transform="rotate(-90 72 72)"
            strokeLinecap="round"
          />
          {/* Foreground arc */}
          <circle
            cx="72" cy="72" r={radius}
            fill="none"
            stroke={color}
            strokeWidth="10"
            strokeDasharray={`${halfCircumference} ${halfCircumference}`}
            strokeDashoffset={halfCircumference / 2 + (halfCircumference - (clampedScore / 100) * halfCircumference)}
            transform="rotate(-90 72 72)"
            strokeLinecap="round"
            style={{ transition: 'stroke-dashoffset 0.6s ease, stroke 0.3s ease' }}
          />
        </svg>
        {/* Centered score text */}
        <div className="absolute bottom-0 left-0 right-0 flex flex-col items-center pb-1">
          <span className="text-white text-3xl font-bold leading-none" style={{ color }}>
            {clampedScore}
          </span>
          <span className="text-zinc-500 text-xs mt-0.5">/ 100</span>
        </div>
      </div>
      <span
        className="mt-2 text-sm font-semibold"
        style={{ color }}
      >
        {label}
      </span>
    </div>
  )
}

// ─── Check Item ────────────────────────────────────────────────────────────────

interface CheckItemProps {
  label: string
  description: string
  status: SecurityCheck['status']
  fixPath?: string
  icon: React.ComponentType<{ className?: string }>
}

function CheckItem({ label, description, status, fixPath, icon: Icon }: CheckItemProps) {
  const passed = status === 'pass'
  const warning = status === 'warn'
  return (
    <div className="flex items-start gap-3 py-3 px-4 rounded-lg bg-zinc-800/40 border border-zinc-800">
      <div
        className={cn(
          'flex-shrink-0 w-7 h-7 rounded-full flex items-center justify-center mt-0.5',
          passed ? 'bg-green-500/15' : warning ? 'bg-amber-500/15' : 'bg-red-500/15',
        )}
      >
        <Icon className={cn('w-3.5 h-3.5', passed ? 'text-green-400' : warning ? 'text-amber-400' : 'text-red-400')} />
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-white text-sm font-medium">{label}</span>
          {passed ? (
            <CheckCircle2 className="w-3.5 h-3.5 text-green-400 flex-shrink-0" />
          ) : warning ? (
            <AlertTriangle className="w-3.5 h-3.5 text-amber-400 flex-shrink-0" />
          ) : (
            <XCircle className="w-3.5 h-3.5 text-red-400 flex-shrink-0" />
          )}
        </div>
        <p className="text-zinc-500 text-xs mt-0.5 leading-relaxed">{description}</p>
      </div>
      {status === 'fail' && fixPath && (
        <a
          href={fixPath}
          className="flex-shrink-0 flex items-center gap-1 text-blue-400 hover:text-blue-300 text-xs font-medium transition-colors"
        >
          Fix
          <ChevronRight className="w-3 h-3" />
        </a>
      )}
    </div>
  )
}

// ─── Add IP Dialog ─────────────────────────────────────────────────────────────

interface AddIPDialogProps {
  open: boolean
  mode: 'blacklist' | 'whitelist'
  onClose: () => void
  onSave: (ip: string, reason: string) => void
  isPending: boolean
}

function AddIPDialog({ open, mode, onClose, onSave, isPending }: AddIPDialogProps) {
  const [ip, setIp] = useState('')
  const [reason, setReason] = useState('')

  function handleSubmit() {
    if (!ip.trim()) {
      toast.error('IP address is required')
      return
    }
    onSave(ip.trim(), reason.trim())
    setIp('')
    setReason('')
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white capitalize">
            Add to {mode}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">IP Address</label>
            <input
              type="text"
              value={ip}
              onChange={(e) => setIp(e.target.value)}
              placeholder="1.2.3.4 or 10.0.0.0/24"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">
              {mode === 'blacklist' ? 'Reason' : 'Comment'}{' '}
              <span className="text-zinc-600">(optional)</span>
            </label>
            <input
              type="text"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={mode === 'blacklist' ? 'e.g. Repeated brute-force' : 'e.g. Office IP'}
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
            />
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
            className={cn(
              'text-white',
              mode === 'blacklist'
                ? 'bg-red-600 hover:bg-red-500'
                : 'bg-green-600 hover:bg-green-500',
            )}
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Add IP
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Ban/Unban Dialog ──────────────────────────────────────────────────────────

interface BanIPDialogProps {
  open: boolean
  onClose: () => void
  onSave: (ip: string, jail: string) => void
  isPending: boolean
  mode: 'ban' | 'unban'
}

function BanIPDialog({ open, onClose, onSave, isPending, mode }: BanIPDialogProps) {
  const [ip, setIp] = useState('')
  const [jail, setJail] = useState('sshd')

  function handleSubmit() {
    if (!ip.trim()) {
      toast.error('IP address is required')
      return
    }
    onSave(ip.trim(), jail.trim() || 'sshd')
    setIp('')
    setJail('sshd')
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white capitalize">
            {mode === 'ban' ? 'Manually Ban IP' : 'Unban IP'}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">IP Address</label>
            <input
              type="text"
              value={ip}
              onChange={(e) => setIp(e.target.value)}
              placeholder="1.2.3.4"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Jail</label>
            <input
              type="text"
              value={jail}
              onChange={(e) => setJail(e.target.value)}
              placeholder="sshd"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
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
            className={cn(
              'text-white',
              mode === 'ban' ? 'bg-red-600 hover:bg-red-500' : 'bg-blue-600 hover:bg-blue-500',
            )}
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            {mode === 'ban' ? 'Ban IP' : 'Unban IP'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Section: Security Score ───────────────────────────────────────────────────

const CHECK_ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  'SSL Certificates': Lock,
  'Firewall': Shield,
  'Fail2Ban': Ban,
  'SSH Key Auth': KeyRound,
  'DKIM Signing': Globe,
}

function SecurityLoadError({ title, retry, retrying }: { title: string; retry: () => void; retrying: boolean }) {
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <AlertTriangle className="size-6 text-red-400" />
      <p className="text-sm text-red-300">{title}</p>
      <Button type="button" size="sm" variant="outline" onClick={retry} disabled={retrying}>
        <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} /> Retry
      </Button>
    </div>
  )
}

function securityMutationError(error: unknown, fallback: string) {
  if (
    typeof error === 'object'
    && error !== null
    && 'status' in error
    && error.status === 403
  ) {
    return 'Permission denied. Admin access is required for security changes.'
  }
  return fallback
}

function SecurityScoreSection() {
  const scoreQuery = useQuery<SecurityScore>({
    queryKey: ['security-score'],
    queryFn: () => api.get<SecurityScore>('/security/score'),
    retry: false,
    refetchInterval: 30_000,
  })
  const score = scoreQuery.data

  const passedCount = score
    ? score.checks.filter((c) => c.status === 'pass').length
    : 0

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-base flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-blue-400" />
          Security Score
        </CardTitle>
      </CardHeader>
      <CardContent>
        {scoreQuery.isLoading ? (
          <div className="flex flex-col sm:flex-row gap-6 items-start">
            <Skeleton className="w-36 h-32 bg-zinc-800 rounded-lg flex-shrink-0" />
            <div className="flex-1 space-y-2.5">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-14 w-full bg-zinc-800 rounded-lg" />
              ))}
            </div>
          </div>
        ) : scoreQuery.isError ? (
          <SecurityLoadError
            title="Security score could not be loaded."
            retry={() => { void scoreQuery.refetch() }}
            retrying={scoreQuery.isFetching}
          />
        ) : score ? (
          <div className="flex flex-col sm:flex-row gap-6 items-start">
            {/* Gauge */}
            <div className="flex-shrink-0 flex flex-col items-center gap-3">
              <ScoreGauge score={score.score} />
              <div className="text-center">
                <p className="text-zinc-400 text-xs">
                  <span className="text-white font-semibold">{passedCount}</span>
                  {' / '}
                  {score.checks.length} checks passed
                </p>
              </div>
            </div>
            {/* Checks */}
            <div className="flex-1 space-y-2">
              {score.checks.map((check) => (
                <CheckItem
                  key={check.name}
                  label={check.name}
                  description={check.detail}
                  status={check.status}
                  icon={CHECK_ICONS[check.name] ?? Shield}
                />
              ))}
            </div>
          </div>
        ) : (
          <div className="py-8 text-center text-sm text-zinc-600">Security score is not available.</div>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Section: Fail2Ban ─────────────────────────────────────────────────────────

function Fail2BanRemediation({
  status,
  retry,
  retrying,
}: {
  status: Fail2BanStatus
  retry: () => void
  retrying: boolean
}) {
  if (status.state === 'not-installed') {
    return (
      <DependencyRemediation
        state="not-configured"
        title="Fail2Ban is not installed"
        summary="IP ban automation is optional and unavailable because fail2ban-client is missing. Heyserver will not install host packages automatically."
        retry={retry}
        retrying={retrying}
        steps={[
          'Install Fail2Ban using this Linux distribution’s supported package manager.',
          <>Enable it with <code className="text-zinc-100">systemctl enable --now fail2ban</code>.</>,
          'Retry detection; jail inventory and IP actions unlock only after the daemon is healthy.',
        ]}
      />
    )
  }
  if (status.state === 'stopped') {
    return (
      <DependencyRemediation
        state="stopped"
        title="Fail2Ban is installed but stopped"
        summary="Heyserver pauses ban and unban actions because the local Fail2Ban daemon is not enforcing jail state."
        retry={retry}
        retrying={retrying}
        steps={[
          <>Inspect the failure with <code className="text-zinc-100">systemctl status fail2ban</code>.</>,
          <>After correcting its configuration, run <code className="text-zinc-100">systemctl start fail2ban</code>.</>,
          'Retry detection. Heyserver will not present an empty jail inventory while the daemon is stopped.',
        ]}
      />
    )
  }
  return (
    <DependencyRemediation
      title="Fail2Ban readiness is unavailable"
      summary="The client exists, but Heyserver could not verify the daemon and complete jail inventory, so IP actions remain paused."
      retry={retry}
      retrying={retrying}
      steps={[
        <>Run <code className="text-zinc-100">systemctl is-active fail2ban</code> and <code className="text-zinc-100">fail2ban-client status</code>.</>,
        'Correct daemon, socket, or service-account access without exposing an arbitrary command boundary.',
        'Retry detection after all configured jails can be read successfully.',
      ]}
    />
  )
}

function Fail2BanSection() {
  const queryClient = useQueryClient()
  const [banDialog, setBanDialog] = useState<'ban' | 'unban' | null>(null)
  const [expandedJail, setExpandedJail] = useState<string | null>(null)

  const statusQuery = useQuery<Fail2BanStatus>({
    queryKey: ['fail2ban-status'],
    queryFn: () => api.get<Fail2BanStatus>('/security/fail2ban/status'),
    refetchInterval: 20_000,
  })
  const status = statusQuery.data

  const jailQuery = useQuery<{ banned: BannedIP[] }>({
    queryKey: ['fail2ban-jail', expandedJail],
    queryFn: () => api.get(`/security/fail2ban/jails/${expandedJail}`),
    enabled: expandedJail !== null && status?.available === true,
  })
  const jailDetail = jailQuery.data

  const banMutation = useMutation({
    mutationFn: ({ ip, jail }: { ip: string; jail: string }) =>
      api.post('/security/fail2ban/ban', { ip, jail }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['fail2ban-status'] })
      queryClient.invalidateQueries({ queryKey: ['fail2ban-jail'] })
      setBanDialog(null)
      toast.success('IP banned successfully')
    },
    onError: (error: unknown) => toast.error(securityMutationError(error, 'Failed to ban IP')),
  })

  const unbanMutation = useMutation({
    mutationFn: ({ ip, jail }: { ip: string; jail: string }) =>
      api.post('/security/fail2ban/unban', { ip, jail }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['fail2ban-status'] })
      queryClient.invalidateQueries({ queryKey: ['fail2ban-jail'] })
      setBanDialog(null)
      toast.success('IP unbanned')
    },
    onError: (error: unknown) => toast.error(securityMutationError(error, 'Failed to unban IP')),
  })

  const jails = status?.jails ?? []

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between flex-wrap gap-3">
          <CardTitle className="text-white text-base flex items-center gap-2">
            <Ban className="w-4 h-4 text-orange-400" />
            Fail2Ban
          </CardTitle>
          <div className="flex items-center gap-2">
            {statusQuery.isLoading ? (
              <Skeleton className="h-7 w-24 bg-zinc-800" />
            ) : statusQuery.isError ? (
              <div className="flex items-center gap-1.5 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-1 text-xs font-medium text-red-400">
                <ShieldOff className="size-3.5" /> Unavailable
              </div>
            ) : status?.available ? (
              <div
                className={cn(
                  'flex items-center gap-1.5 px-3 py-1 rounded-lg border text-xs font-medium',
                  'bg-green-500/10 border-green-500/20 text-green-400',
                )}
              >
                <ShieldCheck className="w-3.5 h-3.5" />
                Running
              </div>
            ) : (
              <div className="flex items-center gap-1.5 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-1 text-xs font-medium text-amber-300">
                <ShieldOff className="size-3.5" />
                {status?.state === 'not-installed' ? 'Not Installed' : status?.state === 'stopped' ? 'Stopped' : 'Unavailable'}
              </div>
            )}
            {!statusQuery.isLoading && !statusQuery.isError && status?.available && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 h-7 text-xs"
                  onClick={() => setBanDialog('unban')}
                >
                  Unban IP
                </Button>
                <Button
                  size="sm"
                  className="bg-red-600 hover:bg-red-500 text-white h-7 text-xs"
                  onClick={() => setBanDialog('ban')}
                >
                  <Ban className="w-3 h-3 mr-1" />
                  Ban IP
                </Button>
              </>
            )}
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Jails list */}
        {statusQuery.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full bg-zinc-800 rounded-lg" />
            ))}
          </div>
        ) : statusQuery.isError ? (
          <SecurityLoadError
            title="Fail2Ban status could not be loaded. IP ban controls are paused."
            retry={() => { void statusQuery.refetch() }}
            retrying={statusQuery.isFetching}
          />
        ) : status && !status.available ? (
          <Fail2BanRemediation
            status={status}
            retry={() => { void statusQuery.refetch() }}
            retrying={statusQuery.isFetching}
          />
        ) : jails.length === 0 ? (
          <div className="flex items-center justify-center py-8 text-zinc-600 gap-2">
            <Server className="w-4 h-4" />
            <span className="text-sm">No jails found</span>
          </div>
        ) : (
          <div className="space-y-1.5">
            {jails.map((jail) => (
              <div key={jail.name}>
                <button
                  onClick={() =>
                    setExpandedJail(expandedJail === jail.name ? null : jail.name)
                  }
                  className={cn(
                    'w-full flex items-center gap-3 px-4 py-3 rounded-lg border transition-colors text-left',
                    expandedJail === jail.name
                      ? 'bg-zinc-800 border-zinc-700'
                      : 'bg-zinc-800/40 border-zinc-800 hover:bg-zinc-800/70',
                  )}
                >
                  <div
                    className={cn(
                      'w-1.5 h-1.5 rounded-full flex-shrink-0',
                      jail.currentlyBanned > 0 ? 'bg-orange-400' : 'bg-green-400',
                    )}
                  />
                  <span className="flex-1 text-white text-sm font-medium font-mono">
                    {jail.name}
                  </span>
                  <div className="flex items-center gap-3">
                    <span className="text-zinc-500 text-xs">
                      Failed:{' '}
                      <span className="text-zinc-300 font-mono">{jail.currentlyFailed ?? 0}</span>
                    </span>
                    <div
                      className={cn(
                        'px-2 py-0.5 rounded text-xs font-mono font-semibold',
                        jail.currentlyBanned > 0
                          ? 'bg-orange-500/15 text-orange-400'
                          : 'bg-zinc-800 text-zinc-500',
                      )}
                    >
                      {jail.currentlyBanned} banned
                    </div>
                    <ChevronRight
                      className={cn(
                        'w-3.5 h-3.5 text-zinc-600 transition-transform',
                        expandedJail === jail.name && 'rotate-90',
                      )}
                    />
                  </div>
                </button>

                {/* Expanded jail detail */}
                {expandedJail === jail.name && (
                  <div className="mt-1 ml-4 pl-3 border-l border-zinc-800">
                    {jailQuery.isLoading ? (
                      <div className="py-3 space-y-2">
                        {Array.from({ length: 2 }).map((_, i) => (
                          <Skeleton key={i} className="h-8 w-full bg-zinc-800" />
                        ))}
                      </div>
                    ) : jailQuery.isError ? (
                      <SecurityLoadError
                        title={`Jail details for ${jail.name} could not be loaded.`}
                        retry={() => { void jailQuery.refetch() }}
                        retrying={jailQuery.isFetching}
                      />
                    ) : jailDetail?.banned && jailDetail.banned.length > 0 ? (
                      <div className="py-2">
                        <p className="text-zinc-500 text-xs mb-2 px-2">Recently banned IPs</p>
                        <div className="rounded-lg overflow-hidden border border-zinc-800">
                          <Table>
                            <TableHeader>
                              <TableRow className="border-zinc-800 hover:bg-transparent">
                                <TableHead className="text-zinc-500 font-medium text-xs h-8">
                                  IP Address
                                </TableHead>
                                <TableHead className="text-zinc-500 font-medium text-xs h-8">
                                  Banned At
                                </TableHead>
                                <TableHead className="text-zinc-500 font-medium text-xs h-8 text-right">
                                  Action
                                </TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {jailDetail.banned.map((entry) => (
                                <TableRow
                                  key={`${entry.ip}-${entry.jail}`}
                                  className="border-zinc-800 hover:bg-zinc-800/50"
                                >
                                  <TableCell className="font-mono text-xs text-red-400 py-2">
                                    {entry.ip}
                                  </TableCell>
                                  <TableCell className="text-xs text-zinc-500 py-2">
                                    {entry.banned_at ?? '—'}
                                  </TableCell>
                                  <TableCell className="py-2 text-right">
                                    <button
                                      onClick={() => {
                                        unbanMutation.mutate({ ip: entry.ip, jail: entry.jail })
                                      }}
                                      disabled={!status?.available || unbanMutation.isPending}
                                      className="text-zinc-500 hover:text-blue-400 text-xs transition-colors"
                                    >
                                      Unban
                                    </button>
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      </div>
                    ) : (
                      <p className="text-zinc-600 text-xs py-3 px-2">No banned IPs in this jail</p>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </CardContent>

      {/* Dialogs */}
      {banDialog && (
        <BanIPDialog
          open={true}
          mode={banDialog}
          onClose={() => setBanDialog(null)}
          onSave={(ip, jail) => {
            if (banDialog === 'ban') {
              banMutation.mutate({ ip, jail })
            } else {
              unbanMutation.mutate({ ip, jail })
            }
          }}
          isPending={!status?.available || banMutation.isPending || unbanMutation.isPending}
        />
      )}
    </Card>
  )
}

// ─── Section: IP Management ────────────────────────────────────────────────────

function IPManagementSection() {
  const queryClient = useQueryClient()
  const [addDialog, setAddDialog] = useState<'blacklist' | 'whitelist' | null>(null)

  const blacklistQuery = useQuery<IPListEntry[]>({
    queryKey: ['security', 'ip-list', 'blacklist'],
    queryFn: async () => await api.get<IPListEntry[]>('/security/ip-blacklist') ?? [],
    refetchInterval: 30_000,
  })
  const whitelistQuery = useQuery<IPListEntry[]>({
    queryKey: ['security', 'ip-list', 'whitelist'],
    queryFn: async () => await api.get<IPListEntry[]>('/security/ip-whitelist') ?? [],
    refetchInterval: 30_000,
  })

  const addIPMutation = useMutation({
    mutationFn: ({ ip, comment, listType }: { ip: string; comment: string; listType: 'blacklist' | 'whitelist' }) =>
      api.post(`/security/ip-${listType}`, { ip, comment }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['security', 'ip-list', variables.listType] })
      setAddDialog(null)
      toast.success(`IP added to ${variables.listType}`)
    },
    onError: (error: unknown, variables) => toast.error(securityMutationError(error, `Failed to add IP to ${variables.listType}`)),
  })

  const removeIPMutation = useMutation({
    mutationFn: ({ ip, listType }: { ip: string; listType: 'blacklist' | 'whitelist' }) =>
      api.delete(`/security/ip-${listType}/${encodeURIComponent(ip)}`),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['security', 'ip-list', variables.listType] })
      toast.success(`IP removed from ${variables.listType}`)
    },
    onError: (error: unknown, variables) => toast.error(securityMutationError(error, `Failed to remove IP from ${variables.listType}`)),
  })

  const blacklist = blacklistQuery.data ?? []
  const whitelist = whitelistQuery.data ?? []

  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-base flex items-center gap-2">
          <Globe className="w-4 h-4 text-red-400" />
          IP Management
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        {/* Blacklist */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <div>
              <p className="text-white text-sm font-semibold">Blacklist</p>
              <p className="text-zinc-500 text-xs mt-0.5">Permanently blocked IPs</p>
            </div>
            {!blacklistQuery.isLoading && !blacklistQuery.isError && (
              <Button
                size="sm"
                className="bg-red-600 hover:bg-red-500 text-white h-7 text-xs"
                onClick={() => setAddDialog('blacklist')}
              >
                <Plus className="w-3 h-3 mr-1" />
                Add IP
              </Button>
            )}
          </div>

          {blacklistQuery.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-10 bg-zinc-800 rounded-lg" />
              ))}
            </div>
          ) : blacklistQuery.isError ? (
            <SecurityLoadError
              title="IP blacklist could not be loaded. Blacklist controls are paused."
              retry={() => { void blacklistQuery.refetch() }}
              retrying={blacklistQuery.isFetching}
            />
          ) : blacklist.length === 0 ? (
            <div className="flex items-center justify-center py-6 rounded-lg bg-zinc-800/30 border border-dashed border-zinc-800">
              <p className="text-zinc-600 text-sm">No blacklisted IPs</p>
            </div>
          ) : (
            <div className="rounded-lg overflow-hidden border border-zinc-800">
              <Table>
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">
                      IP Address
                    </TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">Reason</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">Added</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9 text-right w-12">
                      Del
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {blacklist.map((entry) => (
                    <TableRow
                      key={entry.ip}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="font-mono text-xs text-red-400 py-2.5">
                        {entry.ip}
                      </TableCell>
                      <TableCell className="text-xs text-zinc-400 py-2.5">
                        {entry.comment || '—'}
                      </TableCell>
                      <TableCell className="text-xs text-zinc-500 py-2.5">
                        {entry.createdAt}
                      </TableCell>
                      <TableCell className="py-2.5 text-right">
                        <button
                          onClick={() => removeIPMutation.mutate({ ip: entry.ip, listType: 'blacklist' })}
                          disabled={removeIPMutation.isPending}
                          className="text-zinc-500 hover:text-red-400 p-1 rounded transition-colors"
                          title="Remove from blacklist"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>

        {/* Divider */}
        <div className="border-t border-zinc-800" />

        {/* Whitelist */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <div>
              <p className="text-white text-sm font-semibold">Whitelist</p>
              <p className="text-zinc-500 text-xs mt-0.5">Trusted IPs exempt from rate limiting and bans</p>
            </div>
            {!whitelistQuery.isLoading && !whitelistQuery.isError && (
              <Button
                size="sm"
                variant="outline"
                className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 h-7 text-xs"
                onClick={() => setAddDialog('whitelist')}
              >
                <Plus className="w-3 h-3 mr-1" />
                Add IP
              </Button>
            )}
          </div>

          {whitelistQuery.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 2 }).map((_, i) => (
                <Skeleton key={i} className="h-10 bg-zinc-800 rounded-lg" />
              ))}
            </div>
          ) : whitelistQuery.isError ? (
            <SecurityLoadError
              title="IP whitelist could not be loaded. Whitelist controls are paused."
              retry={() => { void whitelistQuery.refetch() }}
              retrying={whitelistQuery.isFetching}
            />
          ) : whitelist.length === 0 ? (
            <div className="flex items-center justify-center py-6 rounded-lg bg-zinc-800/30 border border-dashed border-zinc-800">
              <p className="text-zinc-600 text-sm">No whitelisted IPs</p>
            </div>
          ) : (
            <div className="rounded-lg overflow-hidden border border-zinc-800">
              <Table>
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">
                      IP Address
                    </TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">Comment</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9">Added</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs h-9 text-right w-12">
                      Del
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {whitelist.map((entry) => (
                    <TableRow
                      key={entry.ip}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="font-mono text-xs text-green-400 py-2.5">
                        {entry.ip}
                      </TableCell>
                      <TableCell className="text-xs text-zinc-400 py-2.5">
                        {entry.comment || '—'}
                      </TableCell>
                      <TableCell className="text-xs text-zinc-500 py-2.5">
                        {entry.createdAt}
                      </TableCell>
                      <TableCell className="py-2.5 text-right">
                        <button
                          onClick={() => removeIPMutation.mutate({ ip: entry.ip, listType: 'whitelist' })}
                          disabled={removeIPMutation.isPending}
                          className="text-zinc-500 hover:text-red-400 p-1 rounded transition-colors"
                          title="Remove from whitelist"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      </CardContent>

      {/* Dialogs */}
      {addDialog && (
        <AddIPDialog
          open={true}
          mode={addDialog}
          onClose={() => setAddDialog(null)}
          onSave={(ip, comment) => {
            addIPMutation.mutate({ ip, comment, listType: addDialog })
          }}
          isPending={addIPMutation.isPending}
        />
      )}
    </Card>
  )
}

// ─── Main Security Page ────────────────────────────────────────────────────────

function SecurityPermissionState({
  state,
  retry,
  retrying,
}: {
  state: 'loading' | 'unavailable' | 'denied'
  retry: () => void
  retrying: boolean
}) {
  const unavailable = state === 'unavailable'
  return (
    <Card className="border-amber-500/20 bg-amber-500/[0.05]">
      <CardContent className="flex flex-col items-center gap-3 py-8 text-center">
        {state === 'loading' ? (
          <Loader2 className="size-6 animate-spin text-amber-300" />
        ) : (
          <Lock className="size-6 text-amber-300" />
        )}
        <div>
          <p className="text-sm font-medium text-amber-200">
            {state === 'loading'
              ? 'Verifying security permissions…'
              : unavailable
                ? 'Security permissions could not be verified.'
                : 'Admin access is required for security administration.'}
          </p>
          <p className="mt-1 text-xs text-zinc-400">
            {state === 'loading'
              ? 'Administrative inventory and mutation controls remain paused until identity verification completes.'
              : unavailable
                ? 'Administrative inventory and mutation controls remain paused. Read-only security score data is still available.'
                : 'Your role can view the read-only security score, but Fail2Ban and IP management are restricted to admins.'}
          </p>
        </div>
        {unavailable && (
          <Button type="button" size="sm" variant="outline" onClick={retry} disabled={retrying}>
            <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} /> Retry permission
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

export default function Security() {
  const currentUserQuery = useCurrentUser()
  const permissionState = currentUserQuery.isLoading
    ? 'loading'
    : currentUserQuery.isError || !currentUserQuery.data
      ? 'unavailable'
      : currentUserQuery.data.role === 'admin'
        ? 'admin'
        : 'denied'

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-white text-xl font-bold">Security</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            Security advisor — score, intrusion prevention, and IP management
          </p>
        </div>
        <div className="flex items-center gap-2">
          <RefreshCw className="size-3.5 text-zinc-600" />
          <span className="text-zinc-500 text-xs">Auto-refresh</span>
        </div>
      </div>

      <SecurityScoreSection />
      {permissionState === 'admin' ? (
        <>
          <Fail2BanSection />
          <IPManagementSection />
        </>
      ) : (
        <SecurityPermissionState
          state={permissionState}
          retry={() => { void currentUserQuery.refetch() }}
          retrying={currentUserQuery.isFetching}
        />
      )}
    </div>
  )
}

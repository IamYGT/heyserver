import { useState, useEffect, useRef } from 'react'
import { useTheme } from '@/hooks/useTheme'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  LayoutDashboard,
  Globe,
  Globe2,
  FileText,
  Activity,
  Cpu,
  Mail,
  LogOut,
  Menu,
  X,
  Server,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Terminal,
  Shield,
  Users,
  ScrollText,
  Settings,
  Lock,
  FolderOpen,
  HardDrive,
  Code2,
  Database,
  Archive,
  Clock,
  Container,
  Rocket,
  Layers3,
  Bell,
  Wifi,
  Cloud,
  Sun,
  Moon,
  Radar,
  Info,
  AlertTriangle,
  CheckCircle2,
  Loader2,
  RotateCcw,
  RefreshCw,
  Braces,
} from 'lucide-react'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { useCurrentUser, useLogout } from '@/hooks/useAuth'
import { useServiceStatuses, useSystemStats } from '@/hooks/useStats'
import { CommandPalette } from '@/components/CommandPalette'
import { ServerQuickControls } from '@/components/ServerQuickControls'
import { useNow } from '@/hooks/useNow'
import { hostActionStatusKey, useHostActionStatus } from '@/hooks/useHostActionStatus'
import { useCommandPalette } from '@/hooks/useCommandPalette'
import { Breadcrumb } from '@/components/Breadcrumb'
import { api } from '@/lib/api'
import type { Backup, BackupStorageSummary, GDriveOAuthCompleteRequest, GDriveStatus } from '@/lib/types'
import {
  LOCAL_SERVER_ID,
  managedNodeOnline,
  isLocalServer,
  managedNavigationTarget,
  managedNodePath,
  managedServerContextForLocation,
  persistedManagedServerID,
  resolvePersistedManagedServer,
  SELECTED_SERVER_KEY,
  serverSwitchTarget,
  serverHealthIssueTarget,
  type ManagedServerID,
} from '@/lib/serverNavigation'
import {
  auditEntryTimestamp,
  auditEventPresentation,
  groupAuditNotifications,
  type AuditEventTone,
} from '@/lib/auditScope'
import { localServerHealth, remoteServerHealth, type ServerHealth } from '@/lib/serverHealth'
import { waitForAgentTask, type AgentTask } from '@/lib/agentTasks'
import {
  hostActionConfirmation, hostActionEndpoint, hostActionLabel, swapResetAvailability,
  type SwapMemorySnapshot,
} from '@/lib/hostControls'
import { GDRIVE_OAUTH_CHANNEL, gdriveOAuthState, openGDriveOAuthPopup } from '@/lib/gdriveOAuth'
import { backupOperationHint } from '@/components/backup/backupErrorHints'

// Types
interface AuditLog {
  id: string | number
  createdAt?: string
  timestamp?: string
  userName?: string
  user?: string
  action: string
  resource: string
  details?: string
  ip: string
}

interface NavItem {
  path: string
  label: string
  icon: React.ComponentType<{ className?: string }>
  external?: boolean
}

interface NavSection {
  title: string
  items: NavItem[]
}

// Navigation structure
const navSections: NavSection[] = [
  {
    title: 'INFRASTRUCTURE',
    items: [
      { path: '/', label: 'Dashboard', icon: LayoutDashboard },
      { path: '/servers', label: 'Servers', icon: Server },
      { path: '/domains', label: 'Domains', icon: Globe },
      { path: '/nginx', label: 'Nginx', icon: FileText },
      { path: '/ssl', label: 'SSL', icon: Shield },
      { path: '/php', label: 'PHP', icon: Code2 },
      { path: '/pm2', label: 'PM2', icon: Cpu },
    ],
  },
  {
    title: 'MONITORING',
    items: [
      { path: '/monitoring', label: 'Monitoring', icon: Activity },
      { path: '/uptime', label: 'Uptime', icon: Radar },
      { path: '/logs', label: 'Logs', icon: ScrollText },
      { path: '/mail', label: 'Mail', icon: Mail },
      { path: '/dns', label: 'DNS', icon: Globe2 },
      { path: '/cloudflare', label: 'Cloudflare', icon: Cloud },
    ],
  },
  {
    title: 'TOOLS',
    items: [
      { path: '/firewall', label: 'Firewall', icon: Lock },
      { path: '/files', label: 'Files', icon: FolderOpen },
      { path: '/disk', label: 'Disk', icon: HardDrive },
      { path: '/databases', label: 'Database', icon: Database },
      { path: '/backups', label: 'Backups', icon: Archive },
      { path: '/cron', label: 'Cron', icon: Clock },
      { path: '/docker', label: 'Docker', icon: Container },
      { path: '/deploy', label: 'Deploy', icon: Rocket },
      { path: '/integrations', label: 'Integrations', icon: Layers3 },
      { path: '/terminal', label: 'Terminal', icon: Terminal },
    ],
  },
  {
    title: 'WEBMAIL',
    items: [
      { path: '/webmail', label: 'Webmail', icon: ExternalLink, external: true },
    ],
  },
]

const adminNavItems: NavItem[] = [
  { path: '/security', label: 'Security', icon: Shield },
  { path: '/notifications', label: 'Notifications', icon: Bell },
  { path: '/users', label: 'Users', icon: Users },
  { path: '/audit', label: 'Audit', icon: ScrollText },
  { path: '/settings', label: 'Settings', icon: Settings },
  { path: '/about', label: 'About', icon: Info },
  { path: '/developer/api', label: 'Developer API', icon: Braces },
]

const routeLabels: Record<string, string> = {
  '/': 'Dashboard',
  '/servers': 'Servers',
  '/domains': 'Domains',
  '/nginx': 'Nginx',
  '/ssl': 'SSL',
  '/php': 'PHP',
  '/pm2': 'PM2',
  '/monitoring': 'Monitoring',
  '/uptime': 'Uptime',
  '/logs': 'Logs',
  '/mail': 'Mail',
  '/dns': 'DNS',
  '/cloudflare': 'Cloudflare',
  '/webmail': 'Webmail',
  '/firewall': 'Firewall',
  '/files': 'Files',
  '/disk': 'Disk',
  '/databases': 'Database',
  '/backups': 'Backups',
  '/cron': 'Cron',
  '/docker': 'Docker',
  '/deploy': 'Deploy',
  '/integrations': 'Integrations',
  '/terminal': 'Terminal',
  '/security': 'Security',
  '/notifications': 'Notifications',
  '/users': 'Users',
  '/audit': 'Audit Log',
  '/settings': 'Settings',
  '/about': 'About',
  '/developer/api': 'Developer API',
}

// Helpers
const UNREAD_KEY = 'hserver_notif_last_seen'
const COLLAPSED_KEY = 'hserver_sidebar_collapsed'

function readPersistedServer(): string | null {
  try {
    return localStorage.getItem(SELECTED_SERVER_KEY)
  } catch {
    return null
  }
}

function writePersistedServer(server: ManagedServerID): void {
  try {
    localStorage.setItem(SELECTED_SERVER_KEY, server)
  } catch {
    // URL context and the in-memory selection remain authoritative when storage is unavailable.
  }
}

interface ManagedNodeSummary {
  id: string
  name: string
  hostname: string
  capabilities: string[]
  last_seen_at?: string
	online?: boolean
  inventory?: {
    uptime_seconds?: number
    memory_total_bytes?: number
    memory_available_bytes?: number
    disk_total_bytes: number
    disk_used_bytes?: number
    disk_available_bytes: number
    disk_use_percent?: number
    services: Array<{ name: string; active: string; sub?: string }>
  }
}

interface ManagedNodeMemoryState {
  memory_total_bytes: number
  memory_available_bytes: number
  swap_total_bytes: number
  swap_used_bytes: number
  swap_free_bytes: number
  swap_reset_eligible: boolean
  swap_reset_reason?: string
}

const healthTone = {
  loading: { dot: 'bg-zinc-500', text: 'text-zinc-400', border: 'border-zinc-700' },
  healthy: { dot: 'bg-emerald-400', text: 'text-emerald-400', border: 'border-zinc-700' },
  warning: { dot: 'bg-amber-400', text: 'text-amber-400', border: 'border-amber-500/40' },
  critical: { dot: 'bg-red-400', text: 'text-red-400', border: 'border-red-500/45' },
} as const

function OperationalStatus({ health, selectedServer, serverLabel, memory, hostActionAvailable, serviceActionAvailable }: { health: ServerHealth; selectedServer: ManagedServerID; serverLabel: string; memory?: SwapMemorySnapshot; hostActionAvailable: boolean; serviceActionAvailable: boolean }) {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: currentUser } = useCurrentUser()
  const ref = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
	const tone = healthTone[health.level]
	const canManage = currentUser?.role === 'admin'
	const actionStatus = useHostActionStatus(selectedServer, canManage && hostActionAvailable)
	const actionStatusUnavailable = canManage && hostActionAvailable && (actionStatus.isLoading || actionStatus.isError)
	const refreshHealth = useMutation({
		mutationFn: async () => {
			const queryKeys = isLocalServer(selectedServer)
				? [['stats', 'system'], ['stats', 'services'], ['gdrive-status'], ['backups']]
				: [['managed-nodes'], ['managed-node-memory', selectedServer]]
			await Promise.all(queryKeys.map((queryKey) => queryClient.refetchQueries(
				{ queryKey, type: 'active' },
				{ throwOnError: true },
			)))
		},
		onSuccess: () => toast.success(`${serverLabel} health checks refreshed`),
		onError: (error: Error) => toast.error(error.message || 'Health checks could not be refreshed'),
	})

  const restartService = useMutation<string, Error, { service: string; server: ManagedServerID }>({
    mutationFn: async ({ service, server }) => {
      if (isLocalServer(server)) {
        const result = await api.post<{ message: string }>('/system/actions/service', { service, action: 'restart' })
        return result.message
      }
      const task = await api.post<AgentTask>(managedNodePath(server, '/tasks'), {
        kind: 'service.action',
        payload: { service, action: 'restart' },
        confirmed: true,
      })
      const completed = await waitForAgentTask(task, (taskID) => api.get<AgentTask>(managedNodePath(server, `/tasks/${taskID}`)))
      const finalState = [completed.result?.active, completed.result?.sub].filter(Boolean).join('/')
      return `${service} restart completed${finalState ? ` · ${finalState}` : ''}`
    },
    onSuccess: async (message, variables) => {
      toast.success(message)
      if (isLocalServer(variables.server)) {
        await queryClient.invalidateQueries({ queryKey: ['stats', 'services'] })
      } else {
        await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
        await queryClient.invalidateQueries({ queryKey: ['managed-node-tasks', variables.server] })
      }
    },
    onError: (error) => toast.error(error.message || 'Service restart failed'),
  })

  const resetSwap = useMutation<{ message: string }, Error, ManagedServerID>({
    mutationFn: (server) => api.post(hostActionEndpoint(server, 'swap-reset')),
    onSuccess: async (result, server) => {
      toast.success(result.message)
      if (isLocalServer(server)) {
        await queryClient.invalidateQueries({ queryKey: ['stats', 'system'] })
      } else {
        await queryClient.invalidateQueries({ queryKey: ['managed-node-memory', server] })
        await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
      }
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', server] })
    },
    onError: (error) => toast.error(error.message || 'Swap reset failed'),
		onSettled: (_result, _error, server) => queryClient.invalidateQueries({ queryKey: hostActionStatusKey(server) }),
  })

  const reconnectGDrive = useMutation({
    mutationFn: () => openGDriveOAuthPopup(),
    onSuccess: () => toast.info('Google hesabınızla giriş yapın — panel bağlantıyı otomatik tamamlayacak'),
    onError: (error: Error) => toast.error(error.message || 'Google Drive OAuth başlatılamadı'),
  })

  const requestRestart = (service: string) => {
    if (window.confirm(`Restart ${service} on ${serverLabel} now? A short interruption may occur.`)) {
      restartService.mutate({ service, server: selectedServer })
    }
  }


  const requestSwapReset = () => {
    const requiredAvailable = swapResetAvailability(memory).requiredAvailable
    if (window.confirm(hostActionConfirmation('swap-reset', serverLabel, memory, requiredAvailable))) {
      resetSwap.mutate(selectedServer)
    }
  }

  const requestGDriveReconnect = () => reconnectGDrive.mutate()

  useEffect(() => {
    if (!open) return
    const closeOutside = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false)
    }
    const closeWithKeyboard = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      setOpen(false)
      requestAnimationFrame(() => triggerRef.current?.focus())
    }
    document.addEventListener('mousedown', closeOutside)
    document.addEventListener('keydown', closeWithKeyboard)
    return () => {
      document.removeEventListener('mousedown', closeOutside)
      document.removeEventListener('keydown', closeWithKeyboard)
    }
  }, [open])

  return (
    <div ref={ref} className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-label={`Server health: ${health.label}`}
        aria-expanded={open}
        aria-controls="server-health-panel"
        className={cn(
          'flex h-8 items-center gap-1.5 rounded-lg border bg-zinc-950/50 px-2.5 text-xs transition-colors hover:bg-zinc-800',
          tone.border,
          tone.text,
        )}
      >
        <span className={cn('size-2 rounded-full', tone.dot, health.level === 'healthy' && 'animate-pulse')} />
        <span className="hidden sm:inline">{health.label}</span>
      </button>

      {open && (
        <div id="server-health-panel" role="dialog" aria-label="Server health" className="absolute right-0 top-10 z-50 w-[min(22rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-zinc-700 bg-zinc-900 shadow-2xl">
          <div className="flex items-center gap-2 border-b border-zinc-800 px-4 py-3">
            {health.level === 'healthy' ? (
              <CheckCircle2 className="size-4 text-emerald-400" />
            ) : (
              <AlertTriangle className={cn('size-4', tone.text)} />
            )}
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold text-zinc-100">Server health</p>
              <p className="text-[11px] text-zinc-500">Live operational checks for the selected server</p>
            </div>
			<Button
				type="button"
				variant="ghost"
				size="icon-xs"
				disabled={refreshHealth.isPending}
				onClick={() => refreshHealth.mutate()}
				title="Refresh server health checks"
				aria-label={`Refresh ${serverLabel} health checks`}
			>
				<RefreshCw className={cn('size-3.5', refreshHealth.isPending && 'animate-spin')} />
			</Button>
          </div>
          {health.issues.length === 0 ? (
            <div className="px-4 py-5 text-center">
              <p className="text-sm font-medium text-emerald-400">No active issues</p>
              <p className="mt-1 text-xs text-zinc-500">Disk, connection and reported services look healthy.</p>
            </div>
          ) : (
            <div className="max-h-80 divide-y divide-zinc-800 overflow-y-auto">
              {health.issues.map((issue) => {
								const canRestart = currentUser?.role === 'admin' && serviceActionAvailable && !!issue.service
								const canResetSwap = currentUser?.role === 'admin' && hostActionAvailable && issue.action === 'swap-reset'
                const canReconnectGDrive = currentUser?.role === 'admin' && issue.action === 'gdrive-reconnect'
                const canAct = canRestart || canResetSwap || canReconnectGDrive
                const restarting = restartService.isPending
                  && restartService.variables?.service === issue.service
                  && restartService.variables?.server === selectedServer
								const resettingSwap = canResetSwap && ((resetSwap.isPending && resetSwap.variables === selectedServer) || (actionStatus.data?.running === true && actionStatus.data.action === 'swap-reset'))
								const retryingActionStatus = canResetSwap && actionStatus.isError && actionStatus.isFetching
								const retryableActionStatus = canResetSwap && actionStatus.isError && !actionStatus.isFetching
                const reconnectingGDrive = canReconnectGDrive && reconnectGDrive.isPending
                return (
                  <div key={issue.id} className="flex items-center transition-colors hover:bg-zinc-800/60">
                    <button
                      type="button"
                      onClick={() => { setOpen(false); navigate(serverHealthIssueTarget(selectedServer, issue.href)) }}
                      className="flex min-w-0 flex-1 items-start gap-3 px-4 py-3 text-left"
                    >
                      <AlertTriangle className={cn('mt-0.5 size-4 shrink-0', issue.level === 'critical' ? 'text-red-400' : 'text-amber-400')} />
                      <span className="min-w-0 flex-1">
                        <span className="block text-xs font-semibold text-zinc-200">{issue.title}</span>
                        <span className="mt-0.5 block break-words text-[11px] leading-relaxed text-zinc-500">{issue.detail}</span>
                      </span>
                      {!canAct && <ChevronRight className="mt-1 size-3.5 shrink-0 text-zinc-600" />}
                    </button>
                    {canAct && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
								disabled={restartService.isPending || resetSwap.isPending || reconnectGDrive.isPending || (canResetSwap && (actionStatus.isLoading || retryingActionStatus || actionStatus.data?.running === true))}
                        onClick={() => canReconnectGDrive
                          ? requestGDriveReconnect()
                          : canResetSwap
												? retryableActionStatus ? void actionStatus.refetch() : requestSwapReset()
                            : requestRestart(issue.service!)}
                        title={canReconnectGDrive
                          ? 'Reconnect Google Drive'
                          : canResetSwap
											? retryableActionStatus
												? 'Retry active host maintenance status'
												: actionStatusUnavailable
												? actionStatus.isFetching ? 'Retrying active host maintenance status' : 'Checking active host maintenance'
												: actionStatus.data?.running
												? `${hostActionLabel(actionStatus.data.action)} is already running`
												: `Reset swap on ${serverLabel}`
								: `Restart ${issue.service} on ${serverLabel}`}
                        aria-label={canReconnectGDrive
                          ? 'Reconnect Google Drive from server health'
                          : canResetSwap
												? retryableActionStatus ? 'Retry host maintenance status from server health' : 'Reset swap from server health'
                            : `Restart ${issue.service} from server health`}
                        className="mr-3 shrink-0 gap-1.5 px-2 text-amber-400 hover:bg-amber-500/10 hover:text-amber-300"
                      >
										{restarting || resettingSwap || reconnectingGDrive || retryingActionStatus
                          ? <Loader2 className="size-3 animate-spin" />
											: retryableActionStatus
												? <RefreshCw className="size-3" />
                          : canReconnectGDrive
                            ? <Cloud className="size-3" />
                            : <RotateCcw className="size-3" />}
                        <span>{retryableActionStatus
                          ? 'Retry'
                          : canReconnectGDrive
                            ? 'Reconnect'
                            : canResetSwap
                              ? 'Reset swap'
                              : 'Restart'}</span>
                      </Button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function notificationSeenKey(server: ManagedServerID): string {
  return `${UNREAD_KEY}_${server}`
}

function getLastSeen(server: ManagedServerID): number {
  return parseInt(
    localStorage.getItem(notificationSeenKey(server))
      ?? localStorage.getItem(UNREAD_KEY)
      ?? '0',
    10,
  )
}

function markAllSeen(server: ManagedServerID, ts: number) {
  localStorage.setItem(notificationSeenKey(server), String(ts))
}

function formatRelative(iso: string | undefined): string {
  if (!iso) return 'unknown time'
  const timestamp = new Date(iso).getTime()
  if (!Number.isFinite(timestamp)) return 'unknown time'
  const diff = Math.max(0, Date.now() - timestamp)
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  if (days > 0) return `${days}d ${hours}h`
  const mins = Math.floor((seconds % 3600) / 60)
  return `${hours}h ${mins}m`
}

// NotificationBell component
function NotificationBell({ selectedServer, serverLabel }: { selectedServer: ManagedServerID; serverLabel: string }) {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [lastSeen, setLastSeen] = useState<Record<ManagedServerID, number>>(() => ({
    local: getLastSeen('local'),
  }))
  const ref = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  const { data: logsResp } = useQuery<{ data: AuditLog[] }>({
    queryKey: ['audit-bell', selectedServer],
    queryFn: () => api.get<{ data: AuditLog[] }>(`/audit?server=${selectedServer}&limit=30`),
    refetchInterval: 30_000,
    staleTime: 10_000,
  })

  const recent = groupAuditNotifications(logsResp?.data ?? []).slice(0, 10)
  const unreadCount = recent.filter(
    ({ entry }) => auditEntryTimestamp(entry) > (lastSeen[selectedServer] ?? getLastSeen(selectedServer)),
  ).length

  useEffect(() => {
    if (!open) return
    const closeOutside = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    const closeWithKeyboard = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      setOpen(false)
      requestAnimationFrame(() => triggerRef.current?.focus())
    }
    document.addEventListener('mousedown', closeOutside)
    document.addEventListener('keydown', closeWithKeyboard)
    return () => {
      document.removeEventListener('mousedown', closeOutside)
      document.removeEventListener('keydown', closeWithKeyboard)
    }
  }, [open])

  const handleOpen = () => {
    setOpen((prev) => !prev)
    if (!open && unreadCount > 0) {
      const ts = Date.now()
      markAllSeen(selectedServer, ts)
      setLastSeen((current) => ({ ...current, [selectedServer]: ts }))
    }
  }

  const eventTone: Record<AuditEventTone, { icon: string; surface: string }> = {
    success: { icon: 'text-emerald-400', surface: 'bg-emerald-500/10' },
    critical: { icon: 'text-red-400', surface: 'bg-red-500/10' },
    warning: { icon: 'text-amber-400', surface: 'bg-amber-500/10' },
    info: { icon: 'text-blue-400', surface: 'bg-blue-500/10' },
    neutral: { icon: 'text-zinc-400', surface: 'bg-zinc-800' },
  }

  return (
    <div ref={ref} className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={handleOpen}
        className="relative flex h-8 w-8 items-center justify-center rounded-lg text-zinc-400 transition-colors hover:bg-zinc-800 hover:text-white"
        aria-label={`Notifications for ${serverLabel}`}
        aria-expanded={open}
        aria-controls="server-notifications-panel"
      >
        <Bell className="w-4 h-4" />
        {unreadCount > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 w-4 items-center justify-center rounded-full bg-blue-600 text-[9px] font-bold text-white">
            {unreadCount > 9 ? '9+' : unreadCount}
          </span>
        )}
      </button>

      {open && (
        <div id="server-notifications-panel" role="dialog" aria-label={`${serverLabel} notifications`} className="absolute right-0 top-10 z-50 w-80 rounded-xl border border-zinc-700 bg-zinc-900 shadow-2xl">
          <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
            <span className="text-sm font-semibold text-white">{serverLabel} notifications</span>
            <span className="text-xs text-zinc-500">{recent.length} grouped events</span>
          </div>
          <div className="max-h-80 overflow-y-auto">
            {recent.length === 0 ? (
              <p className="px-4 py-6 text-center text-xs text-zinc-500">No recent events</p>
            ) : (
              recent.map(({ entry: log, count }) => {
                const event = auditEventPresentation(log)
                const tone = eventTone[event.tone]
                const EventIcon = event.tone === 'critical' || event.tone === 'warning'
                  ? AlertTriangle
                  : event.tone === 'success'
                    ? CheckCircle2
                    : event.tone === 'info'
                      ? Clock
                      : ScrollText
                return (
                  <button
                    type="button"
                    key={log.id}
                    onClick={() => {
                      setOpen(false)
                      navigate(serverSwitchTarget(selectedServer, event.target))
                    }}
                    className="flex w-full items-start gap-3 border-b border-zinc-800/60 px-4 py-3 text-left transition-colors last:border-b-0 hover:bg-zinc-800/40"
                  >
                    <span className={cn('mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg', tone.surface)}>
                      <EventIcon className={cn('size-3.5', tone.icon)} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium text-zinc-200">
                        {event.title}{count > 1 ? ` × ${count}` : ''}
                      </span>
                      <span
                        className="mt-0.5 block truncate text-[10px] text-zinc-500"
                        title={event.detail}
                      >
                        {count > 1 ? `${count} related receipts · latest: ${event.detail}` : event.detail}
                      </span>
                      <span className="mt-1 block text-[9px] text-zinc-600">
                        {log.userName ?? log.user ?? 'System'} · {formatRelative(log.createdAt ?? log.timestamp)}
                      </span>
                    </span>
                    <ChevronRight className="mt-2 size-3.5 shrink-0 text-zinc-700" />
                  </button>
                )
              })
            )}
          </div>
          <div className="border-t border-zinc-800 px-4 py-2.5">
            <button
              onClick={() => {
                setOpen(false)
                navigate(serverSwitchTarget(selectedServer, '/audit'))
              }}
              className="text-xs text-blue-400 hover:text-blue-300 transition-colors"
            >
              View all in Audit Log &rarr;
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

// Layout component
interface LayoutProps {
  children: React.ReactNode
}

export default function Layout({ children }: LayoutProps) {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const mobileMenuTriggerRef = useRef<HTMLButtonElement>(null)
  const { theme, toggle: toggleTheme } = useTheme()
  const { isOpen: cmdOpen, close: cmdClose } = useCommandPalette()
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(COLLAPSED_KEY) === 'true'
    } catch {
      return false
    }
  })

  const location = useLocation()
  const navigate = useNavigate()
  const logout = useLogout()
  const { data: currentUser } = useCurrentUser()
  const queryClient = useQueryClient()
  const {
    data: managedNodes = [],
    isFetched: managedNodesFetched,
  } = useQuery<ManagedNodeSummary[]>({
    queryKey: ['managed-nodes'],
    queryFn: () => api.get('/nodes'),
    refetchInterval: 5000,
  })
  const explicitServer = managedServerContextForLocation(location.pathname, location.search)
  const persistedServer = readPersistedServer()
  const selectedServer = explicitServer
    ?? (managedNodesFetched
      ? resolvePersistedManagedServer(persistedServer, managedNodes)
      : persistedManagedServerID(persistedServer) ?? LOCAL_SERVER_ID)
  const oauthStatesInFlight = useRef(new Set<string>())
  const oauthStatesCompleted = useRef(new Set<string>())

  const completeGDriveOAuth = useMutation({
    mutationFn: (state: string) => api.post('/backups/gdrive/oauth/complete', { state } satisfies GDriveOAuthCompleteRequest),
    onSuccess: async () => {
      try {
        await api.post('/backups/gdrive/dismiss-error')
      } catch {
        // OAuth completion already clears the persisted error.
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['gdrive-status'] }),
        queryClient.invalidateQueries({ queryKey: ['gdrive-remote'] }),
        queryClient.invalidateQueries({ queryKey: ['snapshot-status'] }),
      ])
      toast.success('Google Drive bağlandı')
    },
    onError: (error: Error) => {
      const message = error.message || 'OAuth tamamlanamadı'
      toast.error(message, { description: backupOperationHint(message), duration: 12000 })
    },
  })

  useEffect(() => {
    const complete = (payload: unknown) => {
      const state = gdriveOAuthState(payload)
      if (!state || oauthStatesInFlight.current.has(state) || oauthStatesCompleted.current.has(state)) return
      oauthStatesInFlight.current.add(state)
      completeGDriveOAuth.mutate(state, {
        onSuccess: () => oauthStatesCompleted.current.add(state),
        onSettled: () => oauthStatesInFlight.current.delete(state),
      })
    }
    const onMessage = (event: MessageEvent) => {
      if (event.origin === window.location.origin) complete(event.data)
    }
    window.addEventListener('message', onMessage)

    const channel = typeof BroadcastChannel === 'undefined'
      ? null
      : new BroadcastChannel(GDRIVE_OAUTH_CHANNEL)
    if (channel) channel.onmessage = (event) => complete(event.data)

    return () => {
      window.removeEventListener('message', onMessage)
      channel?.close()
    }
  }, [completeGDriveOAuth])

  useEffect(() => {
    try {
      if (localStorage.getItem(SELECTED_SERVER_KEY) !== selectedServer) {
        localStorage.setItem(SELECTED_SERVER_KEY, selectedServer)
      }
    } catch {
      // URL context remains authoritative when browser storage is unavailable.
    }
  }, [selectedServer])
  const localSelected = isLocalServer(selectedServer)
  const { data: stats } = useSystemStats(localSelected)
  const { data: serviceStatuses } = useServiceStatuses(localSelected)
  const { data: gdriveStatus } = useQuery<GDriveStatus>({
    queryKey: ['gdrive-status'],
    queryFn: () => api.get<GDriveStatus>('/backups/gdrive/status'),
    enabled: localSelected,
    staleTime: 60_000,
    refetchInterval: 60_000,
  })
  const { data: backupData } = useQuery<{ backups: Backup[]; storage: BackupStorageSummary }>({
    queryKey: ['backups'],
    queryFn: () => api.get<{ backups: Backup[]; storage: BackupStorageSummary }>('/backups'),
    enabled: localSelected,
    staleTime: 60_000,
    refetchInterval: 60_000,
  })
  const now = useNow()
  const selectedNode = managedNodes.find((node) => node.id === selectedServer)
  const selectedNodeOnline = !localSelected && managedNodeOnline(selectedNode, now)
  const selectedOnline = localSelected || selectedNodeOnline
  const hostActionAvailable = localSelected || (selectedNode?.capabilities?.includes('host.action') ?? false)
  const serviceActionAvailable = localSelected || (selectedNode?.capabilities?.includes('service.action') ?? false)
  const terminalAvailable = localSelected || (selectedNode?.capabilities?.includes('terminal') ?? false)
  const selectedServerLabel = localSelected
    ? 'Heyserver'
    : selectedNode?.name || selectedNode?.hostname || selectedServer
  const {
    data: selectedNodeMemory,
    isError: selectedNodeMemoryError,
    isRefetchError: selectedNodeMemoryRefetchError,
  } = useQuery<ManagedNodeMemoryState>({
    queryKey: ['managed-node-memory', selectedServer],
    queryFn: () => api.get(managedNodePath(selectedServer, '/memory')),
    enabled: selectedNodeOnline,
    refetchInterval: selectedNodeOnline ? 10_000 : false,
  })
  const serverHealth = localSelected
    ? localServerHealth(stats, serviceStatuses, gdriveStatus, backupData?.storage)
    : remoteServerHealth({
        nodeName: selectedServerLabel,
        online: selectedNodeOnline,
        managementStatus: selectedNodeMemoryError || selectedNodeMemoryRefetchError
          ? 'unreachable'
          : selectedNodeMemory
            ? 'reachable'
            : 'checking',
        diskTotal: selectedNode?.inventory?.disk_total_bytes,
        diskUsed: selectedNode?.inventory?.disk_used_bytes,
        diskAvailable: selectedNode?.inventory?.disk_available_bytes,
        diskUsePercent: selectedNode?.inventory?.disk_use_percent,
        memoryTotal: selectedNodeMemory?.memory_total_bytes ?? selectedNode?.inventory?.memory_total_bytes,
        memoryAvailable: selectedNodeMemory?.memory_available_bytes ?? selectedNode?.inventory?.memory_available_bytes,
        swapTotal: selectedNodeMemory?.swap_total_bytes,
        swapUsed: selectedNodeMemory?.swap_used_bytes,
        services: selectedNode?.inventory?.services,
      })
  const serverControlMemory: SwapMemorySnapshot | undefined = localSelected
    ? stats && {
        total: stats.memory.swapTotal,
        used: stats.memory.swapUsed,
        available: stats.memory.available,
      }
    : selectedNodeMemory && {
        total: selectedNodeMemory.swap_total_bytes,
        used: selectedNodeMemory.swap_used_bytes,
        available: selectedNodeMemory.memory_available_bytes,
      }

  const switchServer = (next: ManagedServerID) => {
    writePersistedServer(next)
    window.dispatchEvent(new CustomEvent('hserver:server-changed', { detail: next }))
    navigate(serverSwitchTarget(next, location.pathname, location.search))
  }

  const persistServer = (next: ManagedServerID) => {
    writePersistedServer(next)
    window.dispatchEvent(new CustomEvent('hserver:server-changed', { detail: next }))
  }

  // Touch swipe-to-close on mobile
  const touchStartX = useRef<number | null>(null)

  useEffect(() => {
    if (!sidebarOpen) return
    const closeWithKeyboard = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      setSidebarOpen(false)
      requestAnimationFrame(() => mobileMenuTriggerRef.current?.focus())
    }
    document.addEventListener('keydown', closeWithKeyboard)
    return () => document.removeEventListener('keydown', closeWithKeyboard)
  }, [sidebarOpen])

  const closeMobileNavigation = () => {
    setSidebarOpen(false)
    requestAnimationFrame(() => mobileMenuTriggerRef.current?.focus())
  }

  const handleTouchStart = (e: React.TouchEvent) => {
    touchStartX.current = e.touches[0].clientX
  }

  const handleTouchEnd = (e: React.TouchEvent) => {
    if (touchStartX.current === null) return
    const dx = e.changedTouches[0].clientX - touchStartX.current
    if (dx < -60) setSidebarOpen(false)
    touchStartX.current = null
  }

  const toggleCollapsed = () => {
    setCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem(COLLAPSED_KEY, String(next))
      } catch {
        // ignore
      }
      return next
    })
  }

  const handleLogout = () => {
    logout.mutate()
    window.location.href = '/login'
  }

  const pageTitle = routeLabels[location.pathname] ?? location.pathname.slice(1)

  // Render a single nav item
  const renderNavItem = (item: NavItem, forMobile = false) => {
    const managedTarget = managedNavigationTarget(selectedServer, item.path)
    const targetPath = managedTarget ?? item.path
    const targetURL = new URL(targetPath, window.location.origin)
    const isActive = location.pathname === targetURL.pathname
      && (!targetURL.search || location.search === targetURL.search)
    const localOnly = !localSelected && !managedTarget && !item.external
    const showLabel = !collapsed || forMobile

    const cls = cn(
      'relative flex items-center gap-3 rounded-lg text-sm transition-all duration-150 group',
      showLabel ? 'px-3 py-2' : 'justify-center py-2.5 w-10 mx-auto',
      isActive
        ? 'bg-blue-600/10 text-blue-400'
        : 'text-zinc-400 hover:text-white hover:bg-zinc-800',
    )

    const border = showLabel ? (
      <span
        className={cn(
          'absolute left-0 top-1 bottom-1 w-0.5 rounded-full transition-all duration-150',
          isActive ? 'bg-blue-500' : 'opacity-0 bg-transparent',
        )}
      />
    ) : null

    const tooltip =
      collapsed && !forMobile ? (
        <span className="absolute left-full ml-2.5 px-2 py-1 bg-zinc-800 text-white text-xs rounded whitespace-nowrap opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 border border-zinc-700 shadow-xl">
          {item.label}
        </span>
      ) : null

    if (item.external) {
      return (
        <a key={item.path} href={item.path} target="_blank" rel="noopener noreferrer" className={cls}>
          {border}
          <item.icon className="w-4 h-4 flex-shrink-0" />
          {showLabel && (
            <>
              <span className="flex-1">{item.label}</span>
              <ExternalLink className="w-3 h-3 opacity-40" />
            </>
          )}
          {tooltip}
        </a>
      )
    }

    return (
      <Link
        key={item.path}
        to={targetPath}
        title={localOnly ? `${item.label} currently manages Heyserver` : undefined}
        onClick={() => {
          setSidebarOpen(false)
          if (localOnly) persistServer('local')
        }}
        className={cls}
      >
        {border}
        <item.icon className="w-4 h-4 flex-shrink-0" />
        {showLabel && <span className="flex-1">{item.label}</span>}
        {showLabel && localOnly && <span className="text-[8px] font-semibold tracking-wider text-zinc-700">LOCAL</span>}
        {tooltip}
      </Link>
    )
  }

  const renderSections = (forMobile = false) => (
    <>
      {navSections.map((section) => (
        <div key={section.title} className="mb-3">
          {!collapsed || forMobile ? (
            <p className="px-3 mb-1 text-[10px] font-semibold tracking-widest text-zinc-600 uppercase select-none">
              {section.title}
            </p>
          ) : (
            <div className="mx-auto mb-2 h-px bg-zinc-800 w-6" />
          )}
          <div className="space-y-0.5">
            {section.items.map((item) => renderNavItem(item, forMobile))}
          </div>
        </div>
      ))}
    </>
  )

  const renderAdmin = (forMobile = false) => (
    <div className="mb-1">
      {!collapsed || forMobile ? (
        <p className="px-3 mb-1 text-[10px] font-semibold tracking-widest text-zinc-600 uppercase select-none">
          ADMIN
        </p>
      ) : (
        <div className="mx-auto mb-2 h-px bg-zinc-800 w-6" />
      )}
      <div className="space-y-0.5">
        {adminNavItems.map((item) => renderNavItem(item, forMobile))}
      </div>
    </div>
  )

  const footerOnline = localSelected ? !!stats : selectedNodeOnline
  const footerHostname = !localSelected
    ? selectedNode?.hostname || selectedNode?.name || selectedServer
    : stats?.hostname ?? 'Local server'
  const footerUptime = !localSelected
    ? selectedNode?.inventory?.uptime_seconds
    : stats?.uptime

  const serverFooter = (compact: boolean) =>
    compact ? (
      <div className="flex justify-center py-3 border-t border-zinc-800 bg-zinc-950/30">
        <span
          className={cn('w-1.5 h-1.5 rounded-full', footerOnline ? 'bg-green-500' : 'bg-red-500')}
          title={`${footerHostname} · ${footerOnline ? 'Online' : 'Offline'}`}
        />
      </div>
    ) : (
      <div className="border-t border-zinc-800 px-4 py-3 bg-zinc-950/30">
        <div className="flex items-center gap-1.5">
          <span className={cn('w-1.5 h-1.5 rounded-full flex-shrink-0', footerOnline ? 'bg-green-500' : 'bg-red-500')} />
          <span className="text-zinc-300 text-xs font-medium truncate">
            {footerHostname}
          </span>
        </div>
        <div className="flex items-center gap-1.5 pl-3 mt-0.5">
          <Wifi className="w-3 h-3 text-zinc-600" />
          <span className="text-zinc-600 text-xs">
            {footerOnline
              ? footerUptime !== undefined ? `Up ${formatUptime(footerUptime)}` : 'Online'
              : !localSelected ? 'Offline · heartbeat stale' : 'Connecting...'}
          </span>
        </div>
      </div>
    )

  const sidebarInner = (forMobile = false) => (
    <>
      {/* Logo */}
      <div
        className={cn(
          'flex items-center gap-3 px-4 py-4 border-b border-zinc-800 flex-shrink-0',
          collapsed && !forMobile && 'justify-center px-2',
        )}
      >
        <div className="w-7 h-7 bg-blue-600 rounded-md flex items-center justify-center flex-shrink-0">
          <Server className="w-3.5 h-3.5 text-white" />
        </div>
        {(!collapsed || forMobile) && (
          <div className="min-w-0">
            <span className="text-white font-semibold text-sm">Heyserver</span>
            <span className="block text-zinc-500 text-xs">Panel v1.0</span>
          </div>
        )}
        {forMobile && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="ml-auto text-zinc-400 hover:text-white h-7 w-7"
            onClick={closeMobileNavigation}
            aria-label="Close navigation"
          >
            <X className="w-4 h-4" />
          </Button>
        )}
      </div>

      {/* Nav sections */}
      <nav className="flex-1 px-2 py-3 overflow-y-auto overflow-x-hidden">
        {renderSections(forMobile)}
      </nav>

      {/* Admin + logout */}
      <div className="px-2 pb-1 border-t border-zinc-800 pt-2 flex-shrink-0">
        {renderAdmin(forMobile)}
        <button
          onClick={handleLogout}
          className={cn(
            'flex items-center gap-3 rounded-lg text-sm text-zinc-400 hover:text-red-400 hover:bg-red-400/10 transition-colors w-full mt-0.5 group relative',
            collapsed && !forMobile
              ? 'justify-center py-2.5 px-0 w-10 mx-auto'
              : 'px-3 py-2',
          )}
        >
          <LogOut className="w-4 h-4 flex-shrink-0" />
          {(!collapsed || forMobile) && <span>Logout</span>}
          {collapsed && !forMobile && (
            <span className="absolute left-full ml-2.5 px-2 py-1 bg-zinc-800 text-white text-xs rounded whitespace-nowrap opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 border border-zinc-700 shadow-xl">
              Logout
            </span>
          )}
        </button>
      </div>

      {/* Server info */}
      {serverFooter(collapsed && !forMobile)}

      {/* Collapse toggle — desktop only */}
      {!forMobile && (
        <button
          onClick={toggleCollapsed}
          className="flex items-center justify-center h-7 border-t border-zinc-800 text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800/50 transition-colors flex-shrink-0"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? (
            <ChevronRight className="w-3.5 h-3.5" />
          ) : (
            <ChevronLeft className="w-3.5 h-3.5" />
          )}
        </button>
      )}
    </>
  )

  return (
    <div className="min-h-screen bg-zinc-950 flex">
      {/* Mobile backdrop */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-20 bg-black/60 lg:hidden backdrop-blur-sm"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Desktop sidebar */}
      <aside
        className="hidden lg:flex flex-col flex-shrink-0 bg-zinc-900 border-r border-zinc-800 transition-all duration-200 ease-in-out"
        style={{ width: collapsed ? '3.5rem' : '14.5rem' }}
      >
        {sidebarInner(false)}
      </aside>

      {/* Mobile sidebar (sheet overlay) */}
      <aside
        id="mobile-navigation"
        aria-label="Mobile navigation"
        aria-hidden={!sidebarOpen}
        inert={!sidebarOpen}
        className={cn(
          'fixed inset-y-0 left-0 z-30 w-60 bg-zinc-900 border-r border-zinc-800 flex flex-col transition-transform duration-200 ease-in-out lg:hidden',
          sidebarOpen ? 'translate-x-0' : '-translate-x-full',
        )}
        onTouchStart={handleTouchStart}
        onTouchEnd={handleTouchEnd}
      >
        {sidebarInner(true)}
      </aside>

      {/* Main content */}
      <div className="flex-1 flex flex-col min-w-0">
        <header className="flex flex-shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-zinc-800 bg-zinc-900 px-4 py-3.5 lg:px-6">
          <Button
            ref={mobileMenuTriggerRef}
            type="button"
            variant="ghost"
            size="icon"
            className="lg:hidden text-zinc-400 hover:text-white h-8 w-8"
            onClick={() => setSidebarOpen(true)}
            aria-label="Open navigation"
            aria-expanded={sidebarOpen}
            aria-controls="mobile-navigation"
          >
            <Menu className="w-5 h-5" />
          </Button>

          <div className="flex-1 min-w-0">
            <h1 className="text-white font-medium text-sm leading-tight">{pageTitle}</h1>
            <div className="hidden sm:block mt-0.5">
              <Breadcrumb selectedServer={selectedServer} />
            </div>
          </div>

          <div className="order-3 flex w-full items-center justify-end gap-2 sm:order-none sm:w-auto">
            <label className="relative flex h-9 min-w-0 flex-1 items-center gap-2 rounded-lg border border-zinc-700 bg-zinc-950/60 px-2.5 text-xs text-zinc-300 shadow-sm transition-colors focus-within:border-blue-500/70 hover:border-zinc-600 sm:flex-none">
              <span
                className={cn(
                  'size-2 shrink-0 rounded-full',
                  healthTone[serverHealth.level].dot,
                )}
              />
              <Server className="size-3.5 shrink-0 text-zinc-500" />
              <select
                value={selectedServer}
                onChange={(event) => switchServer(event.target.value as ManagedServerID)}
                aria-label="Managed server"
                className="max-w-28 appearance-none bg-transparent pr-4 font-medium text-zinc-200 outline-none sm:max-w-none"
              >
                <option value="local">Heyserver</option>
                {!localSelected && !selectedNode && (
                  <option value={selectedServer}>{selectedServer} · Unavailable</option>
                )}
                {managedNodes.map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.name || node.hostname || node.id}{managedNodeOnline(node, now) ? ' · Online' : ' · Offline'}
                  </option>
                ))}
              </select>
              <ChevronRight className="pointer-events-none absolute right-2 size-3 rotate-90 text-zinc-600" />
            </label>
			<ServerQuickControls key={selectedServer} selectedServer={selectedServer} selectedOnline={selectedOnline} serverLabel={selectedServerLabel} localMemory={stats?.memory} canManage={currentUser?.role === 'admin'} hostActionAvailable={hostActionAvailable} terminalAvailable={terminalAvailable} />
            <button
              onClick={toggleTheme}
              className="relative flex h-8 w-8 items-center justify-center rounded-lg text-zinc-400 transition-colors hover:bg-zinc-800 hover:text-white"
              aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            >
              {theme === 'dark' ? (
                <Sun className="w-4 h-4" />
              ) : (
                <Moon className="w-4 h-4" />
              )}
            </button>
            <NotificationBell selectedServer={selectedServer} serverLabel={selectedServerLabel} />
            <div className="h-4 w-px bg-zinc-800" />
            <OperationalStatus health={serverHealth} selectedServer={selectedServer} serverLabel={selectedServerLabel} memory={serverControlMemory} hostActionAvailable={hostActionAvailable} serviceActionAvailable={serviceActionAvailable} />
          </div>
        </header>

        <main className="flex-1 p-4 lg:p-6 overflow-auto">{children}</main>
      </div>

      <CommandPalette
        isOpen={cmdOpen}
        onClose={cmdClose}
        selectedServer={selectedServer}
        selectedOnline={selectedOnline}
        serverLabel={selectedServerLabel}
        managedNodes={managedNodes.map((node) => ({
          id: node.id,
          name: node.name,
          hostname: node.hostname,
          online: managedNodeOnline(node, now),
        }))}
        onSwitchServer={switchServer}
      />
    </div>
  )
}

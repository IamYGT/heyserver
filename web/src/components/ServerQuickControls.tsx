import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import {
  Activity, Cpu, HardDrive, Loader2, MemoryStick, Power,
  RefreshCw, RotateCcw, Terminal, Trash2, Wrench,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import {
  hostActionConfirmation, hostActionEndpoint, hostActionLabel, quickControlTargets, rebootCancelEndpoint,
  rebootControlState, rebootStatusEndpoint, rebootStatusQueryKey, swapResetAvailability, type QuickHostAction, type RebootStatus,
  type SwapBlockReason,
} from '@/lib/hostControls'
import { hostActionStatusKey, useHostActionStatus } from '@/hooks/useHostActionStatus'
import { isLocalServer, managedNodePath, type ManagedServerID } from '@/lib/serverNavigation'
import type { SystemStats } from '@/lib/types'
import { cn } from '@/lib/utils'
import type { AgentTask } from '@/lib/agentTasks'
import { buildOperationHistory, type OperationAuditEntry, type OperationStatus } from '@/lib/operationHistory'
import type { DiskAnalysisStatus } from '@/hooks/useDisk'

interface RemoteMemoryState {
  memory_total_bytes: number
  memory_available_bytes: number
  swap_total_bytes: number
  swap_used_bytes: number
  swap_free_bytes: number
}

interface ServerQuickControlsProps {
  selectedServer: ManagedServerID
  selectedOnline: boolean
  serverLabel: string
  localMemory?: SystemStats['memory']
  canManage: boolean
  hostActionAvailable: boolean
  terminalAvailable: boolean
}

const actionDefinitions: Array<{
  id: QuickHostAction
  label: string
  description: string
  icon: typeof MemoryStick
  destructive?: boolean
}> = [
  { id: 'memory-optimize', label: 'Optimize RAM', description: 'Release reclaimable filesystem caches', icon: MemoryStick },
  { id: 'swap-reset', label: 'Reset swap', description: 'Cycle configured swap with a RAM safety check', icon: RotateCcw },
  { id: 'temp-clean', label: 'Clean temp', description: 'Apply the host tmpfiles expiry policy', icon: Trash2 },
  { id: 'reboot', label: 'Reboot', description: 'Schedule a reboot in 10 seconds', icon: Power, destructive: true },
]

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** power).toFixed(power >= 3 ? 1 : 0)} ${units[power]}`
}

function swapReasonText(reason: SwapBlockReason | undefined, required: number, available: number): string {
  if (reason === 'loading') return 'Loading swap state…'
  if (reason === 'not-configured') return 'No active swap is configured'
  if (reason === 'already-empty') return 'Swap is already empty'
  if (reason === 'insufficient-memory') return `Needs ${formatBytes(required)} available; ${formatBytes(available)} is available`
  return ''
}

function formatRelative(value?: string): string {
  if (!value) return 'unknown time'
  const timestamp = new Date(value).getTime()
  if (Number.isNaN(timestamp)) return value
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

const operationTone: Record<OperationStatus, string> = {
  queued: 'bg-amber-500/10 text-amber-400',
  running: 'bg-blue-500/10 text-blue-400',
  completed: 'bg-emerald-500/10 text-emerald-400',
  failed: 'bg-red-500/10 text-red-400',
}

export function ServerQuickControls({ selectedServer, selectedOnline, serverLabel, localMemory, canManage, hostActionAvailable, terminalAvailable }: ServerQuickControlsProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const localSelected = isLocalServer(selectedServer)
  const targets = quickControlTargets(selectedServer)
  const actionStatus = useHostActionStatus(selectedServer, selectedOnline && canManage && hostActionAvailable)
  const actionStatusUnavailable = canManage && selectedOnline && hostActionAvailable && (actionStatus.isLoading || actionStatus.isError)

  const remoteMemory = useQuery<RemoteMemoryState>({
    queryKey: ['managed-node-memory', selectedServer],
    queryFn: () => api.get(managedNodePath(selectedServer, '/memory')),
    enabled: !localSelected && selectedOnline,
    refetchInterval: !localSelected && selectedOnline ? 10_000 : false,
  })
  const rebootStatus = useQuery<RebootStatus>({
    queryKey: rebootStatusQueryKey(selectedServer),
    queryFn: () => api.get(rebootStatusEndpoint(selectedServer)),
    enabled: selectedOnline && canManage && hostActionAvailable,
    refetchInterval: (query) => query.state.data?.pending ? 1_000 : 10_000,
  })
  const rebootControl = rebootControlState(rebootStatus.data, rebootStatus)
  const auditHistory = useQuery<{ data: OperationAuditEntry[] }>({
    queryKey: ['quick-controls', 'audit-history', selectedServer],
    queryFn: () => api.get(`/audit?${new URLSearchParams({ server: selectedServer, limit: '30' }).toString()}`),
    enabled: open,
    refetchInterval: open ? 15_000 : false,
  })
  const remoteTasks = useQuery<AgentTask[]>({
    queryKey: ['managed-node-tasks', selectedServer],
    queryFn: () => api.get(`${managedNodePath(selectedServer, '/tasks')}?limit=12`),
    enabled: open && !localSelected && selectedOnline,
    refetchInterval: open && !localSelected && selectedOnline ? 10_000 : false,
  })
  const diskAnalysis = useQuery<DiskAnalysisStatus>({
    queryKey: ['disk', 'analysis', 'status'],
    queryFn: () => api.get('/disk/analysis/status'),
    enabled: open && localSelected,
    refetchInterval: (query) => query.state.data?.status === 'queued' || query.state.data?.status === 'running' ? 3_000 : false,
  })

  const memory = useMemo(() => {
    if (localSelected) {
      if (!localMemory) return undefined
      return { total: localMemory.swapTotal, used: localMemory.swapUsed, available: localMemory.available }
    }
    if (!remoteMemory.data) return undefined
    return {
      total: remoteMemory.data.swap_total_bytes,
      used: remoteMemory.data.swap_used_bytes,
      available: remoteMemory.data.memory_available_bytes,
    }
  }, [localMemory, localSelected, remoteMemory.data])
  const swap = swapResetAvailability(memory)
  const swapReason = swapReasonText(swap.reason, swap.requiredAvailable, memory?.available ?? 0)
  const operations = useMemo(
    () => buildOperationHistory(selectedServer, auditHistory.data?.data, remoteTasks.data, diskAnalysis.data).slice(0, 5),
    [auditHistory.data?.data, diskAnalysis.data, remoteTasks.data, selectedServer],
  )

  const cancelReboot = useMutation<{ message: string }, Error, ManagedServerID>({
    mutationFn: (server) => api.post(rebootCancelEndpoint(server)),
    onSuccess: async (result, server) => {
      toast.success(result.message)
      await queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey(server) })
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', server] })
		await queryClient.invalidateQueries({ queryKey: hostActionStatusKey(server) })
    },
    onError: async (error) => {
      toast.error(error.message || 'Could not cancel the scheduled reboot')
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', selectedServer] })
		await queryClient.invalidateQueries({ queryKey: hostActionStatusKey(selectedServer) })
    },
  })

  const actionMutation = useMutation<{ message: string }, Error, { action: QuickHostAction; server: ManagedServerID }>({
    mutationFn: ({ action, server }) => api.post(hostActionEndpoint(server, action)),
    onSuccess: async (result, variables) => {
      toast.success(result.message, variables.action === 'reboot' ? {
        duration: 10_000,
        action: { label: 'Cancel reboot', onClick: () => cancelReboot.mutate(variables.server) },
      } : undefined)
      if (isLocalServer(variables.server)) {
        await queryClient.invalidateQueries({ queryKey: ['stats', 'system'] })
      } else {
        await queryClient.invalidateQueries({ queryKey: ['managed-node-memory', variables.server] })
        await queryClient.invalidateQueries({ queryKey: ['managed-nodes'] })
      }
      if (variables.action === 'reboot') {
        await queryClient.invalidateQueries({ queryKey: rebootStatusQueryKey(variables.server) })
      }
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', variables.server] })
		await queryClient.invalidateQueries({ queryKey: hostActionStatusKey(variables.server) })
    },
    onError: async (error) => {
      toast.error(error.message || 'Host action failed')
      await queryClient.invalidateQueries({ queryKey: ['quick-controls', 'audit-history', selectedServer] })
		await queryClient.invalidateQueries({ queryKey: hostActionStatusKey(selectedServer) })
    },
  })

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

  const openTarget = (target: string) => {
    setOpen(false)
    navigate(target)
  }

  const runAction = (action: QuickHostAction) => {
    if (action === 'reboot' && rebootControl.retryable) {
      void rebootStatus.refetch()
      return
    }
    if (action === 'reboot' && rebootControl.pending) {
      cancelReboot.mutate(selectedServer)
      return
    }
    const confirmation = hostActionConfirmation(action, serverLabel, memory, swap.requiredAvailable)
    if (window.confirm(confirmation)) actionMutation.mutate({ action, server: selectedServer })
  }

  return (
    <div ref={ref} className="relative">
      <Button
        ref={triggerRef}
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => setOpen(current => !current)}
        aria-label={`Quick controls for ${serverLabel}`}
        aria-expanded={open}
        aria-controls="server-quick-controls-panel"
        className="h-8 gap-1.5 px-2 text-zinc-400 hover:text-white"
      >
        <Wrench className="size-4" />
        <span className="hidden xl:inline">Controls</span>
		{actionStatus.data?.running
			? <span className="size-1.5 animate-pulse rounded-full bg-blue-400" />
			: rebootStatus.data?.pending && <span className="size-1.5 rounded-full bg-amber-400" />}
      </Button>

      {open && (
        <div id="server-quick-controls-panel" role="dialog" aria-label={`${serverLabel} quick controls`} className="absolute right-0 top-10 z-50 max-h-[calc(100vh-5rem)] w-[min(25rem,calc(100vw-2rem))] overflow-y-auto rounded-xl border border-zinc-700 bg-zinc-900 shadow-2xl">
          <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
            <div>
              <p className="text-sm font-semibold text-zinc-100">{serverLabel} quick controls</p>
              <p className="mt-0.5 text-[11px] text-zinc-500">Navigate or act without leaving the current workflow</p>
            </div>
            <span className={cn('rounded-full px-2 py-1 text-[9px] font-semibold uppercase', selectedOnline ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400')}>
              {selectedOnline ? 'Online' : 'Offline'}
            </span>
          </div>

			{actionStatus.data?.running && (
				<div className="flex items-center gap-2 border-b border-blue-500/20 bg-blue-500/[0.08] px-4 py-2 text-[10px] text-blue-300">
					<Loader2 className="size-3 animate-spin" />
					<span><strong>{hostActionLabel(actionStatus.data.action)}</strong> is running · started {formatRelative(actionStatus.data.started_at)}</span>
				</div>
			)}
			{actionStatusUnavailable && (
				<div className="flex items-center justify-between gap-3 border-b border-amber-500/20 bg-amber-500/[0.07] px-4 py-2 text-[10px] text-amber-300">
					<span>{actionStatus.isError ? 'Could not verify whether host maintenance is already running.' : 'Checking active host maintenance…'}</span>
					{actionStatus.isError && <Button type="button" variant="ghost" size="xs" onClick={() => actionStatus.refetch()}>Retry</Button>}
				</div>
			)}

          <div className="grid grid-cols-4 gap-1 border-b border-zinc-800 p-2">
            {[
              { label: 'Terminal', target: targets.terminal, icon: Terminal, enabled: terminalAvailable },
              { label: 'Services', target: targets.services, icon: Activity, enabled: true },
              { label: 'Processes', target: targets.processes, icon: Cpu, enabled: true },
              { label: 'Disk', target: targets.disk, icon: HardDrive, enabled: true },
            ].map(({ label, target, icon: Icon, enabled }) => (
              <button key={label} type="button" disabled={!enabled} title={!enabled ? 'Writable terminal is not enabled on this agent' : undefined} onClick={() => openTarget(target)} className="flex flex-col items-center gap-1.5 rounded-lg px-2 py-2 text-[10px] text-zinc-400 transition hover:bg-zinc-800 hover:text-white disabled:cursor-not-allowed disabled:opacity-35 disabled:hover:bg-transparent disabled:hover:text-zinc-400">
                <Icon className="size-4" />
                {label}
              </button>
            ))}
          </div>

			{canManage && !hostActionAvailable && (
				<div className="border-b border-amber-500/20 bg-amber-500/[0.07] px-4 py-3 text-[10px] text-amber-300">
					Host actions are not enabled on this agent. Configure <code>HSERVER_AGENT_ALLOWED_HOST_ACTIONS</code> and restart the agent.
				</div>
			)}

			{canManage ? <div className="grid gap-2 p-3 sm:grid-cols-2">
            {actionDefinitions.map(({ id, label, description, icon: Icon, destructive }) => {
              const rebootPending = id === 'reboot' && rebootControl.pending
              const rebootUnavailable = id === 'reboot' && rebootControl.blocked
              const rebootRetryable = id === 'reboot' && rebootControl.retryable
              const swapBlocked = id === 'swap-reset' && !swap.eligible
						const activeMaintenance = actionStatus.data?.running === true
							const disabled = !selectedOnline || !hostActionAvailable || actionStatusUnavailable || activeMaintenance || actionMutation.isPending || cancelReboot.isPending || rebootUnavailable || swapBlocked
              const pending = (actionMutation.isPending && actionMutation.variables?.action === id)
                || (rebootPending && cancelReboot.isPending)
						|| rebootUnavailable
						|| (activeMaintenance && (actionStatus.data?.action === id || (id === 'reboot' && actionStatus.data?.action === 'reboot-cancel')))
							  const detail = !hostActionAvailable
								? 'Host actions are not enabled on this agent'
								: actionStatusUnavailable
									? (actionStatus.isError ? 'Could not verify active host maintenance' : 'Checking active host maintenance…')
						: activeMaintenance
							? `${hostActionLabel(actionStatus.data?.action)} is already running`
						: id === 'reboot'
							? rebootControl.description
                  : swapBlocked
                    ? swapReason
                    : id === 'swap-reset' && memory
                      ? `${formatBytes(memory.used)} used · ${formatBytes(memory.available)} RAM available · ${formatBytes(swap.requiredAvailable)} required`
                    : id === 'memory-optimize' && memory
                      ? `${formatBytes(memory.available)} RAM available · releases reclaimable caches only`
                    : description
              return (
                <button
                  key={id}
                  type="button"
                  disabled={disabled}
                  onClick={() => runAction(id)}
                  title={disabled ? detail : undefined}
                  className={cn(
                    'flex items-start gap-3 rounded-xl border p-3 text-left transition disabled:cursor-not-allowed disabled:opacity-40',
                    destructive || rebootPending
                      ? 'border-red-900/50 bg-red-950/20 hover:border-red-700/60'
                      : 'border-zinc-800 bg-zinc-950/40 hover:border-zinc-700',
                  )}
                >
                  <span className={cn('grid size-8 shrink-0 place-items-center rounded-lg', destructive || rebootPending ? 'bg-red-500/10 text-red-400' : 'bg-zinc-800 text-zinc-300')}>
                    {pending ? <Loader2 className="size-4 animate-spin" /> : rebootRetryable ? <RefreshCw className="size-4" /> : rebootPending ? <RotateCcw className="size-4" /> : <Icon className="size-4" />}
                  </span>
                  <span className="min-w-0">
                    <span className="block text-xs font-semibold text-zinc-200">{rebootRetryable ? 'Retry reboot status' : rebootPending ? 'Cancel reboot' : label}</span>
                    <span className="mt-0.5 block text-[10px] leading-relaxed text-zinc-500">{detail}</span>
                  </span>
                </button>
              )
            })}
			</div> : <div className="border-b border-zinc-800 px-4 py-3 text-[10px] text-zinc-500">Admin role is required to run host maintenance. Navigation remains available.</div>}

          <div className="border-t border-zinc-800">
            <div className="flex items-center justify-between px-4 py-2.5">
              <div>
                <p className="text-xs font-semibold text-zinc-300">Recent operations</p>
                <p className="mt-0.5 text-[9px] text-zinc-600">Persistent receipts for {serverLabel}</p>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                disabled={auditHistory.isFetching || remoteTasks.isFetching || diskAnalysis.isFetching}
                onClick={() => { auditHistory.refetch(); if (localSelected) diskAnalysis.refetch(); else remoteTasks.refetch() }}
                aria-label={`Refresh ${serverLabel} operation history`}
                title="Refresh operation history"
              >
                <RefreshCw className={cn('size-3 text-zinc-500', (auditHistory.isFetching || remoteTasks.isFetching || diskAnalysis.isFetching) && 'animate-spin')} />
              </Button>
            </div>
            {auditHistory.isLoading || (!localSelected && remoteTasks.isLoading) ? (
              <div className="flex items-center justify-center py-5 text-zinc-600"><Loader2 className="size-4 animate-spin" /></div>
            ) : operations.length === 0 ? (
              <p className="px-4 pb-4 text-[10px] text-zinc-600">No persisted operations for this server yet.</p>
            ) : (
              <div className="divide-y divide-zinc-800/70 border-t border-zinc-800/70">
                {operations.map(operation => (
                  <div key={operation.id} className="flex items-start gap-3 px-4 py-2.5">
                    <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', operation.status === 'completed' ? 'bg-emerald-400' : operation.status === 'failed' ? 'bg-red-400' : operation.status === 'running' ? 'bg-blue-400' : 'bg-amber-400')} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[11px] font-medium text-zinc-300" title={operation.label}>{operation.label}</span>
                      <span className="mt-0.5 block truncate text-[9px] text-zinc-600" title={operation.detail}>{operation.detail}</span>
                    </span>
                    <span className="flex shrink-0 flex-col items-end gap-1">
                      <span className={cn('rounded px-1.5 py-0.5 text-[8px] font-semibold uppercase', operationTone[operation.status])}>{operation.status}</span>
                      <span className="text-[8px] text-zinc-700">{formatRelative(operation.timestamp)}</span>
                    </span>
                  </div>
                ))}
              </div>
            )}
            <button type="button" onClick={() => openTarget(`/audit?${new URLSearchParams({ server: selectedServer }).toString()}`)} className="w-full border-t border-zinc-800 px-4 py-2.5 text-left text-[10px] font-medium text-blue-400 transition hover:bg-zinc-800/50 hover:text-blue-300">
              View full audit log →
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

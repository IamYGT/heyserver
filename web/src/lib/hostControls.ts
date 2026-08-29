import { isLocalServer, managedNodePath, type ManagedServerID } from '@/lib/serverNavigation'

export type QuickHostAction = 'memory-optimize' | 'swap-reset' | 'temp-clean' | 'reboot'

export interface HostActionStatus {
  running: boolean
  action?: QuickHostAction | 'reboot-cancel' | 'disk-cleanup'
  started_at?: string
}

export interface RebootStatus {
  pending: boolean
  scheduled_for?: string
  remaining_seconds?: number
}

export interface RebootStatusQueryState {
  isLoading: boolean
  isError: boolean
  isFetching: boolean
}

export interface RebootControlState {
  pending: boolean
  blocked: boolean
  retryable: boolean
  description: string
}

const hostActionLabels: Record<NonNullable<HostActionStatus['action']>, string> = {
  'memory-optimize': 'RAM optimization',
  'swap-reset': 'Swap reset',
  'temp-clean': 'Temporary-file cleanup',
  reboot: 'Reboot scheduling',
  'reboot-cancel': 'Reboot cancellation',
  'disk-cleanup': 'Disk cleanup',
}

export function hostActionLabel(action?: HostActionStatus['action']): string {
  return action ? hostActionLabels[action] : 'Host maintenance'
}

export function rebootStatusDescription(status?: RebootStatus): string {
  if (!status?.pending) return 'Schedule a reboot in 10 seconds'
  if (typeof status.remaining_seconds === 'number' && status.remaining_seconds > 0) {
    return `Reboot in ${status.remaining_seconds}s · click to cancel`
  }
  return 'Reboot timer is active · click to cancel'
}

export function rebootControlState(
  status: RebootStatus | undefined,
  query: RebootStatusQueryState,
): RebootControlState {
  if (status?.pending) {
    return {
      pending: true,
      blocked: false,
      retryable: false,
      description: query.isError
        ? 'Reboot is scheduled · countdown refresh failed · click to cancel'
        : rebootStatusDescription(status),
    }
  }
  if (query.isLoading) {
    return { pending: false, blocked: true, retryable: false, description: 'Loading reboot timer state…' }
  }
  if (query.isError && query.isFetching) {
    return { pending: false, blocked: true, retryable: false, description: 'Retrying reboot timer state…' }
  }
  if (query.isError) {
    return { pending: false, blocked: false, retryable: true, description: 'Could not read reboot timer state · click to retry' }
  }
  return { pending: false, blocked: false, retryable: false, description: rebootStatusDescription(status) }
}

export interface SwapMemorySnapshot {
  total: number
  used: number
  available: number
}

export type SwapBlockReason = 'loading' | 'not-configured' | 'already-empty' | 'insufficient-memory'

export interface SwapResetAvailability {
  eligible: boolean
  reason?: SwapBlockReason
  requiredAvailable: number
}

const SWAP_SAFETY_RESERVE = 512 * 1024 * 1024

export function swapResetAvailability(memory?: SwapMemorySnapshot): SwapResetAvailability {
  if (!memory) return { eligible: false, reason: 'loading', requiredAvailable: 0 }
  if (memory.total <= 0) return { eligible: false, reason: 'not-configured', requiredAvailable: 0 }
  if (memory.used <= 0) return { eligible: false, reason: 'already-empty', requiredAvailable: 0 }

  const requiredAvailable = memory.used + SWAP_SAFETY_RESERVE
  if (memory.available < requiredAvailable) {
    return { eligible: false, reason: 'insufficient-memory', requiredAvailable }
  }
  return { eligible: true, requiredAvailable }
}

function formatControlBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** power).toFixed(power >= 3 ? 1 : 0)} ${units[power]}`
}

export function swapResetConfirmation(
  serverLabel: string,
  memory: SwapMemorySnapshot,
  requiredAvailable: number,
): string {
  return `Reset ${formatControlBytes(memory.used)} of used swap on ${serverLabel} now? ${formatControlBytes(memory.available)} RAM is currently available; the safety check requires at least ${formatControlBytes(requiredAvailable)}, including a 512 MB reserve. Running processes stay active, but memory pressure can rise briefly.`
}

export function memoryOptimizeConfirmation(serverLabel: string, available?: number): string {
  const measurement = typeof available === 'number' && Number.isFinite(available)
    ? `${formatControlBytes(available)} RAM is currently available. `
    : ''
  return `Optimize RAM on ${serverLabel} now? ${measurement}This syncs pending filesystem writes and releases only reclaimable caches; running processes and swap stay unchanged.`
}

export function tempCleanConfirmation(serverLabel: string): string {
  return `Clean expired temporary files on ${serverLabel} now? This applies the host tmpfiles age policy; recent files and active application data are not targeted.`
}

export function hostActionConfirmation(
  action: QuickHostAction,
  serverLabel: string,
  memory?: SwapMemorySnapshot,
  requiredAvailable = 0,
): string {
  switch (action) {
    case 'memory-optimize':
      return memoryOptimizeConfirmation(serverLabel, memory?.available)
    case 'swap-reset':
      return memory
        ? swapResetConfirmation(serverLabel, memory, requiredAvailable)
        : `Reset configured swap on ${serverLabel} now? The action will stop unless available RAM can absorb used swap plus a 512 MB reserve.`
    case 'temp-clean':
      return tempCleanConfirmation(serverLabel)
    case 'reboot':
      return `Reboot ${serverLabel} in 10 seconds? Active terminal sessions and services will disconnect.`
  }
}

/**
 * @apiRoute /system/actions/{action}
 * @apiRoute /nodes/{server}/actions/{action}
 */
export function hostActionEndpoint(server: ManagedServerID, action: QuickHostAction): string {
  return isLocalServer(server) ? `/system/actions/${action}` : managedNodePath(server, `/actions/${action}`)
}

/**
 * @apiRoute /system/actions/reboot-status
 * @apiRoute /nodes/{server}/actions/reboot-status
 */
export function rebootStatusEndpoint(server: ManagedServerID): string {
  return isLocalServer(server) ? '/system/actions/reboot-status' : managedNodePath(server, '/actions/reboot-status')
}

export function rebootStatusQueryKey(server: ManagedServerID) {
  return ['host-controls', 'reboot-status', server] as const
}

/**
 * @apiRoute /system/actions/status
 * @apiRoute /nodes/{server}/actions/status
 */
export function hostActionStatusEndpoint(server: ManagedServerID): string {
  return isLocalServer(server) ? '/system/actions/status' : managedNodePath(server, '/actions/status')
}

/**
 * @apiRoute /system/actions/reboot-cancel
 * @apiRoute /nodes/{server}/actions/reboot-cancel
 */
export function rebootCancelEndpoint(server: ManagedServerID): string {
  return isLocalServer(server) ? '/system/actions/reboot-cancel' : managedNodePath(server, '/actions/reboot-cancel')
}

export function quickControlTargets(server: ManagedServerID) {
  if (!isLocalServer(server)) {
    const node = new URLSearchParams({ node: server }).toString()
    return {
      terminal: `/terminal?${node}`,
      services: `/servers?${node}&tab=services`,
      processes: `/servers?${node}&tab=processes`,
      disk: `/servers?${node}&tab=disk`,
    }
  }
  return {
    terminal: '/terminal',
    services: '/monitoring',
    processes: '/monitoring?focus=processes',
    disk: '/disk?tab=cleanup',
  }
}

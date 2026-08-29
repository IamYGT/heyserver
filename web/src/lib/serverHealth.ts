import type { BackupStorageSummary, GDriveStatus, ServiceStatus, SystemStats } from '@/lib/types'
import { swapResetAvailability } from '@/lib/hostControls'

export type ServerHealthLevel = 'loading' | 'healthy' | 'warning' | 'critical'

export interface ServerHealthIssue {
  id: string
  title: string
  detail: string
  href: string
  level: 'warning' | 'critical'
  service?: string
  action?: 'swap-reset' | 'gdrive-reconnect'
}

export interface ServerHealth {
  level: ServerHealthLevel
  label: string
  issues: ServerHealthIssue[]
}

interface RemoteServiceState {
  name: string
  active: string
  sub?: string
}

const localManageableServices = new Set([
  'nginx', 'php8.4-fpm', 'php8.5-fpm', 'php7.4-fpm',
  'postgresql', 'mariadb', 'redis-server',
])

function isLocalManageableService(name: string): boolean {
  return localManageableServices.has(name) || /^pm2-[A-Za-z0-9_.-]+$/.test(name)
}

const remoteManageableServices = new Set([
  'nginx.service', 'php8.5-fpm.service', 'mariadb.service',
  'postgresql@17-main.service', 'postgresql@18-main.service', 'pm2-root.service',
])

export interface RemoteHealthInput {
  nodeName?: string
  online: boolean
  managementStatus?: 'checking' | 'reachable' | 'unreachable'
  diskTotal?: number
  diskUsed?: number
  diskAvailable?: number
  diskUsePercent?: number
  memoryTotal?: number
  memoryAvailable?: number
  swapTotal?: number
  swapUsed?: number
  services?: RemoteServiceState[]
}

function diskIssue(usedPercent: number, href: string): ServerHealthIssue | undefined {
  if (!Number.isFinite(usedPercent) || usedPercent < 85) return undefined
  const rounded = Math.min(100, Math.round(usedPercent))
  const critical = usedPercent >= 95
  return {
    id: 'disk',
    title: critical ? 'Disk space is critical' : 'Disk space is running low',
    detail: `Root disk is ${rounded}% full`,
    href,
    level: critical ? 'critical' : 'warning',
  }
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** power).toFixed(power >= 3 ? 1 : 0)} ${units[power]}`
}

function swapIssue(
  swapTotal: number | undefined,
  swapUsed: number | undefined,
  memoryAvailable: number | undefined,
  href: string,
): ServerHealthIssue | undefined {
  if (!swapTotal || !swapUsed || memoryAvailable === undefined) return undefined
  const percentage = (swapUsed / swapTotal) * 100
  if (!Number.isFinite(percentage) || percentage < 50 || swapUsed < 1024 ** 3) return undefined

  const availability = swapResetAvailability({
    total: swapTotal,
    used: swapUsed,
    available: memoryAvailable,
  })
  const detail = availability.eligible
    ? `${formatBytes(swapUsed)} of ${formatBytes(swapTotal)} is used; ${formatBytes(memoryAvailable)} RAM is available for a guarded reset`
    : `${formatBytes(swapUsed)} of ${formatBytes(swapTotal)} is used; reset needs ${formatBytes(availability.requiredAvailable)} available RAM`

  return {
    id: 'swap',
    title: availability.eligible ? 'Swap reset is available' : 'Swap usage is high',
    detail,
    href,
    level: 'warning',
    action: availability.eligible ? 'swap-reset' : undefined,
  }
}

function summarize(issues: ServerHealthIssue[], loading: boolean): ServerHealth {
  if (loading) return { level: 'loading', label: 'Checking', issues: [] }
  if (issues.length === 0) return { level: 'healthy', label: 'Healthy', issues: [] }
  const level = issues.some((issue) => issue.level === 'critical') ? 'critical' : 'warning'
  return {
    level,
    label: issues.length === 1 ? '1 issue' : `${issues.length} issues`,
    issues,
  }
}

export function localServerHealth(
  stats: SystemStats | undefined,
  services: ServiceStatus[] | undefined,
  gdrive?: GDriveStatus,
  backupStorage?: BackupStorageSummary,
): ServerHealth {
  if (!stats) return summarize([], true)

  const issues: ServerHealthIssue[] = []
  const root = stats.disk.find((disk) => disk.mount === '/') ?? stats.disk[0]
  if (root) {
    const issue = diskIssue(root.percentage, '/disk?tab=cleanup')
    if (issue) issues.push(issue)
  }

  const localSwapIssue = swapIssue(
    stats.memory.swapTotal,
    stats.memory.swapUsed,
    stats.memory.available,
    '/',
  )
  if (localSwapIssue) issues.push(localSwapIssue)

  if (backupStorage && backupStorage.orphanedCount > 0 && backupStorage.orphanedBytes > 0) {
    issues.push({
      id: 'backup:orphaned',
      title: `${backupStorage.orphanedCount} interrupted backup artifacts`,
      detail: `${formatBytes(backupStorage.orphanedBytes)} can be reclaimed after reviewing the exact files`,
      href: '/backups?cleanup=orphaned',
      level: backupStorage.rootUsePercent >= 95 ? 'critical' : 'warning',
    })
  }

  for (const service of services ?? []) {
    if (service.status !== 'failed' && service.status !== 'degraded') continue
    issues.push({
      id: `service:${service.name}`,
      title: `${service.name} is ${service.status}`,
      detail: service.detail || 'Open Monitoring for service controls and details',
      href: `/monitoring?service=${encodeURIComponent(service.name)}`,
      level: service.status === 'failed' ? 'critical' : 'warning',
      service: isLocalManageableService(service.name) ? service.name : undefined,
    })
  }

  if (gdrive?.reconnectRequired) {
    issues.push({
      id: 'gdrive:reconnect',
      title: 'Google Drive needs reconnection',
      detail: 'Backup uploads are paused until Google access is authorized again',
      href: '/backups?focus=gdrive',
      level: 'warning',
      action: 'gdrive-reconnect',
    })
  } else if (gdrive?.settings.lastError) {
    issues.push({
      id: 'gdrive:error',
      title: 'Google Drive backup has an error',
      detail: gdrive.settings.lastError,
      href: '/backups?focus=gdrive',
      level: 'warning',
    })
  }

  return summarize(issues, false)
}

export function remoteServerHealth(input: RemoteHealthInput): ServerHealth {
  const nodeName = input.nodeName?.trim() || 'Managed server'
  if (!input.online) {
    return summarize([{
      id: 'offline',
      title: `${nodeName} is offline`,
      detail: 'The latest heartbeat is older than 45 seconds',
      href: '/servers',
      level: 'critical',
    }], false)
  }

  if (input.managementStatus === 'checking') return summarize([], true)

  const issues: ServerHealthIssue[] = []
  if (input.managementStatus === 'unreachable') {
    issues.push({
      id: 'management',
      title: `${nodeName} management is unavailable`,
      detail: 'The heartbeat is current, but the panel could not complete an agent management request',
      href: '/servers?tab=overview',
      level: 'critical',
    })
  }
  if (input.diskTotal && input.diskTotal > 0 && input.diskAvailable !== undefined) {
    let usedPercent = ((input.diskTotal - input.diskAvailable) / input.diskTotal) * 100
    if (input.diskUsePercent !== undefined && Number.isFinite(input.diskUsePercent)) {
      usedPercent = input.diskUsePercent
    } else if (input.diskUsed !== undefined && input.diskUsed >= 0) {
      const denominator = input.diskUsed + input.diskAvailable
      if (denominator > 0) usedPercent = (input.diskUsed / denominator) * 100
    }
    const issue = diskIssue(usedPercent, '/servers?tab=disk')
    if (issue) issues.push(issue)
  }

  const remoteSwapIssue = swapIssue(
    input.swapTotal,
    input.swapUsed,
    input.memoryAvailable,
    '/servers?tab=overview',
  )
  if (remoteSwapIssue) issues.push(remoteSwapIssue)

  for (const service of input.services ?? []) {
    if (service.active !== 'failed' && service.sub !== 'failed') continue
    issues.push({
      id: `service:${service.name}`,
      title: `${service.name} has failed`,
      detail: `Open ${nodeName} Services for controls and details`,
      href: `/servers?tab=services&service=${encodeURIComponent(service.name)}`,
      level: 'critical',
      service: remoteManageableServices.has(service.name) ? service.name : undefined,
    })
  }

  return summarize(issues, false)
}

export type AuditServerScope = 'all' | 'local' | (string & {})

export interface ScopedAuditEntry {
  action: string
  resource: string
  details?: string
}

export interface TimedAuditEntry {
  createdAt?: string
  timestamp?: string
}

export interface AuditNotificationEntry extends ScopedAuditEntry, TimedAuditEntry {
  userName?: string
  user?: string
}

export interface AuditNotificationGroup<T> {
  entry: T
  count: number
}

export type AuditEventTone = 'success' | 'critical' | 'warning' | 'info' | 'neutral'

export interface AuditEventPresentation {
  title: string
  detail: string
  tone: AuditEventTone
  target: string
}

export function auditMatchesServer(entry: ScopedAuditEntry, scope: AuditServerScope): boolean {
  if (scope === 'all') return true
  if (entry.resource !== 'system') return false

  const remote = entry.action.toLowerCase().startsWith('remote_')
  if (scope === 'local') return !remote
  return remote && (entry.details ?? '').trimStart().toLowerCase().startsWith(`${scope.toLowerCase()}:`)
}

export function auditEntryTimestamp(entry: TimedAuditEntry): number {
  const value = entry.createdAt ?? entry.timestamp
  if (!value) return 0
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) ? timestamp : 0
}

export function groupAuditNotifications<T extends AuditNotificationEntry>(
  entries: T[],
  windowMs = 60_000,
): AuditNotificationGroup<T>[] {
  const groups: Array<AuditNotificationGroup<T> & { oldestTimestamp: number }> = []

  for (const entry of entries) {
    const timestamp = auditEntryTimestamp(entry)
    const actor = entry.userName ?? entry.user ?? ''
    const previous = groups.at(-1)
    const previousActor = previous?.entry.userName ?? previous?.entry.user ?? ''
    const belongsToPrevious = previous
      && timestamp > 0
      && previous.oldestTimestamp > 0
      && previous.entry.action === entry.action
      && previous.entry.resource === entry.resource
      && previousActor === actor
      && previous.oldestTimestamp - timestamp <= windowMs

    if (belongsToPrevious) {
      previous.count += 1
      previous.oldestTimestamp = timestamp
      continue
    }

    groups.push({ entry, count: 1, oldestTimestamp: timestamp })
  }

  return groups.map(({ entry, count }) => ({ entry, count }))
}

function humanizeAuditAction(action: string): string {
  const words = action
    .replace(/^remote_/, '')
    .replace(/[_-]+/g, ' ')
    .trim()
  if (!words) return 'System event'
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function auditEventTarget(action: string): string {
  const value = action.toLowerCase()
  if (value.includes('disk') || value.includes('temp')) return '/disk'
  if (value.includes('backup') || value.includes('snapshot')) return '/backups'
  if (value.includes('deploy')) return '/deploy'
  if (value.includes('cron')) return '/cron'
  if (value.includes('docker') || value.includes('container')) return '/docker'
  if (value.includes('nginx')) return '/nginx'
  if (value.includes('php')) return '/php'
  if (value.includes('database') || value.includes('postgres') || value.includes('mariadb') || value.includes('mysql')) return '/databases'
  if (value.includes('ssl') || value.includes('certificate') || value.includes('certbot')) return '/ssl'
  if (value.includes('domain')) return '/domains'
  if (value.includes('firewall') || value.includes('ufw')) return '/firewall'
  if (value.includes('cloudflare')) return '/cloudflare'
  if (value.includes('dns')) return '/dns'
  if (value.includes('mail')) return '/mail'
  if (value.includes('file')) return '/files'
  if (value.includes('terminal') || value.includes('shell')) return '/terminal'
  if (value.includes('security') || value.includes('fail2ban')) return '/security'
  if (value.includes('notification') || value.includes('alert')) return '/notifications'
  if (value.includes('user')) return '/users'
  if (value.includes('setting')) return '/settings'
  if (
    value.includes('memory')
    || value.includes('swap')
    || value.includes('reboot')
    || value.includes('system_action')
    || value.includes('service')
    || value.includes('process')
  ) return '/monitoring'
  return '/audit'
}

function auditEventTone(action: string, detail: string): AuditEventTone {
  const value = `${action} ${detail}`.toLowerCase()
  if (/\b(fail(?:ed|ure)?|error|panic|fatal|rejected)\b/.test(value)) return 'critical'
  if (/\b(cancelled|canceled|warning|degraded)\b/.test(value)) return 'warning'
  if (/\b(succeeded|completed|reclaimed|restored|uploaded|created|removed)\b/.test(value)) return 'success'
  if (/\b(queued|running|scheduled|started|preflight|pending)\b/.test(value)) return 'info'
  return 'neutral'
}

export function auditEventPresentation(entry: ScopedAuditEntry): AuditEventPresentation {
  let detail = (entry.details ?? '').trim()
  if (entry.action.toLowerCase().startsWith('remote_')) {
    detail = detail.replace(/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}:\s*/, '')
  }

  return {
    title: humanizeAuditAction(entry.action),
    detail: detail || entry.resource || 'No additional details',
    tone: auditEventTone(entry.action, detail),
    target: auditEventTarget(entry.action),
  }
}

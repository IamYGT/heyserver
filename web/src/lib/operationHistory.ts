import type { AgentTask } from '@/lib/agentTasks'
import type { ManagedServerID } from '@/lib/serverNavigation'

export interface OperationAuditEntry {
  id: string | number
  action: string
  resource: string
  details?: string
  createdAt?: string
  timestamp?: string
}

export type OperationStatus = 'queued' | 'running' | 'completed' | 'failed'

export interface OperationReceipt {
  id: string
  label: string
  detail: string
  status: OperationStatus
  timestamp?: string
  source: 'audit' | 'agent'
}

export interface DiskAnalysisOperationState {
  id?: string
  status: 'idle' | 'queued' | 'running' | 'completed' | 'failed'
  message: string
  created_at?: string
  started_at?: string
  finished_at?: string
}

function humanize(value: string): string {
  return value
    .replace(/[._-]+/g, ' ')
    .replace(/\b\w/g, character => character.toUpperCase())
}

function auditStatus(entry: OperationAuditEntry): OperationStatus {
  const text = `${entry.action} ${entry.details ?? ''}`.toLowerCase()
  if (text.includes('failed') || text.includes('error')) return 'failed'
  if (text.includes('running')) return 'running'
  if (text.includes('queued') || text.includes('scheduled')) return 'queued'
  return 'completed'
}

function taskReceipt(task: AgentTask): OperationReceipt {
  const service = task.payload?.service ?? task.result?.service
  const action = task.payload?.action
  const label = service
    ? `${service}${action ? ` · ${action}` : ''}`
    : humanize(task.kind)
  const detail = task.status === 'failed'
    ? task.error || 'Agent reported a failure'
    : [task.result?.active, task.result?.sub].filter(Boolean).join('/')
      || (task.status === 'queued' ? 'Waiting for agent pickup' : task.status === 'running' ? 'Agent is executing this task' : 'Completed')
  return {
    id: `task:${task.id}`,
    label,
    detail,
    status: task.status,
    timestamp: task.completed_at ?? task.started_at ?? task.created_at,
    source: 'agent',
  }
}

function auditReceipt(entry: OperationAuditEntry): OperationReceipt {
  return {
    id: `audit:${entry.id}`,
    label: humanize(entry.action),
    detail: formatAuditDetail(entry),
    status: auditStatus(entry),
    timestamp: entry.createdAt ?? entry.timestamp,
    source: 'audit',
  }
}

const diskCleanupBatchWindowMs = 60_000

function formatReclaimedBytes(bytes: number): string {
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** power
  return `${value.toFixed(power === 0 || value >= 10 ? 0 : 1)} ${units[power]}`
}

function formatAuditDetail(entry: OperationAuditEntry): string {
  const detail = entry.details || entry.resource
  const cleanup = detail.match(/^([^:]+):\s*reclaimed\s+(\d+)\s+bytes$/i)
  if (entry.action === 'disk_cleanup' && cleanup) {
    return `${humanize(cleanup[1])} · ${formatReclaimedBytes(Number(cleanup[2]))} reclaimed`
  }
  const cleanupFailure = detail.match(/^([^:]+):\s*failed$/i)
  if (entry.action === 'disk_cleanup' && cleanupFailure) {
    return `${humanize(cleanupFailure[1])} · failed`
  }
  const partial = detail.match(/^removed\s+(\d+)\s+artifacts;\s*reclaimed\s+(\d+)\s+bytes$/i)
  if (entry.action === 'backup_partial_cleanup' && partial) {
    return `${partial[1]} partial artifacts removed · ${formatReclaimedBytes(Number(partial[2]))} reclaimed`
  }
  return detail.replace(/reclaimed\s+(\d+)\s+bytes/gi, (_match, bytes: string) => `${formatReclaimedBytes(Number(bytes))} reclaimed`)
}

function diskCleanupReceipt(entries: OperationAuditEntry[]): OperationReceipt {
  if (entries.length === 1) return auditReceipt(entries[0])

  const newest = entries.reduce((current, entry) =>
    timestampValue(entry.createdAt ?? entry.timestamp) > timestampValue(current.createdAt ?? current.timestamp) ? entry : current,
  )
  const reclaimed = entries.reduce((total, entry) => {
    const match = entry.details?.match(/reclaimed\s+(\d+)\s+bytes/i)
    return total + (match ? Number(match[1]) : 0)
  }, 0)
  const failures = entries.filter(entry => auditStatus(entry) === 'failed').length
  const running = entries.some(entry => auditStatus(entry) === 'running')
  const queued = entries.some(entry => auditStatus(entry) === 'queued')
  const status: OperationStatus = failures > 0 ? 'failed' : running ? 'running' : queued ? 'queued' : 'completed'
  const detail = [
    `${entries.length} cleanup steps`,
    reclaimed > 0 ? `${formatReclaimedBytes(reclaimed)} reclaimed` : undefined,
    failures > 0 ? `${failures} failed` : undefined,
  ].filter(Boolean).join(' · ')

  return {
    id: `audit-batch:${newest.id}`,
    label: 'Disk Cleanup',
    detail,
    status,
    timestamp: newest.createdAt ?? newest.timestamp,
    source: 'audit',
  }
}

function analysisReceipt(entry: OperationAuditEntry, state: DiskAnalysisOperationState): OperationReceipt {
  const status: OperationStatus = state.status === 'idle' ? 'completed' : state.status
  return {
    id: `audit:${entry.id}`,
    label: 'Disk Analysis',
    detail: state.message,
    status,
    timestamp: state.finished_at ?? state.started_at ?? state.created_at ?? entry.createdAt ?? entry.timestamp,
    source: 'audit',
  }
}

function auditReceipts(entries: OperationAuditEntry[], analysis?: DiskAnalysisOperationState): OperationReceipt[] {
  const currentAnalysisEntry = analysis?.id
    ? entries.find(entry => entry.action === 'disk_analysis' && (entry.details ?? '').startsWith(`${analysis.id}:`))
    : undefined
  const currentAnalysisCreatedAt = timestampValue(analysis?.created_at)
  const regular = entries
    .filter(entry => entry.action !== 'disk_cleanup')
    .flatMap(entry => {
      if (entry.action !== 'disk_analysis' || !currentAnalysisEntry || !analysis) return [auditReceipt(entry)]
      if (entry.id === currentAnalysisEntry.id) return [analysisReceipt(entry, analysis)]
      const entryTimestamp = timestampValue(entry.createdAt ?? entry.timestamp)
      return entryTimestamp > 0 && entryTimestamp < currentAnalysisCreatedAt ? [] : [auditReceipt(entry)]
    })
  const cleanupEntries = entries
    .filter(entry => entry.action === 'disk_cleanup')
    .sort((left, right) => timestampValue(right.createdAt ?? right.timestamp) - timestampValue(left.createdAt ?? left.timestamp))

  const cleanupBatches: OperationAuditEntry[][] = []
  for (const entry of cleanupEntries) {
    const timestamp = timestampValue(entry.createdAt ?? entry.timestamp)
    const batch = cleanupBatches.at(-1)
    const newestTimestamp = batch ? timestampValue(batch[0].createdAt ?? batch[0].timestamp) : 0
    if (batch && timestamp > 0 && newestTimestamp - timestamp <= diskCleanupBatchWindowMs) batch.push(entry)
    else cleanupBatches.push([entry])
  }

  return [...regular, ...cleanupBatches.map(diskCleanupReceipt)]
}

function timestampValue(value?: string): number {
  if (!value) return 0
  const timestamp = new Date(value).getTime()
  return Number.isNaN(timestamp) ? 0 : timestamp
}

export function buildOperationHistory(
  server: ManagedServerID,
  audits: OperationAuditEntry[] = [],
  tasks: AgentTask[] = [],
  analysis?: DiskAnalysisOperationState,
): OperationReceipt[] {
  const relevantAudits = audits.filter(entry => {
    if (entry.resource !== 'system') return false
    const remote = entry.action.startsWith('remote_')
    if (server === 'local') return !remote
    return remote && (entry.details ?? '').toLowerCase().startsWith(`${server.toLowerCase()}:`)
  })

  const receipts = auditReceipts(relevantAudits, server === 'local' ? analysis : undefined)
  if (server !== 'local') receipts.push(...tasks.filter(task => task.node_id === server).map(taskReceipt))
  return receipts.sort((left, right) => timestampValue(right.timestamp) - timestampValue(left.timestamp))
}

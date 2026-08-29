import { useDeferredValue, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ScrollText, RefreshCw, Filter, AlertTriangle } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import type { AuditServerScope } from '@/lib/auditScope'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface AuditLog {
  id: string | number
  // API returns createdAt (ISO 8601). The legacy `timestamp` field is kept for
  // backward compatibility but may not be present.
  createdAt?: string
  timestamp?: string
  userName?: string
  user?: string
  action: string
  resource: string
  details?: string
  ip: string
}

interface AuditResponse {
  data: AuditLog[]
  total: number
  limit: number
  offset: number
}

interface ManagedNodeOption {
  id: string
  name?: string
  hostname?: string
}

type ActionType = 'login' | 'create' | 'delete' | 'update' | string

// ─── Helpers ──────────────────────────────────────────────────────────────────

function actionBadge(action: ActionType) {
  const lower = action.toLowerCase()
  const style = lower.includes('login')
    ? 'bg-blue-500/10 text-blue-400 border-blue-500/20'
    : lower.includes('create') || lower.includes('add')
    ? 'bg-green-500/10 text-green-400 border-green-500/20'
    : lower.includes('delete') || lower.includes('remove')
    ? 'bg-red-500/10 text-red-400 border-red-500/20'
    : lower.includes('update') || lower.includes('edit') || lower.includes('change')
    ? 'bg-amber-500/10 text-amber-400 border-amber-500/20'
    : 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20'

  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border capitalize ${style}`}
    >
      {action}
    </span>
  )
}

function formatTimestamp(raw: string | undefined): string {
  if (!raw) return '—'
  const d = new Date(raw)
  // new Date() returns an Invalid Date when the string is unparseable.
  if (isNaN(d.getTime())) return raw
  return d.toLocaleString('en-GB', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

// ─── Filter Bar ───────────────────────────────────────────────────────────────

interface FilterBarProps {
  serverScope: AuditServerScope
  onServerScope: (v: AuditServerScope) => void
  managedNodes: ManagedNodeOption[]
  userFilter: string
  onUserFilter: (v: string) => void
  actionFilter: string
  onActionFilter: (v: string) => void
  dateFrom: string
  onDateFrom: (v: string) => void
  dateTo: string
  onDateTo: (v: string) => void
}

function FilterBar({
  serverScope,
  onServerScope,
  managedNodes,
  userFilter,
  onUserFilter,
  actionFilter,
  onActionFilter,
  dateFrom,
  onDateFrom,
  dateTo,
  onDateTo,
}: FilterBarProps) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardContent className="py-3 px-4">
        <div className="flex flex-wrap gap-3 items-center">
          <div className="flex items-center gap-2 text-zinc-400">
            <Filter className="w-4 h-4" />
            <span className="text-xs font-medium">Filters</span>
          </div>
          <select
            value={serverScope}
            onChange={(e) => onServerScope(e.target.value as AuditServerScope)}
            aria-label="Audit server scope"
            className="bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-1.5 text-sm h-8 focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="all">All activity</option>
            <option value="local">HServer operations</option>
            {serverScope !== 'all' && serverScope !== 'local' && !managedNodes.some((node) => node.id === serverScope) && (
              <option value={serverScope}>{serverScope} operations</option>
            )}
            {managedNodes.map((node) => (
              <option key={node.id} value={node.id}>
                {node.name || node.hostname || node.id} operations
              </option>
            ))}
          </select>
          <Input
            value={userFilter}
            onChange={(e) => onUserFilter(e.target.value)}
            placeholder="Filter by user…"
            className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 h-8 text-sm w-44"
          />
          <Input
            value={actionFilter}
            onChange={(e) => onActionFilter(e.target.value)}
            placeholder="Action contains…"
            aria-label="Filter by action name"
            className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 h-8 text-sm w-44"
          />
          <Input
            type="date"
            value={dateFrom}
            onChange={(e) => onDateFrom(e.target.value)}
            className="bg-zinc-800 border-zinc-700 text-white h-8 text-sm w-36"
          />
          <span className="text-zinc-600 text-xs">to</span>
          <Input
            type="date"
            value={dateTo}
            onChange={(e) => onDateTo(e.target.value)}
            className="bg-zinc-800 border-zinc-700 text-white h-8 text-sm w-36"
          />
        </div>
      </CardContent>
    </Card>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export default function AuditPage() {
  const pageSize = 50
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedScope = searchParams.get('server')
  const serverScope: AuditServerScope = requestedScope || 'all'
  const [userFilter, setUserFilter] = useState('')
  const [actionFilter, setActionFilter] = useState('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [page, setPage] = useState(0)
  const deferredUserFilter = useDeferredValue(userFilter)
  const deferredActionFilter = useDeferredValue(actionFilter)

  const managedNodesQuery = useQuery<ManagedNodeOption[]>({
    queryKey: ['managed-nodes'],
    queryFn: () => api.get('/nodes'),
    staleTime: 10_000,
  })

  const auditQuery = useQuery<AuditResponse>({
    queryKey: ['audit', serverScope, deferredUserFilter, deferredActionFilter, dateFrom, dateTo, page],
    queryFn: () => {
      const query = new URLSearchParams({
        limit: String(pageSize),
        offset: String(page * pageSize),
      })
      if (serverScope !== 'all') query.set('server', serverScope)
      if (deferredUserFilter.trim()) query.set('user', deferredUserFilter.trim())
      if (deferredActionFilter.trim()) query.set('action_contains', deferredActionFilter.trim())
      if (dateFrom) query.set('from', `${dateFrom}T00:00:00Z`)
      if (dateTo) query.set('to', `${dateTo}T23:59:59Z`)
      return api.get<AuditResponse>(`/audit?${query}`)
    },
    staleTime: 0,
    refetchInterval: 30_000,
  })

  const managedNodes = managedNodesQuery.data ?? []
  const logsResp = auditQuery.data
  const logs = logsResp?.data ?? []
  const total = logsResp?.total ?? 0
  const firstVisible = total === 0 ? 0 : page * pageSize + 1
  const lastVisible = Math.min((page + 1) * pageSize, total)
  const pageCount = Math.max(1, Math.ceil(total / pageSize))

  const changeServerScope = (next: AuditServerScope) => {
    const params = new URLSearchParams(searchParams)
    if (next === 'all') params.delete('server')
    else params.set('server', next)
    setSearchParams(params, { replace: true })
    setPage(0)
  }

  const scopeLabel = serverScope === 'local'
    ? 'HServer operations'
    : serverScope !== 'all'
      ? `${managedNodes.find((node) => node.id === serverScope)?.name || managedNodes.find((node) => node.id === serverScope)?.hostname || serverScope} operations`
      : 'All panel activity'

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-zinc-600/10 rounded-lg flex items-center justify-center">
            <ScrollText className="w-4 h-4 text-zinc-400" />
          </div>
          <div>
            <h2 className="text-white font-semibold">Audit Log</h2>
            <p className="text-zinc-500 text-xs">{scopeLabel} — auto-refreshes every 30s</p>
          </div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => auditQuery.refetch()}
          disabled={auditQuery.isFetching}
          className="text-zinc-400 hover:text-white"
        >
          <RefreshCw className={`w-4 h-4 mr-2 ${auditQuery.isFetching ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {/* Filters */}
      <FilterBar
        serverScope={serverScope}
        onServerScope={changeServerScope}
        managedNodes={managedNodes}
        userFilter={userFilter}
        onUserFilter={(value) => { setUserFilter(value); setPage(0) }}
        actionFilter={actionFilter}
        onActionFilter={(value) => { setActionFilter(value); setPage(0) }}
        dateFrom={dateFrom}
        onDateFrom={(value) => { setDateFrom(value); setPage(0) }}
        dateTo={dateTo}
        onDateTo={(value) => { setDateTo(value); setPage(0) }}
      />

      {managedNodesQuery.isError && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-200">
          <span>Managed server options could not be loaded. Local and all-activity filters remain available.</span>
          <Button type="button" variant="outline" size="sm" onClick={() => { void managedNodesQuery.refetch() }} disabled={managedNodesQuery.isFetching} className="border-amber-500/30 text-amber-100">
            <RefreshCw className={`mr-2 size-3.5 ${managedNodesQuery.isFetching ? 'animate-spin' : ''}`} />Retry servers
          </Button>
        </div>
      )}

      {/* Table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <ScrollText className="w-4 h-4 text-zinc-400" />
            {auditQuery.isLoading ? (
              <Skeleton className="h-4 w-24 bg-zinc-800" />
            ) : auditQuery.isError ? (
              <span className="text-red-300">Audit events unavailable</span>
            ) : (
              <>
                {total} event{total !== 1 ? 's' : ''}
                {total > 0 && <span className="text-zinc-500 font-normal"> · showing {firstVisible}–{lastVisible}</span>}
              </>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
          <Table className="min-w-[860px]">
            <TableHeader>
              <TableRow className="border-zinc-800 hover:bg-transparent">
                <TableHead className="text-zinc-400 font-medium">Timestamp</TableHead>
                <TableHead className="text-zinc-400 font-medium">User</TableHead>
                <TableHead className="text-zinc-400 font-medium">Action</TableHead>
                <TableHead className="text-zinc-400 font-medium">Resource</TableHead>
                <TableHead className="text-zinc-400 font-medium">Details</TableHead>
                <TableHead className="text-zinc-400 font-medium">IP Address</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {auditQuery.isLoading ? (
                Array.from({ length: 6 }).map((_, i) => (
                  <TableRow key={i} className="border-zinc-800">
                    <TableCell><Skeleton className="h-4 w-36 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-28 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-5 w-16 bg-zinc-800 rounded" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-32 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-48 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-24 bg-zinc-800" /></TableCell>
                  </TableRow>
                ))
              ) : auditQuery.isError ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={6} className="py-10 text-center">
                    <AlertTriangle className="mx-auto size-5 text-red-400" />
                    <p className="mt-2 text-sm text-red-300">Audit events could not be loaded.</p>
                    <p className="mt-1 text-xs text-zinc-600">{auditQuery.error.message}</p>
                    <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void auditQuery.refetch() }} disabled={auditQuery.isFetching}>
                      <RefreshCw className={`mr-2 size-3.5 ${auditQuery.isFetching ? 'animate-spin' : ''}`} />Retry
                    </Button>
                  </TableCell>
                </TableRow>
              ) : logs.length === 0 ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={6} className="text-center text-zinc-500 py-8">
                    No audit events match the current filters
                  </TableCell>
                </TableRow>
              ) : (
                logs.map((log) => (
                  <TableRow
                    key={log.id}
                    className="border-zinc-800 hover:bg-zinc-800/40"
                  >
                    <TableCell className="text-zinc-400 text-sm font-mono whitespace-nowrap">
                      {formatTimestamp(log.createdAt ?? log.timestamp)}
                    </TableCell>
                    <TableCell className="text-white text-sm">{log.userName ?? log.user ?? ""}</TableCell>
                    <TableCell>{actionBadge(log.action)}</TableCell>
                    <TableCell className="text-zinc-400 text-sm">{log.resource}</TableCell>
                    <TableCell className={`max-w-[360px] truncate text-sm ${/failed|error/i.test(log.details ?? '') ? 'text-red-400' : 'text-zinc-400'}`} title={log.details ?? ''}>{log.details || '—'}</TableCell>
                    <TableCell className="text-zinc-500 text-sm font-mono">{log.ip}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
          </div>
          <div className="flex items-center justify-between gap-3 border-t border-zinc-800 px-4 py-3">
            <span className="text-xs text-zinc-500">Page {page + 1} of {pageCount}</span>
            <div className="flex gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={page === 0 || auditQuery.isFetching || auditQuery.isError}
                onClick={() => setPage(current => Math.max(0, current - 1))}
                className="h-8 border-zinc-700 bg-zinc-900 text-zinc-300 hover:bg-zinc-800 hover:text-white"
              >
                Previous
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={lastVisible >= total || auditQuery.isFetching || auditQuery.isError}
                onClick={() => setPage(current => current + 1)}
                className="h-8 border-zinc-700 bg-zinc-900 text-zinc-300 hover:bg-zinc-800 hover:text-white"
              >
                Next
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

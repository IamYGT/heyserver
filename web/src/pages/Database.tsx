import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Database as DatabaseIcon, Plus, Trash2, Search,
  Table, ChevronRight, HardDrive, Users, Play, Loader2, X,
  KeyRound, Eye, EyeOff, Copy, Archive, CalendarDays, RotateCcw,
  AlertTriangle, RefreshCw,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type {
  Database, DatabaseTable, QueryResult, DatabaseUser,
  CreateDatabaseRequest, DbEngine, PGMCredential, PGMBackup,
} from '@/lib/types'
import EmptyState from '@/components/EmptyState'
import { DependencyRemediation } from '@/components/DependencyRemediation'

function parseSize(sizeStr: string): number {
  if (!sizeStr) return 0
  const match = sizeStr.trim().match(/^([\d.]+)\s*(B|kB|KB|MB|GB|TB)$/i)
  if (!match) return 0
  const value = parseFloat(match[1])
  const unit = match[2].toLowerCase()
  switch (unit) {
    case 'b':  return value
    case 'kb': return value * 1024
    case 'mb': return value * 1024 * 1024
    case 'gb': return value * 1024 * 1024 * 1024
    case 'tb': return value * 1024 * 1024 * 1024 * 1024
    default:   return 0
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// ─── Engine Selector ──────────────────────────────────────────────────────────

interface EngineSelectorProps {
  value: DbEngine
  onChange: (v: DbEngine) => void
}

function EngineSelector({ value, onChange }: EngineSelectorProps) {
  return (
    <div className="flex gap-2">
      {(['postgresql', 'mariadb'] as DbEngine[]).map((eng) => (
        <button
          key={eng}
          type="button"
          onClick={() => onChange(eng)}
          className={`px-3 py-1.5 rounded-lg text-xs font-medium border transition-colors ${
            value === eng
              ? 'bg-blue-600/20 border-blue-500/50 text-blue-300'
              : 'border-zinc-700 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200'
          }`}
        >
          {eng === 'postgresql' ? 'PostgreSQL' : 'MariaDB'}
        </button>
      ))}
    </div>
  )
}

// ─── Query Editor ─────────────────────────────────────────────────────────────

interface QueryEditorProps {
  engine: DbEngine
  dbName: string
}

function QueryEditor({ engine, dbName }: QueryEditorProps) {
  const [query, setQuery] = useState('SELECT version();')
  const [result, setResult] = useState<QueryResult | null>(null)

  const mutation = useMutation({
    mutationFn: async () => {
      const response = await api.post<{
        result: { columns: string[]; rows: unknown[][]; rowCount: number }
      }>(`/databases/${engine}/${encodeURIComponent(dbName)}/query`, {
        query,
      })
      const raw = response.result
      return {
        columns: raw.columns ?? [],
        rows: (raw.rows ?? []).map((values) =>
          Object.fromEntries((raw.columns ?? []).map((column, index) => [column, values[index]])),
        ),
      } satisfies QueryResult
    },
    onSuccess: (data) => setResult(data),
    onError: (err) => {
      const msg = err instanceof Error ? err.message : 'Query failed'
      setResult({ columns: [], rows: [], error: msg })
    },
  })

  return (
    <div className="space-y-3">
      <div className="relative">
        <textarea
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          rows={4}
          className="w-full bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono text-sm rounded-lg p-3 focus:outline-none focus:border-blue-500 resize-none"
          placeholder="Enter SQL query..."
        />
        <button
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending || !query.trim()}
          className="absolute bottom-3 right-3 flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-xs font-medium rounded-md transition-colors"
        >
          {mutation.isPending ? <Loader2 className="w-3 h-3 animate-spin" /> : <Play className="w-3 h-3" />}
          Run
        </button>
      </div>

      {result && (
        <div className="rounded-lg border border-zinc-800 overflow-hidden">
          {result.error ? (
            <div className="px-4 py-3 bg-red-500/10 border-b border-red-500/20">
              <p className="text-red-400 text-xs font-mono">{result.error}</p>
            </div>
          ) : (
            <>
              <div className="px-4 py-2 bg-zinc-900 border-b border-zinc-800 flex items-center justify-between">
                <span className="text-zinc-400 text-xs">
                  {result.rows.length} row{result.rows.length !== 1 ? 's' : ''}
                  {result.duration !== undefined && ` · ${result.duration}ms`}
                </span>
              </div>
              {result.columns.length > 0 && (
                <div className="overflow-x-auto">
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="bg-zinc-800/60">
                        {result.columns.map((col) => (
                          <th key={col} className="px-3 py-2 text-left text-zinc-400 font-medium whitespace-nowrap">
                            {col}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {result.rows.slice(0, 100).map((row, i) => (
                        <tr key={i} className="border-t border-zinc-800 hover:bg-zinc-800/40">
                          {result.columns.map((col) => (
                            <td key={col} className="px-3 py-2 text-zinc-300 font-mono whitespace-nowrap max-w-xs truncate">
                              {row[col] === null ? (
                                <span className="text-zinc-600 italic">null</span>
                              ) : (
                                String(row[col])
                              )}
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Create Database Modal ────────────────────────────────────────────────────

interface CreateDbModalProps {
  engine: DbEngine
  onClose: () => void
  onCreated: () => void
}

function CreateDbModal({ engine, onClose, onCreated }: CreateDbModalProps) {
  const [form, setForm] = useState<CreateDatabaseRequest>({ name: '', owner: '' })

  const mutation = useMutation({
    mutationFn: () => api.post(`/databases`, {
      engine,
      name: form.name,
      owner: form.owner,
    }),
    onSuccess: () => {
      toast.success('Database created successfully')
      onCreated()
      onClose()
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to create database'),
  })

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70">
      <div className="w-full max-w-md bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
          <span className="text-white font-semibold text-sm">Create {engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'} Database</span>
          <button type="button" aria-label="Close create database dialog" onClick={onClose} className="text-zinc-500 hover:text-white transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="p-5 space-y-4">
          <div>
            <label className="text-zinc-400 text-xs font-medium block mb-1.5">Database Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="my_database"
              className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
            />
          </div>
          {engine === 'postgresql' && <div>
            <label className="text-zinc-400 text-xs font-medium block mb-1.5">Owner (optional)</label>
            <input
              type="text"
              value={form.owner}
              onChange={(e) => setForm({ ...form, owner: e.target.value })}
              placeholder="postgres"
              className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-blue-500"
            />
          </div>}
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose} className="border-zinc-700 text-zinc-300">
              Cancel
            </Button>
            <Button
              onClick={() => mutation.mutate()}
              disabled={mutation.isPending || !form.name.trim()}
              className="bg-blue-600 hover:bg-blue-500 text-white"
            >
              {mutation.isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
              Create
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

// ─── Database Row ─────────────────────────────────────────────────────────────

interface DbRowProps {
  db: Database
  engine: DbEngine
  onDelete: (name: string) => void
}

function DatabaseRow({ db, engine, onDelete }: DbRowProps) {
  const [expanded, setExpanded] = useState(false)
  const [activeTab, setActiveTab] = useState<'tables' | 'query'>('tables')

  const { data: tablesResponse, isLoading: tablesLoading, error: tablesError } = useQuery<{ tables: DatabaseTable[] }>({
    queryKey: ['db-tables', engine, db.name],
    queryFn: () => api.get<{ tables: DatabaseTable[] }>(`/databases/${engine}/${encodeURIComponent(db.name)}/tables`),
    enabled: expanded,
  })
  const tables = tablesResponse?.tables ?? []

  return (
    <div className="border border-zinc-800 rounded-xl overflow-hidden">
      <div
        className="flex items-center gap-3 px-4 py-3 bg-zinc-900 cursor-pointer hover:bg-zinc-800/60 transition-colors"
        onClick={() => setExpanded((p) => !p)}
      >
        <ChevronRight
          className={`w-4 h-4 text-zinc-500 transition-transform ${expanded ? 'rotate-90' : ''}`}
        />
        <DatabaseIcon className="w-4 h-4 text-blue-400" />
        <span className="text-white font-medium text-sm flex-1">{db.name}</span>
        <span className="text-zinc-500 text-xs">{db.owner}</span>
        <Badge className="bg-zinc-800 text-zinc-400 border-zinc-700 text-xs">
          {db.tableCount ?? db.tables ?? 0} tables
        </Badge>
        <span className="text-zinc-500 text-xs">{typeof db.size === "number" ? formatBytes(db.size) : String(db.size || "0 B")}</span>
        <button
          type="button"
          aria-label={`Delete database ${db.name}`}
          onClick={(e) => { e.stopPropagation(); onDelete(db.name) }}
          className="text-zinc-600 hover:text-red-400 transition-colors ml-1"
        >
          <Trash2 className="w-3.5 h-3.5" />
        </button>
      </div>

      {expanded && (
        <div className="border-t border-zinc-800 bg-zinc-950">
          <div className="flex gap-0 border-b border-zinc-800">
            {(['tables', 'query'] as const).map((tab) => (
              <button
                key={tab}
                onClick={() => setActiveTab(tab)}
                className={`px-4 py-2.5 text-xs font-medium capitalize transition-colors ${
                  activeTab === tab
                    ? 'text-white border-b-2 border-blue-500'
                    : 'text-zinc-500 hover:text-zinc-300'
                }`}
              >
                {tab === 'tables' ? <><Table className="w-3 h-3 inline mr-1.5" />Tables</> : <><Play className="w-3 h-3 inline mr-1.5" />Query</>}
              </button>
            ))}
          </div>

          <div className="p-4">
            {activeTab === 'tables' ? (
              tablesLoading ? (
                <Skeleton className="h-24 w-full bg-zinc-800" />
              ) : tablesError ? (
                <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-300">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                  <span>{tablesError.message}</span>
                </div>
              ) : tables.length > 0 ? (
                <div className="space-y-1">
                  {tables.map((t) => (
                    <div
                      key={t.name}
                      className="flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-zinc-800/60 transition-colors"
                    >
                      <Table className="w-3.5 h-3.5 text-zinc-500" />
                      <span className="text-zinc-200 text-sm font-mono flex-1">{t.name}</span>
                      <span className="text-zinc-500 text-xs">{(t.rowsEstimate ?? t.rows ?? 0).toLocaleString()} rows</span>
                      <span className="text-zinc-600 text-xs">{typeof t.size === 'number' ? formatBytes(t.size) : t.size}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-zinc-500 text-sm text-center py-4">No tables found</p>
              )
            ) : (
              <QueryEditor engine={engine} dbName={db.name} />
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

// ─── Credentials Tab ─────────────────────────────────────────────────────────

function CredentialsTab() {
  const [revealed, setRevealed] = useState<Set<number>>(new Set())

  const { data: creds = [], isLoading, error, refetch } = useQuery<PGMCredential[]>({
    queryKey: ['pgm-credentials'],
    queryFn: () => api.get<PGMCredential[]>('/databases/credentials'),
  })

  function toggleReveal(id: number) {
    setRevealed((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function copyPassword(password: string) {
    navigator.clipboard.writeText(password)
    toast.success('Password copied to clipboard')
  }

  return (
    <div className="space-y-4">
      <p className="text-zinc-400 text-sm">
        Database credentials stored in <span className="font-mono text-zinc-300">pgm_metadata</span>. Admin only.
      </p>

      <Card className="bg-zinc-900 border-zinc-800 overflow-hidden">
        <CardContent className="p-0">
          {isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : error ? (
            <div className="flex flex-col items-center gap-3 p-8 text-center">
              <AlertTriangle className="size-6 text-red-400" />
              <div><p className="text-sm font-medium text-red-300">Credential store is unavailable</p><p className="mt-1 max-w-2xl break-words font-mono text-xs text-red-400/70">{error.message}</p></div>
              <Button size="sm" variant="outline" onClick={() => refetch()}><RefreshCw className="size-3.5" /> Retry</Button>
            </div>
          ) : creds.length === 0 ? (
            <EmptyState
              icon={KeyRound}
              title="No credentials found"
              description="No database credentials in pgm_metadata."
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm min-w-[560px]">
                <thead>
                  <tr className="border-b border-zinc-800 bg-zinc-800/40">
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">DB Name</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">User</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">Password</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">Host</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">Port</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">Created</th>
                    <th className="px-4 py-3 text-left text-xs font-medium text-zinc-500">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {creds.map((cred) => (
                    <tr key={cred.id} className="border-b border-zinc-800 hover:bg-zinc-800/40 transition-colors">
                      <td className="px-4 py-3 font-mono text-zinc-100 text-xs">{cred.dbName}</td>
                      <td className="px-4 py-3 font-mono text-zinc-300 text-xs">{cred.dbUser}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-xs text-zinc-300 min-w-0 max-w-36 truncate">
                            {revealed.has(cred.id) ? cred.dbPassword : '••••••••••••'}
                          </span>
                          <button
                            onClick={() => toggleReveal(cred.id)}
                            className="text-zinc-600 hover:text-zinc-300 transition-colors flex-shrink-0"
                            title={revealed.has(cred.id) ? 'Hide' : 'Show'}
                          >
                            {revealed.has(cred.id)
                              ? <EyeOff className="w-3.5 h-3.5" />
                              : <Eye className="w-3.5 h-3.5" />
                            }
                          </button>
                          <button
                            onClick={() => copyPassword(cred.dbPassword)}
                            className="text-zinc-600 hover:text-blue-400 transition-colors flex-shrink-0"
                            title="Copy password"
                          >
                            <Copy className="w-3.5 h-3.5" />
                          </button>
                        </div>
                      </td>
                      <td className="px-4 py-3 font-mono text-zinc-400 text-xs">{cred.dbHost}</td>
                      <td className="px-4 py-3 font-mono text-zinc-400 text-xs">{cred.dbPort}</td>
                      <td className="px-4 py-3 text-zinc-500 text-xs">{cred.createdAt || '—'}</td>
                      <td className="px-4 py-3">
                        {cred.isActive ? (
                          <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">active</Badge>
                        ) : (
                          <Badge className="bg-zinc-500/10 text-zinc-500 border-zinc-500/20 text-xs">inactive</Badge>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Backups Tab ──────────────────────────────────────────────────────────────

interface RestoreTarget {
  database: string
  backupPath: string
  backupName: string
}

function BackupsTab() {
  const [restoreTarget, setRestoreTarget] = useState<RestoreTarget | null>(null)
  const [expandedBackup, setExpandedBackup] = useState<string | null>(null)

  const backupsQuery = useQuery<PGMBackup[]>({
    queryKey: ['pgm-backups'],
    queryFn: () => api.get<PGMBackup[]>('/databases/pgm-backups'),
    refetchInterval: 60_000,
  })
  const backups = backupsQuery.data ?? []

  const restoreMutation = useMutation({
    mutationFn: (target: RestoreTarget) =>
      api.post('/databases/pgm-restore', {
        database: target.database,
        backupPath: target.backupPath,
      }),
    onSuccess: (_data, target) => {
      toast.success(`Restored ${target.database} successfully`)
      setRestoreTarget(null)
    },
    onError: (err) => {
      const msg = err instanceof Error ? err.message : 'Restore failed'
      toast.error(`Restore failed: ${msg}`)
    },
  })

  // Derive per-backup database file list from the backup name + databases count.
  // We can't enumerate files from frontend, so we query the backup list's path via expand.
  const backupFilesQuery = useQuery<string[]>({
    queryKey: ['pgm-backup-files', expandedBackup],
    queryFn: () =>
      api.get<string[]>(`/databases/pgm-backup-files/${expandedBackup}`),
    enabled: !!expandedBackup,
    retry: false,
  })
  const backupFiles = backupFilesQuery.data

  const latest = backups.slice(0, 20)

  return (
    <div className="space-y-4">
      {/* Restore confirm modal */}
      {restoreTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70">
          <div className="w-full max-w-md bg-zinc-900 border border-amber-700/50 rounded-xl shadow-2xl">
            <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
              <div className="flex items-center gap-2">
                <RotateCcw className="w-4 h-4 text-amber-400" />
                <span className="text-white font-semibold text-sm">Confirm Restore</span>
              </div>
              <button
                onClick={() => setRestoreTarget(null)}
                className="text-zinc-500 hover:text-white transition-colors"
                disabled={restoreMutation.isPending}
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-5 space-y-4">
              <div className="rounded-lg bg-amber-500/10 border border-amber-500/20 px-4 py-3">
                <p className="text-amber-400 text-xs font-medium">
                  This will overwrite all data in the target database. This action cannot be undone.
                </p>
              </div>
              <div className="space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-zinc-500">Database</span>
                  <span className="font-mono text-zinc-100">{restoreTarget.database}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-zinc-500">Backup</span>
                  <span className="font-mono text-zinc-300 text-xs">{restoreTarget.backupName}</span>
                </div>
              </div>
              <div className="flex justify-end gap-2 pt-2">
                <Button
                  variant="outline"
                  onClick={() => setRestoreTarget(null)}
                  className="border-zinc-700 text-zinc-300"
                  disabled={restoreMutation.isPending}
                >
                  Cancel
                </Button>
                <Button
                  onClick={() => restoreMutation.mutate(restoreTarget)}
                  disabled={restoreMutation.isPending}
                  className="bg-amber-600 hover:bg-amber-500 text-white"
                >
                  {restoreMutation.isPending && (
                    <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />
                  )}
                  Restore
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="flex items-center justify-between">
        <p className="text-zinc-400 text-sm">
          pgm backup snapshots — latest 20 shown. Auto-refresh 60s.
        </p>
        {backups.length > 0 && (
          <div className="flex items-center gap-1.5 text-xs text-zinc-500">
            <div className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
            {backups.length} total backups
          </div>
        )}
      </div>

      <Card className="bg-zinc-900 border-zinc-800 overflow-hidden">
        <CardContent className="p-0">
          {backupsQuery.isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : backupsQuery.isError ? (
            <div className="flex flex-col items-center gap-3 p-8 text-center">
              <AlertTriangle className="size-6 text-red-400" />
              <div>
                <p className="text-sm font-medium text-red-300">Database backups could not be loaded</p>
                <p className="mt-1 max-w-2xl break-words font-mono text-xs text-red-400/70">{backupsQuery.error.message}</p>
              </div>
              <Button size="sm" variant="outline" onClick={() => { void backupsQuery.refetch() }} disabled={backupsQuery.isFetching}>
                <RefreshCw className={`size-3.5 ${backupsQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
              </Button>
            </div>
          ) : latest.length === 0 ? (
            <EmptyState
              icon={Archive}
              title="No backups found"
              description="No database backup snapshots were found in the configured backup directory."
            />
          ) : (
            <div className="divide-y divide-zinc-800">
              {latest.map((bk, i) => {
                const isExpanded = expandedBackup === bk.name
                return (
                  <div key={bk.name}>
                    <div
                      className="flex items-center gap-3 px-4 py-3 hover:bg-zinc-800/40 transition-colors cursor-pointer"
                      onClick={() => setExpandedBackup(isExpanded ? null : bk.name)}
                    >
                      <ChevronRight
                        className={`w-3.5 h-3.5 text-zinc-600 transition-transform flex-shrink-0 ${isExpanded ? 'rotate-90' : ''}`}
                      />
                      <Archive className={`w-3.5 h-3.5 flex-shrink-0 ${i === 0 ? 'text-blue-400' : 'text-zinc-600'}`} />
                      <span className="font-mono text-xs text-zinc-200 flex-1">{bk.name}</span>
                      {i === 0 && (
                        <Badge className="bg-blue-500/10 text-blue-400 border-blue-500/20 text-xs">latest</Badge>
                      )}
                      <span className="font-mono text-zinc-400 text-xs">{bk.size}</span>
                      <Badge className="bg-zinc-800 text-zinc-400 border-zinc-700 text-xs">
                        {bk.databases} DBs
                      </Badge>
                      <div className="flex items-center gap-1 text-zinc-500 text-xs">
                        <CalendarDays className="w-3 h-3" />
                        {bk.createdAt || bk.name}
                      </div>
                    </div>

                    {isExpanded && (
                      <div className="border-t border-zinc-800 bg-zinc-950 px-4 py-3">
                        <p className="text-zinc-500 text-xs mb-3">
                          Select a database to restore from this backup:
                        </p>
                        {backupFilesQuery.isError ? (
                          <div className="flex items-center justify-between gap-3 rounded-lg border border-red-500/20 bg-red-500/5 p-3">
                            <div>
                              <p className="text-xs text-red-300">Backup contents could not be loaded.</p>
                              <p className="mt-1 break-words font-mono text-[11px] text-red-400/70">{backupFilesQuery.error.message}</p>
                            </div>
                            <Button size="sm" variant="outline" onClick={() => { void backupFilesQuery.refetch() }} disabled={backupFilesQuery.isFetching}>
                              <RefreshCw className={`size-3.5 ${backupFilesQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
                            </Button>
                          </div>
                        ) : backupFiles && backupFiles.length > 0 ? (
                          <div className="space-y-1.5">
                            {backupFiles.map((file) => {
                              const dbName = file.replace(/\.sql(\.gz)?$/, '')
                              const filePath = `${bk.path}/${file}`
                              return (
                                <div
                                  key={file}
                                  className="flex items-center justify-between px-3 py-2 rounded-lg bg-zinc-900 border border-zinc-800"
                                >
                                  <div className="flex items-center gap-2">
                                    <DatabaseIcon className="w-3.5 h-3.5 text-zinc-500" />
                                    <span className="font-mono text-xs text-zinc-200">{dbName}</span>
                                    <span className="text-zinc-600 text-xs">{file.endsWith('.gz') ? 'gzip' : 'sql'}</span>
                                  </div>
                                  <button
                                    onClick={(e) => {
                                      e.stopPropagation()
                                      setRestoreTarget({ database: dbName, backupPath: filePath, backupName: bk.name })
                                    }}
                                    className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-amber-400 border border-amber-700/50 rounded-md hover:bg-amber-500/10 transition-colors"
                                  >
                                    <RotateCcw className="w-3 h-3" />
                                    Restore
                                  </button>
                                </div>
                              )
                            })}
                          </div>
                        ) : (
                          <p className="text-zinc-600 text-xs italic">
                            {backupFiles ? 'No database files found in this backup.' : 'Loading...'}
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

type MainTab = 'databases' | 'users' | 'credentials' | 'backups'

interface DatabaseSourceStatus {
  available: boolean
  state?: 'healthy' | 'client-missing' | 'stopped' | 'authentication-failed' | 'unavailable'
  error?: string
}

interface DatabaseSourceRemediationProps {
  engine: DbEngine
  source: DatabaseSourceStatus
  retry: () => void
  retrying: boolean
}

function DatabaseSourceRemediation({ engine, source, retry, retrying }: DatabaseSourceRemediationProps) {
  const label = engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'
  const state = source.state ?? 'unavailable'

  if (state === 'client-missing') {
    return (
      <DependencyRemediation
        title={`${label} client is not installed`}
        summary={`HServer cannot inspect or manage ${label} until its local command-line client is available.`}
        state="not-configured"
        error={source.error}
        retry={retry}
        retrying={retrying}
        steps={engine === 'postgresql' ? [
          <>Install <code>postgresql-client</code> from a supported Ubuntu repository.</>,
          <>Verify <code>psql --version</code> and <code>sudo -u postgres psql -d postgres -c '\conninfo'</code>.</>,
          'Retry detection after the client can reach the local server.',
        ] : [
          <>Install <code>mariadb-client</code> from a supported Ubuntu repository.</>,
          <>Verify <code>mysql --version</code> and local socket connectivity with <code>sudo mysql -u root -e 'SELECT 1;'</code>.</>,
          'Retry detection after the client can reach the local server.',
        ]}
      />
    )
  }

  if (state === 'stopped') {
    return (
      <DependencyRemediation
        title={`${label} is not accepting local connections`}
        summary={`The ${label} client is present, but the local database service or socket is not ready. Mutating controls remain paused.`}
        state="stopped"
        error={source.error}
        retry={retry}
        retrying={retrying}
        steps={engine === 'postgresql' ? [
          <>Inspect <code>systemctl status postgresql</code> and <code>journalctl -u postgresql</code>.</>,
          <>Start the service if appropriate, then verify it with <code>pg_isready</code>.</>,
          'Retry detection after PostgreSQL accepts local connections.',
        ] : [
          <>Inspect <code>systemctl status mariadb</code> and <code>journalctl -u mariadb</code>.</>,
          <>Start the service if appropriate, then verify its local socket.</>,
          'Retry detection after MariaDB accepts local connections.',
        ]}
      />
    )
  }

  if (state === 'authentication-failed') {
    return (
      <DependencyRemediation
        title={`${label} management authentication failed`}
        summary={`HServer reached ${label}, but the installation-owned local management identity was rejected. No password is collected or displayed here.`}
        error={source.error}
        retry={retry}
        retrying={retrying}
        steps={engine === 'postgresql' ? [
          <>Verify the local <code>postgres</code> OS identity and peer authentication policy.</>,
          <>Test <code>sudo -u postgres psql -d postgres -c '\conninfo'</code> without exposing credentials.</>,
          'Retry detection after local administrator access succeeds.',
        ] : [
          <>Verify the installation-owned root or socket authentication policy.</>,
          <>Test <code>sudo mysql -u root -e 'SELECT 1;'</code> without printing credentials.</>,
          'Retry detection after local administrator access succeeds.',
        ]}
      />
    )
  }

  return (
    <DependencyRemediation
      title={`${label} inventory is unavailable`}
      summary={`HServer could not classify the local ${label} inventory failure. Mutating controls remain paused until detection succeeds.`}
      error={source.error}
      retry={retry}
      retrying={retrying}
      steps={[
        'Run the packaged HServer doctor and inspect the reported database check.',
        `Verify the local ${label} client, service, socket, and HServer logs.`,
        'Retry detection after correcting the reported error.',
      ]}
    />
  )
}

interface DatabaseListResponse {
  databases: Database[]
  sources: Partial<Record<DbEngine, DatabaseSourceStatus>>
}

interface DatabaseUsersResponse {
  users: DatabaseUser[]
  sources: Partial<Record<DbEngine, DatabaseSourceStatus>>
}

export default function DatabasePage() {
  const queryClient = useQueryClient()
  const [engine, setEngine] = useState<DbEngine>('postgresql')
  const [search, setSearch] = useState('')
  const [activeTab, setActiveTab] = useState<MainTab>('databases')
  const [showCreateDb, setShowCreateDb] = useState(false)

  const databasesQuery = useQuery<DatabaseListResponse>({
    queryKey: ['databases', engine],
    queryFn: () => api.get<DatabaseListResponse>(`/databases?engine=${engine}`),
  })
  const dbResp = databasesQuery.data
  const databases = dbResp?.databases ?? []
  const sourceStatus = dbResp?.sources?.[engine]
  const inventoryUnavailable = databasesQuery.isError || sourceStatus?.available === false
  const inventoryUnknown = databasesQuery.isLoading || inventoryUnavailable

  const usersQuery = useQuery<DatabaseUsersResponse>({
    queryKey: ['db-users', engine],
    queryFn: () => api.get<DatabaseUsersResponse>(`/databases/users?engine=${engine}`),
    enabled: activeTab === 'users',
  })
  const usersResp = usersQuery.data
  const dbUsers = usersResp?.users ?? []
  const usersSourceUnavailable = usersResp?.sources?.[engine]?.available === false

  const deleteMutation = useMutation({
    mutationFn: (name: string) => api.delete(`/databases/${engine}/${encodeURIComponent(name)}`, { confirm: `DROP ${name}` }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['databases', engine] })
      toast.success('Database deleted')
    },
    onError: (error: Error) => toast.error(error.message || 'Failed to delete database'),
  })

  const filtered = databases.filter((d) => {
    const selectedEngine = engine === 'postgresql' ? 'postgres' : 'mariadb'
    return d.engine === selectedEngine && d.name.toLowerCase().includes(search.toLowerCase())
  })

  const confirmDelete = (name: string) => {
    if (window.confirm(`Delete ${engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'} database "${name}"? This cannot be undone.`)) {
      deleteMutation.mutate(name)
    }
  }

  return (
    <div className="space-y-6">
      {showCreateDb && (
        <CreateDbModal
          engine={engine}
          onClose={() => setShowCreateDb(false)}
          onCreated={() => queryClient.invalidateQueries({ queryKey: ['databases', engine] })}
        />
      )}

      {/* Header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Databases</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {databasesQuery.isLoading
              ? 'Loading database inventory…'
              : inventoryUnavailable
                ? 'Database inventory unavailable'
                : `${databases.length} databases`}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <EngineSelector value={engine} onChange={setEngine} />
          <Button
            onClick={() => setShowCreateDb(true)}
            disabled={databasesQuery.isLoading || inventoryUnavailable}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            <Plus className="w-4 h-4 mr-2" />
            New Database
          </Button>
        </div>
      </div>

      {databasesQuery.isError && (
        <DependencyRemediation
          title="Database API is unavailable"
          summary="HServer could not load database inventory. Metrics and mutating controls remain paused until the API responds."
          error={databasesQuery.error.message}
          retry={() => { void databasesQuery.refetch() }}
          retrying={databasesQuery.isFetching}
          steps={[
            'Run the packaged HServer doctor and inspect HServer service logs.',
            `Verify the selected ${engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'} client locally.`,
            'Retry detection after the API and local client are ready.',
          ]}
        />
      )}

      {!databasesQuery.isError && sourceStatus?.available === false && (
        <DatabaseSourceRemediation
          engine={engine}
          source={sourceStatus}
          retry={() => { void databasesQuery.refetch() }}
          retrying={databasesQuery.isFetching}
        />
      )}

      {/* Stats row */}
      <div className="grid grid-cols-3 gap-4">
        {[
          { label: 'Total Databases', value: inventoryUnknown ? '—' : databases.length, icon: DatabaseIcon, color: 'text-blue-400' },
          { label: 'Total Size', value: inventoryUnknown ? '—' : formatBytes(databases.reduce((acc, d) => acc + (typeof d.size === 'number' ? d.size : parseSize(d.size as string)), 0)), icon: HardDrive, color: 'text-green-400' },
          { label: 'Total Tables', value: inventoryUnknown ? '—' : databases.reduce((acc, d) => acc + (d.tableCount ?? d.tables ?? 0), 0), icon: Table, color: 'text-purple-400' },
        ].map(({ label, value, icon: Icon, color }) => (
          <Card key={label} className="bg-zinc-900 border-zinc-800">
            <CardContent className="pt-4 pb-4">
              <div className="flex items-center gap-3">
                <Icon className={`w-5 h-5 ${color}`} />
                <div>
                  <p className="text-zinc-500 text-xs">{label}</p>
                  <p className="text-white font-bold">{value}</p>
                </div>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Tabs */}
      <div className="flex gap-0 border-b border-zinc-800">
        {(
          [
            { id: 'databases', label: 'Databases', icon: DatabaseIcon },
            { id: 'users', label: 'Users', icon: Users },
            { id: 'credentials', label: 'Credentials', icon: KeyRound },
            { id: 'backups', label: 'Backups', icon: Archive },
          ] as { id: MainTab; label: string; icon: React.ComponentType<{ className?: string }> }[]
        ).map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            onClick={() => setActiveTab(id)}
            className={`px-4 py-2.5 text-sm font-medium transition-colors flex items-center gap-1.5 ${
              activeTab === id
                ? 'text-white border-b-2 border-blue-500'
                : 'text-zinc-500 hover:text-zinc-300'
            }`}
          >
            <Icon className="w-4 h-4" />
            {label}
          </button>
        ))}
      </div>

      {activeTab === 'databases' && (
        <div className="space-y-4">
          {/* Search */}
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-500" />
            <input
              type="text"
              placeholder="Search databases..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full bg-zinc-900 border border-zinc-800 text-white placeholder:text-zinc-600 rounded-lg py-2 pl-9 pr-4 text-sm focus:outline-none focus:border-blue-500"
            />
          </div>

          {databasesQuery.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full bg-zinc-800" />
              ))}
            </div>
          ) : databasesQuery.isError ? (
            <div className="rounded-xl border border-red-500/20 bg-red-500/5 py-10 text-center text-sm text-red-300/80">Database inventory is unavailable until the API becomes ready.</div>
          ) : filtered.length > 0 ? (
            <div className="space-y-2">
              {filtered.map((db) => (
                <DatabaseRow
                  key={db.name}
                  db={db}
                  engine={engine}
                  onDelete={confirmDelete}
                />
              ))}
            </div>
          ) : sourceStatus?.available === false ? (
            <div className="rounded-xl border border-red-500/20 bg-red-500/5 py-10 text-center text-sm text-red-300/80">Database inventory is unavailable until the engine becomes ready.</div>
          ) : (
            <EmptyState
              icon={DatabaseIcon}
              title={search ? 'No databases match your search' : 'No databases found'}
              description={search ? 'Try a different search term.' : `No ${engine} databases found.`}
              actionLabel={search ? undefined : 'Create Database'}
              onAction={search ? undefined : () => setShowCreateDb(true)}
            />
          )}
        </div>
      )}

      {activeTab === 'users' && (
        <div className="space-y-2">
          {usersQuery.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-14 w-full bg-zinc-800" />)}
            </div>
          ) : usersQuery.isError ? (
            <div className="flex flex-col items-center gap-3 rounded-xl border border-red-500/30 bg-red-500/10 p-8 text-center">
              <AlertTriangle className="size-6 text-red-400" />
              <div>
                <p className="text-sm font-medium text-red-300">Database users could not be loaded</p>
                <p className="mt-1 max-w-2xl break-words font-mono text-xs text-red-400/70">{usersQuery.error.message}</p>
              </div>
              <Button size="sm" variant="outline" onClick={() => { void usersQuery.refetch() }} disabled={usersQuery.isFetching}>
                <RefreshCw className={`size-3.5 ${usersQuery.isFetching ? 'animate-spin' : ''}`} /> Retry
              </Button>
            </div>
          ) : usersSourceUnavailable ? (
            <DatabaseSourceRemediation
              engine={engine}
              source={usersResp.sources[engine]!}
              retry={() => { void usersQuery.refetch() }}
              retrying={usersQuery.isFetching}
            />
          ) : null}
          {!usersQuery.isLoading && !usersQuery.isError && !usersSourceUnavailable && dbUsers.map((user) => (
            <Card key={user.name} className="bg-zinc-900 border-zinc-800">
              <CardContent className="p-4">
                <div className="flex items-center gap-3">
                  <Users className="w-4 h-4 text-zinc-400" />
                  <span className="text-white font-medium text-sm flex-1">{user.name}</span>
                  {user.superuser && (
                    <Badge className="bg-amber-500/10 text-amber-400 border-amber-500/20 text-xs">superuser</Badge>
                  )}
                  {user.canLogin ? (
                    <Badge className="bg-green-500/10 text-green-400 border-green-500/20 text-xs">can login</Badge>
                  ) : (
                    <Badge variant="outline" className="text-zinc-500 text-xs">no login</Badge>
                  )}
                  {user.databases && user.databases.length > 0 && (
                    <span className="text-zinc-500 text-xs">{user.databases.join(', ')}</span>
                  )}
                </div>
              </CardContent>
            </Card>
          ))}
          {!usersQuery.isLoading && !usersQuery.isError && !usersSourceUnavailable && dbUsers.length === 0 && (
            <EmptyState
              icon={Users}
              title="No database users found"
              description={`No ${engine} users configured.`}
            />
          )}
        </div>
      )}

      {activeTab === 'credentials' && <CredentialsTab />}
      {activeTab === 'backups' && <BackupsTab />}
    </div>
  )
}

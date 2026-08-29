import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Archive, Database, Loader2, RefreshCw, RotateCw } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { requireRemoteMutationReadiness, useRemoteMutationReadiness } from './remoteMutationReadiness'

interface RemoteDatabase { name: string; size: number; connections: number; objects: number }
interface DatabaseSession { id: string; user: string; database?: string; state: string; age_seconds: number; query?: string }
interface DatabaseEngine { id: 'mariadb' | 'postgresql'; name: string; version: string; unit: string; active: string; data_size: number; databases: RemoteDatabase[]; sessions: DatabaseSession[] }
interface ActionResult { message: string }

function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** power).toFixed(power > 2 ? 1 : 0)} ${units[power]}`
}
function formatAge(seconds: number) { if (seconds < 60) return `${seconds}s`; if (seconds < 3600) return `${Math.floor(seconds / 60)}m`; return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m` }

export function RemoteDatabases({ nodeID, online, readAvailable, actionAvailable, onBackups }: { nodeID: string; online: boolean; readAvailable: boolean; actionAvailable: boolean; onBackups: () => void }) {
  const controlsReady = useRemoteMutationReadiness()
  const nodeBase = `/nodes/${encodeURIComponent(nodeID)}`
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<'mariadb' | 'postgresql'>('mariadb')
  const enginesQuery = useQuery<DatabaseEngine[]>({ queryKey: ['managed-node-databases', nodeID], queryFn: () => api.get(nodeBase + '/databases'), enabled: online && readAvailable, refetchInterval: 30_000 })
  const restartMutation = useMutation<ActionResult, Error, DatabaseEngine>({
    mutationFn: (engine) => { requireRemoteMutationReadiness(controlsReady); return api.post(`${nodeBase}/databases/${engine.id}/actions/restart`) },
    onSuccess: async (result) => { toast.success(result.message); await queryClient.invalidateQueries({ queryKey: ['managed-node-databases', nodeID] }) },
    onError: (error) => toast.error(error.message || 'Database restart failed'),
  })
  const engines = enginesQuery.data ?? []
  const engine = engines.find((item) => item.id === selected) ?? engines[0]

  if (!online) return <DatabaseUnavailable title="Managed node is offline" detail="Database inventory, sessions, and restart actions remain unavailable until the agent reconnects." />
  if (!readAvailable) return <DatabaseUnavailable title="Database inventory is disabled" detail="This agent does not advertise database.read. Enable database inventory locally, then restart the agent." />

  return <div className="space-y-4">
    {enginesQuery.isSuccess && !actionAvailable ? <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-4 text-xs text-amber-200">Database inventory is available. Restart controls are read-only because <code>database.action</code> is disabled locally.</CardContent></Card> : null}
    <div className="flex flex-wrap gap-3">{engines.map((item) => <button key={item.id} onClick={() => setSelected(item.id)} className={cn('min-w-[260px] flex-1 rounded-2xl border p-4 text-left transition', engine?.id === item.id ? 'border-violet-500/50 bg-violet-500/[0.07]' : 'border-zinc-800 bg-zinc-900/80 hover:border-zinc-700')}><div className="flex items-center justify-between"><span className="flex items-center gap-2 text-sm font-semibold text-zinc-200"><Database className="size-4 text-violet-400" />{item.name}</span><span className={cn('rounded-full px-2 py-1 text-[9px] font-bold uppercase', item.active === 'active' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400')}>{item.active}</span></div><p className="mt-3 font-mono text-xs text-zinc-400">{item.version}</p><p className="mt-1 text-[10px] text-zinc-600">{item.databases.length} databases · {formatBytes(item.data_size)} on disk · {item.sessions.length} sessions</p></button>)}</div>
    {enginesQuery.isLoading ? <div className="grid h-56 place-items-center"><Loader2 className="size-5 animate-spin text-zinc-500" /></div> : enginesQuery.error ? <Card className="border-red-500/25 bg-red-500/[0.05]"><CardContent className="flex flex-col gap-3 p-5 text-xs text-red-300 sm:flex-row sm:items-center sm:justify-between"><span>{enginesQuery.error.message}</span><Button variant="outline" size="sm" disabled={enginesQuery.isFetching} onClick={() => enginesQuery.refetch()}><RefreshCw className={cn('size-3.5', enginesQuery.isFetching && 'animate-spin')} /> Retry inventory</Button></CardContent></Card> : !engine ? <Card><CardContent className="p-8 text-center text-sm text-zinc-500">No supported database engine detected.</CardContent></Card> : <>
      <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader className="flex-row items-start justify-between gap-3"><div><CardTitle className="text-sm text-zinc-200">{engine.name} inventory</CardTitle><p className="mt-1 font-mono text-[10px] text-zinc-600">{engine.unit}</p></div><div className="flex gap-2"><Button variant="outline" size="sm" onClick={onBackups}><Archive className="size-3.5" /> Backups</Button><Button variant="outline" size="sm" title={actionAvailable ? undefined : 'database.action is disabled'} disabled={!controlsReady || !online || !actionAvailable || restartMutation.isPending || engine.active !== 'active'} onClick={() => { if (window.confirm(`Restart ${engine.name} on remote server? Active connections may be interrupted.`)) restartMutation.mutate(engine) }}>{restartMutation.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <RotateCw className="size-3.5" />} Restart + health check</Button><Button variant="ghost" size="icon-xs" disabled={!online || enginesQuery.isFetching} onClick={() => enginesQuery.refetch()}><RefreshCw className={cn('size-3', enginesQuery.isFetching && 'animate-spin')} /></Button></div></CardHeader><CardContent className="p-0"><div className="overflow-x-auto"><table className="w-full min-w-[720px] text-xs"><thead><tr className="border-b border-zinc-800 text-zinc-500"><th className="px-4 py-2 text-left">Database</th><th className="px-3 py-2 text-right">Size</th><th className="px-3 py-2 text-right">Connections</th><th className="px-4 py-2 text-right">{engine.id === 'mariadb' ? 'Tables' : 'Objects'}</th></tr></thead><tbody>{engine.databases.map((database) => <tr key={database.name} className="border-b border-zinc-800/60 hover:bg-zinc-800/20"><td className="px-4 py-2.5 font-mono text-zinc-200">{database.name}</td><td className="px-3 py-2.5 text-right text-violet-400">{formatBytes(database.size)}</td><td className="px-3 py-2.5 text-right text-blue-400">{database.connections}</td><td className="px-4 py-2.5 text-right text-zinc-500">{database.objects || '—'}</td></tr>)}</tbody></table></div></CardContent></Card>
      <Card className="border-zinc-800 bg-zinc-900/80"><CardHeader><CardTitle className="flex items-center gap-2 text-sm text-zinc-200"><Activity className="size-4" /> Live sessions</CardTitle><p className="mt-1 text-[10px] text-zinc-500">Read-only activity inventory; session termination remains available through Processes or Terminal</p></CardHeader><CardContent className="p-0">{engine.sessions.length === 0 ? <p className="p-8 text-center text-xs text-zinc-600">No other sessions.</p> : <div className="overflow-x-auto"><table className="w-full min-w-[860px] text-xs"><thead><tr className="border-b border-zinc-800 text-zinc-500"><th className="px-4 py-2 text-left">ID</th><th className="px-3 py-2 text-left">User / Database</th><th className="px-3 py-2 text-left">State</th><th className="px-3 py-2 text-right">Age</th><th className="px-4 py-2 text-left">Query</th></tr></thead><tbody>{engine.sessions.map((session) => <tr key={session.id} className="border-b border-zinc-800/60"><td className="px-4 py-2.5 font-mono text-zinc-400">{session.id}</td><td className="px-3 py-2.5 text-zinc-300">{session.user}<span className="block font-mono text-[9px] text-zinc-600">{session.database || '—'}</span></td><td className="px-3 py-2.5 text-zinc-400">{session.state}</td><td className="px-3 py-2.5 text-right text-zinc-500">{formatAge(session.age_seconds)}</td><td className="max-w-[420px] truncate px-4 py-2.5 font-mono text-[10px] text-zinc-600" title={session.query}>{session.query || '—'}</td></tr>)}</tbody></table></div>}</CardContent></Card>
    </>}
  </div>
}

function DatabaseUnavailable({ title, detail }: { title: string; detail: string }) { return <Card className="border-amber-500/30 bg-amber-500/[0.07]"><CardContent className="p-5 text-xs text-amber-200"><p className="font-semibold">{title}</p><p className="mt-1 text-amber-200/80">{detail}</p></CardContent></Card> }
